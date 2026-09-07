//go:build windows

package main

import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/dreamxwarden01/enfold/internal/app"
)

// lockWatch is the message-only window of APP.md §5: it receives the
// workstation-lock, logoff, disconnect and power-setting notifications a
// zero-window process would otherwise never see, and turns each into a
// lock trigger. It lives on its own OS-thread-locked goroutine with its own
// message loop.
//
// A message-only window receives only what is addressed to it: the WTS
// session notifications and the power-setting notifications it registered
// for. Broadcasts (PBT_APMSUSPEND, WM_QUERYENDSESSION) never reach it;
// suspend comes from Wails' own application event, logoff from WTS.
type lockWatch struct {
	core *app.Core
	log  func(string, ...any)

	hwnd    uintptr
	wtsOK   bool
	power   []uintptr // RegisterPowerSettingNotification handles
	stopped chan struct{}
	quit    chan struct{}
	once    sync.Once
}

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	wtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW                   = user32.NewProc("RegisterClassExW")
	procCreateWindowExW                    = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                     = user32.NewProc("DefWindowProcW")
	procGetMessageW                        = user32.NewProc("GetMessageW")
	procTranslateMessage                   = user32.NewProc("TranslateMessage")
	procDispatchMessageW                   = user32.NewProc("DispatchMessageW")
	procDestroyWindow                      = user32.NewProc("DestroyWindow")
	procPostMessageW                       = user32.NewProc("PostMessageW")
	procPostQuitMessage                    = user32.NewProc("PostQuitMessage")
	procRegisterPowerSettingNotification   = user32.NewProc("RegisterPowerSettingNotification")
	procUnregisterPowerSettingNotification = user32.NewProc("UnregisterPowerSettingNotification")
	procGetLastInputInfo                   = user32.NewProc("GetLastInputInfo")
	procWTSRegisterSessionNotification     = wtsapi32.NewProc("WTSRegisterSessionNotification")
	procWTSUnRegisterSessionNotification   = wtsapi32.NewProc("WTSUnRegisterSessionNotification")
	procWTSQuerySessionInformationW        = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procGetTickCount64                     = kernel32.NewProc("GetTickCount64")
)

const (
	wmDestroy          = 0x0002
	wmPowerBroadcast   = 0x0218
	wmWTSSessionChange = 0x02B1
	wmUser             = 0x0400
	wmStopWatch        = wmUser + 1

	pbtPowerSettingChange = 0x8013

	notifyForThisSession = 0
	hwndMessage          = ^uintptr(2) // HWND_MESSAGE = (HWND)-3

	deviceNotifyWindowHandle = 0

	wtsCurrentServerHandle  = 0
	wtsCurrentSession       = 0xFFFFFFFF
	wtsSessionInfoEx        = 25
	wtsSessionStateLocked   = 0
	wtsSessionStateUnlocked = 1
)

// GUID_SESSION_DISPLAY_STATUS and GUID_SESSION_USER_PRESENCE.
var (
	guidDisplayStatus = windows.GUID{Data1: 0x2B84C20E, Data2: 0xAD23, Data3: 0x4DDF, Data4: [8]byte{0x93, 0xDB, 0x05, 0xFF, 0xBD, 0x7E, 0xFC, 0xA5}}
	guidUserPresence  = windows.GUID{Data1: 0x3C0F4548, Data2: 0xC03F, Data3: 0x4C4D, Data4: [8]byte{0xB9, 0xF2, 0x23, 0x7E, 0xDE, 0x68, 0x63, 0x76}}
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

type powerBroadcastSetting struct {
	powerSetting windows.GUID
	dataLength   uint32
	data         [1]byte
}

// wtsInfoEx mirrors the head of WTSINFOEXW: Level, then the union whose
// LEVEL1 member starts 8-byte aligned (it holds LARGE_INTEGERs) with
// SessionId, SessionState and SessionFlags.
type wtsInfoEx struct {
	level        uint32
	_            [4]byte
	sessionID    uint32
	sessionState uint32
	sessionFlags int32
}

// startLockWatch creates the window and its loop; registration failures
// are reported as a warning and covered by a poll.
func startLockWatch(core *app.Core, logf func(string, ...any)) *lockWatch {
	l := &lockWatch{core: core, log: logf, stopped: make(chan struct{}), quit: make(chan struct{})}
	ready := make(chan error, 1)
	go l.loop(ready)
	if err := <-ready; err != nil {
		l.log("lock watch: %v", err)
		core.SetWarning(warnLockDetection, true)
		go l.poll()
	}
	return l
}

// loop is the message loop; it owns its thread for the life of the window.
func (l *lockWatch) loop(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(l.stopped)
	defer func() {
		if r := recover(); r != nil {
			l.log("lock watch panic: %v", r)
			l.core.LockNow(app.ReasonPanic)
		}
	}()
	inst, _, e := kernel32.NewProc("GetModuleHandleW").Call(0)
	if inst == 0 {
		ready <- fmt.Errorf("GetModuleHandle: %v", e)
		return
	}
	className, _ := windows.UTF16PtrFromString("EnfoldLockWatch")
	wc := wndClassEx{
		size:      uint32(unsafe.Sizeof(wndClassEx{})),
		wndProc:   windows.NewCallback(l.wndProc),
		instance:  windows.Handle(inst),
		className: className,
	}
	if r, _, e := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		ready <- fmt.Errorf("RegisterClassEx: %v", e)
		return
	}
	hwnd, _, e := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(className)),
		0, 0, 0, 0, 0, hwndMessage, 0, inst, 0)
	if hwnd == 0 {
		ready <- fmt.Errorf("CreateWindowEx: %v", e)
		return
	}
	l.hwnd = hwnd

	// WTS: checked, with a short retry (the service may still be starting
	// at logon); the poll covers a failure, so startup is not held up.
	var wtsErr error
	for attempt := 0; attempt < 3; attempt++ {
		r, _, e := procWTSRegisterSessionNotification.Call(hwnd, notifyForThisSession)
		if r != 0 {
			l.wtsOK = true
			break
		}
		wtsErr = fmt.Errorf("WTSRegisterSessionNotification: %v", e)
		time.Sleep(200 * time.Millisecond)
	}
	for _, g := range []*windows.GUID{&guidDisplayStatus, &guidUserPresence} {
		h, _, e := procRegisterPowerSettingNotification.Call(hwnd, uintptr(unsafe.Pointer(g)), deviceNotifyWindowHandle)
		if h == 0 {
			l.log("RegisterPowerSettingNotification: %v", e)
			continue
		}
		l.power = append(l.power, h)
	}
	if l.wtsOK {
		ready <- nil
	} else {
		ready <- wtsErr
	}

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || int32(r) == -1 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	for _, h := range l.power {
		procUnregisterPowerSettingNotification.Call(h)
	}
	if l.wtsOK {
		procWTSUnRegisterSessionNotification.Call(hwnd)
	}
}

