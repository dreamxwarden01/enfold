//go:build windows

// Enfold's shell: the Wails wiring around the core of internal/app
// (docs/APP.md §1, §2.4, §4, §5). It registers the services, owns the
// tray and the one window, forwards file drops and the four typed secrets,
// and runs the ordered shutdown. It decides nothing about vaults.
package main

import (
	"embed"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"

	"github.com/dreamxwarden01/enfold/internal/app"
	"github.com/dreamxwarden01/enfold/internal/app/api"
	"github.com/dreamxwarden01/enfold/internal/app/pivcards"
	"github.com/dreamxwarden01/enfold/internal/brand"
	"github.com/dreamxwarden01/enfold/internal/dragout"
	"github.com/dreamxwarden01/enfold/internal/keystore"
	"github.com/dreamxwarden01/enfold/internal/secmem"
)

//go:embed all:frontend/dist
var assets embed.FS

const (
	appName    = "Enfold"
	windowName = "main"
	uniqueID   = "com.dreamxwarden01.enfold"
	// appOrigin is the page's origin on Windows (the asset server's host);
	// the one-time secret endpoint answers only to it.
	appOrigin = "http://wails.localhost"

	shutdownBudget = 3 * time.Second
	// dragSweepBudget is the sweep at exit's slice of that budget (APP.md
	// §3): housekeeping that must never be what keeps the process standing,
	// so a second of wall time and then whatever is left is the next
	// launch's.
	dragSweepBudget = time.Second
)

// Warnings the shell raises.
const (
	warnLockDetection app.Code = "shell.lock_detection_unavailable"
)

// eventShellOpen is the shell's "open this file" (APP.md §14): the payload
// is an app.OpenRequest. Emitted for a second launch, whose page is
// already listening; the first launch's own argument waits in the slot
// instead (openarg.go).
const eventShellOpen = "shell.open"

// shell is the process: the core, the Wails app, the tray, the window.
type shell struct {
	core *app.Core
	app  *application.App
	tray *tray
	lock *lockWatch
	log  func(string, ...any)

	profile  string
	winMu    sync.Mutex
	quitMu   sync.Mutex
	quitting bool
	settings func() app.Settings
	// open is the "open this from Explorer" a launch carried (APP.md §14,
	// openarg.go): the page takes it at boot, and a second launch fills it
	// again and says so with `shell.open`.
	open pendingOpen
	// ours marks a close the shell is performing itself — the page asking
	// to go to the tray (CloseWindow), the close question answered *tray*,
	// and the quit's own — so the WindowClosing gate lets it through
	// without asking (APP.md §2.4). It is set before the close and taken
	// by the gate, one close each.
	ours atomic.Bool
	// themeMu orders the caption's updates (syncTheme, themeLoaded): a
	// saved theme, a flip of Windows' own mode and a page's load each read
	// the setting and the mode afresh under it, so the update applied last
	// has read the newest of both. It guards caption.
	themeMu sync.Mutex
	// caption is what the shell knows of the window's caption: the one
	// window it may touch, whose page has loaded, and the theme that
	// window wears, so that a save which leaves the resolved theme as it
	// was repaints nothing (captionState).
	caption captionState
	// made is the theme ensureWindow makes a window in, handed to that
	// window's load listener by the OnCreate hook in main. The hook runs
	// inside NewWithOptions, on ensureWindow's goroutine and under winMu,
	// so what it reads is what was stored for that window.
	made atomic.Int32
	// backdrop is what every window of this process is made over, Solid
	// or Mica (backdropFor), decided once in main before anything can make
	// a window and never written again.
	backdrop backdrop
}

// The caption's theme, as captionState records it.
const (
	captionUnknown int32 = iota
	captionLight
	captionDark
)

// secretRefused tells the page that a submitted secret was not accepted
// (a stale prompt, no ceremony); the value itself is not echoed.
type secretRefused struct {
	Code app.Code `json:"code"`
}

