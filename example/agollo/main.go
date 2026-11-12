// Package main demonstrates a REAL agollo v5 client connecting to the
// embedded miniapollo server — no real Apollo, no docker required.
//
// It covers three scenarios:
//  1. Full client lifecycle: service discovery -> initial pull -> hot refresh.
//  2. AddChangeListener: OnChange callback with OldValue/NewValue.
//  3. OnNewestChange: full-snapshot callback (fires before OnChange).
//
// Run:
//
//	go run example/agollo/main.go            # all three demos
//	go run example/agollo/main.go newest     # a single demo
//
// agollo keeps process-global components (one long-poll notify loop per
// process), so the default mode runs each demo in its own subprocess to
// keep them from stealing each other's notifications.
package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/apolloconfig/agollo/v5"
	"github.com/apolloconfig/agollo/v5/env/config"
	"github.com/apolloconfig/agollo/v5/storage"

	"github.com/odkrjs/miniapollo"
)

func main() {
	if len(os.Args) > 1 {
		// single-demo mode (used by the subprocesses below)
		switch os.Args[1] {
		case "lifecycle":
			runLifecycleDemo()
			return
		case "onchange":
			runOnChangeDemo()
			return
		case "newest":
			runOnNewestChangeDemo()
			return
		default:
			log.Fatalf("unknown demo %q (want lifecycle|onchange|newest)", os.Args[1])
		}
	}

	// default: run each demo in its own subprocess, so agollo's process-global
	// notify component only ever polls the current demo's server.
	for _, demo := range []string{"lifecycle", "onchange", "newest"} {
		fmt.Println("--- demo:", demo)
		cmd := exec.Command(os.Args[0], demo)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("demo %s failed: %v", demo, err)
		}
	}
}

// newServer creates a miniapollo server with a short poll timeout
// so the demo runs fast.
func newServer() *miniapollo.Server {
	m, err := miniapollo.NewServer("SampleApp", "default")
	if err != nil {
		log.Fatalf("start miniapollo: %v", err)
	}
	m.SetPollTimeout(500 * time.Millisecond)
	return m
}

// newClient starts a real agollo client pointed at the miniapollo meta URL.
func newClient(m *miniapollo.Server, appID, cluster, namespace string) agollo.Client {
	client, err := agollo.StartWithConfig(func() (*config.AppConfig, error) {
		return &config.AppConfig{
			AppID:         appID,
			Cluster:       cluster,
			NamespaceName: namespace,
			IP:            m.URL(),
		}, nil
	})
	if err != nil {
		log.Fatalf("agollo start: %v", err)
	}
	return client
}

// runLifecycleDemo drives the full client lifecycle against miniapollo:
// service discovery -> initial pull -> change notification -> re-pull.
func runLifecycleDemo() {
	m := newServer()
	defer func() { _ = m.Close() }()

	// Publish the initial release BEFORE the client starts.
	m.SetWith("SampleApp", "default", "application", map[string]string{
		"rpc.timeout":   "100",
		"feature.flags": "on",
	})

	client := newClient(m, "SampleApp", "default", "application")
	defer client.Close() // release agollo's global components for the next demo

	fmt.Println("[lifecycle] initial rpc.timeout =", client.GetValue("rpc.timeout"))

	// Change a value server-side; agollo long-poll must wake and refresh.
	m.SetWith("SampleApp", "default", "application", map[string]string{
		"rpc.timeout":   "250",
		"feature.flags": "on",
	})

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if client.GetValue("rpc.timeout") == "250" {
			fmt.Println("[lifecycle] hot refresh picked up: rpc.timeout = 250")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Fatalf("[lifecycle] client never observed the new value")
}

// runOnChangeDemo verifies that AddChangeListener fires OnChange with
// correct OldValue/NewValue when miniapollo publishes a new release.
//
// The listener is registered AFTER StartWithConfig (agollo does not
// expose the client before start). The initial pull therefore does not
// trigger our listener. When the server publishes a second release, the
// re-pull fires a MODIFIED event with OldValue="100" / NewValue="250".
func runOnChangeDemo() {
	m := newServer()
	defer func() { _ = m.Close() }()

	m.SetWith("SampleApp", "default", "application", map[string]string{
		"rpc.timeout": "100",
	})

	client := newClient(m, "SampleApp", "default", "application")

	// Register listener after start; we will catch the MODIFIED event
	// from the second publish.
	got := make(chan *storage.ChangeEvent, 4)
	client.AddChangeListener(&chanChangeListener{ch: got})

	// Sanity: initial load is correct.
	if v := client.GetValue("rpc.timeout"); v != "100" {
		log.Fatalf("[OnChange] initial rpc.timeout = %q, want 100", v)
	}

	// Publish a new release; the re-pull fires a MODIFIED event.
	m.SetWith("SampleApp", "default", "application", map[string]string{
		"rpc.timeout": "250",
	})

	// Wait for the MODIFIED event, skipping any stale ADDED events.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case event := <-got:
			cc := event.Changes["rpc.timeout"]
			if cc.ChangeType == storage.MODIFIED {
				old, _ := cc.OldValue.(string)
				newVal, _ := cc.NewValue.(string)
				fmt.Printf("[OnChange] MODIFIED: namespace=%s rpc.timeout: %s -> %s\n",
					event.Namespace, old, newVal)
				return
			}
		case <-time.After(500 * time.Millisecond):
		}
	}
	log.Fatalf("[OnChange] MODIFIED event was never triggered")
}

