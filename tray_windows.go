package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modshell32 = windows.NewLazySystemDLL("shell32.dll")
	moduser32   = windows.NewLazySystemDLL("user32.dll")
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procShellNotifyIconW   = modshell32.NewProc("Shell_NotifyIconW")
	procCreatePopupMenu    = moduser32.NewProc("CreatePopupMenu")
	procAppendMenuW        = moduser32.NewProc("AppendMenuW")
	procTrackPopupMenu     = moduser32.NewProc("TrackPopupMenu")
	procDestroyMenu        = moduser32.NewProc("DestroyMenu")
	procGetCursorPos       = moduser32.NewProc("GetCursorPos")
	procSetForegroundWindow = moduser32.NewProc("SetForegroundWindow")
	procRegisterClassExW   = moduser32.NewProc("RegisterClassExW")
	procCreateWindowExW    = moduser32.NewProc("CreateWindowExW")
	procDefWindowProcW     = moduser32.NewProc("DefWindowProcW")
	procGetMessageW        = moduser32.NewProc("GetMessageW")
	procTranslateMessage   = moduser32.NewProc("TranslateMessage")
	procDispatchMessageW   = moduser32.NewProc("DispatchMessageW")
	procPostQuitMessage    = moduser32.NewProc("PostQuitMessage")
	procDestroyWindow      = moduser32.NewProc("DestroyWindow")
	procPostMessageW       = moduser32.NewProc("PostMessageW")
	procLoadImageW         = moduser32.NewProc("LoadImageW")
	procGetModuleHandleW   = modkernel32.NewProc("GetModuleHandleW")
)

const (
	NIM_ADD        = 0x00000000
	NIM_MODIFY     = 0x00000001
	NIM_DELETE     = 0x00000002
	NIF_MESSAGE    = 0x00000001
	NIF_ICON       = 0x00000002
	NIF_TIP        = 0x00000004

	WM_APP         = 0x8000
	WM_TRAY_MSG    = WM_APP + 100
	WM_COMMAND     = 0x0111
	WM_LBUTTONUP   = 0x0202
	WM_LBUTTONDBLCLK = 0x0203
	WM_RBUTTONUP   = 0x0205

	MF_STRING      = 0x00000000
	MF_SEPARATOR   = 0x00000800
	MF_GRAYED      = 0x00000001
	MF_DISABLED    = 0x00000002

	TPM_BOTTOMALIGN = 0x0020
	TPM_LEFTALIGN   = 0x0000

	IMAGE_ICON     = 1
	LR_DEFAULTSIZE = 0x0040
	LR_SHARED      = 0x8000

	IDM_TITLE      = 1000
	IDM_OPEN       = 1001
	IDM_SYNC       = 1002
	IDM_STOP       = 1003
	IDM_QUIT       = 1004
)

type NOTIFYICONDATAW struct {
	CbSize           uint32
	HWnd             windows.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            windows.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	TimeoutOrVersion uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     windows.Handle
}

type POINT struct {
	X int32
	Y int32
}

type TrayManager struct {
	hwnd   windows.Handle
	nid    NOTIFYICONDATAW
	onOpen func()
	onSync func()
	onStop func()
	onQuit func()
	mu     sync.Mutex
}

var globalTray *TrayManager

func trayWndProc(hwnd windows.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	if msg == WM_TRAY_MSG {
		switch lParam {
		case WM_LBUTTONUP, WM_LBUTTONDBLCLK:
			if globalTray != nil && globalTray.onOpen != nil {
				go globalTray.onOpen()
			}
			return 0
		case WM_RBUTTONUP:
			if globalTray != nil {
				globalTray.showMenu(hwnd)
			}
			return 0
		}
	} else if msg == WM_COMMAND {
		id := uint32(wParam & 0xFFFF)
		if globalTray != nil {
			switch id {
			case IDM_OPEN:
				if globalTray.onOpen != nil {
					go globalTray.onOpen()
				}
			case IDM_SYNC:
				if globalTray.onSync != nil {
					go globalTray.onSync()
				}
			case IDM_STOP:
				if globalTray.onStop != nil {
					go globalTray.onStop()
				}
			case IDM_QUIT:
				if globalTray.onQuit != nil {
					globalTray.onQuit()
				} else {
					os.Exit(0)
				}
			}
		}
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return ret
}

func (tm *TrayManager) showMenu(hwnd windows.Handle) {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return
	}
	defer procDestroyMenu.Call(hMenu)

	titleStr, _ := windows.UTF16PtrFromString("Google Sync Lite")
	openStr, _ := windows.UTF16PtrFromString("Открыть интерфейс")
	syncStr, _ := windows.UTF16PtrFromString("Синхронизировать сейчас")
	stopStr, _ := windows.UTF16PtrFromString("Остановить")
	quitStr, _ := windows.UTF16PtrFromString("Выход")

	procAppendMenuW.Call(hMenu, MF_STRING|MF_DISABLED, uintptr(IDM_TITLE), uintptr(unsafe.Pointer(titleStr)))
	procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
	procAppendMenuW.Call(hMenu, MF_STRING, uintptr(IDM_OPEN), uintptr(unsafe.Pointer(openStr)))
	procAppendMenuW.Call(hMenu, MF_STRING, uintptr(IDM_SYNC), uintptr(unsafe.Pointer(syncStr)))
	procAppendMenuW.Call(hMenu, MF_STRING, uintptr(IDM_STOP), uintptr(unsafe.Pointer(stopStr)))
	procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
	procAppendMenuW.Call(hMenu, MF_STRING, uintptr(IDM_QUIT), uintptr(unsafe.Pointer(quitStr)))

	var pt POINT
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	procSetForegroundWindow.Call(uintptr(hwnd))
	procTrackPopupMenu.Call(hMenu, TPM_BOTTOMALIGN|TPM_LEFTALIGN, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hwnd), 0)
}