// wndProc turns notifications into lock triggers. Every branch calls
// LockNow synchronously (the keys are zeroed before this returns) and
// leaves the unbounded work to the core.
func (l *lockWatch) wndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	defer func() {
		if r := recover(); r != nil {
			l.log("lock watch handler panic: %v", r)
			l.core.LockNow(app.ReasonPanic)
		}
	}()
	switch message {
	case wmWTSSessionChange:
		switch wParam {
		case windows.WTS_SESSION_LOCK, windows.WTS_SESSION_REMOTE_CONTROL:
			l.core.LockNow(app.ReasonWorkstation)
		case windows.WTS_CONSOLE_DISCONNECT, windows.WTS_REMOTE_DISCONNECT:
			l.core.LockNow(app.ReasonInactive)
		case windows.WTS_SESSION_LOGOFF:
			l.core.ResolveForShutdown(2 * time.Second)
			l.core.LockNow(app.ReasonLogoff)
		}
		return 0
	case wmPowerBroadcast:
		// Only the registered power-setting notifications arrive here.
		if wParam == pbtPowerSettingChange && lParam != 0 {
			pbs := (*powerBroadcastSetting)(ptr(lParam))
			if pbs.dataLength >= 1 {
				switch {
				case pbs.powerSetting == guidDisplayStatus && pbs.data[0] == 0:
					l.core.LockNow(app.ReasonDisplayOff)
				case pbs.powerSetting == guidUserPresence && pbs.data[0] == 2: // PowerUserInactive
					l.core.LockNow(app.ReasonInactive)
				}
			}
		}
		return 1 // TRUE
	case wmStopWatch:
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return r
}

// poll is the substitute when WTS registration failed: the session's lock
// flag every 5 s, until stop.
func (l *lockWatch) poll() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if locked, ok := sessionLocked(); ok && locked {
				l.core.LockNow(app.ReasonWorkstation)
			}
		case <-l.quit:
			return
		}
	}
}

// sessionLocked asks WTSQuerySessionInformation(WTSSessionInfoEx). ok is
// false when the answer is unusable (unknown state, wrong level).
func sessionLocked() (locked, ok bool) {
	var buf uintptr
	var n uint32
	r, _, _ := procWTSQuerySessionInformationW.Call(wtsCurrentServerHandle, wtsCurrentSession, wtsSessionInfoEx,
		uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&n)))
	if r == 0 || buf == 0 {
		return false, false
	}
	defer windows.WTSFreeMemory(buf)
	if uintptr(n) < unsafe.Sizeof(wtsInfoEx{}) {
		return false, false
	}
	info := (*wtsInfoEx)(ptr(buf))
	if info.level != 1 {
		return false, false
	}
	switch info.sessionFlags {
	case wtsSessionStateLocked:
		return true, true
	case wtsSessionStateUnlocked:
		return false, true
	}
	return false, false // WTS_SESSIONSTATE_UNKNOWN
}

// stop ends the loop and the poll; it is called from the shutdown hook.
func (l *lockWatch) stop() {
	l.once.Do(func() {
		close(l.quit)
		if l.hwnd != 0 {
			procPostMessageW.Call(l.hwnd, wmStopWatch, 0, 0)
		}
		select {
		case <-l.stopped:
		case <-time.After(time.Second):
		}
	})
}

// ptr turns a pointer the system returned as a uintptr back into a pointer.
// The memory is the system's, never Go's, so the garbage collector has no
// interest in it; the form is the one vet's unsafeptr check accepts.
func ptr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

// lastInput is the core's InputSource: GetLastInputInfo, so the page's
// activity heartbeat is granted only when the session saw input.
type lastInput struct{}

type lastInputInfo struct {
	size uint32
	time uint32
}

func (lastInput) LastInput() (time.Time, bool) {
	var lii lastInputInfo
	lii.size = uint32(unsafe.Sizeof(lii))
	r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&lii)))
	if r == 0 {
		return time.Time{}, false
	}
	tick, _, _ := procGetTickCount64.Call()
	elapsed := uint32(tick) - lii.time // both wrap at 32 bits; the difference is right
	return time.Now().Add(-time.Duration(elapsed) * time.Millisecond), true
}
