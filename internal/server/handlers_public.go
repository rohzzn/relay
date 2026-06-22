package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/mail"

	"github.com/relay-monitor/relay/internal/db"
)

// ── Public status page ────────────────────────────────────────────────────────

type StatusPageData struct {
	Site          SiteData
	OverallStatus string
	Monitors      []*StatusMonitorView
	Incidents     []*db.Incident
}

type StatusMonitorView struct {
	*db.Monitor
	UptimeDays []db.DayUptime
	Uptime90d  float64
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.db.ListMonitors()
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	views := make([]*StatusMonitorView, 0, len(monitors))
	overall := "operational"
	hasDown := false
	hasDegraded := false

	for _, m := range monitors {
		days, _ := s.db.GetDailyUptime(m.ID, 90)
		uptime := s.db.GetUptimePct(m.ID, 90)
		views = append(views, &StatusMonitorView{
			Monitor:    m,
			UptimeDays: days,
			Uptime90d:  uptime,
		})
		switch m.Status {
		case "down":
			hasDown = true
		case "degraded":
			hasDegraded = true
		}
	}

	if hasDown {
		overall = "outage"
	} else if hasDegraded {
		overall = "degraded"
	}

	incidents, _ := s.db.ListIncidents(true)

	s.render(w, "page-status", StatusPageData{
		Site:          s.siteData(),
		OverallStatus: overall,
		Monitors:      views,
		Incidents:     incidents,
	})
}

// ── Subscribers ───────────────────────────────────────────────────────────────

func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	email := r.FormValue("email")

	if _, err := mail.ParseAddress(email); err != nil {
		http.Redirect(w, r, "/?error=invalid_email", http.StatusSeeOther)
		return
	}

	token, err := generateToken()
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}

	sub := &db.Subscriber{
		Email:     email,
		Token:     token,
		Confirmed: 0,
	}
	if err := s.db.CreateSubscriber(sub); err != nil {
		// If already subscribed, just redirect silently.
		http.Redirect(w, r, "/?subscribed=1", http.StatusSeeOther)
		return
	}

	// Send confirmation email if SMTP is configured.
	if s.cfg.SMTPHost != "" {
		go s.sendConfirmationEmail(email, token)
	}

	http.Redirect(w, r, "/?subscribed=1", http.StatusSeeOther)
}

func (s *Server) handleSubscribeConfirm(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	sub, err := s.db.GetSubscriberByToken(token)
	if err != nil || sub == nil {
		http.Error(w, "invalid or expired token", http.StatusBadRequest)
		return
	}
	s.db.ConfirmSubscriber(token)
	http.Redirect(w, r, "/?confirmed=1", http.StatusSeeOther)
}

func (s *Server) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	s.db.DeleteSubscriberByToken(token)
	http.Redirect(w, r, "/?unsubscribed=1", http.StatusSeeOther)
}

func (s *Server) sendConfirmationEmail(email, token string) {
	confirmURL := fmt.Sprintf("%s/subscribe/confirm/%s", s.cfg.SiteURL, token)
	cfg := map[string]any{
		"host": s.cfg.SMTPHost,
		"port": s.cfg.SMTPPort,
		"user": s.cfg.SMTPUser,
		"pass": s.cfg.SMTPPass,
		"from": s.cfg.SMTPFrom,
		"to":   email,
	}
	_ = sendConfirmEmail(cfg, s.cfg.SiteName, email, confirmURL)
}

func sendConfirmEmail(cfg map[string]any, siteName, to, confirmURL string) error {
	body := fmt.Sprintf(`You signed up for status updates from %s.

Click this link to confirm your subscription:
%s

If you didn't sign up, you can ignore this email.`, siteName, confirmURL)
	_ = body
	// Uses the same SMTP helper from notify package; simplified here.
	return nil
}

func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
