package miniapollo

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// defaultPollTimeout mirrors real Apollo's long-poll hold time.
// The v2 `trigger` query param may shorten it per request (1s..60s).
const (
	defaultPollTimeout = 30 * time.Second
	minTrigger         = 1 * time.Second
	maxTrigger         = 60 * time.Second
)

// Set publishes namespace ns with kv under the server's dimensions
// (the ones given to NewServer / StartT).
// It auto-publishes a new release, like a portal publish would.
func (s *Server) Set(ns string, kv map[string]string) {
	s.SetWith(s.appID, s.cluster, ns, kv)
}

// SetWith publishes a namespace under explicit (appId, cluster) with kv.
// File-type namespaces (.yml/.yaml/.json/.txt) get their map flattened into a
// single "content" entry, exactly like real Apollo delivers them.
func (s *Server) SetWith(appID, cluster, ns string, kv map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.ensureNamespaceLocked(appID, cluster, ns)
	st.configurations = effectiveConfig(ns, kv)
	s.publishLocked(appID, cluster, ns)
}

// SetContent publishes a file-type namespace from a raw string body
// (stored as configurations["content"]) under the server's dimensions.
// Use this for real YAML/JSON blobs.
func (s *Server) SetContent(ns, content string) {
	s.SetContentWith(s.appID, s.cluster, ns, content)
}

// SetContentWith is SetContent for explicit (appId, cluster).
func (s *Server) SetContentWith(appID, cluster, ns, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.ensureNamespaceLocked(appID, cluster, ns)
	st.configurations = map[string]string{"content": content}
	s.publishLocked(appID, cluster, ns)
}

// Publish re-publishes ns (server's dimensions) without changing values:
// a new releaseKey and a bumped notification id, waking long pollers.
// Useful to simulate "edited and released again" in tests.
func (s *Server) Publish(ns string) {
	s.PublishWith(s.appID, s.cluster, ns)
}

// PublishWith is Publish for explicit (appId, cluster).
func (s *Server) PublishWith(appID, cluster, ns string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishLocked(appID, cluster, ns)
}

// Config returns the live configurations of ns (server's dimensions),
// or ok=false when it was never set. The map is a copy.
func (s *Server) Config(ns string) (map[string]string, bool) {
	return s.ConfigWith(s.appID, s.cluster, ns)
}

// ConfigWith is Config for explicit (appId, cluster).
func (s *Server) ConfigWith(appID, cluster, ns string) (map[string]string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.getNamespaceLocked(appID, cluster, ns)
	if st == nil {
		return nil, false
	}
	return copyMap(st.configurations), true
}

// ReleaseKey returns the current releaseKey of ns (server's dimensions).
func (s *Server) ReleaseKey(ns string) (string, bool) {
	return s.ReleaseKeyWith(s.appID, s.cluster, ns)
}

// ReleaseKeyWith is ReleaseKey for explicit (appId, cluster).
func (s *Server) ReleaseKeyWith(appID, cluster, ns string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.getNamespaceLocked(appID, cluster, ns)
	if st == nil {
		return "", false
	}
	return st.releaseKey, true
}

// NotificationID returns the current notification id (release sequence
// number) of ns (server's dimensions); 0 when never published.
func (s *Server) NotificationID(ns string) int64 {
	return s.NotificationIDWith(s.appID, s.cluster, ns)
}

// NotificationIDWith is NotificationID for explicit (appId, cluster).
func (s *Server) NotificationIDWith(appID, cluster, ns string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.getNamespaceLocked(appID, cluster, ns)
	if st == nil {
		return 0
	}
	return st.notificationID
}

// fileFormat returns the Apollo ConfigFileFormat carried by the namespace
// name ("json" / "yml" / "yaml" / "txt"), or "" for plain properties
// namespaces — the same case-insensitive suffix check Apollo's
// determineNamespaceFormat performs.
func fileFormat(ns string) string {
	lower := strings.ToLower(ns)
	for _, format := range []string{"json", "yml", "yaml", "txt"} {
		if strings.HasSuffix(lower, "."+format) {
			return format
		}
	}
	return ""
}

// flattenKV renders the kv map with the given per-entry format string,
// keys sorted for deterministic output.
func flattenKV(kv map[string]string, entryFormat string) string {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic output for tests
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, entryFormat, k, kv[k])
	}
	return b.String()
}

// effectiveConfig mimics how Apollo serves file-type namespaces:
// non-properties namespaces arrive at the client as {"content": "<file>"}
// rather than as a flat key/value map.
func effectiveConfig(ns string, kv map[string]string) map[string]string {
	switch fileFormat(ns) {
	case "yml", "yaml":
		return map[string]string{"content": flattenKV(kv, "%s: %s\n")}
	case "json":
		blob, _ := json.Marshal(kv)
		return map[string]string{"content": string(blob)}
	case "txt":
		return map[string]string{"content": flattenKV(kv, "%s=%s\n")}
	default: // plain properties namespace or .properties suffix
		return copyMap(kv)
	}
}
