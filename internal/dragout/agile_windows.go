//go:build windows

package dragout

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// Agility: why the data object aggregates the free-threaded marshaler.
//
// An apartment-bound object is created on the thread that called
// OleInitialize, and COM marshals a pointer to it back to that apartment
// for every caller in any other one — which is why the prototype's first
// real 5 GiB drop arrived entirely on one thread id, the reads included:
// Explorer's copy engine ran on a worker thread, but each of its calls was
// marshalled onto the STA and answered there. In the application that STA
// is the WebView's UI thread, so the whole extraction would be pumped
// through the thread that also has to draw.
//
// Aggregating the free-threaded marshaler and answering IID_IAgileObject is
// the documented way to say "call me on any thread". CoCreateFreeThreadedMarshaler
// "Aggregates this marshaler to the object specified by the punkOuter
// parameter", and when it is asked to marshal into the same process it
// "copies the interface pointer into the marshaling stream" instead of
// building a proxy, so the caller's thread calls straight into us. Measured
// 2026-09-11: with the object agile, the extraction inside GetData ran on
// Explorer's own thread, 390 of 726 COM calls off the drag thread.
//
// It is not free. The documentation is blunt about the bargain: "the
// performance of objects which aggregate the free threaded marshaler is
// obtained through a calculated violation of the rules of COM, with the
// ever-present risk of undefined behavior unless the object operates within
// certain restrictions", and ATL's page adds the duty that follows: "classes
// must take responsibility for the thread-safety of their data". The
// restrictions are that such an object must not hold direct pointers to
// objects that are not themselves agile, and must not hold proxies to
// objects in other apartments. Nothing here holds any COM pointer at all —
// the data object hands out paths — so the only duty left is thread safety,
// which is what the mutexes and atomics in this package are for and what
// the -race tests hold it to.
//
// IDropSource is deliberately left apartment-bound: OLE calls it on the
// thread that called DoDragDrop and nowhere else, so agility would buy
// nothing and would only widen the surface that has to be thread-safe.

// The three IUnknown slots, which every COM interface starts with. They are
// how this package calls out through an interface pointer it holds as an
// address — the aggregated marshaler, and the source's own calls on its
// async capability.
const (
	comSlotQueryInterface = 0
	comSlotAddRef         = 1
	comSlotRelease        = 2
)

// createFreeThreadedMarshaler is a variable so that a test can put a fake
// inner object in ole32's place and check the aggregation rules — which
// reference count moves, and when the inner object dies — without COM. The
// production value is the real thing, and a test of that is beside it.
var createFreeThreadedMarshaler = coCreateFreeThreadedMarshaler

// coCreateFreeThreadedMarshaler wraps CoCreateFreeThreadedMarshaler(punkOuter,
// &punkMarshal). punkOuter is documented as "A pointer to the aggregating
// object's controlling IUnknown", which is this object's interface 0, and
// ppunkMarshal receives "the interface pointer to the aggregatable
// marshaler" — the inner object's own, non-delegating IUnknown.
//
// The result comes back as **uintptr rather than an integer because that is
// what a COM interface pointer is: a pointer to a cell holding the vtable's
// address. Receiving it with its real shape means no code here ever has to
// turn an integer back into a pointer.
func coCreateFreeThreadedMarshaler(punkOuter uintptr) (**uintptr, uintptr) {
	var punkMarshal **uintptr
	hr, _, _ := procCoCreateFreeThreadedMarshaler.Call(punkOuter, uintptr(unsafe.Pointer(&punkMarshal)))
	return punkMarshal, hr
}

// aggregateFreeThreadedMarshaler makes this object agile, or leaves it
// exactly as it was.
//
// It runs after the interface cells are in the registry, because punkOuter
// has to be a callable IUnknown before it is handed to anything, and before
// the object's address has left the thread that created it, which is what
// makes the two atomics below a publication rather than a race.
//
// Aggregation's reference-count rule is the whole of the lifetime story
// here: "The aggregable object must not call AddRef when holding a
// reference to the controlling IUnknown pointer" — so creating the
// marshaler does not move this object's count — and the single reference
// the call returns is this object's to give back, once, when it dies. ATL
// says the same thing in the shape of code: the marshaler is created in
// FinalConstruct and released in FinalRelease.
func (o *comObject) aggregateFreeThreadedMarshaler() {
	punk, hr := createFreeThreadedMarshaler(o.unknown())
	if hr != sOK || punk == nil {
		// Not fatal, and not silently half-agile either: an object that
		// answered IID_IAgileObject without being able to marshal itself by
		// value would be making a promise COM would then keep it to.
		o.log("drag: %s: CoCreateFreeThreadedMarshaler -> %s; the object stays apartment-bound and the extraction will run on the window's thread", o.name, hrName(hr))
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
// non-delegating QueryInterface is what answers, and by aggregation's rules
// the interface it hands back has its IUnknown methods pointing at this
// object's controlling IUnknown — so the AddRef that goes with a successful
// QueryInterface has already been taken on this object's count by the time
// the call returns. Taking another one here would be the classic
// aggregation leak.
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
// returned, and is called from exactly one place: this object's own
// destruction. The swap is what makes a second call impossible; the inner
// object's non-delegating Release cannot re-enter this object, so nothing
// has to be held or counted around it.
func (o *comObject) releaseMarshaler() {
	punk := o.marshal.Swap(nil)
	if punk == nil {
		return
	}
	rel := unsafe.Slice(*punk, comSlotRelease+1)[comSlotRelease]
	syscallPinned(rel, uintptr(unsafe.Pointer(punk)))
}
