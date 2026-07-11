package serve

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// keyedMutexes serializes load-modify-save operations for one bundle while
// allowing unrelated manuscripts to be edited independently.
type keyedMutexes struct{ locks sync.Map }

func (m *keyedMutexes) lock(key string) func() {
	v, _ := m.locks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// allowMutation rejects browser requests that reached the loopback server
// from a foreign web origin (or through DNS rebinding). Command-line clients
// generally omit Origin/Sec-Fetch-Site and remain supported, but their Host
// must still name loopback when enforcement is active in Serve.
func (s *Server) allowMutation(w http.ResponseWriter, r *http.Request) bool {
	if !s.enforceLocalMutations {
		return true // direct Handler use in embedders/tests; Serve enables it
	}
	host := requestHostname(r.Host)
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		http.Error(w, "mutation requests must use the loopback host", http.StatusForbidden)
		return false
	}
	if site := strings.ToLower(r.Header.Get("Sec-Fetch-Site")); site == "cross-site" {
		http.Error(w, "cross-site mutation refused", http.StatusForbidden)
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if err != nil || !strings.EqualFold(u.Scheme, scheme) || !strings.EqualFold(u.Host, r.Host) {
			http.Error(w, "foreign-origin mutation refused", http.StatusForbidden)
			return false
		}
	}
	return true
}

func requestHostname(hostport string) string {
	host, _, err := net.SplitHostPort(hostport)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(hostport, "[]")
}
