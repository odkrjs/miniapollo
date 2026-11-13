package main

import (
	"testing"
	"time"

	"github.com/apolloconfig/agollo/v4"
	"github.com/apolloconfig/agollo/v4/env/config"

	"github.com/odkrjs/miniapollo"
)

// TestAgolloV4MustStartFirstSync pins the gap the /configfiles family closes:
// agollo's initial sync requests configfiles/json/{app}/{cluster}/{ns} —
// without that endpoint a MustStart client gets zero configs and fails with
// "start failed cause no config was read" (v4 is the same story as v5's
// lenient first sync, just fatal).
//
// With the endpoint in place the contract is stronger than the v5 demos
// exercise: values must be available the very moment StartWithConfig returns —
// no 2s grace period for the re-sync ticker — for properties and file-type
// namespaces alike, and hot refresh keeps working afterwards.
func TestAgolloV4MustStartFirstSync(t *testing.T) {
	m, err := miniapollo.NewServer("SampleApp", "default")
	if err != nil {
		t.Fatalf("miniapollo: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	m.SetPollTimeout(500 * time.Millisecond)
	m.Set("application", map[string]string{"rpc.timeout": "100"})
	m.SetContent("layout.json", `{"port": 8081}`)

	client, err := agollo.StartWithConfig(func() (*config.AppConfig, error) {
		return &config.AppConfig{
			AppID:         "SampleApp",
			Cluster:       "default",
			NamespaceName: "application,layout.json",
			IP:            m.URL(),
			MustStart:     true, // strict mode: first sync must deliver everything
		}, nil
	})
	if err != nil {
		t.Fatalf("agollo v4 start with MustStart: %v", err)
	}
	defer client.Close()

	// First sync has already populated the cache — no polling, no grace period.
	if got := client.GetValue("rpc.timeout"); got != "100" {
		t.Fatalf("initial rpc.timeout = %q, want 100 (first sync failed?)", got)
	}
	layout := client.GetConfig("layout.json")
	if layout == nil {
		t.Fatal("layout.json not loaded by first sync")
	}
	if got := layout.GetValue("content"); got != `{"port": 8081}` {
		t.Fatalf("layout.json content = %q", got)
	}

	// Hot refresh: re-publish and let the client's re-sync loop pick it up.
	m.Set("application", map[string]string{"rpc.timeout": "250"})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if client.GetValue("rpc.timeout") == "250" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("hot refresh never observed rpc.timeout=250 (still %q)", client.GetValue("rpc.timeout"))
}
