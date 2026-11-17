# ledger-core

面向资金账户与模拟证券交易的 Java 后端基础框架。当前提供工程分层、中间件接入和 gRPC 诊断调用链，尚未实现开户、记账、下单等业务。

## 文档导航

| 任务 | 文档 |
| --- | --- |
| 首次运行、启动服务和调用诊断接口 | 本页“本地启动”和“gRPC 验证” |
| 启动 Go 动态 HTTP → gRPC 网关、配置动态路由 | [网关指南](gateway/README.md) |
| 了解模块依赖、调用链和当前边界 | [架构说明](docs/architecture.md) |
| 配置数据库、Nacos、Kafka、Redis 或复用已有中间件 | [配置与运行](docs/configuration.md) |
| 初始化数据库、手动执行 SQL | [数据库 SQL](db/README.md) |
| 使用 MyBatis-Plus、新增表、Mapper 和事务 | [数据库访问约定](docs/persistence.md) |
| 修改协议、新增功能、构建验证和排查问题 | [开发与排错](docs/development.md) |

## 技术版本

| 组件 | 版本 |
| --- | --- |
| JDK | 25 |
| Spring Boot | 4.1.2 |
| Spring Cloud | 2025.1.4 |
| Spring Cloud Alibaba | 2025.1.0.0 |
| Spring gRPC | 1.1.2，由 Spring Boot 管理 |
| Go（网关） | 1.27.4 |
| MyBatis-Plus | 3.5.18，Spring Boot 4 starter |

Java 依赖版本以根 [pom.xml](pom.xml) 及其 BOM 为准，Go 版本以 [gateway/go.mod](gateway/go.mod) 为准。PostgreSQL、Nacos、Kafka 和 Redis 由运行环境提供，仓库不再附带中间件 Compose 文件。

## 工程结构

```text
ledger-pb/                 # Protobuf 协议及构建时生成的 Java stub
account-service/           # 账户服务，gRPC 8081，独立 account schema
trading-service/           # 交易服务，gRPC 8082，独立 trading schema
gateway/                   # Go 动态网关，独立 Go Modules 构建
  config/config.yaml       # 网关启动配置：监听、Nacos、TLS、token 等
  config/routes.yml        # HTTP → gRPC 路由规则，支持热更新
db/                        # 按服务划分的 SQL，部署前手动执行
config/nacos/              # 发布到 Nacos 的公共与服务专属配置模板
.env.example               # Java 服务的环境变量模板
```

服务通过 `interfaces.grpc → application → domain` 组织业务，基础设施实现放在 `infrastructure`。当前只有诊断接口及必要配置，业务分层随实际用例添加。禁止跨服务直接访问对方 schema。

日常业务开发主要修改两个 `*-service`；每个服务在自己的 POM 中声明中间件依赖。`application.yml` 保留必要启动配置和 Nacos 公共参数，dev 的完整运行配置放在本地 `bootstrap-dev.yml`，Nacos 仅用于注册与发现；test/prod 通过对应 bootstrap 文件加载 Nacos 配置，模板见 [config/nacos/](config/nacos/README.md)。Nacos 注册与 MyBatis 插件配置类位于各服务的 `infrastructure.config`。协议维护在 `ledger-pb`，无需单独启动。

## 本地启动

需要 JDK 25、本地 Maven 3.9+，以及可访问的 PostgreSQL、Nacos、Kafka 和 Redis；Go 网关可选。开发环境先准备中间件，在 Nacos 创建 `ledger-dev` namespace，将 `.env.example` 复制为 `.env` 并填写注册中心连接信息，按实际环境调整两个服务的 `bootstrap-dev.yml`。dev 无需发布 Nacos 配置；test/prod 则需按 [Nacos 发布说明](config/nacos/README.md)发布公共、账户和交易配置。以下命令从项目根目录运行，首次构建需要下载依赖。

启动服务前需完成数据库初始化：按[数据库 SQL 说明](db/README.md)执行 `db/account/001_create_schema.sql` 和 `db/trading/001_create_schema.sql`。后续表结构变更也需手动执行对应 SQL，服务启动时不自动初始化数据库。

