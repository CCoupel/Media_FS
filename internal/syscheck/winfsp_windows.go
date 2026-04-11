// Package syscheck verifies runtime prerequisites (WinFSP on Windows, FUSE on Linux).
package syscheck

import (
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// MinWinFSPMajor is the minimum WinFSP major version required.
const MinWinFSPMajor = 1

// WinFSPStatus describes the WinFSP installation state.
type WinFSPStatus struct {
	Present    bool   `json:"present"`
	Version    string `json:"version"`    // e.g. "2.1.25156"
	Compatible bool   `json:"compatible"` // major >= MinWinFSPMajor
	Note       string `json:"note,omitempty"`
}

// CheckWinFSP reads the Windows registry to determine whether WinFSP is
// installed and extracts the version from its DLL.
func CheckWinFSP() WinFSPStatus {
	installDir, err := readInstallDir()
	if err != nil {
		return WinFSPStatus{Present: false, Note: "WinFSP introuvable — installez-le depuis winfsp.net"}
	}

	version, err := dllVersion(filepath.Join(installDir, `bin\winfsp-x64.dll`))
	if err != nil {
		// Installed but version unreadable — treat as present + compatible.
		return WinFSPStatus{Present: true, Version: "?", Compatible: true}
	}

	compatible := version[0] >= MinWinFSPMajor
	note := ""
	if !compatible {
		note = fmt.Sprintf("version %d.%d trop ancienne — requis ≥ %d.x", version[0], version[1], MinWinFSPMajor)
	}
	ver := fmt.Sprintf("%d.%d.%d", version[0], version[1], version[2])
	return WinFSPStatus{Present: true, Version: ver, Compatible: compatible, Note: note}
}

// readInstallDir returns the WinFSP InstallDir from the registry.
func readInstallDir() (string, error) {
	candidates := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\WinFsp`},
		{registry.LOCAL_MACHINE, `SOFTWARE\WinFsp`},
	}
	for _, c := range candidates {
		k, err := registry.OpenKey(c.root, c.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("InstallDir")
		k.Close()
		if err == nil && v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("WinFSP registry key not found")
}

// dllVersion reads the file version from a PE DLL using version.dll APIs.
// Returns [major, minor, patch, build].
func dllVersion(path string) ([4]uint32, error) {
	var zero windows.Handle
	size, err := windows.GetFileVersionInfoSize(path, &zero)
	if err != nil || size == 0 {
		return [4]uint32{}, fmt.Errorf("GetFileVersionInfoSize: %w", err)
	}

	buf := make([]byte, size)
	if err := windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&buf[0])); err != nil {
		return [4]uint32{}, fmt.Errorf("GetFileVersionInfo: %w", err)
	}

	var ffi *windows.VS_FIXEDFILEINFO
	var ffiLen uint32
	if err := windows.VerQueryValue(unsafe.Pointer(&buf[0]), `\`, unsafe.Pointer(&ffi), &ffiLen); err != nil {
		return [4]uint32{}, fmt.Errorf("VerQueryValue: %w", err)
	}

	return [4]uint32{
		ffi.FileVersionMS >> 16,
		ffi.FileVersionMS & 0xffff,
		ffi.FileVersionLS >> 16,
		ffi.FileVersionLS & 0xffff,
	}, nil
}
