package internal

import (
	"net"
	"net/http"
	"strings"
)

// IsLoopbackBind reports whether addr listens on loopback only.
func IsLoopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			return false
		}
		host = addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return strings.EqualFold(host, "localhost")
	}
	return ip.IsLoopback()
}

func (m *Module) requireHTTPAuth(w http.ResponseWriter, r *http.Request) bool {
	m.cfgMu.RLock()
	tok := m.httpToken
	m.cfgMu.RUnlock()
	if tok == "" {
		return true
	}
	got := strings.TrimSpace(r.Header.Get("X-Debrid-Token"))
	if got == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
			got = strings.TrimSpace(h[7:])
		}
	}
	if got != tok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"missing or invalid debrid HTTP token"}`))
		return false
	}
	return true
}
