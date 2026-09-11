//go:build windows

package main

import (
	"fmt"
	"reflect"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A pure-Go COM object, cgo-free.
//
// The shape is: every object COM can see is a small pointer-free []uintptr --
// one cell per interface, each cell holding that interface's vtable address --
// pinned with runtime.Pinner and registered in comRegistry by the address of
// each cell. A callback arriving from COM gets its `this` as a plain uintptr,
// never dereferences it, and looks the Go object up by that address instead.
// So the Go implementation behind an interface is never named by a pointer COM
// holds: what COM holds is an address into a pinned, pointer-free block, the
// collector has nothing to trace through it, and the implementation stays
// reachable for exactly as long as the reference count says it must.
//
// The other callback parameters are a different matter, and the rule above does
// not extend to them: riid *windows.GUID, ppv *uintptr, pmedium *stgMedium,
// rgelt *formatEtc, pv *byte, pstatstg *statStg and pstm **uintptr are real Go
// pointers to memory COM owns, because that is how the ABI passes them and
// there is no way to receive them otherwise. Dereferencing those is safe for a
// reason of its own rather than by this design: their addresses lie outside the
// Go heap, and the runtime leaves an address it does not own alone -- the
// collector does not trace it, and the stack copier does not rewrite it. Every
// Go COM binding rests on the same fact.
//
// The vtables themselves are Go structs of uintptr fields, declared in the same
// order the SDK's *Vtbl structs declare them, then copied into pinned
// pointer-free memory. Declaring them as named structs is what lets a unit test
// check the method order without calling anything.

// comImpl is the Go side of a COM object.
type comImpl interface {
	// comDestroy runs exactly once, when the last reference goes.
	comDestroy()
}

// comSelfAware is implemented by an object that has to hold a reference to
// itself -- the data object does, because SetAsyncMode is documented to AddRef
// the object and give the reference back in EndOperation. newCOMObject hands
// the object over before the interface cells are published, so that a callback
// arriving on the first instant a cell is visible already finds it set.
type comSelfAware interface {
	setCOMObject(o *comObject)
}

// comIface is one interface an object exposes: the vtable COM will see and the
// IIDs QueryInterface answers with this interface's own cell address.
type comIface struct {
	name string
	vtbl uintptr
	iids []windows.GUID
}

type comObject struct {
	name   string
	block  []uintptr // one cell per interface; COM sees &block[i]
	pin    runtime.Pinner
	base   uintptr
	ifaces []comIface
	refs   atomic.Int32
	impl   comImpl
	// seq is the order the object was created in, so that a list of live
	// objects can be printed in the order a reader of the log met them.
	seq int64

	// The agility half, all of it described in agile_windows.go. Both fields are
	// written before the object's address has left the thread that created it
	// and read from every thread afterwards; atomics say so, and make the race
	// detector agree.
	agile   atomic.Bool
	marshal atomic.Pointer[*uintptr]

	// Per-thread call accounting. counted in comLookup, printed at the end of
	// the object's life: with -agile this is the number that says whether the
	// reads moved off the STA.
	callsHome atomic.Int64
	callsAway atomic.Int64
	tidMu     sync.Mutex
	tids      []threadTally
}

// comSeq numbers objects in creation order.
var comSeq atomic.Int64

type comEntry struct {
	obj  *comObject
	slot int
	// dead marks a cell whose object has been released. The entry stays in the
	// map for the life of the process; see release.
	dead bool
}

var comRegistry = struct {
	mu sync.RWMutex
	m  map[uintptr]comEntry
	// live counts the entries that are not tombstones, which is what the leak
	// check at the end of a drag is asking about.
	live int
}{m: make(map[uintptr]comEntry)}

// newCOMObject builds an object with one cell per interface and hands back a
// single reference, as COM requires of anything that creates an object.
// Interface 0 is the object's identity: QueryInterface(IID_IUnknown) always
// answers with its address, which is the rule a target uses to decide whether
// two pointers are the same object.
func newCOMObject(name string, impl comImpl, ifaces ...comIface) *comObject {
	return newCOMObjectWith(name, impl, false, ifaces)
}

// newTransferCOMObject builds an object the target is going to be handed and
// may want to call from an apartment of its own: the data object, its format
// enumerator and every IStream. With -agile those aggregate the free-threaded
// marshaler and answer IID_IAgileObject; without it they are ordinary
// apartment-bound objects, which is the baseline the experiment measures
// against. IDropSource is not built this way on purpose -- OLE calls it on the
// drag thread and nowhere else.
func newTransferCOMObject(name string, impl comImpl, ifaces ...comIface) *comObject {
	return newCOMObjectWith(name, impl, agileMode.Load(), ifaces)
}

func newCOMObjectWith(name string, impl comImpl, agile bool, ifaces []comIface) *comObject {
	if len(ifaces) == 0 {
		panic("dragproto: COM object with no interfaces")
	}
	o := &comObject{
		name:   name,
		block:  make([]uintptr, len(ifaces)),
		ifaces: ifaces,
		impl:   impl,
		seq:    comSeq.Add(1),
	}
	for i, f := range ifaces {
		if f.vtbl == 0 {
			panic("dragproto: nil vtable for " + f.name)
		}
		o.block[i] = f.vtbl
	}
	o.pin.Pin(&o.block[0])
	o.base = uintptr(unsafe.Pointer(&o.block[0]))
	o.refs.Store(1)

	// Before the cells go into the registry, not after: publishing them is what
	// makes the object callable, and an implementation that needs to reference
	// itself must already be able to when the first call arrives.
	if sa, ok := impl.(comSelfAware); ok {
		sa.setCOMObject(o)
	}

	comRegistry.mu.Lock()
	for i := range ifaces {
		comRegistry.m[o.slotAddr(i)] = comEntry{obj: o, slot: i}
	}
	comRegistry.live += len(ifaces)
	n := comRegistry.live
	comRegistry.mu.Unlock()

	// After registration, because the marshaler is handed this object's
	// controlling IUnknown and that pointer has to resolve if anything calls
	// through it; still before the address leaves this thread.
	if agile {
		o.aggregateFreeThreadedMarshaler()
	}

	logf("new %s at 0x%X (%d interfaces, refs=1, registry=%d cells, %s)",
		name, o.base, len(ifaces), n, agileWord(o))
	return o
}

// agileWord names an object's apartment behaviour for the log, and says so at
// creation rather than only at death: a run where -agile was asked for but
// CoCreateFreeThreadedMarshaler refused would otherwise look like a run where
// agility did not help.
func agileWord(o *comObject) string {
	if o.agile.Load() {
		return "agile: aggregates the free-threaded marshaler"
	}
	return "apartment-bound"
}

func (o *comObject) slotAddr(i int) uintptr {
	return o.base + uintptr(i)*unsafe.Sizeof(uintptr(0))
}

// unknown is the pointer a caller passes to DoDragDrop and friends: interface 0.
func (o *comObject) unknown() uintptr { return o.base }

// ifaceAddr returns the cell address for the interface whose name matches, or 0.
func (o *comObject) ifaceAddr(name string) uintptr {
	for i, f := range o.ifaces {
		if f.name == name {
			return o.slotAddr(i)
		}
	}
	return 0
}

func comLookup(this uintptr) (*comObject, int, bool) {
	comRegistry.mu.RLock()
	e, ok := comRegistry.m[this]
	comRegistry.mu.RUnlock()
	if ok && e.dead {
		// A call on a tombstone is a consumer using a pointer it has already
		// released. Naming it is the whole reason the tombstone is kept: this
		// line is a finding about the consumer, and the alternative is silence.
		logf("%s: CALL ON A RELEASED this=0x%X (slot %d); refusing", e.obj.name, this, e.slot)
		return nil, 0, false
	}
	if ok {
		// Every callback that resolves an interface pointer comes through here
		// exactly once, which makes this the one place that can count calls
		// without a line per call in each of them. The four IDataObject methods
		// that could answer without a lookup do one anyway, so that the totals
		// are every call and not most of them.
		e.obj.tally()
	}
	return e.obj, e.slot, ok
}

// liveComObjects returns every object that has not been released yet, in
// creation order. It is the leak list at exit, and the inventory a close prints
// while the target is still holding things.
func liveComObjects() []*comObject {
	comRegistry.mu.RLock()
	seen := make(map[*comObject]bool)
	live := make([]*comObject, 0, 8)
	for _, e := range comRegistry.m {
		if e.dead || seen[e.obj] {
			continue
		}
		seen[e.obj] = true
		live = append(live, e.obj)
	}
	comRegistry.mu.RUnlock()
	sort.Slice(live, func(i, j int) bool { return live[i].seq < live[j].seq })
	return live
}

// comRegistrySize is what the tests assert against to prove that a released
// object leaves nothing behind. Tombstones do not count: they are not
// references anyone holds, they are addresses kept out of circulation.
func comRegistrySize() int {
	comRegistry.mu.RLock()
	defer comRegistry.mu.RUnlock()
	return comRegistry.live
}

func (o *comObject) addRef() int32 {
	return o.refs.Add(1)
}

func (o *comObject) release() int32 {
	n := o.refs.Add(-1)
	if n > 0 {
		return n
	}
	if n < 0 {
		// Over-release is a bug in the caller or in us; say so rather than
		// letting the object be torn down twice.
		logf("%s: OVER-RELEASE, refcount now %d", o.name, n)
		return n
	}
	// Tombstone rather than delete, and keep the pin: a freed block's address
	// can back a later allocation, and then a stale `this` from a consumer that
	// over-released would resolve -- silently, and to a different live object of
	// possibly a different type. Keeping the cell and its pin for the life of
	// the process makes that address unusable by anything else, which turns a
	// misattributed call into the log line comLookup prints. A few cells and a
	// small Go object per drag is nothing against a prototype whose only product
	// is a truthful log; the shell will want a real free, and will have to solve
	// stale pointers some other way.
	comRegistry.mu.Lock()
	for i := range o.ifaces {
		a := o.slotAddr(i)
		if e := comRegistry.m[a]; !e.dead {
			e.dead = true
			comRegistry.m[a] = e
			comRegistry.live--
		}
	}
	comRegistry.mu.Unlock()
	// Aggregation's other half: the inner marshaler's lifetime is bound to this
	// object's, and this is the only place its reference is given back. Outside
	// the registry lock, because it is a call out of this package.
	o.releaseMarshaler()
	logf("%s: %s", o.name, o.threadSummary())
	if o.impl != nil {
		o.impl.comDestroy()
	}
	return 0
}

// ---------------------------------------------------------------------------
// IUnknown, shared by every interface of every object.
//
// One set of callbacks serves them all: `this` identifies not just the object
// but which of its interfaces was called, through the registry, so there is
// nothing per-interface to do.

func comQueryInterface(this uintptr, riid *windows.GUID, ppv *uintptr) uintptr {
	if ppv == nil {
		return ePointer
	}
	*ppv = 0
	if riid == nil {
		return ePointer
	}
	o, _, ok := comLookup(this)
	if !ok {
		logf("QueryInterface on unknown this=0x%X", this)
		return eUnexpected
	}
	want := *riid
	// IID_IUnknown must always come back as the same pointer, whichever
	// interface it was asked through: that identity is how a target decides two
	// pointers are one object.
	if want == iidIUnknown {
		*ppv = o.slotAddr(0)
		n := o.addRef()
		logf("%s::QueryInterface(IID_IUnknown) -> 0x%X, refs=%d", o.name, *ppv, n)
		return sOK
	}
	// The two IIDs an agile object answers that are not in any cell's list.
	if o.agile.Load() {
		switch want {
		case iidIAgileObject:
			// "The IAgileObject interface is a marker interface that indicates
			// that an object is free threaded and can be called from any
			// apartment." It inherits from IUnknown and adds nothing, so the
			// identity cell is a complete IAgileObject: the answer is the claim.
			*ppv = o.slotAddr(0)
			n := o.addRef()
			logf("%s::QueryInterface(IID_IAgileObject) -> 0x%X, refs=%d (this object may be called from any apartment)",
				o.name, *ppv, n)
			return sOK
		case iidIMarshal:
			// Delegated, and not AddRef'd here: the inner marshaler's
			// QueryInterface has already taken the reference on this object's
			// count, which is what aggregation means.
			hr := o.marshalQueryInterface(riid, ppv)
			logf("%s::QueryInterface(IID_IMarshal) -> delegated to the free-threaded marshaler: %s, ppv=0x%X, refs=%d",
				o.name, hrName(hr), *ppv, o.refs.Load())
			return hr
		}
	}
	for i, f := range o.ifaces {
		for _, iid := range f.iids {
			if iid == want {
				*ppv = o.slotAddr(i)
				n := o.addRef()
				logf("%s::QueryInterface(%s) -> 0x%X, refs=%d", o.name, iidName(want), *ppv, n)
				return sOK
			}
		}
	}
	logf("%s::QueryInterface(%s) -> E_NOINTERFACE", o.name, iidName(want))
	return eNoInterface
}

func comAddRef(this uintptr) uintptr {
	o, _, ok := comLookup(this)
	if !ok {
		logf("AddRef on unknown this=0x%X", this)
		return 0
	}
	n := o.addRef()
	logf("%s::AddRef -> %d", o.name, n)
	return uintptr(uint32(n))
}

func comRelease(this uintptr) uintptr {
	o, _, ok := comLookup(this)
	if !ok {
		logf("Release on unknown this=0x%X", this)
		return 0
	}
	name := o.name
	n := o.release()
	if n == 0 {
		logf("%s::Release -> 0 (destroyed)", name)
	} else {
		logf("%s::Release -> %d", name, n)
	}
	if n < 0 {
		// Release returns a ULONG. An over-release has already been logged as
		// the bug it is; do not hand the caller four billion as the new count.
		return 0
	}
	return uintptr(uint32(n))
}

var (
	cbQueryInterface = syscall.NewCallback(comQueryInterface)
	cbAddRef         = syscall.NewCallback(comAddRef)
	cbRelease        = syscall.NewCallback(comRelease)
)

// ---------------------------------------------------------------------------
// Vtables.

// vtblPins keeps the process-lifetime vtables pinned and reachable. They are
// never freed: COM may hold a vtable address for as long as the process runs.
var vtblPins struct {
	mu   sync.Mutex
	pin  runtime.Pinner
	keep [][]uintptr
}

// pinVtbl copies a vtable struct into pinned, pointer-free memory and returns
// its address. The struct is declared in Go, field for field in the order the
// SDK's *Vtbl declares the methods, because that declaration is the ABI; the
// copy is only what COM dereferences.
func pinVtbl(v any) uintptr {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Struct {
		panic("dragproto: pinVtbl wants a struct")
	}
	n := rv.NumField()
	slots := make([]uintptr, n)
	for i := 0; i < n; i++ {
		f := rv.Field(i)
		if f.Kind() != reflect.Uintptr {
			panic(fmt.Sprintf("dragproto: vtable field %s is %s, want uintptr",
				rv.Type().Field(i).Name, f.Kind()))
		}
		p := uintptr(f.Uint())
		if p == 0 {
			// A zero slot is a callback that was never built. COM would call
			// through it and take the process down somewhere unrelated.
			panic(fmt.Sprintf("dragproto: vtable %s slot %d (%s) is nil",
				rv.Type().Name(), i, rv.Type().Field(i).Name))
		}
		slots[i] = p
	}
	vtblPins.mu.Lock()
	vtblPins.pin.Pin(&slots[0])
	vtblPins.keep = append(vtblPins.keep, slots)
	vtblPins.mu.Unlock()
	return uintptr(unsafe.Pointer(&slots[0]))
}

type iUnknownVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
}