func main() {
	windows.SetErrorMode(windows.SEM_NOGPFAULTERRORBOX | windows.SEM_FAILCRITICALERRORS)
	dataDir := dataDirectory()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		fatal(err)
	}
	logger := openLog(dataDir)
	defer logger.close()
	excludeFromWER(logger.printf) // the other half of the SetErrorMode above
	secmem.Log = logger.printf    // a page the kernel would not lock is worth one line here
	keystore.Log = logger.printf  // a scrub that failed after its flip, or a predicate read short at Open (R41)

	s := &shell{log: logger.printf}
	core, err := app.New(app.Deps{
		Cards:    pivcards.New(),
		Events:   s,
		Input:    lastInput{},
		Log:      logger.printf,
		DataDir:  dataDir,
		Drag:     s.beginDrag,
		DragRoot: dragStagingRoot(),
	})
	if err != nil {
		fatal(err)
	}
	s.core = core
	s.settings = core.GetSettings

	// The window's backdrop (APP.md §6, Controls: the ground), read once
	// for every window this process makes, before anything can make one:
	// the developer's switch and whether this Windows draws the material.
	// It is decided here, before application.New, since the single-instance
	// listener New starts can make a window, but logged only once New has
	// returned (below).
	asked := os.Getenv("ENFOLD_BACKDROP")
	s.backdrop = backdropFor(asked, w32.SupportsBackdropTypes())

	vault, archives, archive, keys, settings := api.Services(core)
	shellSvc := api.NewShell(api.Hooks{
		ShowWindow:   s.ensureWindow,
		CloseWindow:  s.closeWindow,
		CloseDecided: s.closeDecided,
		PickFiles:    s.pickFiles,
		PickFolder:   s.pickFolder,
		SaveFile:     s.saveFile,
		Reveal:       s.reveal,
		OpenFolder:   s.openFolder,
		DataDir:      dataDir,
		Quit:         s.quit,
		DragOut:      s.dragOut,
		PendingOpen:  s.open.take,
		// Paste's source (APP.md §3, §6): Explorer's CF_HDROP, read by the
		// shell since the WebView's clipboard holds no files.
		ClipboardFiles: s.clipboardFiles,
	})

	profile := filepath.Join(dataDir, "WebView2")
	s.profile = profile
	opts := application.Options{
		Name:        appName,
		Description: "Compression and encryption archives, unlocked by a YubiKey.",
		// The window takes its icon from the executable's resources, the
		// .ico through the .syso; Wails falls back to this one, the .ico's
		// 256 px drawing, when a build carries none.
		Icon: brand.PNG(brand.Icon(256)),
		Services: []application.Service{
			application.NewService(vault),
			application.NewService(archives),
			application.NewService(archive),
			application.NewService(keys),
			application.NewService(settings),
			application.NewService(shellSvc),
		},
		MarshalError: api.MarshalError,
		Assets: application.AssetOptions{
			Handler:    application.AssetFileServerFS(assets),
			Middleware: securityHeaders, // set once the preview port is known
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: uniqueID,
			// A second launch hands its arguments over and exits (APP.md
			// §2.4, §14). The request is staged before the window is asked
			// for, so that a launch which has to recreate a window closed
			// to the tray is found by that fresh page's PendingOpen; the
			// event is for the page that is already up and listening.
			OnSecondInstanceLaunch: func(d application.SecondInstanceData) {
				req := s.open.stage(openArguments(afterProgram(d.Args), d.WorkingDir))
				s.ensureWindow()
				if req != nil {
					s.app.Event.Emit(eventShellOpen, *req)
				}
			},
		},
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
			WebviewUserDataPath:           profile,
		},
		ShouldQuit:        func() bool { return true }, // never shows UI
		OnShutdown:        s.onShutdown,
		RawMessageHandler: s.rawMessage,
		PanicHandler: func(d *application.PanicDetails) {
			logger.printf("panic: %v", d.Error)
			core.LockNow(app.ReasonPanic)
		},
	}
	// The second instance exits inside application.New, before anything
	// below touches the profile or the vault.
	s.app = application.New(opts)
	logger.printf("start: application created")
	// The backdrop is logged here and not where it is decided, so that a
	// run tried with the switch says what it got: a second launch, which
	// exits inside New (beta.20, application.go 207-216) while the running
	// process brings its own window forward, never writes the line.
	logger.printf("start: backdrop %s (ENFOLD_BACKDROP %q)", s.backdrop, asked)
	// A window's caption is the shell's to set only once its page has
	// loaded (captionState says why). The load listener is attached
	// through OnCreate, which Wails runs as it adds a window and before the
	// window runs (beta.20, window_manager.go 60-72), so no window's first
	// load can come before it; that load then catches up a window made in
	// a theme that has moved since. It is registered here, before anything
	// can make a window, a second launch's ensureWindow included.
	s.app.Window.OnCreate(func(w application.Window) {
		if w.Name() != windowName {
			return
		}
		id, made := w.ID(), s.made.Load()
		w.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(*application.WindowEvent) {
			s.themeLoaded(id, made)
		})
	})

	// The launch's own argument, staged before the window is asked for:
	// there is no page to emit to until one has drawn, so this waits for
	// the page's Shell.PendingOpen at boot (APP.md §14, openarg.go).
	wd, _ := os.Getwd()
	if req := s.open.stage(openArguments(os.Args[1:], wd)); req != nil {
		logger.printf("start: launched to open a file (%d in all)", 1+len(req.Rest))
	}

	// No OleInitialize here any more (APP.md §3, ruled 2026-09-11): each
	// drag takes its own apartment on its own thread, so the main thread
	// needs none — and the one that used to be taken here, directly rather
	// than through an InvokeSync the application could not yet serve, is
	// what killed the first launch. WebView2 initialises this thread's
	// apartment for itself when it needs one.

	sweepProfile(profile)
	port, err := core.Start()
	if err != nil {
		fatal(err)
	}
	core.SetAppOrigin(appOrigin)
	setPreviewPort(port)
	logger.printf("start: core started, preview on port %d", port)

	s.tray = newTray(s)
	logger.printf("start: tray ready")
	s.lock = startLockWatch(core, logger.printf)
	logger.printf("start: lock watch ready (wts=%v)", s.lock.wtsOK)
	s.app.Event.OnApplicationEvent(events.Windows.APMSuspend, func(*application.ApplicationEvent) {
		core.LockNow(app.ReasonSuspend)
	})
	// While the Theme setting is "system" the caption follows Windows' own
	// app mode (APP.md §2.4). The window is never made with Wails'
	// SystemDefault, whose per-window listener re-applies the system's
	// theme on every flip and would put the caption back on it after the
	// user had chosen Light or Dark (beta.20, webview_window_windows.go
	// 573-577). This one resolves the setting each time, so an explicit
	// choice stands through a flip and costs nothing.
	s.app.Event.OnApplicationEvent(events.Windows.SystemThemeChanged, func(*application.ApplicationEvent) {
		s.syncTheme()
	})
	// The window is created before Run, as Wails expects; a second launch
	// and the tray recreate it through the same ensureWindow.
	s.ensureWindow()
	logger.printf("start: window requested; running")

	if err := s.app.Run(); err != nil {
		logger.printf("run: %v", err)
		os.Exit(1)
	}
}

func fatal(err error) {
	log.Printf("enfold: %v", err)
	os.Exit(1)
}

