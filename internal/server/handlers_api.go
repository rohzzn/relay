package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/rohzzn/relay/internal/db"
)

// ── REST API v1 ───────────────────────────────────────────────────────────────

func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.db.ListMonitors()
	if err != nil {
		apiErr(w, "db error", http.StatusInternalServerError)
		return
	}

	overall := "operational"
	for _, m := range monitors {
		switch m.Status {
		case "down":
			overall = "outage"
		case "degraded":
			if overall != "outage" {
				overall = "degraded"
			}
		}
	}

	incidents, _ := s.db.ListIncidents(true)

	jsonOK(w, map[string]any{
		"status":           overall,
		"monitor_count":    len(monitors),
		"active_incidents": len(incidents),
		"checked_at":       time.Now().Unix(),
	})
}

func (s *Server) handleAPIListMonitors(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.db.ListMonitors()
	if err != nil {
		apiErr(w, "db error", http.StatusInternalServerError)
		return
	}

	out := make([]map[string]any, 0, len(monitors))
	for _, m := range monitors {
		last, _ := s.db.GetLatestCheck(m.ID)
		row := map[string]any{
			"id":         m.ID,
			"name":       m.Name,
			"type":       m.Type,
			"target":     m.Target,
			"status":     m.Status,
			"group":      m.GroupName,
			"paused":     m.Paused,
			"interval_s": m.IntervalS,
			"uptime_24h": s.db.GetUptimePct(m.ID, 1),
			"uptime_7d":  s.db.GetUptimePct(m.ID, 7),
			"uptime_30d": s.db.GetUptimePct(m.ID, 30),
			"uptime_90d": s.db.GetUptimePct(m.ID, 90),
			"created_at": m.CreatedAt,
		}
		if last != nil {
			row["last_check"] = map[string]any{
				"status":     last.Status,
				"latency_ms": last.LatencyMs.Int64,
				"detail":     last.Detail.String,
				"checked_at": last.CheckedAt,
			}
		}
		out = append(out, row)
	}
	jsonOK(w, map[string]any{"monitors": out, "total": len(out)})
}

func (s *Server) handleAPIGetMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := s.db.GetMonitor(id)
	if err != nil || m == nil {
		apiErr(w, "monitor not found", http.StatusNotFound)
		return
	}

	last, _ := s.db.GetLatestCheck(m.ID)
	resp := map[string]any{
		"id":         m.ID,
		"name":       m.Name,
		"type":       m.Type,
		"target":     m.Target,
		"status":     m.Status,
		"group":      m.GroupName,
		"paused":     m.Paused,
		"interval_s": m.IntervalS,
		"config":     json.RawMessage(m.Config),
		"uptime_24h": s.db.GetUptimePct(m.ID, 1),
		"uptime_7d":  s.db.GetUptimePct(m.ID, 7),
		"uptime_30d": s.db.GetUptimePct(m.ID, 30),
		"uptime_90d": s.db.GetUptimePct(m.ID, 90),
		"created_at": m.CreatedAt,
	}
	if last != nil {
		resp["last_check"] = map[string]any{
			"status":     last.Status,
			"latency_ms": last.LatencyMs.Int64,
			"detail":     last.Detail.String,
			"checked_at": last.CheckedAt,
		}
	}
	jsonOK(w, resp)
}