// runOnNewestChangeDemo verifies that OnNewestChange fires with the FULL
// current key-value map (not just the diff) on every config refresh.
func runOnNewestChangeDemo() {
	m := newServer()
	defer func() { _ = m.Close() }()

	m.SetWith("SampleApp", "default", "application", map[string]string{
		"rpc.timeout":   "100",
		"feature.flags": "on",
	})

	client := newClient(m, "SampleApp", "default", "application")
	defer client.Close() // release agollo's global components for the next demo

	got := make(chan *storage.FullChangeEvent, 4)
	client.AddChangeListener(&fullChangeCapture{ch: got})

	// Publish a new release; OnNewestChange fires with the updated snapshot.
	m.SetWith("SampleApp", "default", "application", map[string]string{
		"rpc.timeout":   "250",
		"feature.flags": "on",
	})

	// Wait for the OnNewestChange event carrying the updated value.
	// Besides the push path (long-poll wake -> re-pull), reading a value
	// on a ticker also drives agollo's read-refresh path. If the event is
	// still pending after 3s, republish (id+1) to force another client
	// refresh — OnNewestChange fires on every SyncAndUpdate.
	deadline := time.Now().Add(30 * time.Second)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for time.Now().Before(deadline) {
		select {
		case event := <-got:
			val, _ := event.Changes["rpc.timeout"].(string)
			if val == "250" {
				flags, _ := event.Changes["feature.flags"].(string)
				fmt.Printf("[OnNewestChange] updated snapshot: namespace=%s rpc.timeout=%s feature.flags=%s\n",
					event.Namespace, val, flags)
				return
			}
		case <-tick.C:
			_ = client.GetValue("feature.flags")
		case <-time.After(3 * time.Second):
			m.SetWith("SampleApp", "default", "application", map[string]string{
				"rpc.timeout":   "250",
				"feature.flags": "on",
			})
		}
	}
	log.Fatalf("[OnNewestChange] updated event was never triggered")
}

// chanChangeListener implements storage.ChangeListener, forwarding
// every OnChange event into a channel.
type chanChangeListener struct {
	ch chan *storage.ChangeEvent
}

func (l *chanChangeListener) OnChange(event *storage.ChangeEvent) {
	l.ch <- event
}

func (l *chanChangeListener) OnNewestChange(_ *storage.FullChangeEvent) {}

// fullChangeCapture implements storage.ChangeListener, capturing only
// the FullChangeEvent delivered via OnNewestChange.
type fullChangeCapture struct {
	ch chan *storage.FullChangeEvent
}

func (l *fullChangeCapture) OnChange(_ *storage.ChangeEvent) {}

func (l *fullChangeCapture) OnNewestChange(event *storage.FullChangeEvent) {
	l.ch <- event
}
