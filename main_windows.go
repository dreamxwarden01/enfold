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
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"

	"github.com/dreamxwarden01/enfold/internal/app"
	"github.com/dreamxwarden01/enfold/internal/app/api"
	"github.com/dreamxwarden01/enfold/internal/app/pivcards"
	"github.com/dreamxwarden01/enfold/internal/brand"
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
)

// Warnings the shell raises.
const (
	warnLockDetection app.Code = "shell.lock_detection_unavailable"
)

// shell is the process: the core, the Wails app, the tray, the window.
type shell struct {
	core *app.Core
	app  *application.App
	tray *tray
	lock *lockWatch

	winMu    sync.Mutex
	quitMu   sync.Mutex
	quitting bool
	settings func() app.Settings
}

func main() {
	windows.SetErrorMode(windows.SEM_NOGPFAULTERRORBOX | windows.SEM_FAILCRITICALERRORS)
	dataDir := dataDirectory()
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		fatal(err)
	}
	logger := openLog(dataDir)
	defer logger.close()

	s := &shell{}
	core, err := app.New(app.Deps{
		Cards:   pivcards.New(),
		Events:  s,
		Input:   lastInput{},
		Log:     logger.printf,
		DataDir: dataDir,
	})
	if err != nil {
		fatal(err)
	}
	s.core = core
	s.settings = core.GetSettings

	vault, archives, archive, keys, settings := api.Services(core)
	shellSvc := api.NewShell(api.Hooks{
		ShowWindow:  s.ensureWindow,
		CloseWindow: s.closeWindow,
		PickFiles:   s.pickFiles,
		PickFolder:  s.pickFolder,
		SaveFile:    s.saveFile,
		Reveal:      s.reveal,
		Quit:        s.quit,
	})

	profile := filepath.Join(dataDir, "WebView2")
	opts := application.Options{
		Name:        appName,
		Description: "Compression and encryption archives, unlocked by a YubiKey.",
		Icon:        brand.PNG(brand.Mark(256, false)),
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
			UniqueID:               uniqueID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { s.ensureWindow() },
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

// sweepProfile empties the WebView2 user-data folder before the first
// window (DESIGN trap 13: nothing from a previous run's previews survives).
func sweepProfile(dir string) {
	os.RemoveAll(dir) // best effort; a locked file stays until next start
	os.MkdirAll(dir, 0o700)
}

// Emit is the core's Emitter: every event goes to the page and, for the
// vault state, to the tray.
func (s *shell) Emit(name string, payload any) {
	if s.app != nil {
		s.app.Event.Emit(name, payload)
	}
	if name == app.EventVaultState && s.tray != nil {
		if st, ok := payload.(app.VaultStatus); ok {
			s.tray.update(st)
		}
	}
}

// rawMessage receives the four typed secrets: "secret <kind> <promptID>
// <value>", from the main frame of the page's own origin only. Nothing here
// is logged.
func (s *shell) rawMessage(_ application.Window, message string, origin *application.OriginInfo) {
	if !strings.HasPrefix(message, "secret ") {
		return
	}
	if origin != nil && !fromApp(origin.Origin) {
		return
	}
	parts := strings.SplitN(message, " ", 4)
	if len(parts) != 4 {
		return
	}
	s.core.SubmitSecret(parts[1], parts[2], parts[3])
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
	theme := application.SystemDefault
	bg := application.NewRGB(0xF1, 0xF1, 0xF4)
	switch s.settings().Theme {
	case "dark":
		theme, bg = application.Dark, application.NewRGB(0x1C, 0x1C, 0x20)
	case "light":
		theme = application.Light
	default:
		if s.app.Env.IsDarkMode() {
			bg = application.NewRGB(0x1C, 0x1C, 0x20)
		}
	}
	w := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:                       windowName,
		Title:                      appName,
		Width:                      1100,
		Height:                     700,
		MinWidth:                   880,
		MinHeight:                  560,
		URL:                        "/",
		BackgroundColour:           bg,
		EnableFileDrop:             true,
		DefaultContextMenuDisabled: true,
		Permissions:                deny,
		Windows: application.WindowsWindow{
			Theme:                   theme,
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
		drop := dropPayload{Paths: files}
		if d := ctx.DropTargetDetails(); d != nil {
			drop.ArchiveID = d.Attributes["data-archive-id"]
			drop.Folder = d.Attributes["data-folder"]
		}
		s.app.Event.Emit("shell.drop", drop)
	})
}

