# ledger-core

面向资金账户与模拟证券交易的 Java 后端基础框架。当前提供工程分层、中间件接入和 gRPC 服务基础配置，尚未实现开户、记账、下单等业务。

## 项目现状

当前处于基础设施与接口联调阶段，可通过 Go 网关调用交易服务，再由交易服务调用账户服务。Hello 示例不读写数据库；数据库脚本仅创建两个 schema。

| 范围 | 已实现 | 待建设 |
| --- | --- | --- |
| Java 服务 | gRPC 服务端、统一异常处理、请求标识与调用日志、共享客户端与连接管理、Nacos 注册与发现、虚拟线程执行器 | 账户与交易用例、业务校验、自动化测试 |
| 动态网关 | HTTP/JSON 转换、协议按需加载、服务发现、路由热更新 | 用户身份与权限体系、流量治理 |
| 数据与消息 | PostgreSQL、MyBatis-Plus、Redis、Kafka 客户端配置 | 业务表、账务规则、业务幂等、可靠事件投递 |
| 运行交付 | 环境配置、手动 SQL、构建与网关测试命令 | 数据库迁移管理、持续集成、对账与恢复流程 |

Java 模块当前没有自动化测试及测试依赖；Go 网关保留独立测试。当前架构及约束见[架构说明](docs/architecture.md)，后续建设顺序见[架构演进路线](docs/roadmap.md)。

## 文档导航

| 任务 | 文档 |
| --- | --- |
| 首次运行、启动服务和验证 gRPC 服务 | 本页“本地启动”和“gRPC 验证” |
| 启动 Go 动态 HTTP → gRPC 网关、配置动态路由 | [网关指南](gateway/README.md) |
| 了解模块依赖、调用链和当前边界 | [架构说明](docs/architecture.md) |
| 了解架构差距、建设顺序和阶段验收目标 | [架构演进路线](docs/roadmap.md) |
| 配置数据库、Nacos、Kafka、Redis 或复用已有中间件 | [配置与运行](docs/configuration.md) |
| 初始化数据库、手动执行 SQL | [数据库 SQL](db/README.md) |
| 使用 MyBatis-Plus、新增表、Mapper 和事务 | [数据库访问约定](docs/persistence.md) |
| 修改协议、新增功能、构建验证和排查问题 | [开发与排错](docs/development.md) |
| 使用共享客户端、配置调用策略或维护协议 | [gRPC 模块指南](ledger-grpc/README.md) |

## 技术版本

| 组件 | 版本 |
| --- | --- |
| JDK | 25 |
| Spring Boot | 4.1.2 |
| Spring Cloud | 2025.1.4 |
| Spring Cloud Alibaba | 2025.1.0.0 |
| Spring gRPC | 1.1.2，由 Spring Boot 管理 |
| Go（网关） | 1.27.4 |
| MyBatis-Plus | 3.5.18.1，Spring Boot 4 starter |
| Lombok | 由 Spring Boot 管理，仅用于编译期代码生成 |

Java 依赖版本以根 [pom.xml](pom.xml) 及其 BOM 为准，Go 版本以 [gateway/go.mod](gateway/go.mod) 为准。PostgreSQL、Nacos、Kafka 和 Redis 由运行环境提供，仓库不再附带中间件 Compose 文件。

## 工程结构

```text
ledger-grpc/               # 协议、生成的 stub、共享客户端与服务发现连接管理
account-service/           # 账户服务，gRPC 8081，独立 account schema
trading-service/           # 交易服务，gRPC 8082，独立 trading schema
gateway/                   # Go 动态网关，独立 Go Modules 构建
  config/config.yaml       # 网关启动配置：监听、Nacos、TLS、token 等
  config/routes.example.yml # HTTP → gRPC 服务映射示例，需发布到 Nacos
db/                        # 按服务划分的 SQL，部署前手动执行
docs/                      # 架构、运行配置、持久层与开发说明
```

服务通过 `interfaces.grpc → application → domain` 组织业务，基础设施实现放在 `infrastructure`。当前包含必要基础配置和 Hello 示例接口，业务分层随实际用例添加。禁止跨服务直接访问对方 schema。

日常业务开发主要修改两个 `*-service`，协议和共享客户端维护在 `ledger-grpc`。交易服务已按需启用账户客户端；账户服务默认不创建下游客户端。Java 默认使用 dev 本地配置；test/prod 从 Nacos 导入运行配置。文件职责、环境切换及发布步骤统一见[配置与运行](docs/configuration.md)。

## 本地启动

准备 JDK 25、Maven 3.9+ 和运行环境提供的中间件；启动 Go 网关还需 Go 1.27.4。仅编译打包无需启动中间件，首次构建需要下载依赖。

启动前完成以下准备：

