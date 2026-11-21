# Go 动态 HTTP → gRPC 网关

[返回项目首页](../README.md)

基于 Go 1.27.4 的独立 Go Modules 工程，不参与 Maven 构建。网关不生成业务 stub：通过反射或 `FileDescriptorSet` 装载协议，以 `dynamicpb`、`protojson` 和 `ClientConn.Invoke` 调用 unary RPC。

对外路径统一为 `/api/{版本}/{服务简称}/{方法名}`。只配置服务对应关系，新增已开放 protobuf service 中的 unary 方法无需添加 HTTP 路由。

首次启动阅读[启动与验证](#启动与验证)，配置字段见[配置项](#配置项)，请求格式见[请求参数](#请求参数)，运行行为见[协议缓存、发现与热更新](#协议缓存发现与热更新)。Java 服务启动步骤见[项目首页](../README.md#本地启动)。

## 启动与验证

1. 准备 Nacos，调整 [config/config.yaml](config/config.yaml) 的地址、namespace、group 和凭据。
2. 将 [config/routes.example.yml](config/routes.example.yml) 完整发布到 Nacos：默认 Data ID 为 `gateway-routes.yml`，Group 为 `LEDGER_GROUP`，namespace ID 为 `ledger-dev`。
3. 在 `gateway/` 目录启动：

```bash
go test ./...
go run ./cmd/gateway
```

默认监听 `0.0.0.0:8080`，支持 `-config config/config.local.yaml` 选择个人启动配置；不会自动加载 `.env`。初始服务配置不存在或格式无效时退出。启动阶段不连接业务服务，不检查反射或描述文件，账户和交易服务可以稍后启动。

网关默认在加载有效服务配置并开始 HTTP 监听后，注册为同一 Nacos namespace/group 下的 `ledger-gateway` 临时实例，集群默认 `DEFAULT`、权重为 1，元数据为 `protocol=http`、`metrics_enabled=false`。注册失败会清理可能已注册的实例并停止 HTTP 服务；正常退出时先注销再等待已有请求结束，之后关闭 Nacos 客户端。强制终止依赖 Nacos 的临时实例失效机制。控制台可在相应 namespace 的 `LEDGER_GROUP` 分组查看实例；HTTP 就绪状态仍通过 `/readyz` 验证。

默认注册实际监听端口；具体监听 IP 会直接用于注册，`0.0.0.0` / `::` 通配监听时使用 Nacos SDK 的本机 IP 检测。多网卡、VPN、容器或端口映射部署需显式配置 `registration.ip` 和必要的 `registration.port`，保证调用方能访问注册地址；`127.0.0.1` 仅适合同机访问。不会注册通配、组播或链路本地地址。注册不会创建网关业务路由，也不会使网关成为 Java gRPC 下游。当前未提供 `/metrics`，后续接入 Prometheus 时再增加指标出口及相应元数据。

以下 HTTP 示例使用 Bash：

```bash
curl -fsS 'http://localhost:8080/healthz'
curl -fsS 'http://localhost:8080/readyz'
# 账户服务可用后：
curl -fsS 'http://localhost:8080/api/v1/account/SayHello?name=Alice'
curl -fsS -H 'Content-Type: application/json' \
  -d '{"name":"Alice"}' 'http://localhost:8080/api/v1/account/SayHello'
```

PowerShell 使用 `Invoke-RestMethod`，避免 Windows PowerShell 中 `curl` 别名及 JSON 参数转义的差异：

```powershell
Invoke-RestMethod 'http://localhost:8080/healthz'
Invoke-RestMethod 'http://localhost:8080/readyz'
Invoke-RestMethod 'http://localhost:8080/api/v1/account/SayHello?name=Alice'
$body = @{ name = 'Alice' } | ConvertTo-Json -Compress
Invoke-RestMethod -Method Post -Uri 'http://localhost:8080/api/v1/account/SayHello' -ContentType 'application/json' -Body $body
```

账户示例预期返回 `message: Hello, Alice!` 和 `service: account-service`。如配置了 `api_token`，业务请求还需提供 `Authorization: Bearer <token>`；Bash 使用 `-H`，PowerShell 使用 `-Headers @{ Authorization = 'Bearer <token>' }`。

`/healthz` 表示 HTTP 进程可响应；`/readyz` 返回生效的配置 revision，只表示服务映射已加载，不表示后端或协议可用。交易接口为 `/api/v1/trading/SayHello`，其实现仍需调用账户服务。

## 服务配置与迁移

```yaml
version: 1
revision: services-1
services:
  account:
    target_service: account-service
    proto_services:
      v1: ledger.account.v1.AccountService
    get_methods:
      v1: [SayHello]
    timeout: 2s
    # 可选：指定版本使用本地描述文件；省略时使用反射。
    # descriptor_sets:
    #   v1: descriptors/account.pb
```

| 字段 | 含义 |
| --- | --- |
| `version` / `revision` | 配置格式固定为 1；revision 必须非空 |
| `services` | 1～256 个服务，键为 URL 中的服务简称，如 account |
| `target_service` | 同 namespace/group 下的 Nacos 服务名 |
| `proto_services` | URL 版本到完整 protobuf service 的映射；每个服务 1～32 个版本 |
| `get_methods` | 每个版本允许 GET 的只读方法名列表；默认不开放 GET |
| `timeout` | 下游 RPC deadline，默认 2s，范围大于 0 且不超过 30s |
| `descriptor_sets` | 可选，按版本配置描述文件；相对 `descriptor_dir` 解析 |

服务简称使用小写字母开头，后续允许小写字母、数字和连字符；版本使用 `v1`、`v2` 等正整数版本。URL 区分大小写，必须恰好包含版本、服务和方法三个部分，不支持自定义路径或路径参数。外部请求无法指定任意上游地址或任意 protobuf service。

配置的 protobuf service 中所有 unary 方法均可通过 POST 访问；只应配置对外服务，内部 RPC 应放在其他 protobuf service 中。GET 清单由维护者保证只包含只读操作，网关不根据方法名猜测是否修改数据。流式方法返回 400；未知服务、版本或方法返回 404；不允许的 HTTP 方法返回 405 并携带 Allow 头。只有 POST 和清单内的 GET 可用，不自动开放 HEAD。

**旧版本迁移：** Nacos Data ID 和启动字段 `routes_data_id` 保持原名，但内容从 `routes` 列表改为 `services` 映射。删除 `http_method`、`http_path`、`grpc_method`、`query_fields`、`path_fields` 等逐接口字段，按最新示例发布。旧格式会被严格校验拒绝；部署时需协调配置切换与新网关重启，或通过另一个 Data ID 分阶段发布。更新本地示例不会直接改变 Nacos。

## 请求参数

GET 的 Query 参数必须与 `.proto` 中的原始字段名完全一致，区分大小写；嵌套路径的每一层均遵循此规则，不接受 JSON 字段名或自定义 `json_name`。例如 `account_id=123` 合法，`accountId=123` 返回 400。不再配置字段绑定：

| 请求字段 | Query 示例 |
| --- | --- |
| 字符串、数字、布尔、枚举 | `name=Alice&count=12&enabled=true&status=ACTIVE` |
| repeated 标量 | `tags=a&tags=b` |
| 嵌套消息的标量 | `filter.status=ACTIVE` |
| bytes | 标准带 padding 的 Base64，保留字符需 URL 编码 |

整数按目标位宽检查溢出，不经过浮点数转换；布尔只接受 true/false，枚举支持已声明名称或已声明数值。未知字段、重复的单值字段、oneof 多成员冲突、错误类型或编码均返回 400。省略字段遵循 Protobuf 默认值和 presence；业务必填校验由后端负责。Query 最多 16 KiB、128 个参数项、8 层字段路径；长度超限返回 414。map、消息列表以及完整消息字段通过 POST JSON 表达。

GET 不接受非空白 body。POST 只接受 JSON body，不接受 Query 参数，即使 Query 与 body 指向不同字段也返回 400；非空白 body 必须使用 `Content-Type: application/json`。JSON 使用标准 ProtoJSON 规则，仍接受原始字段名和 JSON 字段名，支持嵌套消息、map、消息列表和特殊类型；未知字段及非法 JSON 返回 400。空 body 表示空请求消息。

响应使用 ProtoJSON：字段名默认 lowerCamelCase，int64 为字符串，Timestamp 为标准时间字符串。成功统一返回 200，目前未实现 201/204、OpenAPI 或 PATCH FieldMask 等策略。

## 协议缓存、发现与热更新

首次请求某个服务版本时，网关建立可复用连接，加载该 protobuf service 的完整协议及依赖，再查找方法。同一服务版本的并发首次访问共享一次加载，单个等待者取消不会取消其他请求的加载；后台加载最多 5 秒。加载失败不缓存，后续请求可重试，无需重新发布配置。业务 RPC 使用独立的服务 timeout；HTTP ReadTimeout 为 10 秒，WriteTimeout 为 50 秒。

成功协议缓存 1 分钟，过期后由下一次请求刷新；缓存期间新增方法可能暂时返回 404。可以更新 revision 立即使新请求使用空缓存，避免等待过期。部署新接口时，先确保对应实例已实现方法；滚动发布的协议不一致仍需通过发布流程或独立服务名隔离。

反射先使用 v1，不支持时尝试 v1alpha。反射服务应在后端内部开放给网关。关闭反射时，先准备可用的 `protoc`，再从项目根目录生成描述文件。Bash 示例：

```bash
mkdir -p gateway/descriptors
protoc -I ledger-grpc/src/main/proto \
  --include_imports \
  --descriptor_set_out=gateway/descriptors/account.pb \
  ledger/account/v1/account_service.proto
```

PowerShell 示例：

```powershell
New-Item -ItemType Directory -Force gateway/descriptors | Out-Null
protoc -I ledger-grpc/src/main/proto --include_imports --descriptor_set_out=gateway/descriptors/account.pb ledger/account/v1/account_service.proto
```

在对应服务配置 `descriptor_sets.v1: descriptors/account.pb`。默认 `descriptor_dir: ..` 相对启动配置文件目录解析为 `gateway/`；描述文件路径再相对于它解析。文件需包含所有依赖和 Any 自定义消息。描述上限 8 MiB，反射最多加载 256 个文件。文件修改在缓存刷新后读取，更新 revision 可以立即刷新。文件不存在或内容无效会在请求时失败，不影响网关启动。

Nacos 只选择 healthy、enabled、weight>0 且有 `protocol=grpc` 的实例，按 round_robin 分配，不按相对权重分配。空实例列表停止向已移除地址发送新请求；实例恢复后可以重新调用，无需发布配置。

Nacos 配置热更新只做格式校验，不联系后端验证方法或协议；这些检查推迟到首次请求。配置有效时整体切换服务快照，每个新快照重新按需创建连接和缓存；旧请求继续使用旧快照。待请求及后台协议加载完成后，异步释放旧连接和订阅。无效更新记录 `service update rejected` 并保留当前快照。关闭时停止接入并等待当前和旧快照清理。

## 配置项

默认读取当前工作目录下的 `config/config.yaml`；可用 `go run ./cmd/gateway -config /path/to/config.yaml` 指定其他文件。启动 YAML 支持环境变量占位符，不提供逐项覆盖的启动参数。未知字段、错误类型或无效配置会阻止启动。

配置中的相对文件/目录路径均以实际选用的启动配置文件所在目录为基准。启动配置修改后需要重启；Nacos 中的服务配置支持热更新。真实密码和 token 可通过启动 YAML 中的环境变量占位符注入，也可放在已被 Git 忽略的 `config/config.local.yaml`，通过 `-config config/config.local.yaml` 使用。

| YAML 字段 | 默认值或作用 |
| --- | --- |
| `listen_addr` | `0.0.0.0:8080` |
| `registration.enabled` | 默认 true；false 仅关闭自身注册，仍读取 Nacos 配置并发现下游 |
| `registration.service_name` / `registration.cluster` | 默认 ledger-gateway / DEFAULT，使用 `nacos.namespace` 和 `nacos.group` |
| `registration.ip` / `registration.port` | IP 默认取具体监听地址或由 SDK 检测；端口默认 0，表示使用实际监听端口；支持显式配置可达 IP 和映射端口 |
| `routes_data_id` | `gateway-routes.yml` |
| `descriptor_dir` | `..`，默认解析到 `gateway/`，作为描述文件的相对路径基准 |
| `api_token` | 可选固定 Bearer token，默认关闭 |
| `state_dir` | 默认用户缓存目录下的 ledger-core-gateway，存放 SDK 缓存与日志 |
| `grpc_tls.enabled` | 默认 false；true 时启用上游证书验证 |
| `grpc_tls.ca_file` / `grpc_tls.server_name` | 可选上游 CA 和证书名称，CA 需要启用 gRPC TLS |
| `nacos.server_addr` | 默认 localhost:8848，支持逗号分隔的多个地址及 http/https scheme |
| `nacos.namespace` / `nacos.group` | 默认 ledger-dev / LEDGER_GROUP |
| `nacos.username` / `nacos.password` | 已有部署的认证信息 |
| `nacos.tls.enabled` / `nacos.tls.ca_file` / `nacos.tls.server_name` | Nacos SDK TLS 开关、CA 和证书名称，默认关闭 TLS |

`descriptor_dir`、非空 `state_dir` 以及两个 TLS 节点的 `ca_file` 都按启动配置所在目录解析。`nacos.namespace` 填命名空间 ID，空字符串表示 public。`api_token` 非空时，业务请求需携带 `Authorization: Bearer <token>`；`/healthz` 和 `/readyz` 不校验该 token。

HTTP 入口当前为明文开发监听，对外 HTTPS 由部署入口处理。上游 gRPC TLS 与 Nacos TLS 独立配置。目前没有 JWT、角色授权、mTLS、CORS 策略、限流或熔断。

### 环境变量占位符

启动 YAML 的字段值支持 `${VAR:默认值}` 和 `${VAR}`。变量未设置时使用默认值；没有默认值则启动报错。已设置但为空字符串的变量保留空值；默认值可包含冒号，例如 IPv6 地址。支持同一字段内多个占位符，不支持嵌套占位符或递归展开。布尔字段使用 `true` / `false`。只展开字段值，环境变量中的引号、换行等不会被解析为新的 YAML 内容。此功能只用于启动配置，不用于服务 YAML。

```yaml
listen_addr: "${GATEWAY_LISTEN_ADDR:0.0.0.0:8080}"
nacos:
  server_addr: "${NACOS_SERVER_ADDR:localhost:8848}"
  password: "${NACOS_PASSWORD:}"
```

PowerShell 中设置环境变量后启动：

```powershell
$env:GATEWAY_LISTEN_ADDR = '[::]:8080'
$env:NACOS_SERVER_ADDR = 'localhost:8848'
go run ./cmd/gateway
```

默认启动 YAML 已声明下表中的变量。自定义 YAML 只有显式写入占位符的字段才会读取环境变量。修改环境变量或启动配置后重启网关。旧的 `-listen`、`-routes`、`-routes-source`、`-static-targets` 和 `-descriptor-dir` 参数已移除，统一使用 `-config` 选择文件。

| 环境变量 | YAML 字段 |
| --- | --- |
| `GATEWAY_LISTEN_ADDR` | `listen_addr` |
| `GATEWAY_REGISTER_ENABLED` / `GATEWAY_SERVICE_NAME` | `registration.enabled` / `registration.service_name` |
| `GATEWAY_REGISTER_IP` / `GATEWAY_REGISTER_CLUSTER` | `registration.ip` / `registration.cluster`；映射端口通过 YAML 的 `registration.port` 配置 |
| `GATEWAY_ROUTES_DATA_ID` | `routes_data_id` |
| `GATEWAY_DESCRIPTOR_DIR` | `descriptor_dir` |
| `GATEWAY_API_TOKEN` / `GATEWAY_STATE_DIR` | `api_token` / `state_dir` |
| `GATEWAY_GRPC_TLS` / `GATEWAY_GRPC_CA_FILE` / `GATEWAY_GRPC_SERVER_NAME` | `grpc_tls.enabled` / `grpc_tls.ca_file` / `grpc_tls.server_name` |
| `NACOS_SERVER_ADDR` / `NACOS_NAMESPACE` / `NACOS_GROUP` | `nacos.server_addr` / `nacos.namespace` / `nacos.group` |
| `NACOS_USERNAME` / `NACOS_PASSWORD` | `nacos.username` / `nacos.password` |
| `NACOS_TLS_ENABLED` / `NACOS_CA_FILE` / `NACOS_SERVER_NAME` | `nacos.tls.enabled` / `nacos.tls.ca_file` / `nacos.tls.server_name` |

Java 服务的 Nacos 默认值在本地 YAML 中，也支持 `NACOS_*` 环境变量覆盖；部署时需与网关 YAML 保持一致，无需 `.env`。网关不会自动加载 `.env` 文件。

## 错误与上下文

错误 JSON 包含 `code`、`message`、`requestId`；上游详细错误留在内部日志。body 上限 1 MiB，上游响应上限 4 MiB，服务 YAML 上限 1 MiB。

| 情况 | HTTP |
| --- | --- |
| 参数错误 / 未认证 / 无权限 | 400 / 401 / 403 |
| 服务、版本、方法或资源不存在 / 方法错误 | 404 / 405 |
| ALREADY_EXISTS、ABORTED | 409 |
| body 超限 / Query 长度超限 / 内容类型错误 | 413 / 414 / 415 |
| RESOURCE_EXHAUSTED / 客户端取消 | 429 / 499 |
| UNIMPLEMENTED / UNAVAILABLE / DEADLINE_EXCEEDED | 501 / 503 / 504 |
| 其他内部异常 | 500 |

合法 `X-Request-ID` 会原样使用，否则随机生成；标识写入响应头和 gRPC metadata。仅额外转发 `Authorization`、`traceparent`、`tracestate`，不转发 `X-User-ID` 等自报身份字段。业务请求体中的标识字段与 HTTP 请求标识独立。

固定 API token 只提供简单访问门槛，不构成身份系统，健康检查不受 token 限制。网关显式禁用 gRPC 自动重试；资金写请求超时后仍可能已执行，幂等与结果查询由后端业务实现。

## 代码组织

启动入口和运行编排位于 `cmd/gateway`，核心实现位于 `internal`。同一包内按职责拆分文件，测试可直接复用对应包的实现：

```text
cmd/gateway/
  main.go          # 日志、进程信号和程序入口
  run.go           # 配置加载、服务配置订阅、HTTP 服务与关闭流程
  settings.go      # 上游 TLS 与 Nacos SDK 参数构造
internal/
  config/          # 启动配置、环境变量展开、服务配置校验
  descriptor/      # 反射与描述文件加载
  discovery/       # Nacos 服务发现及 gRPC resolver
  gateway/
    engine.go      # 服务快照、协议缓存与连接生命周期
    handler.go     # HTTP 请求处理、动态 gRPC 调用及响应编码
    bindings.go    # Query 自动绑定校验与 Protobuf 字段赋值
    errors.go      # gRPC 状态转换与统一 JSON 错误响应
    middleware.go  # 请求标识、固定 token 校验与访问日志
```

使用 `go run ./cmd/gateway` 或 `go build ./cmd/gateway` 构建整个入口包；单独运行 `main.go` 不会包含同包的其他文件。测试文件与被测代码放在同一目录，生成的描述文件放在 `descriptors/`，按协议版本分发。

## 测试

在 `gateway/` 运行：

```bash
go test ./...
go vet ./...
```

需要并发检测时，先准备可用的 C 编译器，再启用 CGO。Bash：

```bash
CGO_ENABLED=1 go test -race ./...
```

PowerShell：

```powershell
$env:CGO_ENABLED = '1'
go test -race ./...
```

默认测试不需要中间件，后端动态构造协议，不依赖业务 stub。覆盖配置校验、自动路由、Query 自动转换、JSON/请求体限制、HTTP 方法限制、超时/错误、metadata、后端离线启动与恢复、描述文件失败重试、并发冷请求、取消等待者、协议加载期间热更新，以及关闭时等待当前和旧快照的请求完成。

真实 Nacos 与 Java 服务联调时，先发布新版服务配置，再使用本页的两个 SayHello URL 验证。
