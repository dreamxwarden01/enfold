//go:build windows

package dragout

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The drag itself: OLE on the caller's thread, one native drag at a time,
// and the teardown at the end of the process.

var (
	// oleMu guards the OLE bookkeeping; oleThread is the thread that called
	// OleInitialize — the one DoDragDrop and OleUninitialize belong to — and
	// oleReady whether that call succeeded.
	oleMu     sync.Mutex
	oleThread uint32
	oleReady  bool
	oleLog    func(string, ...any) = discard

	// running is the one drag at a time per process: set by Begin, cleared
	// when Run returns. Two cannot run at once — DoDragDrop is modal on its
	// thread — and a second Begin while one runs is ErrBusy.
	running atomic.Bool
)

// InitOLE is OleInitialize(NULL) on the CALLING thread, which must be the
// thread that runs the window's message loop and will call Run: DoDragDrop
// "must be called from the thread that owns the window" and needs an OLE
// apartment there — Wails never calls OleInitialize itself (APP.md §3). The
// HRESULT is checked; S_FALSE — the apartment was already initialised — is
// a success that still has to be balanced by UninitOLE, so it is counted as
// one. RPC_E_CHANGED_MODE is the thread being an MTA, which no drag can run
// from: an error, and Begin then answers ErrUnsupported.
func InitOLE() error {
	hr, _, _ := procOleInitialize.Call(0)
	if failed(hr) {
		return fmt.Errorf("dragout: OleInitialize: %s", hrName(hr))
	}
	oleMu.Lock()
	oleThread = windows.GetCurrentThreadId()
	oleReady = true
	oleMu.Unlock()
	return nil
}

// UninitOLE is the end of the process's use of OLE, on the thread that
// called InitOLE, after everything else. Every staging folder still
// registered is decided about — a folder nothing was handed out of goes,
// one whose paths a target has is left manifested for the next launch's
// scavenge, because consumers open dropped paths late — and then the
// apartment is closed, unless the target still holds a data object of ours.
//
// That last rule is a finding turned into behaviour (tools/dragproto,
// "Findings so far"): OleUninitialize "Closes the COM library on the
// apartment, releases any class factories, other COM objects, or servers
// held by the apartment" — in the prototype's second real drop, that one
// call took the data object from five references to zero under a target
// that was still using it, and Explorer's progress dialog could not even be
// cancelled afterwards. So a data object the target still references is
// never destroyed by us: it is revoked instead — from here on its GetData
// answers E_UNEXPECTED rather than name files out of a drag that is over —
// and the apartment is left alone, for process exit to end the transfer in
// a way the target's own RPC layer understands.
func UninitOLE() {
	oleMu.Lock()
	ready := oleReady
	oleReady = false
	log := oleLog
	oleMu.Unlock()
	closeAllStages()
	if !ready {
		return
	}
	if held := revokeLiveDataObjects(); held > 0 {
		log("drag: skipping OleUninitialize: the target still holds %d data object(s), and the call would take them away from it", held)
		return
	}
	procOleUninitialize.Call()
}

// revokeLiveDataObjects is the forced teardown's half over the objects:
// every data object the target still holds answers E_UNEXPECTED from here
// on. It returns how many there were.
func revokeLiveDataObjects() int {
	held := 0
	for _, o := range liveComObjects() {
		if d, ok := o.impl.(*dataObject); ok {
			d.revoked.Store(true)
			held++
		}
	}
	return held
}

// Drag is one native drag, from Begin to the end of its DoDragDrop; the
// staging folder it made outlives it as the watch and the scavenge decide.
type Drag struct {
	opts Options
	s    *stage
	log  func(string, ...any)
	ran  atomic.Bool
}

// Begin makes the staging folder atomically with its manifest and builds
// what the drag needs, before DoDragDrop: a target that asks for CF_HDROP
// during the hover is answered with the final paths, and they exist from
// here. It answers ErrUnsupported when InitOLE did not succeed on this
// process and ErrBusy while another drag is running; Run must follow on
// the thread that called InitOLE.
func Begin(opts Options) (*Drag, error) {
	oleMu.Lock()
	ready := oleReady
	if opts.Log != nil {
		oleLog = opts.Log
	}
	oleMu.Unlock()
	if !ready {
		return nil, ErrUnsupported
	}
	if !running.CompareAndSwap(false, true) {
		return nil, ErrBusy
	}
	s, err := newStage(opts, ScavengeAge)
	if err != nil {
		running.Store(false)
		return nil, err
	}
	d := &Drag{opts: opts, s: s, log: s.log}
	d.log("drag %s: staging folder made for %d item(s), %d at the top", s.id, len(s.paths), len(s.drop))
	return d, nil
}

