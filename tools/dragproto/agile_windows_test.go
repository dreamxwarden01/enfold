//go:build windows

package main

import (
	"bytes"
	"fmt"
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

// What these hold down.
//
// -agile changes two things that are easy to get wrong and silent when they
// are: the reference-count arithmetic of aggregating the free-threaded
// marshaler, and the assumption every other file in this package makes that a
// call arrives on one thread at a time. The first half is checked twice --
// against a fake inner object, where every count can be watched, and against
// ole32's real marshaler, because a rule that only holds against a fake is a
// rule about the fake. The second half is checked by hammering one stream from
// several goroutines under -race.
//
// Nothing here opens a window, starts a drag or talks to the shell. The COM
// calls that do leave this package go to ole32 and no further.

// ---------------------------------------------------------------------------
// Calling an interface pointer the way COM does.

// qiThroughVtable calls QueryInterface through the vtable, with the out
// parameter typed as what it really is: a pointer to a cell holding a vtable
// address. Going the long way round rather than calling comQueryInterface
// directly is what makes the answer usable -- an interface pointer received as
// a bare integer could not be called back without turning an integer into a
// pointer, which is the one thing this package never does.
func qiThroughVtable(this uintptr, iid *windows.GUID) (**uintptr, uintptr) {
	var out **uintptr
	hr, _, _ := syscallPinned(cbQueryInterface, this,
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))
	return out, hr
}

// releaseThroughVtable calls Release on an interface pointer, whoever's it is:
// one of this package's cells, or an interface the aggregated marshaler handed
// back, whose Release delegates to the object that aggregated it.
func releaseThroughVtable(punk **uintptr) uint32 {
	rel := unsafe.Slice(*punk, comSlotRelease+1)[comSlotRelease]
	n, _, _ := syscallPinned(rel, uintptr(unsafe.Pointer(punk)))
	return uint32(n)
}

func addrOf(punk **uintptr) uintptr { return uintptr(unsafe.Pointer(punk)) }

// ---------------------------------------------------------------------------
// The fake inner marshaler.
//
// It imitates the one rule of aggregation that decides every reference count
// here: the inner object's non-delegating QueryInterface hands back an
// interface whose IUnknown methods belong to the *outer* object, so the AddRef
// that goes with a successful QueryInterface lands on the outer's count. COM
// says the same thing from the other side: "The outer object must call its
// controlling IUnknown Release method if it queries for a pointer to any of the
// inner object's interfaces."

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
		// The inner object's IMarshal, which is the delegating cell: its
		// IUnknown methods are the outer object's, so the AddRef that goes with
		// this QueryInterface lands on the outer's count.
		*ppv = o.slotAddr(fakeMarshalerIMarshalSlot)
		comAddRef(f.outer)
		return sOK
	case iidIUnknown:
		// The non-delegating IUnknown: its own count, nobody else's. "The
		// aggregable (or inner) object's implementation of QueryInterface,
		// AddRef, and Release for its IUnknown interface controls the inner
		// object's reference count, and this implementation must not delegate
		// to the outer object's unknown."
		*ppv = o.slotAddr(fakeMarshalerUnknownSlot)
		o.addRef()
		return sOK
	}
	return eNoInterface
}

// The delegating half. A real inner object's every interface other than its own
// IUnknown forwards these three to the controlling IUnknown; three slots are
// all this fake needs, because nothing here ever calls an IMarshal method.

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
	fakeMarshalerVtblNon  uintptr // the non-delegating IUnknown
	fakeMarshalerVtblDel  uintptr // every other interface
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

