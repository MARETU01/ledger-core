# Go 动态 HTTP → gRPC 网关

[返回项目首页](../README.md)

基于 Go 1.27.4 的独立 Go Modules 工程，不参与 Maven 构建。网关不生成或引用业务 stub：通过反射或 `FileDescriptorSet` 装载协议，使用 `dynamicpb`、`protojson` 和 `ClientConn.Invoke` 转换并调用 unary RPC。

新增接口时，实现并部署 Java RPC，然后发布对应的路由与协议描述，无需重新编译 Go 网关。修改网关转换逻辑或引入新的基础设施仍需重新构建。

## 启动与验证

先按[项目首页](../README.md)启动中间件、账户服务和交易服务。网关使用 [config/config.yaml](config/config.yaml)，无需导入 `.env`。从项目根目录进入 Go 模块：

```bash
cd gateway
go test ./...
go run ./cmd/gateway
```

默认监听 `127.0.0.1:8080`，通过 Nacos 发现上游，每秒检查 `config/routes.yml`。初始配置无效或反射加载失败时退出，因此应先启动后端服务。

dev 默认使用本地启动配置和路由文件，Nacos 仅作注册与发现，无需发布网关路由或 Java 运行配置。Java 服务的完整开发配置在各自的 `bootstrap-dev.yml`；test/prod 才使用远端 Java 配置。

PowerShell 用户在项目根目录执行 `Set-Location gateway` 和 `go run ./cmd/gateway`。在另一个终端验证 HTTP：

```powershell
Invoke-RestMethod 'http://localhost:8080/healthz'
Invoke-RestMethod 'http://localhost:8080/readyz'
Invoke-RestMethod 'http://localhost:8080/api/v1/probes/trading?requestId=chain'
```

以下 `curl` 和带行尾 `\` 的示例使用 Bash。

```bash
curl -fsS 'http://localhost:8080/healthz'
curl -fsS 'http://localhost:8080/readyz'
curl -fsS 'http://localhost:8080/api/v1/probes/account?requestId=hello'
curl -fsS -X POST 'http://localhost:8080/api/v1/probes/account' \
  -H 'Content-Type: application/json' -d '{"requestId":"hello"}'
curl -fsS 'http://localhost:8080/api/v1/probes/trading?requestId=chain'
```

`/healthz` 表示 HTTP 进程可响应；`/readyz` 返回已应用的路由 revision，不代表所有上游当前可用。当前探针不执行资金或交易操作。

```bash
go build -o bin/gateway ./cmd/gateway
./bin/gateway
```

`bin/` 已被 Git 忽略。Ctrl+C/SIGTERM 会停止接入、等待正在执行的请求并关闭连接和配置监听。

### 使用个人配置

在 `gateway/` 目录复制启动配置，再按环境修改：

```bash
cp -n config/config.yaml config/config.local.yaml
go run ./cmd/gateway -config config/config.local.yaml
```

PowerShell 对应命令：

```powershell
if (!(Test-Path -LiteralPath config/config.local.yaml)) {
    Copy-Item -LiteralPath config/config.yaml -Destination config/config.local.yaml
}
go run ./cmd/gateway -config config/config.local.yaml
```

`config/config.local.yaml` 已被 Git 忽略。配置文件必须存在，只接受一个 YAML 文档，大小不超过 1 MiB；省略的字段使用内置默认值。完整字段见 [config/config.yaml](config/config.yaml) 和下方“配置项”。从其他目录运行已构建的网关时，通过 `-config` 指定启动配置的路径。

## 路由格式

默认配置见 [config/routes.yml](config/routes.yml)。路径参数示例：

```yaml
version: 1
revision: account-2
routes:
  - id: account-probe
    http_method: GET
    http_path: /api/v1/probes/account/{requestId}
    target_service: account-service
    grpc_method: /ledger.platform.v1.AccountProbe/Check
    timeout: 2s
    path_fields:
      requestId: request_id
