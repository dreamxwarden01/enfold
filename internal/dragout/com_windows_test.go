//go:build windows

package dragout

import (
	"encoding/binary"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The COM half. Nothing here opens a window, starts a drag or talks to the
// shell: the callbacks are ordinary Go functions invoked directly with
// `this` set to a real interface-cell address, and the one call that
// leaves the process goes to ole32's free-threaded marshaler and no
// further.

type fakeImpl struct {
	destroyed int
}

func (f *fakeImpl) comDestroy() { f.destroyed++ }

// fakeVtbl is never dereferenced by these tests; it only has to be
// non-zero, because a COM object with a null vtable pointer is a crash
// waiting to happen and newCOMObject refuses one.
const fakeVtbl = uintptr(0x1000)

var (
	testIIDA = windows.GUID{Data1: 0xAAAA0001, Data4: [8]byte{1}}
	testIIDB = windows.GUID{Data1: 0xBBBB0002, Data4: [8]byte{2}}
	testIIDC = windows.GUID{Data1: 0xCCCC0003, Data4: [8]byte{3}}
)

func newTestObject(impl comImpl, log func(string, ...any)) *comObject {
	return newCOMObject("TestObject", impl, false, log,
		comIface{name: "A", vtbl: fakeVtbl, iids: []windows.GUID{testIIDA}},
		comIface{name: "B", vtbl: fakeVtbl + 8, iids: []windows.GUID{testIIDB}},
	)
}

// The reference count and the registry are one mechanism: the registry is
// what keeps a Go object reachable while COM holds a pointer into its block,
// and the count is what says when to let go. Getting it wrong does not fail
// loudly — it either leaks the archive handle a real drag would be holding
// open, or frees it while Explorer is still reading. These tests pin both
// ends.
func TestRegistryLifetime(t *testing.T) {
	before := comRegistrySize()
	impl := &fakeImpl{}
	o := newTestObject(impl, nil)

	if got := comRegistrySize(); got != before+2 {
		t.Fatalf("registry holds %d cells, want %d (one per interface)", got, before+2)
	}
	if o.refs.Load() != 1 {
		t.Fatalf("a new object has %d references, want 1", o.refs.Load())
	}
	// Every cell resolves to the same object, and to its own slot.
	for i := 0; i < 2; i++ {
		got, slot, ok := comLookup(o.slotAddr(i))
		if !ok || got != o || slot != i {
			t.Fatalf("cell %d resolved to %v/%d/%v", i, got, slot, ok)
		}
	}
	if o.slotAddr(1)-o.slotAddr(0) != unsafe.Sizeof(uintptr(0)) {
		t.Fatalf("interface cells are not adjacent")
	}
	if o.block[0] != fakeVtbl || o.block[1] != fakeVtbl+8 {
		t.Fatalf("cells do not hold their vtable addresses: %v", o.block)
	}
	if n := o.addRef(); n != 2 {
		t.Fatalf("addRef gave %d, want 2", n)
	}
	if n := o.release(); n != 1 {
		t.Fatalf("release gave %d, want 1", n)
	}
	if impl.destroyed != 0 {
		t.Fatalf("destroyed at a non-zero reference count")
	}
	if n := o.release(); n != 0 {
		t.Fatalf("final release gave %d, want 0", n)
	}
	if impl.destroyed != 1 {
		t.Fatalf("comDestroy ran %d times, want exactly 1", impl.destroyed)
	}
	if got := comRegistrySize(); got != before {
		t.Fatalf("registry holds %d cells after release, want %d back at the start", got, before)
	}
	if _, _, ok := comLookup(o.slotAddr(0)); ok {
		t.Fatalf("a released object is still in the registry")
	}
}

// TestCallOnAReleasedThisIsRefusedAndLogged pins the tombstone. A consumer
// that releases once too often keeps a pointer to a cell that no longer
// belongs to anything; if that address were handed back to the allocator, a
// later object could be sitting on it and the call would land on the wrong
// implementation without a word. The cell is kept, and pinned, for the life
// of the process instead, so the only thing such a call can produce is a
// log line.
func TestCallOnAReleasedThisIsRefusedAndLogged(t *testing.T) {
	lg := &testLog{}
	impl := &fakeImpl{}
	o := newTestObject(impl, lg.printf)
	this := o.slotAddr(0)
	if n := o.release(); n != 0 {
		t.Fatalf("release gave %d, want 0", n)
	}
	ppv := uintptr(0xDEAD)
	if hr := comQueryInterface(this, &iidIUnknown, &ppv); hr != eUnexpected {
		t.Errorf("QueryInterface on a released this -> %s, want E_UNEXPECTED", hrName(hr))
	}
	if ppv != 0 {
		t.Errorf("QueryInterface on a released this left *ppv = 0x%X", ppv)
	}
	if got := comAddRef(this); got != 0 {
		t.Errorf("AddRef on a released this -> %d, want 0", got)
	}
	if got := comRelease(this); got != 0 {
		t.Errorf("Release on a released this -> %d, want 0", got)
	}
	if n := o.refs.Load(); n != 0 {
		t.Errorf("calls on a released this moved the reference count to %d", n)
	}
	if impl.destroyed != 1 {
		t.Errorf("comDestroy ran %d times, want exactly 1", impl.destroyed)
	}
	if got := strings.Count(lg.text(), "call on a released interface pointer"); got != 3 {
		t.Errorf("the log names %d calls on a released this, want 3:\n%s", got, lg.text())
	}
	comRegistry.mu.RLock()
	e, ok := comRegistry.m[this]
	comRegistry.mu.RUnlock()
	if !ok || !e.dead {
		t.Fatalf("the released cell is not kept as a tombstone (%v, dead=%v)", ok, e.dead)
	}
}

func TestQueryInterfaceIdentityAndRefcounts(t *testing.T) {
	o := newTestObject(&fakeImpl{}, nil)
	defer func() {
		for o.refs.Load() > 0 {
			o.release()
		}
	}()
	var ppv uintptr
	// IID_IUnknown must come back as the same pointer from every interface.
	for i := 0; i < 2; i++ {
		ppv = 0
		if hr := comQueryInterface(o.slotAddr(i), &iidIUnknown, &ppv); hr != sOK {
			t.Fatalf("QI(IUnknown) through cell %d -> %s", i, hrName(hr))
		}
		if ppv != o.slotAddr(0) {
			t.Fatalf("QI(IUnknown) through cell %d gave 0x%X, want the identity cell 0x%X", i, ppv, o.slotAddr(0))
		}
		o.release()
	}
	for _, tc := range []struct {
		iid  windows.GUID
		want uintptr
	}{{testIIDA, o.slotAddr(0)}, {testIIDB, o.slotAddr(1)}} {
		for i := 0; i < 2; i++ {
			ppv = 0
			before := o.refs.Load()
			if hr := comQueryInterface(o.slotAddr(i), &tc.iid, &ppv); hr != sOK {
				t.Fatalf("QI -> %s", hrName(hr))
			}
			if ppv != tc.want {
				t.Fatalf("QI gave 0x%X, want 0x%X", ppv, tc.want)
			}
			if o.refs.Load() != before+1 {
				t.Fatalf("QueryInterface did not AddRef")
			}
			o.release()
		}
	}
	// An IID the object does not have: E_NOINTERFACE, *ppv nulled, no AddRef.
	ppv = 0xDEAD
	before := o.refs.Load()
	if hr := comQueryInterface(o.slotAddr(0), &testIIDC, &ppv); hr != eNoInterface {
		t.Fatalf("QI(unknown IID) -> %s, want E_NOINTERFACE", hrName(hr))
	}
	if ppv != 0 || o.refs.Load() != before {
		t.Fatalf("a failed QueryInterface left *ppv = 0x%X, refs %d", ppv, o.refs.Load())
	}
	if hr := comQueryInterface(o.slotAddr(0), &testIIDA, nil); hr != ePointer {
		t.Fatalf("QI(ppv=nil) -> %s, want E_POINTER", hrName(hr))
	}
	// An apartment-bound object answers neither of the agile IIDs, which is
	// what makes COM marshal it back to the apartment that created it.
	for _, iid := range []windows.GUID{iidIMarshal, iidIAgileObject} {
		ppv = 0xDEAD
		if hr := comQueryInterface(o.unknown(), &iid, &ppv); hr != eNoInterface || ppv != 0 {
			t.Errorf("QI(agile IID) on an apartment-bound object -> %s, *ppv 0x%X", hrName(hr), ppv)
		}
	}
	// A `this` the registry has never seen must be refused, not followed.
	if hr := comQueryInterface(0xBADC0FFEE0, &iidIUnknown, &ppv); hr != eUnexpected {
		t.Fatalf("QI on an unknown this -> %s, want E_UNEXPECTED", hrName(hr))
	}
	if comAddRef(0xBADC0FFEE0) != 0 || comRelease(0xBADC0FFEE0) != 0 {
		t.Fatal("AddRef or Release on an unknown this answered a count")
	}
}

func TestAddRefReleaseCallbacksReportCounts(t *testing.T) {
	impl := &fakeImpl{}
	o := newTestObject(impl, nil)
	if got := comAddRef(o.slotAddr(1)); got != 2 {
		t.Fatalf("AddRef returned %d, want 2", got)
	}
	if got := comRelease(o.slotAddr(1)); got != 1 {
		t.Fatalf("Release returned %d, want 1", got)
	}
	if got := comRelease(o.slotAddr(0)); got != 0 {
		t.Fatalf("final Release returned %d, want 0", got)
	}
	if impl.destroyed != 1 {
		t.Fatalf("destroyed %d times", impl.destroyed)
	}
}

// ---------------------------------------------------------------------------
// The data object.

// newTestDataObject builds a real data object over a stage. Everything it
// touches outside this process is RegisterClipboardFormatW, which is
// process-local and asks nothing of the shell; no window, no OLE, no drag.
// The marshaler is the fake's, so that the object is agile without an
// apartment.
func newTestDataObject(t *testing.T, items []Item) (*comObject, *dataObject, *stage, uintptr) {
	t.Helper()
	installFakeMarshaler(t)
	s, _, _ := newTestStage(t, items, nil)
	o := newHDropDataObject(s, nil)
	d, ok := o.impl.(*dataObject)
	if !ok {
		t.Fatalf("newHDropDataObject built a %T", o.impl)
	}
	if d.self != o {
		t.Fatalf("the data object does not know its own comObject")
	}
	async := o.ifaceAddr("IDataObjectAsyncCapability")
	if async == 0 {
		t.Fatalf("the data object has no IDataObjectAsyncCapability cell")
	}
	return o, d, s, async
}

// readHGlobal copies a rendered medium out and frees it, as the receiver
// would.
func readHGlobal(t *testing.T, medium stgMedium) []byte {
	t.Helper()
	if medium.tymed != tymedHGlobal || medium.data == 0 {
		t.Fatalf("the medium is {tymed %d, data 0x%X}", medium.tymed, medium.data)
	}
	if medium.pUnkForRelease != 0 {
		t.Error("pUnkForRelease must be NULL: the receiver frees the medium")
	}
	n := globalSize(medium.data)
	blob := make([]byte, n)
	p := globalLock(medium.data)
	if p == 0 {
		t.Fatal("GlobalLock failed on the medium we just allocated")
	}
	copyFromNative(blob, p)
	globalUnlock(medium.data)
	globalFree(medium.data)
	return blob
}

func TestHDropDataObjectFormats(t *testing.T) {
	isolateStages(t)
	o, d, s, _ := newTestDataObject(t, []Item{{Name: "f.bin", Size: 128}})
	defer o.release()

	if len(d.formats) != 2 {
		t.Fatalf("the object offers %d formats, want 2", len(d.formats))
	}
	for _, f := range d.formats {
		if f.tymed != tymedHGlobal || f.dwAspect != dvAspectContent || f.lindex != -1 {
			t.Errorf("%s is offered as {aspect %d, lindex %d, tymed %d}, want {CONTENT, -1, TYMED_HGLOBAL}",
				formatName(f.cfFormat), f.dwAspect, f.lindex, f.tymed)
		}
	}
	if !d.offers(cfHDrop) || !d.offers(cfPreferredDropEffect) {
		t.Error("the object does not offer both CF_HDROP and CFSTR_PREFERREDDROPEFFECT")
	}
	// A GetData for a format never offered is DV_E_FORMATETC; the wrong
	// medium, aspect or index of one that is says which.
	var medium stgMedium
	fe := formatEtc{cfFormat: cfPerformedDropEffect, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
	if hr := dataGetData(o.slotAddr(0), &fe, &medium); hr != dvEFormatEtc {
		t.Fatalf("GetData of an unoffered format -> %s, want DV_E_FORMATETC", hrName(hr))
	}
	fe = formatEtc{cfFormat: cfHDrop, dwAspect: dvAspectContent, lindex: -1, tymed: 4}
	if hr := dataGetData(o.slotAddr(0), &fe, &medium); hr != dvETymed {
		t.Fatalf("GetData(CF_HDROP as a stream) -> %s, want DV_E_TYMED", hrName(hr))
	}
	fe = formatEtc{cfFormat: cfHDrop, dwAspect: 2, lindex: -1, tymed: tymedHGlobal}
	if hr := dataGetData(o.slotAddr(0), &fe, &medium); hr != dvEDvAspect {
		t.Fatalf("GetData(CF_HDROP, another aspect) -> %s, want DV_E_DVASPECT", hrName(hr))
	}
	fe = formatEtc{cfFormat: cfHDrop, dwAspect: dvAspectContent, lindex: 0, tymed: tymedHGlobal}
	if hr := dataGetData(o.slotAddr(0), &fe, &medium); hr != dvELIndex {
		t.Fatalf("GetData(CF_HDROP, lindex 0) -> %s, want DV_E_LINDEX", hrName(hr))
	}
	if hr := dataQueryGetData(o.slotAddr(0), &fe); hr != dvEFormatEtc {
		t.Fatalf("QueryGetData of a wrong FORMATETC -> %s, want DV_E_FORMATETC", hrName(hr))
	}

	// A hover request for CF_HDROP renders a real block naming the final
	// paths, and QueryGetData says yes.
	fe = formatEtc{cfFormat: cfHDrop, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal | 4}
	if hr := dataQueryGetData(o.slotAddr(0), &fe); hr != sOK {
		t.Fatalf("QueryGetData(CF_HDROP) -> %s", hrName(hr))
	}
	medium = stgMedium{}
	if hr := dataGetData(o.slotAddr(0), &fe, &medium); hr != sOK {
		t.Fatalf("GetData(CF_HDROP) returned %s", hrName(hr))
	}
	_, _, _, _, fWide, paths := decodeDropFiles(t, readHGlobal(t, medium))
	if fWide == 0 {
		t.Error("fWide is zero in a rendered CF_HDROP block")
	}
	if len(paths) != 1 || paths[0] != s.paths[0] {
		t.Fatalf("the rendered block names %q, want %q", paths, s.paths)
	}
	// The preferred effect is a DWORD, MOVE.
	fe = formatEtc{cfFormat: cfPreferredDropEffect, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
	medium = stgMedium{}
	if hr := dataGetData(o.slotAddr(0), &fe, &medium); hr != sOK {
		t.Fatalf("GetData(CFSTR_PREFERREDDROPEFFECT) returned %s", hrName(hr))
	}
	if v := binary.LittleEndian.Uint32(readHGlobal(t, medium)); v != dropEffectMove {
		t.Fatalf("CFSTR_PREFERREDDROPEFFECT = %s, want DROPEFFECT_MOVE", effectName(v))
	}
	// GetCanonicalFormatEtc: the same FORMATETC with a NULL ptd.
	in := formatEtc{cfFormat: cfHDrop, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal, ptd: 0x1234}
	var out formatEtc
	if hr := dataGetCanonicalFormatEtc(o.slotAddr(0), &in, &out); hr != dataSSameFormatEtc || out.ptd != 0 || out.cfFormat != cfHDrop {
		t.Fatalf("GetCanonicalFormatEtc -> %s, %+v", hrName(hr), out)
	}
	// The enumerator walks both formats and no more.
	var penum uintptr
	if hr := dataEnumFormatEtc(o.slotAddr(0), dataDirGet, &penum); hr != sOK || penum == 0 {
		t.Fatalf("EnumFormatEtc -> %s, 0x%X", hrName(hr), penum)
	}
	var got [3]formatEtc
	var fetched uint32
	if hr := enumNext(penum, 3, &got[0], &fetched); hr != sFALSE || fetched != 2 {
		t.Fatalf("Next(3) -> %s, %d fetched; want S_FALSE and 2", hrName(hr), fetched)
	}
	if hr := enumReset(penum); hr != sOK {
		t.Fatalf("Reset -> %s", hrName(hr))
	}
	if hr := enumSkip(penum, 1); hr != sOK {
		t.Fatalf("Skip(1) -> %s", hrName(hr))
	}
	var clone uintptr
	if hr := enumClone(penum, &clone); hr != sOK || clone == 0 {
		t.Fatalf("Clone -> %s", hrName(hr))
	}
	if hr := enumNext(clone, 1, &got[0], nil); hr != sOK || got[0].cfFormat != cfPreferredDropEffect {
		t.Fatalf("a clone did not keep its position: %s, %s", hrName(hr), formatName(got[0].cfFormat))
	}
	if hr := dataEnumFormatEtc(o.slotAddr(0), dataDirSet, &penum); hr != eNotImpl {
		t.Fatalf("EnumFormatEtc(DATADIR_SET) -> %s, want E_NOTIMPL", hrName(hr))
	}
	if hr := dataEnumFormatEtc(o.slotAddr(0), 7, &penum); hr != eInvalidArg {
		t.Fatalf("EnumFormatEtc(7) -> %s, want E_INVALIDARG", hrName(hr))
	}
	comRelease(clone)
	comRelease(penum)
	// The advisory methods decline the documented way.
	var conn uint32
	if hr := dataDAdvise(o.slotAddr(0), &fe, 0, 0, &conn); hr != oleEAdviseNotSupp {
		t.Fatalf("DAdvise -> %s", hrName(hr))
	}
	if hr := dataGetDataHere(o.slotAddr(0), &fe, &medium); hr != dvEFormatEtc {
		t.Fatalf("GetDataHere -> %s", hrName(hr))
	}
}

// TestAsyncSelfReferenceLedger walks every path through the
// IDataObjectAsyncCapability handshake and checks the one thing all of them
// share: the reference SetAsyncMode is documented to take is given back
// exactly once. Both ways of getting it wrong are silent: taken and never
// given back leaves the object alive forever, given back without having
// been taken frees it while Explorer is still reading from it.
func TestAsyncSelfReferenceLedger(t *testing.T) {
	isolateStages(t)
	setAsync := func(t *testing.T, async uintptr, v uintptr) {
		t.Helper()
		if hr := dataSetAsyncMode(async, v); hr != sOK {
			t.Fatalf("SetAsyncMode(0x%X) -> %s", v, hrName(hr))
		}
	}
	inOperation := func(t *testing.T, async uintptr) bool {
		t.Helper()
		var v uint32
		if hr := dataInOperation(async, &v); hr != sOK {
			t.Fatalf("InOperation -> %s", hrName(hr))
		}
		return v != 0
	}
	endOperation := func(t *testing.T, async uintptr) {
		t.Helper()
		if hr := dataEndOperation(async, sOK, 0, dropEffectCopy); hr != sOK {
			t.Fatalf("EndOperation -> %s", hrName(hr))
		}
	}
	refs := func(t *testing.T, o *comObject, want int32, when string) {
		t.Helper()
		if got := o.refs.Load(); got != want {
			t.Fatalf("%s: refcount %d, want %d", when, got, want)
		}
	}
	for _, tc := range []struct {
		name string
		run  func(t *testing.T, o *comObject, d *dataObject, s *stage, async uintptr)
	}{
		{"cancelled drag", func(t *testing.T, o *comObject, d *dataObject, s *stage, async uintptr) {
			setAsync(t, async, variantTrue)
			refs(t, o, 2, "after SetAsyncMode(TRUE)")
			if inOperation(t, async) {
				t.Fatal("InOperation is true with no StartOperation")
			}
			d.finishSync()
			refs(t, o, 1, "after finishSync")
			d.finishSync()
			refs(t, o, 1, "after a second finishSync")
		}},
		{"asynchronous drop", func(t *testing.T, o *comObject, d *dataObject, s *stage, async uintptr) {
			setAsync(t, async, variantTrue)
			var v uint32
			if hr := dataGetAsyncMode(async, &v); hr != sOK || v == 0 {
				t.Fatalf("GetAsyncMode -> %s, %d", hrName(hr), v)
			}
			// The target takes a reference of its own in Drop, as it must
			// to have anything to call EndOperation on later.
			comAddRef(async)
			if hr := dataStartOperation(async, 0); hr != sOK {
				t.Fatalf("StartOperation -> %s", hrName(hr))
			}
			if !inOperation(t, async) || !d.inOperation() {
				t.Fatal("InOperation is false after StartOperation")
			}
			s.mu.Lock()
			asyncOp := s.asyncOp
			s.mu.Unlock()
			if !asyncOp {
				t.Fatal("the stage was not told the protocol was negotiated")
			}
			// Step 4 of the source procedure: release the data object. The
			// object must survive it on the target's and the negotiation's
			// references.
			if n := o.release(); n != 2 {
				t.Fatalf("the source's release left %d references, want 2", n)
			}
			if _, ok := dataObjectOf(async); !ok {
				t.Fatal("the object died while the target was still extracting")
			}
			endOperation(t, async)
			refs(t, o, 1, "after EndOperation: the target's own reference is left")
			if _, ok := dataObjectOf(async); !ok {
				t.Fatal("the object died under the target's own reference")
			}
			comRelease(async)
			refs(t, o, 0, "after the target's release")
			if _, ok := dataObjectOf(async); ok {
				t.Fatal("the object outlived the target's release")
			}
			s.mu.Lock()
			endOp, released := s.endOp, s.released
			s.mu.Unlock()
			if !endOp || !released {
				t.Fatalf("the stage heard endOp=%v released=%v", endOp, released)
			}
		}},
		{"the target lets go without EndOperation", func(t *testing.T, o *comObject, d *dataObject, s *stage, async uintptr) {
			// Explorer's sequence for a CF_HDROP source (measured
			// 2026-09-11): it negotiates the protocol, takes its references,
			// asks after DoDragDrop has returned, and never calls
			// EndOperation. The reference SetAsyncMode took would keep the
			// object — and the stage's Awaiting phase — alive for ever
			// unless the target's own references reaching zero gives it
			// back.
			setAsync(t, async, variantTrue)
			comAddRef(async)
			comAddRef(async)
			dataStartOperation(async, 0)
			refs(t, o, 4, "ours, the negotiation's and the target's two")
			if n := o.release(); n != 3 {
				t.Fatalf("the source's release left %d references, want 3", n)
			}
			if n := comRelease(async); n != 2 {
				t.Fatalf("the target's first release -> %d, want 2", n)
			}
			if _, ok := dataObjectOf(async); !ok {
				t.Fatal("the object died while the target still held it")
			}
			s.mu.Lock()
			released := s.released
			s.mu.Unlock()
			if released {
				t.Fatal("the stage heard a release while the target still held the object")
			}
			if n := comRelease(async); n != 1 {
				// The count the target is told is what was left when its
				// release landed; the self-reference goes in the same call.
				t.Fatalf("the target's last release -> %d, want 1", n)
			}
			refs(t, o, 0, "after the target's last release")
			if _, ok := dataObjectOf(async); ok {
				t.Fatal("the object outlived the target: the self-reference was never given back")
			}
			d.mu.Lock()
			selfRef := d.selfRef
			d.mu.Unlock()
			if selfRef {
				t.Fatal("the ledger still says the self-reference is held")
			}
			s.mu.Lock()
			endOp, released := s.endOp, s.released
			s.mu.Unlock()
			if endOp || !released {
				t.Fatalf("the stage heard endOp=%v released=%v, want false/true", endOp, released)
			}
			// A late EndOperation on the pointer the target released is a
			// call on a tombstone: refused, and nothing given back twice.
			if hr := dataEndOperation(async, sOK, 0, dropEffectCopy); hr != eUnexpected {
				t.Fatalf("EndOperation after the release -> %s, want E_UNEXPECTED", hrName(hr))
			}
			refs(t, o, 0, "after a late EndOperation")
		}},
		{"the source's own release with nothing else holding on", func(t *testing.T, o *comObject, d *dataObject, s *stage, async uintptr) {
			// A drag that never reached a drop, with no finishSync: the
			// self-reference alone must not keep the object alive once the
			// last reference from outside — ours — has gone.
			setAsync(t, async, variantTrue)
			refs(t, o, 2, "after SetAsyncMode(TRUE)")
			if n := o.release(); n != 1 {
				t.Fatalf("the source's release -> %d, want 1 (the self-reference, given back in the same call)", n)
			}
			refs(t, o, 0, "after the source's release")
			if _, ok := dataObjectOf(async); ok {
				t.Fatal("the object outlived its last outside reference")
			}
			s.mu.Lock()
			released := s.released
			s.mu.Unlock()
			if !released {
				t.Fatal("the stage was not told the object had gone")
			}
		}},
		{"EndOperation before InOperation", func(t *testing.T, o *comObject, d *dataObject, s *stage, async uintptr) {
			setAsync(t, async, variantTrue)
			dataStartOperation(async, 0)
			refs(t, o, 2, "after StartOperation")
			endOperation(t, async)
			refs(t, o, 1, "after EndOperation")
			d.finishSync()
			refs(t, o, 1, "after finishSync following EndOperation")
			endOperation(t, async)
			refs(t, o, 1, "after a second EndOperation")
		}},
		{"SetAsyncMode(TRUE) twice, then off", func(t *testing.T, o *comObject, d *dataObject, s *stage, async uintptr) {
			setAsync(t, async, variantTrue)
			setAsync(t, async, variantTrue)
			refs(t, o, 2, "after two SetAsyncMode(TRUE) calls")
			setAsync(t, async, variantFalse)
			refs(t, o, 2, "after SetAsyncMode(FALSE): nothing is given back there")
			d.finishSync()
			refs(t, o, 1, "after finishSync")
		}},
		{"no async mode", func(t *testing.T, o *comObject, d *dataObject, s *stage, async uintptr) {
			d.finishSync()
			refs(t, o, 1, "after finishSync with no SetAsyncMode")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := comRegistrySize()
			o, d, s, async := newTestDataObject(t, []Item{{Name: "async.bin", Size: 8}})
			refs(t, o, 1, "a new object")
			tc.run(t, o, d, s, async)
			if n := o.refs.Load(); n > 0 {
				if r := o.release(); r != 0 {
					t.Fatalf("the final release left %d references", r)
				}
			}
			if got := comRegistrySize(); got != before {
				t.Fatalf("%d live cells after the drag, want %d", got, before)
			}
		})
	}
}

// TestFormatRequestsAreTraced is APP.md §3's last sentence: every GetData
// and QueryGetData is logged with the format asked for and where it sits
// against the button's release, and its answer — a probe for a format never
// offered included — and never with a file name in it.
func TestFormatRequestsAreTraced(t *testing.T) {
	isolateStages(t)
	lg := &testLog{}
	installFakeMarshaler(t)
	const name = "secret-plans.bin"
	items := []Item{{Name: name, Size: 8}}
	// One log for the stage and the object: the trace is one drag's.
	s, err := newStage(Options{Root: t.TempDir(), Items: items, Extract: writeItems(items), Log: lg.printf}, time.Hour)
	if err != nil {
		t.Fatalf("newStage: %v", err)
	}
	t.Cleanup(s.finish)
	o := newHDropDataObject(s, lg.printf)
	defer o.release()
	this := o.unknown()

	hdrop := formatEtc{cfFormat: cfHDrop, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
	other := formatEtc{cfFormat: cfPerformedDropEffect, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
	if hr := dataQueryGetData(this, &hdrop); hr != sOK {
		t.Fatalf("QueryGetData(CF_HDROP) -> %s", hrName(hr))
	}
	if hr := dataQueryGetData(this, &other); hr != dvEFormatEtc {
		t.Fatalf("QueryGetData of an unoffered format -> %s", hrName(hr))
	}
	var medium stgMedium
	if hr := dataGetData(this, &hdrop, &medium); hr != sOK {
		t.Fatalf("GetData(CF_HDROP) during the hover -> %s", hrName(hr))
	}
	readHGlobal(t, medium)
	s.arm(false)
	medium = stgMedium{}
	if hr := dataGetData(this, &hdrop, &medium); hr != sOK {
		t.Fatalf("GetData(CF_HDROP) after the release -> %s", hrName(hr))
	}
	readHGlobal(t, medium)
	medium = stgMedium{}
	if hr := dataGetData(this, &other, &medium); hr != dvEFormatEtc {
		t.Fatalf("GetData of an unoffered format -> %s", hrName(hr))
	}

	text := lg.text()
	for _, want := range []string{
		"QueryGetData #1 {CF_HDROP, ptd=0x0, DVASPECT_CONTENT, lindex=-1, TYMED_HGLOBAL}, during the hover -> S_OK",
		"QueryGetData #2 {CFSTR_PERFORMEDDROPEFFECT, ptd=0x0, DVASPECT_CONTENT, lindex=-1, TYMED_HGLOBAL}, during the hover -> DV_E_FORMATETC",
		"GetData #1 {CF_HDROP, ptd=0x0, DVASPECT_CONTENT, lindex=-1, TYMED_HGLOBAL}, during the hover",
		"GetData #1 -> S_OK",
		"GetData #2 {CF_HDROP, ptd=0x0, DVASPECT_CONTENT, lindex=-1, TYMED_HGLOBAL}, +",
		"after the release",
		"GetData #2 -> S_OK",
		"GetData #3 {CFSTR_PERFORMEDDROPEFFECT",
		"GetData #3 -> DV_E_FORMATETC",
		"extraction started: 1 file(s), 8 bytes planned",
		"the extraction wrote 1 item(s), 16 bytes in", // the name is the content, 16 bytes of it
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the trace lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, name) || strings.Contains(text, s.root) {
		t.Errorf("the trace names a path:\n%s", text)
	}
}

// TestSetDataReadsOnlyFeedbackMedia is the overread guard. Every feedback
// format a drop target sends back is a DWORD in an HGLOBAL, so reading the
// first four bytes of one is reading what was sent; the medium under any
// other format is the caller's, of a length only the caller knows.
func TestSetDataReadsOnlyFeedbackMedia(t *testing.T) {
	isolateStages(t)
	lg := &testLog{}
	installFakeMarshaler(t)
	s, _, _ := newTestStage(t, []Item{{Name: "fb.bin"}}, nil)
	o := newHDropDataObject(s, lg.printf)
	defer o.release()
	this := o.unknown()

	h := globalAlloc(gHND, 4)
	if h == 0 {
		t.Fatal("GlobalAlloc(4) failed")
	}
	defer globalFree(h)
	p := globalLock(h)
	copyIntoNative(p, []byte{dropEffectMove, 0, 0, 0})
	globalUnlock(h)

	feedback := formatEtc{cfFormat: cfPerformedDropEffect, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
	medium := stgMedium{tymed: tymedHGlobal, data: h}
	// fRelease is FALSE throughout: the medium stays this test's to free.
	if hr := dataSetData(this, &feedback, &medium, 0); hr != sOK {
		t.Fatalf("SetData(CFSTR_PERFORMEDDROPEFFECT) -> %s, want S_OK", hrName(hr))
	}
	if !strings.Contains(lg.text(), "CFSTR_PERFORMEDDROPEFFECT = DROPEFFECT_MOVE") {
		t.Errorf("SetData did not report the performed effect:\n%s", lg.text())
	}
	// The same medium under a format this object never offered is declined
	// before anything is read out of it.
	n := len(lg.text())
	hdrop := formatEtc{cfFormat: cfHDrop, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
	if hr := dataSetData(this, &hdrop, &medium, 0); hr != eNotImpl {
		t.Fatalf("SetData(CF_HDROP) -> %s, want E_NOTIMPL", hrName(hr))
	}
	if len(lg.text()) != n {
		t.Errorf("SetData of an unoffered format logged something:\n%s", lg.text())
	}
	// A TYMED_NULL medium has nothing behind it at all.
	nullMedium := stgMedium{}
	if hr := dataSetData(this, &feedback, &nullMedium, 0); hr != sOK {
		t.Fatalf("SetData with a TYMED_NULL medium -> %s, want S_OK", hrName(hr))
	}
	if strings.Count(lg.text(), "= DROPEFFECT") != 1 {
		t.Errorf("SetData read a value out of a medium that has none:\n%s", lg.text())
	}
}

// TestRevokedDataObjectFailsGetData is the forced teardown: the process is
// going while the target still holds the object, and from then on GetData
// answers E_UNEXPECTED rather than name files out of a drag that is over.
// IUnknown still works, and has to: the target has to be able to let go.
func TestRevokedDataObjectFailsGetData(t *testing.T) {
	isolateStages(t)
	o, _, s, _ := newTestDataObject(t, []Item{{Name: "held.bin", Size: 8}})
	s.arm(false)
	fe := formatEtc{cfFormat: cfHDrop, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
	var medium stgMedium
	if hr := dataGetData(o.unknown(), &fe, &medium); hr != sOK {
		t.Fatalf("GetData before the teardown -> %s", hrName(hr))
	}
	readHGlobal(t, medium)
	// The target holds a reference of its own; ours goes as it does at the
	// end of every Run.
	comAddRef(o.unknown())
	o.release()

	if held := revokeLiveDataObjects(); held != 1 {
		t.Fatalf("the teardown found %d held data object(s), want 1", held)
	}
	medium = stgMedium{}
	if hr := dataGetData(o.unknown(), &fe, &medium); hr != eUnexpected {
		t.Fatalf("GetData after the teardown -> %s, want E_UNEXPECTED", hrName(hr))
	}
	if medium.data != 0 {
		t.Error("a refused GetData left a medium behind")
	}
	if n := comAddRef(o.unknown()); n != 2 {
		t.Errorf("AddRef on a revoked object -> %d, want 2", n)
	}
	if n := comRelease(o.unknown()); n != 1 {
		t.Errorf("Release on a revoked object -> %d, want 1", n)
	}
	if n := comRelease(o.unknown()); n != 0 {
		t.Errorf("the target's last Release -> %d, want 0", n)
	}
	if revokeLiveDataObjects() != 0 {
		t.Error("a released object is still counted as held")
	}
}

// TestDropSourceTellsASelfDropApart: the drop source arms the stage when the
// button comes up, as a self-drop when the window under the cursor is the
// caller's own, and answers Escape and Cancel with DRAGDROP_S_CANCEL.
func TestDropSourceTellsASelfDropApart(t *testing.T) {
	isolateStages(t)
	prev := cursorRootWindow
	t.Cleanup(func() { cursorRootWindow = prev })
	const ours, theirs = uintptr(0x1000), uintptr(0x2000)

	for _, tc := range []struct {
		name  string
		under uintptr
		self  bool
	}{{"over the desktop", theirs, false}, {"over our own window", ours, true}} {
		t.Run(tc.name, func(t *testing.T) {
			cursorRootWindow = func() uintptr { return tc.under }
			s, _, _ := newTestStage(t, []Item{{Name: "d.bin"}}, nil)
			src := newDropSourceObject(s, ours, nil)
			defer src.release()
			if hr := dropSourceQueryContinueDrag(src.unknown(), 0, mkLButton); hr != sOK {
				t.Fatalf("with the button down -> %s, want S_OK", hrName(hr))
			}
			if hr := dropSourceGiveFeedback(src.unknown(), dropEffectMove); hr != dragDropSUseDefaultCursors {
				t.Fatalf("GiveFeedback -> %s", hrName(hr))
			}
			if hr := dropSourceQueryContinueDrag(src.unknown(), 0, 0); hr != dragDropSDrop {
				t.Fatalf("with the button up -> %s, want DRAGDROP_S_DROP", hrName(hr))
			}
			s.mu.Lock()
			armed, self := s.armed, s.selfDrop
			s.mu.Unlock()
			if !armed || self != tc.self {
				t.Fatalf("armed=%v selfDrop=%v, want true/%v", armed, self, tc.self)
			}
		})
	}
	s, _, _ := newTestStage(t, []Item{{Name: "e.bin"}}, nil)
	src := newDropSourceObject(s, ours, nil)
	defer src.release()
	if hr := dropSourceQueryContinueDrag(src.unknown(), 1, mkLButton); hr != dragDropSCancel {
		t.Fatalf("Escape -> %s, want DRAGDROP_S_CANCEL", hrName(hr))
	}
	s.cancel()
	if hr := dropSourceQueryContinueDrag(src.unknown(), 0, mkLButton); hr != dragDropSCancel {
		t.Fatalf("after Cancel -> %s, want DRAGDROP_S_CANCEL", hrName(hr))
	}
	// A window of zero — none known — is never a self-drop.
	cursorRootWindow = func() uintptr { return 0 }
	s2, _, _ := newTestStage(t, []Item{{Name: "f.bin"}}, nil)
	src2 := newDropSourceObject(s2, 0, nil)
	defer src2.release()
	dropSourceQueryContinueDrag(src2.unknown(), 0, 0)
	s2.mu.Lock()
	self := s2.selfDrop
	s2.mu.Unlock()
	if self {
		t.Fatal("a drag with no window of its own reported a self-drop")
	}
}

// ---------------------------------------------------------------------------
// Aggregating the free-threaded marshaler.

// qiThroughVtable calls QueryInterface through the vtable, with the out
// parameter typed as what it really is: a pointer to a cell holding a
// vtable address.
func qiThroughVtable(this uintptr, iid *windows.GUID) (**uintptr, uintptr) {
	var out **uintptr
	hr, _, _ := syscallPinned(cbQueryInterface, this,
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	return out, hr
}

// releaseThroughVtable calls Release on an interface pointer, whoever's it
// is: one of this package's cells, or an interface the aggregated marshaler
// handed back, whose Release delegates to the object that aggregated it.
func releaseThroughVtable(punk **uintptr) uint32 {
	rel := unsafe.Slice(*punk, comSlotRelease+1)[comSlotRelease]
	n, _, _ := syscallPinned(rel, uintptr(unsafe.Pointer(punk)))
	return uint32(n)
}

func addrOf(punk **uintptr) uintptr { return uintptr(unsafe.Pointer(punk)) }

// The fake inner marshaler imitates the one rule of aggregation that
// decides every reference count here: the inner object's non-delegating
// QueryInterface hands back an interface whose IUnknown methods belong to
// the outer object, so the AddRef that goes with a successful
// QueryInterface lands on the outer's count.
type fakeMarshaler struct {
	outer     uintptr
	qiCalls   atomic.Int64
	destroyed atomic.Int64
}

func (f *fakeMarshaler) comDestroy() { f.destroyed.Add(1) }

func fakeMarshalerQI(this uintptr, riid *windows.GUID, ppv *uintptr) uintptr {
	if ppv == nil || riid == nil {
		return ePointer
	}
	*ppv = 0
	o, _, ok := comLookup(this)
	if !ok {
		return eUnexpected
	}
	f, ok := o.impl.(*fakeMarshaler)
	if !ok {
		return eUnexpected
	}
	f.qiCalls.Add(1)
	switch *riid {
	case iidIMarshal:
		*ppv = o.slotAddr(fakeMarshalerIMarshalSlot)
		comAddRef(f.outer)
		return sOK
	case iidIUnknown:
		*ppv = o.slotAddr(fakeMarshalerUnknownSlot)
		o.addRef()
		return sOK
	}
	return eNoInterface
}

func fakeMarshalerOuter(this uintptr) (*fakeMarshaler, bool) {
	o, _, ok := comLookup(this)
	if !ok {
		return nil, false
	}
	f, ok := o.impl.(*fakeMarshaler)
	return f, ok
}

func fakeMarshalerDelegateQI(this uintptr, riid *windows.GUID, ppv *uintptr) uintptr {
	f, ok := fakeMarshalerOuter(this)
	if !ok {
		return eUnexpected
	}
	return comQueryInterface(f.outer, riid, ppv)
}

func fakeMarshalerDelegateAddRef(this uintptr) uintptr {
	f, ok := fakeMarshalerOuter(this)
	if !ok {
		return 0
	}
	return comAddRef(f.outer)
}

func fakeMarshalerDelegateRelease(this uintptr) uintptr {
	f, ok := fakeMarshalerOuter(this)
	if !ok {
		return 0
	}
	return comRelease(f.outer)
}

const (
	fakeMarshalerUnknownSlot  = 0
	fakeMarshalerIMarshalSlot = 1
)

var (
	fakeMarshalerVtblOnce sync.Once
	fakeMarshalerVtblNon  uintptr
	fakeMarshalerVtblDel  uintptr
)

func fakeMarshalerVtbls() (nonDelegating, delegating uintptr) {
	fakeMarshalerVtblOnce.Do(func() {
		fakeMarshalerVtblNon = pinVtbl(iUnknownVtbl{
			QueryInterface: syscall.NewCallback(fakeMarshalerQI),
			AddRef:         cbAddRef,
			Release:        cbRelease,
		})
		fakeMarshalerVtblDel = pinVtbl(iUnknownVtbl{
			QueryInterface: syscall.NewCallback(fakeMarshalerDelegateQI),
			AddRef:         syscall.NewCallback(fakeMarshalerDelegateAddRef),
			Release:        syscall.NewCallback(fakeMarshalerDelegateRelease),
		})
	})
	return fakeMarshalerVtblNon, fakeMarshalerVtblDel
}

// installFakeMarshaler makes agile objects aggregate the fake instead of
// ole32's, for the duration of one test, and hands back the fakes it built.
func installFakeMarshaler(t *testing.T) *[]*comObject {
	t.Helper()
	made := &[]*comObject{}
	prev := createFreeThreadedMarshaler
	createFreeThreadedMarshaler = func(punkOuter uintptr) (**uintptr, uintptr) {
		nonDelegating, delegating := fakeMarshalerVtbls()
		f := &fakeMarshaler{outer: punkOuter}
		o := newCOMObject("fakeMarshaler", f, false, nil,
			comIface{name: "IUnknown", vtbl: nonDelegating, iids: nil},
			comIface{name: "IMarshal", vtbl: delegating, iids: nil},
		)
		*made = append(*made, o)
		return (**uintptr)(unsafe.Pointer(&o.block[fakeMarshalerUnknownSlot])), sOK
	}
	t.Cleanup(func() { createFreeThreadedMarshaler = prev })
	return made
}

// TestAggregationRefcountRules is the arithmetic, watched from both sides.
func TestAggregationRefcountRules(t *testing.T) {
	made := installFakeMarshaler(t)
	before := comRegistrySize()
	o := newEnumFormatEtcObject(nil, 0, nil)

	if !o.agile.Load() {
		t.Fatalf("an agile object did not aggregate a marshaler")
	}
	if len(*made) != 1 {
		t.Fatalf("%d marshalers created, want 1", len(*made))
	}
	inner := (*made)[0]
	f := inner.impl.(*fakeMarshaler)
	// "The aggregable object must not call AddRef when holding a reference
	// to the controlling IUnknown pointer": creating the marshaler must not
	// have moved our count, and the pointer it was given must be our
	// identity.
	if n := o.refs.Load(); n != 1 {
		t.Errorf("aggregating the marshaler left %d references, want 1", n)
	}
	if f.outer != o.unknown() {
		t.Errorf("the marshaler was aggregated with 0x%X, want the controlling IUnknown 0x%X", f.outer, o.unknown())
	}
	if n := inner.refs.Load(); n != 1 {
		t.Errorf("the inner object holds %d references of its own, want the one we own", n)
	}
	// QueryInterface(IID_IMarshal) is delegated, and the reference that comes
	// with it is taken by the inner object on our count — exactly one.
	// Taking another one here is the classic aggregation leak, and would
	// show as 3.
	im, hr := qiThroughVtable(o.unknown(), &iidIMarshal)
	if hr != sOK {
		t.Fatalf("QI(IID_IMarshal) -> %s, want S_OK", hrName(hr))
	}
	if im == nil || addrOf(im) != inner.slotAddr(fakeMarshalerIMarshalSlot) {
		t.Errorf("QI(IID_IMarshal) gave 0x%X, want the inner object's interface", addrOf(im))
	}
	if f.qiCalls.Load() != 1 {
		t.Errorf("the inner object's QueryInterface was called %d times, want 1", f.qiCalls.Load())
	}
	if n := o.refs.Load(); n != 2 {
		t.Errorf("after the delegated QI our count is %d, want 2 (one reference, taken once)", n)
	}
	if n := inner.refs.Load(); n != 1 {
		t.Errorf("the delegated QI moved the inner object's own count to %d, want 1", n)
	}
	if n := releaseThroughVtable(im); n != 1 {
		t.Errorf("releasing the delegated IMarshal reported %d, want our count of 1", n)
	}
	// IAgileObject is our own identity, AddRef'd like any other QueryInterface.
	ag, hr := qiThroughVtable(o.unknown(), &iidIAgileObject)
	if hr != sOK || addrOf(ag) != o.unknown() {
		t.Fatalf("QI(IID_IAgileObject) -> %s, 0x%X; want S_OK and our IUnknown", hrName(hr), addrOf(ag))
	}
	if n := releaseThroughVtable(ag); n != 1 {
		t.Errorf("releasing the IAgileObject pointer reported %d, want 1", n)
	}
	// The inner object's lifetime is bound to ours: alive while we are,
	// released exactly once when we die, and never twice on an over-release.
	if f.destroyed.Load() != 0 {
		t.Fatalf("the inner object died while we were still alive")
	}
	if n := o.release(); n != 0 {
		t.Fatalf("the last release left %d references", n)
	}
	if f.destroyed.Load() != 1 || inner.refs.Load() != 0 {
		t.Errorf("the inner object was destroyed %d times and ends with %d references", f.destroyed.Load(), inner.refs.Load())
	}
	o.release()
	if f.destroyed.Load() != 1 {
		t.Errorf("an over-release destroyed the inner object again (%d)", f.destroyed.Load())
	}
	if got := comRegistrySize(); got != before {
		t.Errorf("%d live cells after the object and its marshaler died, want %d", got, before)
	}
}

// TestFreeThreadedMarshalerFromOle32 is the same arithmetic against the real
// thing: the entry point exists in ole32.dll, it takes the controlling
// IUnknown, it answers S_OK, and the aggregation rules this package relies
// on are the ones ole32 actually implements. It creates no window, starts no
// drag and touches nothing outside the process.
func TestFreeThreadedMarshalerFromOle32(t *testing.T) {
	// CoCreateFreeThreadedMarshaler belongs to an initialised apartment, and
	// an apartment belongs to a thread, so the goroutine has to stay on one.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil {
		t.Skipf("CoInitializeEx: %v", err)
	}
	// "each successful call to CoInitialize or CoInitializeEx, including any
	// call that returns S_FALSE, must be balanced by a corresponding call to
	// CoUninitialize."
	defer windows.CoUninitialize()

	before := comRegistrySize()
	o := newEnumFormatEtcObject(nil, 0, nil)
	if !o.agile.Load() {
		t.Fatalf("CoCreateFreeThreadedMarshaler did not aggregate; the object stayed apartment-bound")
	}
	if n := o.refs.Load(); n != 1 {
		t.Errorf("CoCreateFreeThreadedMarshaler moved our reference count to %d, want 1", n)
	}
	im, hr := qiThroughVtable(o.unknown(), &iidIMarshal)
	if hr != sOK || im == nil {
		t.Fatalf("QI(IID_IMarshal) on an object aggregating the real marshaler -> %s", hrName(hr))
	}
	for i := range o.ifaces {
		if addrOf(im) == o.slotAddr(i) {
			t.Errorf("QI(IID_IMarshal) gave back our own cell %d", i)
		}
	}
	if n := o.refs.Load(); n != 2 {
		t.Errorf("the real marshaler's delegated QI left our count at %d, want 2", n)
	}
	if n := releaseThroughVtable(im); n != 1 {
		t.Errorf("releasing the real IMarshal reported %d, want our count of 1", n)
	}
	ag, hr := qiThroughVtable(o.unknown(), &iidIAgileObject)
	if hr != sOK || addrOf(ag) != o.unknown() {
		t.Fatalf("QI(IID_IAgileObject) -> %s", hrName(hr))
	}
	releaseThroughVtable(ag)
	if n := o.release(); n != 0 {
		t.Fatalf("the last release left %d references", n)
	}
	if got := comRegistrySize(); got != before {
		t.Errorf("%d live cells after the object died, want %d", got, before)
	}
}

// TestInitOLEOnThisThread: OleInitialize on a locked thread answers S_OK or
// S_FALSE, both balanced by UninitOLE, and Begin refuses before it. No
// window, no drag: Run is never called.
func TestInitOLEOnThisThread(t *testing.T) {
	isolateStages(t)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	oleMu.Lock()
	wasReady := oleReady
	oleMu.Unlock()
	if wasReady {
		t.Skip("OLE is already initialised in this process")
	}
	items := []Item{{Name: "x.bin"}}
	opts := Options{Root: t.TempDir(), Items: items, Extract: writeItems(items)}
	if _, err := Begin(opts); err != ErrUnsupported {
		t.Fatalf("Begin before InitOLE -> %v, want ErrUnsupported", err)
	}
	if err := InitOLE(); err != nil {
		t.Skipf("OleInitialize: %v", err)
	}
	defer UninitOLE()
	if oleThreadID() != windows.GetCurrentThreadId() {
		t.Fatal("InitOLE did not record the calling thread")
	}
	d, err := Begin(opts)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if _, err := Begin(opts); err != ErrBusy {
		t.Fatalf("a second Begin -> %v, want ErrBusy", err)
	}
	// Run on another thread is refused rather than attempted: the apartment
	// is this thread's.
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		_, err := d.Run()
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "thread") {
			t.Fatalf("Run on another thread -> %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run on another thread did not return")
	}
	if running.Load() {
		t.Fatal("the drag is still marked running after Run returned")
	}
	if _, err := os.Stat(d.s.root); !os.IsNotExist(err) {
		t.Errorf("a drag that could not run left its folder: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Layouts, byte for byte.

var sdkVtableOrder = map[string][]string{
	"iUnknownVtbl": {"QueryInterface", "AddRef", "Release"},
	"iDataObjectVtbl": {
		"QueryInterface", "AddRef", "Release",
		"GetData", "GetDataHere", "QueryGetData", "GetCanonicalFormatEtc",
		"SetData", "EnumFormatEtc", "DAdvise", "DUnadvise", "EnumDAdvise",
	},
	"iDataObjectAsyncCapabilityVtbl": {
		"QueryInterface", "AddRef", "Release",
		"SetAsyncMode", "GetAsyncMode", "StartOperation", "InOperation", "EndOperation",
	},
	"iEnumFORMATETCVtbl": {
		"QueryInterface", "AddRef", "Release",
		"Next", "Skip", "Reset", "Clone",
	},
	"iDropSourceVtbl": {
		"QueryInterface", "AddRef", "Release",
		"QueryContinueDrag", "GiveFeedback",
	},
}

// A vtable is an array of function pointers indexed by position: a method
// in the wrong slot is not a compile error, not a crash at the call site,
// but a jump into an unrelated function with the wrong arguments. The
// orders were read out of the Windows SDK 10.0.26100.0 headers — objidl.h,
// oleidl.h, ShlDisp.h — and this test holds the Go structs to them.
func TestVtableMethodOrder(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeOf(iUnknownVtbl{}),
		reflect.TypeOf(iDataObjectVtbl{}),
		reflect.TypeOf(iDataObjectAsyncCapabilityVtbl{}),
		reflect.TypeOf(iEnumFORMATETCVtbl{}),
		reflect.TypeOf(iDropSourceVtbl{}),
	}
	seen := map[string]bool{}
	for _, rt := range types {
		name := rt.Name()
		seen[name] = true
		want, ok := sdkVtableOrder[name]
		if !ok {
			t.Errorf("no SDK order recorded for %s", name)
			continue
		}
		if rt.NumField() != len(want) {
			t.Errorf("%s has %d slots, the SDK declares %d", name, rt.NumField(), len(want))
			continue
		}
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			if f.Name != want[i] {
				t.Errorf("%s slot %d is %s, the SDK declares %s", name, i, f.Name, want[i])
			}
			if f.Type.Kind() != reflect.Uintptr {
				t.Errorf("%s.%s is %s, must be uintptr", name, f.Name, f.Type)
			}
			if f.Offset != uintptr(i)*unsafe.Sizeof(uintptr(0)) {
				t.Errorf("%s.%s is at offset %d, want %d", name, f.Name, f.Offset, uintptr(i)*unsafe.Sizeof(uintptr(0)))
			}
		}
	}
	for name := range sdkVtableOrder {
		if !seen[name] {
			t.Errorf("SDK order recorded for %s but no Go struct was checked", name)
		}
	}
	// Every installed vtable holds a real callback in every slot, and the
	// guard that makes that so refuses a nil one.
	for _, v := range []uintptr{vtblIDataObject, vtblIDataObjectAsyncCapability, vtblIDropSource, vtblIEnumFORMATETC()} {
		if v == 0 {
			t.Error("a vtable was never built")
		}
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Errorf("pinVtbl accepted a vtable with a nil slot")
			}
		}()
		pinVtbl(iUnknownVtbl{QueryInterface: cbQueryInterface, AddRef: 0, Release: cbRelease})
	}()
}

// TestWin32StructLayouts holds the Go mirrors to the sizes and offsets
// printed from the SDK headers for x64.
func TestWin32StructLayouts(t *testing.T) {
	check := func(name string, got, want uintptr) {
		if got != want {
			t.Errorf("%s = %d, SDK says %d", name, got, want)
		}
	}
	var fe formatEtc
	check("sizeof(FORMATETC)", unsafe.Sizeof(fe), 32)
	check("FORMATETC.cfFormat", unsafe.Offsetof(fe.cfFormat), 0)
	check("FORMATETC.ptd", unsafe.Offsetof(fe.ptd), 8)
	check("FORMATETC.dwAspect", unsafe.Offsetof(fe.dwAspect), 16)
	check("FORMATETC.lindex", unsafe.Offsetof(fe.lindex), 20)
	check("FORMATETC.tymed", unsafe.Offsetof(fe.tymed), 24)
	var sm stgMedium
	check("sizeof(STGMEDIUM)", unsafe.Sizeof(sm), 24)
	check("STGMEDIUM.tymed", unsafe.Offsetof(sm.tymed), 0)
	check("STGMEDIUM.hGlobal", unsafe.Offsetof(sm.data), 8)
	check("STGMEDIUM.pUnkForRelease", unsafe.Offsetof(sm.pUnkForRelease), 16)
	check("sizeof(POINT)", unsafe.Sizeof(point{}), 8)
	check("sizeof(GUID)", unsafe.Sizeof(windows.GUID{}), 16)
}

// TestIIDBytes checks the GUIDs byte for byte against what the SDK's uuid
// library holds. A GUID written down in the wrong byte order still looks
// like a GUID; QueryInterface just never matches, and the failure surfaces
// as Explorer quietly declining the drag.
func TestIIDBytes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		iid   windows.GUID
		bytes string
	}{
		{"IID_IUnknown", iidIUnknown, "0000000000000000C000000000000046"},
		{"IID_IDataObject", iidIDataObject, "0E01000000000000C000000000000046"},
		{"IID_IEnumFORMATETC", iidIEnumFORMATETC, "0301000000000000C000000000000046"},
		{"IID_IDropSource", iidIDropSource, "2101000000000000C000000000000046"},
		{"IID_IMarshal", iidIMarshal, "0300000000000000C000000000000046"},
		{"IID_IAgileObject", iidIAgileObject, "942BEA94CCE9E049C0FFEE64CA8F5B90"},
		{"IID_IDataObjectAsyncCapability", iidIDataObjectAsyncCapability, "90058B3D91F6D2118EA9006097DF5BD4"},
	} {
		if got := guidBytes(tc.iid); got != tc.bytes {
			t.Errorf("%s = %s, SDK says %s", tc.name, got, tc.bytes)
		}
	}
	all := []windows.GUID{iidIUnknown, iidIDataObject, iidIEnumFORMATETC, iidIDropSource, iidIMarshal, iidIAgileObject, iidIDataObjectAsyncCapability}
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if all[i] == all[j] {
				t.Errorf("IIDs %d and %d are the same", i, j)
			}
		}
	}
}

// guidBytes renders a GUID in memory order, which is how it arrives from
// COM: Data1 and Data2/Data3 little-endian, Data4 as written.
func guidBytes(g windows.GUID) string {
	const hex = "0123456789ABCDEF"
	b := make([]byte, 0, 32)
	put := func(v byte) { b = append(b, hex[v>>4], hex[v&0xF]) }
	put(byte(g.Data1))
	put(byte(g.Data1 >> 8))
	put(byte(g.Data1 >> 16))
	put(byte(g.Data1 >> 24))
	put(byte(g.Data2))
	put(byte(g.Data2 >> 8))
	put(byte(g.Data3))
	put(byte(g.Data3 >> 8))
	for _, v := range g.Data4 {
		put(v)
	}
	return string(b)
}
