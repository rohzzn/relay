package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/relay-monitor/relay/internal/db"
)

// ── View types ────────────────────────────────────────────────────────────────

type MonitorView struct {
	*db.Monitor
	LastCheck   *db.Check
	Uptime24h   float64
	Uptime7d    float64
	Uptime30d   float64
	Uptime90d   float64
	UptimeDays  []db.DayUptime
	RecentChecks []*db.Check
	AvgLatency  int64
}

type DashboardData struct {
	Site      SiteData
	Monitors  []*MonitorView
	Stats     Stats
	Incidents []*db.Incident
}

type SiteData struct {
	Name string
	URL  string
	Logo string
}

type Stats struct {
	Total    int
	Up       int
	Down     int
	Degraded int
	Pending  int
}

// ── Dashboard ─────────────────────────────────────────────────────────────────

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.db.ListMonitors()
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	views := make([]*MonitorView, 0, len(monitors))
	stats := Stats{Total: len(monitors)}
	for _, m := range monitors {
		mv, _ := s.buildMonitorView(m)
		views = append(views, mv)
		switch m.Status {
		case "up":
			stats.Up++
		case "down":
			stats.Down++
		case "degraded":
			stats.Degraded++
		default:
			stats.Pending++
		}
	}

	incidents, _ := s.db.ListIncidents(true)

	s.render(w, "page-dashboard", DashboardData{
		Site:      s.siteData(),
		Monitors:  views,
		Stats:     stats,
		Incidents: incidents,
	})
}

func (s *Server) buildMonitorView(m *db.Monitor) (*MonitorView, error) {
	mv := &MonitorView{Monitor: m}
	mv.LastCheck, _ = s.db.GetLatestCheck(m.ID)
	mv.Uptime24h = s.db.GetUptimePct(m.ID, 1)
	mv.Uptime7d = s.db.GetUptimePct(m.ID, 7)
	mv.Uptime30d = s.db.GetUptimePct(m.ID, 30)
	mv.Uptime90d = s.db.GetUptimePct(m.ID, 90)
	mv.UptimeDays, _ = s.db.GetDailyUptime(m.ID, 90)
	mv.RecentChecks, _ = s.db.ListChecks(m.ID, 20)
	mv.AvgLatency = s.db.GetAvgLatency(m.ID, 24)
	return mv, nil
}

func (s *Server) siteData() SiteData {
	return SiteData{
		Name: s.cfg.SiteName,
		URL:  s.cfg.SiteURL,
		Logo: s.cfg.LogoURL,
	}
}

// ── Monitor CRUD ─────────────────────────────────────────────────────────────

func (s *Server) handleMonitorNew(w http.ResponseWriter, r *http.Request) {
	s.render(w, "page-monitor-form", map[string]any{
		"Site":    s.siteData(),
		"Monitor": nil,
		"Action":  "/admin/monitors",
		"Title":   "Add Monitor",
	})
}

func (s *Server) handleMonitorCreate(w http.ResponseWriter, r *http.Request) {
	m := monitorFromForm(r)
	if err := s.db.CreateMonitor(m); err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.scheduler.Add(m)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleMonitorEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := s.db.GetMonitor(id)
	if err != nil || m == nil {
		http.NotFound(w, r)
		return
	}

	// Decode config JSON for editing
	var cfg map[string]any
	json.Unmarshal([]byte(m.Config), &cfg)

	s.render(w, "page-monitor-form", map[string]any{
		"Site":    s.siteData(),
		"Monitor": m,
		"Config":  cfg,
		"Action":  fmt.Sprintf("/admin/monitors/%s", m.ID),
		"Title":   "Edit Monitor",
	})
}

func (s *Server) handleMonitorUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m := monitorFromForm(r)
	m.ID = id
	if err := s.db.UpdateMonitor(m); err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.scheduler.Update(m)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleMonitorDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.scheduler.Remove(id)
	s.db.DeleteMonitor(id)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func monitorFromForm(r *http.Request) *db.Monitor {
	r.ParseForm()
	intervalS, _ := strconv.Atoi(r.FormValue("interval_s"))
	if intervalS < 30 {
		intervalS = 60
	}

	// Build config JSON from individual form fields.
	cfg := map[string]any{}
	if v := r.FormValue("timeout_s"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg["timeout_s"] = n
		}
	}
	if v := r.FormValue("keyword"); v != "" {
		cfg["keyword"] = v
	}
	if v := r.FormValue("expected_status"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg["expected_status"] = n
		}
	}
	if v := r.FormValue("warn_days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg["warn_days"] = n
		}
	}
	cfgJSON, _ := json.Marshal(cfg)

	return &db.Monitor{
		Name:      r.FormValue("name"),
		Type:      r.FormValue("type"),
		Target:    r.FormValue("target"),
		IntervalS: intervalS,
		Regions:   "local",
		Config:    string(cfgJSON),
	}
}

// ── Incidents ─────────────────────────────────────────────────────────────────

