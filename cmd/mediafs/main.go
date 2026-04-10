package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"mediafs/internal/cache"
	"mediafs/internal/config"
	"mediafs/internal/connector"
	_ "mediafs/internal/connector/emby"
	_ "mediafs/internal/connector/jellyfin"
	"mediafs/internal/downloader"
	"mediafs/internal/tray"
	"mediafs/internal/vfs"
	"mediafs/internal/webui"

	"github.com/winfsp/cgofuse/fuse"
)

func main() {
	if len(os.Args) > 1 {
		runCLI(os.Args[1:])
		return
	}
	runTray()
}

// runTray is the default mode: system tray + background VFS.
func runTray() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	cacheInst, err := cache.Open(
		time.Duration(cfg.Cache.TTLItemsSec)*time.Second,
		time.Duration(cfg.Cache.TTLMetaSec)*time.Second,
		time.Duration(cfg.Cache.TTLArtworkSec)*time.Second,
	)
	if err != nil {
		log.Fatalf("cache: %v", err)
	}
	defer cacheInst.Close()

	dl := &downloader.Downloader{
		ParallelChunks: cfg.Download.ParallelChunks,
		ChunkSizeMB:    cfg.Download.ChunkSizeMB,
		BufferSizeKB:   cfg.Download.BufferSizeKB,
		HTTPClient:     &http.Client{Timeout: 0}, // no timeout for streaming
	}

	fs := vfs.New(cacheInst, dl)

	// Start web config UI
	webSrv, err := webui.New(cfg, func(newCfg *config.Config) error {
		return newCfg.Save()
	})
	if err != nil {
		log.Printf("webui: %v (config UI unavailable)", err)
	} else {
		webSrv.Start()
	}

	// Declare trayMgr before closures so they can capture it by reference.
	var trayMgr *tray.Manager
	trayMgr = tray.New(cfg, tray.Callbacks{
		OnMountAll:   func() { mountAll(cfg, fs, trayMgr) },
		OnUnmountAll: func() { /* TODO */ },
		OnMount:      func(key string) { mountServer(cfg, key, fs, trayMgr) },
		OnUnmount:    func(key string) { /* TODO */ },
		OnRefresh:    func(key string) { cacheInst.Invalidate(key) },
		OnOpenConfig: func() {
			if webSrv != nil {
				openBrowser(webSrv.URL())
			}
		},
		OnQuit: func() { os.Exit(0) },
	})

	// Auto-mount on start
	go mountAll(cfg, fs, trayMgr)

	// Start VFS in background
	go func() {
		host := fuse.NewFileSystemHost(fs)
		var args []string
		if runtime.GOOS == "windows" {
			args = []string{cfg.Mount.DriveLetter + ":"}
		} else {
			args = []string{cfg.Mount.MountPoint}
		}
		if !host.Mount("", args) {
			log.Println("VFS mount failed")
		}
	}()

	trayMgr.Run() // blocks
}

func mountAll(cfg *config.Config, fs *vfs.MediaFS, mgr *tray.Manager) {
	for _, srv := range cfg.Servers {
		if srv.Enabled {
			mountServer(cfg, srv.ServerKey(), fs, mgr)
		}
	}
}

func mountServer(cfg *config.Config, key string, fs *vfs.MediaFS, mgr *tray.Manager) {
	var srvCfg *config.ServerConfig
	for i := range cfg.Servers {
		if cfg.Servers[i].ServerKey() == key {
			srvCfg = &cfg.Servers[i]
			break
		}
	}
	if srvCfg == nil {
		return
	}

	conn := connector.New(srvCfg.Type)
	if conn == nil {
		mgr.UpdateStatus(key, false, fmt.Sprintf("unknown connector type %q", srvCfg.Type))
		return
	}
	if err := conn.Connect(*srvCfg); err != nil {
		mgr.UpdateStatus(key, false, err.Error())
		return
	}

	libs, err := conn.GetLibraries()
	if err != nil {
		mgr.UpdateStatus(key, false, err.Error())
		return
	}

	fs.AddServer(&vfs.MountedServer{
		Key:       key,
		Conn:      conn,
		Libraries: libs,
	})
	mgr.UpdateStatus(key, true, "")
}

// --- CLI subcommands ---

func runCLI(args []string) {
	switch args[0] {
	case "mount":
		fmt.Println("mount: use the tray UI or run mediafs without arguments")
	case "umount", "unmount":
		fmt.Println("unmount: not yet implemented via CLI")
	case "status":
		fmt.Println("status: not yet implemented via CLI")
	case "refresh":
		fmt.Println("refresh: not yet implemented via CLI")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		os.Exit(1)
	}
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
