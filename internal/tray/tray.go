package tray

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"fyne.io/systray"

	"github.com/CCoupel/Media_FS/internal/config"
)

// ServerStatus represents the current state of a mounted server.
type ServerStatus struct {
	Key     string
	Mounted bool
	Error   string
}

// Callbacks are called by the tray in response to user actions.
type Callbacks struct {
	OnMount        func(serverKey string)
	OnUnmount      func(serverKey string)
	OnMountAll     func()
	OnUnmountAll   func()
	OnOpenConfig   func() // opens the web config UI in the browser
	OnRefresh      func(serverKey string)
	OnQuit         func()
}

// Manager manages the system tray icon and menu.
type Manager struct {
	cfg      *config.Config
	cb       Callbacks
	statuses map[string]*ServerStatus

	// menu item references for dynamic updates
	serverItems map[string]*systray.MenuItem
}

// New creates a new tray Manager. Call Run() to start the tray event loop.
func New(cfg *config.Config, cb Callbacks) *Manager {
	return &Manager{
		cfg:         cfg,
		cb:          cb,
		statuses:    make(map[string]*ServerStatus),
		serverItems: make(map[string]*systray.MenuItem),
	}
}

// Run starts the systray event loop. Blocks until the tray is stopped.
func (m *Manager) Run() {
	systray.Run(m.onReady, m.onExit)
}

// UpdateStatus refreshes the menu item for a given server (green/red indicator).
func (m *Manager) UpdateStatus(key string, mounted bool, errMsg string) {
	m.statuses[key] = &ServerStatus{Key: key, Mounted: mounted, Error: errMsg}
	if item, ok := m.serverItems[key]; ok {
		item.SetTitle(serverLabel(key, mounted, errMsg))
	}
}

func (m *Manager) onReady() {
	systray.SetTitle("MediaFS")
	systray.SetTooltip("Media_FS — Virtual Media Library")
	setIcon(systray.SetIcon)

	// Per-server submenu
	for _, srv := range m.cfg.Servers {
		if !srv.Enabled {
			continue
		}
		key := srv.ServerKey()
		item := systray.AddMenuItem(serverLabel(key, false, ""), "")
		m.serverItems[key] = item

		mountItem := item.AddSubMenuItem("Mount", "")
		umountItem := item.AddSubMenuItem("Unmount", "")
		refreshItem := item.AddSubMenuItem("Refresh cache", "")

		go func(k string, mi, ui, ri *systray.MenuItem) {
			for {
				select {
				case <-mi.ClickedCh:
					if m.cb.OnMount != nil {
						m.cb.OnMount(k)
					}
				case <-ui.ClickedCh:
					if m.cb.OnUnmount != nil {
						m.cb.OnUnmount(k)
					}
				case <-ri.ClickedCh:
					if m.cb.OnRefresh != nil {
						m.cb.OnRefresh(k)
					}
				}
			}
		}(key, mountItem, umountItem, refreshItem)
	}

	systray.AddSeparator()

	mountAll := systray.AddMenuItem("Mount all", "")
	umountAll := systray.AddMenuItem("Unmount all", "")

	systray.AddSeparator()

	configItem := systray.AddMenuItem("Configuration…", "Open configuration in browser")
	quitItem := systray.AddMenuItem("Quit", "")

	go func() {
		for {
			select {
			case <-mountAll.ClickedCh:
				if m.cb.OnMountAll != nil {
					m.cb.OnMountAll()
				}
			case <-umountAll.ClickedCh:
				if m.cb.OnUnmountAll != nil {
					m.cb.OnUnmountAll()
				}
			case <-configItem.ClickedCh:
				if m.cb.OnOpenConfig != nil {
					m.cb.OnOpenConfig()
				}
			case <-quitItem.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

func (m *Manager) onExit() {
	if m.cb.OnQuit != nil {
		m.cb.OnQuit()
	}
}

func serverLabel(key string, mounted bool, errMsg string) string {
	indicator := "○"
	if mounted {
		indicator = "●"
	}
	if errMsg != "" {
		indicator = "✕"
	}
	return fmt.Sprintf("%s %s", indicator, key)
}

// setIcon sets the tray icon.
// Generates a minimal 16×16 PNG at runtime until real assets are embedded (issue #16).
func setIcon(fn func([]byte)) {
	fn(generateDefaultIcon())
}

func generateDefaultIcon() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	col := color.RGBA{R: 99, G: 102, B: 241, A: 255} // indigo — placeholder until #16
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, col)
		}
	}
	var pngBuf bytes.Buffer
	_ = png.Encode(&pngBuf, img)
	return wrapInICO(pngBuf.Bytes(), 16)
}

// wrapInICO wraps PNG bytes into a minimal ICO container (Vista+ PNG-in-ICO format).
// fyne.io/systray on Windows requires ICO format; PNG embedded in ICO is supported
// on Windows Vista and later via CreateIconFromResourceEx.
func wrapInICO(pngData []byte, size int) []byte {
	buf := make([]byte, 22+len(pngData))
	// ICO header
	binary.LittleEndian.PutUint16(buf[0:], 0) // reserved
	binary.LittleEndian.PutUint16(buf[2:], 1) // type = 1 (ICO)
	binary.LittleEndian.PutUint16(buf[4:], 1) // count = 1 image
	// Directory entry
	buf[6] = byte(size) // width
	buf[7] = byte(size) // height
	buf[8] = 0          // color count (0 = no palette)
	buf[9] = 0          // reserved
	binary.LittleEndian.PutUint16(buf[10:], 1)                    // planes
	binary.LittleEndian.PutUint16(buf[12:], 32)                   // bit count
	binary.LittleEndian.PutUint32(buf[14:], uint32(len(pngData))) // image data size
	binary.LittleEndian.PutUint32(buf[18:], 22)                   // image data offset
	copy(buf[22:], pngData)
	return buf
}
