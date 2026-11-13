package miniapollo

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// getBody fetches a path and returns status, body and content type.
// /configfiles responses are plain text or JSON bodies — unlike /configs,
// errors here are a bare 404 with an empty body, so the helper keeps raw.
func getBody(t *testing.T, s *Server, path string) (int, string, string) {
	t.Helper()
	resp, err := http.Get(s.URL() + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return resp.StatusCode, string(body), resp.Header.Get("Content-Type")
}

// TestConfigFilesJson pins the agollo first-sync contract: the configurations
// map serialized as JSON — properties namespaces as a kv object, file-type
// namespaces wrapped in a single "content" key (what agollo's processJSONFiles
// feeds to its format parsers). Missing namespaces answer a bare 404.
func TestConfigFilesJson(t *testing.T) {
	s := StartT(t, "SampleApp", "default")
	s.Set("application", map[string]string{"timeout": "100"})
	s.SetContent("layout.json", `{"port": 8081}`)
	s.SetWith("myapp", "pro", "application", map[string]string{"k": "v"})

	// properties namespace → kv JSON object
	code, body, ctype := getBody(t, s, "/configfiles/json/SampleApp/default/application")
	if code != http.StatusOK {
		t.Fatalf("application: want 200, got %d", code)
	}
	if !strings.HasPrefix(ctype, "application/json") {
		t.Fatalf("application: content type %q", ctype)
	}
	got := map[string]string{}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("application: not valid JSON: %v (%s)", err, body)
	}
	if got["timeout"] != "100" {
		t.Fatalf("application: got %v, want timeout=100", got)
	}

	// file-type namespace → {"content": "<file text>"}
	code, body, _ = getBody(t, s, "/configfiles/json/SampleApp/default/layout.json")
	if code != http.StatusOK {
		t.Fatalf("layout.json: want 200, got %d", code)
	}
	wrapped := map[string]string{}
	if err := json.Unmarshal([]byte(body), &wrapped); err != nil {
		t.Fatalf("layout.json: not valid JSON: %v (%s)", err, body)
	}
	if wrapped["content"] != `{"port": 8081}` {
		t.Fatalf("layout.json: content = %q", wrapped["content"])
	}

	// explicit dimensions round-trip
	code, body, _ = getBody(t, s, "/configfiles/json/myapp/pro/application")
	if code != http.StatusOK {
		t.Fatalf("explicit dims: want 200, got %d", code)
	}
	if !strings.Contains(body, `"k":"v"`) {
		t.Fatalf("explicit dims: body %s", body)
	}

	// unpublished namespace → bare 404 (empty body), like real Apollo
	code, body, _ = getBody(t, s, "/configfiles/json/SampleApp/default/missing")
	if code != http.StatusNotFound {
		t.Fatalf("missing: want 404, got %d", code)
	}
	if strings.TrimSpace(body) != "" {
		t.Fatalf("missing: want empty 404 body, got %q", body)
	}
}

// TestConfigFilesProperties covers the plain form: configurations flattened
// to k=v lines as text/plain — including file-type namespaces, whose single
// "content" entry flattens like any other key (real Apollo behavior).
func TestConfigFilesProperties(t *testing.T) {
	s := StartT(t, "SampleApp", "default")
	s.Set("application", map[string]string{"timeout": "100", "flag": "on"})
	s.SetContent("layout.json", `{"port": 8081}`)

	code, body, ctype := getBody(t, s, "/configfiles/SampleApp/default/application")
	if code != http.StatusOK {
		t.Fatalf("application: want 200, got %d", code)
	}
	if !strings.HasPrefix(ctype, "text/plain") {
		t.Fatalf("application: content type %q", ctype)
	}
	if body != "flag=on\ntimeout=100\n" {
		t.Fatalf("application: body %q", body)
	}

	// file-type namespace flattens its single content entry
	code, body, ctype = getBody(t, s, "/configfiles/SampleApp/default/layout.json")
	if code != http.StatusOK || !strings.HasPrefix(ctype, "text/plain") {
		t.Fatalf("layout.json: code=%d ctype=%q", code, ctype)
	}
	if body != "content={\"port\": 8081}\n" {
		t.Fatalf("layout.json: body %q", body)
	}

	code, _, _ = getBody(t, s, "/configfiles/SampleApp/default/missing")
	if code != http.StatusNotFound {
		t.Fatalf("missing: want 404, got %d", code)
	}
}

// TestConfigFilesRaw covers the raw form: the file content verbatim with a
// format-derived content type; properties namespaces flatten to k=v text.
func TestConfigFilesRaw(t *testing.T) {
	s := StartT(t, "SampleApp", "default")
	s.SetContent("layout.json", `{"port": 8081}`)
	s.SetContent("layout.yml", "port: 8081\n")
	s.SetContent("notes.txt", "hello\n")
	s.Set("application", map[string]string{"timeout": "100"})

	cases := []struct {
		ns    string
		want  string
		ctype string
	}{
		{"layout.json", `{"port": 8081}`, "application/json"},
		{"layout.yml", "port: 8081\n", "application/yaml"},
		{"notes.txt", "hello\n", "text/plain"},
		{"application", "timeout=100\n", "text/plain"},
	}
	for _, tc := range cases {
		code, body, ctype := getBody(t, s, "/configfiles/raw/SampleApp/default/"+tc.ns)
		if code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d", tc.ns, code)
		}
		if body != tc.want {
			t.Fatalf("%s: body %q, want %q", tc.ns, body, tc.want)
		}
		if !strings.HasPrefix(ctype, tc.ctype) {
			t.Fatalf("%s: content type %q, want %q", tc.ns, ctype, tc.ctype)
		}
	}

	code, _, _ := getBody(t, s, "/configfiles/raw/SampleApp/default/missing")
	if code != http.StatusNotFound {
		t.Fatalf("missing: want 404, got %d", code)
	}
}