// installFakeMarshaler makes newTransferCOMObject aggregate the fake instead of
// ole32's, for the duration of one test, and hands back the fakes it built.
func installFakeMarshaler(t *testing.T) *[]*comObject {
	t.Helper()
	made := &[]*comObject{}
	prev := createFreeThreadedMarshaler
	createFreeThreadedMarshaler = func(punkOuter uintptr) (**uintptr, uintptr) {
		nonDelegating, delegating := fakeMarshalerVtbls()
		f := &fakeMarshaler{outer: punkOuter}
		o := newCOMObject("fakeMarshaler", f,
			comIface{name: "IUnknown", vtbl: nonDelegating, iids: nil},
			comIface{name: "IMarshal", vtbl: delegating, iids: nil},
		)
		*made = append(*made, o)
		// The pointer handed back is the non-delegating IUnknown, which is what
		// CoCreateFreeThreadedMarshaler returns and what the outer object owns
		// one reference to.
		return (**uintptr)(unsafe.Pointer(&o.block[fakeMarshalerUnknownSlot])), sOK
	}
	t.Cleanup(func() { createFreeThreadedMarshaler = prev })
	return made
}

// withAgile turns -agile on for one test.
func withAgile(t *testing.T, on bool) {
	t.Helper()
	prev := agileMode.Load()
	agileMode.Store(on)
	t.Cleanup(func() { agileMode.Store(prev) })
}

func testStreamObject(t *testing.T, size int64) *comObject {
	t.Helper()
	f := &synthFile{name: "agile.bin", size: size, seed: 0x9E3779B9}
	return newStreamObject(f, 0)
}