```

| 字段 | 约定 |
| --- | --- |
| `version` | 当前固定为 1 |
| `revision` | 必填版本标识，发布/回滚时建议使用可辨认的新值 |
| `routes` / `id` | 1～256 条路由，每条 ID 唯一 |
| `http_method` | GET、POST、PUT、PATCH、DELETE；GET 具有 Go HTTP 标准的 HEAD 匹配行为 |
| `http_path` | 必须以 `/api/` 开头，支持单段 `{name}` 参数，不支持尾段通配符 |
| `target_service` | Nacos 服务名或静态服务别名 |
| `grpc_method` | `/完整协议包.服务/方法`，只接受 unary RPC |
| `timeout` | 默认 2s，必须大于 0 且不超过 30s |
| `descriptor_set` | 可选描述文件；相对 `descriptor_dir` 解析，留空使用反射 |
| `path_fields` | 路径参数 → 请求字段，每个占位符都必须声明 |
| `query_fields` | Query 参数 → 请求字段 |

绑定目标支持 proto 字段名、JSON 字段名和点路径，例如 `request_id`、`requestId`、`filter.status`。中间字段必须是非重复消息，末端支持标量或重复标量；Query 重复参数映射为列表，路径参数不能映射到重复字段。两个 URL 参数不能绑定到同一目标字段。

处理顺序为 JSON body → Query → 路径参数，后者覆盖前者。未声明的 Query、未知 JSON 字段、错误类型或整数溢出返回 400。GET 不接受非空 body；其他方法的非空 body 必须为 `application/json`。

响应遵循 ProtoJSON：默认 lowerCamelCase，int64 为字符串，Timestamp 为标准时间字符串。成功响应当前统一为 200；尚未生成 OpenAPI 或实现 201/204、PATCH FieldMask 等业务响应策略。

## 热更新

```text
读取最新 YAML → 校验路由 → 新建连接并加载协议
             → 等待旧请求完成 → 整体切换 → 释放旧连接
```

新配置在独立快照中准备。YAML、路由、字段或方法校验失败时，日志记录 `route update rejected`，保留旧快照。切换需要等待旧请求结束，新请求可能短暂等待。路由 timeout 只覆盖下游 RPC；读取请求体另受 HTTP 服务的 ReadTimeout（10 秒）约束。新快照未包含的旧路由会被移除。

描述文件修改后，也需要修改路由文件或重新发布 Nacos 配置以触发重载。相同内容不会触发文件更新；被拒绝的配置需要修正后再次发布，当前没有管理 HTTP 接口或自动重试加载任务。

### 本地文件

```bash
cp config/routes.yml config/routes.local.yml
```

在 `config/config.yaml` 中设置 `routes_file: routes.local.yml`，再运行 `go run ./cmd/gateway`。编辑并保存所选路由文件即可更新。启动时文件必须有效，运行时文件暂时丢失则保留旧配置。

### Nacos 配置中心

在 Nacos 中创建 YAML 配置，以默认路由文件作为初始内容：Data ID 为 `gateway-routes.yml`，Group 为 `LEDGER_GROUP`，namespace 默认为 `ledger-dev`。通过 `config/config.yaml` 的 `nacos_data_id`、`nacos.group`、`nacos.namespace` 修改这些值；test/prod 对应 `ledger-test` / `ledger-prod` 或实际 namespace ID。

```yaml
# config/config.yaml
routes_source: nacos
```

保存后运行 `go run ./cmd/gateway`。

此模式不回退读取本地路由文件。收到变更通知后重新读取最新配置，避免直接应用排队的过时通知。服务发现与配置中心共用 Nacos namespace/group。配置数据是否在 Nacos 重启后保留取决于 Nacos 部署的持久化设置。

## 协议来源

反射先使用 v1，不支持时尝试 v1alpha。获取一个方法时，在同一条反射流内解析依赖，描述缓存于路由快照，请求处理不再访问反射。

后端关闭反射时，可以在项目根目录生成包含依赖的描述文件：

```bash
mkdir -p gateway/descriptors
protoc -I ledger-pb/src/main/proto \
  --include_imports \
  --descriptor_set_out=gateway/descriptors/platform.pb \
  ledger/platform/v1/probe.proto
