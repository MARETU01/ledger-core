# ledger-core

面向资金账户与模拟证券交易的 Java 后端基础框架。当前提供工程分层、中间件接入和 gRPC 诊断调用链，尚未实现开户、记账、下单等业务。

## 文档导航

| 任务 | 文档 |
| --- | --- |
| 首次运行、启动服务和调用诊断接口 | 本页“本地启动”和“gRPC 验证” |
| 了解模块依赖、调用链和当前边界 | [架构说明](docs/architecture.md) |
| 配置数据库、Nacos、Kafka、Redis 或复用已有中间件 | [配置与运行](docs/configuration.md) |
| 使用 MyBatis-Plus、新增表、Mapper 和事务 | [数据库访问约定](docs/persistence.md) |
| 修改协议、新增功能、选择测试和排查问题 | [开发与排错](docs/development.md) |

## 技术版本

| 组件 | 版本 |
| --- | --- |
| JDK | 25 |
| Spring Boot | 4.1.2 |
| Spring Cloud | 2025.1.4 |
| Spring Cloud Alibaba | 2025.1.0.0 |
| Spring gRPC | 1.1.2，由 Spring Boot 管理 |
| MyBatis-Plus | 3.5.18，Spring Boot 4 starter |
| PostgreSQL | 18.7 |
| Nacos | 3.3.0 |
| Kafka | 4.4.0 |
| Redis | 8.10.3 |

Spring Boot、Spring Cloud 选用搭建时 Maven Central 可用的最新正式版本，不采用 milestone/RC。Cloud 2025.1.2 起支持 Boot 4.1.x，参见 [Spring Cloud 兼容矩阵](https://spring.io/projects/spring-cloud/)。gRPC、Protobuf、驱动和客户端依赖跟随 Boot BOM；中间件镜像版本参考 `/root/docker`，该目录未修改。

## 工程结构

```text
ledger-contracts/          # Protobuf 协议及构建时生成的 Java stub
ledger-platform/           # 公共配置、Nacos gRPC 注册和发现客户端
account-service/           # 账户服务，gRPC 19091，独立 account schema
trading-service/           # 交易服务，gRPC 19082，独立 trading schema
ledger-integration-tests/  # 调用链与可选真实中间件验证
gateway/                   # 网关占位，暂不参与构建
compose.yml                # 本项目独立的开发中间件
```

服务通过 `interfaces.grpc → application → domain` 组织业务，基础设施实现放在 `infrastructure`。当前只有诊断接口及必要配置，业务分层随实际用例添加。禁止跨服务直接访问对方 schema。

## 本地启动

需要 JDK 25、本地 Maven 3.9+ 和 Docker Compose。以下命令使用 Bash，并在项目根目录运行。首次构建需要下载项目依赖。

```bash
# 已有 .env 时保留自己的配置。
test -f .env || cp .env.example .env
docker compose up -d --wait --wait-timeout 180

# Docker Compose 自动读取 .env；Java 进程需要显式导入环境变量。
set -a
source .env
set +a

mvn clean verify
java -jar account-service/target/account-service-0.1.0-SNAPSHOT-exec.jar
```

另开终端，在项目根目录导入同一份 `.env`，再启动交易服务：

```bash
set -a
source .env
set +a
java -jar trading-service/target/trading-service-0.1.0-SNAPSHOT-exec.jar
```

所有服务使用 `web-application-type: none`，不启动 HTTP 服务、不提供 REST 接口。gRPC 服务启动后将实际监听端口注册到 Nacos，元数据为 `protocol=grpc`；关闭应用时由 Nacos 自动注册组件注销实例。

| 中间件 | 本项目地址 |
| --- | --- |
| PostgreSQL | `localhost:15432/ledger` |
| Redis | `localhost:16379` |
| Kafka | `localhost:19092` |
| Nacos API / 客户端 gRPC | `localhost:18848` / `localhost:19848` |
| Nacos 控制台 | `http://localhost:18888` |

开发 Compose 绑定本机回环地址，Kafka advertised address 面向在宿主机运行的 Java 服务。PostgreSQL 使用独立命名卷；`docker compose down` 停止本项目中间件并保留数据库数据。开发 Nacos 使用内置 Derby 和 tmpfs，停止容器后其数据不会保留。

## gRPC 验证

安装 `grpcurl` 后：

```bash
grpcurl -plaintext localhost:19091 list
grpcurl -plaintext -d '{"requestId":"smoke-001"}' \
  localhost:19091 ledger.platform.v1.AccountProbe/Check
grpcurl -plaintext -d '{"requestId":"smoke-001"}' \
  localhost:19082 ledger.platform.v1.TradingProbe/Check
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

`grpcurl` 是可选工具，未安装时可以通过下方测试命令验证调用链。诊断接口不执行开户、资金变更或证券委托。

## 中间件与配置

- PostgreSQL：MyBatis-Plus + Spring JDBC + Flyway；服务启动时创建自己的 schema 和迁移历史。V1 只初始化迁移基线，后续业务表通过新增版本迁移维护。MyBatis-Plus 提供 BaseMapper、条件构造器、PostgreSQL 分页（单页上限 500）及 `@Version` 乐观锁，使用方法见[数据库访问约定](docs/persistence.md)。
- Kafka：预置 `KafkaTemplate` 和消费者工厂，生产者开启幂等及 `acks=all`，消费者关闭自动提交。当前不创建业务 topic，不启动业务监听器；生产者幂等不等于业务端到端幂等。
- Redis：预置 `StringRedisTemplate`；尚未缓存业务数据。
- Nacos：同时接入服务发现与配置中心。默认读取 `LEDGER_GROUP` 下的 `account-service.yml` / `trading-service.yml`，允许配置暂时不存在；namespace 使用命名空间 ID，默认空值表示 public。

全部连接参数见 [.env.example](.env.example)。可复用已有中间件：修改项目 `.env` 中的地址及凭据，无需运行本项目 Compose。现有 `/root/docker` 的 Nacos 配置含 TLS 与认证，需匹配客户端 TLS/信任配置；Kafka 使用 `SASL_PLAINTEXT`，需配置 `KAFKA_SECURITY_PROTOCOL`、`KAFKA_SASL_MECHANISM` 和 `KAFKA_SASL_JAAS_CONFIG`，并保证 broker advertised address 对服务可达。现有密码和证书未复制到仓库。

## 验证命令

```bash
# 无需运行中间件：启动真实 TCP gRPC 服务，验证调用链和无下游时的错误码。
mvn verify

# 先启动项目 Compose 并导入 .env，再验证真实 PostgreSQL、Redis、Kafka、Nacos。
LEDGER_INFRA_TEST=true mvn verify
```

中间件测试会在开发库执行 Flyway 初始化，写入带 TTL 的临时 Redis key、创建并删除随机 Kafka topic，并验证 Nacos 注册端口和服务间调用。MyBatis-Plus 测试使用 PostgreSQL 临时表验证 CRUD、驼峰映射、分页上限、乐观锁及事务连接共享，完成后回滚。请使用本项目开发环境运行。