func (s *Server) handleAPICreateMonitor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string         `json:"name"`
		Type      string         `json:"type"`
		Target    string         `json:"target"`
		IntervalS int            `json:"interval_s"`
		Group     string         `json:"group"`
		Config    map[string]any `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apiErr(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.Type == "" || body.Target == "" {
		apiErr(w, "name, type, and target are required", http.StatusBadRequest)
		return
	}
	if body.IntervalS < 30 {
		body.IntervalS = 60
	}

	cfgJSON := []byte("{}")
	if body.Config != nil {
		cfgJSON, _ = json.Marshal(body.Config)
	}

	m := &db.Monitor{
		Name:      body.Name,
		Type:      body.Type,
		Target:    body.Target,
		IntervalS: body.IntervalS,
		Regions:   "local",
		Config:    string(cfgJSON),
		GroupName: body.Group,
	}
	if err := s.db.CreateMonitor(m); err != nil {
		apiErr(w, "db error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.scheduler.Add(m)
	s.db.WriteAudit("api", "create", "monitor", m.ID, m.Name)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"id": m.ID, "name": m.Name})
}

func (s *Server) handleAPIUpdateMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := s.db.GetMonitor(id)
	if err != nil || m == nil {
		apiErr(w, "monitor not found", http.StatusNotFound)
		return
	}

	var body struct {
		Name      *string        `json:"name"`
		Target    *string        `json:"target"`
		IntervalS *int           `json:"interval_s"`
		Group     *string        `json:"group"`
		Paused    *bool          `json:"paused"`
		Config    map[string]any `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apiErr(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if body.Name != nil {
		m.Name = *body.Name
	}
	if body.Target != nil {
		m.Target = *body.Target
	}
	if body.IntervalS != nil && *body.IntervalS >= 30 {
		m.IntervalS = *body.IntervalS
	}
	if body.Group != nil {
		m.GroupName = *body.Group
	}
	if body.Paused != nil {
		m.Paused = *body.Paused
	}
	if body.Config != nil {
		cfgJSON, _ := json.Marshal(body.Config)
		m.Config = string(cfgJSON)
	}

	if err := s.db.UpdateMonitor(m); err != nil {
		apiErr(w, "db error", http.StatusInternalServerError)
		return
	}
	s.scheduler.Update(m)
	s.db.WriteAudit("api", "update", "monitor", m.ID, m.Name)
	jsonOK(w, map[string]any{"id": m.ID, "updated": true})
}

func (s *Server) handleAPIDeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := s.db.GetMonitor(id)
	if err != nil || m == nil {
		apiErr(w, "monitor not found", http.StatusNotFound)
		return
	}
	s.scheduler.Remove(id)
	s.db.DeleteMonitor(id)
	s.db.WriteAudit("api", "delete", "monitor", id, m.Name)
	jsonOK(w, map[string]any{"deleted": true})
}

func (s *Server) handleAPIListIncidents(w http.ResponseWriter, r *http.Request) {
	activeOnly := r.URL.Query().Get("active") == "true"
	incidents, err := s.db.ListIncidents(activeOnly)
	if err != nil {
		apiErr(w, "db error", http.StatusInternalServerError)
		return
	}

	out := make([]map[string]any, 0, len(incidents))
	for _, inc := range incidents {
		row := map[string]any{
			"id":         inc.ID,
			"title":      inc.Title,
			"status":     inc.Status,
			"started_at": inc.StartedAt,
		}
		if inc.MonitorID.Valid {
			row["monitor_id"] = inc.MonitorID.String
		}
		if inc.Body.Valid {
			row["body"] = inc.Body.String
		}
		if inc.ResolvedAt.Valid {
			row["resolved_at"] = inc.ResolvedAt.Int64
		}
		out = append(out, row)
	}
	jsonOK(w, map[string]any{"incidents": out, "total": len(out)})
}

func (s *Server) handleAPICreateIncident(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title     string `json:"title"`
		Status    string `json:"status"`
		Body      string `json:"body"`
		MonitorID string `json:"monitor_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apiErr(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.Title == "" {
		apiErr(w, "title is required", http.StatusBadRequest)
		return
	}
	if body.Status == "" {
		body.Status = "investigating"
	}

	inc := &db.Incident{
		Title:  body.Title,
		Status: body.Status,
	}
	if body.Body != "" {
		inc.Body.Valid = true
		inc.Body.String = body.Body
	}
	if body.MonitorID != "" {
		inc.MonitorID.Valid = true
		inc.MonitorID.String = body.MonitorID
	}

	if err := s.db.CreateIncident(inc); err != nil {
		apiErr(w, "db error", http.StatusInternalServerError)
		return
	}
	s.db.WriteAudit("api", "create", "incident", inc.ID, inc.Title)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"id": inc.ID})
}

func (s *Server) handleAPIMonitorMetrics(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := s.db.GetMonitor(id)
	if err != nil || m == nil {
		apiErr(w, "monitor not found", http.StatusNotFound)
		return
	}

	hours := 24
	if h, err := strconv.Atoi(r.URL.Query().Get("hours")); err == nil && h > 0 && h <= 720 {
		hours = h
	}

	points, err := s.db.GetLatencyHistory(id, hours)
	if err != nil {
		apiErr(w, "db error", http.StatusInternalServerError)
		return
	}

	jsonOK(w, map[string]any{
		"monitor_id": id,
		"hours":      hours,
		"uptime_pct": s.db.GetUptimePct(id, hours/24+1),
		"avg_latency_ms": s.db.GetAvgLatency(id, hours),
		"data_points": points,
	})
}

// ── JSON helpers ──────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
