// Package version holds the application version string.
// The default value "dev" is overridden at build time via:
//
//	go build -ldflags "-X github.com/CCoupel/Media_FS/internal/version.Version=v1.2.3"
package version

// Version is the current application version.
var Version = "dev"
