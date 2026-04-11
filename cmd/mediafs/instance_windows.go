//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// singleInstanceMutex is kept alive for the lifetime of the process.
// Windows releases it automatically on exit, even after a crash.
var singleInstanceMutex windows.Handle

// ensureSingleInstance exits immediately if another instance of Media_FS is
// already running. Uses a named Windows mutex in the Local namespace so it
// works for the current user session (no UAC elevation required).
func ensureSingleInstance() {
	name, err := windows.UTF16PtrFromString("Local\\MediaFS_SingleInstance")
	if err != nil {
		return // can't verify — let the process continue
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		os.Exit(0) // another instance is running — exit silently
	}
	// Keep handle open; do not close it.
	singleInstanceMutex = h
}