// iDataObjectVtbl is IDataObjectVtbl from objidl.h, in declaration order.
type iDataObjectVtbl struct {
	QueryInterface        uintptr
	AddRef                uintptr
	Release               uintptr
	GetData               uintptr
	GetDataHere           uintptr
	QueryGetData          uintptr
	GetCanonicalFormatEtc uintptr
	SetData               uintptr
	EnumFormatEtc         uintptr
	DAdvise               uintptr
	DUnadvise             uintptr
	EnumDAdvise           uintptr
}

// iDataObjectAsyncCapabilityVtbl is IDataObjectAsyncCapabilityVtbl from
// ShlDisp.h, in declaration order.
type iDataObjectAsyncCapabilityVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	SetAsyncMode   uintptr
	GetAsyncMode   uintptr
	StartOperation uintptr
	InOperation    uintptr
	EndOperation   uintptr
}

// iEnumFORMATETCVtbl is IEnumFORMATETCVtbl from objidl.h, in declaration order.
type iEnumFORMATETCVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	Next           uintptr
	Skip           uintptr
	Reset          uintptr
	Clone          uintptr
}

// iDropSourceVtbl is IDropSourceVtbl from oleidl.h, in declaration order.
type iDropSourceVtbl struct {
	QueryInterface    uintptr
	AddRef            uintptr
	Release           uintptr
	QueryContinueDrag uintptr
	GiveFeedback      uintptr
}

