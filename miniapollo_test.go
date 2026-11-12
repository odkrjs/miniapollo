package miniapollo

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// StartT starts a server and wires it to the test lifecycle. Every shortcut
// API (Set / SetContent / Publish / Config / LoadDir / LoadFile / ...)
// publishes under the given (appID, cluster):
//
//	m := miniapollo.StartT(t, "shop", "dev")
//	m.Set("application", map[string]string{"rpc.timeout": "100"})
//	// served at /configs/shop/dev/application
//
// The server is closed when the test finishes.
func StartT(t *testing.T, appID, cluster string) *Server {
	t.Helper()
	s, err := NewServer(appID, cluster)
	if err != nil {
		t.Fatalf("miniapollo: could not start server: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Logf("miniapollo: close: %v", err)
		}
	})
	return s
}

// ---- helpers ----

func getJSON(t *testing.T, s *Server, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(s.URL() + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func poll(t *testing.T, s *Server, appID, cluster string, items []notifyItem, trigger string) (int, []notifyItem) {
	t.Helper()
	q := url.Values{}
	q.Set("appId", appID)
	q.Set("cluster", cluster)
	if raw, err := json.Marshal(items); err == nil {
		q.Set("notifications", string(raw))
	}
	if trigger != "" {
		q.Set("trigger", trigger)
	}
	resp, err := http.Get(s.URL() + "/notifications/v2?" + q.Encode())
	if err != nil {
		t.Fatalf("GET /notifications/v2: %v", err)
	}
	defer resp.Body.Close()
	var out []notifyItem
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode notifications body: %v", err)
		}
	}
	return resp.StatusCode, out
}

// ---- /configs ----

func TestConfigsEndpoint(t *testing.T) {
	s := StartT(t, "SampleApp", "default")
	s.Set("application", map[string]string{"timeout": "100"})
	s.SetWith("myapp", "pro", "application", map[string]string{"k": "v"})

	// published -> 200 with configurations and a release key
	code, body := getJSON(t, s, "/configs/SampleApp/default/application")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	cfgs, _ := body["configurations"].(map[string]any)
	if cfgs["timeout"] != "100" {
		t.Fatalf("configurations mismatch: %v", body["configurations"])
	}
	if rk, _ := body["releaseKey"].(string); rk == "" {
		t.Fatalf("empty releaseKey")
	}
	if body["appId"] != "SampleApp" || body["namespaceName"] != "application" {
		t.Fatalf("dto header mismatch: %v", body)
	}

	// explicit dimensions round-trip
	code, body = getJSON(t, s, "/configs/myapp/pro/application")
	if code != http.StatusOK || body["cluster"] != "pro" {
		t.Fatalf("explicit dims: code=%d body=%v", code, body)
	}
	if v := body["configurations"].(map[string]any)["k"]; v != "v" {
		t.Fatalf("myapp config mismatch: %v", body["configurations"])
	}

	// never published -> 404 (client would fall back to local cache)
	code, body = getJSON(t, s, "/configs/unknown/app/application")
	if code != http.StatusNotFound {
		t.Fatalf("want 404 for unknown namespace, got %d", code)
	}
	if m, _ := body["message"].(string); m == "" {
		t.Fatalf("404 body should carry a message: %v", body)
	}
}

func TestConfigsFileNamespaces(t *testing.T) {
	s := StartT(t, "SampleApp", "default")

	// .yml -> flattened to content=Apollo-style single "content" entry
	s.Set("application.yml", map[string]string{"server": "localhost", "port": "8080"})
	code, body := getJSON(t, s, "/configs/SampleApp/default/application.yml")
	if code != http.StatusOK {
		t.Fatalf("yml: want 200, got %d", code)
	}
	content := body["configurations"].(map[string]any)["content"].(string)
	want := "port: 8080\nserver: localhost\n"
	if content != want {
		t.Fatalf("yml content:\n got %q\nwant %q", content, want)
	}

	// .json -> content is the JSON blob
	s.Set("rules.json", map[string]string{"max": "9"})
	_, body = getJSON(t, s, "/configs/SampleApp/default/rules.json")
	blob := body["configurations"].(map[string]any)["content"].(string)
	var back map[string]string
	if err := json.Unmarshal([]byte(blob), &back); err != nil || back["max"] != "9" {
		t.Fatalf("json content round-trip failed: %q", blob)
	}

	// raw content via SetContent (real YAML blob)
	s.SetContent("layout.yaml", "items:\n  - one\n  - two\n")
	_, body = getJSON(t, s, "/configs/SampleApp/default/layout.yaml")
	if got := body["configurations"].(map[string]any)["content"]; got != "items:\n  - one\n  - two\n" {
		t.Fatalf("SetContent lost body: %q", got)
	}
}

// ---- /notifications ----