// TestAggregationRefcountRules is the arithmetic, watched from both sides.
func TestAggregationRefcountRules(t *testing.T) {
	withAgile(t, true)
	made := installFakeMarshaler(t)

	before := comRegistrySize()
	o := testStreamObject(t, 4096)

	if !o.agile.Load() {
		t.Fatalf("an object built with -agile did not aggregate a marshaler")
	}
	if len(*made) != 1 {
		t.Fatalf("%d marshalers created, want 1", len(*made))
	}
	inner := (*made)[0]
	f := inner.impl.(*fakeMarshaler)

	// "The aggregable object must not call AddRef when holding a reference to
	// the controlling IUnknown pointer": creating the marshaler must not have
	// moved our count, and the pointer it was given must be our identity.
	if n := o.refs.Load(); n != 1 {
		t.Errorf("aggregating the marshaler left %d references, want 1", n)
	}
	if f.outer != o.unknown() {
		t.Errorf("the marshaler was aggregated with 0x%X, want the controlling IUnknown 0x%X",
			f.outer, o.unknown())
	}
	if n := inner.refs.Load(); n != 1 {
		t.Errorf("the inner object holds %d references of its own, want the one we own", n)
	}

	// QueryInterface(IID_IMarshal) is delegated, and the reference that comes
	// with it is taken by the inner object on our count -- exactly one. Taking
	// another one here is the classic aggregation leak, and would show as 3.
	im, hr := qiThroughVtable(o.unknown(), &iidIMarshal)
	if hr != sOK {
		t.Fatalf("QI(IID_IMarshal) -> %s, want S_OK", hrName(hr))
	}
	if im == nil || addrOf(im) != inner.slotAddr(fakeMarshalerIMarshalSlot) {
		t.Errorf("QI(IID_IMarshal) gave 0x%X, want the inner object's interface 0x%X",
			addrOf(im), inner.slotAddr(fakeMarshalerIMarshalSlot))
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

	// Releasing that pointer goes to our count, because that is what delegation
	// means: the inner object is not what the caller is holding.
	if n := releaseThroughVtable(im); n != 1 {
		t.Errorf("releasing the delegated IMarshal reported %d, want our count of 1", n)
	}
	if n := o.refs.Load(); n != 1 {
		t.Errorf("after releasing the delegated IMarshal our count is %d, want 1", n)
	}

	// IAgileObject is our own identity, AddRef'd like any other QueryInterface.
	ag, hr := qiThroughVtable(o.unknown(), &iidIAgileObject)
	if hr != sOK {
		t.Fatalf("QI(IID_IAgileObject) -> %s, want S_OK", hrName(hr))
	}
	if addrOf(ag) != o.unknown() {
		t.Errorf("QI(IID_IAgileObject) gave 0x%X, want our IUnknown 0x%X", addrOf(ag), o.unknown())
	}
	if n := o.refs.Load(); n != 2 {
		t.Errorf("QI(IID_IAgileObject) left %d references, want 2", n)
	}
	if n := releaseThroughVtable(ag); n != 1 {
		t.Errorf("releasing the IAgileObject pointer reported %d, want 1", n)
	}

	// The inner object's lifetime is bound to ours: alive while we are, released
	// exactly once when we die.
	if f.destroyed.Load() != 0 {
		t.Fatalf("the inner object died while we were still alive")
	}
	if n := o.release(); n != 0 {
		t.Fatalf("the last release left %d references", n)
	}
	if f.destroyed.Load() != 1 {
		t.Errorf("the inner object was destroyed %d times, want exactly 1", f.destroyed.Load())
	}
	if n := inner.refs.Load(); n != 0 {
		t.Errorf("the inner object ends with %d references, want 0", n)
	}

	// An over-release must not give the marshaler back twice. The swap in
	// releaseMarshaler is what makes that impossible; this is the proof.
	o.release()
	if f.destroyed.Load() != 1 {
		t.Errorf("an over-release destroyed the inner object again (%d)", f.destroyed.Load())
	}
	if got := comRegistrySize(); got != before {
		t.Errorf("%d live cells after the object and its marshaler died, want %d", got, before)
	}
}

// TestApartmentBoundObjectAnswersNeitherIID is the baseline the flag is
// measured against: without -agile the object says no to both, which is what
// makes COM marshal it back to the apartment that created it.
func TestApartmentBoundObjectAnswersNeitherIID(t *testing.T) {
	withAgile(t, false)
	made := installFakeMarshaler(t)

	o := testStreamObject(t, 4096)
	defer o.release()

	if len(*made) != 0 {
		t.Errorf("an apartment-bound object created %d marshalers, want none", len(*made))
	}
	if o.agile.Load() {
		t.Errorf("an object built without -agile claims to be agile")
	}
	for _, iid := range []windows.GUID{iidIMarshal, iidIAgileObject} {
		ppv := uintptr(0xDEAD)
		before := o.refs.Load()
		if hr := comQueryInterface(o.unknown(), &iid, &ppv); hr != eNoInterface {
			t.Errorf("QI(%s) -> %s, want E_NOINTERFACE", iidName(iid), hrName(hr))
		}
		if ppv != 0 {
			t.Errorf("QI(%s) left *ppv = 0x%X", iidName(iid), ppv)
		}
		if o.refs.Load() != before {
			t.Errorf("a refused QI(%s) changed the reference count", iidName(iid))
		}
	}
}

// TestFreeThreadedMarshalerFromOle32 is the same arithmetic against the real
// thing. It is what says the binding is right -- the entry point exists in
// ole32.dll, it takes the controlling IUnknown, it answers S_OK -- and that the
// aggregation rules this package relies on are the ones ole32 actually
// implements, rather than the ones a fake was written to satisfy.
//
// It creates no window, starts no drag and touches nothing outside the process.
func TestFreeThreadedMarshalerFromOle32(t *testing.T) {
	// CoCreateFreeThreadedMarshaler belongs to an initialised apartment, and an
	// apartment belongs to a thread, so the goroutine has to stay on one.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	procCoInitializeEx := modole32.NewProc("CoInitializeEx")
	procCoUninitialize := modole32.NewProc("CoUninitialize")
	const (
		coinitApartmentThreaded = 0x2
		rpcEChangedMode         = 0x80010106
	)
	hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded)
	if uint32(hr) == rpcEChangedMode {
		t.Skip("this thread is already in an apartment of another kind")
	}
	if int32(uint32(hr)) < 0 {
		t.Skipf("CoInitializeEx -> %s", hrName(hr))
	}
	// "each successful call to CoInitialize or CoInitializeEx, including any
	// call that returns S_FALSE, must be balanced by a corresponding call to
	// CoUninitialize."
	defer procCoUninitialize.Call()

	withAgile(t, true)
	before := comRegistrySize()
	o := testStreamObject(t, 4096)

	if !o.agile.Load() {
		t.Fatalf("CoCreateFreeThreadedMarshaler did not aggregate; the object stayed apartment-bound")
	}
	if n := o.refs.Load(); n != 1 {
		t.Errorf("CoCreateFreeThreadedMarshaler moved our reference count to %d, want 1", n)
	}

	im, hr := qiThroughVtable(o.unknown(), &iidIMarshal)
	if hr != sOK {
		t.Fatalf("QI(IID_IMarshal) on an object aggregating the real marshaler -> %s", hrName(hr))
	}
	if im == nil {
		t.Fatalf("QI(IID_IMarshal) succeeded with a null pointer")
	}
	// It is the marshaler's interface, not one of ours.
	for i := range o.ifaces {
		if addrOf(im) == o.slotAddr(i) {
			t.Errorf("QI(IID_IMarshal) gave back our own cell %d", i)
		}
	}
	// The reference came out of our count, which is what aggregation means and
	// what this package is counting on when it does not AddRef in that branch.
	if n := o.refs.Load(); n != 2 {
		t.Errorf("the real marshaler's delegated QI left our count at %d, want 2", n)
	}
	if n := releaseThroughVtable(im); n != 1 {
		t.Errorf("releasing the real IMarshal reported %d, want our count of 1", n)
	}
	if n := o.refs.Load(); n != 1 {
		t.Errorf("after releasing the real IMarshal our count is %d, want 1", n)
	}

	ag, hr := qiThroughVtable(o.unknown(), &iidIAgileObject)
	if hr != sOK {
		t.Fatalf("QI(IID_IAgileObject) -> %s", hrName(hr))
	}
	if addrOf(ag) != o.unknown() {
		t.Errorf("QI(IID_IAgileObject) gave 0x%X, want our IUnknown 0x%X", addrOf(ag), o.unknown())
	}
	releaseThroughVtable(ag)

	if n := o.release(); n != 0 {
		t.Fatalf("the last release left %d references", n)
	}
	if got := comRegistrySize(); got != before {
		t.Errorf("%d live cells after the object died, want %d", got, before)
	}
}

