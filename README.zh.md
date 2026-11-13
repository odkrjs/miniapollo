[![Go Reference](https://pkg.go.dev/badge/github.com/odkrjs/miniapollo.svg)](https://pkg.go.dev/github.com/odkrjs/miniapollo)
[![Go Report Card](https://goreportcard.com/badge/github.com/odkrjs/miniapollo)](https://goreportcard.com/report/github.com/odkrjs/miniapollo)
![Go Version](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go)
![License](https://img.shields.io/badge/license-MIT-blue)

[English](README.md) | 中文

# miniapollo

**嵌入式 Apollo 配置中心 mock，专为单元测试而生**

在测试里起一个真正的 Apollo 太重了（Meta/Config/Admin 三件套 + MySQL）。`miniapollo` 是一个**纯标准库、零第三方依赖**的进程内
HTTP 服务，完整模拟 Apollo **Config Service 对客户端暴露的行为**，让真实 Apollo 客户端（Go
的 [agollo](https://github.com/apolloconfig/agollo)、Java SDK、[apollo-client](https://github.com/apolloconfig/apollo)
等）无需感知地连进来。

## 为什么需要 miniapollo

测试 Apollo 客户端逻辑时，通常面临两个选择：

| 方案           | 问题                                                     |
|--------------|--------------------------------------------------------|
| 起一套真 Apollo  | Meta + Config + Admin 三件套、MySQL、Eureka — CI 里跑不动，维护成本高 |
| 手写 mock HTTP | 容易遗漏长轮询语义、服务发现、文件型 namespace 折叠等细节，mock 与真实行为脱节        |

miniapollo 的切入点：**用一个进程内 HTTP 服务替代真 Apollo**，只模拟客户端能观察到的行为（`/configs`、
`/configfiles` 家族、`/notifications/v2` 长轮询、`/services/config` 服务发现），让真实客户端无感知地连进来，
测试代码只需 `Set` / `SetWith` 发布配置即可。

### 适用场景

- 单元测试中需要读取 Apollo 配置的业务逻辑
- 测试配置变更后的热刷新行为（`OnChange` / `OnNewestChange` 回调）
- 本地开发调试，不想依赖外部 Apollo 实例
- CI 流水线中跑集成测试，无法启动完整 Apollo

```go
m := miniapollo.StartT(t, "SampleApp", "default")  // 起服务，测试结束自动关闭
m.Set("application", map[string]string{            // 发布一个 namespace（自动出新 release）
    "rpc.timeout": "100",
})
m.Set("application.yml", map[string]string{"log": "info"})  // 文件型 namespace 也行

// 把你的客户端/被测代码指向 m.URL()
//   agollo:  env APOLLO_META=m.URL(), APP_ID=SampleApp
//   Java:    -Dapollo.meta=m.URL() -Dapp.id=SampleApp
```

## 模拟了 Apollo 的哪些行为

| Apollo 行为                                                  | miniapollo | 说明                                                                                                                             |
|------------------------------------------------------------|------------|--------------------------------------------------------------------------------------------------------------------------------|
| `GET /configs/{appId}/{cluster}/{namespace}`               | ✅          | 返回 `ApolloConfigDTO`：`configurations` + `releaseKey`；未发布 → **404**（客户端走本地缓存，与真实一致）                                             |
| `GET /configfiles/{appId}/{cluster}/{namespace}` | ✅ | configurations 拍平为 `k=v` 文本，`text/plain`（ConfigFileController 的 properties 形态） |
| `GET /configfiles/json/{appId}/{cluster}/{namespace}` | ✅ | configurations 序列化为 JSON —— **agollo 首次同步请求的正是这个**；文件型 namespace 以 `{"content": "<文件原文>"}` 到达；未发布 → **404 空 body**（与 `/configs` 不同，此处真实 Apollo 就是裸 404） |
| `GET /configfiles/raw/{appId}/{cluster}/{namespace}` | ✅ | 文件原文返回；`Content-Type` 按格式：`.json` → `application/json`，`.yml/.yaml` → `application/yaml`，其余 `text/plain`；properties namespace 拍平为 `k=v` |
| properties / `.yml` / `.yaml` / `.json` / `.txt` namespace | ✅          | 文件型 namespace 按真实方式折叠为 `configurations["content"]`；`.json` 为 map 的 JSON 序列化，`.yml/.yaml` 为扁平 `key: value`，`.txt` 为 `key=value` |
| `GET /notifications/v2` **长轮询**                            | ✅          | 客户端 id 落后 → 立即 200 返回**首个变化的 namespace**（真实 Apollo 同样只回一个）；无变化 → 挂住请求直到有发布或超时；超时返回 **404**（"无变更"，客户端继续下一轮）                     |
| `trigger` 参数（v2）                                           | ✅          | 按秒指定挂起时长，钳制在 1s..60s；非法值回退默认，与真实服务端一致                                                                                          |
| 未创建的 namespace 参与通知                                        | ✅          | 服务端视其为 id `-1`：客户端初始 `-1` 与之相等 → 不触发通知（真实 Apollo 语义）                                                                           |
| `notificationId` 语义                                        | ✅          | 每次发布 +1，per-(appId,cluster,namespace) 独立计数                                                                                     |
| `releaseKey` 格式                                            | ✅          | `yyyyMMddHHmmss-<12hex>`，每次发布轮换                                                                                                |
| `GET /notifications`（v1）                                   | ✅          | 同 v2 逻辑（真实客户端兼容层）                                                                                                              |
| `GET /services/config`（Meta 服务发现）                          | ✅          | 返回 `APOLLO-CONFIGSERVICE` 单实例，`homepageUrl` 即本服务 —— 客户端只给 meta 地址也能找到配置服务                                                      |
| 默认 `cluster=default`                                       | ✅          | 通知请求缺 cluster 时按 `default` 处理                                                                                                  |
| 404/400 错误体                                                | ✅          | `{"message": "..."}`（Spring 风格错误体）                                                                                             |
| 客户端断连                                                      | ✅          | 挂起的长轮询随 `r.Context().Done()` 释放，不泄漏 goroutine                                                                                  |
| 并发发布/多客户端轮询                                                | ✅          | 发布原子唤醒所有等待者；醒来后重算全部差异，事件不会丢失                                                                                                   |

### 暂不模拟（与真实 Apollo 的差异）

- **Portal / OpenAPI**（`/openapi/v1/*`）：那是管理面接口，客户端测试用不到。
- **灰度发布（Grayscale）、发布审批、公共 namespace 关联**：config service 对客户端不呈现这些概念。
- `Set` 即发布（隐式 release），没有"改了没发布"的中间态 —— 测试里少一步，语义更直接；真实感场景可用 `SetContent` + `Publish`
  组合。
- Admin Service `/services/admin`：无消费方，省略。
- 集群内多实例负载均衡：单实例即代表集群。

> 差异设计原则：**只模拟客户端可观察到的行为**。凡是客户端看不见的（Portal UI、DB、Eureka 集群拓扑），一律不作 —— 这是
> miniapollo 与"起一套真 Apollo"的本质区别。

## 已验证的真实客户端

| 客户端                                                          | 状态     | 说明                                                                                 |
|--------------------------------------------------------------|--------|------------------------------------------------------------------------------------|
| [agollo v5](https://github.com/apolloconfig/agollo) (Go SDK) | ✅ 示例通过 | 开箱即用（v5.0.0），首次同步即刻拿到真实值；见 `example/agollo/main.go`（`go run example/agollo/main.go`） |
| [agollo v4](https://github.com/apolloconfig/agollo) (Go SDK) | ✅ 测试通过 | v4.4.0 含严格 `MustStart` 首同步（依赖 `/configfiles/json`，缺它会报 "no config was read"）；见 `example/agollo/agollo_test.go`（`go test ./example/agollo`） |

agollo 适配要点（其他客户端同理）：`AppConfig.IP` 填 `m.URL()`（meta 地址），`AppID`/`Cluster`/`NamespaceName`
与发布维度一致即可。miniapollo 的 `/services/config` 会把自己注册为 config service，客户端无需额外地址。

### AddChangeListener / OnNewestChange

agollo v5 的 `AddChangeListener` 接受 `storage.ChangeListener` 接口（需实现 `OnChange` 和 `OnNewestChange` 两个方法），不接受裸
`func`。两个回调的区别：

| 回调               | 事件类型                       | 触发时机                  | 数据内容                                                                            |
|------------------|----------------------------|-----------------------|---------------------------------------------------------------------------------|
| `OnChange`       | `*storage.ChangeEvent`     | 每次检测到配置变更             | 只包含变更的 key，`ConfigChange.OldValue`/`NewValue` 为 `interface{}`（需 `.(string)` 断言） |
| `OnNewestChange` | `*storage.FullChangeEvent` | 每次配置刷新（先于 `OnChange`） | 包含 namespace 的**全量键值快照**，`Changes` 为 `map[string]interface{}`                   |

完整示例见 `example/agollo/main.go`。

## 安装与使用

```
go get github.com/odkrjs/miniapollo
```

### 单元测试

```go
func TestFetchConfig(t *testing.T) {
    m := miniapollo.StartT(t, "shop", "dev")
    m.Set("application", map[string]string{"rpc.timeout": "100"})
    m.SetContent("layout.yaml", "items: [a, b]")

    // 直接对 endpoint 断言，或把 m.URL() 注入你的真实客户端
    resp, err := http.Get(m.URL() + "/configs/shop/dev/application")
    require.NoError(t, err)
    defer resp.Body.Close()
    // ...json.Decode / 断言
}
```

### 测"配置变更推送"路径（长轮询被唤醒）

```go
func TestOnConfigChange(t *testing.T) {
    m := miniapollo.StartT(t, "SampleApp", "default")
    m.SetPollTimeout(150 * time.Millisecond)          // 测试别等 30s
    m.Set("application", map[string]string{"k": "v"})

    got := make(chan struct{})
    go func() {                                       // 你的客户端：挂长轮询
        resp, _ := http.Get(m.URL() + "/notifications/v2?appId=SampleApp&cluster=default" +
            `&notifications=[{"namespaceName":"application","notificationId":1}]`)
        if resp.StatusCode == 200 { close(got) }       // 被唤醒即收到 200
    }()

    time.Sleep(60 * time.Millisecond)                 // 让客户端先挂上
    m.Set("application", map[string]string{"k": "v2"}) // 发布 → 立即唤醒

    select {
    case <-got: // success
    case <-time.After(3 * time.Second):
        t.Fatal("long poll was not woken by publish")
    }
}
```

### 接真实客户端（agollo 为例）

```go
m := miniapollo.StartT(t, "SampleApp", "default")
m.Set("application", map[string]string{"k": "v"})

client, err := agollo.StartWithConfig(func() (*config.AppConfig, error) {
    return &config.AppConfig{
        AppID:         "SampleApp",
        Cluster:       "default",
        NamespaceName: "application",
        IP:            m.URL(),           // meta 地址，miniapollo 自动服务发现
    }, nil
})
// client.GetValue("k") → "v"
// client.AddChangeListener(...) → OnChange / OnNewestChange 回调
```

完整可运行示例见 `example/agollo/main.go`（`go run example/agollo/main.go`），覆盖三个场景：

1. 完整生命周期（服务发现 → 初始拉取 → 热刷新）
2. `AddChangeListener` → `OnChange`（OldValue/NewValue 差量回调）
3. `OnNewestChange`（全量快照回调）

### 创建服务时指定 appId / cluster

所有快捷 API（`Set` / `SetContent` / `Publish` / `Config` / `LoadDir` / ...）都发布到服务器创建时指定的维度。`NewServer(appID, cluster)` / `StartT(t, appID, cluster)` 在创建时一次性指定，免得每次都写 `SetWith`：

```go
m := miniapollo.StartT(t, "shop", "dev")
m.Set("application", map[string]string{"rpc.timeout": "100"})
// 服务于 /configs/shop/dev/application —— 不再需要 SetWith 重复三维度
```

### 从本地加载配置文件（文件名 = namespace）

`LoadDir` 把目录下每个可识别的配置文件发布为一个 namespace —— 手头已有配置文件时最方便：

```go
m := miniapollo.StartT(t, "shop", "dev")

loaded, err := m.LoadDir("./configs")
// loaded == []string{"application", "layout.yml", "rules.json"}
```

命名遵循真实 Apollo 惯例：

| 文件                     | namespace          | 内容                                        |
|------------------------|--------------------|---------------------------------------------|
| `application.properties` | `application`    | 按 `key=value` 逐行解析为扁平 kv               |
| `layout.yml` / `.yaml`  | `layout.yml`      | 文件原文作为 `configurations["content"]`       |
| `rules.json`            | `rules.json`      | 原文作为 `content`                          |
| `notes.txt`             | `notes.txt`       | 原文作为 `content`                          |

子目录、点开头文件、其他扩展名会被跳过。每个文件立即发布（新 release + 通知），挂起的长轮询会被唤醒。`LoadFile` 加载单个文件；`LoadDirWith` / `LoadFileWith` 支持显式指定维度。

## API 一览

| 方法                                               | 说明                                                                    |
|--------------------------------------------------|-----------------------------------------------------------------------|
| `NewServer(appID, cluster) (*Server, error)`     | 启动（127.0.0.1 随机端口），指定维度（快捷 API 全部用它）                  |
| `StartT(t, appID, cluster)`                      | 测试助手：启动 + 自动关闭，同时指定维度                                    |
| `Close()`                                        | 关闭；可重复调用                                                              |
| `URL() / Host()`                                 | `http://127.0.0.1:port` / `127.0.0.1:port`，给 `APOLLO_META` / meta 地址用 |
| `Set(ns, kv)`                                    | 发布 namespace（服务器维度），自动出新 release                             |
| `SetWith(app, cluster, ns, kv)`                  | 同上，显式三维度                                                              |
| `SetContent(ns, s)` / `SetContentWith(...)`      | 以原文发布文件型 namespace（真实 YAML/JSON blob）                                 |
| `LoadDir(dir) ([]string, error)`                 | 加载目录下全部配置文件为 namespace（文件名即 namespace），返回排序后列表        |
| `LoadDirWith(app, cluster, dir)`                 | 同上，显式指定维度                                                            |
| `LoadFile(file) (string, error)` / `LoadFileWith(...)` | 加载单个配置文件，返回发布成的 namespace                                |
| `Publish(ns)` / `PublishWith(...)`               | 不改值再发布一次（id+1、releaseKey 轮换、唤醒轮询）                                     |
| `Config(ns)` / `ConfigWith(...)`                 | 读回当前配置（拷贝），断言用                                                        |
| `ReleaseKey(ns)` / `ReleaseKeyWith(...)`         | 当前 releaseKey                                                         |
| `NotificationID(ns)` / `NotificationIDWith(...)` | 当前通知 id（发布序号）                                                         |
| `SetPollTimeout(d)`                              | 长轮询挂起时长，默认 30s（同真实 Apollo）；测试建议 100ms 级                               |

## 项目结构

```
miniapollo/
├── miniapollo.go              # 服务端核心（NewServer）
├── configs.go                 # /configs 端点
├── configfiles.go             # /configfiles 家族：properties / json / raw（agollo 首同步）
├── notifications.go           # /notifications 长轮询端点
├── services.go                # /services/config 服务发现
├── api.go                     # 公共 API（Set, SetWith, Publish, Config, ...）
├── loader.go                  # LoadDir / LoadFile：本地配置文件加载为 namespace
├── miniapollo_test.go         # 自身单元测试 + StartT 测试助手（go test 直接运行）
├── configfiles_test.go        # /configfiles 家族单元测试
├── loader_test.go             # LoadDir / LoadFile / 维度 单元测试
└── example/
    ├── basic/
    │   └── main.go            # 基础示例：指定维度 + 发布 + LoadDir
    └── agollo/
        ├── main.go            # agollo v5 客户端示例：lifecycle + OnChange + OnNewestChange
        └── agollo_test.go     # agollo v4 MustStart 首同步测试（go test ./example/agollo）
```

### 运行测试与示例

```bash
# miniapollo 自身单元测试（纯标准库，无需第三方）
go test -v

# 基础示例
go run example/basic/main.go

# agollo 客户端示例（需要 agollo 依赖）
go run example/agollo/main.go

# agollo v4 严格首同步测试（MustStart，需要 agollo 依赖）
go test -race ./example/agollo
```

## 设计要点

- **零依赖**：miniapollo 自身纯标准库（`net/http` + `encoding/json`），无第三方引入。`go.mod` 中的 agollo 依赖仅用于
  `example/agollo/` 示例，库本身零依赖。
- **Go 1.22+**：使用标准库 `ServeMux` 的方法+通配模式（`GET /configs/{appId}/{cluster}/{namespace}`），需要 Go 1.22 及以上。
- **发布广播**：`Publish` 唤醒所有长轮询等待者（容量 1 的 channel 投递，醒来后重算全量差异 —— 不丢事件的兜底，与 Redis 客户端
  lib 的惯用法一致）。
- **锁粒度**：单一 `sync.Mutex` 保护存储与订阅表；handler 不在锁内做 IO。

## Contributing

欢迎提 Issue 和 PR。

- 新增端点模拟：确保与真实 Apollo Config Service 的 HTTP 响应格式一致（含错误体、状态码语义）
- 新增客户端示例：放在 `example/` 目录下，以 `main.go` 形式提供可运行程序
- 单元测试：放在项目根目录的 `_test.go` 文件中（`package miniapollo`），`go test` 直接运行
- 提交前运行 `go test -v` 和 `go vet ./...` 确保通过

## License

MIT — 见 [LICENSE](LICENSE)。