// dataDirectory is %LOCALAPPDATA%\Enfold, or ENFOLD_DATA_DIR when set: a
// throwaway data folder for trying a build without touching the real one.
func dataDirectory() string {
	if d := os.Getenv("ENFOLD_DATA_DIR"); d != "" {
		return d
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		if d, err := os.UserCacheDir(); err == nil {
			base = d
		} else {
			base = "."
		}
	}
	return filepath.Join(base, appName)
}

// dragStagingRoot is %TEMP%\Enfold\drag, where every drag out stages
// (APP.md §3, ruled 2026-09-11, moved there from %LOCALAPPDATA%\Enfold\drag)
// — the conventional home of an application's transient files, WinRAR's own
// Rar$DIa… among them — for three reasons:
//
//   - Storage Sense and Disk Cleanup sweep %TEMP% eventually. That is an
//     incidental backstop and never a thing relied on: nothing empties
//     %TEMP% at a boot, a delete-at-reboot needs an administrator, and under
//     Fast Startup a shutdown is a hibernation that would never process one
//     — the cleaners that matter are this program's own, at launch, every
//     ten minutes, and at a normal exit (endDragOut). It is where users and
//     cleaning tools look for a program's leavings, which is worth
//     something by itself.
//   - A user's own redirection is honoured. os.TempDir is GetTempPath,
//     documented to read TMP, then TEMP, then USERPROFILE, then the Windows
//     directory (Go 1.26 prefers GetTempPath2 where Windows has it, which
//     differs only for a process running as SYSTEM — this one never is). A
//     RAM disk therefore keeps the staged plaintext off persistent storage
//     for free, and another volume costs only that a drop on the desktop
//     becomes a copy instead of a rename.
//   - The later "open a file in an outside program, repack what changed"
//     workflow belongs beside it, under the same manifest and the same
//     sweep.
//
// %LOCALAPPDATA%\Enfold\drag is neither migrated nor touched: the sweep
// only ever looks under the root it is given, so a folder an older build
// left there is simply no longer ours to take.
func dragStagingRoot() string {
	return filepath.Join(os.TempDir(), appName, "drag")
}

// sweepProfile empties the WebView2 user-data folder before the first
// window (DESIGN trap 13: nothing from a previous run's previews survives).
func sweepProfile(dir string) {
	os.RemoveAll(dir) // best effort; a locked file stays until next start
	os.MkdirAll(dir, 0o700)
}

// Emit is the core's Emitter: every event goes to the page and, for the
// vault state and an operation's progress and end, to the tray.
func (s *shell) Emit(name string, payload any) {
	if s.app != nil {
		s.app.Event.Emit(name, payload)
	}
	if name == app.EventSettingsChanged {
		// The caption follows a saved theme at once (APP.md §2.4): Wails
		// sets it only when it makes the window, so without this it kept
		// the old theme until the window was made again.
		s.syncTheme()
		return
	}
	if s.tray == nil {
		return
	}
	switch name {
	case app.EventVaultState:
		if st, ok := payload.(app.VaultStatus); ok {
			s.tray.update(st)
		}
	case app.EventOpProgress, app.EventOpDone:
		// The tray's running line moves with the operation (direction 10).
		if v, ok := payload.(app.OpView); ok {
			s.tray.opEvent(v)
		}
	}
}

// rawMessage receives the four typed secrets: "secret <kind> <promptID>
// <value>", from the main frame of the page's own origin only. It carries a
// replacement's two answers besides, kinds kept and confirm, whose value is
// empty ("secret kept <promptID> ", the last space kept): a ceremony is
// answered through one mailbox, and the kind and the id must both match the
// prompt standing. Nothing here is logged.
func (s *shell) rawMessage(_ application.Window, message string, origin *application.OriginInfo) {
	if !strings.HasPrefix(message, "secret ") {
		return
	}
	if origin != nil && !fromApp(origin.Origin) {
		return
	}
	if origin != nil && origin.TopOrigin != "" && origin.Origin != origin.TopOrigin {
		return // not the main frame
	}
	parts := strings.SplitN(message, " ", 4)
	if len(parts) != 4 {
		return
	}
	if e := s.core.SubmitSecret(parts[1], parts[2], parts[3]); e != nil {
		s.app.Event.Emit("secret.refused", secretRefused{Code: e.Code})
	}
}

// fromApp reports whether a WebView2 source URL is the page's own origin.
func fromApp(source string) bool {
	u, err := url.Parse(source)
	if err != nil {
		return false
	}
	return u.Scheme == "http" && u.Hostname() == "wails.localhost"
}

