//go:build windows

package main

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procFindWindowW = moduser32.NewProc("FindWindowW")
	procShowWindow  = moduser32.NewProc("ShowWindow")
)

const swRestore = 9

// AcquireSingleInstanceLock attempts to obtain a system-wide named mutex.
// If an instance is already running, it brings the existing window to the foreground
// and returns false so the duplicate instance terminates immediately.
func AcquireSingleInstanceLock() (windows.Handle, bool) {
	mutexName, err := windows.UTF16PtrFromString("Local\\GoogleSyncLite_SingleInstance_Mutex")
	if err != nil {
		return 0, true
	}

	h, err := windows.CreateMutex(nil, true, mutexName)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		bringExistingInstanceToFront()
		if h != 0 {
			_ = windows.CloseHandle(h)
		}
		return 0, false
	}
	if err != nil && h == 0 {
		return 0, true
	}

	return h, true
}

func ReleaseSingleInstanceLock(h windows.Handle) {
	if h != 0 {
		_ = windows.CloseHandle(h)
	}
}

func bringExistingInstanceToFront() {
	titlePtr, _ := windows.UTF16PtrFromString("Google Sync Lite")
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(titlePtr)))
	if hwnd != 0 {
		procShowWindow.Call(hwnd, uintptr(swRestore))
		procSetForegroundWindow.Call(hwnd)
	}
}
