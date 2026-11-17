# 配置与运行

[返回项目首页](../README.md)

## 配置文件的作用

| 文件或来源 | 作用 |
| --- | --- |
| [`.env.example`](../.env.example) | 本项目 Compose 的客户端连接模板 |
| 本机 `.env` | 个人连接配置，Git 忽略；Java 不会自动读取，需要导出环境变量 |
| [ledger-defaults.yml](../ledger-platform/src/main/resources/ledger-defaults.yml) | 公共配置及环境变量占位符 |
| 服务的 `application.yml` | 服务名、监听端口、Flyway schema/迁移目录及 Nacos 导入 |
| Nacos 的服务配置 | 按服务名导入的集中配置，可覆盖本地配置项 |
| Java 启动命令的 `--属性=值` | 对当前进程进行临时覆盖 |

`.env.example` 的端口对应项目内 Compose；公共 YAML 的默认端口是中间件标准端口。启动项目 Compose 后应先导入 `.env`，不能直接依赖 YAML 默认地址。环境变量通过 YAML 占位符提供值；如果 Nacos 同时提供同名完整属性，最终以 Spring 属性来源优先级为准，不能假设占位符一定覆盖远端配置。排查时可先移除重复配置，或用启动参数明确指定。

```bash
test -f .env || cp .env.example .env
set -a
source .env
set +a
```

## 环境变量

| 变量 | 项目 Compose 示例 | 含义 |
| --- | --- | --- |
| `POSTGRES_URL` | `jdbc:postgresql://localhost:15432/ledger` | JDBC 地址 |
| `POSTGRES_USER` / `POSTGRES_PASSWORD` | 见 `.env.example` | 数据库登录信息 |
| `REDIS_HOST` / `REDIS_PORT` | `localhost` / `16379` | Redis 连接地址 |
| `REDIS_USERNAME` / `REDIS_PASSWORD` | 空 | Redis ACL 用户及密码 |
| `KAFKA_BOOTSTRAP_SERVERS` | `localhost:19092` | Kafka 初始 broker 地址，多个地址逗号分隔 |
| `KAFKA_SECURITY_PROTOCOL` | `PLAINTEXT` | 客户端连接协议 |
| `KAFKA_SASL_MECHANISM` | `PLAIN` | SASL 机制，仅启用 SASL 协议时使用 |
| `KAFKA_SASL_JAAS_CONFIG` | 空 | SASL 登录配置 |
| `NACOS_SERVER_ADDR` | `localhost:18848` | Nacos 服务端地址 |
| `NACOS_USERNAME` / `NACOS_PASSWORD` | 空 | Nacos 认证信息 |
| `NACOS_NAMESPACE` | 空 | namespace ID，空表示 public |
| `NACOS_GROUP` | `LEDGER_GROUP` | 配置和服务发现使用的组 |
| `SERVICE_IP` | `127.0.0.1` | 注册给其他服务访问的地址 |
| `GRPC_PORT` | 按服务默认值 | 覆盖当前 Java 进程的 gRPC 端口 |

`SERVICE_IP=127.0.0.1` 适用于两个服务都在宿主机启动。跨机器或容器部署时，要改成调用方可达的地址。分别启动多个服务时，按进程设置 `GRPC_PORT`，避免两个进程共用同一个导出的端口。

```bash
GRPC_PORT=19191 java -jar account-service/target/account-service-0.1.0-SNAPSHOT-exec.jar
```

## Nacos 配置中心

| 服务 | Data ID | 默认 Group | 默认 namespace |
| --- | --- | --- | --- |
| 账户 | `account-service.yml` | `LEDGER_GROUP` | public |
| 交易 | `trading-service.yml` | `LEDGER_GROUP` | public |

配置格式选 YAML。可以先不创建配置，直接使用本地值。`optional:nacos:` 允许配置资源不存在，不表示 Nacos 不可用时整个应用仍保证启动；服务注册仍依赖 Nacos。

例如在某个服务的 Data ID 中调整连接池大小：

```yaml
spring:
  datasource:
    hikari:
      maximum-pool-size: 15
```

发布配置后，重启相应服务验证。当前没有为业务 Bean 设计动态刷新机制，不保证所有配置都能即时生效。开发 Nacos 使用 tmpfs，容器停止后配置数据不保留。

## 复用 `/root/docker` 中的中间件

只修改本项目 `.env` 或应用启动参数，不修改 `/root/docker` 文件。该环境的中间件版本与项目 Compose 一致，但地址、认证和存储配置不同：

- PostgreSQL：填入已有实例的库、用户和密码。当前 Flyway 会创建服务 schema，因此数据库用户需要相应建 schema/表权限。
- Nacos：现有配置启用了认证和 TLS，需同时匹配服务器地址、客户端 TLS 和证书信任设置。仅填写用户名密码不等于已完成 TLS 接入；具体证书参数由部署环境提供。
- Kafka：现有监听器使用 `SASL_PLAINTEXT`。在 `.env` 设置相应协议、机制及登录信息，且 advertised address 必须从运行 Java 的机器可达。
- Redis：按已有实例填写地址、ACL 用户和密码。

SASL 配置值包含空格和分号，供 Bash `source` 使用时用单引号包住整个值。以下使用示例凭据：

```bash
KAFKA_SECURITY_PROTOCOL=SASL_PLAINTEXT
KAFKA_SASL_MECHANISM=PLAIN
KAFKA_SASL_JAAS_CONFIG='org.apache.kafka.common.security.plain.PlainLoginModule required username="example-user" password="example-password";'
```

## 中间件生命周期

```bash
docker compose up -d --wait --wait-timeout 180
docker compose ps
docker compose logs --tail 100 nacos
docker compose logs --tail 100 kafka
docker compose down
```

这些命令只操作项目名为 `ledger-core` 的 Compose 资源。PostgreSQL 使用命名卷，普通 `down` 保留数据；Redis 和 Kafka 没有配置业务数据持久卷。修改 `.env` 中的 PostgreSQL 密码不会自动修改已初始化数据库卷内的用户密码，应与已有数据库凭据保持一致。

当前 Kafka advertised address 固定为 `localhost:19092`，用于宿主机上的 Java 服务；将 Java 服务容器化时还需单独设计内外部监听地址。