// ensureWindow shows the window, creating it when there is none (§2.4).
func (s *shell) ensureWindow() {
	s.winMu.Lock()
	defer s.winMu.Unlock()
	if w, ok := s.app.Window.GetByName(windowName); ok {
		w.UnMinimise()
		w.Show()
		w.Focus()
		return
	}
	deny := map[application.PermissionType]application.Permission{}
	for _, k := range []application.PermissionType{
		application.PermissionMicrophone, application.PermissionCamera, application.PermissionGeolocation,
		application.PermissionNotifications, application.PermissionClipboardRead,
	} {
		deny[k] = application.PermissionDeny
	}
	denyWV2 := map[application.CoreWebView2PermissionKind]application.CoreWebView2PermissionState{}
	for k := application.CoreWebView2PermissionKindUnknownPermission; k <= application.CoreWebView2PermissionKindClipboardRead; k++ {
		denyWV2[k] = application.CoreWebView2PermissionStateDeny
	}
	// The window is made in the theme the setting resolves to now, Light or
	// Dark and never SystemDefault (the listener in main says why); every
	// change after this one is the shell's own (syncTheme), once the
	// window's page has loaded. Windows' own mode is read from the
	// registry itself: Wails' Env.IsDarkMode answers false until the
	// application runs (beta.20, environment_manager.go 36-41, its impl
	// set only in Run, application.go 667), and the first window is made
	// before it does. What the window is made in goes to its load listener
	// through made; under a contrast theme Wails sets no theme at all
	// (webview_window_windows.go 1476-1478), so the window wears none
	// known, and its load applies one.
	dark := themeDark(s.settings().Theme, w32.IsCurrentlyDarkMode())
	theme := application.Light
	if dark {
		theme = application.Dark
	}
	made := captionOf(dark)
	if w32.IsCurrentlyHighContrastMode() {
		made = captionUnknown
	}
	s.made.Store(made)
	// The backdrop decided in main: the URL that tells the page, the
	// background and the backdrop Wails is asked for, and the colour the
	// window holds (backdrop.look).
	look := s.backdrop.look(dark)
	w := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:                       windowName,
		Title:                      appName,
		Width:                      1100,
		Height:                     700,
		MinWidth:                   880,
		MinHeight:                  560,
		URL:                        look.url,
		BackgroundType:             look.background,
		BackgroundColour:           look.colour,
		EnableFileDrop:             true,
		DefaultContextMenuDisabled: true,
		Permissions:                deny,
		Windows: application.WindowsWindow{
			Theme:                   theme,
			BackdropType:            look.backdrop,
			Permissions:             denyWV2,
			GeneralAutofillEnabled:  false,
			PasswordAutosaveEnabled: false,
		},
	})
	w.OnWindowEvent(events.Common.WindowFilesDropped, func(e *application.WindowEvent) {
		ctx := e.Context()
		files := ctx.DroppedFiles()
		if len(files) == 0 {
			return
		}
		drop := dropPayload{Paths: files, IsDir: make([]bool, len(files))}
		if d := ctx.DropTargetDetails(); d != nil {
			drop.ArchiveID = d.Attributes["data-archive-id"]
			drop.DirID = d.Attributes["data-dir-id"]
		}
		// The event is read on this thread and left before anything slow:
		// a stat of a few thousand paths, or of one on a sleeping drive or
		// a network share, would hold the main thread for the whole batch
		// with no repaint (APP.md §3, Shell).
		go func() {
			for i, p := range drop.Paths {
				if fi, err := os.Stat(p); err == nil && fi.IsDir() {
					drop.IsDir[i] = true
				}
			}
			s.app.Event.Emit("shell.drop", drop)
		}()
	})
	// The close question's gate (APP.md §2.4, ruled 2026-09-13: "we must
	// not default to going to the tray"). Common.WindowClosing is what
	// WM_CLOSE becomes under the default event mapping — the button,
	// Alt+F4, the taskbar's close — and what Close() emits.
	//
	// This is a *hook* and not a listener, and the difference is the whole
	// mechanism. Wails takes each window event off its channel on a
	// goroutine of its own — off the main thread, as the listener below
	// relies on — and then runs the event's hooks on that goroutine,
	// synchronously and in order: a hook's Cancel() returns before a single
	// listener has been started. The listeners are each started in their
	// own goroutine and only read IsCancelled() on entry, so cancelling
	// from a listener is a race against Wails' own destroy-the-window
	// listener, which NewWindow registers before this file sees the window.
	// A cancelled close costs nothing: while the window has not been told
	// to close unconditionally, WM_CLOSE returns 0 without destroying
	// anything, so the window simply stays.
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if s.ownClose() {
			return // ours: the tray, the answered question, the way out
		}
		switch s.settings().CloseAction {
		case app.CloseTray:
			// The user has said so: on to the tray, as below.
		case app.CloseQuit:
			e.Cancel()
			go s.quit() // off this goroutine: quit shows a native question
		default:
			// ask, and anything a file no longer holds: the page asks.
			e.Cancel()
			s.app.Event.Emit("shell.close")
		}
	})
	// Destroying the window leaves every page (APP.md §2.3, §2.4): the
	// process keeps running in the tray and a window recreated from it
	// starts at the list, so every archive a page held closes — or drains
	// while a body is in flight — exactly as if its page had been left.
	// Wails runs each listener on a goroutine of its own, off the main
	// thread, and none of them runs at all when the hook above cancelled.
	w.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		s.core.LeaveAllArchives()
	})
}

// ownClose reports whether this close is the shell's own, and takes the
// mark if it is: a quit never asks, and neither does the one close a
// CloseWindow or an answered close question performs.
func (s *shell) ownClose() bool {
	s.quitMu.Lock()
	quitting := s.quitting
	s.quitMu.Unlock()
	return quitting || s.ours.Swap(false)
}

// dropPayload is what a file drop becomes for the page: the paths, what
// kind each one is, and the target the page named — the archive and the
// directory *id* it is showing, from Page's dirID and Crumbs, never a path
// (APP.md §3, FORMAT.md R39, DESIGN.md trap 31). The core validates both:
// a stale id is refused, where a stale path would resolve to whatever
// folder now happens to carry that name.
//
// IsDir runs beside Paths, one entry each: the page sends files to
// AddFiles and each directory to AddFolder, and it cannot stat a path
// itself — the core answers a directory handed to AddFiles with one failed
// outcome of params. A path that cannot be stat'ed at all (it went between
// the drop and this) is offered as a file, and the add reports it.
type dropPayload struct {
	Paths     []string `json:"paths"`
	IsDir     []bool   `json:"isDir"`
	ArchiveID string   `json:"archiveId"`
	DirID     string   `json:"dirId"`
}

// closeWindow is Shell.CloseWindow: the page's own request to put Enfold
// in the tray. It is a close the shell performs, so the question is not
// asked of a page that has already decided (APP.md §2.4).
func (s *shell) closeWindow() {
	if w, ok := s.app.Window.GetByName(windowName); ok {
		s.ours.Store(true)
		w.Close()
	}
}

