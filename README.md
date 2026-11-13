[![Go Reference](https://pkg.go.dev/badge/github.com/odkrjs/miniapollo.svg)](https://pkg.go.dev/github.com/odkrjs/miniapollo)
[![Go Report Card](https://goreportcard.com/badge/github.com/odkrjs/miniapollo)](https://goreportcard.com/report/github.com/odkrjs/miniapollo)
![Go Version](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go)
![License](https://img.shields.io/badge/license-MIT-blue)

English | [中文](README.zh.md)

# miniapollo

**An embedded Apollo config center mock, built for unit tests.**

Spinning up a real Apollo in tests is heavy (Meta + Config + Admin + MySQL). `miniapollo` is a **pure standard-library,
zero-dependency** in-process HTTP server that faithfully simulates the **Config Service behavior exposed to Apollo
clients**, so real Apollo clients (Go's [agollo](https://github.com/apolloconfig/agollo), the Java
SDK, [apollo-client](https://github.com/apolloconfig/apollo), etc.) can connect without any modification.

## Why miniapollo

When testing Apollo client logic, you typically face two options:

| Approach              | Problem                                                                                                      |
|-----------------------|--------------------------------------------------------------------------------------------------------------|
| Spin up real Apollo   | Meta + Config + Admin trio, MySQL, Eureka — too heavy for CI, high maintenance cost                          |
| Hand-rolled HTTP mock | Easy to miss long-poll semantics, service discovery, file-namespace folding — mock drifts from real behavior |

miniapollo's approach: **replace real Apollo with an in-process HTTP server** that only simulates what clients can
observe (`/configs`, the `/configfiles` family, `/notifications/v2` long-poll, `/services/config` service
discovery). Real clients connect transparently; test code just calls `Set` / `SetWith` to publish configs.

### Use cases

- Unit tests for business logic that reads Apollo configs
- Testing hot-reload behavior on config changes (`OnChange` / `OnNewestChange` callbacks)
- Local development without an external Apollo instance
- CI pipelines running integration tests where a full Apollo stack is impractical

```go
m := miniapollo.StartT(t, "SampleApp", "default")  // start server, auto-closed on test end
m.Set("application", map[string]string{            // publish a namespace (auto new release)
    "rpc.timeout": "100",
})
m.Set("application.yml", map[string]string{"log": "info"})  // file-type namespaces work too

// Point your client / code under test at m.URL()
//   agollo:  env APOLLO_META=m.URL(), APP_ID=SampleApp
//   Java:    -Dapollo.meta=m.URL() -Dapp.id=SampleApp
```

## What miniapollo simulates

| Apollo behavior                                             | miniapollo | Notes                                                                                                                                                                                                                             |
|-------------------------------------------------------------|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `GET /configs/{appId}/{cluster}/{namespace}`                | ✅          | Returns `ApolloConfigDTO`: `configurations` + `releaseKey`; unpublished → **404** (client falls back to local cache, same as real Apollo)                                                                                         |
| `GET /configfiles/{appId}/{cluster}/{namespace}` | ✅ | Configurations flattened to `k=v` text, `text/plain` (ConfigFileController properties form) |
| `GET /configfiles/json/{appId}/{cluster}/{namespace}` | ✅ | Configurations as JSON — **the form agollo requests at first sync**; file-type namespaces arrive as `{"content": "<file>"}`; unpublished → **404 with empty body** (bare 404 here, unlike `/configs`, same as real Apollo) |
| `GET /configfiles/raw/{appId}/{cluster}/{namespace}` | ✅ | File content verbatim; `Content-Type` by format: `.json` → `application/json`, `.yml/.yaml` → `application/yaml`, else `text/plain`; properties namespaces flatten to `k=v` |
| properties / `.yml` / `.yaml` / `.json` / `.txt` namespaces | ✅          | File-type namespaces folded into `configurations["content"]` the real way; `.json` is JSON-serialized map, `.yml/.yaml` is flat `key: value`, `.txt` is `key=value`                                                               |
| `GET /notifications/v2` **long-poll**                       | ✅          | Client id behind → immediate 200 with the **first changed namespace** (real Apollo also returns only one); no change → request hangs until publish or timeout; timeout returns **404** ("no change", client continues next round) |
| `trigger` parameter (v2)                                    | ✅          | Specifies hang duration in seconds, clamped to 1s..60s; invalid values fall back to default, matching real server behavior                                                                                                        |
| Non-existent namespaces in notifications                    | ✅          | Server treats them as id `-1`: client initial `-1` equals it → no notification triggered (real Apollo semantics)                                                                                                                  |
| `notificationId` semantics                                  | ✅          | Increments by 1 on each publish, counted independently per (appId, cluster, namespace)                                                                                                                                            |
| `releaseKey` format                                         | ✅          | `yyyyMMddHHmmss-<12hex>`, rotated on each publish                                                                                                                                                                                 |
| `GET /notifications` (v1)                                   | ✅          | Same logic as v2 (real client compatibility layer)                                                                                                                                                                                |
| `GET /services/config` (Meta service discovery)             | ✅          | Returns a single `APOLLO-CONFIGSERVICE` instance with `homepageUrl` pointing to this server — clients given only the meta address can still find the config service                                                               |
| Default `cluster=default`                                   | ✅          | Notification requests missing cluster are treated as `default`                                                                                                                                                                    |
| 404/400 error bodies                                        | ✅          | `{"message": "..."}` (Spring-style error body)                                                                                                                                                                                    |
| Client disconnect                                           | ✅          | Hung long-polls released via `r.Context().Done()`, no goroutine leaks                                                                                                                                                             |
| Concurrent publishes / multi-client polling                 | ✅          | Publish atomically wakes all waiters; on wake, full diff is recomputed — no events lost                                                                                                                                           |

### Not simulated (differences from real Apollo)

- **Portal / OpenAPI** (`/openapi/v1/*`): management-plane APIs, not needed for client tests.
- **Grayscale release, publish approval, public namespace association**: config service doesn't expose these concepts to
  clients.
- `Set` is an implicit publish (no "modified but unpublished" intermediate state) — one less step in tests, more direct
  semantics; for realistic scenarios use `SetContent` + `Publish`.
- Admin Service `/services/admin`: no consumers, omitted.
- Multi-instance load balancing within a cluster: a single instance represents the cluster.

> Design principle: **only simulate what clients can observe**. Anything invisible to clients (Portal UI, DB, Eureka
> cluster topology) is intentionally omitted — this is the fundamental difference between miniapollo and "spinning up real
> Apollo".

## Verified real clients

| Client                                                       | Status           | Notes                                                                                                                 |
|--------------------------------------------------------------|------------------|-----------------------------------------------------------------------------------------------------------------------|
| [agollo v5](https://github.com/apolloconfig/agollo) (Go SDK) | ✅ Example passes | Works out of the box (v5.0.0); first sync lands real values immediately; see `example/agollo/main.go` (`go run example/agollo/main.go`) |
| [agollo v4](https://github.com/apolloconfig/agollo) (Go SDK) | ✅ Test passes | v4.4.0 incl. strict `MustStart` — first sync needs `/configfiles/json`, previously failed with "no config was read"; see `example/agollo/agollo_test.go` (`go test ./example/agollo`) |

agollo integration notes (same applies to other clients): set `AppConfig.IP` to `m.URL()` (meta address), and keep
`AppID`/`Cluster`/`NamespaceName` consistent with the publish dimensions. miniapollo's `/services/config` registers
itself as the config service, so clients need no extra address.

### AddChangeListener / OnNewestChange

agollo v5's `AddChangeListener` accepts a `storage.ChangeListener` interface (requiring both `OnChange` and
`OnNewestChange` methods), not a bare `func`. The two callbacks differ:

| Callback         | Event type                 | Trigger timing                             | Data content                                                                                         |
|------------------|----------------------------|--------------------------------------------|------------------------------------------------------------------------------------------------------|
| `OnChange`       | `*storage.ChangeEvent`     | On each detected config change             | Only changed keys; `ConfigChange.OldValue`/`NewValue` are `interface{}` (need `.(string)` assertion) |
| `OnNewestChange` | `*storage.FullChangeEvent` | On each config refresh (before `OnChange`) | **Full key-value snapshot** of the namespace; `Changes` is `map[string]interface{}`                  |

See `example/agollo/main.go` for a complete example.

## Installation & Usage

```
go get github.com/odkrjs/miniapollo
```

### Unit tests

```go
func TestFetchConfig(t *testing.T) {
    m := miniapollo.StartT(t, "shop", "dev")
    m.Set("application", map[string]string{"rpc.timeout": "100"})
    m.SetContent("layout.yaml", "items: [a, b]")

    // Assert directly on the endpoint, or inject m.URL() into your real client
    resp, err := http.Get(m.URL() + "/configs/shop/dev/application")
    require.NoError(t, err)
    defer resp.Body.Close()
    // ...json.Decode / assertions
}
```

### Testing the "config change push" path (long-poll wake-up)

```go
func TestOnConfigChange(t *testing.T) {
    m := miniapollo.StartT(t, "SampleApp", "default")
    m.SetPollTimeout(150 * time.Millisecond)          // don't wait 30s in tests
    m.Set("application", map[string]string{"k": "v"})

    got := make(chan struct{})
    go func() {                                       // your client: hang on long-poll
        resp, _ := http.Get(m.URL() + "/notifications/v2?appId=SampleApp&cluster=default" +
            `&notifications=[{"namespaceName":"application","notificationId":1}]`)
        if resp.StatusCode == 200 { close(got) }       // woken up → received 200
    }()

    time.Sleep(60 * time.Millisecond)                 // let the client hang first
    m.Set("application", map[string]string{"k": "v2"}) // publish → immediate wake-up

    select {
    case <-got: // success
    case <-time.After(3 * time.Second):
        t.Fatal("long poll was not woken by publish")
    }
}
```

### Connecting a real client (agollo example)

```go
m := miniapollo.StartT(t, "SampleApp", "default")
m.Set("application", map[string]string{"k": "v"})

client, err := agollo.StartWithConfig(func() (*config.AppConfig, error) {
    return &config.AppConfig{
        AppID:         "SampleApp",
        Cluster:       "default",
        NamespaceName: "application",
        IP:            m.URL(),           // meta address, miniapollo handles service discovery
    }, nil
})
// client.GetValue("k") → "v"
// client.AddChangeListener(...) → OnChange / OnNewestChange callbacks
```

A fully runnable example is in `example/agollo/main.go` (`go run example/agollo/main.go`), covering three scenarios:

1. Full lifecycle (service discovery → initial fetch → hot reload)
2. `AddChangeListener` → `OnChange` (OldValue/NewValue delta callback)
3. `OnNewestChange` (full snapshot callback)

### Custom dimensions at server creation

All the shortcut APIs (`Set` / `SetContent` / `Publish` / `Config` / `LoadDir` / ...) publish under the dimensions specified at server creation. `NewServer(appID, cluster)` / `StartT(t, appID, cluster)` let you set them once, instead of repeating `SetWith` on every call:

```go
m := miniapollo.StartT(t, "shop", "dev")
m.Set("application", map[string]string{"rpc.timeout": "100"})
// served at /configs/shop/dev/application — no SetWith repetition
```

### Loading local config files (file name = namespace)

`LoadDir` publishes every recognized config file in a directory as a namespace — handy when you already keep configs as files on disk and want the embedded server to serve them:

```go
m := miniapollo.StartT(t, "shop", "dev")

loaded, err := m.LoadDir("./configs")
// loaded == []string{"application", "layout.yml", "rules.json"}
```

Naming follows real Apollo conventions:

| File                  | Namespace          | Content                                              |
|-----------------------|--------------------|------------------------------------------------------|
| `application.properties` | `application`    | parsed `key=value` lines (flat kv map)               |
| `layout.yml` / `.yaml`  | `layout.yml`     | raw file text as `configurations["content"]`         |
| `rules.json`            | `rules.json`     | raw file text as `content`                           |
| `notes.txt`             | `notes.txt`      | raw file text as `content`                           |

Sub-directories, dot-files and other extensions are skipped. Each file is published immediately (fresh release + notification), so parked long-pollers are woken. `LoadFile` does the same for a single file; `LoadDirWith` / `LoadFileWith` take explicit dimensions.

## API Reference

| Method                                           | Description                                                                                 |
|--------------------------------------------------|---------------------------------------------------------------------------------------------|
| `NewServer(appID, cluster) (*Server, error)`     | Start server (127.0.0.1 random port), shortcut APIs use the given dimensions               |
| `StartT(t, appID, cluster)`                      | Test helper: start + `t.Cleanup(Close)`, sets dimensions                                    |
| `Close()`                                        | Close; safe to call repeatedly                                                              |
| `URL() / Host()`                                 | `http://127.0.0.1:port` / `127.0.0.1:port`, for `APOLLO_META` / meta address                |
| `Set(ns, kv)`                                    | Publish namespace (server's dimensions), auto new release                                   |
| `SetWith(app, cluster, ns, kv)`                  | Same, with explicit three dimensions                                                        |
| `SetContent(ns, s)` / `SetContentWith(...)`      | Publish file-type namespace as raw content (real YAML/JSON blob)                            |
| `LoadDir(dir) ([]string, error)`                 | Load every config file in dir as a namespace (file name = namespace); returns sorted list   |
| `LoadDirWith(app, cluster, dir)`                 | Same, for explicit dimensions                                                               |
| `LoadFile(file) (string, error)` / `LoadFileWith(...)` | Load a single config file; returns the namespace it was published as                 |
| `Publish(ns)` / `PublishWith(...)`               | Publish again without changing values (id+1, releaseKey rotation, wake polls)               |
| `Config(ns)` / `ConfigWith(...)`                 | Read back current config (copy), for assertions                                             |
| `ReleaseKey(ns)` / `ReleaseKeyWith(...)`         | Current releaseKey                                                                          |
| `NotificationID(ns)` / `NotificationIDWith(...)` | Current notification id (publish sequence number)                                           |
| `SetPollTimeout(d)`                              | Long-poll hang duration, default 30s (same as real Apollo); recommend 100ms range for tests |

## Project Structure

```
miniapollo/
├── miniapollo.go              # Server core (NewServer)
├── configs.go                 # /configs endpoint
├── configfiles.go             # /configfiles family: properties / json / raw (agollo first-sync)
├── notifications.go           # /notifications long-poll endpoint
├── services.go                # /services/config service discovery
├── api.go                     # Public API (Set, SetWith, Publish, Config, ...)
├── loader.go                  # LoadDir / LoadFile: load local config files as namespaces
├── miniapollo_test.go         # Unit tests + StartT test helper (run with go test directly)
├── configfiles_test.go        # Unit tests for the /configfiles family
├── loader_test.go             # Unit tests for LoadDir / LoadFile / dimensions
└── example/
    ├── basic/
    │   └── main.go            # Basic example: specified dims + publish + LoadDir
    └── agollo/
        ├── main.go            # agollo v5 client example: lifecycle + OnChange + OnNewestChange
        └── agollo_test.go     # agollo v4 MustStart first-sync test (go test ./example/agollo)
```

### Running tests & examples

```bash
# miniapollo unit tests (pure stdlib, no third-party deps)
go test -v

# Basic example
go run example/basic/main.go

# agollo client example (requires agollo dependency)
go run example/agollo/main.go

# agollo v4 strict first-sync test (MustStart, requires agollo dependency)
go test -race ./example/agollo
```

## Design Highlights

- **Zero dependencies**: miniapollo itself uses only the standard library (`net/http` + `encoding/json`), no third-party
  imports. The agollo dependency in `go.mod` is solely for the `example/agollo/` example; the library itself has zero
  dependencies.
- **Go 1.22+**: Uses the standard library `ServeMux` method + wildcard patterns (
  `GET /configs/{appId}/{cluster}/{namespace}`), requiring Go 1.22 or later.
- **Publish broadcast**: `Publish` wakes all long-poll waiters (capacity-1 channel delivery; on wake, full diff is
  recomputed — a safety net against lost events, consistent with the Redis client lib idiom).
- **Lock granularity**: A single `sync.Mutex` protects storage and the subscription table; handlers never do IO while
  holding the lock.

## Contributing

Issues and PRs are welcome.

- New endpoint simulation: ensure HTTP response format matches real Apollo Config Service (including error bodies and
  status code semantics)
- New client examples: place under `example/` as a runnable `main.go` program
- Unit tests: place in `_test.go` files at the project root (`package miniapollo`), runnable via `go test`
- Run `go test -v` and `go vet ./...` before submitting to ensure they pass

## License

MIT — see [LICENSE](LICENSE).