// iStreamVtbl is IStreamVtbl from objidl.h, in declaration order. Read and
// Write come from ISequentialStream, which IStream derives from, so they sit
// ahead of IStream's own methods.
type iStreamVtbl struct {
	QueryInterface uintptr
	AddRef         uintptr
	Release        uintptr
	Read           uintptr
	Write          uintptr
	Seek           uintptr
	SetSize        uintptr
	CopyTo         uintptr
	Commit         uintptr
	Revert         uintptr
	LockRegion     uintptr
	UnlockRegion   uintptr
	Stat           uintptr
	Clone          uintptr
}

// ---------------------------------------------------------------------------
// IIDs, byte for byte as printed from the SDK's uuid library.

var (
	iidIUnknown          = windows.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIDataObject       = windows.GUID{Data1: 0x0000010E, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIEnumFORMATETC    = windows.GUID{Data1: 0x00000103, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIDropSource       = windows.GUID{Data1: 0x00000121, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIStream           = windows.GUID{Data1: 0x0000000C, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidISequentialStream = windows.GUID{Data1: 0x0C733A30, Data2: 0x2A1C, Data3: 0x11CE, Data4: [8]byte{0xAD, 0xE5, 0x00, 0xAA, 0x00, 0x44, 0x77, 0x3D}}
	iidIMarshal          = windows.GUID{Data1: 0x00000003, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIAgileObject      = windows.GUID{Data1: 0x94EA2B94, Data2: 0xE9CC, Data3: 0x49E0, Data4: [8]byte{0xC0, 0xFF, 0xEE, 0x64, 0xCA, 0x8F, 0x5B, 0x90}}

	// IDataObjectAsyncCapability and the old IAsyncOperation are the same IID:
	// shldisp.idl says the interface "used to be named IAsyncOperation ... It
	// remains binary compatible with the old definition", and the uuid on the
	// interface is unchanged. So answering this one GUID answers both names,
	// and a target compiled against either header finds us.
	iidIDataObjectAsyncCapability = windows.GUID{Data1: 0x3D8B0590, Data2: 0xF691, Data3: 0x11D2, Data4: [8]byte{0x8E, 0xA9, 0x00, 0x60, 0x97, 0xDF, 0x5B, 0xD4}}
)

// iidName resolves an IID for the log. Anything not in the table prints as its
// GUID: guessing at a name would be worse than none.
func iidName(g windows.GUID) string {
	switch g {
	case iidIUnknown:
		return "IID_IUnknown"
	case iidIDataObject:
		return "IID_IDataObject"
	case iidIEnumFORMATETC:
		return "IID_IEnumFORMATETC"
	case iidIDropSource:
		return "IID_IDropSource"
	case iidIStream:
		return "IID_IStream"
	case iidISequentialStream:
		return "IID_ISequentialStream"
	case iidIMarshal:
		return "IID_IMarshal"
	case iidIAgileObject:
		return "IID_IAgileObject"
	case iidIDataObjectAsyncCapability:
		return "IID_IDataObjectAsyncCapability/IID_IAsyncOperation"
	}
	return guidString(g)
}

func guidString(g windows.GUID) string {
	return fmt.Sprintf("{%08X-%04X-%04X-%02X%02X-%02X%02X%02X%02X%02X%02X}",
		g.Data1, g.Data2, g.Data3,
		g.Data4[0], g.Data4[1], g.Data4[2], g.Data4[3],
		g.Data4[4], g.Data4[5], g.Data4[6], g.Data4[7])
}
