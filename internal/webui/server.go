// Package webui provides a local HTTP configuration interface opened in the
// system browser when the user clicks "Configuration" in the tray menu.
package webui

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/CCoupel/Media_FS/internal/config"
	"github.com/CCoupel/Media_FS/internal/connector"
	_ "github.com/CCoupel/Media_FS/internal/connector/emby"
	_ "github.com/CCoupel/Media_FS/internal/connector/jellyfin"
)

//go:embed templates/*
var templateFS embed.FS

// Server is the local HTTP config server.
type Server struct {
	cfg      *config.Config
	onSave   func(*config.Config) error // called when the user saves config
	listener net.Listener
	port     int
	tmpl     *template.Template
}

// New creates a config server. onSave is called (with the new config) when
// the user submits the configuration form.
func New(cfg *config.Config, onSave func(*config.Config) error) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0") // random available port
	if err != nil {
		return nil, err
	}

	tmpl, err := template.New("").Funcs(template.FuncMap{
		"upper": strings.ToUpper,
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:      cfg,
		onSave:   onSave,
		listener: ln,
		port:     ln.Addr().(*net.TCPAddr).Port,
		tmpl:     tmpl,
	}
	return s, nil
}

// Start begins serving in the background.
func (s *Server) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/servers", s.handleServers)
	mux.HandleFunc("/api/servers/add", s.handleAddServer)
	mux.HandleFunc("/api/servers/update", s.handleUpdateServer)
	mux.HandleFunc("/api/servers/remove", s.handleRemoveServer)
	mux.HandleFunc("/api/servers/test", s.handleTestServer)
	mux.HandleFunc("/api/save", s.handleSave)

	go http.Serve(s.listener, mux) //nolint:errcheck
}

// URL returns the base URL of the config UI.
func (s *Server) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.port)
}

// Stop shuts down the HTTP server.
func (s *Server) Stop() {
	s.listener.Close()
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"Servers":     s.cfg.Servers,
		"MountConfig": s.cfg.Mount,
		"Download":    s.cfg.Download,
	}
	if err := s.tmpl.ExecuteTemplate(w, "config.html", data); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func (s *Server) handleServers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.cfg.Servers)
}

func (s *Server) handleAddServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var srv config.ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&srv); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	srv.Enabled = true
	s.cfg.Servers = append(s.cfg.Servers, srv)
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleRemoveServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var body struct{ Alias string }
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	updated := s.cfg.Servers[:0]
	for _, srv := range s.cfg.Servers {
		if srv.Alias != body.Alias {
			updated = append(updated, srv)
		}
	}
	s.cfg.Servers = updated
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleUpdateServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var srv config.ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&srv); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	for i, existing := range s.cfg.Servers {
		if existing.Alias == srv.Alias {
			s.cfg.Servers[i] = srv
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	http.Error(w, fmt.Sprintf("server %q not found", srv.Alias), 404)
}

// testLevel represents the outcome of a connectivity test.
// "ok" = green, "auth_error" = orange, "unreachable" = red.
type testLevel struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

func writeTestLevel(w http.ResponseWriter, status, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(testLevel{Status: status, Message: msg})
}

func (s *Server) handleTestServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	var srv config.ServerConfig
	if err := json.NewDecoder(r.Body).Decode(&srv); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	// Step 1 — reachability: hit the public info endpoint (no auth required)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(srv.URL + "/System/Info/Public")
	if err != nil {
		writeTestLevel(w, "unreachable", err.Error())
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		writeTestLevel(w, "unreachable", fmt.Sprintf("server error %d", resp.StatusCode))
		return
	}

	// Step 2 — authentication: Connect resolves the user ID (requires valid API key)
	conn := connector.New(srv.Type)
	if conn == nil {
		writeTestLevel(w, "unreachable", fmt.Sprintf("type inconnu : %s", srv.Type))
		return
	}
	if err := conn.Connect(srv); err != nil {
		writeTestLevel(w, "auth_error", err.Error())
		return
	}

	writeTestLevel(w, "ok", "")
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	if err := s.onSave(s.cfg); err != nil {
		jsonError(w, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}

func jsonError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(400)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
