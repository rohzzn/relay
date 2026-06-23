package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"net/smtp"

	"github.com/rohzzn/relay/internal/db"
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
	if s.cfg.SMTPHost == "" {
		// No SMTP — auto-confirm so the feature works out of the box.
		s.db.ConfirmSubscriber(token)
		return
	}
	confirmURL := fmt.Sprintf("%s/subscribe/confirm/%s", s.cfg.SiteURL, token)
	cfg := map[string]any{
		"host": s.cfg.SMTPHost,
		"port": float64(s.cfg.SMTPPort),
		"user": s.cfg.SMTPUser,
		"pass": s.cfg.SMTPPass,
		"from": s.cfg.SMTPFrom,
		"to":   email,
	}
	if err := sendRawConfirmEmail(cfg, s.cfg.SiteName, confirmURL); err != nil {
		log.Printf("confirmation email to %s: %v", email, err)
	}
}

func sendRawConfirmEmail(cfg map[string]any, siteName, confirmURL string) error {
	host, _ := cfg["host"].(string)
	port := 587
	if v, ok := cfg["port"].(float64); ok {
		port = int(v)
	}
	user, _ := cfg["user"].(string)
	pass, _ := cfg["pass"].(string)
	from, _ := cfg["from"].(string)
	recipient, _ := cfg["to"].(string)

	if host == "" || recipient == "" {
		return fmt.Errorf("smtp not configured")
	}

	auth := smtp.PlainAuth("", user, pass, host)
	addr := fmt.Sprintf("%s:%d", host, port)
	subject := fmt.Sprintf("Confirm your subscription to %s", siteName)
	body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nYou subscribed to status updates from %s.\r\n\r\nClick to confirm:\r\n%s\r\n\r\nIf you didn't sign up, ignore this email.",
		from, recipient, subject, siteName, confirmURL)

	return smtp.SendMail(addr, auth, from, []string{recipient}, []byte(body))
}

func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