```

随后设置 `descriptor_set: descriptors/platform.pb`。启动配置中的 `descriptor_dir` 默认为 `..`，相对于该启动配置所在目录，默认解析到 `gateway/`；路由的 `descriptor_set` 再相对于 `descriptor_dir` 解析，与路由文件所在目录无关。生成物已被忽略，应与协议版本一起分发。文件必须包含所有依赖；Any 自定义消息也需包含在装载的描述集合中。描述上限为 8 MiB，反射最多加载 256 个文件。

本地描述有效不代表所有上游都实现了方法。滚动发布时，先确保目标实例支持新方法，或通过不同的 Nacos 服务名隔离版本，再发布路由；网关不自行推断实例协议版本。

## 服务发现

只选择 healthy、enabled、weight>0 且带 `protocol=grpc` 的 Nacos 实例。订阅变更后更新 gRPC resolver，按 round_robin 均衡可用连接；当前不按相对权重分配。空实例列表会停止向已移除地址发送新请求，实例恢复后可继续调用。

也可以使用静态地址：

```yaml
# config/config.yaml
static_targets: 'account-service=127.0.0.1:8081;trading-service=127.0.0.1:8082'
```

同一服务的地址用逗号分隔，不同服务用分号。静态模式只替代网关发现，交易服务自身访问账户服务仍使用 Java 侧配置。

## 配置项

默认读取当前工作目录下的 `config/config.yaml`；可用 `go run ./cmd/gateway -config /path/to/config.yaml` 指定其他文件。网关不再读取环境变量，也不再提供逐项覆盖的启动参数。未知字段、错误类型或无效配置会阻止启动。

配置中的相对文件/目录路径均以 `config/config.yaml` 所在目录为基准。启动配置修改后需要重启；`routes_file` 或 Nacos 中的路由规则仍支持热更新。真实密码和 token 请放在单独的本地配置中，例如已被 Git 忽略的 `config/config.local.yaml`，通过 `-config config/config.local.yaml` 使用。

| YAML 字段 | 默认值或作用 |
| --- | --- |
| `listen_addr` | `127.0.0.1:8080` |
| `routes_source` / `routes_file` | `file` / `routes.yml`，默认解析到 `gateway/config/routes.yml` |
| `nacos_data_id` | `gateway-routes.yml` |
| `descriptor_dir` | `..`，默认解析到 `gateway/`，作为描述文件的相对路径基准 |
| `static_targets` | 空时使用 Nacos |
| `api_token` | 可选固定 Bearer token，默认关闭 |
| `state_dir` | 默认用户缓存目录下的 ledger-core-gateway，存放 SDK 缓存与日志 |
| `grpc_tls.enabled` | 默认 false；true 时启用上游证书验证 |
| `grpc_tls.ca_file` / `grpc_tls.server_name` | 可选上游 CA 和证书名称，CA 需要启用 gRPC TLS |
| `nacos.server_addr` | 默认 localhost:8848，支持逗号分隔的多个地址及 http/https scheme |
| `nacos.namespace` / `nacos.group` | 默认 ledger-dev / LEDGER_GROUP |
| `nacos.username` / `nacos.password` | 已有部署的认证信息 |
| `nacos.tls.enabled` / `nacos.tls.ca_file` / `nacos.tls.server_name` | Nacos SDK TLS 开关、CA 和证书名称，默认关闭 TLS |

`routes_file`、`descriptor_dir`、非空 `state_dir` 以及两个 TLS 节点的 `ca_file` 都按启动配置所在目录解析。`nacos.namespace` 填命名空间 ID，空字符串表示 public。`api_token` 非空时，业务请求需携带 `Authorization: Bearer <token>`；`/healthz` 和 `/readyz` 不校验该 token。

HTTP 入口当前为明文开发监听，对外 HTTPS 由部署入口处理。上游 gRPC TLS 与 Nacos TLS 独立配置。目前没有 JWT、角色授权、mTLS、CORS 策略、限流或熔断。

### 从环境变量迁移

将原配置值填入 YAML 后重启网关。旧的 `-listen`、`-routes`、`-routes-source`、`-static-targets` 和 `-descriptor-dir` 参数已移除，统一使用 `-config` 选择文件。

| 原环境变量 | YAML 字段 |
| --- | --- |
| `GATEWAY_LISTEN_ADDR` | `listen_addr` |
| `GATEWAY_ROUTES_SOURCE` / `GATEWAY_ROUTES_FILE` | `routes_source` / `routes_file` |
| `GATEWAY_NACOS_DATA_ID` | `nacos_data_id` |
| `GATEWAY_DESCRIPTOR_DIR` / `GATEWAY_STATIC_TARGETS` | `descriptor_dir` / `static_targets` |
| `GATEWAY_API_TOKEN` / `GATEWAY_STATE_DIR` | `api_token` / `state_dir` |
| `GATEWAY_GRPC_TLS` / `GATEWAY_GRPC_CA_FILE` / `GATEWAY_GRPC_SERVER_NAME` | `grpc_tls.enabled` / `grpc_tls.ca_file` / `grpc_tls.server_name` |
| `NACOS_SERVER_ADDR` / `NACOS_NAMESPACE` / `NACOS_GROUP` | `nacos.server_addr` / `nacos.namespace` / `nacos.group` |
| `NACOS_USERNAME` / `NACOS_PASSWORD` | `nacos.username` / `nacos.password` |
| `NACOS_TLS_ENABLED` / `NACOS_CA_FILE` / `NACOS_SERVER_NAME` | `nacos.tls.enabled` / `nacos.tls.ca_file` / `nacos.tls.server_name` |

Java 服务仍使用 `.env` 中的 `NACOS_*`，需要与网关 YAML 保持一致。YAML 中的 `${变量名}` 不会展开为环境变量。

## 错误与上下文

错误 JSON 包含 `code`、`message`、`requestId`；上游详细错误留在内部日志。body 上限 1 MiB，上游响应上限 4 MiB，路由 YAML 上限 1 MiB。

| 情况 | HTTP |
| --- | --- |
| 参数错误 / 未认证 / 无权限 | 400 / 401 / 403 |
| 路由或资源不存在 / 方法错误 | 404 / 405 |
| ALREADY_EXISTS、ABORTED | 409 |
| body 超限 / 内容类型错误 | 413 / 415 |
| RESOURCE_EXHAUSTED / 客户端取消 | 429 / 499 |
| UNIMPLEMENTED / UNAVAILABLE / DEADLINE_EXCEEDED | 501 / 503 / 504 |
| 其他内部异常 | 500 |

合法 `X-Request-ID` 会原样使用，否则随机生成；标识写入响应头和 gRPC metadata。仅额外转发 `Authorization`、`traceparent`、`tracestate`，不转发 `X-User-ID` 等自报身份字段。诊断 JSON 的 requestId 与 HTTP 请求标识是不同字段。

固定 API token 只提供简单访问门槛，不构成身份系统，健康检查不受 token 限制。网关显式禁用 gRPC 自动重试；资金写请求超时后仍可能已执行，幂等与结果查询由后端业务实现。

## 测试

在 `gateway/` 运行：

```bash
go test ./...
go vet ./...
# 需要可用的 C 编译器和 CGO。
CGO_ENABLED=1 go test -race ./...
```

默认测试不需要中间件，测试后端在运行时构造协议，不依赖业务 stub。覆盖启动配置的默认值、相对路径、错误配置、TLS/Nacos 参数，以及反射依赖、描述文件、动态方法、参数类型、body 限制、超时/错误、身份头过滤、路由切换、实例移除及恢复。

进程级联调当前使用 Unix SIGTERM 关闭临时网关，建议在 Linux/WSL 中运行。测试的静态发现分支仍固定访问账户 `19091`、交易 `19082`，因此需要在两个已导入 `.env` 的终端中分别覆盖 Java 端口：

```bash
# 终端一：从项目根目录运行。
GRPC_PORT=19091 java -jar account-service/target/account-service-0.1.0-SNAPSHOT-exec.jar

# 终端二：从项目根目录运行。
GRPC_PORT=19082 java -jar trading-service/target/trading-service-0.1.0-SNAPSHOT-exec.jar
```

项目 Nacos 和上述服务启动后，核对 `gateway/config/config.yaml` 中的 Nacos 配置，在第三个终端进入 `gateway/` 执行：

```bash
go test -v ./cmd/gateway \
  -run TestJavaGatewayHotReload -count=1 -timeout=180s -args -java-test
```

测试启动临时网关，分别验证文件/静态地址和 Nacos 配置/发现模式，动态切换到另一项 Java RPC，发布无效方法后确认旧路由可用。测试创建并删除随机 Data ID，结束时关闭临时网关；Java 服务与中间件由启动者管理。
