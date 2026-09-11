//go:build windows

// Command dragproto is the stand-alone prototype APP.md §3 and DECISIONS.md
// ("Drag out, researched") call for: a window that drags synthetic virtual
// files -- by default one of 5 GiB -- onto the desktop or an Explorer folder,
// with a pure-Go, cgo-free OLE drag source behind it.
//
// It is here to answer the questions the design cannot answer on paper, before
// a line of this enters the shell:
//
//   - does Explorer take FileGroupDescriptorW plus per-file FileContents
//     IStreams from a source like this at all;
//   - does a 5 GiB file stay bounded in memory, or does something buffer it;
//   - does Explorer put up its own Replace or Skip Files dialog on a collision;
//   - what does Esc mid-drag do, and what does DoDragDrop return;
//   - does IDataObjectAsyncCapability actually move the reads off the UI
//     thread, and on which thread do they then arrive;
//   - it does not, as the first real 5 GiB drop showed -- so does making the
//     objects agile (-agile) move them, and what does that cost;
//   - what does a slow producer (-delay) do to the window's responsiveness.
//
// Every COM call is logged to stderr with its thread id. That log is the
// result; the window is only the handle.
//
// Nothing here talks to a vault, a key or a real archive. The bytes are
// generated from a counter, and their SHA-256 is printed at start so the
// dropped file can be checked against it.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// hashSyncLimit is the size up to which a file's SHA-256 is computed before the
// window opens. Anything larger waits for -hash and is computed on a goroutine,
// because 5 GiB of generator takes long enough that doing it up front would
// look like a hang.
const hashSyncLimit = 64 << 20

type appState struct {
	files []synthFile
	write time.Time

	cxDrag, cyDrag int32
	streamAtEnd    bool

	// -hdrop: the staged route. stageParent is the drag root every staging
	// folder is made under, and stageCfg is the rest of the mode's flags. Both
	// are set once, before the window exists.
	hdrop       bool
	stageParent string
	stageCfg    stageConfig

	// The gesture. Only the window thread touches these: WM_LBUTTONDOWN arms
	// the drag, the first WM_MOUSEMOVE past the system threshold starts it, and
	// DoDragDrop runs its own modal loop until the button comes up.
	armed    bool
	dragging bool
	start    point
	drags    int
}

var app appState

