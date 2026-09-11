//go:build windows

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// captureLog points the COM log at a buffer for the duration of one test, so a
// test can assert on what a call said as well as on what it returned -- for the
// refusals whose whole purpose is the log line. TestMain sends the log to
// io.Discard; this restores whatever was there. The returned function takes the
// log's own mutex, so reading it is safe even while something else logs.
func captureLog(t *testing.T) func() string {
	t.Helper()
	buf := &bytes.Buffer{}
	logMu.Lock()
	prev := logWriter
	logWriter = buf
	logMu.Unlock()
	t.Cleanup(func() {
		logMu.Lock()
		logWriter = prev
		logMu.Unlock()
	})
	return func() string {
		logMu.Lock()
		defer logMu.Unlock()
		return buf.String()
	}
}

// The reference count and the registry are one mechanism: the registry is what
// keeps a Go object reachable while COM holds a pointer into its block, and the
// count is what says when to let go. Getting it wrong does not fail loudly --
// it either leaks the archive handle a real drag would be holding open, or
// frees it while Explorer is still reading. These tests pin both ends.
//
// Nothing here calls into COM. The callbacks are ordinary Go functions; they
// are invoked directly, with `this` set to a real interface-cell address.

type fakeImpl struct {
	destroyed int
}

func (f *fakeImpl) comDestroy() { f.destroyed++ }

// fakeVtbl is never dereferenced by these tests; it only has to be non-zero,
// because a COM object with a null vtable pointer is a crash waiting to happen
// and newCOMObject refuses one.
const fakeVtbl = uintptr(0x1000)

var (
	testIIDA = windows.GUID{Data1: 0xAAAA0001, Data4: [8]byte{1}}
	testIIDB = windows.GUID{Data1: 0xBBBB0002, Data4: [8]byte{2}}
	testIIDC = windows.GUID{Data1: 0xCCCC0003, Data4: [8]byte{3}}
)

func newTestObject(impl comImpl) *comObject {
	return newCOMObject("TestObject", impl,
		comIface{name: "A", vtbl: fakeVtbl, iids: []windows.GUID{testIIDA}},
		comIface{name: "B", vtbl: fakeVtbl + 8, iids: []windows.GUID{testIIDB}},
	)
}

func TestRegistryLifetime(t *testing.T) {
	before := comRegistrySize()

	impl := &fakeImpl{}
	o := newTestObject(impl)

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
	// The cells are one pointer apart and hold their vtables.
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
	// And the cells no longer resolve: a stale pointer from COM must be
	// recognised as stale rather than land on a freed object.
	if _, _, ok := comLookup(o.slotAddr(0)); ok {
		t.Fatalf("a released object is still in the registry")
	}
}

// TestCallOnAReleasedThisIsRefusedAndLogged pins the tombstone. A consumer that
// releases once too often keeps a pointer to a cell that no longer belongs to
// anything; if that address were handed back to the allocator, a later object
// could be sitting on it and the call would land on the wrong implementation
// without a word. The cell is kept, and pinned, for the life of the process
// instead, so the only thing such a call can produce is a log line.
func TestCallOnAReleasedThisIsRefusedAndLogged(t *testing.T) {
	impl := &fakeImpl{}
	o := newTestObject(impl)
	this := o.slotAddr(0)
	if n := o.release(); n != 0 {
		t.Fatalf("release gave %d, want 0", n)
	}

	logText := captureLog(t)

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
	// Nothing resurrected the object and nothing tore it down twice.
	if n := o.refs.Load(); n != 0 {
		t.Errorf("calls on a released this moved the reference count to %d", n)
	}
	if impl.destroyed != 1 {
		t.Errorf("comDestroy ran %d times, want exactly 1", impl.destroyed)
	}

	if got := strings.Count(logText(), "CALL ON A RELEASED this"); got != 3 {
		t.Errorf("the log names %d calls on a released this, want 3:\n%s", got, logText())
	}

	// The cell is still there -- that is what keeps the address out of
	// circulation -- but it does not count as live.
	comRegistry.mu.RLock()
	e, ok := comRegistry.m[this]
	comRegistry.mu.RUnlock()
	if !ok {
		t.Fatalf("the released cell was dropped from the registry; its address can be reused")
	}
	if !e.dead {
		t.Errorf("the released cell is not marked dead")
	}
}