type WNDCLASSEXW struct {
	CbSize        uint32
	Style         uint32
	PfnWndProc    uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     windows.Handle
	HIcon         windows.Handle
	HCursor       windows.Handle
	HbrBackground windows.Handle
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       windows.Handle
}

func StartTray(onOpen, onSync, onStop, onQuit func()) (*TrayManager, error) {
	tm := &TrayManager{
		onOpen: onOpen,
		onSync: onSync,
		onStop: onStop,
		onQuit: onQuit,
	}
	globalTray = tm

	initChan := make(chan error, 1)

	go func() {
		// Native message loop must be bound to OS thread
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		hInst, _, _ := procGetModuleHandleW.Call(0)
		className, _ := windows.UTF16PtrFromString("GoogleSyncTrayWndClass")

		var wc WNDCLASSEXW
		wc.CbSize = uint32(unsafe.Sizeof(wc))
		wc.PfnWndProc = syscall.NewCallback(trayWndProc)
		wc.HInstance = windows.Handle(hInst)
		wc.LpszClassName = className

		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

		hwnd, _, _ := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(className)),
			uintptr(unsafe.Pointer(className)),
			0, 0, 0, 0, 0,
			0, 0, hInst, 0,
		)

		if hwnd == 0 {
			initChan <- fmt.Errorf("failed to create tray hidden window")
			return
		}
		tm.hwnd = windows.Handle(hwnd)

		// Load embedded icon (resource ID 1)
		hIcon, _, _ := procLoadImageW.Call(
			hInst,
			1, // resource ID 1 from rsrc.syso
			IMAGE_ICON,
			0, 0,
			LR_DEFAULTSIZE|LR_SHARED,
		)
		if hIcon == 0 {
			// Fallback to application default
			hIcon, _, _ = procLoadImageW.Call(0, 32512, IMAGE_ICON, 0, 0, LR_DEFAULTSIZE|LR_SHARED)
		}

		tm.nid.CbSize = uint32(unsafe.Sizeof(tm.nid))
		tm.nid.HWnd = tm.hwnd
		tm.nid.UID = 1
		tm.nid.UFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP
		tm.nid.UCallbackMessage = WM_TRAY_MSG
		tm.nid.HIcon = windows.Handle(hIcon)

		tipUTF16, _ := windows.UTF16FromString("Google Sync Lite")
		copy(tm.nid.SzTip[:], tipUTF16)

		res, _, _ := procShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&tm.nid)))
		if res == 0 {
			initChan <- fmt.Errorf("failed to call Shell_NotifyIconW")
			return
		}

		initChan <- nil

		// Message loop
		var msg struct {
			hwnd    windows.Handle
			message uint32
			wParam  uintptr
			lParam  uintptr
			time    uint32
			pt      POINT
		}

		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}

		// Remove icon on exit
		procShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&tm.nid)))
	}()

	err := <-initChan
	return tm, err
}

func (tm *TrayManager) UpdateTooltip(tip string) {
	if tm == nil || tm.hwnd == 0 {
		return
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tipUTF16, _ := windows.UTF16FromString(tip)
	copy(tm.nid.SzTip[:], tipUTF16)
	procShellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&tm.nid)))
}

func (tm *TrayManager) Stop() {
	if tm == nil || tm.hwnd == 0 {
		return
	}
	procShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&tm.nid)))
	procPostMessageW.Call(uintptr(tm.hwnd), 0x0010 /* WM_CLOSE */, 0, 0)
}