1. 创建 Nacos namespace，ID 为 `ledger-dev`；默认地址 `localhost:8848`，Group 为 `LEDGER_GROUP`。
2. 核对两个服务的 `application-dev.yml`，填写实际数据库、Nacos、Redis 和 Kafka 参数，无需 `.env`。
3. 按[数据库 SQL](db/README.md)执行两份 `001_create_schema.sql`；服务不会自动建 schema 或表。

Java 默认使用 dev 本地配置，Nacos 只负责注册与发现。test/prod 的环境切换和远端配置发布步骤见[配置与运行](docs/configuration.md#环境切换)。`application-test.yml` 是测试环境运行配置，与自动化测试代码无关。

### 启动服务

以下单行命令同样适用于 Bash 和 PowerShell，从项目根目录执行：

```bash
mvn clean verify
# 首次启动前，先按 db/README.md 手动执行数据库初始化 SQL。
java -jar account-service/target/account-service-0.1.0-SNAPSHOT-exec.jar
```

另开终端，在项目根目录启动交易服务：

```bash
java -jar trading-service/target/trading-service-0.1.0-SNAPSHOT-exec.jar
```

无需导入环境变量文件。实际 Nacos 地址、namespace 或凭据与默认值不同时，可用命令行或部署环境变量覆盖，详见[临时覆盖配置](docs/configuration.md#临时覆盖配置)。

### 地址与启动顺序

| 进程 | 默认地址 | 启动顺序 |
| --- | --- | --- |
| 账户服务 | gRPC `localhost:8081` | 中间件就绪并完成数据库初始化后启动 |
| 交易服务 | gRPC `localhost:8082` | 中间件就绪并完成数据库初始化后启动 |
| Go 网关（可选） | HTTP `localhost:8080` | Nacos 就绪并发布服务映射后即可启动，Java 服务可稍后启动 |

表中是 dev 本地配置及 Nacos 发布示例中的默认端口。设置 `GRPC_PORT` 或修改对应 YAML 后，以服务实际监听端口为准。

Java 服务使用 `web-application-type: none`，不启动 HTTP 服务、不提供 REST 接口。gRPC 服务启动后将实际监听端口注册到 Nacos，元数据为 `protocol=grpc`；关闭应用时由 Nacos 自动注册组件注销实例。

| 中间件 | 配置模板中的开发环境示例地址 |
| --- | --- |
| PostgreSQL | `localhost:5432/ledger` |
| Redis | `localhost:6379` |
| Kafka | `localhost:9092` |
| Nacos API | `localhost:8848` |

这些地址是模板示例，不会自动创建对应服务。Nacos 的客户端 gRPC 和控制台端口、Kafka advertised address、认证及数据持久化由实际部署决定；客户端需要能够访问相关地址。

## gRPC 验证

dev 环境可以通过反射查看接口，两个服务分别提供账户和交易的 SayHello RPC：

```bash
grpcurl -plaintext localhost:8081 list
grpcurl -plaintext localhost:8082 list
```

检查交易到账户的实际调用链，可从 PowerShell 或 Bash 执行：

```bash
grpcurl -plaintext -d '{}' localhost:8082 ledger.trading.v1.TradingService/SayHello
```

默认请求返回 `Hello, World!`，响应中的 `service` 为 `trading-service`、`downstreamService` 为 `account-service`。仅列出反射接口不能证明下游调用成功。

业务协议按 `ledger/account/v1`、`ledger/trading/v1` 组织，详见[协议说明](ledger-grpc/README.md)。

## Go 动态网关

网关对外提供 HTTP/JSON，通过 Nacos 发现 Java gRPC 服务，支持协议按需加载和路由热更新；使用独立 Go Modules 构建。

先将 `gateway/config/config.yaml` 中的 `nacos` 地址、namespace、group 和凭据与 Java 服务对齐，将服务映射示例 `gateway/config/routes.example.yml` 发布到 Nacos：Data ID 为 `gateway-routes.yml`，Group 为 `LEDGER_GROUP`，namespace 默认为 `ledger-dev`。再从项目根目录启动：

```bash
cd gateway
go run ./cmd/gateway
```

启动配置、HTTP 调用、TLS、环境变量和热更新规则统一见[网关指南](gateway/README.md)。

## 中间件与配置

PostgreSQL、Redis、Kafka 和 Nacos 均由运行环境管理。连接参数与各环境配置来源见[配置与运行](docs/configuration.md)，数据库 CRUD、分页、乐观锁和事务约定见[数据库访问约定](docs/persistence.md)。当前没有业务表、Kafka 业务 topic 或业务缓存。

## 构建验证

```bash
# 编译、打包 Java 模块，无需运行中间件。
mvn verify
# Go 网关独立构建和测试，从项目根目录执行：
go -C gateway test ./...
```

Java 构建用于验证编译与打包，Go 测试使用测试后端；真实服务发现和跨服务调用按上方“gRPC 验证”联调。验证范围及排查步骤见[开发与排错](docs/development.md#构建与运行验证)。
