package miniapollo

import (
	"encoding/json"
	"net/http"
)

// writeError emits an Apollo-style error body ({"message": "..."}).
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": msg})
}

// configDTO mirrors Apollo's ConfigDTO.
type configDTO struct {
	AppId          string            `json:"appId"`
	Cluster        string            `json:"cluster"`
	NamespaceName  string            `json:"namespaceName"`
	Configurations map[string]string `json:"configurations"`
	ReleaseKey     string            `json:"releaseKey"`
}

// handleConfigs serves GET /configs/{appId}/{cluster}/{namespace}.
// Never-published namespaces answer 404, which is what makes clients
// fall back to their local cache, like against real Apollo.
func (s *Server) handleConfigs(w http.ResponseWriter, r *http.Request) {
	appID := r.PathValue("appId")
	cluster := r.PathValue("cluster")
	ns := r.PathValue("namespace")

	s.mu.Lock()
	st := s.getNamespaceLocked(appID, cluster, ns)
	var dto configDTO
	if st != nil {
		dto = configDTO{
			AppId:          appID,
			Cluster:        cluster,
			NamespaceName:  ns,
			Configurations: copyMap(st.configurations),
			ReleaseKey:     st.releaseKey,
		}
	}
	s.mu.Unlock()

	if st == nil {
		writeError(w, http.StatusNotFound,
			"could not load config with namespace: "+ns+" not found")
		return
	}

	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	_ = json.NewEncoder(w).Encode(dto)
}
