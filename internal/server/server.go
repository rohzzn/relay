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

	"github.com/rohzzn/relay/internal/alert"
	"github.com/rohzzn/relay/internal/check"
	"github.com/rohzzn/relay/internal/config"
	"github.com/rohzzn/relay/internal/db"
	"github.com/rohzzn/relay/internal/scheduler"
	"github.com/rohzzn/relay/internal/state"
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

	s := &Server{
		cfg:        cfg,
		db:         database,
		dispatcher: dispatcher,
		hub:        hub,
		webFS:      webFS,
	}

	stateManager := state.New(database, func(evt state.Event) {
		dispatcher.Dispatch(evt)
		s.broadcastMonitorUpdate(evt.Monitor)
	})

	s.scheduler = scheduler.New(database, cfg.CheckConcurrency, func(m *db.Monitor, r check.Result, inMaintenance bool) {
		stateManager.Record(m, r, inMaintenance)
		// Always broadcast visual update even during maintenance.
		s.broadcastMonitorUpdate(m)
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
		if !m.Paused {
			s.scheduler.Add(m)
		}
	}

	addr := fmt.Sprintf(":%d", s.cfg.Port)
	log.Printf("relay: listening on %s", addr)

	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      s.recoveryMiddleware(s.mux),
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
	mux.HandleFunc("GET /history", s.handleHistory)
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
	mux.HandleFunc("POST /admin/monitors/{id}/pause", s.requireAuth(s.handleMonitorPause))
	mux.HandleFunc("POST /admin/monitors/{id}/test", s.requireAuth(s.handleMonitorTest))

	mux.HandleFunc("GET /admin/incidents", s.requireAuth(s.handleIncidents))
	mux.HandleFunc("POST /admin/incidents", s.requireAuth(s.handleIncidentCreate))
	mux.HandleFunc("POST /admin/incidents/{id}", s.requireAuth(s.handleIncidentUpdate))
	mux.HandleFunc("POST /admin/incidents/{id}/resolve", s.requireAuth(s.handleIncidentResolve))

	mux.HandleFunc("GET /admin/channels", s.requireAuth(s.handleChannels))
	mux.HandleFunc("POST /admin/channels", s.requireAuth(s.handleChannelCreate))
	mux.HandleFunc("POST /admin/channels/{id}/delete", s.requireAuth(s.handleChannelDelete))
	mux.HandleFunc("POST /admin/channels/{id}/test", s.requireAuth(s.handleChannelTest))

	mux.HandleFunc("GET /admin/maintenance", s.requireAuth(s.handleMaintenance))
	mux.HandleFunc("POST /admin/maintenance", s.requireAuth(s.handleMaintenanceCreate))
	mux.HandleFunc("POST /admin/maintenance/{id}/delete", s.requireAuth(s.handleMaintenanceDelete))

	mux.HandleFunc("GET /admin/users", s.requireAuth(s.handleUsers))
	mux.HandleFunc("POST /admin/users", s.requireAuth(s.handleUserCreate))
	mux.HandleFunc("POST /admin/users/{id}/delete", s.requireAuth(s.handleUserDelete))
	mux.HandleFunc("POST /admin/users/{id}/role", s.requireAuth(s.handleUserRole))

	mux.HandleFunc("GET /admin/audit", s.requireAuth(s.handleAuditLog))

	mux.HandleFunc("GET /admin/settings", s.requireAuth(s.handleSettings))
	mux.HandleFunc("POST /admin/settings", s.requireAuth(s.handleSettingsSave))
	mux.HandleFunc("POST /admin/api-keys", s.requireAuth(s.handleAPIKeyCreate))
	mux.HandleFunc("POST /admin/api-keys/{id}/delete", s.requireAuth(s.handleAPIKeyDelete))

	// REST API v1 — authenticated via API key or session
	mux.HandleFunc("GET /api/v1/status", s.requireAPIOrSession(s.handleAPIStatus))
	mux.HandleFunc("GET /api/v1/monitors", s.requireAPIOrSession(s.handleAPIListMonitors))
	mux.HandleFunc("GET /api/v1/monitors/{id}", s.requireAPIOrSession(s.handleAPIGetMonitor))
	mux.HandleFunc("POST /api/v1/monitors", s.requireAPIOrSession(s.handleAPICreateMonitor))
	mux.HandleFunc("PUT /api/v1/monitors/{id}", s.requireAPIOrSession(s.handleAPIUpdateMonitor))
	mux.HandleFunc("DELETE /api/v1/monitors/{id}", s.requireAPIOrSession(s.handleAPIDeleteMonitor))
	mux.HandleFunc("GET /api/v1/incidents", s.requireAPIOrSession(s.handleAPIListIncidents))
	mux.HandleFunc("POST /api/v1/incidents", s.requireAPIOrSession(s.handleAPICreateIncident))
	mux.HandleFunc("GET /api/v1/monitors/{id}/metrics", s.requireAPIOrSession(s.handleAPIMonitorMetrics))

	// WebSocket (admin only)
	mux.HandleFunc("GET /ws", s.requireAuth(s.hub.serveWS))

	// Healthcheck
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// Catch-all 404
	mux.HandleFunc("/", s.handleNotFound)

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
			case "paused":
				return "Paused"
			default:
				return "Pending"
			}
		},
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"mul": func(a, b int) int { return a * b },
		"dict": func(vals ...any) map[string]any {
			m := make(map[string]any)
			for i := 0; i+1 < len(vals); i += 2 {
				m[fmt.Sprint(vals[i])] = vals[i+1]
			}
			return m
		},
		"round":    func(f float64) int { return int(math.Round(f)) },
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
		"roleLabel": func(r string) string {
			switch r {
			case "admin":
				return "Admin"
			case "editor":
				return "Editor"
			default:
				return "Viewer"
			}
		},
		"formatUnixTime": func(ts int64) string {
			if ts == 0 {
				return "Never"
			}
			return time.Unix(ts, 0).Format("Jan 2, 2006 15:04")
		},
		"isActive": func(startsAt, endsAt int64) bool {
			now := time.Now().Unix()
			return startsAt <= now && endsAt >= now
		},
		"isFuture": func(startsAt int64) bool {
			return startsAt > time.Now().Unix()
		},
		"initial": func(s string) string {
			if s == "" {
				return "?"
			}
			for _, r := range s {
				return string(r)
			}
			return "?"
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(s.webFS,
		"templates/partials/*.html",
		"templates/pages/*.html",
	)
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

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderWithStatus(w, http.StatusNotFound, "page-404", map[string]any{"Site": s.siteData()})
}

func (s *Server) recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic recovered: %v", rec)
				s.renderWithStatus(w, http.StatusInternalServerError, "page-500", map[string]any{"Site": s.siteData()})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) renderWithStatus(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
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
