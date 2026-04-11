//go:build !windows

package main

func ensureSingleInstance() {} // no-op on non-Windows platforms
