package miniapollo

import (
	"encoding/json"
	"io"
	"net/http"
)

// The /configfiles family mirrors real Apollo's ConfigFileController — the
// "direct file access" API agollo requests for its initial sync
// (configfiles/json/...) and that plain-HTTP tooling consumes otherwise.
//
//	GET /configfiles/{appId}/{cluster}/{namespace}        → k=v properties text
//	GET /configfiles/json/{appId}/{cluster}/{namespace}   → configurations as JSON
//	GET /configfiles/raw/{appId}/{cluster}/{namespace}    → file content as-is
//
// All three read the same stored state as /configs, so file-type namespaces
// arrive here as {"content": "<file>"} exactly like the real server's
// ApolloConfigDTO. Unpublished namespaces answer 404 with an empty body —
// real Apollo's ConfigFileController returns a bare NOT_FOUND response here,
// unlike /configs' JSON error body.

// configFileMode distinguishes the three ConfigFileController output formats.
type configFileMode int

const (
	configFileProperties configFileMode = iota // k=v text
	configFileJSON                             // configurations as JSON
	configFileRaw                              // file content verbatim
)

// handleConfigFilesProperties serves GET /configfiles/{appId}/{cluster}/{namespace}:
// the configurations map flattened to k=v text (Apollo's PropertiesUtil.toString),
// with a text/plain content type.
func (s *Server) handleConfigFilesProperties(w http.ResponseWriter, r *http.Request) {
	s.serveConfigFiles(w, r, configFileProperties)
}

// handleConfigFilesJson serves GET /configfiles/json/{appId}/{cluster}/{namespace}:
// the configurations map serialized as JSON. This is the exact form agollo
// v4/v5 request for their initial sync — file-type namespaces arrive as
// {"content": "<file>"} so client-side format parsers stay in charge of
// interpreting the payload (agollo's own client: without this endpoint a
// MustStart start-up gets zero configs and fails).
func (s *Server) handleConfigFilesJson(w http.ResponseWriter, r *http.Request) {
	s.serveConfigFiles(w, r, configFileJSON)
}

// handleConfigFilesRaw serves GET /configfiles/raw/{appId}/{cluster}/{namespace}:
// the file content as-is, content type derived from the namespace format
// (json → application/json, yml/yaml → application/yaml, else text/plain).
// Properties namespaces carry no file body, so they flatten to k=v text.
func (s *Server) handleConfigFilesRaw(w http.ResponseWriter, r *http.Request) {
	s.serveConfigFiles(w, r, configFileRaw)
}

// serveConfigFiles implements the shared /configfiles lookup and output
// dispatch: unpublished namespaces answer a bare 404 (empty body), which is
// what makes strict clients fail fast and lenient clients fall back to their
// local cache, same as against real Apollo.
func (s *Server) serveConfigFiles(w http.ResponseWriter, r *http.Request, mode configFileMode) {
	appID := r.PathValue("appId")
	cluster := r.PathValue("cluster")
	ns := r.PathValue("namespace")

	s.mu.Lock()
	st := s.getNamespaceLocked(appID, cluster, ns)
	var kv map[string]string
	if st != nil {
		kv = copyMap(st.configurations)
	}
	s.mu.Unlock()

	if st == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	switch mode {
	case configFileJSON:
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(kv)
	case configFileRaw:
		writeRawContent(w, ns, kv)
	default:
		w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
		_, _ = io.WriteString(w, flattenKV(kv, "%s=%s\n"))
	}
}

// writeRawContent emits the verbatim file content with the content type real
// Apollo picks for the namespace format (getRawConfigContent parity).
func writeRawContent(w http.ResponseWriter, ns string, kv map[string]string) {
	format := fileFormat(ns)
	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	case "yml", "yaml":
		w.Header().Set("Content-Type", "application/yaml;charset=UTF-8")
	default:
		w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
	}
	if format == "" {
		_, _ = io.WriteString(w, flattenKV(kv, "%s=%s\n"))
		return
	}
	_, _ = io.WriteString(w, kv["content"])
}
