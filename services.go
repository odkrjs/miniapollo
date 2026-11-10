package miniapollo

import (
	"encoding/json"
	"net/http"
)

// serviceDTO mirrors Apollo meta-server's ServiceDTO
// (what /services/config returns to clients doing discovery).
type serviceDTO struct {
	AppName     string `json:"appName"`
	InstanceId  string `json:"instanceId"`
	HomepageURL string `json:"homepageUrl"`
}

// handleServicesConfig serves GET /services/config: service discovery
// answering with this server itself, so a real client (agollo, Java SDK)
// that only knows the meta URL finds the config service automatically.
func (s *Server) handleServicesConfig(w http.ResponseWriter, r *http.Request) {
	out := []serviceDTO{{
		AppName:     "APOLLO-CONFIGSERVICE",
		InstanceId:  "miniapollo",
		HomepageURL: s.URL(),
	}}
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	_ = json.NewEncoder(w).Encode(out)
}