func TestNotificationsImmediateChange(t *testing.T) {
	s := StartT(t, "SampleApp", "default")
	s.Set("application", map[string]string{"a": "b"}) // id becomes 1

	// client initial id -1 vs server id 1 -> instant 200
	code, out := poll(t, s, "SampleApp", "default",
		[]notifyItem{{NamespaceName: "application", NotificationID: -1}}, "")
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d", code)
	}
	if len(out) != 1 || out[0].NamespaceName != "application" {
		t.Fatalf("changed list should hold exactly the changed ns: %v", out)
	}
	if out[0].NotificationID != 1 {
		t.Fatalf("notification id: want 1, got %d", out[0].NotificationID)
	}

	// up to date -> hold until 404 (use a short timeout to keep the test fast)
	s.SetPollTimeout(120 * time.Millisecond)
	start := time.Now()
	code, _ = poll(t, s, "SampleApp", "default",
		[]notifyItem{{NamespaceName: "application", NotificationID: 1}}, "")
	if code != http.StatusNotFound {
		t.Fatalf("want 404 on no change, got %d", code)
	}
	if el := time.Since(start); el < 100*time.Millisecond {
		t.Fatalf("server did not hold the request: %v", el)
	}
}

func TestNotificationsWakeOnPublish(t *testing.T) {
	s := StartT(t, "SampleApp", "default")
	s.Set("application", map[string]string{"a": "1"}) // id 1

	type res struct {
		code int
		out  []notifyItem
	}
	done := make(chan res, 1)
	go func() {
		code, out := poll(t, s, "SampleApp", "default",
			[]notifyItem{{NamespaceName: "application", NotificationID: 1}}, "")
		done <- res{code, out}
	}()

	// give the poller time to park, then publish: it must wake before
	// the generous 5s hold (default 30s capped here) expires.
	time.Sleep(150 * time.Millisecond)
	s.Set("application", map[string]string{"a": "2"}) // id 2

	select {
	case r := <-done:
		if r.code != http.StatusOK {
			t.Fatalf("woke with %d, want 200", r.code)
		}
		if len(r.out) != 1 || r.out[0].NamespaceName != "application" || r.out[0].NotificationID != 2 {
			t.Fatalf("wake body mismatch: %v", r.out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("long poll did not wake on publish")
	}
}

func TestNotificationsFirstChangedOnly(t *testing.T) {
	s := StartT(t, "SampleApp", "default")
	s.Set("ns.one", map[string]string{"a": "1"}) // id 1
	s.Set("ns.two", map[string]string{"b": "2"}) // id 1

	// both namespaces changed client-side-behind; answer must be a single
	// entry, the first one in request order (real Apollo behaviour)
	items := []notifyItem{
		{NamespaceName: "ns.one", NotificationID: -1},
		{NamespaceName: "ns.two", NotificationID: -1},
	}
	code, out := poll(t, s, "SampleApp", "default", items, "")
	if code != http.StatusOK || len(out) != 1 {
		t.Fatalf("want 200 with a single entry, got %d %v", code, out)
	}
	if out[0].NamespaceName != "ns.one" {
		t.Fatalf("want first-changed ns.one, got %v", out)
	}
}

func TestNotificationsAbsentNamespaceHolds(t *testing.T) {
	s := StartT(t, "SampleApp", "default")
	s.SetPollTimeout(100 * time.Millisecond)

	// namespace never published: server id -1 == client -1 -> no change
	code, _ := poll(t, s, "SampleApp", "default",
		[]notifyItem{{NamespaceName: "nope", NotificationID: -1}}, "")
	if code != http.StatusNotFound {
		t.Fatalf("absent namespace should 404-hold, got %d", code)
	}
}

func TestNotificationsTriggerParam(t *testing.T) {
	s := StartT(t, "SampleApp", "default")
	s.Set("application", map[string]string{"a": "b"})

	start := time.Now()
	code, _ := poll(t, s, "SampleApp", "default",
		[]notifyItem{{NamespaceName: "application", NotificationID: 1}}, "1")
	if code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", code)
	}
	// trigger=1s must clamp to >= 1s
	if el := time.Since(start); el < 900*time.Millisecond {
		t.Fatalf("trigger=1 held only %v", el)
	}

	// malformed trigger falls back to default poll timeout, not an error
	s.SetPollTimeout(80 * time.Millisecond)
	code, _ = poll(t, s, "SampleApp", "default",
		[]notifyItem{{NamespaceName: "application", NotificationID: 1}}, "abc")
	if code != http.StatusNotFound {
		t.Fatalf("bad trigger should fall back, got %d", code)
	}
}

// ---- /services/config ----

func TestServicesConfig(t *testing.T) {
	s := StartT(t, "SampleApp", "default")
	resp, err := http.Get(s.URL() + "/services/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []serviceDTO
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].AppName != "APOLLO-CONFIGSERVICE" {
		t.Fatalf("services/config payload: %v", out)
	}
	if out[0].HomepageURL != s.URL() {
		t.Fatalf("homepageUrl %q should be the server URL %q", out[0].HomepageURL, s.URL())
	}
}

// ---- API surface ----

func TestPublicAPI(t *testing.T) {
	s := StartT(t, "SampleApp", "default")
	if v, ok := s.Config("application"); ok {
		t.Fatalf("unpublished Config should miss, got %v", v)
	}
	if _, ok := s.ReleaseKey("application"); ok {
		t.Fatal("unpublished ReleaseKey should miss")
	}
	if id := s.NotificationID("application"); id != 0 {
		t.Fatalf("unpublished NotificationID want 0, got %d", id)
	}

	s.Set("application", map[string]string{"x": "1"})
	if got, _ := s.Config("application"); got["x"] != "1" {
		t.Fatalf("Config mismatch: %v", got)
	}
	rk1, _ := s.ReleaseKey("application")
	id1 := s.NotificationID("application")

	s.Set("application", map[string]string{"x": "2"})
	rk2, _ := s.ReleaseKey("application")
	id2 := s.NotificationID("application")
	if id1 == id2 {
		t.Fatalf("NotificationID did not advance: %d", id2)
	}
	if rk1 == rk2 {
		t.Fatalf("ReleaseKey did not change: %q", rk2)
	}

	// returned map is a copy: mutating it must not corrupt the server
	m, _ := s.Config("application")
	m["x"] = "999"
	if got, _ := s.Config("application"); got["x"] != "2" {
		t.Fatalf("Config map not defensive: %v", got)
	}

	// Publish alone re-releases without value change
	before, _ := s.Config("application")
	s.Publish("application")
	if after, _ := s.Config("application"); fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("Publish changed values: %v -> %v", before, after)
	}
	if s.NotificationID("application") != id2+1 {
		t.Fatalf("Publish should bump id to %d, got %d", id2+1, s.NotificationID("application"))
	}
}