func (s *Server) handleIncidents(w http.ResponseWriter, r *http.Request) {
	incidents, _ := s.db.ListIncidents(false)
	monitors, _ := s.db.ListMonitors()
	s.render(w, "page-incidents", map[string]any{
		"Site":      s.siteData(),
		"Incidents": incidents,
		"Monitors":  monitors,
	})
}

func (s *Server) handleIncidentCreate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	inc := &db.Incident{
		Title:     r.FormValue("title"),
		Status:    r.FormValue("status"),
		StartedAt: time.Now().Unix(),
	}
	if inc.Status == "" {
		inc.Status = "investigating"
	}
	inc.Body.Valid = true
	inc.Body.String = r.FormValue("body")
	if mid := r.FormValue("monitor_id"); mid != "" {
		inc.MonitorID.Valid = true
		inc.MonitorID.String = mid
	}
	s.db.CreateIncident(inc)
	http.Redirect(w, r, "/admin/incidents", http.StatusSeeOther)
}

func (s *Server) handleIncidentUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inc, err := s.db.GetIncident(id)
	if err != nil || inc == nil {
		http.NotFound(w, r)
		return
	}
	r.ParseForm()
	if v := r.FormValue("title"); v != "" {
		inc.Title = v
	}
	if v := r.FormValue("body"); v != "" {
		inc.Body.Valid = true
		// Append update to existing body with timestamp separator.
		if inc.Body.String != "" {
			inc.Body.String += fmt.Sprintf("\n\n---\n**Update** (%s)\n%s",
				time.Now().Format("Jan 2, 15:04 UTC"), v)
		} else {
			inc.Body.String = v
		}
	}
	if v := r.FormValue("status"); v != "" {
		inc.Status = v
	}
	s.db.UpdateIncident(inc)
	http.Redirect(w, r, "/admin/incidents", http.StatusSeeOther)
}

func (s *Server) handleIncidentResolve(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inc, err := s.db.GetIncident(id)
	if err != nil || inc == nil {
		http.NotFound(w, r)
		return
	}
	inc.Status = "resolved"
	inc.ResolvedAt.Valid = true
	inc.ResolvedAt.Int64 = time.Now().Unix()
	s.db.UpdateIncident(inc)
	http.Redirect(w, r, "/admin/incidents", http.StatusSeeOther)
}

// ── Alert channels ────────────────────────────────────────────────────────────

func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request) {
	channels, _ := s.db.ListAlertChannels()
	s.render(w, "page-channels", map[string]any{
		"Site":     s.siteData(),
		"Channels": channels,
	})
}

func (s *Server) handleChannelCreate(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	chanType := r.FormValue("type")

	var cfg map[string]any
	switch chanType {
	case "webhook":
		cfg = map[string]any{"url": r.FormValue("url")}
	case "slack":
		cfg = map[string]any{"url": r.FormValue("url")}
	case "email":
		cfg = map[string]any{
			"host": r.FormValue("smtp_host"),
			"port": toInt(r.FormValue("smtp_port"), 587),
			"user": r.FormValue("smtp_user"),
			"pass": r.FormValue("smtp_pass"),
			"from": r.FormValue("smtp_from"),
			"to":   r.FormValue("to"),
		}
	}
	cfgJSON, _ := json.Marshal(cfg)

	ch := &db.AlertChannel{
		Name:   r.FormValue("name"),
		Type:   chanType,
		Config: string(cfgJSON),
	}
	s.db.CreateAlertChannel(ch)
	http.Redirect(w, r, "/admin/channels", http.StatusSeeOther)
}

func (s *Server) handleChannelDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.db.DeleteAlertChannel(id)
	http.Redirect(w, r, "/admin/channels", http.StatusSeeOther)
}

// ── Settings ──────────────────────────────────────────────────────────────────

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.render(w, "page-settings", map[string]any{
		"Site":        s.siteData(),
		"Config":      s.cfg,
		"Subscribers": s.db.CountSubscribers(),
	})
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	// Runtime settings are read-only for now (env-based config).
	http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
}

// ── Auth ──────────────────────────────────────────────────────────────────────

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.sessionUser(r) != "" {
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	}
	s.render(w, "page-login", map[string]any{
		"Site":  s.siteData(),
		"Error": r.URL.Query().Get("error"),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	user := r.FormValue("username")
	pass := r.FormValue("password")

	if user == s.cfg.AdminUser && pass == s.cfg.AdminPass {
		s.createSession(w, user)
		next := r.FormValue("next")
		if next == "" || next == "/login" {
			next = "/admin"
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/login?error=invalid", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ── Heartbeat ─────────────────────────────────────────────────────────────────

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := s.db.GetMonitor(id)
	if err != nil || m == nil || m.Type != "heartbeat" {
		http.NotFound(w, r)
		return
	}
	s.db.RecordHeartbeat(id)
	w.WriteHeader(http.StatusNoContent)
}

func toInt(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
