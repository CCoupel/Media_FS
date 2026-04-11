//go:build !windows

// Package syscheck verifies runtime prerequisites (WinFSP on Windows, FUSE on Linux).
package syscheck

// WinFSPStatus describes the WinFSP installation state.
type WinFSPStatus struct {
	Present    bool   `json:"present"`
	Version    string `json:"version"`
	Compatible bool   `json:"compatible"`
	Note       string `json:"note,omitempty"`
}

// CheckWinFSP is a no-op on non-Windows platforms.
func CheckWinFSP() WinFSPStatus {
	return WinFSPStatus{Present: true, Version: "N/A", Compatible: true, Note: "Linux: FUSE"}
}
