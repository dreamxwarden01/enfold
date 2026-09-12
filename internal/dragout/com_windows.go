//go:build windows

package dragout

import (
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A pure-Go COM object, cgo-free.
//
// The shape is: every object COM can see is a small pointer-free []uintptr
// — one cell per interface, each cell holding that interface's vtable
// address — pinned with runtime.Pinner and registered in comRegistry by the
// address of each cell. A callback arriving from COM gets its `this` as a
// plain uintptr, never dereferences it, and looks the Go object up by that
// address instead. So the Go implementation behind an interface is never
// named by a pointer COM holds: what COM holds is an address into a pinned,
// pointer-free block, the collector has nothing to trace through it, and
// the implementation stays reachable for exactly as long as the reference
// count says it must.
//
// The other callback parameters are a different matter, and the rule above
// does not extend to them: riid *windows.GUID, ppv *uintptr, pmedium
// *stgMedium and rgelt *formatEtc are real Go pointers to memory COM owns,
// because that is how the ABI passes them and there is no way to receive
// them otherwise. Dereferencing those is safe for a reason of its own: their
// addresses lie outside the Go heap, and the runtime leaves an address it
// does not own alone — the collector does not trace it, and the stack
// copier does not rewrite it. Every Go COM binding rests on the same fact.
//
// The vtables themselves are Go structs of uintptr fields, declared in the
// same order the SDK's *Vtbl structs declare them, then copied into pinned
// pointer-free memory. Declaring them as named structs is what lets a unit
// test check the method order without calling anything.

// comImpl is the Go side of a COM object.
type comImpl interface {
	// comDestroy runs exactly once, when the last reference goes.
	comDestroy()
}

// comSelfAware is implemented by an object that has to know the comObject
// it lives in — the data object does, to answer what its own identity is.
// newCOMObject hands the object over before the interface cells are
// published, so that a callback arriving on the first instant a cell is
// visible already finds it set.
type comSelfAware interface {
	setCOMObject(o *comObject)
}

// comIface is one interface an object exposes: the vtable COM will see and
// the IIDs QueryInterface answers with this interface's own cell address.
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
	log    func(string, ...any)

	// The agility half, all of it described in agile_windows.go. Both
	// fields are written before the object's address has left the thread
	// that created it and read from every thread afterwards; atomics say
	// so, and make the race detector agree.
	agile   atomic.Bool
	marshal atomic.Pointer[*uintptr]
}

type comEntry struct {
	obj  *comObject
	slot int
	// dead marks a cell whose object has been released. The entry stays in
	// the map for the life of the process; see release.
	dead bool
}

var comRegistry = struct {
	mu sync.RWMutex
	m  map[uintptr]comEntry
	// live counts the entries that are not tombstones, which is what the
	// leak check at the end of a drag is asking about.
	live int
}{m: make(map[uintptr]comEntry)}

// newCOMObject builds an object with one cell per interface and hands back
// a single reference, as COM requires of anything that creates an object.
// Interface 0 is the object's identity: QueryInterface(IID_IUnknown) always
// answers with its address, which is the rule a target uses to decide
// whether two pointers are the same object. With agile set the object
// aggregates the free-threaded marshaler and answers IID_IAgileObject, so
// that the target may call it on threads of its own — the data object and
// its enumerator do; IDropSource does not, since OLE calls it on the drag
// thread and nowhere else.
func newCOMObject(name string, impl comImpl, agile bool, log func(string, ...any), ifaces ...comIface) *comObject {
	if len(ifaces) == 0 {
		panic("dragout: COM object with no interfaces")
	}
	if log == nil {
		log = discard
	}
	o := &comObject{
		name:   name,
		block:  make([]uintptr, len(ifaces)),
		ifaces: ifaces,
		impl:   impl,
		log:    log,
	}
	for i, f := range ifaces {
		if f.vtbl == 0 {
			panic("dragout: nil vtable for " + f.name)
		}
		o.block[i] = f.vtbl
	}
	o.pin.Pin(&o.block[0])
	o.base = uintptr(unsafe.Pointer(&o.block[0]))
	o.refs.Store(1)

	// Before the cells go into the registry, not after: publishing them is
	// what makes the object callable, and an implementation that needs to
	// reference itself must already be able to when the first call arrives.
	if sa, ok := impl.(comSelfAware); ok {
		sa.setCOMObject(o)
	}

	comRegistry.mu.Lock()
	for i := range ifaces {
		comRegistry.m[o.slotAddr(i)] = comEntry{obj: o, slot: i}
	}
	comRegistry.live += len(ifaces)
	comRegistry.mu.Unlock()

	// After registration, because the marshaler is handed this object's
	// controlling IUnknown and that pointer has to resolve if anything
	// calls through it; still before the address leaves this thread.
	if agile {
		o.aggregateFreeThreadedMarshaler()
	}
	return o
}

