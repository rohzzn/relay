package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rohzzn/relay/internal/check"
	"github.com/rohzzn/relay/internal/db"
	"golang.org/x/crypto/bcrypt"
)

// ── View types ────────────────────────────────────────────────────────────────

type MonitorView struct {
	*db.Monitor
	LastCheck    *db.Check
	Uptime24h    float64
	Uptime7d     float64
	Uptime30d    float64
	Uptime90d    float64
	UptimeDays   []db.DayUptime
	RecentChecks []*db.Check
	AvgLatency   int64
	Sparkline    string
	PingBase     string
	ChannelIDs   []string
}

type MonitorGroup struct {
	Name     string
	Monitors []*MonitorView
}

type DashboardData struct {
	Site      SiteData
	Groups    []MonitorGroup
	Stats     Stats
	Incidents []*db.Incident
	Flash     map[string]string
	PingBase  string
}

type SiteData struct {
	Name       string
	URL        string
	Logo       string
	FooterText string
}

type Stats struct {
	Total    int
	Up       int
	Down     int
	Degraded int
	Pending  int
	Paused   int
}

// ── Dashboard ─────────────────────────────────────────────────────────────────

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.db.ListMonitors()
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	stats := Stats{Total: len(monitors)}
	groupMap := make(map[string][]*MonitorView)
	groupOrder := []string{}

	for _, m := range monitors {
		mv, _ := s.buildMonitorView(m)
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
		if m.Paused {
			stats.Paused++
		}

		group := m.GroupName
		if group == "" {
			group = "Ungrouped"
		}
		if _, exists := groupMap[group]; !exists {
			groupOrder = append(groupOrder, group)
		}
		groupMap[group] = append(groupMap[group], mv)
	}

	groups := make([]MonitorGroup, 0, len(groupMap))
	for _, name := range groupOrder {
		groups = append(groups, MonitorGroup{Name: name, Monitors: groupMap[name]})
	}

	incidents, _ := s.db.ListIncidents(true)

	s.render(w, "page-dashboard", DashboardData{
		Site:      s.siteData(),
		Groups:    groups,
		Stats:     stats,
		Incidents: incidents,
		Flash:     flashData(r),
		PingBase:  s.cfg.SiteURL,
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
	mv.RecentChecks, _ = s.db.ListChecks(m.ID, 40)
	mv.AvgLatency = s.db.GetAvgLatency(m.ID, 24)
	mv.Sparkline = sparklineSVG(mv.RecentChecks, sparklineColor(m.Status))
	mv.PingBase = s.cfg.SiteURL
	mv.ChannelIDs, _ = s.db.GetMonitorChannelIDs(m.ID)
	return mv, nil
}

func (s *Server) siteData() SiteData {
	ft := s.cfg.FooterText
	if ft == "" {
		ft = "Powered by Relay"
	}
	return SiteData{
		Name:       s.cfg.SiteName,
		URL:        s.cfg.SiteURL,
		Logo:       s.cfg.LogoURL,
		FooterText: ft,
	}
}

// ── Monitor CRUD ─────────────────────────────────────────────────────────────

func (s *Server) handleMonitorNew(w http.ResponseWriter, r *http.Request) {
	allChannels, _ := s.db.ListAlertChannels()
	s.render(w, "page-monitor-form", map[string]any{
		"Site":        s.siteData(),
		"Monitor":     nil,
		"Config":      map[string]any{},
		"Action":      "/admin/monitors",
		"Title":       "Add Monitor",
		"AllChannels": allChannels,
		"ChannelIDs":  []string{},
	})
}

func (s *Server) handleMonitorCreate(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
	m := monitorFromForm(r)
	if err := s.db.CreateMonitor(m); err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if m.Type == "heartbeat" {
		m.Target = m.ID
		s.db.UpdateMonitor(m)
	}
	// Save per-monitor channel routing.
	channelIDs := r.Form["channel_ids"]
	s.db.SetMonitorChannels(m.ID, channelIDs)

	if !m.Paused {
		s.scheduler.Add(m)
	}
	s.db.WriteAudit(actor, "create", "monitor", m.ID, m.Name)
	setFlash(w, r, "/admin", "success", "Monitor \""+m.Name+"\" created")
}

func (s *Server) handleMonitorEdit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := s.db.GetMonitor(id)
	if err != nil || m == nil {
		http.NotFound(w, r)
		return
	}

	var cfg map[string]any
	json.Unmarshal([]byte(m.Config), &cfg)

	allChannels, _ := s.db.ListAlertChannels()
	channelIDs, _ := s.db.GetMonitorChannelIDs(id)

	s.render(w, "page-monitor-form", map[string]any{
		"Site":        s.siteData(),
		"Monitor":     m,
		"Config":      cfg,
		"Action":      fmt.Sprintf("/admin/monitors/%s", m.ID),
		"Title":       "Edit Monitor",
		"AllChannels": allChannels,
		"ChannelIDs":  channelIDs,
	})
}

