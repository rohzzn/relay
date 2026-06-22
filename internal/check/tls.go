package check

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"
)

// TLS checks a TLS certificate for validity and expiry.
type TLS struct{}

func (t *TLS) Check(ctx context.Context, target string, cfg map[string]any) Result {
	timeout := durationCfg(cfg, "timeout_s", 10)
	warnDays := intCfg(cfg, "warn_days", 14)

	host, _, err := net.SplitHostPort(target)
	if err != nil {
		// target might be just a hostname without port
		host = target
		target = target + ":443"
	}

	start := time.Now()
	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: timeout},
		"tcp", target,
		&tls.Config{ServerName: host},
	)
	latency := time.Since(start)

	if err != nil {
		return resultDown(fmt.Sprintf("TLS handshake failed: %v", err))
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return resultDown("no certificates returned")
	}

	cert := certs[0]
	daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)
	detail := fmt.Sprintf("cert expires %s (%d days)", cert.NotAfter.Format("2006-01-02"), daysLeft)

	if daysLeft <= 0 {
		return resultDown(fmt.Sprintf("certificate expired %s", cert.NotAfter.Format("2006-01-02")))
	}
	if daysLeft <= warnDays {
		return resultDegraded(latency, detail)
	}
	return resultUp(latency, detail)
}