// TestCallTallyNamesTheThread checks the summary that answers the whole
// question -agile exists to ask: did the calls arrive on the drag thread or
// somewhere else.
func TestCallTallyNamesTheThread(t *testing.T) {
	// Locked, so that the goroutine below cannot be scheduled onto this very
	// thread and make "another thread" a thread it is not.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	prev := dragThreadID.Load()
	dragThreadID.Store(windows.GetCurrentThreadId())
	t.Cleanup(func() { dragThreadID.Store(prev) })

	o := testStreamObject(t, 1024)
	defer o.release()
	this := o.unknown()

	comAddRef(this)
	comRelease(this)
	home := o.callsHome.Load()
	if home < 2 {
		t.Fatalf("%d calls counted on this thread, want at least the two just made", home)
	}
	if away := o.callsAway.Load(); away != 0 {
		t.Errorf("%d calls counted on other threads before any were made", away)
	}

	done := make(chan uint32, 1)
	go func() {
		// A locked thread of its own, so the tid really is a different one.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		comAddRef(this)
		comRelease(this)
		done <- windows.GetCurrentThreadId()
	}()
	other := <-done

	if got := o.callsAway.Load(); got != 2 {
		t.Errorf("%d calls counted on other threads, want the two made on the goroutine's", got)
	}
	if got := o.callsHome.Load(); got != home {
		t.Errorf("calls on another thread were counted against the drag thread (%d, was %d)", got, home)
	}
	sum := o.threadSummary()
	for _, want := range []string{"on the drag thread", "on other threads"} {
		if !strings.Contains(sum, want) {
			t.Errorf("the summary %q does not say %q", sum, want)
		}
	}
	if other != 0 && !strings.Contains(sum, "(other)") {
		t.Errorf("the summary %q does not name the other thread", sum)
	}
}

