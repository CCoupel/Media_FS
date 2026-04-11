package tray

import (
	"fmt"
	"sync"

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
	OnMount      func(serverKey string)
	OnUnmount    func(serverKey string)
	OnMountAll   func()
	OnUnmountAll func()
	OnOpenConfig func() // opens the web config UI in the browser
	OnRefresh    func(serverKey string)
	OnQuit       func()
}

// Manager manages the system tray icon and menu.
type Manager struct {
	cfg      *config.Config
	cb       Callbacks
	statuses map[string]*ServerStatus

	// menu item references for dynamic updates
	serverItems map[string]*systray.MenuItem

	// icon state
	mu       sync.Mutex
	ready    bool
	state    TrayState
	winfspOK bool // false → always show Error regardless of server state
}

// New creates a new tray Manager. Call Run() to start the tray event loop.
func New(cfg *config.Config, cb Callbacks) *Manager {
	return &Manager{
		cfg:         cfg,
		cb:          cb,
		statuses:    make(map[string]*ServerStatus),
		serverItems: make(map[string]*systray.MenuItem),
		winfspOK:    true,
	}
}

// MarkSysError forces the tray into permanent Error state (e.g. WinFSP absent).
// Safe to call from any goroutine at any time.
func (m *Manager) MarkSysError() {
	m.mu.Lock()
	m.winfspOK = false
	m.mu.Unlock()
	m.SetState(TrayStateError)
}

// Run starts the systray event loop. Blocks until the tray is stopped.
func (m *Manager) Run() {
	systray.Run(m.onReady, m.onExit)
}

// UpdateStatus refreshes the menu item for a given server and recomputes the
// tray icon state from all known server statuses.
func (m *Manager) UpdateStatus(key string, mounted bool, errMsg string) {
	m.statuses[key] = &ServerStatus{Key: key, Mounted: mounted, Error: errMsg}
	if item, ok := m.serverItems[key]; ok {
		item.SetTitle(serverLabel(key, mounted, errMsg))
	}
	m.updateTrayState()
}

// SetState updates the tray icon to reflect the given state.
// Safe to call from any goroutine after Run() has been called.
func (m *Manager) SetState(state TrayState) {
	m.mu.Lock()
	m.state = state
	rdy := m.ready
	m.mu.Unlock()
	if rdy {
		systray.SetIcon(generateTrayIcon(state))
	}
}

// updateTrayState recomputes the TrayState from current server statuses and
// calls SetState. Logic:
//   - all servers mounted, no errors → Active (green ▶)
//   - some servers failed, some OK   → Warning (orange ▶)
//   - all servers failed             → Error (red ✕)
//   - no servers mounted, no errors  → Idle (grey ⏸)
func (m *Manager) updateTrayState() {
	m.mu.Lock()
	winfspOK := m.winfspOK
	m.mu.Unlock()
	if !winfspOK {
		m.SetState(TrayStateError)
		return
	}

	mounted, errored, enabled := 0, 0, 0
	for _, srv := range m.cfg.Servers {
		if !srv.Enabled {
			continue
		}
		enabled++
		if s, ok := m.statuses[srv.ServerKey()]; ok {
			if s.Error != "" {
				errored++
			} else if s.Mounted {
				mounted++
			}
		}
	}

	var state TrayState
	switch {
	case enabled == 0:
		state = TrayStateIdle
	case errored == enabled:
		state = TrayStateError
	case errored > 0 || (mounted > 0 && mounted < enabled):
		state = TrayStateWarning
	case mounted == enabled:
		state = TrayStateActive
	default:
		state = TrayStateIdle
	}
	m.SetState(state)
}

func (m *Manager) onReady() {
	systray.SetTitle("MediaFS")
	systray.SetTooltip("Media_FS — Virtual Media Library")

	// Initialise icon with current state (Idle until first mount completes).
	m.mu.Lock()
	m.ready = true
	state := m.state
	m.mu.Unlock()
	systray.SetIcon(generateTrayIcon(state))

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
