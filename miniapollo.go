// Package miniapollo provides an embedded Apollo configuration server for
// unit tests, in the spirit of miniredis: it speaks enough of the real Apollo
// config-service protocol that an actual Apollo client can be pointed at it.
//
// Supported Apollo endpoints (see README for the full compatibility matrix):
//
//	GET /configs/{appId}/{cluster}/{namespace}
//	GET /notifications/v2?appId=&cluster=&notifications=[...]   (long polling)
//	GET /notifications?...                                      (v1 alias)
//	GET /services/config                                        (meta service discovery)
package miniapollo

import (
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"sync"
	"time"
)

// Default dimensions used by the shortcut APIs (Set / Publish / Lookup).
const (
	DefaultAppID   = "SampleApp"
	DefaultCluster = "default"
)

// Server is the embedded Apollo configuration server.
// It is safe for concurrent use.
type Server struct {
	mu sync.Mutex

	ln     net.Listener
	srv    *http.Server
	urlStr string

	// pollTimeout is how long /notifications hold a request when nothing
	// changed, mirroring Apollo's long-polling behaviour.
	pollTimeout time.Duration

	// store: appId -> cluster -> namespace -> namespace state
	store map[string]map[string]map[string]*namespaceState

	// long-poll subscribers
	watchers map[int64]chan struct{}
	watchSeq int64
}

// namespaceState is the per-(appId, cluster, namespace) state,
// guarded by Server.mu.
type namespaceState struct {
	configurations map[string]string
	releaseKey     string
	notificationID int64
}

// NewServer starts a mini Apollo server listening on 127.0.0.1 with a random
// port and returns it. Always pair with Close (or use StartT in tests).
func NewServer() (*Server, error) {
	s := &Server{
		pollTimeout: defaultPollTimeout,
		store:       map[string]map[string]map[string]*namespaceState{},
		watchers:    map[int64]chan struct{}{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /configs/{appId}/{cluster}/{namespace}", s.handleConfigs)
	mux.HandleFunc("GET /notifications/v2", s.handleNotifications)
	mux.HandleFunc("GET /notifications", s.handleNotifications) // v1 alias
	mux.HandleFunc("GET /services/config", s.handleServicesConfig)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no such endpoint: %s", r.URL.Path))
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.ln = ln
	s.urlStr = "http://" + ln.Addr().String()
	s.srv = &http.Server{Handler: mux}

	go func() { _ = s.srv.Serve(ln) }() //nolint:errcheck // Serve always returns ErrServerClosed here
	return s, nil
}

// Close shuts the server down. Safe to call more than once.
func (s *Server) Close() error {
	s.mu.Lock()
	srv := s.srv
	s.mu.Unlock()
	if srv != nil {
		return srv.Close()
	}
	return nil
}

// URL returns the base URL, e.g. "http://127.0.0.1:49152".
// Point your client's meta server / APOLLO_META here.
func (s *Server) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.urlStr
}

// Host returns "host:port" of this server.
func (s *Server) Host() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// SetPollTimeout overrides how long /notifications holds requests with no
// changes. Default 30s (real Apollo default). Tests usually want ~100ms.
func (s *Server) SetPollTimeout(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pollTimeout = d
}

// ---- internal helpers (mu must be held unless noted) ----

// ensureNamespaceLocked returns the state, creating it if needed.
func (s *Server) ensureNamespaceLocked(appID, cluster, ns string) *namespaceState {
	byCluster, ok := s.store[appID]
	if !ok {
		byCluster = map[string]map[string]*namespaceState{}
		s.store[appID] = byCluster
	}
	byNs, ok := byCluster[cluster]
	if !ok {
		byNs = map[string]*namespaceState{}
		byCluster[cluster] = byNs
	}
	st, ok := byNs[ns]
	if !ok {
		st = &namespaceState{configurations: map[string]string{}}
		byNs[ns] = st
	}
	return st
}

// getNamespaceLocked looks a namespace up; nil means "never published",
// which participates in notifications as server-side id -1 (like real Apollo).
func (s *Server) getNamespaceLocked(appID, cluster, ns string) *namespaceState {
	return s.store[appID][cluster][ns]
}

// publishLocked bumps the release of a namespace:
// notification id +1, new releaseKey, then wakes all long pollers.
// Both Publish* and Set* funnel through here (Set auto-publishes,
// which keeps tests terse; see README "Differences").
func (s *Server) publishLocked(appID, cluster, ns string) {
	st := s.ensureNamespaceLocked(appID, cluster, ns)
	st.notificationID++

	// releaseKey looks like real Apollo's "<yyyyMMddHHmmss>-<12 hex>".
	h := fnv.New64a()
	fmt.Fprintf(h, "%d/%s", st.notificationID, st.releaseKey) // id participates => unique
	st.releaseKey = time.Now().Format("20060102150405") + "-" +
		fmt.Sprintf("%012x", h.Sum64()&0xffffffffffff)

	s.broadcastLocked()
}

// copyMap returns a defensive copy so callers can't mutate server state.
func copyMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