// closeDecided is Shell.CloseDecided: the answer to the question the page
// asked when the close was cancelled (APP.md §2.4). The service has
// already refused anything but "tray" and "quit". With remember, the
// answer goes through the core's SetCloseAction, which moves that one
// field and writes the file — never a read-change-write of the whole
// snapshot from out here, which would take the settings lock twice and
// could overwrite a save the page had in flight. A refused write is
// reported and nothing is closed: a close the user cannot undo in
// Settings is worse than a question asked twice.
func (s *shell) closeDecided(action string, remember bool) *app.Error {
	if remember {
		if e := s.core.SetCloseAction(action); e != nil {
			return e
		}
	}
	if action == app.CloseQuit {
		go s.quit() // the bound call answers; the native question follows
		return nil
	}
	s.closeWindow()
	return nil
}

func (s *shell) window() application.Window {
	w, _ := s.app.Window.GetByName(windowName)
	return w
}

// syncTheme sets the window's caption, and the ground behind its page, to
// the theme the setting resolves to now (APP.md §2.4). It runs on a saved
// setting (settings.changed) and on a flip of Windows' own mode
// (SystemThemeChanged), reading both afresh under themeMu, so whichever
// runs last has read the newest of each. It touches only a window whose
// page has loaded (captionState); one still being made is caught up by
// its load (themeLoaded).
//
// Wails sets a window's theme only when it makes the window (beta.20 has
// no SetTheme on a window), so the shell calls the same exported
// w32.SetTheme on the window's HWND — DWMWA_USE_IMMERSIVE_DARK_MODE,
// attribute 20 (19 before build 18985) — and then has DWM re-read the
// frame: SetTheme invalidates the client area alone (pkg/w32/theme.go
// 231), and a caption no message repaints keeps its old colours.
// SetWindowPos with SWP_FRAMECHANGED is how Wails itself has DWM re-read a
// frame (webview_window_windows.go 1360-1368); SWP_NOACTIVATE is added
// because a flip of Windows' mode lands while Windows' own Settings is in
// front, and a repaint must not bring Enfold forward.
func (s *shell) syncTheme() {
	if s.app == nil {
		return
	}
	s.themeMu.Lock()
	defer s.themeMu.Unlock()
	s.syncThemeLocked()
}

// themeLoaded is the window's WebViewNavigationCompleted (the listener
// the OnCreate hook in main attaches): from its first load the window is
// syncTheme's to touch, wearing made, and a save or a flip that came while
// it was being made — which touched nothing — is applied now. It runs on a
// listener's goroutine, never the main thread (Wails starts each window
// listener on its own, webview_window.go 1010-1017).
func (s *shell) themeLoaded(id uint, made int32) {
	s.themeMu.Lock()
	defer s.themeMu.Unlock()
	s.caption.loaded(id, made)
	s.syncThemeLocked()
}

// syncThemeLocked is syncTheme's work, with themeMu held. It is never
// called on the main thread, which it waits for.
func (s *shell) syncThemeLocked() {
	dark := themeDark(s.settings().Theme, w32.IsCurrentlyDarkMode())
	want := captionOf(dark)
	w := s.window()
	if w == nil || !s.caption.touch(w.ID(), want) {
		// In the tray, the next window is made from the setting; a window
		// whose page has not loaded is caught up by its load; and one that
		// wears want already has nothing to repaint.
		return
	}
	set := application.InvokeSyncWithResult(func() bool {
		hwnd := uintptr(w.NativeWindow())
		// A window destroyed since, or a contrast theme: Windows draws that
		// caption in the system's colours, and Wails' own updateTheme leaves
		// it alone there (webview_window_windows.go 1476-1478), as this
		// does.
		if hwnd == 0 || w32.IsCurrentlyHighContrastMode() {
			return false
		}
		w32.SetTheme(hwnd, dark)
		w32.SetWindowPos(hwnd, 0, 0, 0, 0, 0,
			w32.SWP_FRAMECHANGED|w32.SWP_NOMOVE|w32.SWP_NOSIZE|w32.SWP_NOZORDER|w32.SWP_NOACTIVATE)
		// The solid colour behind the page before it paints and the one a
		// resize uncovers: Wails paints it on WM_ERASEBKGND and hands it to
		// WebView2 as its default background, and it stays the creation's
		// unless it is set again. Over Mica it is the same colour held
		// transparent, as the window was made with it (backdrop.ground).
		w.SetBackgroundColour(s.backdrop.ground(dark))
		return true
	})
	if set {
		s.caption.wears = want
	}
}

// themeDark resolves the Theme setting to the window's theme: "dark" and
// "light" are themselves, and "system" — or anything else, which the core
// never stores — is Windows' own app mode, osDark (AppsUseLightTheme 0, as
// w32.IsCurrentlyDarkMode reads it, beta.20 w32/theme.go 267-279).
func themeDark(theme string, osDark bool) bool {
	switch theme {
	case "dark":
		return true
	case "light":
		return false
	}
	return osDark
}

// groundOf is the window's BackgroundColour in a theme, the stylesheet's
// --ground: the solid colour behind the page before it first paints, and
// the one a resize uncovers before WebView2 has caught up.
func groundOf(dark bool) application.RGBA {
	if dark {
		return application.NewRGB(0x1C, 0x1C, 0x20)
	}
	return application.NewRGB(0xF1, 0xF1, 0xF4)
}

// captionOf is a resolved theme as captionState records it.
func captionOf(dark bool) int32 {
	if dark {
		return captionDark
	}
	return captionLight
}