// ---------------------------------------------------------------------------
// Concurrency.
//
// With -agile these callbacks can run on several of the target's threads at
// once, and on the drag thread at the same time. Everything below goes through
// the COM entry points rather than the Go core, because the entry points are
// what Explorer calls and the locking is theirs to get right.

// TestStreamSurvivesConcurrentCallers is the -race test. Two goroutines read
// and seek the same stream while a third asks for its size and a fourth clones
// it; what must come out the other end is no data race, no panic, and -- the
// part a race detector cannot check -- no read that returns bytes stitched
// together from two positions.
//
// The invariant that makes that checkable without locking the test to the
// stream: every seek is to a multiple of the chunk size and every read asks for
// exactly one chunk, so whatever interleaving happens, a buffer that comes back
// full must equal the file at some chunk boundary. A read that took its first
// half from one position and its second from another matches nothing.
func TestStreamSurvivesConcurrentCallers(t *testing.T) {
	const (
		size   = 64 << 10
		chunk  = 256
		rounds = 200
	)
	whole := make([]byte, size)
	f := &synthFile{name: "concurrent.bin", size: size, seed: 0x5EEDFACE}
	patternAt(f.seed, 0, whole)

	o := newStreamObject(f, 0)
	defer o.release()
	this := o.unknown()

	// atChunkBoundary reports whether buf is the file's bytes at some multiple
	// of chunk -- the only thing a correct read can be.
	atChunkBoundary := func(buf []byte) bool {
		for off := 0; off+len(buf) <= size; off += chunk {
			if bytes.Equal(whole[off:off+len(buf)], buf) {
				return true
			}
		}
		return false
	}

	var wg sync.WaitGroup
	bad := make(chan string, 16)
	// Failures are collected rather than reported from the goroutine: a
	// testing.T is not for use after the test function returns, and a race
	// detector run is exactly where a goroutine outlives its expectations.
	report := func(format string, args ...any) {
		select {
		case bad <- fmt.Sprintf(format, args...):
		default:
		}
	}

	// Two readers of the same stream, seeking under each other.
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			buf := make([]byte, chunk)
			for i := 0; i < rounds; i++ {
				if i%7 == 0 {
					off := int64((i * chunk) % (size - chunk))
					off -= off % chunk
					if hr := streamSeek(this, uintptr(off), streamSeekSet, nil); hr != sOK {
						report("Seek(%d) -> %s", off, hrName(hr))
						return
					}
				}
				var got uint32
				hr := streamRead(this, &buf[0], chunk, &got)
				if hr != sOK && hr != sFALSE {
					report("Read -> %s", hrName(hr))
					return
				}
				if got > chunk {
					report("Read returned %d bytes for a %d-byte buffer", got, chunk)
					return
				}
				if got == chunk && !atChunkBoundary(buf) {
					report("a read returned bytes that are not the file at any chunk boundary")
					return
				}
			}
		}(g)
	}

	// Stat, which answers outside the mutex and must stay true throughout.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			var st statStg
			// STATFLAG_NONAME: no name means no CoTaskMemAlloc to free, and the
			// size is the part under test.
			if hr := streamStat(this, &st, statFlagNoName); hr != sOK {
				report("Stat -> %s", hrName(hr))
				return
			}
			if st.cbSize != size {
				report("Stat reports cbSize %d, want %d", st.cbSize, size)
				return
			}
		}
	}()

	// Clones, each with a seek pointer of its own, read to the end and checked
	// against the generator. This is the shape a target that copies several
	// files at once would produce.
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				var clone uintptr
				if hr := streamClone(this, &clone); hr != sOK {
					report("Clone -> %s", hrName(hr))
					return
				}
				if hr := streamSeek(clone, 0, streamSeekSet, nil); hr != sOK {
					report("Seek on a clone -> %s", hrName(hr))
					comRelease(clone)
					return
				}
				buf := make([]byte, 4096)
				got := make([]byte, 0, size)
				for {
					var n uint32
					hr := streamRead(clone, &buf[0], uintptr(len(buf)), &n)
					if hr != sOK && hr != sFALSE {
						report("Read on a clone -> %s", hrName(hr))
						break
					}
					got = append(got, buf[:n]...)
					if n == 0 || hr == sFALSE {
						break
					}
				}
				if !bytes.Equal(got, whole) {
					report("a clone read %d bytes that are not the file", len(got))
				}
				comRelease(clone)
			}
		}()
	}

	wg.Wait()
	close(bad)
	for msg := range bad {
		t.Error(msg)
	}
}