// dropPayload is what a file drop becomes for the page: the paths and the
// target the page named. The core validates both.
type dropPayload struct {
	Paths     []string `json:"paths"`
	ArchiveID string   `json:"archiveId"`
	Folder    string   `json:"folder"`
}

func (s *shell) closeWindow() {
	if w, ok := s.app.Window.GetByName(windowName); ok {
		w.Close()
	}
}

func (s *shell) window() application.Window {
	w, _ := s.app.Window.GetByName(windowName)
	return w
}

func (s *shell) pickFiles(title string, multiple bool) ([]string, error) {
	d := s.app.Dialog.OpenFile().SetTitle(title).CanChooseFiles(true).CanChooseDirectories(false)
	if w := s.window(); w != nil {
		d.AttachToWindow(w)
	}
	if multiple {
		return d.PromptForMultipleSelection()
	}
	p, err := d.PromptForSingleSelection()
	if err != nil || p == "" {
		return nil, err
	}
	return []string{p}, nil
}

func (s *shell) pickFolder(title string) (string, error) {
	d := s.app.Dialog.OpenFile().SetTitle(title).CanChooseFiles(false).CanChooseDirectories(true).CanCreateDirectories(true)
	if w := s.window(); w != nil {
		d.AttachToWindow(w)
	}
	return d.PromptForSingleSelection()
}

func (s *shell) saveFile(title, filename string) (string, error) {
	d := s.app.Dialog.SaveFile().SetMessage(title).SetFilename(filename).CanCreateDirectories(true)
	if w := s.window(); w != nil {
		d.AttachToWindow(w)
	}
	return d.PromptForSingleSelection()
}

func (s *shell) reveal(path string) error {
	return s.app.Env.OpenFileManager(path, true)
}

// quit is the tray's and the page's Quit: unsaved changes are named and
// confirmed, then the ordered shutdown runs and the process ends. Never
// asked twice.
func (s *shell) quit() {
	s.quitMu.Lock()
	if s.quitting {
		s.quitMu.Unlock()
		return
	}
	s.quitting = true
	s.quitMu.Unlock()
	st := s.core.Status()
	if st.DirtyArchives > 0 || st.State == app.StateUnlocking {
		msg := fmt.Sprintf("%d archive(s) have changes not yet saved. They are saved as one step before quitting.", st.DirtyArchives)
		if st.State == app.StateUnlocking {
			msg = "An unlock is in progress; quitting cancels it."
		}
		d := s.app.Dialog.Question().SetTitle(appName).SetMessage(msg)
		quitBtn := d.AddButton("Quit")
		cancel := d.AddButton("Cancel")
		d.SetDefaultButton(quitBtn).SetCancelButton(cancel)
		proceed := make(chan bool, 1)
		quitBtn.OnClick(func() { proceed <- true })
		cancel.OnClick(func() { proceed <- false })
		if w := s.window(); w != nil {
			d.AttachToWindow(w)
		}
		go func() {
			d.Show()
		}()
		if !<-proceed {
			s.quitMu.Lock()
			s.quitting = false
			s.quitMu.Unlock()
			return
		}
	}
	s.app.Quit()
}

// onShutdown is the bounded, ordered end (§5): resolve, lock, stop.
func (s *shell) onShutdown() {
	s.core.ResolveForShutdown(shutdownBudget)
	if s.lock != nil {
		s.lock.stop()
	}
	s.core.Close()
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
	f, err := os.OpenFile(filepath.Join(dir, "enfold.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
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