// backdrop is what the window is made over (APP.md §6, Controls: the
// ground): the Solid window, whose page paints its own ground, the glow
// that stands in for the material, or Windows' Mica, with the page's
// ground transparent over it. The phase-5 study's judge (§4) and its
// window designer (§1.5) chose it; Solid stays the default until the user
// has judged the material in the real window.
type backdrop uint8

const (
	backdropSolid backdrop = iota
	backdropMica
)

func (b backdrop) String() string {
	if b == backdropMica {
		return "mica"
	}
	return "solid"
}

// backdropFor is the backdrop rule. asked is ENFOLD_BACKDROP, the switch
// the shell reads once before it makes a window: "mica" asks for the
// material (in any case, with the trailing space a .cmd's set line leaves
// trimmed), and anything else, "solid", nothing at all or a word it does
// not know, keeps the Solid window. supported is w32.SupportsBackdropTypes,
// Windows build 22621 or later (beta.20, pkg/w32/theme.go 190-192, the
// build read once from HKLM's CurrentBuildNumber, consts.go 31 and 62-74).
// Below it Wails does not fall back to a solid window: a translucent one
// gets the undocumented Windows 10 blur behind it (ACCENT_ENABLE_BLURBEHIND,
// webview_window_windows.go 1418-1432), so the shell chooses Solid itself
// there. Neither Wails nor w32 says whether Windows' transparency effects
// are on, so that is no input here: where the material cannot draw, DWM
// draws in its place (backdrop.ground says why none of the window's own
// colour shows there).
func backdropFor(asked string, supported bool) backdrop {
	if supported && strings.EqualFold(strings.TrimSpace(asked), "mica") {
		return backdropMica
	}
	return backdropSolid
}

// windowLook is what a backdrop sets of the window's options.
type windowLook struct {
	// url is the page's: "/?backdrop=mica" only over Mica, which is how
	// the page learns the backdrop the shell applied, never the one asked
	// for (main.ts sets data-backdrop="mica" on its root from it, and
	// app.css's Mica block reads that attribute alone).
	url        string
	background application.BackgroundType
	backdrop   application.BackdropType
	colour     application.RGBA
}

// look is the window's options under b: over Mica, Wails' translucent
// background with the Mica backdrop and the URL that tells the page; under
// Solid, the window exactly as it was before the switch — the solid
// background, no backdrop (Auto, the zero value, which Wails reads only
// for a translucent window, webview_window_windows.go 588-590) and the
// page at "/".
func (b backdrop) look(dark bool) windowLook {
	if b == backdropMica {
		return windowLook{
			url:        "/?backdrop=mica",
			background: application.BackgroundTypeTranslucent,
			backdrop:   application.Mica,
			colour:     b.ground(dark),
		}
	}
	return windowLook{
		url:        "/",
		background: application.BackgroundTypeSolid,
		backdrop:   application.Auto,
		colour:     b.ground(dark),
	}
}

// ground is the BackgroundColour the window holds under b: the
// stylesheet's ground in the theme (groundOf), which under Solid is what
// shows before the page paints and where a resize uncovers the window, as
// before. Over Mica the colour has no effect (beta.20): a translucent
// window's setBackgroundColour gives the window class no brush and sets
// WebView2's default background transparent whatever it is given
// (webview_window_windows.go 357-365), its WM_ERASEBKGND fills only a
// Solid window (1714-1732), and it has no redirection surface to fill
// (WS_EX_NOREDIRECTIONBITMAP, 402-415); setupChromium hands WebView2 the
// colour as given (2590-2597), but run() sets it transparent again at
// 587, before the window is first shown. The ground's RGB is kept there at
// alpha 0 so that nothing can ever hand WebView2 an opaque ground under
// the material. Where the material cannot draw (transparency effects off,
// battery saver, Remote Desktop), what shows behind the page is what DWM
// draws in its place, never this colour.
func (b backdrop) ground(dark bool) application.RGBA {
	c := groundOf(dark)
	if b == backdropMica {
		c.Alpha = 0
	}
	return c
}

// captionState is what the shell knows of the window's caption (APP.md
// §2.4), kept under shell.themeMu: the window it may touch and the theme
// that window wears. It holds the rule apart from Wails, where a test can
// reach it: no window is touched before its page has loaded, and its
// first load is when a window made in a theme that has moved since is
// caught up.
//
// Not before then, because Wails pumps the main thread's messages while it
// creates a window's WebView2 (chromium.Embed, beta.20
// internal/webview2/pkg/edge/chromium.go 225-283), after the window has
// its HWND and can be found by name (webview_window_windows.go 477, 509;
// window_manager.go 60-72). A background colour set from inside that pump
// reaches a controller that is nil — a panic recovered inside a nested
// InvokeSync whose wait is then never released, so the main thread stops
// (mainthread.go 23-32) — or one not yet ready, whose refusal ends the
// process (chromium.go 163-166, 676-696). And a change that lands before
// the HWND exists would find nothing to set, while the window is then made
// in the theme read before it: its load is what catches that up.
type captionState struct {
	win   uint  // the id of the window whose page has loaded; 0 while none has
	wears int32 // the theme win wears; captionUnknown while that is not known
}

// loaded is window id's WebViewNavigationCompleted: from its first load it
// is the window to touch, wearing made, the theme ensureWindow made it in.
// A later load of the same window (a reload) changes nothing of what it
// wears, which is what the shell has set since. Wails numbers windows
// upward from 1 and never reuses a number (webview_window.go 265-270), so
// a window made again from the tray is a new id.
func (c *captionState) loaded(id uint, made int32) {
	if c.win != id {
		c.win, c.wears = id, made
	}
}

// touch reports whether window id is to be set to want: it is the window
// whose page has loaded, and it does not wear want already.
func (c *captionState) touch(id uint, want int32) bool {
	return id != 0 && id == c.win && c.wears != want
}

