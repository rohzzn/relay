package server

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/relay-monitor/relay/internal/alert"
	"github.com/relay-monitor/relay/internal/check"
	"github.com/relay-monitor/relay/internal/config"
	"github.com/relay-monitor/relay/internal/db"
	"github.com/relay-monitor/relay/internal/scheduler"
	"github.com/relay-monitor/relay/internal/state"
)

// Server is the HTTP server for the Relay admin and public status page.
type Server struct {
	cfg        *config.Config
	db         *db.DB
	scheduler  *scheduler.Scheduler
	dispatcher *alert.Dispatcher
	hub        *Hub
	tmpl       *template.Template
	webFS      fs.FS
	mux        *http.ServeMux
}

// New wires up the Server with all its dependencies.
func New(cfg *config.Config, database *db.DB, webFS fs.FS) (*Server, error) {
	hub := newHub()
	dispatcher := alert.New(database)

	// s is created first so that closures below can capture it safely.
	s := &Server{
		cfg:        cfg,
		db:         database,
		dispatcher: dispatcher,
		hub:        hub,
		webFS:      webFS,
	}

	// State manager emits events when monitor status changes.
	stateManager := state.New(database, func(evt state.Event) {
		dispatcher.Dispatch(evt)
		s.broadcastMonitorUpdate(evt.Monitor)
	})

	// Scheduler ticks per-monitor and feeds results into the state machine.
	s.scheduler = scheduler.New(database, cfg.CheckConcurrency, func(m *db.Monitor, r check.Result) {
		stateManager.Record(m, r)
	})

	if err := s.parseTemplates(); err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	s.routes()
	return s, nil
}

// Start launches the hub goroutine, loads all monitors into the scheduler,
// and begins serving HTTP on the configured port.
func (s *Server) Start() error {
	go s.hub.run()

	monitors, err := s.db.ListMonitors()
	if err != nil {
		return fmt.Errorf("list monitors: %w", err)
	}
	for _, m := range monitors {
		s.scheduler.Add(m)
	}

	addr := fmt.Sprintf(":%d", s.cfg.Port)
	log.Printf("relay: listening on %s", addr)

	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      s.mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	return httpSrv.ListenAndServe()
}

func (s *Server) routes() {
	mux := http.NewServeMux()

	// Static files
	staticFS, _ := fs.Sub(s.webFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// Public routes
	mux.HandleFunc("GET /{$}", s.handleStatus)
	mux.HandleFunc("POST /subscribe", s.handleSubscribe)
	mux.HandleFunc("GET /subscribe/confirm/{token}", s.handleSubscribeConfirm)
	mux.HandleFunc("GET /unsubscribe/{token}", s.handleUnsubscribe)
	mux.HandleFunc("POST /ping/{id}", s.handleHeartbeat)

	// Auth
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("POST /logout", s.handleLogout)

	// Admin — all require auth
	mux.HandleFunc("GET /admin", s.requireAuth(s.handleDashboard))
	mux.HandleFunc("GET /admin/", s.requireAuth(s.handleDashboard))

	mux.HandleFunc("GET /admin/monitors/new", s.requireAuth(s.handleMonitorNew))
	mux.HandleFunc("POST /admin/monitors", s.requireAuth(s.handleMonitorCreate))
	mux.HandleFunc("GET /admin/monitors/{id}/edit", s.requireAuth(s.handleMonitorEdit))
	mux.HandleFunc("POST /admin/monitors/{id}", s.requireAuth(s.handleMonitorUpdate))
	mux.HandleFunc("POST /admin/monitors/{id}/delete", s.requireAuth(s.handleMonitorDelete))

	mux.HandleFunc("GET /admin/incidents", s.requireAuth(s.handleIncidents))
	mux.HandleFunc("POST /admin/incidents", s.requireAuth(s.handleIncidentCreate))
	mux.HandleFunc("POST /admin/incidents/{id}", s.requireAuth(s.handleIncidentUpdate))
	mux.HandleFunc("POST /admin/incidents/{id}/resolve", s.requireAuth(s.handleIncidentResolve))

	mux.HandleFunc("GET /admin/channels", s.requireAuth(s.handleChannels))
	mux.HandleFunc("POST /admin/channels", s.requireAuth(s.handleChannelCreate))
	mux.HandleFunc("POST /admin/channels/{id}/delete", s.requireAuth(s.handleChannelDelete))

	mux.HandleFunc("GET /admin/settings", s.requireAuth(s.handleSettings))
	mux.HandleFunc("POST /admin/settings", s.requireAuth(s.handleSettingsSave))

	// WebSocket (admin only)
	mux.HandleFunc("GET /ws", s.requireAuth(s.hub.serveWS))

	// Healthcheck (used by Docker)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	s.mux = mux
}

// ── Template helpers ──────────────────────────────────────────────────────────

func (s *Server) parseTemplates() error {
	funcMap := template.FuncMap{
		"formatTime": func(ts int64) string {
			return time.Unix(ts, 0).Format("Jan 2, 2006 15:04 UTC")
		},
		"formatDuration": func(ts int64) string {
			d := time.Since(time.Unix(ts, 0))
			return formatDuration(d)
		},
		"uptimePct": func(pct float64) string {
			if pct < 0 {
				return "—"
			}
			return fmt.Sprintf("%.2f%%", pct)
		},
		"statusClass": func(status string) string {
			switch status {
			case "up":
				return "status-up"
			case "down":
				return "status-down"
			case "degraded":
				return "status-degraded"
			default:
				return "status-pending"
			}
		},
		"statusLabel": func(status string) string {
			switch status {
			case "up":
				return "Operational"
			case "down":
				return "Down"
			case "degraded":
				return "Degraded"
			default:
				return "Pending"
			}
		},
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"dict": func(vals ...any) map[string]any {
			m := make(map[string]any)
			for i := 0; i+1 < len(vals); i += 2 {
				m[fmt.Sprint(vals[i])] = vals[i+1]
			}
			return m
		},
		"round": func(f float64) int { return int(math.Round(f)) },
		"safeHTML": func(s string) template.HTML { return template.HTML(s) },
		"incidentStatusLabel": func(s string) string {
			labels := map[string]string{
				"investigating": "Investigating",
				"identified":    "Identified",
				"monitoring":    "Monitoring",
				"resolved":      "Resolved",
			}
			if l, ok := labels[s]; ok {
				return l
			}
			return s
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(s.webFS, "templates/partials/*.html", "templates/pages/*.html")
	if err != nil {
		return err
	}
	s.tmpl = tmpl
	return nil
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (s *Server) renderFragment(name string, data any) ([]byte, error) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// broadcastMonitorUpdate pushes an HTML fragment for the given monitor to all WS clients.
func (s *Server) broadcastMonitorUpdate(m *db.Monitor) {
	mv, err := s.buildMonitorView(m)
	if err != nil {
		return
	}
	frag, err := s.renderFragment("monitor-row", mv)
	if err != nil {
		return
	}
	s.hub.Broadcast(frag)
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

