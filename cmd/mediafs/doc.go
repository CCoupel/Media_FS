// Package main is the Media_FS entry point.
//
// # Windows resources (.syso)
//
// The committed rsrc_windows_*.syso files embed the app icon and version
// metadata (version "dev"). They are used automatically by go build.
//
// To regenerate locally (requires go-winres):
//
//	cd cmd/mediafs && go generate
//
// In CI, the version is injected from the git tag before calling go-winres
// (see .github/workflows/release.yml — "Generate Windows resources" step).
//
//go:generate go run ../../tools/gen-appicon/main.go
//go:generate go-winres make
package main
