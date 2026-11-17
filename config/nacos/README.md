# Nacos 配置

[返回项目首页](../../README.md)

本目录保存 test/prod 的发布模板，不作为服务的本地回退配置，不会打包进应用 JAR。test/prod 先读取本地 Nacos 连接配置，再加载公共配置和各自的服务配置。dev 完全使用各服务的 `bootstrap-dev.yml`，Nacos 仅作注册中心，不读取这些配置。

## namespace 与 Group

| Profile | namespace ID | Nacos 用途 | Group |
| --- | --- | --- | --- |
| dev | `ledger-dev` | 仅服务注册与发现，无需发布配置 | `LEDGER_GROUP` |
| test | `ledger-test` | 注册与配置中心 | `LEDGER_GROUP` |
| prod | `ledger-prod` | 注册与配置中心 | `LEDGER_GROUP` |

在目标 Nacos 中创建上述 namespace，ID 与表中一致；如果只能使用自动生成的 ID，启动时通过 `NACOS_NAMESPACE` 填入实际 ID。Go 网关的 `nacos.namespace` 同样填写该 ID。

## 发布内容

在 test/prod 的 namespace 下创建以下配置，格式均选 YAML；dev 无需执行这些发布步骤：

| Data ID | 模板 | 使用方 |
| --- | --- | --- |
| `ledger-infrastructure.yml` | [公共配置](ledger-infrastructure.yml) | 两个 Java 服务 |
| `account-service.yml` | [账户配置](account-service.yml) | 账户服务 |
| `trading-service.yml` | [交易配置](trading-service.yml) | 交易服务 |

公共模板包含数据库、Redis、Kafka、连接池、MyBatis、日志、虚拟线程、关闭超时及 gRPC 反射/健康配置。服务模板包含各自的 gRPC 端口，也可覆盖公共配置，例如为账户服务配置独立的数据库用户或连接池大小。后加载的服务配置优先于公共配置。

1. 选择目标环境对应的 namespace，使用 `LEDGER_GROUP`。
2. 将公共模板中的地址、凭据和运行参数改为目标环境的实际值，再发布。仓库中的值只是连接示例。
3. 分别发布账户和交易配置，默认端口为 8081、8082。
4. 将 `.env.example` 复制为 `.env`，填写 Nacos 连接信息并导入终端；按需选择 profile。
5. 手动执行数据库初始化 SQL，再启动服务并验证 gRPC 调用链。

test/prod 的每个 namespace 均需发布完整的三份配置，切换 profile 不会复制远端配置。模板文件的修改也不会自动同步到 Nacos，dev 的本地配置与本目录模板分别维护。

`spring.config.import` 使用非 optional 的 `nacos:` 导入，加载异常不会被忽略。当前 Nacos SDK 可能将不存在或为空的 Data ID 视为空配置，因此启动前仍需核对三份配置均已发布且非空。服务不再提供本地数据库、Redis、Kafka 等配置回退。

配置中的 `${spring.application.name}` 在应用端解析，用于 Kafka consumer group。服务端口保留 `${GRPC_PORT:8081}` / `${GRPC_PORT:8082}`，支持每个实例临时覆盖端口；其他中间件参数直接在 Nacos 中填写实际值，无需导出原有的 `POSTGRES_*`、`REDIS_*`、`KAFKA_*` 变量。

发布运行配置后重启对应服务验证。当前未实现客户端的完整动态重建，不保证数据库连接池、Kafka 客户端、端口等配置可以即时刷新。本地的服务名、profile、`web-application-type` 和 Nacos 连接信息必须保留，不应依赖远端提供这些启动参数。
