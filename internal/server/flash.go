package server

import (
	"net/http"
	"net/url"
)

// setFlash appends a one-time flash message via query param on redirect.
func setFlash(w http.ResponseWriter, r *http.Request, dest, kind, msg string) {
	u, _ := url.Parse(dest)
	q := u.Query()
	q.Set("flash", msg)
	q.Set("flash_kind", kind)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

// flashData returns flash message data from the request query string.
func flashData(r *http.Request) map[string]string {
	msg := r.URL.Query().Get("flash")
	kind := r.URL.Query().Get("flash_kind")
	if msg == "" {
		return nil
	}
	if kind == "" {
		kind = "success"
	}
	return map[string]string{"message": msg, "kind": kind}
}