func (s *Server) handleMonitorUpdate(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
	id := r.PathValue("id")
	m := monitorFromForm(r)
	m.ID = id
	if err := s.db.UpdateMonitor(m); err != nil {
		http.Error(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	channelIDs := r.Form["channel_ids"]
	s.db.SetMonitorChannels(m.ID, channelIDs)
	s.scheduler.Update(m)
	s.db.WriteAudit(actor, "update", "monitor", m.ID, m.Name)
	setFlash(w, r, "/admin", "success", "Monitor \""+m.Name+"\" updated")
}

func (s *Server) handleMonitorDelete(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
	id := r.PathValue("id")
	m, _ := s.db.GetMonitor(id)
	name := id
	if m != nil {
		name = m.Name
	}
	s.scheduler.Remove(id)
	s.db.DeleteMonitor(id)
	s.db.WriteAudit(actor, "delete", "monitor", id, name)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleMonitorPause(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
	id := r.PathValue("id")
	m, err := s.db.GetMonitor(id)
	if err != nil || m == nil {
		http.NotFound(w, r)
		return
	}

	newPaused := !m.Paused
	s.db.SetMonitorPaused(id, newPaused)

	action := "pause"
	if !newPaused {
		action = "resume"
		m.Paused = false
		s.scheduler.Add(m)
	} else {
		s.scheduler.Remove(id)
	}
	s.db.WriteAudit(actor, action, "monitor", id, m.Name)
	setFlash(w, r, "/admin", "success", fmt.Sprintf("Monitor \"%s\" %sd", m.Name, action))
}

// handleMonitorTest runs a single probe immediately and returns JSON.
func (s *Server) handleMonitorTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := s.db.GetMonitor(id)
	if err != nil || m == nil {
		jsonError(w, "monitor not found", http.StatusNotFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	var result check.Result
	var cfg map[string]any
	json.Unmarshal([]byte(m.Config), &cfg)
	if cfg == nil {
		cfg = map[string]any{}
	}

	switch m.Type {
	case "http", "https":
		result = (&check.HTTP{}).Check(ctx, m.Target, cfg)
	case "tcp":
		result = (&check.TCP{}).Check(ctx, m.Target, cfg)
	case "tls":
		result = (&check.TLS{}).Check(ctx, m.Target, cfg)
	case "dns":
		result = (&check.DNS{}).Check(ctx, m.Target, cfg)
	default:
		jsonError(w, "test not supported for this monitor type", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":     result.Status,
		"latency_ms": result.LatencyMs,
		"detail":     result.Detail,
		"checked_at": result.CheckedAt,
	})
}

func monitorFromForm(r *http.Request) *db.Monitor {
	r.ParseForm()
	intervalS, _ := strconv.Atoi(r.FormValue("interval_s"))
	if intervalS < 30 {
		intervalS = 60
	}

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
	if v := r.FormValue("expected_ip"); v != "" {
		cfg["expected_ip"] = v
	}
	if v := r.FormValue("max_latency_ms"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg["max_latency_ms"] = n
		}
	}
	cfgJSON, _ := json.Marshal(cfg)

	paused := r.FormValue("paused") == "1"

	return &db.Monitor{
		Name:      r.FormValue("name"),
		Type:      r.FormValue("type"),
		Target:    r.FormValue("target"),
		IntervalS: intervalS,
		Regions:   "local",
		Config:    string(cfgJSON),
		GroupName: strings.TrimSpace(r.FormValue("group_name")),
		Paused:    paused,
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
	actor := s.sessionUser(r)
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
	s.db.WriteAudit(actor, "create", "incident", inc.ID, inc.Title)
	http.Redirect(w, r, "/admin/incidents", http.StatusSeeOther)
}

func (s *Server) handleIncidentUpdate(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
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
	s.db.WriteAudit(actor, "update", "incident", inc.ID, inc.Title)
	http.Redirect(w, r, "/admin/incidents", http.StatusSeeOther)
}

func (s *Server) handleIncidentResolve(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
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
	s.db.WriteAudit(actor, "resolve", "incident", inc.ID, inc.Title)
	http.Redirect(w, r, "/admin/incidents", http.StatusSeeOther)
}

// ── Alert channels ────────────────────────────────────────────────────────────

func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request) {
	channels, _ := s.db.ListAlertChannels()
	s.render(w, "page-channels", map[string]any{
		"Site":     s.siteData(),
		"Channels": channels,
		"Flash":    flashData(r),
	})
}

func (s *Server) handleChannelCreate(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
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
	case "pagerduty":
		cfg = map[string]any{"integration_key": r.FormValue("integration_key")}
	case "opsgenie":
		cfg = map[string]any{
			"api_key": r.FormValue("api_key"),
			"region":  r.FormValue("region"),
		}
	}
	cfgJSON, _ := json.Marshal(cfg)

	ch := &db.AlertChannel{
		Name:   r.FormValue("name"),
		Type:   chanType,
		Config: string(cfgJSON),
	}
	s.db.CreateAlertChannel(ch)
	s.db.WriteAudit(actor, "create", "channel", ch.ID, ch.Name)
	http.Redirect(w, r, "/admin/channels", http.StatusSeeOther)
}

func (s *Server) handleChannelDelete(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
	id := r.PathValue("id")
	ch, _ := s.db.GetAlertChannel(id)
	name := id
	if ch != nil {
		name = ch.Name
	}
	s.db.DeleteAlertChannel(id)
	s.db.WriteAudit(actor, "delete", "channel", id, name)
	http.Redirect(w, r, "/admin/channels", http.StatusSeeOther)
}

func (s *Server) handleChannelTest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.dispatcher.TestChannel(id); err != nil {
		setFlash(w, r, "/admin/channels", "error", "Test failed: "+err.Error())
		return
	}
	setFlash(w, r, "/admin/channels", "success", "Test alert sent successfully")
}

// ── Settings ──────────────────────────────────────────────────────────────────

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	apiKeys, _ := s.db.ListAPIKeys()
	s.render(w, "page-settings", map[string]any{
		"Site":        s.siteData(),
		"Config":      s.cfg,
		"Subscribers": s.db.CountSubscribers(),
		"APIKeys":     apiKeys,
		"Flash":       flashData(r),
	})
}