// ---- end-to-end: full client flow against the embedded server ----

func TestEndToEndClientFlow(t *testing.T) {
	s := StartT(t, "SampleApp", "default")
	s.SetPollTimeout(200 * time.Millisecond)

	// 1. initial config goes live
	s.SetWith("shop", "dev", "application", map[string]string{"timeout": "100"})
	app := "shop"

	// 2. client starts: notify check reports the namespace (client at -1)
	code, out := poll(t, s, app, "dev",
		[]notifyItem{{NamespaceName: "application", NotificationID: -1}}, "")
	if code != http.StatusOK || out[0].NotificationID != 1 {
		t.Fatalf("step2: %d %v", code, out)
	}

	// 3. client pulls the config
	code, body := getJSON(t, s, "/configs/shop/dev/application")
	if code != http.StatusOK {
		t.Fatalf("step3: %d", code)
	}
	rk1 := body["releaseKey"].(string)
	if body["configurations"].(map[string]any)["timeout"] != "100" {
		t.Fatalf("step3 values: %v", body["configurations"])
	}

	// 4. client parks on notifications with the fresh id; server changes it
	woke := make(chan []notifyItem, 1)
	go func() {
		c, o := poll(t, s, app, "dev",
			[]notifyItem{{NamespaceName: "application", NotificationID: 1}}, "")
		if c == http.StatusOK {
			woke <- o
			return
		}
		woke <- nil
	}()
	time.Sleep(60 * time.Millisecond) // let the client park
	s.SetWith("shop", "dev", "application", map[string]string{"timeout": "200"})

	select {
	case o := <-woke:
		if o == nil || o[0].NotificationID != 2 {
			t.Fatalf("step4 wake: %v", o)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("step4: client never woke")
	}

	// 5. client re-pulls and sees the new value + a new release key
	code, body = getJSON(t, s, "/configs/shop/dev/application")
	if body["configurations"].(map[string]any)["timeout"] != "200" {
		t.Fatalf("step5 values: %v", body["configurations"])
	}
	if body["releaseKey"].(string) == rk1 {
		t.Fatal("step5: releaseKey should rotate on republish")
	}

	// 6. a fresh namespace answers 404 on /configs (not yet existing client side)
	code, _ = getJSON(t, s, "/configs/shop/dev/other")
	if code != http.StatusNotFound {
		t.Fatalf("step6: want 404, got %d", code)
	}
}

func TestBadNotificationRequests(t *testing.T) {
	s := StartT(t, "SampleApp", "default")

	resp, err := http.Get(s.URL() + "/notifications/v2?appId=x&notifications=notjson")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad notifications param: want 400, got %d", resp.StatusCode)
	}

	resp, err = http.Get(s.URL() + "/notifications/v2?notifications=")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing appId: want 400, got %d", resp.StatusCode)
	}

	// unknown path -> 404 with a message body
	resp, err = http.Get(s.URL() + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown path: want 404, got %d", resp.StatusCode)
	}
}

func TestServerCloseIdempotent(t *testing.T) {
	s, err := NewServer("SampleApp", "default")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close must be a no-op, got %v", err)
	}
	if s.Host() == "" {
		t.Fatal("Host() should survive Close")
	}
}
