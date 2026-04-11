//go:build ignore

// gen-appicon generates the application icon PNG used by go-winres to embed the
// exe icon on Windows. Run via go:generate from cmd/mediafs/:
//
//	go run ../../tools/gen-appicon/main.go
package main

import (
	"log"
	"os"

	"github.com/CCoupel/Media_FS/internal/appicon"
)

func main() {
	data := appicon.GeneratePNG()
	if err := os.MkdirAll("winres", 0755); err != nil {
		log.Fatalf("mkdir winres: %v", err)
	}
	if err := os.WriteFile("winres/app-icon.png", data, 0644); err != nil {
		log.Fatalf("write winres/app-icon.png: %v", err)
	}
	log.Printf("wrote winres/app-icon.png (%d bytes)", len(data))
}