func (s *Server) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
}

// ── API Key management ────────────────────────────────────────────────────────

func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
	r.ParseForm()
	name := r.FormValue("name")
	role := r.FormValue("role")
	if role == "" {
		role = "viewer"
	}

	raw, hash, err := GenerateAPIKey()
	if err != nil {
		setFlash(w, r, "/admin/settings", "error", "Failed to generate key")
		return
	}

	key := &db.APIKey{Name: name, KeyHash: hash, Role: role}
	if err := s.db.CreateAPIKey(key); err != nil {
		setFlash(w, r, "/admin/settings", "error", "Failed to save key")
		return
	}
	s.db.WriteAudit(actor, "create", "api_key", key.ID, name)

	// Show the raw key once — store it in the flash message.
	setFlash(w, r, "/admin/settings", "success",
		fmt.Sprintf("API key created. Save it now — it won't be shown again: %s", raw))
}

func (s *Server) handleAPIKeyDelete(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
	id := r.PathValue("id")
	s.db.DeleteAPIKey(id)
	s.db.WriteAudit(actor, "delete", "api_key", id, "")
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
		"Next":  r.URL.Query().Get("next"),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	user := r.FormValue("username")
	pass := r.FormValue("password")

	// Check env-var admin first.
	if user == s.cfg.AdminUser && pass == s.cfg.AdminPass {
		s.createSession(w, user)
		next := r.FormValue("next")
		if next == "" || next == "/login" {
			next = "/admin"
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
		return
	}

	// Then check DB users.
	dbUser, err := s.db.GetUserByUsername(user)
	if err == nil && dbUser != nil {
		if bcrypt.CompareHashAndPassword([]byte(dbUser.HashedPass), []byte(pass)) == nil {
			s.createSession(w, user)
			next := r.FormValue("next")
			if next == "" || next == "/login" {
				next = "/admin"
			}
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
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

// ── Users ──────────────────────────────────────────────────────────────────────

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	users, _ := s.db.ListUsers()
	s.render(w, "page-users", map[string]any{
		"Site":  s.siteData(),
		"Users": users,
		"Flash": flashData(r),
	})
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
	r.ParseForm()

	username := strings.TrimSpace(r.FormValue("username"))
	email := strings.TrimSpace(r.FormValue("email"))
	pass := r.FormValue("password")
	role := r.FormValue("role")
	if role == "" {
		role = "viewer"
	}

	if username == "" || pass == "" {
		setFlash(w, r, "/admin/users", "error", "Username and password are required")
		return
	}

	hashed, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		setFlash(w, r, "/admin/users", "error", "Failed to hash password")
		return
	}

	u := &db.User{
		Username:   username,
		Email:      email,
		HashedPass: string(hashed),
		Role:       role,
	}
	if err := s.db.CreateUser(u); err != nil {
		setFlash(w, r, "/admin/users", "error", "Failed to create user (username may already exist)")
		return
	}
	s.db.WriteAudit(actor, "create", "user", u.ID, username)
	setFlash(w, r, "/admin/users", "success", fmt.Sprintf("User \"%s\" created", username))
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
	id := r.PathValue("id")
	s.db.DeleteUser(id)
	s.db.WriteAudit(actor, "delete", "user", id, "")
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (s *Server) handleUserRole(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
	id := r.PathValue("id")
	r.ParseForm()
	role := r.FormValue("role")
	s.db.UpdateUserRole(id, role)
	s.db.WriteAudit(actor, "update_role", "user", id, role)
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// ── Maintenance windows ────────────────────────────────────────────────────────

func (s *Server) handleMaintenance(w http.ResponseWriter, r *http.Request) {
	windows, _ := s.db.ListMaintenanceWindows()
	monitors, _ := s.db.ListMonitors()
	s.render(w, "page-maintenance", map[string]any{
		"Site":     s.siteData(),
		"Windows":  windows,
		"Monitors": monitors,
		"Flash":    flashData(r),
		"Now":      time.Now().Unix(),
	})
}

func (s *Server) handleMaintenanceCreate(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
	r.ParseForm()

	label := r.FormValue("label")
	startsAt := parseDateTime(r.FormValue("starts_at"))
	endsAt := parseDateTime(r.FormValue("ends_at"))

	if startsAt == 0 || endsAt == 0 || endsAt <= startsAt {
		setFlash(w, r, "/admin/maintenance", "error", "Invalid time range")
		return
	}

	win := &db.MaintenanceWindow{
		Label:    label,
		StartsAt: startsAt,
		EndsAt:   endsAt,
	}
	if mid := r.FormValue("monitor_id"); mid != "" {
		win.MonitorID.Valid = true
		win.MonitorID.String = mid
	}
	s.db.CreateMaintenanceWindow(win)
	s.db.WriteAudit(actor, "create", "maintenance_window", win.ID, label)
	setFlash(w, r, "/admin/maintenance", "success", fmt.Sprintf("Maintenance window \"%s\" scheduled", label))
}

func (s *Server) handleMaintenanceDelete(w http.ResponseWriter, r *http.Request) {
	actor := s.sessionUser(r)
	id := r.PathValue("id")
	s.db.DeleteMaintenanceWindow(id)
	s.db.WriteAudit(actor, "delete", "maintenance_window", id, "")
	http.Redirect(w, r, "/admin/maintenance", http.StatusSeeOther)
}

// ── Audit log ─────────────────────────────────────────────────────────────────

func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	page := 1
	if p, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && p > 0 {
		page = p
	}
	limit := 50
	offset := (page - 1) * limit

	entries, total, _ := s.db.ListAuditLog(limit, offset)
	totalPages := (total + limit - 1) / limit

	s.render(w, "page-audit", map[string]any{
		"Site":       s.siteData(),
		"Entries":    entries,
		"Page":       page,
		"TotalPages": totalPages,
		"Total":      total,
	})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func toInt(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// parseDateTime parses a datetime-local input value ("2006-01-02T15:04") to Unix timestamp.
func parseDateTime(s string) int64 {
	if s == "" {
		return 0
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", s, time.Local)
	if err != nil {
		return 0
	}
	return t.Unix()
}