// The native dialogs report a cancelled dialog as an error (the common
// file dialog's cancel HRESULT); to the page, cancelling is "nothing
// chosen", never a failure. A real failure is logged.
func (s *shell) pickFiles(title string, multiple bool) ([]string, error) {
	d := s.app.Dialog.OpenFile().SetTitle(title).CanChooseFiles(true).CanChooseDirectories(false)
	if w := s.window(); w != nil {
		d.AttachToWindow(w)
	}
	if multiple {
		p, err := d.PromptForMultipleSelection()
		if err != nil {
			s.log("open dialog: %v", err)
			return nil, nil
		}
		return p, nil
	}
	p, err := d.PromptForSingleSelection()
	if err != nil {
		s.log("open dialog: %v", err)
		return nil, nil
	}
	if p == "" {
		return nil, nil
	}
	return []string{p}, nil
}

func (s *shell) pickFolder(title string) (string, error) {
	d := s.app.Dialog.OpenFile().SetTitle(title).CanChooseFiles(false).CanChooseDirectories(true).CanCreateDirectories(true)
	if w := s.window(); w != nil {
		d.AttachToWindow(w)
	}
	p, err := d.PromptForSingleSelection()
	if err != nil {
		s.log("folder dialog: %v", err)
		return "", nil
	}
	return p, nil
}

// saveFile opens the native Save dialog. dir, when given, is the folder
// it opens in — the folder the last archive was made in (APP.md §6);
// empty leaves the choice to the shell's own last place.
func (s *shell) saveFile(title, filename, dir string) (string, error) {
	d := s.app.Dialog.SaveFile().SetMessage(title).SetFilename(filename).CanCreateDirectories(true)
	if dir != "" {
		d.SetDirectory(dir)
	}
	if w := s.window(); w != nil {
		d.AttachToWindow(w)
	}
	p, err := d.PromptForSingleSelection()
	if err != nil {
		s.log("save dialog: %v", err)
		return "", nil
	}
	return p, nil
}

func (s *shell) reveal(path string) error {
	return s.app.Env.OpenFileManager(path, true)
}

// openFolder is Shell.OpenDataFolder's half (APP.md 2.4): the folder
// opened itself — explorer.exe "<path>" — not shown selected in its
// parent. Wails' OpenFileManager already drops the select for a directory
// (fileexplorer.go: selectFile && !IsDir), so Reveal would open it too;
// asking for no select says what is meant rather than leaning on that.
func (s *shell) openFolder(path string) error {
	return s.app.Env.OpenFileManager(path, false)
}

// beginDrag is the core's Deps.Drag: the native drag of internal/dragout,
// which runs on a thread of its own and takes its own OLE apartment there,
// so there is nothing for this side to have made ready. What it does need
// from the shell is a way onto the window's thread for one call —
// ReleaseCapture before the hand-off, which "Releases the mouse capture
// from a window in the current thread" — and application.InvokeSync is it.
// The drag asks before its own thread exists, from the goroutine of the
// bound call, so the main thread is free to answer.
func (s *shell) beginDrag(o dragout.Options) (app.DragHandle, error) {
	o.OnWindowThread = func(f func()) { application.InvokeSync(f) }
	d, err := dragout.Begin(o)
	if err != nil {
		// A nil interface, not an interface over a nil *Drag.
		return nil, err
	}
	return d, nil
}

// dragOut is Shell.DragOut (APP.md §3): the core plans the drag and
// registers its operation, and the drag itself runs on a thread of its own
// (ruled 2026-09-11 on the measurements in docs/research/drag-out.md). This
// call waits for its end — it is a bound call, so this is its own goroutine
// and never the main thread — and there is no InvokeSync around the drag:
// the main thread is left to Wails' dispatcher for the whole gesture, which
// is what keeps the page's events and the operation strip moving while
// Explorer is inside its own Drop. The window's HWND is what the drop
// source compares with the window under the cursor at the button's release
// to tell a self-drop apart, and where the drag's own thread finds the
// thread to attach its input to.
//
// The drop is synchronous (ruled 2026-09-11, WinRAR's model): the data
// object offers no IDataObjectAsyncCapability, so the target has to take
// the drop inside its Drop, and this wait is for that and no more —
// DoDragDrop returns when Explorer has the paths, and that instant is the
// end of the drag for Enfold. It is not a wait for Explorer's copy: a
// cross-volume copy is handed to Explorer's own engine and runs on after
// the return (measured — docs/research/drag-out.md, "The drag thread,
// measured": a staged file still open 18.75 s later), and the conflict
// dialog belongs to that copy. Nothing of Enfold's is held meanwhile: the
// window is alive, Explorer's own window is in front, and the strip with
// its bar is the only sign.
func (s *shell) dragOut(archiveID string, recordIDs []string) (app.DragOutResult, *app.Error) {
	var hwnd uintptr
	if w := s.window(); w != nil {
		hwnd = uintptr(w.NativeWindow())
	}
	d, e := s.core.BeginDragOut(archiveID, recordIDs, hwnd)
	if e != nil {
		return app.DragOutResult{}, e
	}
	return d.Run()
}

