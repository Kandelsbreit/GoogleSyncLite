//go:build !windows

package main

func AcquireSingleInstanceLock() (uintptr, bool) {
	return 0, true
}

func ReleaseSingleInstanceLock(h uintptr) {}
