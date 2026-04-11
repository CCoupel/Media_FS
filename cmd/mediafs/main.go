package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/CCoupel/Media_FS/internal/cache"
	"github.com/CCoupel/Media_FS/internal/config"
	"github.com/CCoupel/Media_FS/internal/connector"
	_ "github.com/CCoupel/Media_FS/internal/connector/emby"
	_ "github.com/CCoupel/Media_FS/internal/connector/jellyfin"
	"github.com/CCoupel/Media_FS/internal/downloader"
	"github.com/CCoupel/Media_FS/internal/tray"
	"github.com/CCoupel/Media_FS/internal/vfs"
	"github.com/CCoupel/Media_FS/internal/webui"

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
				openConfigPopup(webSrv.URL())
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
	if err := connectServer(cfg, key, fs); err != nil {
		mgr.UpdateStatus(key, false, err.Error())
		return
	}
	mgr.UpdateStatus(key, true, "")
}

// connectServer connects a server and registers it in the VFS, without tray dependency.
func connectServer(cfg *config.Config, key string, fs *vfs.MediaFS) error {
	var srvCfg *config.ServerConfig
	for i := range cfg.Servers {
		if cfg.Servers[i].ServerKey() == key {
			srvCfg = &cfg.Servers[i]
			break
		}
	}
	if srvCfg == nil {
		return fmt.Errorf("server %q not found in config", key)
	}
	log.Printf("[mount] connecting %s (%s @ %s)", key, srvCfg.Type, srvCfg.URL)

	conn := connector.New(srvCfg.Type)
	if conn == nil {
		return fmt.Errorf("unknown connector type %q", srvCfg.Type)
	}
	if err := conn.Connect(*srvCfg); err != nil {
		log.Printf("[mount] connect error for %s: %v", key, err)
		return err
	}
	log.Printf("[mount] authenticated as %s", key)

	libs, err := conn.GetLibraries()
	if err != nil {
		log.Printf("[mount] GetLibraries error for %s: %v", key, err)
		return err
	}
	log.Printf("[mount] %s: %d libraries found", key, len(libs))
	for _, l := range libs {
		log.Printf("[mount]   lib: %q (id=%s type=%s)", l.Name, l.ID, l.Type)
	}

	fs.AddServer(&vfs.MountedServer{
		Key:       key,
		Conn:      conn,
		Libraries: libs,
	})
	log.Printf("[mount] %s registered in VFS", key)
	return nil
}

// --- CLI subcommands ---

func runCLI(args []string) {
	switch args[0] {
	case "mount":
		runMount(args[1:])
	case "umount", "unmount":
		fmt.Fprintln(os.Stderr, "umount: use the tray menu or kill the mediafs process")
	case "status":
		runStatus()
	case "refresh":
		runRefresh(args[1:])
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`Usage: mediafs [command] [server...]

Commands:
  mount [key...]    Mount servers headless (all enabled if no key given)
  umount [key...]   Unmount servers (use tray or kill process for now)
  status            Ping all configured servers and show reachability
  refresh [key...]  Invalidate cache (all servers if no key given)

Run without arguments to start with system tray.
`)
}

func runMount(keys []string) {
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
		HTTPClient:     &http.Client{Timeout: 0},
	}

	fs := vfs.New(cacheInst, dl)

	targets := keys
	if len(targets) == 0 {
		for _, srv := range cfg.Servers {
			if srv.Enabled {
				targets = append(targets, srv.ServerKey())
			}
		}
	}

	for _, key := range targets {
		if err := connectServer(cfg, key, fs); err != nil {
			log.Printf("  ✕ %s: %v", key, err)
		} else {
			log.Printf("  ● %s: connected", key)
		}
	}

	host := fuse.NewFileSystemHost(fs)
	var mountArgs []string
	if runtime.GOOS == "windows" {
		mountArgs = []string{cfg.Mount.DriveLetter + ":"}
	} else {
		mountArgs = []string{cfg.Mount.MountPoint}
	}
	log.Printf("Mounting at %s (Ctrl+C to unmount)...", mountArgs[0])
	if !host.Mount("", mountArgs) {
		log.Fatal("VFS mount failed")
	}
}

func runStatus() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if len(cfg.Servers) == 0 {
		fmt.Println("No servers configured.")
		return
	}
	for _, srv := range cfg.Servers {
		if !srv.Enabled {
			fmt.Printf("  ○ %s (disabled)\n", srv.ServerKey())
			continue
		}
		conn := connector.New(srv.Type)
		if conn == nil {
			fmt.Printf("  ✕ %s: unknown type %q\n", srv.ServerKey(), srv.Type)
			continue
		}
		if err := conn.Connect(srv); err != nil {
			fmt.Printf("  ✕ %s: %v\n", srv.ServerKey(), err)
			continue
		}
		if err := conn.Ping(); err != nil {
			fmt.Printf("  ✕ %s: unreachable (%v)\n", srv.ServerKey(), err)
			continue
		}
		fmt.Printf("  ● %s: reachable (%s)\n", srv.ServerKey(), srv.URL)
	}
}

func runRefresh(keys []string) {
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

	targets := keys
	if len(targets) == 0 {
		for _, srv := range cfg.Servers {
			targets = append(targets, srv.ServerKey())
		}
	}
	for _, key := range targets {
		if err := cacheInst.Invalidate(key); err != nil {
			fmt.Printf("  ✕ %s: %v\n", key, err)
		} else {
			fmt.Printf("  ✓ %s: cache cleared\n", key)
		}
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

// openConfigPopup opens the config UI as a minimal popup window using Edge app mode.
// Falls back to the system browser if Edge is not found.
func openConfigPopup(url string) {
	if runtime.GOOS == "windows" {
		edgePaths := []string{
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		}
		for _, path := range edgePaths {
			if _, err := os.Stat(path); err == nil {
				cmd := exec.Command(path,
					"--app="+url,
					"--window-size=980,760",
					"--window-position=80,60",
				)
				if cmd.Start() == nil {
					return
				}
			}
		}
	}
	openBrowser(url)
}