// ---------------------------------------------------------------------------
// Tearing down under a target that is still holding on.

// TestRevokedStreamAnswersReverted pins the second half of the close fix: after
// a forced teardown every method answers, and answers the same way, instead of
// working on state that is being dismantled.
func TestRevokedStreamAnswersReverted(t *testing.T) {
	o := testStreamObject(t, 4096)
	defer o.release()
	this := o.unknown()

	buf := make([]byte, 64)
	var got uint32
	if hr := streamRead(this, &buf[0], uintptr(len(buf)), &got); hr != sOK || got != 64 {
		t.Fatalf("the first read -> %s, %d bytes", hrName(hr), got)
	}

	s, ok := streamOf(this)
	if !ok {
		t.Fatal("the stream does not resolve")
	}
	s.revoke()
	if !s.revoked.Load() {
		t.Fatal("revoke did not take")
	}

	got = 7
	if hr := streamRead(this, &buf[0], uintptr(len(buf)), &got); hr != stgEReverted {
		t.Errorf("Read after a revoke -> %s, want STG_E_REVERTED", hrName(hr))
	}
	if got != 0 {
		t.Errorf("a refused Read left pcbRead = %d, want 0", got)
	}
	if hr := streamSeek(this, 0, streamSeekSet, nil); hr != stgEReverted {
		t.Errorf("Seek after a revoke -> %s, want STG_E_REVERTED", hrName(hr))
	}
	var st statStg
	if hr := streamStat(this, &st, statFlagNoName); hr != stgEReverted {
		t.Errorf("Stat after a revoke -> %s, want STG_E_REVERTED", hrName(hr))
	}
	clone := uintptr(0xDEAD)
	if hr := streamClone(this, &clone); hr != stgEReverted {
		t.Errorf("Clone after a revoke -> %s, want STG_E_REVERTED", hrName(hr))
	}
	if clone != 0 {
		t.Errorf("a refused Clone left *ppstm = 0x%X", clone)
	}
	if hr := streamWrite(this, &buf[0], 1, nil); hr != stgEReverted {
		t.Errorf("Write after a revoke -> %s, want STG_E_REVERTED", hrName(hr))
	}

	// IUnknown still works, and has to: the target has to be able to let go.
	if n := comAddRef(this); n != 2 {
		t.Errorf("AddRef on a revoked stream -> %d, want 2", n)
	}
	if n := comRelease(this); n != 1 {
		t.Errorf("Release on a revoked stream -> %d, want 1", n)
	}
	// Revoking twice is one revoke.
	s.revoke()
}

// resetCloseState gives one test the close state machine to itself.
func resetCloseState(t *testing.T) {
	t.Helper()
	prev := [4]bool{closeDeferred.Load(), closeForced.Load(), endOpAfterClose.Load(), transferRevoked.Load()}
	closeDeferred.Store(false)
	closeForced.Store(false)
	endOpAfterClose.Store(false)
	transferRevoked.Store(false)
	t.Cleanup(func() {
		closeDeferred.Store(prev[0])
		closeForced.Store(prev[1])
		endOpAfterClose.Store(prev[2])
		transferRevoked.Store(prev[3])
	})
}

