package miniapollo

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// ---- Feature A: NewServer / StartT (custom dimensions) ----

func TestNewServerWithApp(t *testing.T) {
	s := StartT(t, "shop", "dev")
	s.Set("application", map[string]string{"rpc.timeout": "100"})

	// shortcut APIs publish under the custom dimensions
	if got, ok := s.Config("application"); !ok || got["rpc.timeout"] != "100" {
		t.Fatalf("Config under custom dims: %v ok=%v", got, ok)
	}
	if id := s.NotificationID("application"); id != 1 {
		t.Fatalf("NotificationID under custom dims: want 1, got %d", id)
	}
	if _, ok := s.ReleaseKey("application"); !ok {
		t.Fatal("ReleaseKey under custom dims: missing")
	}

	// ...and are actually served there
	code, body := getJSON(t, s, "/configs/shop/dev/application")
	if code != http.StatusOK {
		t.Fatalf("custom dims config: want 200, got %d", code)
	}
	cfgs, _ := body["configurations"].(map[string]any)
	if cfgs["rpc.timeout"] != "100" {
		t.Fatalf("custom dims values: %v", body["configurations"])
	}

	// default dimensions stay untouched (no accidental SampleApp writes)
	code, _ = getJSON(t, s, "/configs/SampleApp/default/application")
	if code != http.StatusNotFound {
		t.Fatalf("SampleApp/default should stay 404, got %d", code)
	}
}

func TestNewServerValidation(t *testing.T) {
	if _, err := NewServer("", "dev"); err == nil {
		t.Fatal("empty appId should error")
	}
	if _, err := NewServer("app", ""); err == nil {
		t.Fatal("empty cluster should error")
	}
	if _, err := NewServer("app", "dev"); err != nil {
		t.Fatalf("valid dims should not error: %v", err)
	}
}

// ---- Feature B: LoadDir / LoadFile ----

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadDir(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"application.properties": "rpc.timeout=100\n\n# a comment\nname = mini\n",
		"layout.yml":             "items:\n  - one\n  - two\n",
		"rules.json":             `{"max": 9}`,
		"notes.txt":              "hello=world\n",
		"readme.md":              "ignored-extension",
		".hidden.properties":     "x=1",
	})
	s := StartT(t, "SampleApp", "default")

	loaded, err := s.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	sort.Strings(loaded)
	want := []string{"application", "layout.yml", "notes.txt", "rules.json"}
	if !reflect.DeepEqual(loaded, want) {
		t.Fatalf("loaded=%v want=%v", loaded, want)
	}

	// properties parsed to flat kv (comments/blank lines dropped)
	cfg, ok := s.Config("application")
	if !ok {
		t.Fatal("application not loaded")
	}
	if cfg["rpc.timeout"] != "100" || cfg["name"] != "mini" || len(cfg) != 2 {
		t.Fatalf("properties parse: %v", cfg)
	}

	// file-type namespaces keep the full name and raw content
	for ns, wantContent := range map[string]string{
		"layout.yml": "items:\n  - one\n  - two\n",
		"rules.json": `{"max": 9}`,
		"notes.txt":  "hello=world\n",
	} {
		cfg, ok := s.Config(ns)
		if !ok {
			t.Fatalf("%s not loaded", ns)
		}
		if cfg["content"] != wantContent {
			t.Fatalf("%s content: got %q want %q", ns, cfg["content"], wantContent)
		}
	}

	// all of them visible over HTTP, each with a real release
	code, body := getJSON(t, s, "/configs/SampleApp/default/rules.json")
	if code != http.StatusOK {
		t.Fatalf("rules.json http: %d", code)
	}
	if body["configurations"].(map[string]any)["content"] != `{"max": 9}` {
		t.Fatalf("rules.json http content: %v", body["configurations"])
	}

	// notifications see them as changed (client id 0 vs server id 1)
	code, out := poll(t, s, "SampleApp", "default",
		[]notifyItem{{NamespaceName: "application"}}, "")
	if code != http.StatusOK || len(out) != 1 || out[0].NotificationID != 1 {
		t.Fatalf("loaded namespace not notified: %d %v", code, out)
	}
}

func TestLoadDirWithCustomDims(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"application.properties": "k=v\n",
		"layout.yml":             "a: b\n",
	})
	s := StartT(t, "SampleApp", "default") // server dims, but we load with explicit dims

	loaded, err := s.LoadDirWith("shop", "dev", dir)
	if err != nil {
		t.Fatalf("LoadDirWith: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded: %v", loaded)
	}
	code, _ := getJSON(t, s, "/configs/shop/dev/application")
	if code != http.StatusOK {
		t.Fatalf("shop/dev/application: got %d", code)
	}
	// and NOT leaked into the server's own default dimensions
	code, _ = getJSON(t, s, "/configs/SampleApp/default/application")
	if code != http.StatusNotFound {
		t.Fatalf("SampleApp/default must stay 404, got %d", code)
	}
}

func TestLoadDirWithCustomDimsViaServer(t *testing.T) {
	// NewServer + LoadDir: both use the same custom dims
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"application.properties": "k=v\n"})
	s := StartT(t, "shop", "dev")

	loaded, err := s.LoadDir(dir)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("LoadDir on custom-dims server: %v %v", loaded, err)
	}
	if got, _ := s.Config("application"); got["k"] != "v" {
		t.Fatalf("custom dims LoadDir values: %v", got)
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "extra.yml")
	if err := os.WriteFile(f, []byte("a: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := StartT(t, "shop", "dev")

	ns, err := s.LoadFile(f)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if ns != "extra.yml" {
		t.Fatalf("namespace: got %q", ns)
	}
	cfg, _ := s.Config(ns)
	if cfg["content"] != "a: b\n" {
		t.Fatalf("LoadFile content: %v", cfg)
	}
}

func TestLoadDirMissingDir(t *testing.T) {
	s := StartT(t, "SampleApp", "default")
	if _, err := s.LoadDir(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("missing dir should error")
	}
	// empty dir loads nothing without error
	empty := t.TempDir()
	loaded, err := s.LoadDir(empty)
	if err != nil || len(loaded) != 0 {
		t.Fatalf("empty dir: %v %v", loaded, err)
	}
}

func TestParseProperties(t *testing.T) {
	src := "# comment\n!bang comment\n\nk1 = v1\nk2:v2\n nospace=3 \nnoseparatorline\n"
	got := parseProperties(src)
	want := map[string]string{"k1": "v1", "k2": "v2", "nospace": "3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseProperties: got %v want %v", got, want)
	}
}

func TestNamespaceOf(t *testing.T) {
	cases := map[string]string{
		"application.properties": "application",
		"application.PROPERTIES": "application", // case-insensitive ext
		"layout.yml":             "layout.yml",
		"layout.YAML":            "layout.YAML", // name untouched, ext recognized
		"rules.json":             "rules.json",
		"notes.txt":              "notes.txt",
	}
	for file, want := range cases {
		got, ok := namespaceOf(file)
		if !ok || got != want {
			t.Fatalf("namespaceOf(%q) = %q,%v want %q", file, got, ok, want)
		}
	}
	if _, ok := namespaceOf("readme.md"); ok {
		t.Fatal("namespaceOf(.md) should not match")
	}
}
