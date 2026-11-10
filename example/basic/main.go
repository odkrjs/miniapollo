// Package main demonstrates the typical miniapollo usage pattern:
// start the embedded server, publish namespaces, and inspect what
// any Apollo client would see through the HTTP endpoints.
//
// Run:
//
//	go run example/basic/main.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/odkrjs/miniapollo"
)

func main() {
	m, err := miniapollo.NewServer()
	if err != nil {
		panic(err)
	}
	defer func() { _ = m.Close() }()

	// Publish a first release (properties namespace + file namespace).
	m.Set("application", map[string]string{"greeting.timeout": "100"})
	m.Set("application.yml", map[string]string{"log": "info"})

	// What any Apollo client sees on /configs:
	resp, _ := http.Get(m.URL() + "/configs/SampleApp/default/application")
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
}