func main() {
	// DoDragDrop, OleInitialize and every COM object below belong to one
	// thread's apartment. Pinning the goroutine to the OS thread first is what
	// makes "the thread that called OleInitialize" and "the thread running the
	// message loop" the same thread for the whole run.
	runtime.LockOSThread()

	var (
		count  = flag.Int("n", 2, "number of virtual files to offer")
		size   = sizeFlag(5 << 30)
		delay  = flag.Int("delay", 0, "milliseconds the producer takes per MiB, to simulate a slow decrypt")
		folder = flag.Bool("folder", false, `put the files under a relative folder "Proto\" in the descriptor`)
		hash   = flag.Bool("hash", false, "also compute the SHA-256 of files larger than 64 MiB")
		// IDataObject::GetData's Notes to Callers define the data on a stream
		// medium as everything before the seek pointer the provider leaves
		// behind, so a provider handing over a whole file should leave it at
		// the end -- which is what Microsoft's own virtual-file sample does.
		// The obvious reading is the opposite: hand it over ready to read. Which
		// one Explorer means is unknown, and a wrong choice shows up as a
		// zero-byte file at the destination, so it is a flag.
		//
		// The default is "start" because that is the reading a consumer written
		// against IStream would have to hold, and because a run that produces
		// files is the run worth having first. "end" is then the experiment: it
		// is what the documentation and the sample say, and whether Explorer
		// still produces whole files that way is the thing to report.
		streamAt = flag.String("streamat", "start",
			`where a file's stream is positioned when it is handed over: "start" (the default) or "end" (the documented convention)`)
		// Off by default on purpose: the apartment-bound run is the baseline
		// every earlier experiment was measured against, and the point of the
		// flag is to be able to compare the two in one sitting. See
		// agile_windows.go for what it changes and what it costs.
		agile = flag.Bool("agile", false,
			"aggregate the free-threaded marshaler and answer IID_IAgileObject on the data object, its enumerator and every stream, so the target can call them on its own threads")

		// The staged route, which is the one the design ruled for. It is a mode
		// rather than a replacement because the virtual-file mode is the record
		// of what the other route does, and the two are only comparable if both
		// can still be run. See hdrop_windows.go.
		hdrop = flag.Bool("hdrop", false,
			"offer CF_HDROP by delayed rendering over a staging folder (the staged route) instead of virtual files")
		stageEarly = flag.Bool("stage-early", false,
			"with -hdrop: write the staged files at drag start instead of inside the drop's own GetData")
		keep = flag.Bool("keep", false,
			"with -hdrop: never delete the staging folder, so it can be inspected afterwards")
		failExtract = flag.Bool("fail-extract", false,
			"with -hdrop: make the extraction fail on purpose, to see what the target does with a GetData that fails")
		copyOnly = flag.Bool("copy-only", false,
			"with -hdrop: allow DROPEFFECT_COPY only and prefer copy, instead of allowing copy and move and preferring move")
		scavenge = flag.Duration("scavenge", time.Hour,
			"with -hdrop: at launch and every ten minutes, delete manifested staging folders older than this")
	)
	flag.Var(&size, "size", "size of the first file, e.g. 5GiB, 256MiB, 1048576")
	flag.Parse()

	// Before anything is created: every object asks agileMode what it is, and
	// every call asks dragThreadID which thread it arrived on.
	agileMode.Store(*agile)
	dragThreadID.Store(windows.GetCurrentThreadId())

	switch *streamAt {
	case "end", "start":
	default:
		fmt.Fprintf(os.Stderr, "dragproto: -streamat must be %q or %q, not %q\n", "end", "start", *streamAt)
		os.Exit(2)
	}
	streamAtEnd := *streamAt == "end"

	app.write = time.Now()
	app.streamAtEnd = streamAtEnd
	app.files = buildFileSet(fileSetConfig{
		count:  *count,
		size:   int64(size),
		delay:  time.Duration(*delay) * time.Millisecond,
		folder: *folder,
	})

	app.hdrop = *hdrop
	if app.hdrop {
		root, err := stagingRoot()
		if err != nil {
			fmt.Fprintf(os.Stderr, "dragproto: -hdrop needs a staging folder: %v\n", err)
			os.Exit(1)
		}
		app.stageParent = root
		app.stageCfg = stageConfig{
			keep:        *keep,
			early:       *stageEarly,
			failExtract: *failExtract,
			copyOnly:    *copyOnly,
			maxAge:      *scavenge,
		}
	}

	if app.hdrop {
		fmt.Printf("dragproto: -hdrop, the staged route: %d file(s) extracted into a staging folder at drop time\n", len(app.files))
	} else {
		fmt.Printf("dragproto: %d virtual file(s)\n", len(app.files))
	}
	for i, f := range app.files {
		fmt.Printf("  [%d] %-40s %14d bytes\n", i, f.name, f.size)
	}
	if app.hdrop {
		fmt.Printf("  staging under %s\\<random 8 hex>\n", app.stageParent)
		fmt.Printf("  effects: allowed %s, preferred %s\n",
			effectName(app.stageCfg.allowedEffects()), effectName(app.stageCfg.preferredEffect()))
		if *stageEarly {
			fmt.Printf("  -stage-early: the files are written before the drag, not inside the drop's own GetData\n")
		} else {
			fmt.Printf("  the extraction runs inside the first GetData(CF_HDROP) after the button is released\n")
		}
		fmt.Printf("  scavenge: manifested folders older than %s, at launch and every %s\n", *scavenge, scavengeInterval)
		if *keep {
			fmt.Printf("  -keep: no staging folder of this run will be deleted\n")
		}
		if *failExtract {
			fmt.Printf("  -fail-extract: the extraction will fail on purpose and GetData will return E_UNEXPECTED\n")
		}
	} else {
		fmt.Printf("  file streams are handed over with their seek pointer at the %s\n", *streamAt)
	}
	if *agile {
		fmt.Printf("  -agile: the data object, its enumerator and every stream are agile;\n" +
			"          the target may call them on threads of its own\n")
	} else {
		fmt.Printf("  the objects are apartment-bound: every call is marshalled onto this thread\n")
	}
	if *delay > 0 {
		fmt.Printf("  producer delay: %d ms/MiB (a 5 GiB file would take about %s)\n",
			*delay, time.Duration(*delay)*time.Millisecond*time.Duration(int64(size)>>20))
	}
	printHashes(app.files, *hash)
	fmt.Println("\nDrag from the window's client area. The COM log is on stderr.")

	// "You must call OleInitialize before calling this function." S_FALSE means
	// the apartment was already initialised, and is still a success that must be
	// balanced by OleUninitialize.
	hr, _, _ := procOleInitialize.Call(0)
	logf("OleInitialize(NULL) -> %s", hrName(hr))
	if int32(uint32(hr)) < 0 {
		fmt.Fprintf(os.Stderr, "dragproto: OleInitialize failed: %s\n", hrName(hr))
		os.Exit(1)
	}
	defer func() {
		// OleUninitialize is not a formality here. It "Closes the COM library on
		// the apartment, releases any class factories, other COM objects, or
		// servers held by the apartment, disables RPC on the apartment" -- so on
		// a run where the target still holds this program's data object and its
		// streams, this one call is what takes them away from it. That is
		// exactly what happened in the second real drop: the window was closed
		// while Explorer's copy was stalled, this call released the data object
		// from five references to zero, the streams were destroyed under it, and
		// Explorer's progress dialog could not even be cancelled afterwards.
		//
		// So an exit with anything still outstanding does not pretend to be an
		// orderly shutdown. It leaves the apartment alone and lets process exit
		// end the transfer, which at least ends it in a way the target's own RPC
		// layer understands. The test is the registry rather than how the close
		// was decided: if nothing is left, there is nothing to take away and the
		// ordinary call is right.
		if live := liveComObjects(); len(live) > 0 {
			logf("skipping OleUninitialize: the target still holds %d object(s) (forced close: %v), and this call is what would take them away",
				len(live), closeForced.Load())
			return
		}
		procOleUninitialize.Call()
		logf("OleUninitialize")
	}()
	logf("thread %d is the drag thread: it owns the window, the apartment and DoDragDrop; -agile=%v",
		dragThreadID.Load(), agileMode.Load())

	app.cxDrag = getSystemMetrics(smCXDrag)
	app.cyDrag = getSystemMetrics(smCYDrag)
	logf("drag threshold: SM_CXDRAG=%d SM_CYDRAG=%d", app.cxDrag, app.cyDrag)
	registerFormats()
	if app.hdrop {
		logf("-hdrop: staging under %s; effects allowed %s, preferred %s",
			app.stageParent, effectName(app.stageCfg.allowedEffects()), effectName(app.stageCfg.preferredEffect()))
		// The sweep is the cleanup policy, not a backstop: it is what removes
		// the folders of drags whose consumers took the paths away with them.
		// It runs on a goroutine because one pass can sit in a bounded backoff
		// for over a minute, and the window must not wait for it.
		startScavenger(app.stageParent, app.stageCfg.maxAge)
	}

	hwnd := createWindow()
	runMessageLoop()
	_ = hwnd

	if peak, ok := peakWorkingSet(); ok {
		logf("peak working set: %d bytes (%.1f MiB)", peak, float64(peak)/(1<<20))
	} else {
		logf("peak working set: GetProcessMemoryInfo failed")
	}
	logf("%d drag(s) started; %d COM interface cells still registered", app.drags, comRegistrySize())
	logf("%s", comCallSummary())
	// A staging folder still here at exit is not a leak: a folder whose paths
	// were handed out is meant to outlive the process, and the next launch's
	// sweep is what removes it. Saying which ones, and why, is the difference
	// between that and forgetting.
	for _, s := range remainingStages() {
		logf("staging folder left behind: %s", s.summary())
	}
	// An object still alive here is one the target never let go of. Its own
	// summary line is printed when it dies, and it is not going to die, so it is
	// printed here instead -- with the reference count that explains why.
	for _, o := range liveComObjects() {
		logf("still held by the target at exit: %s (refs=%d): %s", o.name, o.refs.Load(), o.threadSummary())
	}
}

