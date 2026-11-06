//go:build windows

package main

import (
	"sync"
	"strings"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	pRegisterClassExW = user32.NewProc("RegisterClassExW")
	pCreateWindowExW  = user32.NewProc("CreateWindowExW")
	pDefWindowProcW   = user32.NewProc("DefWindowProcW")
	pGetMessageW      = user32.NewProc("GetMessageW")
	pTranslateMessage = user32.NewProc("TranslateMessage")
	pDispatchMessageW = user32.NewProc("DispatchMessageW")
	pPostQuitMessage  = user32.NewProc("PostQuitMessage")
	pPostMessageW     = user32.NewProc("PostMessageW")
	pLoadImageW       = user32.NewProc("LoadImageW")
	pGetSystemMetrics = user32.NewProc("GetSystemMetrics")
	pCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	pAppendMenuW      = user32.NewProc("AppendMenuW")
	pTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	pDestroyMenu      = user32.NewProc("DestroyMenu")
	pGetCursorPos     = user32.NewProc("GetCursorPos")
	pSetForegroundWin = user32.NewProc("SetForegroundWindow")
	pShellNotifyIconW = shell32.NewProc("Shell_NotifyIconW")
	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")
)

const (
	wmApp        = 0x8000
	wmTray       = wmApp + 1
	wmQuitApp    = wmApp + 2
	wmClose      = 0x0010
	wmDestroy    = 0x0002
	wmLButtonUp  = 0x0202
	wmLButtonDbl = 0x0203
	wmRButtonUp  = 0x0205
	nimAdd       = 0
	nimDelete    = 2
	nifMessage   = 1
	nifIcon      = 2
	nifTip       = 4
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type point struct{ x, y int32 }

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
	private uint32
}

type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

var trayHwnd uintptr

func hidden(c *exec.Cmd) *exec.Cmd {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // no console window flashing up
	return c
}

// The window: Microsoft Edge in app mode (on every Windows 10/11 PC) shows the page like a normal program
// window, without tabs or an address bar. If Edge isn't there, the normal browser opens it.
// The Live Link's window, if one is open: a top-level window titled "TW Live Link" (the page's title) that belongs to
// Edge's app windows (class Chrome_WidgetWin_1). Used so starting the app again, or Open in the tray, brings that
// window to the front instead of opening another one.
var (
	pEnumWindows         = user32.NewProc("EnumWindows")
	pGetWindowTextW      = user32.NewProc("GetWindowTextW")
	pGetClassNameW       = user32.NewProc("GetClassNameW")
	pIsWindowVisible     = user32.NewProc("IsWindowVisible")
	pShowWindow          = user32.NewProc("ShowWindow")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pIsIconic            = user32.NewProc("IsIconic")
)

// (the search callback is made once: Windows allows only a limited number of them per program)
var foundWin uintptr
var enumCB = syscall.NewCallback(func(h uintptr, _ uintptr) uintptr {
	if v, _, _ := pIsWindowVisible.Call(h); v == 0 {
		return 1
	}
	buf := make([]uint16, 256)
	n, _, _ := pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 256)
	if n == 0 || !strings.HasPrefix(syscall.UTF16ToString(buf[:n]), "TW Live Link") {
		return 1
	}
	cls := make([]uint16, 128)
	m, _, _ := pGetClassNameW.Call(h, uintptr(unsafe.Pointer(&cls[0])), 128)
	if !strings.HasPrefix(syscall.UTF16ToString(cls[:m]), "Chrome_WidgetWin") {
		return 1
	}
	foundWin = h
	return 0 // stop: found it
})
var findMu sync.Mutex

func findOurWindow() uintptr {
	findMu.Lock()
	defer findMu.Unlock()
	foundWin = 0
	pEnumWindows.Call(enumCB, 0)
	return foundWin
}

// showWindow brings the open window to the front (restoring it if it was minimised); false if none is open
func showWindow() bool {
	h := findOurWindow()
	if h == 0 {
		return false
	}
	if ic, _, _ := pIsIconic.Call(h); ic != 0 {
		pShowWindow.Call(h, 9) // SW_RESTORE
	} else {
		pShowWindow.Call(h, 5) // SW_SHOW
	}
	pSetForegroundWindow.Call(h)
	return true
}

func openWindow(u string) {
	if showWindow() { // already open: bring it to the front rather than opening another
		return
	}
	cands := []string{}
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
		if d := os.Getenv(env); d != "" {
			cands = append(cands, filepath.Join(d, "Microsoft", "Edge", "Application", "msedge.exe"))
		}
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			if err := exec.Command(c, "--app="+u, "--window-size=470,800", "--new-window").Start(); err == nil {
				return
			}
		}
	}
	hidden(exec.Command("rundll32", "url.dll,FileProtocolHandler", u)).Start()
}

// Start with Windows: an entry in the user's Run key that starts the app in the background (tray only).
func setAutostart(on bool) {
	exe, _ := os.Executable()
	key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	if on {
		hidden(exec.Command("reg", "add", key, "/v", "TW Live Link", "/t", "REG_SZ", "/d", `"`+exe+`" --background`, "/f")).Run()
	} else {
		hidden(exec.Command("reg", "delete", key, "/v", "TW Live Link", "/f")).Run()
	}
}

func autostartPossible() bool { return true }

