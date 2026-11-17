# Go 动态 HTTP → gRPC 网关

[返回项目首页](../README.md)

基于 Go 1.27.4 的独立 Go Modules 工程，不参与 Maven 构建。网关不生成或引用业务 stub：通过反射或 `FileDescriptorSet` 装载协议，使用 `dynamicpb`、`protojson` 和 `ClientConn.Invoke` 转换并调用 unary RPC。

新增接口时，实现并部署 Java RPC，然后发布对应的路由与协议描述，无需重新编译 Go 网关。修改网关转换逻辑或引入新的基础设施仍需重新构建。

## 启动与验证

先按[项目首页](../README.md)启动中间件、账户服务和交易服务。新开 Bash 终端，在项目根目录导出环境变量，再进入 Go 模块：

```bash
set -a
source .env
set +a
cd gateway
go test ./...
go run ./cmd/gateway
```

默认监听 `127.0.0.1:8080`，通过 Nacos 发现上游，每秒检查 `config/routes.yml`。初始配置无效或反射加载失败时退出，因此应先启动后端服务。

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
| `descriptor_set` | 可选描述文件；相对 `GATEWAY_DESCRIPTOR_DIR` 解析，留空使用反射 |
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

新配置在独立快照中准备。YAML、路由、字段或方法校验失败时，日志记录 `route update rejected`，保留旧快照。切换期间不会中断旧请求，新请求可能短暂等待，受旧路由超时约束。新快照未包含的旧路由会被移除。

描述文件修改后，也需要修改路由文件或重新发布 Nacos 配置以触发重载。相同内容不会触发文件更新；被拒绝的配置需要修正后再次发布，当前没有管理 HTTP 接口或自动重试加载任务。

### 本地文件

```bash
cp config/routes.yml config/routes.local.yml
go run ./cmd/gateway -routes config/routes.local.yml
```

编辑并保存所选文件即可更新。启动时文件必须有效，运行时文件暂时丢失则保留旧配置。

### Nacos 配置中心

在 Nacos 中创建 YAML 配置，以默认路由文件作为初始内容：Data ID 为 `gateway-routes.yml`，Group 为 `LEDGER_GROUP`，namespace 默认为 public。通过环境变量可覆盖这些值。

```bash
GATEWAY_ROUTES_SOURCE=nacos go run ./cmd/gateway
```

此模式不回退读取本地路由文件。收到变更通知后重新读取最新配置，避免直接应用排队的过时通知。服务发现与配置中心共用 Nacos namespace/group。开发 Nacos 使用 tmpfs，停止后需要重新发布配置。

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

随后设置 `descriptor_set: descriptors/platform.pb`，从 `gateway/` 启动时描述根目录默认为 `.`。生成物已被忽略，应与协议版本一起分发。文件必须包含所有依赖；Any 自定义消息也需包含在装载的描述集合中。描述上限为 8 MiB，反射最多加载 256 个文件。

本地描述有效不代表所有上游都实现了方法。滚动发布时，先确保目标实例支持新方法，或通过不同的 Nacos 服务名隔离版本，再发布路由；网关不自行推断实例协议版本。

## 服务发现

只选择 healthy、enabled、weight>0 且带 `protocol=grpc` 的 Nacos 实例。订阅变更后更新 gRPC resolver，按 round_robin 均衡可用连接；当前不按相对权重分配。空实例列表会停止向已移除地址发送新请求，实例恢复后可继续调用。

也可以使用静态地址：

```bash
go run ./cmd/gateway \
  -static-targets 'account-service=127.0.0.1:19091;trading-service=127.0.0.1:19082'
```

同一服务的地址用逗号分隔，不同服务用分号。静态模式只替代网关发现，交易服务自身访问账户服务仍使用 Java 侧配置。

## 配置项

启动参数 `-listen`、`-routes`、`-routes-source`、`-static-targets`、`-descriptor-dir` 优先于对应环境变量；`go run ./cmd/gateway -help` 查看参数。

| 环境变量 | 默认值或作用 |
| --- | --- |
| `GATEWAY_LISTEN_ADDR` | `127.0.0.1:8080` |
| `GATEWAY_ROUTES_SOURCE` / `GATEWAY_ROUTES_FILE` | `file` / `config/routes.yml` |
| `GATEWAY_NACOS_DATA_ID` | `gateway-routes.yml` |
| `GATEWAY_DESCRIPTOR_DIR` | `.`，描述文件的相对路径基准 |
| `GATEWAY_STATIC_TARGETS` | 空时使用 Nacos |
| `GATEWAY_API_TOKEN` | 可选固定 Bearer token，默认关闭 |
| `GATEWAY_STATE_DIR` | 默认用户缓存目录下的 ledger-core-gateway，存放 SDK 缓存与日志 |
| `GATEWAY_GRPC_TLS` | 默认 false；true 时启用上游证书验证 |
| `GATEWAY_GRPC_CA_FILE` / `GATEWAY_GRPC_SERVER_NAME` | 可选上游 CA 和证书名称，需要启用 gRPC TLS |
| `NACOS_SERVER_ADDR` | 默认 localhost:18848，支持逗号分隔的多个地址及 http/https scheme |
| `NACOS_NAMESPACE` / `NACOS_GROUP` | 默认 public / LEDGER_GROUP |
| `NACOS_USERNAME` / `NACOS_PASSWORD` | 已有部署的认证信息 |
| `NACOS_TLS_ENABLED` / `NACOS_CA_FILE` / `NACOS_SERVER_NAME` | Nacos SDK TLS 开关、CA 和证书名称，默认关闭 TLS |

HTTP 入口当前为明文开发监听，对外 HTTPS 由部署入口处理。上游 gRPC TLS 与 Nacos TLS 独立配置。目前没有 JWT、角色授权、mTLS、CORS 策略、限流或熔断。

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

默认不需要 Docker，测试后端也在运行时构造协议，不依赖业务 stub。覆盖反射依赖、描述文件、动态方法、参数类型、body 限制、超时/错误、身份头过滤、路由切换、实例移除及恢复。

账户服务、交易服务与项目 Nacos 已运行且导入 `.env` 后，可以执行进程级联调：

```bash
GATEWAY_JAVA_TEST=true go test -v ./cmd/gateway \
  -run TestJavaGatewayHotReload -count=1 -timeout=180s
```

测试启动临时网关，分别验证文件/静态地址和 Nacos 配置/发现模式，动态切换到另一项 Java RPC，发布无效方法后确认旧路由可用。测试创建并删除随机 Data ID，结束时关闭临时网关；Java 服务与中间件由启动者管理。