// TestCloseWaitsForEndOperation is the first half of the close fix, and the
// answer to a real drop that went wrong: a window that goes away mid-transfer
// takes the transfer with it.
func TestCloseWaitsForEndOperation(t *testing.T) {
	resetCloseState(t)

	o := newDataObject([]synthFile{{name: "close.bin", size: 4096, seed: 3}},
		time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC), false)
	d := o.impl.(*dataObject)
	async := o.ifaceAddr("IDataObjectAsyncCapability")
	defer func() {
		for o.refs.Load() > 0 {
			o.release()
		}
	}()

	if hr := dataSetAsyncMode(async, variantTrue); hr != sOK {
		t.Fatalf("SetAsyncMode -> %s", hrName(hr))
	}
	if hr := dataStartOperation(async, 0); hr != sOK {
		t.Fatalf("StartOperation -> %s", hrName(hr))
	}
	if !d.inOperation() {
		t.Fatal("InOperation is false after StartOperation")
	}

	found := false
	for _, live := range transfersInFlight() {
		if live == o {
			found = true
		}
	}
	if !found {
		t.Fatal("a data object with an operation running is not reported as in flight")
	}

	// The first close is swallowed.
	if mayClose() {
		t.Fatal("the window closed while a drop operation was in flight")
	}
	if !closeDeferred.Load() {
		t.Error("the deferred close was not recorded")
	}

	// EndOperation is what it was waiting for. There is no window in a test, so
	// nothing is posted; the flag it sets is what the next WM_CLOSE reads.
	if hr := dataEndOperation(async, sOK, 0, dropEffectCopy); hr != sOK {
		t.Fatalf("EndOperation -> %s", hrName(hr))
	}
	if !endOpAfterClose.Load() {
		t.Error("EndOperation after a deferred close did not release the close")
	}
	if !mayClose() {
		t.Error("the window did not close after EndOperation")
	}
	if closeForced.Load() {
		t.Error("a close that waited politely was recorded as forced")
	}
}

// TestSecondCloseForcesAndRevokes is the other path: the target never comes
// back, the user closes again, and everything still out there is named and
// revoked rather than quietly torn down.
func TestSecondCloseForcesAndRevokes(t *testing.T) {
	resetCloseState(t)

	o := newDataObject([]synthFile{{name: "forced.bin", size: 4096, seed: 4}},
		time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC), false)
	async := o.ifaceAddr("IDataObjectAsyncCapability")
	defer func() {
		for o.refs.Load() > 0 {
			o.release()
		}
	}()
	if hr := dataSetAsyncMode(async, variantTrue); hr != sOK {
		t.Fatalf("SetAsyncMode -> %s", hrName(hr))
	}
	if hr := dataStartOperation(async, 0); hr != sOK {
		t.Fatalf("StartOperation -> %s", hrName(hr))
	}

	// A stream the target is holding, exactly as GetData would have handed it.
	var medium stgMedium
	fe := formatEtc{cfFormat: cfFileContents, dwAspect: dvAspectContent, lindex: 0, tymed: tymedIStream}
	if hr := dataGetData(o.unknown(), &fe, &medium); hr != sOK {
		t.Fatalf("GetData(FileContents) -> %s", hrName(hr))
	}
	stream := medium.data
	defer comRelease(stream)

	if mayClose() {
		t.Fatal("the first close was not deferred")
	}
	if !mayClose() {
		t.Fatal("the second close did not force the exit")
	}
	if !closeForced.Load() {
		t.Error("the forced close was not recorded")
	}

	s, ok := streamOf(stream)
	if !ok {
		t.Fatal("the stream the target holds does not resolve")
	}
	if !s.revoked.Load() {
		t.Error("a forced close left a live stream serving bytes")
	}
	buf := make([]byte, 16)
	var got uint32
	if hr := streamRead(stream, &buf[0], uintptr(len(buf)), &got); hr != stgEReverted {
		t.Errorf("a read after a forced close -> %s, want STG_E_REVERTED", hrName(hr))
	}

	// And a stream created after the teardown is born revoked, so a target that
	// asks for one more file gets an answer rather than bytes.
	var second stgMedium
	if hr := dataGetData(o.unknown(), &fe, &second); hr != sOK {
		t.Fatalf("GetData after a forced close -> %s", hrName(hr))
	}
	defer comRelease(second.data)
	if s2, ok := streamOf(second.data); !ok || !s2.revoked.Load() {
		t.Error("a stream handed out after a forced close was not born revoked")
	}
}