Java 服务默认使用 `dev`，namespace ID 为 `ledger-dev`。测试或生产环境通过 `--spring.profiles.active=test` / `prod` 或 `SPRING_PROFILES_ACTIVE` 切换，默认 namespace 分别为 `ledger-test` / `ledger-prod`；可用 `NACOS_NAMESPACE` 覆盖实际 ID。详见[环境切换](docs/configuration.md#环境切换)。`bootstrap-*.yml` 由 `application.yml` 显式导入。

### Bash

```bash
# 已有 .env 时保留自己的配置。
test -f .env || cp .env.example .env

# 首次使用时先编辑 .env；Java 进程需要显式导入环境变量。
set -a
source .env
set +a

mvn clean verify
# 首次启动前，先按 db/README.md 手动执行数据库初始化 SQL。
java -jar account-service/target/account-service-0.1.0-SNAPSHOT-exec.jar
```

另开终端，在项目根目录导入同一份 `.env`，再启动交易服务：

```bash
set -a
source .env
set +a
java -jar trading-service/target/trading-service-0.1.0-SNAPSHOT-exec.jar
```

### Windows PowerShell

```powershell
if (!(Test-Path -LiteralPath .env)) { Copy-Item -LiteralPath .env.example -Destination .env }

# 首次使用时先编辑 .env，再导入环境变量。
foreach ($line in Get-Content -LiteralPath .env -Encoding utf8) {
    if ($line -match '^\s*([A-Za-z_][A-Za-z0-9_]*)=(.*)$') {
        $value = $Matches[2].Trim().Trim([char[]]@(34, 39))
        [Environment]::SetEnvironmentVariable($Matches[1], $value, 'Process')
    }
}

mvn clean verify
# 首次启动前，先按 db/README.md 手动执行数据库初始化 SQL。
java -jar account-service/target/account-service-0.1.0-SNAPSHOT-exec.jar
```

另开终端，按[PowerShell 环境变量导入](docs/configuration.md#powershell)加载 `.env`，再运行 `java -jar trading-service/target/trading-service-0.1.0-SNAPSHOT-exec.jar`。

### 地址与启动顺序

| 进程 | 默认地址 | 启动顺序 |
| --- | --- | --- |
| 账户服务 | gRPC `localhost:8081` | 中间件就绪并完成数据库初始化后启动 |
| 交易服务 | gRPC `localhost:8082` | 账户服务启动后验证调用链 |
| Go 网关（可选） | HTTP `localhost:8080` | 两个 Java 服务启动后启动 |

表中是 dev 本地配置和 test/prod 远端模板中的默认端口。设置 `GRPC_PORT` 或修改对应 YAML 后，以服务实际监听端口为准。

Java 服务使用 `web-application-type: none`，不启动 HTTP 服务、不提供 REST 接口。gRPC 服务启动后将实际监听端口注册到 Nacos，元数据为 `protocol=grpc`；关闭应用时由 Nacos 自动注册组件注销实例。

| 中间件 | 配置模板中的开发环境示例地址 |
| --- | --- |
| PostgreSQL | `localhost:5432/ledger` |
| Redis | `localhost:6379` |
| Kafka | `localhost:9092` |
| Nacos API | `localhost:8848` |

这些地址是模板示例，不会自动创建对应服务。Nacos 的客户端 gRPC 和控制台端口、Kafka advertised address、认证及数据持久化由实际部署决定；客户端需要能够访问相关地址。

## gRPC 验证

安装 `grpcurl` 后执行以下 Bash 命令。PowerShell 用户也可以使用[网关指南中的 HTTP 验证](gateway/README.md#启动与验证)。

```bash
grpcurl -plaintext localhost:8081 list
grpcurl -plaintext -d '{"requestId":"smoke-001"}' \
  localhost:8081 ledger.platform.v1.AccountProbe/Check
grpcurl -plaintext -d '{"requestId":"smoke-001"}' \
  localhost:8082 ledger.platform.v1.TradingProbe/Check
```

最后一个调用会经 Nacos 查找账户服务，通过真实 gRPC 请求账户服务，返回 `service=trading-service` 和 `downstreamService=account-service`。客户端复用连接、按请求轮询发现的实例，调用超时为 2 秒；无实例时返回 `UNAVAILABLE`，不自动重试。当前连接使用明文，后续在对外部署前增加认证及 TLS。

交易服务的预期响应：

```json
{
  "service": "trading-service",
  "requestId": "smoke-001",
  "downstreamService": "account-service"
}
```

`grpcurl` 是可选工具，未安装时可以通过 Go 网关调用诊断接口，见[网关指南](gateway/README.md)。诊断接口不执行开户、资金变更或证券委托。

## Go 动态网关

网关对外提供 HTTP/JSON，对内通过 Nacos 发现 Java gRPC 服务，支持反射/描述文件动态调用与路由热更新。它不需要生成业务 Go stub，不参与 Maven 构建；需要 Go 1.27.4。网关独立读取 [gateway/config/config.yaml](gateway/config/config.yaml)，无需导入 `.env`。启动方式及更新路由见[网关指南](gateway/README.md)。

先将 `gateway/config/config.yaml` 中的 `nacos` 地址、namespace、group 和凭据与 Java 服务对齐，再从项目根目录启动：

```bash
cd gateway
go run ./cmd/gateway
```

使用其他配置文件时运行 `go run ./cmd/gateway -config config/config.local.yaml`。配置内相对路径以配置文件所在目录为基准。启动配置修改后重启网关；路由规则支持热更新。网关不再读取 `GATEWAY_*` 或 `NACOS_*` 环境变量。

## 中间件与配置

- PostgreSQL：MyBatis-Plus + Spring JDBC；SQL 集中在根目录 `db/`，按服务划分并手动执行，服务启动时不自动建 schema 或表。MyBatis-Plus 提供 BaseMapper、条件构造器、PostgreSQL 分页（单页上限 500）及 `@Version` 乐观锁，使用方法见[数据库访问约定](docs/persistence.md)。
- Kafka：预置 `KafkaTemplate` 和消费者工厂，生产者开启幂等及 `acks=all`，消费者关闭自动提交。当前不创建业务 topic，不启动业务监听器；生产者幂等不等于业务端到端幂等。
- Redis：预置 `StringRedisTemplate`；尚未缓存业务数据。
- Nacos：dev 仅使用注册与发现，配置中心已关闭。test/prod 先读取 `LEDGER_GROUP` 下的 `ledger-infrastructure.yml`，再读取自身的 `account-service.yml` / `trading-service.yml`，需在启动前发布。环境 namespace 默认分别为 `ledger-dev`、`ledger-test`、`ledger-prod`。

Java 的 Nacos 连接模板见 [.env.example](.env.example)；dev 运行参数在各服务的 `bootstrap-dev.yml`，test/prod 远端模板见 [config/nacos/](config/nacos/README.md)。Go 网关配置见 [gateway/config/config.yaml](gateway/config/config.yaml)。接入中间件时，按[配置与运行](docs/configuration.md#复用已有中间件)填写地址、认证及 TLS 配置。

## 构建验证

```bash
# 编译、打包 Java 模块，无需运行中间件。
mvn verify
```

当前 Java 模块未包含自动化测试。运行时调用链按上方“gRPC 验证”检查；Go 网关的测试命令见[网关指南](gateway/README.md)。