func (o *comObject) slotAddr(i int) uintptr {
	return o.base + uintptr(i)*unsafe.Sizeof(uintptr(0))
}

// unknown is the pointer a caller passes to DoDragDrop and friends:
// interface 0.
func (o *comObject) unknown() uintptr { return o.base }

// ifaceAddr returns the cell address for the interface whose name matches,
// or 0.
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
		// line is a finding about the consumer, and the alternative is
		// silence.
		e.obj.log("drag: %s: call on a released interface pointer (slot %d); refused", e.obj.name, e.slot)
		return nil, 0, false
	}
	return e.obj, e.slot, ok
}

// liveComObjects returns every object that has not been released yet. It is
// the inventory the close at exit consults while the target is still
// holding things.
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
	return live
}

// comRegistrySize is what the tests assert against to prove that a
// released object leaves nothing behind. Tombstones do not count: they are
// not references anyone holds, they are addresses kept out of circulation.
func comRegistrySize() int {
	comRegistry.mu.RLock()
	defer comRegistry.mu.RUnlock()
	return comRegistry.live
}

func (o *comObject) addRef() int32 {
	return o.refs.Add(1)
}

// release gives one reference back. No object of this package holds a
// reference on itself any more: the one that did was the data object, for
// the reference SetAsyncMode took, and the asynchronous capability is gone
// (APP.md §3, ruled 2026-09-11) — so the count is the plain sum of what the
// target and we hold, and zero is the end.
func (o *comObject) release() int32 {
	n := o.refs.Add(-1)
	if n > 0 {
		return n
	}
	if n < 0 {
		// Over-release is a bug in the caller or in us; say so rather than
		// letting the object be torn down twice.
		o.log("drag: %s: over-release, refcount now %d", o.name, n)
		return n
	}
	// Tombstone rather than delete, and keep the pin: a freed block's
	// address can back a later allocation, and then a stale `this` from a
	// consumer that over-released would resolve — silently, and to a
	// different live object of possibly a different type. Keeping the cell
	// and its pin for the life of the process makes that address unusable by
	// anything else, which turns a misattributed call into the log line
	// comLookup prints. A few cells and a small Go object per drag is
	// nothing against what a stale pointer would cost.
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
	// Aggregation's other half: the inner marshaler's lifetime is bound to
	// this object's, and this is the only place its reference is given
	// back. Outside the registry lock, because it is a call out of this
	// package.
	o.releaseMarshaler()
	if o.impl != nil {
		o.impl.comDestroy()
	}
	return 0
}