// quit is the tray's and the page's Quit: the running operations are named
// and the quit confirmed, then the ordered shutdown runs and the process
// ends. Never asked twice. There are no unsaved changes to ask about since
// 2026-09-09 — every operation commits at its own end (APP.md §2.3) — so
// what the question names is what quitting would cancel.
func (s *shell) quit() {
	s.quitMu.Lock()
	if s.quitting {
		s.quitMu.Unlock()
		return
	}
	s.quitting = true
	s.quitMu.Unlock()
	st := s.core.Status()
	if running := runningOps(st); running > 0 || st.State == app.StateUnlocking {
		// A Windows question dialog is a Yes/No message box: Show blocks
		// until it closes and then runs the callback of the button whose
		// label is the one pressed — "Yes" or "No", nothing else.
		msg := fmt.Sprintf("%d operation(s) are still running. Cancel them and quit?", running)
		if st.State == app.StateUnlocking {
			msg = "An unlock is in progress. Cancel it and quit?"
		}
		d := s.app.Dialog.Question().SetTitle(appName).SetMessage(msg)
		yes := d.AddButton("Yes")
		no := d.AddButton("No")
		d.SetDefaultButton(yes).SetCancelButton(no)
		var proceed atomic.Bool
		yes.OnClick(func() { proceed.Store(true) })
		if w := s.window(); w != nil {
			d.AttachToWindow(w)
		}
		d.Show()
		if !proceed.Load() {
			s.quitMu.Lock()
			s.quitting = false
			s.quitMu.Unlock()
			return
		}
	}
	// Out of sight first, then the ordered end: a pending touch (§2.2)
	// may keep the card busy for the key's own timeout, and nobody stands
	// at a frozen window for that — the window and the tray go, the
	// process lingers unseen until the card answers and is released, then
	// ends. onShutdown's second ResolveForShutdown finds nothing left.
	if w := s.window(); w != nil {
		w.Hide()
	}
	if s.tray != nil {
		s.tray.t.Hide()
	}
	s.core.ResolveForShutdown(shutdownBudget)
	s.core.AwaitPendingTouch()
	s.app.Quit()
}

// runningOps counts the operations of a status that have not finished: what
// the Quit question names, and what the tray's tooltip says is under way.
func runningOps(st app.VaultStatus) int {
	n := 0
	for _, o := range st.Ops {
		if !o.Finished {
			n++
		}
	}
	return n
}

// onShutdown is the bounded, ordered end (§5): resolve, lock, stop.
func (s *shell) onShutdown() {
	s.core.ResolveForShutdown(shutdownBudget)
	if s.lock != nil {
		s.lock.stop()
	}
	s.core.Close()
	// The WebView2 profile: best effort now (the browser still holds some
	// of it), swept for certain at the next start (DESIGN trap 13).
	if s.profile != "" {
		os.RemoveAll(s.profile)
	}
	// The drag out last, after everything else: the staging root is swept
	// once more and then a staging folder nothing was handed out of goes,
	// one a target has is left for the next launch's scavenge, and a data
	// object Explorer still holds is revoked rather than pulled from under
	// it (APP.md §3). There is no apartment to close here — each drag's was
	// its own thread's.
	endDragOut(s.core.SweepDragAtExit, dragSweepBudget, dragout.Shutdown)
}

// endDragOut is the drag out's half of the ordered end, in the one order
// that is safe: the sweep of the staging root FIRST, while every folder
// this run made is still registered live and still manifested "live" — two
// rules, either of which is enough to keep the sweep off a folder whose
// files a target may be reading at this very moment — and dragout.Shutdown
// after it, which is precisely what lets those two go (it deletes the
// folders nothing was handed out of, leaves the rest manifested for the
// next launch, and takes every stage off the live set on the way). The
// other order would put the sweep after the only two rules that know which
// folder is live, with nothing between it and this run's own drag but the
// hour's age limit.
//
// The sweep is bounded twice over — one pass, no backoff, and a wall-time
// cap — because this runs inside the shutdown's own budget, which belongs
// to the archives and the lock before it belongs to housekeeping.
func endDragOut(sweep func(budget time.Duration), budget time.Duration, shutdown func()) {
	sweep(budget)
	shutdown()
}

// securityHeaders is the asset middleware (§4): the CSP names the preview
// port, everything else is shut. Production only; the dev server has its
// own origin.
var (
	previewPortMu sync.Mutex
	previewPort   int
)

func setPreviewPort(p int) {
	previewPortMu.Lock()
	previewPort = p
	previewPortMu.Unlock()
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if production {
			previewPortMu.Lock()
			port := previewPort
			previewPortMu.Unlock()
			loop := fmt.Sprintf("http://127.0.0.1:%d", port)
			h := w.Header()
			h.Set("Content-Security-Policy", strings.Join([]string{
				"default-src 'self'",
				"img-src 'self' " + loop,
				"media-src 'self' " + loop,
				"connect-src 'self' " + loop,
				"script-src 'self'",
				"style-src 'self'",
				"font-src 'self'",
				"object-src 'none'",
				"base-uri 'none'",
				"form-action 'none'",
				"frame-src 'none'",
				"frame-ancestors 'none'",
			}, "; "))
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), display-capture=(), "+
				"clipboard-read=(), clipboard-write=(), midi=(), local-fonts=(), window-management=(), "+
				"usb=(), serial=(), hid=(), payment=(), accelerometer=(), gyroscope=(), magnetometer=()")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// fileLog is the core's log: operations and lower-layer errors, never a
// secret. Truncated at every start.
type fileLog struct {
	mu sync.Mutex
	f  *os.File
}

func openLog(dir string) *fileLog {
	path := filepath.Join(dir, "enfold.log")
	// Appended, so what went wrong in the previous run survives a restart;
	// past 2 MiB the file becomes enfold.log.1 and a new one starts, so the
	// most recent window is never the one thrown away.
	if fi, err := os.Stat(path); err == nil && fi.Size() > 2<<20 {
		os.Remove(path + ".1")
		os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return &fileLog{}
	}
	return &fileLog{f: f}
}

func (l *fileLog) printf(format string, a ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return
	}
	fmt.Fprintf(l.f, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, a...))
}

func (l *fileLog) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}