// printHashes streams the same generator the streams use through SHA-256, so
// the user has something to compare Get-FileHash against.
func printHashes(files []synthFile, wantBig bool) {
	fmt.Println("\nexpected SHA-256 of each file:")
	for i := range files {
		f := files[i]
		if f.size > hashSyncLimit && !wantBig {
			fmt.Printf("  [%d] %-40s (not computed; pass -hash)\n", i, f.name)
			continue
		}
		if f.size > hashSyncLimit {
			idx := i
			fmt.Printf("  [%d] %-40s computing...\n", idx, f.name)
			go func() {
				start := time.Now()
				sum := fileSHA256(&f)
				fmt.Printf("  [%d] %-40s %s  (%s)\n", idx, f.name, sum, time.Since(start).Round(time.Second))
			}()
			continue
		}
		fmt.Printf("  [%d] %-40s %s\n", i, f.name, fileSHA256(&f))
	}
}

// fileSHA256 hashes a synthetic file without going through a stream or a
// producer: the generator is the definition of the file's contents, and hashing
// it directly means -delay does not slow the hash down.
func fileSHA256(f *synthFile) string {
	h := sha256.New()
	buf := make([]byte, 1<<20)
	for off := int64(0); off < f.size; {
		n := int64(len(buf))
		if r := f.size - off; r < n {
			n = r
		}
		patternAt(f.seed, off, buf[:n])
		h.Write(buf[:n])
		off += n
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ---------------------------------------------------------------------------
// The window.

const windowClass = "EnfoldDragProtoWindow"
const windowTitle = "Enfold drag-out prototype - drag from this window"

var wndProcCallback = syscall.NewCallback(wndProc)

func createWindow() windows.Handle {
	hinst, _, _ := procGetModuleHandleW.Call(0)
	cls, err := windows.UTF16PtrFromString(windowClass)
	if err != nil {
		panic(err)
	}
	title, err := windows.UTF16PtrFromString(windowTitle)
	if err != nil {
		panic(err)
	}
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	brush, _, _ := procGetSysColorBrush.Call(colorWindow)

	wc := wndClassExW{
		style:         csHRedraw | csVRedraw,
		lpfnWndProc:   wndProcCallback,
		hInstance:     windows.Handle(hinst),
		hCursor:       windows.Handle(cursor),
		hbrBackground: windows.Handle(brush),
		lpszClassName: cls,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	atom, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		fmt.Fprintf(os.Stderr, "dragproto: RegisterClassExW failed: %v\n", err)
		os.Exit(1)
	}
	h, _, err := procCreateWindowExW.Call(
		0, uintptr(atom), uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow,
		cwUseDefault, cwUseDefault, 620, 300,
		0, 0, hinst, 0)
	if h == 0 {
		fmt.Fprintf(os.Stderr, "dragproto: CreateWindowExW failed: %v\n", err)
		os.Exit(1)
	}
	procShowWindow.Call(h, swShowNormal)
	procUpdateWindow.Call(h)
	mainHWND.Store(h)
	logf("window 0x%X created on this thread", h)
	return windows.Handle(h)
}

func runMessageLoop() {
	var m msgW
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// lParamPoint unpacks a mouse message's client coordinates. Both halves are
// signed: a drag that leaves the window to the left gives a negative x.
func lParamPoint(lParam uintptr) point {
	return point{x: int32(int16(uint32(lParam))), y: int32(int16(uint32(lParam) >> 16))}
}

func wndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmLButtonDown:
		app.armed = true
		app.start = lParamPoint(lParam)
		procSetCapture.Call(hwnd)
		return 0

	case wmMouseMove:
		if !app.armed || app.dragging || uint32(wParam)&mkLButton == 0 {
			return 0
		}
		p := lParamPoint(lParam)
		if abs32(p.x-app.start.x) <= app.cxDrag && abs32(p.y-app.start.y) <= app.cyDrag {
			return 0
		}
		app.armed = false
		// DoDragDrop takes the mouse itself; holding capture as well would stop
		// it from ever seeing the cursor leave the window.
		procReleaseCapture.Call()
		startDrag()
		return 0

	case wmLButtonUp:
		app.armed = false
		procReleaseCapture.Call()
		return 0

	case wmPaint:
		paint(hwnd)
		return 0

	case wmClose:
		if !mayClose() {
			// Swallowed: DefWindowProc is what would call DestroyWindow, and
			// not calling it is the whole of "do not tear anything down".
			return 0
		}
		// Break out to DefWindowProc below, which destroys the window.

	case wmDestroy:
		// The window can also be destroyed without a WM_CLOSE of ours -- a task
		// manager, a shutdown -- and a transfer can still be live even after a
		// close that waited politely, because EndOperation is not the target
		// letting go. Nothing can be deferred from here, so say what is being
		// taken away and make the exit path treat it as forced. The
		// closeForced test is only so that a close that already said all this
		// does not say it twice.
		if live := transfersInFlight(); len(live) > 0 && !closeForced.Load() {
			closeForced.Store(true)
			logf("WM_DESTROY with a transfer still in flight; the target is about to lose these:")
			logTransferInventory()
			revokeLiveStreams()
			if s := currentStage.Load(); s != nil {
				s.noteForcedClose()
			}
		}
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return r
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func paint(hwnd uintptr) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc != 0 {
		font, _, _ := procGetStockObject.Call(defaultGUIFnt)
		procSelectObject.Call(hdc, font)
		procSetBkMode.Call(hdc, transparent)
		var rc rect
		procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		rc.left += 16
		rc.top += 16
		rc.right -= 16
		rc.bottom -= 16
		if text, err := windows.UTF16FromString(windowText()); err == nil && len(text) > 1 {
			procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)-1),
				uintptr(unsafe.Pointer(&rc)), dtLeft|dtTop|dtWordBreak|dtExpandTabs|dtNoPrefix)
		}
		procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	}
}