// newTestDataObject builds a real data object. Everything it touches outside
// this process is RegisterClipboardFormatW, which is process-local and asks
// nothing of the shell; no window, no OLE, no drag.
func newTestDataObject(t *testing.T) (*comObject, *dataObject, uintptr) {
	t.Helper()
	o := newDataObject([]synthFile{{name: "async.bin", size: 4096, seed: 1}},
		time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC), false)
	d, ok := o.impl.(*dataObject)
	if !ok {
		t.Fatalf("newDataObject built a %T", o.impl)
	}
	// setCOMObject runs before the cells are published, so the object knows
	// itself from the first instant a call could arrive.
	if d.self != o {
		t.Fatalf("the data object does not know its own comObject")
	}
	async := o.ifaceAddr("IDataObjectAsyncCapability")
	if async == 0 {
		t.Fatalf("the data object has no IDataObjectAsyncCapability cell")
	}
	return o, d, async
}

// TestAsyncSelfReferenceLedger walks every path through the
// IDataObjectAsyncCapability handshake and checks the one thing all of them
// share: the reference SetAsyncMode is documented to take -- "If fDoOpAsync is
// set to VARIANT_TRUE, SetAsyncMode must call AddRef, and store the interface
// pointer for use by EndOperation" -- is given back exactly once.
//
// Both ways of getting it wrong are silent. Taken and never given back leaves
// the object, its files and its streams alive forever, which in the real thing
// is an archive held open. Given back without having been taken -- the shape
// the flag and the AddRef could get into if they were set apart from each other
// -- frees the object while Explorer is still reading from it.
func TestAsyncSelfReferenceLedger(t *testing.T) {
	setAsync := func(t *testing.T, async uintptr, v uintptr) {
		t.Helper()
		if hr := dataSetAsyncMode(async, v); hr != sOK {
			t.Fatalf("SetAsyncMode(0x%X) -> %s", v, hrName(hr))
		}
	}
	getAsync := func(t *testing.T, async uintptr) bool {
		t.Helper()
		var v uint32
		if hr := dataGetAsyncMode(async, &v); hr != sOK {
			t.Fatalf("GetAsyncMode -> %s", hrName(hr))
		}
		return v != 0
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
		run  func(t *testing.T, o *comObject, d *dataObject, async uintptr)
	}{
		{
			// Esc, or a drop on nothing: the target never touched the object.
			// DoDragDrop comes back, InOperation says no, and the source's own
			// cleanup step gives the reference back.
			name: "cancelled drag",
			run: func(t *testing.T, o *comObject, d *dataObject, async uintptr) {
				setAsync(t, async, variantTrue)
				refs(t, o, 2, "after SetAsyncMode(TRUE)")
				if inOperation(t, async) {
					t.Fatalf("InOperation is true with no StartOperation")
				}
				d.finishSync()
				refs(t, o, 1, "after finishSync")
			},
		},
		{
			// The target agreed to look, then extracted on the spot: it asked
			// about async mode but never started one.
			name: "synchronous drop",
			run: func(t *testing.T, o *comObject, d *dataObject, async uintptr) {
				setAsync(t, async, variantTrue)
				refs(t, o, 2, "after SetAsyncMode(TRUE)")
				if !getAsync(t, async) {
					t.Fatalf("GetAsyncMode is false after SetAsyncMode(TRUE)")
				}
				if inOperation(t, async) {
					t.Fatalf("InOperation is true with no StartOperation")
				}
				d.finishSync()
				refs(t, o, 1, "after finishSync")
				// A second finishSync -- a source calling its own cleanup twice
				// -- must not give back what it no longer owes.
				d.finishSync()
				refs(t, o, 1, "after a second finishSync")
			},
		},
		{
			// The documented asynchronous path. The source drops its own
			// reference while the target is still extracting; what keeps the
			// object alive from there to EndOperation is the SetAsyncMode
			// reference and nothing else.
			name: "asynchronous drop",
			run: func(t *testing.T, o *comObject, d *dataObject, async uintptr) {
				setAsync(t, async, variantTrue)
				refs(t, o, 2, "after SetAsyncMode(TRUE)")
				if hr := dataStartOperation(async, 0); hr != sOK {
					t.Fatalf("StartOperation -> %s", hrName(hr))
				}
				if !inOperation(t, async) {
					t.Fatalf("InOperation is false after StartOperation")
				}
				// Step 4 of the source procedure: release the data object. The
				// object must survive it.
				if n := o.release(); n != 1 {
					t.Fatalf("the source's release left %d references, want 1", n)
				}
				if _, ok := dataObjectOf(async); !ok {
					t.Fatalf("the object died while the target was still extracting")
				}
				endOperation(t, async)
				refs(t, o, 0, "after EndOperation")
				// It is gone: the cell no longer resolves.
				if _, ok := dataObjectOf(async); ok {
					t.Fatalf("the object outlived EndOperation")
				}
			},
		},
		{
			// A target that calls EndOperation before the source gets round to
			// InOperation. The reference goes back at EndOperation, and
			// finishSync must then find nothing to do rather than release a
			// second time.
			name: "EndOperation before InOperation",
			run: func(t *testing.T, o *comObject, d *dataObject, async uintptr) {
				setAsync(t, async, variantTrue)
				if hr := dataStartOperation(async, 0); hr != sOK {
					t.Fatalf("StartOperation -> %s", hrName(hr))
				}
				refs(t, o, 2, "after StartOperation")
				endOperation(t, async)
				refs(t, o, 1, "after EndOperation")
				if inOperation(t, async) {
					t.Fatalf("InOperation is still true after EndOperation")
				}
				d.finishSync()
				refs(t, o, 1, "after finishSync following EndOperation")
			},
		},
		{
			name: "double EndOperation",
			run: func(t *testing.T, o *comObject, d *dataObject, async uintptr) {
				setAsync(t, async, variantTrue)
				if hr := dataStartOperation(async, 0); hr != sOK {
					t.Fatalf("StartOperation -> %s", hrName(hr))
				}
				endOperation(t, async)
				refs(t, o, 1, "after EndOperation")
				endOperation(t, async)
				refs(t, o, 1, "after a second EndOperation")
			},
		},
		{
			// The target declines the offer. Async mode goes off, but the
			// reference is still owed -- SetAsyncMode(FALSE) is not documented
			// to give anything back -- and the source's cleanup is what returns
			// it, exactly as in the synchronous case.
			name: "target sets async mode off",
			run: func(t *testing.T, o *comObject, d *dataObject, async uintptr) {
				setAsync(t, async, variantTrue)
				refs(t, o, 2, "after SetAsyncMode(TRUE)")
				setAsync(t, async, variantFalse)
				refs(t, o, 2, "after SetAsyncMode(FALSE)")
				if getAsync(t, async) {
					t.Fatalf("GetAsyncMode is true after SetAsyncMode(FALSE)")
				}
				if inOperation(t, async) {
					t.Fatalf("InOperation is true with no StartOperation")
				}
				d.finishSync()
				refs(t, o, 1, "after finishSync")
			},
		},
		{
			// Twice on is once: the flag and the AddRef are decided together,
			// so a second SetAsyncMode(TRUE) cannot take a reference the first
			// one already took.
			name: "SetAsyncMode(TRUE) twice",
			run: func(t *testing.T, o *comObject, d *dataObject, async uintptr) {
				setAsync(t, async, variantTrue)
				setAsync(t, async, variantTrue)
				refs(t, o, 2, "after two SetAsyncMode(TRUE) calls")
				d.finishSync()
				refs(t, o, 1, "after finishSync")
			},
		},
		{
			// A drag that never offered async mode at all owes nothing.
			name: "no async mode",
			run: func(t *testing.T, o *comObject, d *dataObject, async uintptr) {
				refs(t, o, 1, "with no SetAsyncMode")
				d.finishSync()
				refs(t, o, 1, "after finishSync with no SetAsyncMode")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := comRegistrySize()
			o, d, async := newTestDataObject(t)
			refs(t, o, 1, "a new object")

			tc.run(t, o, d, async)

			// Whatever the path, the object ends at zero references and nothing
			// stays registered. On every path but the asynchronous one the
			// source's own reference is the last one and giving it back is what
			// destroys the object; on that one the target's EndOperation
			// already did, and there is nothing left to release.
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

// TestSetDataReadsOnlyFeedbackMedia is the overread guard. Every feedback
// format a drop target sends back is a DWORD in an HGLOBAL, so reading the
// first four bytes of one is reading what was sent; the medium under any other
// format is the caller's, of a length only the caller knows, and reading it is
// reading past the end of somebody else's allocation.
func TestSetDataReadsOnlyFeedbackMedia(t *testing.T) {
	registerFormats()
	o, _, _ := newTestDataObject(t)
	defer o.release()
	this := o.unknown()

	// A drop target reporting DROPEFFECT_COPY: four bytes, as the format says.
	h := globalAlloc(gHND, 4)
	if h == 0 {
		t.Fatal("GlobalAlloc(4) failed")
	}
	defer globalFree(h)
	p := globalLock(h)
	if p == 0 {
		t.Fatal("GlobalLock failed")
	}
	copyIntoNative(p, []byte{dropEffectCopy, 0, 0, 0})
	globalUnlock(h)

	feedback := formatEtc{
		cfFormat: cfPerformedDropEffect, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal,
	}
	medium := stgMedium{tymed: tymedHGlobal, data: h}

	logText := captureLog(t)
	// fRelease is FALSE throughout: the medium stays this test's to free.
	if hr := dataSetData(this, &feedback, &medium, 0); hr != sOK {
		t.Fatalf("SetData(CFSTR_PERFORMEDDROPEFFECT) -> %s, want S_OK", hrName(hr))
	}
	if !strings.Contains(logText(), "first DWORD = 1 (DROPEFFECT_COPY)") {
		t.Errorf("SetData did not report the feedback value:\n%s", logText())
	}

	// The same medium under a format this object never offered. It is declined
	// before anything is read out of it -- the format is the only thing that
	// says the medium is a DWORD, so with an unknown format there is nothing to
	// justify the read.
	other := captureLog(t)
	hdrop := formatEtc{cfFormat: 15, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
	if hr := dataSetData(this, &hdrop, &medium, 0); hr != eNotImpl {
		t.Fatalf("SetData(CF_HDROP) -> %s, want E_NOTIMPL", hrName(hr))
	}
	if strings.Contains(other(), "first DWORD") {
		t.Errorf("SetData read a DWORD out of a medium whose format never promised one:\n%s", other())
	}

	// A feedback format whose medium is shorter than the DWORD the format
	// promises: GlobalSize is what stops the read.
	small := globalAlloc(gHND, 1)
	if small == 0 {
		t.Fatal("GlobalAlloc(1) failed")
	}
	defer globalFree(small)
	if n := globalSize(small); n >= 4 {
		// "The size of a memory block may be larger than the size requested
		// when the memory was allocated" -- if this allocator rounded a byte up
		// past a DWORD there is no short block to test with, and saying so is
		// better than asserting something that is not being exercised.
		t.Logf("GlobalAlloc(1) reports GlobalSize %d; the short-medium case cannot be provoked here", n)
	} else {
		short := captureLog(t)
		shortMedium := stgMedium{tymed: tymedHGlobal, data: small}
		if hr := dataSetData(this, &feedback, &shortMedium, 0); hr != sOK {
			t.Fatalf("SetData with a short medium -> %s, want S_OK", hrName(hr))
		}
		if strings.Contains(short(), "first DWORD") {
			t.Errorf("SetData read a DWORD out of a %d-byte medium:\n%s", globalSize(small), short())
		}
	}

	// A TYMED_NULL medium has nothing behind it at all.
	null := captureLog(t)
	nullMedium := stgMedium{tymed: tymedNull}
	if hr := dataSetData(this, &feedback, &nullMedium, 0); hr != sOK {
		t.Fatalf("SetData with a TYMED_NULL medium -> %s, want S_OK", hrName(hr))
	}
	if strings.Contains(null(), "first DWORD") {
		t.Errorf("SetData read a DWORD out of a TYMED_NULL medium:\n%s", null())
	}
}

func TestQueryInterfaceIdentityAndRefcounts(t *testing.T) {
	impl := &fakeImpl{}
	o := newTestObject(impl)
	defer func() {
		for o.refs.Load() > 0 {
			o.release()
		}
	}()

	var ppv uintptr

	// IID_IUnknown must come back as the same pointer from every interface --
	// that identity is how a target decides two pointers are one object.
	for i := 0; i < 2; i++ {
		ppv = 0
		if hr := comQueryInterface(o.slotAddr(i), &iidIUnknown, &ppv); hr != sOK {
			t.Fatalf("QI(IUnknown) through cell %d -> %s", i, hrName(hr))
		}
		if ppv != o.slotAddr(0) {
			t.Fatalf("QI(IUnknown) through cell %d gave 0x%X, want the identity cell 0x%X",
				i, ppv, o.slotAddr(0))
		}
		o.release() // give back the reference QueryInterface took
	}

	// Each interface's own IID gives its own cell, whichever cell was asked.
	for _, tc := range []struct {
		iid  windows.GUID
		want uintptr
	}{
		{testIIDA, o.slotAddr(0)},
		{testIIDB, o.slotAddr(1)},
	} {
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
	if ppv != 0 {
		t.Fatalf("QI(unknown IID) left *ppv = 0x%X, want 0", ppv)
	}
	if o.refs.Load() != before {
		t.Fatalf("a failed QueryInterface changed the reference count")
	}

	// Null out-pointer: E_POINTER, nothing else.
	if hr := comQueryInterface(o.slotAddr(0), &testIIDA, nil); hr != ePointer {
		t.Fatalf("QI(ppv=nil) -> %s, want E_POINTER", hrName(hr))
	}
	if o.refs.Load() != before {
		t.Fatalf("QI with a null ppv changed the reference count")
	}

	// A `this` the registry has never seen must be refused, not followed.
	ppv = 0
	if hr := comQueryInterface(0xBADC0FFEE0, &iidIUnknown, &ppv); hr != eUnexpected {
		t.Fatalf("QI on an unknown this -> %s, want E_UNEXPECTED", hrName(hr))
	}
	if hr := comAddRef(0xBADC0FFEE0); hr != 0 {
		t.Fatalf("AddRef on an unknown this -> %d", hr)
	}
	if hr := comRelease(0xBADC0FFEE0); hr != 0 {
		t.Fatalf("Release on an unknown this -> %d", hr)
	}
}

func TestAddRefReleaseCallbacksReportCounts(t *testing.T) {
	impl := &fakeImpl{}
	o := newTestObject(impl)

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

func TestIfaceAddrAndUnknown(t *testing.T) {
	impl := &fakeImpl{}
	o := newTestObject(impl)
	defer o.release()

	if o.unknown() != o.slotAddr(0) {
		t.Fatalf("unknown() is not the identity cell")
	}
	if o.ifaceAddr("B") != o.slotAddr(1) {
		t.Fatalf("ifaceAddr(B) is wrong")
	}
	if o.ifaceAddr("nope") != 0 {
		t.Fatalf("ifaceAddr for an interface the object does not have returned non-zero")
	}
}

// TestDataObjectFormatMatching exercises the FORMATETC matching rules without
// registering a clipboard format or creating a COM object: the descriptor is
// advertised once with lindex -1, and FileContents once per file with its own
// zero-based index, which is how "a particular file's CFSTR_FILECONTENTS
// format" is named.
func TestDataObjectFormatMatching(t *testing.T) {
	const (
		cfDesc = uint16(49158)
		cfCont = uint16(49159)
	)
	d := &dataObject{files: make([]synthFile, 3)}
	d.formats = append(d.formats, formatEtc{
		cfFormat: cfDesc, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal,
	})
	for i := range d.files {
		d.formats = append(d.formats, formatEtc{
			cfFormat: cfCont, dwAspect: dvAspectContent, lindex: int32(i), tymed: tymedIStream,
		})
	}
	if len(d.formats) != 4 {
		t.Fatalf("3 files should advertise 4 formats, got %d", len(d.formats))
	}

	for _, tc := range []struct {
		name string
		fe   formatEtc
		want bool
	}{
		{"the descriptor", formatEtc{cfFormat: cfDesc, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}, true},
		{"file 0", formatEtc{cfFormat: cfCont, dwAspect: dvAspectContent, lindex: 0, tymed: tymedIStream}, true},
		{"file 2", formatEtc{cfFormat: cfCont, dwAspect: dvAspectContent, lindex: 2, tymed: tymedIStream}, true},
		// "You can specify more than one acceptable tymed medium with the
		// Boolean OR operator": an overlap is a match.
		{"file 1, several media offered", formatEtc{cfFormat: cfCont, dwAspect: dvAspectContent, lindex: 1, tymed: tymedHGlobal | tymedIStream | 8}, true},
		{"file 3 does not exist", formatEtc{cfFormat: cfCont, dwAspect: dvAspectContent, lindex: 3, tymed: tymedIStream}, false},
		{"the descriptor is not per-file", formatEtc{cfFormat: cfDesc, dwAspect: dvAspectContent, lindex: 0, tymed: tymedHGlobal}, false},
		{"wrong medium", formatEtc{cfFormat: cfCont, dwAspect: dvAspectContent, lindex: 0, tymed: tymedHGlobal}, false},
		{"wrong aspect", formatEtc{cfFormat: cfCont, dwAspect: 2, lindex: 0, tymed: tymedIStream}, false},
		{"a format we never offered", formatEtc{cfFormat: 15, dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}, false},
	} {
		fe := tc.fe
		if _, got := d.match(&fe); got != tc.want {
			t.Errorf("%s: match = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEnumFormatEtcCore(t *testing.T) {
	formats := []formatEtc{
		{cfFormat: 1, lindex: -1},
		{cfFormat: 2, lindex: 0},
		{cfFormat: 2, lindex: 1},
	}
	o := newEnumFormatEtcObject(formats, 0)
	defer o.release()
	e := o.impl.(*enumFormatEtc)

	// The enumerator copies: a later edit of the source must not reach it.
	formats[0].cfFormat = 999
	if e.formats[0].cfFormat != 1 {
		t.Fatalf("the enumerator aliased the caller's slice")
	}

	if e.idx != 0 {
		t.Fatalf("a new enumerator starts at %d", e.idx)
	}
	e.idx = 2
	c := newEnumFormatEtcObject(e.formats, e.idx)
	defer c.release()
	if c.impl.(*enumFormatEtc).idx != 2 {
		t.Fatalf("Clone did not preserve the position")
	}
}
