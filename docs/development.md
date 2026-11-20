# 开发与排错

[返回项目首页](../README.md)

首次接触项目可先阅读[当前架构](architecture.md)，按[项目首页](../README.md#本地启动)启动服务。业务建设顺序与阶段目标见[架构演进路线](roadmap.md)。

## 构建与启动

从 Maven 根 POM 导入 IDE 项目，Project SDK 与 Maven Runner 都选择 JDK 25。生成的 Protobuf Java 文件位于 `ledger-grpc/target`，由构建维护，不提交到 Git。

```bash
java -version
mvn --version
mvn clean verify
```

仅构建账户服务及其依赖时：

```bash
mvn -pl account-service -am package
```

完整启动流程见首页。dev 只需创建 Nacos 注册 namespace 并调整本地 `application-dev.yml`，无需发布配置；test/prod 先按 [Nacos 发布说明](configuration.md#nacos-配置中心)发布运行配置。IDE 可直接运行 `AccountApplication` 或 `TradingApplication`，无需 `.env`。默认 Nacos 地址为 `localhost:8848`、Group 为 `LEDGER_GROUP`，namespace 随 profile 选择；部署时按需覆盖。默认环境为 dev，可用 Active profiles、`SPRING_PROFILES_ACTIVE` 或 `--spring.profiles.active=test` / `prod` 切换。公共配置通过 `spring.profiles.default: dev` 设置默认环境，Spring Boot 自动加载对应的 `application-{profile}.yml`，无需手动导入本地环境文件或添加 bootstrap starter。停服务使用 `Ctrl+C`，触发应用关闭及 Nacos 注销。

## Lombok 与注解处理

三个 Java 模块均将 Lombok 声明为 `provided`、`optional` 依赖，版本由 Spring Boot 管理。根 POM 统一配置编译器的 `annotationProcessorPaths`，适用于当前 JDK 25；服务可执行 JAR 排除 Lombok。配置方式参考 [Lombok Maven 指南](https://projectlombok.org/setup/maven)。

`GrpcClientProperties` 使用 `@Getter` / `@Setter` 生成访问方法，保留嵌套配置对象、默认值和校验逻辑；纯字段赋值的构造器使用 `@RequiredArgsConstructor`。包含参数校验或父类初始化的构造器继续显式编写。

修改依赖后重新导入 Maven 项目。若命令行编译正常而 IDE 提示 getter 或构造器不存在，检查 IDE 的 Lombok 支持和注解处理设置。

## 修改 gRPC 协议

1. 在 `ledger-grpc/src/main/proto/ledger/<业务>/v1/` 中维护 `.proto`。
2. 定义 `package`、`java_package` 和 `java_multiple_files`，按服务职责设计 RPC。
3. 执行 `mvn -pl ledger-grpc -am compile` 生成消息类与 stub，不手改 target 中的生成文件。
4. 在对应服务的 `interfaces.grpc` 中用 `@GrpcService`（`org.springframework.grpc.server.service.GrpcService`）注册生成的 `ImplBase` 实现，协议对象转换后交给 application 层处理。
5. 增加真实 gRPC 调用测试，覆盖成功响应、错误状态和超时等与新增行为有关的场景。

协议新增字段使用新编号，已删除字段的编号及名称用 `reserved` 保留，避免重新分配。新增资金写接口时，需要单独设计业务请求标识和幂等规则。

协议按业务领域和版本组织：`ledger/account/v1` 对应 `ledger.account.v1` 和 Java 包 `com.ledger.pb.account.v1`，交易协议同理。账户和交易协议分别提供 SayHello 示例；目录约定见 [协议说明](../ledger-grpc/README.md)。模块或 Java 包改名不要求修改 Protobuf package。

调用其他服务时，在使用方配置中启用相应客户端，注入共享模块的 `AccountGrpcClient` 或 `TradingGrpcClient`。默认超时为 2 秒，支持配置与单次覆盖；服务发现和连接管理统一位于 `ledger-grpc`。配置和新增 RPC 的方法见[共享客户端指南](../ledger-grpc/README.md)，当前 [TradingHelloEndpoint](../trading-service/src/main/java/com/ledger/trading/interfaces/grpc/TradingHelloEndpoint.java) 展示了账户客户端的用法。

每个服务独立声明中间件依赖并维护 `application.yml`。Nacos gRPC 注册与 MyBatis 插件使用各服务 `infrastructure.config` 下的普通 `@Configuration` 类，由应用扫描加载，无需自动配置导入清单。Nacos 注册监听器在 gRPC 启动事件发生时获取注册组件，避免提前判断 Bean 是否存在；未启用 Nacos 注册时跳过处理。修改这两项基础配置时需分别检查两个服务。

## 新增数据库访问

按[数据库访问约定](persistence.md)在根目录 `db/` 添加并手动执行 SQL，再添加实体与 Mapper。schema 归属、主键、版本字段由业务明确声明。多个 Mapper 操作需要原子性时，在被其他 Spring Bean 调用的 application 层公共方法上标记 `@Transactional`。

## 新增业务用例

实现前先明确数据归属、业务不变量、合法状态迁移和失败语义，按[业务分层约定](architecture.md#业务分层与数据归属)组织代码。涉及资金与跨服务流程时，参考[架构演进路线](roadmap.md)确定一致性方案。

1. 明确写入由哪个服务负责，定义 RPC 请求、响应和错误状态；检查方法是否需要通过网关对外开放。
2. 设计表约束和本地事务，将余额、冻结、流水与幂等记录等需要原子更新的数据放入同一事务边界。
3. 定义稳定的业务操作标识、重复提交行为及结果查询方式；请求超时后也要能够确认执行结果。
4. 如需业务事件，明确投递恢复、消费去重及乱序处理；跨服务流程记录可恢复的中间状态。
5. 增加与行为相关的测试，至少覆盖成功、非法状态、重复请求、并发竞争及事务回滚；涉及 RPC 或事件时补充超时与恢复场景。

当前已有共享客户端与跨服务接口测试，首个业务用例仍需补充数据库并发、事务和幂等测试。数据库访问文档中的示例实体和表仅用于说明用法，不能直接作为资金模型。

## Kafka 与 Redis 扩展入口

Kafka 当前使用字符串 key/value 序列化，后续定义事件格式时需要同步调整生产者和消费者。项目不创建业务 topic，新增 topic 应通过明确的配置或运维步骤创建。`enable.idempotence=true` 解决生产者协议层的重复写入问题，消费处理仍需业务去重；数据库事务也不会自动覆盖 Kafka 发送。

Redis 可注入 `StringRedisTemplate`，业务自行约定 key 前缀、序列化和过期时间。目前没有统一缓存注解策略，也没有使用 Redis 作为资金余额的权威存储。

## 构建与运行验证

```bash
# 编译、打包 Java 模块，无需运行中间件。
mvn verify
```

`mvn verify` 执行共享 gRPC 模块与交易服务的自动化测试，覆盖客户端装配、发现和连接生命周期、本地真实 RPC 调用、超时及错误传播；无需真实中间件。通过不代表运行时中间件已连通。启动中间件和两个服务后，按[项目首页的 gRPC 验证](../README.md#grpc-验证)检查 gRPC 服务是否响应。Go 网关仍有独立测试，命令见下方“Go 网关开发”。

## 常见问题

| 现象 | 检查与处理 |
| --- | --- |
| Enforcer 提示 Java 版本不匹配 | 检查 `java -version`、`mvn --version` 和 IDE Maven Runner，均应使用 JDK 25 |
| Java 服务连接了错误的数据库/Redis/Kafka 地址 | dev 检查本地 `application-dev.yml`；test/prod 检查当前 namespace 下的公共配置及服务覆盖项；`POSTGRES_*` 等旧变量不再被配置引用 |
| 切换到 test/prod 后无法连接 Nacos | 默认地址为 localhost:8848，按实际部署覆盖，并核对所选环境的 namespace、group 和认证 |
| test/prod 的 Nacos 配置加载失败或数据源缺少 URL | 核对公共和服务专属 Data ID 均已发布且非空；不会回退到 dev 的本地配置 |
| gRPC 提示 address already in use | 检查旧服务进程；使用当前进程的 `GRPC_PORT` 覆盖监听端口 |
| 下游 RPC 返回 UNAVAILABLE | 确认账户服务已启动且注册成功，双方 namespace/group 一致，实例有 `protocol=grpc` 且地址可达；停止服务后发现视图可能需要时间更新 |
| 共享客户端 Bean 不存在 | 检查 `ledger.grpc.client.enabled=true` 及对应 `account.enabled` / `trading.enabled` 开关；重新导入 Maven 项目 |
| gRPC 返回 DEADLINE_EXCEEDED | 检查下游服务地址、网络及耗时，以及调用方设置的 deadline |
| grpcurl list 失败 | 确认访问的是 Java 服务端口 8081/8082，并使用 `-plaintext`；8848/9848 属于 Nacos |
| Nacos 登录或连接失败 | 按实际部署区分 API、客户端 gRPC 和控制台端口，核对认证与 TLS；Java 和 Go 网关的连接配置需分别填写 |
| PostgreSQL relation does not exist | 核对根目录 `db/` 中的 SQL 是否已在目标数据库手动执行，以及 `@TableName` 的 schema/table；MyBatis-Plus 不自动建表 |
| Mapper 无法注入 | 确认有 `@Mapper`，位于应用扫描包下；跨包使用 `@MapperScan` |
| Invalid bound statement | 核对 Mapper 方法名、XML namespace、XML 路径及是否已打包到资源目录 |
| 乐观锁没有拦截更新 | 确认实体有 `@Version`、数据库版本字段非空、实体携带读取到的版本，并检查 updateById 返回值 |
| Kafka 能连接 bootstrap 却无法收发 | broker 返回的 advertised address 也必须可达；核对 SASL 配置与 topic 是否存在 |
| 修改 PostgreSQL 开发密码后认证失败 | 已存在的数据卷不会重新初始化密码，使用库内现有凭据或按数据库管理流程修改 |
| 网关提示 open gateway config | 默认从当前工作目录读取 `config/config.yaml`；进入 `gateway/` 启动，或使用 `-config` 指定文件 |
| 修改 `.env` 后网关配置没有变化 | 网关不会自动加载 `.env`；在启动 YAML 中声明占位符并设置对应进程环境变量，或直接修改 YAML 字段，然后重启 |
| 设置环境变量后网关配置没有变化 | 检查实际使用的 `-config` 文件是否在对应字段声明了 `${VAR:默认值}` 或 `${VAR}`；直接写死的字段不会被环境变量覆盖，修改后需重启 |
| 网关提示 flag provided but not defined | 旧的逐项启动参数已移除，使用 `-config config/config.local.yaml` |
| 网关提示 field ... not found | 启动配置严格校验字段；对照 `gateway/config/config.yaml` 检查字段名、缩进，并确认没有将路由 YAML 当作启动配置 |
| 网关找不到路由或证书文件 | 配置中的相对路径以启动配置所在目录为准；`descriptor_sets` 中的文件以 `descriptor_dir` 为准 |

当前 Java 业务服务没有 HTTP Actuator 端口。不要通过 HTTP 请求 8081/8082 判断服务是否启动；使用 grpcurl 反射列表或启动日志。

## Go 网关开发

网关使用独立 Go Modules，与 Maven 分开构建和测试。进入 `gateway/` 后执行 `go test ./...`、`go vet ./...`，需要并发检测时执行 `CGO_ENABLED=1 go test -race ./...`（需要 C 编译器）。

网关测试使用测试后端，不要求运行 Java 服务或 Nacos。真实环境的 HTTP 调用示例见[网关指南](../gateway/README.md#启动与验证)。

启动配置在 `gateway/config/config.yaml`，服务映射示例在 `gateway/config/routes.example.yml`，声明账户和交易服务，需发布到 Nacos 的 `gateway-routes.yml`。网关无需导入 `.env`；在 IDE 中将工作目录设为 `gateway/`，或通过 `-config` 指定启动配置文件。监听、Nacos、TLS 和 token 等启动配置修改后需重启，Nacos 服务映射支持热更新。个人配置可复制为已忽略的 `gateway/config/config.local.yaml`，旧配置中的 `routes_source`、`routes_file` 字段需要删除。

新增已配置 protobuf service 中的 unary RPC 后，可通过 `/api/{版本}/{服务简称}/{方法名}` POST JSON 调用，无需逐接口发布路由。GET Query 调用需将只读方法加入对应版本的 `get_methods`。协议按需加载，缓存 1 分钟；需要立即刷新时更新配置 revision。新增服务或版本仍需发布服务映射。网关可以先于 Java 服务启动，协议加载失败后的请求会重新尝试。`/readyz` 返回生效 revision，不检查后端连通性；无效配置更新记录 `service update rejected` 并保留旧配置。完整字段及迁移步骤见[网关指南](../gateway/README.md)。