func windowText() string {
	var b strings.Builder
	b.WriteString("Press the left mouse button here and drag onto the desktop or an Explorer folder.\r\n")
	b.WriteString("Esc while dragging cancels. Every COM call is logged to stderr.\r\n")
	b.WriteString("A close while a drop is still being served is refused once; close again to force it.\r\n\r\n")
	if app.hdrop {
		b.WriteString("-hdrop: the drag offers CF_HDROP over a staging folder, written at drop time:\r\n")
	} else {
		b.WriteString("The drag offers:\r\n")
	}
	for i, f := range app.files {
		fmt.Fprintf(&b, "    [%d]  %s   -   %s\r\n", i, f.name, humanSize(f.size))
	}
	if app.hdrop {
		fmt.Fprintf(&b, "\r\nStaging under %s\r\n", app.stageParent)
		fmt.Fprintf(&b, "Effects: allowed %s, preferred %s\r\n",
			effectName(app.stageCfg.allowedEffects()), effectName(app.stageCfg.preferredEffect()))
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// Closing the window while a drop is still being served.
//
// This is a finding turned into behaviour. In the second real drop, Explorer
// took the file, asked about the collision, was told to replace, called
// StartOperation and asked for the contents stream -- and then read nothing at
// all for eighteen seconds, because the destination file was held open by
// something else and its copy engine was stuck before its first read. The window
// was closed during that pause. Closing it ran OleUninitialize, which released
// the data object from five references to zero and destroyed the streams under a
// target that was still holding them; Explorer's progress dialog was then left
// in a state where even Cancel did nothing.
//
// The lesson is not about Explorer. A source that offers virtual files has no
// idea when the target will get round to reading them -- a dialog, a locked
// destination, a slow disk -- and the only thing it is told is EndOperation. So
// the window does not go away while a transfer is live; it says so, and waits.
// A second close forces it, with an inventory of everything being taken away.

var (
	// mainHWND is the window, kept so that a thread of the target's can post to
	// it. PostMessageW "Places (posts) a message in the message queue associated
	// with the thread that created the specified window and returns without
	// waiting for the thread to process the message" -- which is the only safe
	// way to reach a window from a callback that may be running anywhere.
	mainHWND atomic.Uintptr

	// closeDeferred is set by the first close that found a transfer in flight,
	// closeForced by the second one (or by a destroy that could not be stopped),
	// and endOpAfterClose by an EndOperation that arrived while a close was
	// waiting for it.
	closeDeferred   atomic.Bool
	closeForced     atomic.Bool
	endOpAfterClose atomic.Bool

	// transferRevoked makes every stream created after a forced teardown be born
	// revoked, so that a target that asks for one more file gets an answer
	// rather than bytes from a transfer that is over.
	transferRevoked atomic.Bool
)

// transfersInFlight returns the data objects the target is still holding: our
// own reference is dropped at the end of every drag, so a data object that is
// still alive is one the target has not let go of. An asynchronous operation it
// started and has not ended is the stronger case, and is reported per object.
func transfersInFlight() []*comObject {
	var live []*comObject
	for _, o := range liveComObjects() {
		if _, ok := o.impl.(*dataObject); ok {
			live = append(live, o)
		}
	}
	return live
}

// logTransferInventory says what is still out there, in the words that matter
// for each kind: whether the operation is still running, and how far each
// stream got. Nothing here takes a stream's mutex -- a stream that is blocked
// mid-read is precisely the case this is printed for.
func logTransferInventory() {
	for _, o := range liveComObjects() {
		switch impl := o.impl.(type) {
		case *dataObject:
			logf("    %s refs=%d, InOperation=%v, %d GetData call(s)",
				o.name, o.refs.Load(), impl.inOperation(), impl.getCalls.Load())
		case *fileStream:
			logf("    %s refs=%d, %d read(s), %d of %d bytes served",
				o.name, o.refs.Load(), impl.reads.Load(), impl.served.Load(), impl.file.size)
		default:
			logf("    %s refs=%d", o.name, o.refs.Load())
		}
	}
}

// revokeLiveStreams is the second half of a forced teardown: from here on every
// stream the target still holds answers STG_E_REVERTED instead of serving bytes
// out of a transfer that is being dismantled. It takes no stream's mutex, so it
// cannot be held up by a read that is waiting on a producer.
func revokeLiveStreams() {
	transferRevoked.Store(true)
	for _, o := range liveComObjects() {
		if s, ok := o.impl.(*fileStream); ok {
			s.revoke()
		}
	}
}

// stagedFilesInUse is the -hdrop half of the close guard: a staging folder whose
// files somebody else has open right now. The COM half cannot see this at all --
// a CF_HDROP target holds no object of ours, it holds paths -- so the two are
// asked separately and either one defers a close.
func stagedFilesInUse() []string {
	s := currentStage.Load()
	if s == nil {
		return nil
	}
	return s.inUseNow()
}

// mayClose decides what a WM_CLOSE does. It runs on the window thread only.
func mayClose() bool {
	live := transfersInFlight()
	busy := stagedFilesInUse()
	switch {
	case len(live) == 0 && len(busy) == 0:
		// Nothing is being served and nothing is being read. The staging folder,
		// if there is one, decides its own fate: a folder whose paths were handed
		// out is deliberately left behind for the scavenge, because a consumer
		// may open them long after this process has gone.
		if s := currentStage.Load(); s != nil {
			s.closeQuietly()
		}
		return true

	case endOpAfterClose.Load() && len(busy) == 0:
		// This WM_CLOSE is the one EndOperation posted. The operation is over;
		// that the target has not finished letting go is its own business.
		logf("the drop operation ended after the deferred close; closing now")
		if s := currentStage.Load(); s != nil {
			s.closeQuietly()
		}
		return true

	case !closeDeferred.Load():
		closeDeferred.Store(true)
		logf("close requested while a drop operation is in flight -- waiting for EndOperation (close again to force)")
		logTransferInventory()
		for _, p := range busy {
			logf("    staged file still open by another process: %s", p)
		}
		return false

	default:
		closeForced.Store(true)
		logf("CLOSED AGAIN: forcing the exit while the target still holds these")
		logTransferInventory()
		for _, p := range busy {
			logf("    staged file still open by another process: %s", p)
		}
		revokeLiveStreams()
		if s := currentStage.Load(); s != nil {
			// The forced path is the only one that calls
			// MOVEFILE_DELAY_UNTIL_REBOOT, and it calls it to record what an
			// unelevated process gets rather than to rely on it.
			s.noteForcedClose()
		}
		return true
	}
}

// dropOperationEnded is called from EndOperation, on whatever thread it arrived
// on. If a close is waiting for it, the window is told by posting to it.
func dropOperationEnded() {
	if !closeDeferred.Load() || endOpAfterClose.Swap(true) {
		return
	}
	h := mainHWND.Load()
	if h == 0 {
		return
	}
	logf("EndOperation arrived after a deferred close; posting WM_CLOSE to the window")
	procPostMessageW.Call(h, wmClose, 0, 0)
}

// ---------------------------------------------------------------------------
// The drag.

func startDrag() {
	if app.dragging {
		logf("a drag is already running; ignoring the gesture")
		return
	}
	app.dragging = true
	app.drags++
	defer func() { app.dragging = false }()

	logf("---- drag %d starting ----", app.drags)

	// -hdrop: the staging folder is made HERE, before DoDragDrop, because a
	// target may ask for CF_HDROP during the hover and the answer has to be the
	// final paths. Nothing is written into it yet unless -stage-early says so.
	var stage *dragStage
	if app.hdrop {
		s, err := newDragStage(app.stageParent, app.files, app.stageCfg)
		if err != nil {
			logf("staging: could not prepare the drag: %v -- the drag is abandoned", err)
			return
		}
		stage = s
		currentStage.Store(s)
		logf("staging folder for drag %d: %s", app.drags, s.root)
		for i, p := range s.paths {
			logf("    [%d] %s (%d bytes) -- the path the file WILL have", i, p, app.files[i].size)
		}
		if len(s.drop) != len(s.paths) {
			logf("    CF_HDROP will name the folder(s) rather than the files: %s", strings.Join(s.drop, " | "))
		}
		if app.stageCfg.early {
			s.stageEarly()
		}
	}

	var dataObj *comObject
	if stage != nil {
		dataObj = newHDropDataObject(stage)
	} else {
		dataObj = newDataObject(app.files, app.write, app.streamAtEnd)
	}
	d := dataObj.impl.(*dataObject)
	srcObj := newDropSourceObject(stage)
	asyncPtr := dataObj.ifaceAddr("IDataObjectAsyncCapability")

	// Step 2 of the documented source procedure: "Call SetAsyncMode with
	// fDoOpAsync set to VARIANT_TRUE to indicate that an asynchronous operation
	// is supported."
	hr, _, _ := syscallPinned(asyncVtbl.SetAsyncMode, asyncPtr, variantTrue)
	logf("source called SetAsyncMode(VARIANT_TRUE) -> %s", hrName(hr))

	// The virtual-file mode allows DROPEFFECT_COPY only: a file the target reads
	// out of a stream is a copy and there is nothing to move.
	//
	// The staged mode allows copy and move, and prefers move, which is 7-Zip's
	// practice and APP.md 3's ruling: the staged file is a disposable copy, so a
	// move on the same volume is one rename with no second pass over the bytes
	// and nothing left to clean up, and a move across volumes is the target's
	// copy followed by its own delete of the staging. A move here never touches
	// the record inside the archive -- it moves the temporary. -copy-only
	// restores the old behaviour for comparison.
	allowed := uintptr(dropEffectCopy)
	if stage != nil {
		allowed = uintptr(stage.cfg.allowedEffects())
	}
	var effect uint32 = dropEffectNone
	logf("calling DoDragDrop(dataObject=0x%X, dropSource=0x%X, %s)",
		dataObj.unknown(), srcObj.unknown(), effectName(uint32(allowed)))
	start := time.Now()
	ret, _, _ := procDoDragDrop.Call(dataObj.unknown(), srcObj.unknown(),
		allowed, uintptr(unsafe.Pointer(&effect)))
	logf("DoDragDrop returned %s after %s, effect = %s",
		hrName(ret), time.Since(start).Round(time.Millisecond), effectName(effect))
	if stage != nil {
		// "If the return value is DRAGDROP_S_DROP, DoDragDrop calls
		// IDropTarget::Drop ... The DoDragDrop function returns the last effect
		// code to the source"; DRAGDROP_S_CANCEL is the cancel. An effect of
		// DROPEFFECT_NONE after a drop means the target took nothing, which for
		// the staging folder is the same situation as a cancel.
		stage.noteDragEnded(fmt.Sprintf("DoDragDrop -> %s, effect %s", hrName(ret), effectName(effect)))
	}

	// Step 3: "After DoDragDrop returns, call InOperation."
	//
	// Through syscallPinned, like every other indirect call here: pfInAsyncOp is
	// an out-parameter, and syscall.SyscallN would leave it wherever escape
	// analysis put it -- which is the stack, which can move under a callback.
	var inOp uint32
	hr, _, _ = syscallPinned(asyncVtbl.InOperation, asyncPtr, uintptr(unsafe.Pointer(&inOp)))
	logf("source called InOperation -> %s, pfInAsyncOp = 0x%08X", hrName(hr), inOp)
	if inOp != 0 {
		logf("the target is extracting on a thread of its own; the data object stays alive until EndOperation")
	} else {
		if stage != nil {
			// The answer to one of this mode's questions, in one line: without a
			// negotiated operation there is no EndOperation, so nothing will
			// ever tell this program that the copy is over and the folder is the
			// scavenge's to take.
			logf("staging: the target did NOT negotiate the asynchronous protocol for this CF_HDROP source -- there will be no EndOperation")
		}
		d.finishSync()
	}

	// Step 4: "Release the data object." Ours, not the shell's: if the shell is
	// still extracting it holds references of its own, and the object lives on.
	srcObj.release()
	dataObj.release()

	if peak, ok := peakWorkingSet(); ok {
		logf("peak working set after drag %d: %d bytes (%.1f MiB)", app.drags, peak, float64(peak)/(1<<20))
	}
	if stage != nil {
		logf("%s", stage.summary())
	}
	logf("---- drag %d handed over; %d COM interface cells still registered ----",
		app.drags, comRegistrySize())
}

// ---------------------------------------------------------------------------

// sizeFlag accepts a plain byte count or a binary suffix, so that -size 256MiB
// reads the way the file names do.
type sizeFlag int64

func (s *sizeFlag) String() string { return humanSize(int64(*s)) }

func (s *sizeFlag) Set(v string) error {
	n, err := parseSize(v)
	if err != nil {
		return err
	}
	*s = sizeFlag(n)
	return nil
}

func parseSize(v string) (int64, error) {
	t := strings.TrimSpace(v)
	mult := int64(1)
	for _, suffix := range []struct {
		s string
		m int64
	}{
		{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10},
		{"T", 1 << 40}, {"G", 1 << 30}, {"M", 1 << 20}, {"K", 1 << 10},
	} {
		if len(t) > len(suffix.s) && strings.EqualFold(t[len(t)-len(suffix.s):], suffix.s) {
			mult = suffix.m
			t = t[:len(t)-len(suffix.s)]
			break
		}
	}
	n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad size %q: %w", v, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("bad size %q: must be positive", v)
	}
	return n * mult, nil
}
