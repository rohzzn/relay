package notify

import (
	"bytes"
	"fmt"
	"net/smtp"
	"strings"
)

// SendSMTP sends an alert email via SMTP.
func SendSMTP(cfg map[string]any, p Payload) error {
	host, _ := cfg["host"].(string)
	port := 587
	if v, ok := cfg["port"].(float64); ok {
		port = int(v)
	}
	user, _ := cfg["user"].(string)
	pass, _ := cfg["pass"].(string)
	from, _ := cfg["from"].(string)
	to, _ := cfg["to"].(string)

	if host == "" || to == "" {
		return fmt.Errorf("smtp: host and to are required")
	}

	auth := smtp.PlainAuth("", user, pass, host)
	addr := fmt.Sprintf("%s:%d", host, port)

	var body bytes.Buffer
	body.WriteString("From: " + from + "\r\n")
	body.WriteString("To: " + to + "\r\n")
	body.WriteString("Subject: " + p.Subject() + "\r\n")
	body.WriteString("MIME-Version: 1.0\r\n")
	body.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	body.WriteString("\r\n")
	body.WriteString(emailHTML(p))

	recipients := strings.Split(to, ",")
	for i := range recipients {
		recipients[i] = strings.TrimSpace(recipients[i])
	}

	return smtp.SendMail(addr, auth, from, recipients, body.Bytes())
}

func emailHTML(p Payload) string {
	statusColor := "#e53e3e"
	switch p.EventType {
	case "up":
		statusColor = "#38a169"
	case "degraded":
		statusColor = "#d69e2e"
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<body style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:#f7fafc;padding:40px 0">
<table width="560" cellpadding="0" cellspacing="0" style="margin:0 auto;background:#fff;border-radius:12px;overflow:hidden;box-shadow:0 1px 3px rgba(0,0,0,.1)">
  <tr><td style="background:%s;padding:24px 32px">
    <h1 style="margin:0;color:#fff;font-size:20px">%s %s</h1>
  </td></tr>
  <tr><td style="padding:32px">
    <p style="margin:0 0 16px;color:#4a5568"><strong>Monitor:</strong> %s</p>
    <p style="margin:0 0 16px;color:#4a5568"><strong>Target:</strong> %s</p>
    <p style="margin:0 0 16px;color:#4a5568"><strong>Status:</strong> %s</p>
    %s
    <p style="margin:0;color:#718096;font-size:13px">%s</p>
  </td></tr>
  <tr><td style="background:#f7fafc;padding:16px 32px;border-top:1px solid #e2e8f0">
    <p style="margin:0;color:#a0aec0;font-size:12px">Sent by Relay Monitor</p>
  </td></tr>
</table>
</body>
</html>`,
		statusColor,
		p.Emoji(), p.Subject(),
		p.MonitorName,
		p.Target,
		p.Status,
		func() string {
			if p.Detail != "" {
				return fmt.Sprintf(`<p style="margin:0 0 16px;color:#4a5568"><strong>Detail:</strong> %s</p>`, p.Detail)
			}
			return ""
		}(),
		p.Time.Format("Mon, 02 Jan 2006 15:04:05 MST"),
	)
}
