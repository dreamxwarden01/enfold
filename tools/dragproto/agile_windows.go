//go:build windows

package main

import (
	"fmt"
	"strings"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// -agile: what it changes, and why it is a flag.
//
// Without it, every object this program hands over is apartment-bound. It is
// created on the thread that called OleInitialize, and COM marshals a pointer
// to it back to that apartment for every caller in any other one -- which is
// why the first real 5 GiB drop arrived entirely on one thread id, the reads
// included: Explorer's copy engine ran on a worker thread, but each of its
// 20481 IStream::Read calls was marshalled onto our STA and answered there. In
// the real application that STA is the WebView's UI thread, so the whole copy
// would be pumped through the thread that also has to draw.
//
// With -agile the same objects aggregate the free-threaded marshaler and answer
// IID_IAgileObject, which is the documented way to say "call me on any thread".
// CoCreateFreeThreadedMarshaler "Aggregates this marshaler to the object
// specified by the punkOuter parameter", and when it is asked to marshal into
// the same process it "copies the interface pointer into the marshaling stream"
// instead of building a proxy, so the caller's thread calls straight into us.
//
// It is off by default because it is not free. The documentation is unusually
// blunt about the bargain: "the performance of objects which aggregate the free
// threaded marshaler is obtained through a calculated violation of the rules of
// COM, with the ever-present risk of undefined behavior unless the object
// operates within certain restrictions", and ATL's page on the same option adds
// the duty that follows: "classes must take responsibility for the thread-safety
// of their data". The restrictions are that such an object must not hold direct
// pointers to objects that are not themselves agile, and must not hold proxies
// to objects in other apartments. Nothing here holds any COM pointer at all --
// the files are synthetic, the streams generate their bytes -- so the only duty
// left is thread safety, which is what the mutexes and atomics in this package
// are for and what the -race tests hold it to.
//
// IDropSource is deliberately left apartment-bound: OLE calls it on the thread
// that called DoDragDrop and nowhere else, so agility would buy nothing and
// would only widen the surface that has to be thread-safe.
//
// There is a second thing -agile is quietly the first test of. Without it every
// callback arrives on the thread this program created and the Go runtime owns;
// with it they arrive on threads of the shell's that the runtime has never seen,
// and each one has to be attached to the runtime on its first call in -- the
// same path a cgo callback takes, and not free. If a run with -agile shows a
// cost at the start of a copy that the baseline does not, that is where to look.
//
// One consequence worth naming, because it is not obvious: with -agile,
// GetData runs on the target's thread, so the IStream it hands back is created
// there too. An apartment-bound object created on a stranger's thread would
// belong to that stranger's apartment; this one aggregates the marshaler as it
// is built, so it belongs to no apartment at all and the question does not
// arise. If CoCreateFreeThreadedMarshaler ever failed for such a stream, the
// stream would be bound to whatever apartment that thread was in -- which is
// why that failure is logged rather than shrugged off.

// agileMode is the -agile flag. It is read on every object creation, and one of
// those -- IStream::Clone -- can itself arrive on a thread of Explorer's, so it
// is an atomic rather than a plain bool.
var agileMode atomic.Bool

// dragThreadID is the thread that created the window, called OleInitialize and
// calls DoDragDrop: the apartment every call arrives on without -agile. It stays
// zero in unit tests, where there is no such thread, so a test that cares sets
// it for the duration.
//
// Under -thread it is still the WINDOW's thread; the drag runs on oleThreadID
// instead, and the accounting below has to count both as ours -- the question
// -agile asks is whether a call landed on a thread of this program's or on one
// of the target's, and -thread does not change what that question means.
var dragThreadID atomic.Uint32

// ourThread is that distinction. Zero never matches a real thread id, so a mode
// with no OLE thread needs no extra guard.
func ourThread(tid uint32) bool {
	return tid == dragThreadID.Load() || tid == oleThreadID.Load()
}

// threadKind names a thread for the per-object summary.
func threadKind(tid uint32) string {
	switch {
	case tid == dragThreadID.Load():
		return "drag thread"
	case tid == oleThreadID.Load():
		return "the drag's own OLE thread (-thread)"
	default:
		return "other"
	}
}

// comCalls is the process-wide half of the per-object accounting below.
var comCalls struct {
	home atomic.Int64
	away atomic.Int64
}

// The three IUnknown slots, which every COM interface starts with. They are how
// this package calls out through an interface pointer it holds as an address --
// the aggregated marshaler, and nothing else so far.
const (
	comSlotQueryInterface = 0
	comSlotAddRef         = 1
	comSlotRelease        = 2
)

// ---------------------------------------------------------------------------
// Aggregating the free-threaded marshaler.

// createFreeThreadedMarshaler is a variable so that a test can put a fake inner
// object in ole32's place and check the aggregation rules -- which reference
// count moves, and when the inner object dies -- without COM. The production
// value is the real thing, and a test of that is beside it.
var createFreeThreadedMarshaler = coCreateFreeThreadedMarshaler

// coCreateFreeThreadedMarshaler wraps CoCreateFreeThreadedMarshaler(punkOuter,
// &punkMarshal). punkOuter is documented as "A pointer to the aggregating
// object's controlling IUnknown", which is this object's interface 0, and
// ppunkMarshal receives "the interface pointer to the aggregatable marshaler" --
// the inner object's own, non-delegating IUnknown.
//
// The result comes back as **uintptr rather than an integer because that is
// what a COM interface pointer is: a pointer to a cell holding the vtable's
// address. Receiving it with its real shape means no code here ever has to turn
// an integer back into a pointer.
func coCreateFreeThreadedMarshaler(punkOuter uintptr) (**uintptr, uintptr) {
	var punkMarshal **uintptr
	hr, _, _ := procCoCreateFreeThreadedMarshaler.Call(punkOuter, uintptr(unsafe.Pointer(&punkMarshal)))
	return punkMarshal, hr
}

// aggregateFreeThreadedMarshaler makes this object agile, or leaves it exactly
// as it was.
//
// It runs after the interface cells are in the registry, because punkOuter has
// to be a callable IUnknown before it is handed to anything, and before the
// object's address has left the thread that created it, which is what makes the
// two atomics below a publication rather than a race.
//
// Aggregation's reference-count rule is the whole of the lifetime story here:
// "The aggregable object must not call AddRef when holding a reference to the
// controlling IUnknown pointer" -- so creating the marshaler does not move this
// object's count -- and the single reference the call returns is this object's
// to give back, once, when it dies. ATL says the same thing in the shape of
// code: the marshaler is created in FinalConstruct and released in FinalRelease.
func (o *comObject) aggregateFreeThreadedMarshaler() {
	punk, hr := createFreeThreadedMarshaler(o.unknown())
	if hr != sOK || punk == nil {
		// Not fatal, and not silently half-agile either: an object that answered
		// IID_IAgileObject without being able to marshal itself by value would be
		// making a promise COM would then keep it to.
		logf("%s: CoCreateFreeThreadedMarshaler -> %s; the object stays apartment-bound", o.name, hrName(hr))
		return
	}
	o.marshal.Store(punk)
	o.agile.Store(true)
}

// marshalQueryInterface answers QueryInterface(IID_IMarshal) the way
// CoCreateFreeThreadedMarshaler's Remarks require: "The aggregating object's
// implementation of IMarshal should delegate QueryInterface calls for
// IID_IMarshal to the IUnknown of the free-threaded marshaler."
//
// Delegating means exactly that and nothing more. The inner object's
// non-delegating QueryInterface is what answers, and by aggregation's rules the
// interface it hands back has its IUnknown methods pointing at this object's
// controlling IUnknown -- so the AddRef that goes with a successful
// QueryInterface has already been taken on this object's count by the time the
// call returns. Taking another one here would be the classic aggregation leak.
func (o *comObject) marshalQueryInterface(riid *windows.GUID, ppv *uintptr) uintptr {
	punk := o.marshal.Load()
	if punk == nil {
		return eNoInterface
	}
	qi := unsafe.Slice(*punk, comSlotQueryInterface+1)[comSlotQueryInterface]
	hr, _, _ := syscallPinned(qi, uintptr(unsafe.Pointer(punk)),
		uintptr(unsafe.Pointer(riid)), uintptr(unsafe.Pointer(ppv)))
	return hr
}

// releaseMarshaler gives back the one reference CoCreateFreeThreadedMarshaler
// returned, and is called from exactly one place: this object's own destruction.
// The swap is what makes a second call impossible; the inner object's
// non-delegating Release cannot re-enter this object, so nothing has to be held
// or counted around it.
func (o *comObject) releaseMarshaler() {
	punk := o.marshal.Swap(nil)
	if punk == nil {
		return
	}
	rel := unsafe.Slice(*punk, comSlotRelease+1)[comSlotRelease]
	n, _, _ := syscallPinned(rel, uintptr(unsafe.Pointer(punk)))
	logf("%s: released the aggregated free-threaded marshaler at 0x%X -> %d",
		o.name, uintptr(unsafe.Pointer(punk)), uint32(n))
}

// ---------------------------------------------------------------------------
// Which thread did the call arrive on?
//
// This is the measurement -agile exists for, and the log's per-line thread id
// only answers it for the calls that are logged: a 5 GiB copy is tens of
// thousands of reads and all but a handful of them are throttled away. So every
// call that resolves a `this` is counted here, per object and per thread, and
// the count is printed when the object dies.

type threadTally struct {
	tid   uint32
	calls int64
}

// tally records one call on this object. It runs inside comLookup, which every
// callback that resolves an interface pointer goes through exactly once, so the
// totals are the number of COM method calls the object answered.
func (o *comObject) tally() {
	tid := windows.GetCurrentThreadId()
	if ourThread(tid) {
		o.callsHome.Add(1)
		comCalls.home.Add(1)
	} else {
		o.callsAway.Add(1)
		comCalls.away.Add(1)
	}
	// A mutex rather than an atomic map: the list is a handful of entries, the
	// lock is this object's alone, and it is taken well below the cost of the
	// call that is being counted. It must never be held while anything else is.
	o.tidMu.Lock()
	for i := range o.tids {
		if o.tids[i].tid == tid {
			o.tids[i].calls++
			o.tidMu.Unlock()
			return
		}
	}
	o.tids = append(o.tids, threadTally{tid: tid, calls: 1})
	o.tidMu.Unlock()
}

// threadSummary is the line printed when an object dies, and at exit for one
// that never did. "the drag thread" is the STA that owns the window and called
// DoDragDrop; anything else is a thread of the target's, which without -agile
// should never appear.
func (o *comObject) threadSummary() string {
	home := o.callsHome.Load()
	away := o.callsAway.Load()
	o.tidMu.Lock()
	parts := make([]string, 0, len(o.tids))
	for _, t := range o.tids {
		parts = append(parts, fmt.Sprintf("tid %d (%s): %d", t.tid, threadKind(t.tid), t.calls))
	}
	o.tidMu.Unlock()
	agile := "apartment-bound"
	if o.agile.Load() {
		agile = "agile"
	}
	return fmt.Sprintf("%s, %d call(s): %d on the drag thread, %d on other threads [%s]",
		agile, home+away, home, away, strings.Join(parts, ", "))
}

// comCallSummary is the process-wide version, for the end of a run.
func comCallSummary() string {
	home := comCalls.home.Load()
	away := comCalls.away.Load()
	return fmt.Sprintf("%d COM call(s) in all: %d on the drag thread, %d on other threads",
		home+away, home, away)
}
