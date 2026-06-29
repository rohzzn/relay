package server

import (
	"crypto/hmac"
	"crypto/rand"
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
		Name:     sessionCookieName,
		Value:    "",
		MaxAge:   -1,
		Path:     "/",
		HttpOnly: true,
	})
}

// sessionUser returns the authenticated username, or "" if invalid/expired.
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

// requireAuth redirects to /login if not authenticated via session.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.sessionUser(r) == "" {
			http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusFound)
			return
		}
		next(w, r)
	}
}

// requireAPIOrSession allows access via API key bearer token OR admin session.
func (s *Server) requireAPIOrSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Try API key first.
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			raw := strings.TrimPrefix(auth, "Bearer ")
			hash := hashAPIKey(raw)
			key, err := s.db.GetAPIKeyByHash(hash)
			if err == nil && key != nil {
				s.db.TouchAPIKey(key.ID)
				next(w, r)
				return
			}
			jsonError(w, "invalid API key", http.StatusUnauthorized)
			return
		}
		// Fall back to session.
		if s.sessionUser(r) != "" {
			next(w, r)
			return
		}
		jsonError(w, "unauthorized", http.StatusUnauthorized)
	}
}

// GenerateAPIKey returns a new random API key and its SHA-256 hash.
func GenerateAPIKey() (raw, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	raw = "relay_" + hex.EncodeToString(b)
	hash = hashAPIKey(raw)
	return
}

func hashAPIKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func jsonError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	fmt.Fprintf(w, `{"error":%q}`, msg)
}