// ---------------------------------------------------------------------------
// IUnknown, shared by every interface of every object.
//
// One set of callbacks serves them all: `this` identifies not just the
// object but which of its interfaces was called, through the registry, so
// there is nothing per-interface to do.

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
		return eUnexpected
	}
	want := *riid
	// IID_IUnknown must always come back as the same pointer, whichever
	// interface it was asked through: that identity is how a target decides
	// two pointers are one object.
	if want == iidIUnknown {
		*ppv = o.slotAddr(0)
		o.addRef()
		return sOK
	}
	// The two IIDs an agile object answers that are not in any cell's list.
	if o.agile.Load() {
		switch want {
		case iidIAgileObject:
			// "The IAgileObject interface is a marker interface that
			// indicates that an object is free threaded and can be called
			// from any apartment." It inherits from IUnknown and adds
			// nothing, so the identity cell is a complete IAgileObject: the
			// answer is the claim.
			*ppv = o.slotAddr(0)
			o.addRef()
			return sOK
		case iidIMarshal:
			// Delegated, and not AddRef'd here: the inner marshaler's
			// QueryInterface has already taken the reference on this
			// object's count, which is what aggregation means.
			return o.marshalQueryInterface(riid, ppv)
		}
	}
	for i, f := range o.ifaces {
		for _, iid := range f.iids {
			if iid == want {
				*ppv = o.slotAddr(i)
				o.addRef()
				return sOK
			}
		}
	}
	return eNoInterface
}

func comAddRef(this uintptr) uintptr {
	o, _, ok := comLookup(this)
	if !ok {
		return 0
	}
	return uintptr(uint32(o.addRef()))
}

func comRelease(this uintptr) uintptr {
	o, _, ok := comLookup(this)
	if !ok {
		return 0
	}
	n := o.release()
	if n < 0 {
		// Release returns a ULONG. An over-release has already been logged
		// as the bug it is; do not hand the caller four billion as the new
		// count.
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

// vtblPins keeps the process-lifetime vtables pinned and reachable. They
// are never freed: COM may hold a vtable address for as long as the process
// runs.
var vtblPins struct {
	mu   sync.Mutex
	pin  runtime.Pinner
	keep [][]uintptr
}

// pinVtbl copies a vtable struct into pinned, pointer-free memory and
// returns its address. The struct is declared in Go, field for field in the
// order the SDK's *Vtbl declares the methods, because that declaration is
// the ABI; the copy is only what COM dereferences.
func pinVtbl(v any) uintptr {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Struct {
		panic("dragout: pinVtbl wants a struct")
	}
	n := rv.NumField()
	slots := make([]uintptr, n)
	for i := 0; i < n; i++ {
		f := rv.Field(i)
		if f.Kind() != reflect.Uintptr {
			panic(fmt.Sprintf("dragout: vtable field %s is %s, want uintptr",
				rv.Type().Field(i).Name, f.Kind()))
		}
		p := uintptr(f.Uint())
		if p == 0 {
			// A zero slot is a callback that was never built. COM would
			// call through it and take the process down somewhere
			// unrelated.
			panic(fmt.Sprintf("dragout: vtable %s slot %d (%s) is nil",
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

// iEnumFORMATETCVtbl is IEnumFORMATETCVtbl from objidl.h, in declaration
// order.
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

// ---------------------------------------------------------------------------
// IIDs, byte for byte as printed from the SDK's uuid library.

var (
	iidIUnknown       = windows.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIDataObject    = windows.GUID{Data1: 0x0000010E, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIEnumFORMATETC = windows.GUID{Data1: 0x00000103, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIDropSource    = windows.GUID{Data1: 0x00000121, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIMarshal       = windows.GUID{Data1: 0x00000003, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIAgileObject   = windows.GUID{Data1: 0x94EA2B94, Data2: 0xE9CC, Data3: 0x49E0, Data4: [8]byte{0xC0, 0xFF, 0xEE, 0x64, 0xCA, 0x8F, 0x5B, 0x90}}

	// IDataObjectAsyncCapability is the interface this data object
	// deliberately does NOT implement (APP.md §3, ruled 2026-09-11): without
	// it the target must finish the drop inside Drop, and DoDragDrop's
	// return is the end of the drag. The IID is kept because it is the one
	// a QueryInterface has to be refused for — and it answers to the old
	// name too: shldisp.idl says the interface "used to be named
	// IAsyncOperation ... It remains binary compatible with the old
	// definition", with the uuid unchanged.
	iidIDataObjectAsyncCapability = windows.GUID{Data1: 0x3D8B0590, Data2: 0xF691, Data3: 0x11D2, Data4: [8]byte{0x8E, 0xA9, 0x00, 0x60, 0x97, 0xDF, 0x5B, 0xD4}}
)
