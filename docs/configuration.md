# 配置与运行

[返回项目首页](../README.md)

## 选择配置入口

| 场景 | 修改位置 | 生效方式 |
| --- | --- | --- |
| 本地启动 Java 服务 | 各服务 `application-dev.yml`，或进程环境变量 | 重启对应服务 |
| 切换 Java 环境 | `SPRING_PROFILES_ACTIVE` 或 `--spring.profiles.active` | 启动时选择 profile |
| 调整 test/prod 中间件参数 | 对应 namespace 的 `ledger-infrastructure.yml` 与服务专属配置 | 发布后重启对应服务验证 |
| 调整网关监听、自身注册、Nacos、TLS、token | `gateway/config/config.yaml` 或 `-config` 指定的文件 | 重启网关 |
| 调整 HTTP 到 gRPC 的服务映射 | Nacos `gateway-routes.yml` | 有效配置整体热更新，无效更新保留旧配置 |

首次运行按[项目首页](../README.md#本地启动)准备依赖；数据库和角色的创建步骤见[数据库迁移](../db/README.md#首次初始化)。Java 与 Go 网关分别读取配置，切换环境时需将两者的 Nacos 地址、namespace ID、group 和认证对齐。

## 配置文件的作用

| 文件或来源 | 作用 |
| --- | --- |
| [账户 application.yml](../account-service/src/main/resources/application.yml) / [交易 application.yml](../trading-service/src/main/resources/application.yml) | 服务名、默认 dev、非 HTTP 启动方式、Flyway 所属 schema 与迁移策略、Nacos 公共配置，以及交易服务的下游客户端启用项 |
| [账户 application-dev.yml](../account-service/src/main/resources/application-dev.yml) / [交易 application-dev.yml](../trading-service/src/main/resources/application-dev.yml) | 完整的本地开发配置，也是准备 test/prod 运行参数的参考；Nacos 仅用于注册与发现，配置中心关闭 |
| 各服务的 `application-test.yml` / `application-prod.yml` | 环境对应的 Nacos 地址、namespace 及远端配置导入 |
| Nacos 的 `ledger-infrastructure.yml` | 数据库、Redis、Kafka、连接池、MyBatis、日志等公共运行配置 |
| Nacos 的 `account-service.yml` / `trading-service.yml` | 各服务的 gRPC 端口、共享客户端参数及公共配置覆盖项 |
| [gateway/config/config.yaml](../gateway/config/config.yaml) | Go 网关启动配置，独立于 Java 与 Nacos 运行配置 |
| [gateway/config/routes.example.yml](../gateway/config/routes.example.yml) | 网关路由示例，需发布到 Nacos 的 `gateway-routes.yml`，运行时不读取此文件 |

Java dev 环境从本地 `application-dev.yml` 读取完整运行配置，Nacos 仅用于注册与发现，不读取配置中心。test/prod 在本地取得 Nacos 连接信息，再读取远端运行配置。Go 网关独立读取启动 YAML，不读取 `.env`；所有环境均使用 Nacos 路由配置和服务发现，启动前需发布网关路由。

## 共享 gRPC 客户端

`ledger-grpc` 默认不启用客户端。交易服务在本地 `application.yml` 开启 `ledger.grpc.client.enabled` 和 `ledger.grpc.client.account.enabled`；账户服务不启用下游客户端。启用不要求下游在线，实际发现与连接在首次调用时发生。

客户端的 `account.service-id` / `trading.service-id` 默认分别为 `account-service` / `trading-service`，`timeout` 默认 `2s`，均位于 `ledger.grpc.client` 前缀下。开发环境可在本地 profile 配置中覆盖；test/prod 可在服务专属 Nacos 配置中增加这些字段。`plaintext` 默认 true；false 启用标准 TLS，需同时配置服务端。变更后重启使用方服务验证，不保证客户端属性即时刷新。

完整字段、按需装配与连接清理规则见[共享客户端指南](../ledger-grpc/README.md)。服务名必须与目标服务的 Nacos 注册名对应，namespace/group 继续使用调用方已有的 Nacos 配置。

客户端启用项位于本地 `application.yml`，因此交易服务在 dev/test/prod 中默认都会启用账户客户端。环境配置可覆盖该开关；关闭前需同时调整依赖该客户端的业务组件，当前 `TradingHelloEndpoint` 必须注入账户客户端。

## 环境切换

每个 Java 服务的资源目录包含：

```text
application.yml       # 必要启动配置、默认 profile 和 Nacos 公共参数
application-dev.yml   # 完整本地开发配置，仅使用 Nacos 注册与发现
application-test.yml  # Nacos 测试环境连接与配置导入
application-prod.yml  # Nacos 生产环境连接与配置导入
```

Spring Boot 根据启用的 profile 自动加载对应的 `application-{profile}.yml`，并与公共的 `application.yml` 合并。公共配置只声明默认环境，不再手动拼接文件名：

```yaml
spring:
  profiles:
    default: dev
```

未显式启用 profile 时使用 dev。通过环境变量 `SPRING_PROFILES_ACTIVE`、IDE 的 Active profiles 或 `--spring.profiles.active=test` 选择环境；启用 test/prod 后不会加载 dev 配置。Spring Boot 原生支持逗号分隔的多个 profile，例如 `dev,local`，并按声明顺序加载，后者覆盖前者；`application-local.yml` 需自行提供并妥善管理其中的本地凭据。每次选择 dev、test、prod 中的一个环境，再按需叠加附加 profile，避免混用不同环境的 Nacos 连接配置。显式启用其他 profile 时，dev 默认配置不会自动叠加，需要开发配置时应明确包含 dev。

项目使用 Spring Boot Config Data 机制；test/prod 仍通过各自文件内的 `spring.config.import` 导入 Nacos 配置。无需添加 `spring-cloud-starter-bootstrap`，也不依赖旧的 Bootstrap 加载机制。

| Profile | 默认 namespace ID | Nacos 地址 |
| --- | --- | --- |
| dev | `ledger-dev` | `NACOS_SERVER_ADDR`，默认 `localhost:8848` |
| test | `ledger-test` | `NACOS_SERVER_ADDR`，默认 `localhost:8848` |
| prod | `ledger-prod` | `NACOS_SERVER_ADDR`，默认 `localhost:8848` |

Group 默认 `LEDGER_GROUP`。namespace 必须提前创建；`NACOS_NAMESPACE` 可覆盖默认 ID。切换 profile 会选择对应 namespace，但不会自动创建 namespace、复制配置或替换已导出的 Nacos 地址。若显式设置了 `NACOS_NAMESPACE`，需在切换环境时一起调整。

dev 的 `spring.cloud.nacos.config.enabled` 和 `import-check.enabled` 均为 false，`spring.cloud.nacos.discovery.enabled` 为 true。开发时直接修改各服务 `application-dev.yml` 中的数据库、Redis、Kafka 等参数，无需在 `ledger-dev` 发布 Java 运行配置；注册中心仍需可访问。启用 Go 网关时仍需发布 `gateway-routes.yml`。test/prod 的公共与服务专属配置需先发布到对应 namespace。

```bash
# 默认开发环境，使用本地配置，只需准备中间件和 Nacos 注册中心
java -jar account-service/target/account-service-0.1.0-SNAPSHOT-exec.jar
# 测试环境
java -jar account-service/target/account-service-0.1.0-SNAPSHOT-exec.jar --spring.profiles.active=test
# 生产环境
java -jar trading-service/target/trading-service-0.1.0-SNAPSHOT-exec.jar --spring.profiles.active=prod
```

上述单行命令同样适用于 PowerShell。也可通过 `SPRING_PROFILES_ACTIVE` 或 IDE 的 Active profiles 选择环境；命令行参数优先于环境变量。项目无需创建或导入 `.env` 文件。

## 临时覆盖配置

所有环境都已有 YAML 默认值，不需要 `.env` 文件。默认 Nacos 地址为 `localhost:8848`，Group 为 `LEDGER_GROUP`，namespace 随 profile 选择。与实际部署不同时，可以直接修改对应 YAML，或在启动命令中覆盖：

```bash
java -jar account-service/target/account-service-0.1.0-SNAPSHOT-exec.jar --spring.profiles.active=test --spring.cloud.nacos.server-addr=nacos.example:8848
```

也可以使用进程环境变量；例如 PowerShell：

```powershell
$env:SPRING_PROFILES_ACTIVE = 'test'
$env:NACOS_SERVER_ADDR = 'nacos.example:8848'
java -jar account-service/target/account-service-0.1.0-SNAPSHOT-exec.jar
```

部署时通过容器或 Pod 的环境变量注入实际地址和凭据。IDE 中可设置 Active profiles 和进程环境变量。这些覆盖均不依赖 dotenv 文件。

## 环境变量

| 可选环境变量 | 默认值 | 含义 |
| --- | --- | --- |
| `SPRING_PROFILES_ACTIVE` | 未显式启用 profile 时默认 `dev` | 选择 dev / test / prod，可按需叠加附加 profile |
| `NACOS_SERVER_ADDR` | `localhost:8848` | Nacos API 地址 |
| `NACOS_USERNAME` / `NACOS_PASSWORD` | 空 | Nacos 认证信息 |
| `NACOS_NAMESPACE` | 默认随 profile 选择 | 实际 namespace ID；留空字符串表示 public，通常无需显式设置 |
| `NACOS_GROUP` | `LEDGER_GROUP` | 配置与服务发现的组 |
| `GRPC_PORT` | 账户 8081 / 交易 8082 | dev 本地配置及按下方示例发布的 test/prod 配置支持覆盖当前实例端口 |

Nacos 客户端自动选择本机 IP 作为实例注册地址，无需设置 `SERVICE_IP`。Pod 中通常会选择容器网络地址；多网卡环境若自动选择的地址不可达，可通过 Spring 的 `spring.cloud.nacos.discovery.network-interface` 指定网卡。调用方需能访问所注册的地址。

dev 直接修改本地 `application-dev.yml`，test/prod 在目标 Nacos 中修改中间件参数。历史 `.env` 无需保留，但迁移前应确认其中的自定义地址或凭据已转移到对应配置或部署环境。不要将真实凭据写回仓库模板。

## Nacos 配置中心

dev 只使用注册中心，无需发布 Java 运行配置。test/prod 先创建 `ledger-test` / `ledger-prod` namespace；若使用自动生成的 namespace ID，通过 `NACOS_NAMESPACE` 填入实际 ID，Go 网关也使用同一 ID。默认 Group 为 `LEDGER_GROUP`，可由 `NACOS_GROUP` 覆盖。

运行参数以各服务的 [application-dev.yml（账户）](../account-service/src/main/resources/application-dev.yml) 和 [application-dev.yml（交易）](../trading-service/src/main/resources/application-dev.yml) 为参考，仓库不再维护另一套独立的 Nacos 模板。发布前按目标环境修改地址、凭据、连接池和其他运行参数。

在 test/prod 各自的 namespace 下发布以下配置，格式均选 YAML；Data ID 与服务的 `application-test.yml` / `application-prod.yml` 中的导入保持一致：

| Data ID | 参考各服务配置中的字段 | 用途 |
| --- | --- | --- |
| `ledger-infrastructure.yml` | `spring.threads`、`spring.lifecycle`、`spring.datasource`、`spring.data.redis`、`spring.kafka`、`spring.grpc.server.reflection`、`spring.grpc.server.health`、`mybatis-plus`、`logging` | 两个服务共享的中间件与运行参数 |
| `account-service.yml` | 账户的 `spring.grpc.server.port`，以及账户专属覆盖项 | 账户端口与专属配置 |
| `trading-service.yml` | 交易的 `spring.grpc.server.port`，以及交易专属覆盖项 | 交易端口与专属配置 |

按字段筛选后保留原 YAML 层级，将公共字段合并到同一份 YAML 中；`spring.grpc.server.port` 分别放在服务配置中。不要整份复制 `application-dev.yml`：其中的 `spring.cloud.nacos` 属于本地环境连接设置，包含关闭配置中心的 dev 开关，不应发布到远端。服务名、profile、`web-application-type`、Nacos 地址、namespace、认证及 `spring.config.import` 也继续保留在服务本地的 `application*.yml` 中。

服务配置的最小内容如下，分别发布到对应的 Data ID：

```yaml
# Data ID: account-service.yml
spring:
  grpc:
    server:
      port: ${GRPC_PORT:8081}
```

```yaml
# Data ID: trading-service.yml
spring:
  grpc:
    server:
      port: ${GRPC_PORT:8082}
```

两个服务分别先导入公共配置，再导入自身配置；服务配置可覆盖公共项，例如账户使用独立的数据库用户或不同的连接池大小：

```yaml
spring:
  datasource:
    hikari:
      maximum-pool-size: 15
```

每个 test/prod namespace 均需准备公共、账户和交易三份配置，切换 profile 不会复制远端配置。test/prod 使用非 optional 的 `nacos:` 导入，加载异常会终止启动，不回退读取 dev 配置。当前 SDK 可能将缺失或为空的 Data ID 视为空配置，启动前应核对全部配置已发布且非空。

配置中的 `${spring.application.name}` 在应用端解析，用于 Kafka consumer group；服务端口的 `${GRPC_PORT:8081}` / `${GRPC_PORT:8082}` 支持实例级覆盖。数据库、Redis、Kafka 等其他参数在 Nacos 中填写目标环境的实际值。

Flyway 的 `spring.flyway` 默认项保留在各服务本地 `application.yml`，适用于 dev/test/prod，不要将两个服务的 `default-schema` / `schemas` 合并进公共 Nacos 配置。默认复用当前环境数据源，启动时自动迁移。使用独立迁移角色时，将 `spring.flyway.url`、`user`、`password` 放入对应服务的专属配置。已有数据库接入及发布任务模式见[数据库迁移](../db/README.md)。

准备好数据库、角色和迁移权限后启动服务，确认 Flyway 迁移成功，再按[项目首页的 gRPC 验证](../README.md#grpc-验证)检查调用链。发布运行配置后重启对应服务验证，目前不保证数据库连接池、Kafka 客户端或 gRPC 端口支持即时刷新。修改本地参考配置不会自动发布到 Nacos，配置中心的修改也不会自动写回仓库。

## 复用已有中间件

Java 的 Nacos 默认连接参数位于本地 YAML；地址或认证不同于默认值时，通过部署环境变量、IDE 或命令行覆盖。dev 在本地 `application-dev.yml` 中配置中间件，test/prod 在目标 namespace 的远端 YAML 中配置：

- PostgreSQL：修改 `spring.datasource` 的库、用户和密码，按[数据库迁移](../db/README.md)核对已有结构及权限；启动时由 Flyway 执行所属服务的迁移。
- Redis：修改 `spring.data.redis` 的地址、ACL 用户和密码。
- Kafka：修改 `spring.kafka` 的地址、安全协议及 SASL 参数。broker 的 advertised address 也必须从 Java 服务所在机器可达。
- Nacos：地址、namespace、group 和认证均从本地启动配置取得；TLS 需按部署环境配置。Go 网关使用独立的 YAML 连接配置。

Kafka 的 SASL 配置填写在当前环境的本地或远端 YAML，例如：

```yaml
spring:
  kafka:
    properties:
      security.protocol: SASL_PLAINTEXT
      sasl.mechanism: PLAIN
      sasl.jaas.config: 'org.apache.kafka.common.security.plain.PlainLoginModule required username="example-user" password="example-password";'
```

## 中间件生命周期

中间件由部署环境单独管理。启动服务前确认 Nacos 注册中心和数据库可访问，数据库角色具备 Flyway 迁移权限；schema 及后续业务表由迁移管理。test/prod 还需确认远端配置已发布。使用 Kafka/Redis 业务能力时需对应服务可用。修改 YAML 中的数据库密码只改变客户端配置，不会修改数据库内的密码。数据库数据、Nacos 配置和消息的持久化策略由实际部署决定。

## Go 动态网关

网关独立读取 [gateway/config/config.yaml](../gateway/config/config.yaml)，无需导入 `.env`。开发环境默认 namespace 为 `ledger-dev`；连接 test/prod 时将 `nacos.namespace` 改为 `ledger-test` / `ledger-prod` 或实际 ID，地址、group 和凭据也需与 Java 服务对齐。

启动 YAML 的字段值支持 `${VAR:默认值}` 和 `${VAR}`；默认配置已声明 `GATEWAY_*`、`NACOS_*` 占位符，也可通过 `NACOS_NAMESPACE` 切换 namespace。自定义 YAML 仅在字段显式声明占位符时读取对应环境变量，网关不会自动加载 `.env` 文件。变量未设置且无默认值时启动报错，已设置但为空的变量保留空值。修改启动配置或环境变量后需重启网关，完整规则见[环境变量占位符](../gateway/README.md#环境变量占位符)。

从 `gateway/` 运行 `go run ./cmd/gateway`，或用 `-config` 指定启动配置。相对路径以启动配置所在目录为准。网关路由、TLS 及热更新说明见[网关指南](../gateway/README.md)。