// the update: run the new installer silently (Windows asks for permission once), then open the new Live Link
// as this user again. A small script does it, so it carries on after this copy is closed by the installer.
func runInstallerAndRestart(setup string) error {
	exe, _ := os.Executable()
	script := filepath.Join(os.TempDir(), "tw-live-link-update.cmd")
	body := "@echo off\r\n" +
		"start \"\" /wait \"" + setup + "\" /S\r\n" +
		"start \"\" \"" + exe + "\"\r\n" +
		"del \"%~f0\"\r\n"
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		return err
	}
	return hidden(exec.Command("cmd", "/c", script)).Start()
}

// is the Start with Windows entry there (the installer may have made it)
func autostartActive() bool {
	err := hidden(exec.Command("reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "TW Live Link")).Run()
	return err == nil
}

// Which programs on this PC listen for UDP (telemetry apps wait for Forza's data that way): netstat for the
// ports, tasklist for the program names.
func scanUDPListeners() map[string][]int {
	out := map[string][]int{}
	ns, err := hidden(exec.Command("netstat", "-ano", "-p", "UDP")).Output()
	if err != nil {
		return out
	}
	tl, _ := hidden(exec.Command("tasklist", "/FO", "CSV", "/NH")).Output()
	return parseNetstat(string(ns), string(tl), os.Getpid())
}

// ---------------- the tray icon ----------------
func runTray(open func()) {
	runtime.LockOSThread()
	hInst, _, _ := pGetModuleHandleW.Call(0)
	cls, _ := syscall.UTF16PtrFromString("TWLiveLinkTray")
	proc := syscall.NewCallback(func(hwnd, m, wp, lp uintptr) uintptr {
		switch m {
		case wmTray:
			switch lp & 0xFFFF {
			case wmLButtonUp, wmLButtonDbl:
				go open()
			case wmRButtonUp:
				menu, _, _ := pCreatePopupMenu.Call()
				t1, _ := syscall.UTF16PtrFromString("Open TW Live Link")
				t2, _ := syscall.UTF16PtrFromString("Quit")
				pAppendMenuW.Call(menu, 0, 1, uintptr(unsafe.Pointer(t1)))
				pAppendMenuW.Call(menu, 0x800, 0, 0) // separator
				pAppendMenuW.Call(menu, 0, 2, uintptr(unsafe.Pointer(t2)))
				var pt point
				pGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
				pSetForegroundWin.Call(hwnd)
				cmd, _, _ := pTrackPopupMenu.Call(menu, 0x0100|0x0080, uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0)
				pDestroyMenu.Call(menu)
				if cmd == 1 {
					go open()
				} else if cmd == 2 {
					removeTray(hwnd)
					pPostQuitMessage.Call(0)
				}
			}
			return 0
		case wmQuitApp:
			removeTray(hwnd)
			pPostQuitMessage.Call(0)
			return 0
		case wmDestroy:
			pPostQuitMessage.Call(0)
			return 0
		}
		r, _, _ := pDefWindowProcW.Call(hwnd, m, wp, lp)
		return r
	})
	wc := wndClassEx{lpfnWndProc: proc, hInstance: hInst, lpszClassName: cls}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	title, _ := syscall.UTF16PtrFromString("TW Live Link")
	hwnd, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(title)), 0, 0, 0, 0, 0, 0, 0, hInst, 0)
	trayHwnd = hwnd
	// the tray icon in exactly the size the tray uses on this screen (16 px at 100%, 20 at 125%, 24 at 150%, 32 at
	// 200%), drawn for that size: not a big icon shrunk down. Icon 2 is the same with a green dot: shown while live.
	sz, _, _ := pGetSystemMetrics.Call(49) // SM_CXSMICON
	if sz == 0 {
		sz = 16
	}
	trayInst = hInst
	trayIcons[0], _, _ = pLoadImageW.Call(hInst, 1, 1, sz, sz, 0)
	trayIcons[1], _, _ = pLoadImageW.Call(hInst, 2, 1, sz, sz, 0)
	if trayIcons[1] == 0 {
		trayIcons[1] = trayIcons[0]
	}
	icon := trayIcons[0]
	nid := notifyIconData{hWnd: hwnd, uID: 1, uFlags: nifMessage | nifIcon | nifTip, uCallbackMessage: wmTray, hIcon: icon}
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	tip, _ := syscall.UTF16FromString("TW Live Link: click to open")
	copy(nid.szTip[:], tip)
	if r, _, err := pShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); r == 0 {
		log.Printf("tray icon failed: %v", err)
	}
	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	log.Printf("quitting")
	os.Exit(0)
}

var trayInst uintptr
var trayIcons [2]uintptr
var trayLiveNow = -1

// setTrayLive swaps the tray icon: with a green dot while you're live, without it otherwise
func setTrayLive(live bool) {
	if trayHwnd == 0 || trayIcons[0] == 0 {
		return
	}
	v := 0
	if live {
		v = 1
	}
	if v == trayLiveNow {
		return
	}
	trayLiveNow = v
	nid := notifyIconData{hWnd: trayHwnd, uID: 1, uFlags: nifIcon | nifTip, hIcon: trayIcons[v]}
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	tipText := "TW Live Link: click to open"
	if live {
		tipText = "TW Live Link: live"
	}
	tip, _ := syscall.UTF16FromString(tipText)
	copy(nid.szTip[:], tip)
	pShellNotifyIconW.Call(1, uintptr(unsafe.Pointer(&nid))) // NIM_MODIFY
}

func removeTray(hwnd uintptr) {
	nid := notifyIconData{hWnd: hwnd, uID: 1}
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	pShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func quitApp() {
	if trayHwnd != 0 {
		pPostMessageW.Call(trayHwnd, wmQuitApp, 0, 0)
		return
	}
	os.Exit(0)
}
