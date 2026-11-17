# 配置与运行

[返回项目首页](../README.md)

## 配置文件的作用

| 文件或来源 | 作用 |
| --- | --- |
| [`.env.example`](../.env.example) | Java 服务的中间件连接模板 |
| 本机 `.env` | 个人连接配置，Git 忽略；Java 不会自动读取，需要导出环境变量 |
| [gateway/config/config.yaml](../gateway/config/config.yaml) | Go 网关启动配置，包括监听、路由来源、Nacos、TLS 和访问 token |
| [gateway/config/routes.yml](../gateway/config/routes.yml) | HTTP → gRPC 路由规则，支持热更新 |
| [ledger-defaults.yml](../ledger-common/src/main/resources/ledger-defaults.yml) | 公共配置及环境变量占位符 |
| 服务的 `application.yml` | 服务名、监听端口及 Nacos 导入 |
| Nacos 的服务配置 | 按服务名导入的集中配置，可覆盖本地配置项 |
| Java 启动命令的 `--属性=值` | 对当前进程进行临时覆盖 |

Java 服务与 Go 网关使用独立的配置来源：Java 使用 `.env` 导出的环境变量及 Spring/Nacos 配置；Go 网关只读取指定的启动 YAML。修改 `.env` 不会影响网关，修改 `gateway/config/config.yaml` 也不会影响 Java 服务。

`.env.example` 使用示例端口；公共 YAML 的默认值使用中间件标准端口。请按实际部署编辑 `.env`，再为 Java 服务导入环境变量。仓库不附带中间件 Compose 文件。环境变量通过 YAML 占位符提供值；如果 Nacos 同时提供同名完整属性，最终以 Spring 属性来源优先级为准，不能假设占位符一定覆盖远端配置。排查时可先移除重复配置，或用启动参数明确指定。

### Bash

```bash
test -f .env || cp .env.example .env
set -a
source .env
set +a
```

### PowerShell

在项目根目录执行。以下代码读取模板使用的 `KEY=VALUE` 格式，忽略空行和注释，并移除值两端的单引号或双引号：

```powershell
if (!(Test-Path -LiteralPath .env)) { Copy-Item -LiteralPath .env.example -Destination .env }
foreach ($line in Get-Content -LiteralPath .env -Encoding utf8) {
    if ($line -match '^\s*([A-Za-z_][A-Za-z0-9_]*)=(.*)$') {
        $value = $Matches[2].Trim().Trim([char[]]@(34, 39))
        [Environment]::SetEnvironmentVariable($Matches[1], $value, 'Process')
    }
}
```

它只设置当前终端及其子进程的环境变量，不执行 Bash 的变量展开或命令。每个用于启动服务的新终端都需要重新导入；在 IDE 中运行时，将变量填入运行配置。

## 环境变量

| 变量 | 模板示例 | 含义 |
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
| `GRPC_PORT` | 账户 8081 / 交易 8082 | 覆盖当前 Java 进程的 gRPC 端口 |

`SERVICE_IP=127.0.0.1` 适用于两个服务都在宿主机启动。跨机器或容器部署时，要改成调用方可达的地址。分别启动多个服务时，按进程设置 `GRPC_PORT`，避免两个进程共用同一个导出的端口。

```bash
GRPC_PORT=19191 java -jar account-service/target/account-service-0.1.0-SNAPSHOT-exec.jar
```

PowerShell 中，在启动该服务的终端执行 `$env:GRPC_PORT = '19191'`；恢复默认值使用 `Remove-Item Env:GRPC_PORT`。

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

发布配置后，重启相应服务验证。当前没有为业务 Bean 设计动态刷新机制，不保证所有配置都能即时生效。配置数据的持久化由 Nacos 部署管理。

## 复用已有中间件

为 Java 服务填写 `.env` 或应用启动参数，为 Go 网关填写 `gateway/config/config.yaml`。按目标环境核对以下内容：

- PostgreSQL：填入已有实例的库、用户和密码。启动服务前按[数据库 SQL](../db/README.md)手动执行初始化或结构变更脚本；执行脚本的用户需要相应建 schema/表权限，应用用户需要对应的数据访问权限。
- Nacos：如果目标环境启用了认证或 TLS，需匹配服务器地址、客户端认证和证书信任设置。仅填写用户名密码不等于已完成 TLS 接入；具体证书参数由部署环境提供，Go 网关配置见[网关指南](../gateway/README.md#配置项)。
- Kafka：按目标监听器填写安全协议、SASL 机制及登录信息，且 advertised address 必须从运行 Java 的机器可达。
- Redis：按已有实例填写地址、ACL 用户和密码。

SASL 配置值包含空格和分号，供 Bash `source` 使用时用单引号包住整个值。以下使用示例凭据：

```bash
KAFKA_SECURITY_PROTOCOL=SASL_PLAINTEXT
KAFKA_SASL_MECHANISM=PLAIN
KAFKA_SASL_JAAS_CONFIG='org.apache.kafka.common.security.plain.PlainLoginModule required username="example-user" password="example-password";'
```

## 中间件生命周期

中间件由各自部署环境启动、停止和维护。启动 Java 服务前，确认数据库及 Nacos 可访问，并按[数据库 SQL](../db/README.md)完成 schema 初始化。Kafka 和 Redis 的业务使用需对应服务可用。

修改 `.env` 中的 PostgreSQL 密码只改变客户端配置，不会修改数据库内的用户密码。数据库数据、Nacos 配置和消息的持久化策略由实际部署决定。Kafka advertised address 必须从运行 Java 服务的机器可达；容器部署时需核对内外部监听地址。

## Go 动态网关

Go 网关独立读取 [gateway/config/config.yaml](../gateway/config/config.yaml)，无需导入 `.env`，也不读取环境变量。按 Java 服务使用的 Nacos 地址、namespace、group 和凭据填写其中的 `nacos` 配置。从 `gateway/` 运行 `go run ./cmd/gateway`，或用 `-config` 指定配置文件；相对路径以配置文件所在目录为基准。网关独立监听 HTTP 8080，不改变 Java gRPC 端口。路由、TLS、静态地址及热更新说明见[网关指南](../gateway/README.md)。

例如 Java 的 `.env` 使用 `NACOS_SERVER_ADDR=localhost:18848` 和 `NACOS_GROUP=LEDGER_GROUP`，网关需单独填写：

```yaml
nacos:
  server_addr: localhost:18848
  namespace: ""
  group: LEDGER_GROUP
  username: ""
  password: ""
```

从旧环境变量配置迁移时，将 `GATEWAY_*` 值转填到网关 YAML；Nacos 地址和凭据也填入 `nacos` 节点。个人 `.env` 中原有的 `GATEWAY_*` 可以删除，Java 使用的 `NACOS_*` 仍需保留。网关不会展开 YAML 字符串中的 `${变量名}`。