// Run is DoDragDrop on the calling thread — the one that called InitOLE,
// which runs the window's message loop; DoDragDrop pumps messages itself,
// so the window stays alive for as long as the button is down. It returns
// when DoDragDrop returns, which for a target that negotiated the
// asynchronous protocol is before the extraction: the data object stays
// alive on the reference the negotiation took, and Options.OnPhase says how
// the drag ends. A self-drop's folder is gone by the time Run returns.
func (d *Drag) Run() (Result, error) {
	if !d.ran.CompareAndSwap(false, true) {
		return Result{}, fmt.Errorf("dragout: Run called twice")
	}
	defer running.Store(false)
	s := d.s
	if tid := windows.GetCurrentThreadId(); tid != oleThreadID() {
		// The apartment is the thread's; a drag from anywhere else would be
		// RPC_E_WRONG_THREAD at best and a hang at worst.
		err := fmt.Errorf("dragout: Run on thread %d, but OLE was initialised on thread %d", tid, oleThreadID())
		s.reportDone(Failed)
		s.remove()
		return Result{Folder: s.root}, err
	}

	dataObj := newHDropDataObject(s, d.log)
	dobj := dataObj.impl.(*dataObject)
	srcObj := newDropSourceObject(s, d.opts.Window, d.log)
	asyncPtr := dataObj.ifaceAddr("IDataObjectAsyncCapability")

	// Step 2 of the documented source procedure: "Call SetAsyncMode with
	// fDoOpAsync set to VARIANT_TRUE to indicate that an asynchronous
	// operation is supported."
	syscallPinned(asyncVtbl.SetAsyncMode, asyncPtr, variantTrue)

	var effect uint32 = dropEffectNone
	start := time.Now()
	hr, _, _ := procDoDragDrop.Call(dataObj.unknown(), srcObj.unknown(),
		uintptr(allowedEffects), uintptr(unsafe.Pointer(&effect)))
	took := time.Since(start)

	// Step 3: "After DoDragDrop returns, call InOperation." Through
	// syscallPinned: pfInAsyncOp is an out-parameter, and syscall.SyscallN
	// would leave it wherever escape analysis put it.
	var inOp uint32
	syscallPinned(asyncVtbl.InOperation, asyncPtr, uintptr(unsafe.Pointer(&inOp)))

	note := ""
	if inOp != 0 {
		// For an asynchronous drop the effect out-parameter is
		// DROPEFFECT_NONE whatever the target goes on to do: a drop that
		// moved a 5 GiB file to the desktop reported NONE (measured
		// 2026-09-11). What the target did is learnt from the files.
		note = " (asynchronous: the effect is not reported here)"
	}
	d.log("drag %s: DoDragDrop returned %s after %s, effect %s%s", s.id, hrName(hr), took.Round(time.Millisecond), effectName(effect), note)

	// "If the return value is DRAGDROP_S_DROP, DoDragDrop calls
	// IDropTarget::Drop ... The DoDragDrop function returns the last effect
	// code to the source"; DRAGDROP_S_CANCEL is the cancel, and anything
	// failed is a drag that could not be run at all.
	var end Reason
	var err error
	switch {
	case uint32(hr) == dragDropSCancel:
		end = Cancelled
	case failed(hr):
		end = Failed
		err = fmt.Errorf("dragout: DoDragDrop: %s", hrName(hr))
	}
	s.noteDragEnded(end)

	if inOp == 0 {
		dobj.finishSync()
	}
	// Step 4: "Release the data object." Ours, not the target's: if the
	// target is still extracting it holds references of its own, and the
	// object lives on.
	srcObj.release()
	dataObj.release()

	s.mu.Lock()
	res := Result{SelfDrop: s.selfDrop, Extracted: s.extracted && !s.failed, Effect: effect, Folder: s.root}
	s.mu.Unlock()
	return res, err
}

// Cancel ends the drag from any thread: a running extraction returns at its
// next chunk with the context's error, so the drop's GetData fails and the
// target abandons the drop, and a drag still in the hover ends at the
// source's next QueryContinueDrag as Escape would.
func (d *Drag) Cancel() {
	d.s.cancel()
}

func oleThreadID() uint32 {
	oleMu.Lock()
	defer oleMu.Unlock()
	return oleThread
}
