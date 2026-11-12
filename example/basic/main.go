// Package main demonstrates the typical miniapollo usage patterns:
// start the embedded server, publish namespaces, load a config directory,
// and inspect what any Apollo client would see through the HTTP endpoints.
//
// Run:
//
//	go run example/basic/main.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/odkrjs/miniapollo"
)

func main() {
	// Start with custom dimensions: every shortcut API below
	// publishes under (shop, dev) — no SetWith repetition needed.
	m, err := miniapollo.NewServer("shop", "dev")
	if err != nil {
		panic(err)
	}
	defer func() { _ = m.Close() }()

	// Publish a first release (properties namespace + file namespace).
	m.Set("application", map[string]string{"greeting.timeout": "100"})
	m.Set("application.yml", map[string]string{"log": "info"})

	// What any Apollo client sees on /configs:
	resp, _ := http.Get(m.URL() + "/configs/shop/dev/application")
	var cfg struct {
		Configurations map[string]string `json:"configurations"`
		ReleaseKey     string            `json:"releaseKey"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&cfg)
	resp.Body.Close()
	fmt.Println("timeout =", cfg.Configurations["greeting.timeout"])
	fmt.Println("releaseKey set:", len(cfg.ReleaseKey) > 0)

	// Change a value and observe the notification flow clients rely on:
	fmt.Println("id before:", m.NotificationID("application"))
	m.Set("application", map[string]string{"greeting.timeout": "42"})
	fmt.Println("id after: ", m.NotificationID("application"))
	v, _ := m.Config("application")
	fmt.Println("new timeout =", v["greeting.timeout"])

	// Load a whole directory of config files: the file names become
	// namespaces (application.properties -> "application",
	// layout.yml -> "layout.yml", ...).
	dir, _ := os.MkdirTemp("", "miniapollo-example")
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, "extra.properties"), []byte("feature.flags=on\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "layout.yml"), []byte("items: [a, b]\n"), 0o644)

	loaded, err := m.LoadDir(dir)
	if err != nil {
		panic(err)
	}
	fmt.Println("loaded from dir:", loaded)
	for _, ns := range loaded {
		c, _ := m.Config(ns)
		fmt.Printf("  %s -> %v\n", ns, c)
	}
}
