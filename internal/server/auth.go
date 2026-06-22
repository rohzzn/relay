package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const sessionCookieName = "relay_session"
const sessionTTL = 24 * time.Hour

func (s *Server) createSession(w http.ResponseWriter, username string) {
	expiry := time.Now().Add(sessionTTL)
	payload := fmt.Sprintf("%s|%d", username, expiry.Unix())
	sig := signHMAC(payload, s.cfg.Secret)
	value := payload + "|" + sig

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Expires:  expiry,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:    sessionCookieName,
		Value:   "",
		MaxAge:  -1,
		Path:    "/",
		HttpOnly: true,
	})
}

// sessionUser returns the authenticated username, or "" if the session is invalid/expired.
func (s *Server) sessionUser(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}

	parts := strings.SplitN(cookie.Value, "|", 3)
	if len(parts) != 3 {
		return ""
	}
	username, expiryStr, sig := parts[0], parts[1], parts[2]
	payload := username + "|" + expiryStr

	if !verifyHMAC(payload, sig, s.cfg.Secret) {
		return ""
	}

	var expiry int64
	fmt.Sscanf(expiryStr, "%d", &expiry)
	if time.Now().Unix() > expiry {
		return ""
	}
	return username
}

func signHMAC(msg, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}

func verifyHMAC(msg, sig, secret string) bool {
	expected := signHMAC(msg, secret)
	return hmac.Equal([]byte(expected), []byte(sig))
}

// requireAuth is a middleware that redirects to /login if the user is not authenticated.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.sessionUser(r) == "" {
			http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusFound)
			return
		}
		next(w, r)
	}
}
