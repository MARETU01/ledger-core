# 开发与排错

[返回项目首页](../README.md)

## 构建与启动

从 Maven 根 POM 导入 IDE 项目，Project SDK 与 Maven Runner 都选择 JDK 25。生成的 Protobuf Java 文件位于 `ledger-pb/target`，由构建维护，不提交到 Git。

```bash
java -version
mvn --version
mvn clean verify
```

仅构建账户服务及其依赖时：

```bash
mvn -pl account-service -am package
```

完整启动流程见首页。IDE 可分别运行 `AccountApplication` 和 `TradingApplication`，将 `.env` 中的变量填写到各运行配置。停服务使用 `Ctrl+C`，触发应用关闭及 Nacos 注销；中间件单独由 Compose 管理。

## 修改 gRPC 协议

1. 在 `ledger-pb/src/main/proto/ledger/<业务>/v1/` 中维护 `.proto`。
2. 定义 `package`、`java_package` 和 `java_multiple_files`，按服务职责设计 RPC。
3. 执行 `mvn -pl ledger-pb -am compile` 生成消息类与 stub，不手改 target 中的生成文件。
4. 在对应服务的 `interfaces.grpc` 中用 `@Service` 注册生成的 `ImplBase` 实现，协议对象转换后交给 application 层处理。
5. 增加真实 gRPC 调用测试，覆盖成功响应、错误状态和超时等与新增行为有关的场景。

协议新增字段使用新编号，已删除字段的编号及名称用 `reserved` 保留，避免重新分配。当前 `ProbeRequest.request_id` 用于诊断透传；新增资金写接口时，需要单独设计业务请求标识和幂等规则。

模块名为 `ledger-pb`，生成的 Java 包为 `com.ledger.pb.platform.v1`；当前 Protobuf package 仍为 `ledger.platform.v1`，因此 gRPC 方法名仍是 `/ledger.platform.v1.AccountProbe/Check` 等。模块或 Java 包改名不要求修改协议 package。

调用其他服务时，可以参考 [TradingProbeEndpoint](../trading-service/src/main/java/com/ledger/trading/interfaces/grpc/TradingProbeEndpoint.java) 与 [DiscoveryGrpcChannels](../ledger-common/src/main/java/com/ledger/common/DiscoveryGrpcChannels.java)。当前探针使用 blocking stub 和 2 秒 deadline；该值写在代码中，尚未暴露环境变量。

## 新增数据库访问

按[数据库访问约定](persistence.md)在根目录 `db/` 添加并手动执行 SQL，再添加实体与 Mapper。schema 归属、主键、版本字段由业务明确声明。多个 Mapper 操作需要原子性时，在被其他 Spring Bean 调用的 application 层公共方法上标记 `@Transactional`。

## Kafka 与 Redis 扩展入口

Kafka 当前使用字符串 key/value 序列化，后续定义事件格式时需要同步调整生产者和消费者。Compose 关闭自动创建 topic，业务 topic 应通过明确的配置或运维步骤创建。`enable.idempotence=true` 解决生产者协议层的重复写入问题，消费处理仍需业务去重；数据库事务也不会自动覆盖 Kafka 发送。

Redis 可注入 `StringRedisTemplate`，业务自行约定 key 前缀、序列化和过期时间。目前没有统一缓存注解策略，也没有使用 Redis 作为资金余额的权威存储。

## 构建与运行验证

```bash
# 编译、打包 Java 模块，无需运行中间件。
mvn verify
```

当前 Java 模块未包含自动化测试；`mvn verify` 成功表示构建通过，不代表运行时中间件已连通。启动中间件和两个服务后，按[项目首页的 gRPC 验证](../README.md#grpc-验证)检查诊断调用链。Go 网关仍有独立测试，命令见下方“Go 网关开发”。

## 常见问题

| 现象 | 检查与处理 |
| --- | --- |
| Enforcer 提示 Java 版本不匹配 | 检查 `java -version`、`mvn --version` 和 IDE Maven Runner，均应使用 JDK 25 |
| 服务仍连接 5432/6379/8848，未使用 Compose 端口 | 当前终端没有导入 `.env`；Java 不会自动读取该文件 |
| gRPC 提示 address already in use | 检查旧服务进程；使用当前进程的 `GRPC_PORT` 覆盖监听端口 |
| TradingProbe 返回 UNAVAILABLE | 确认账户服务已启动且注册成功，双方 namespace/group 一致，实例有 `protocol=grpc` 且地址可达；停止服务后发现视图可能需要时间更新 |
| gRPC 返回 DEADLINE_EXCEEDED | 检查账户服务地址、网络及耗时；当前下游调用 deadline 为 2 秒 |
| grpcurl list 失败 | 确认访问的是 Java 服务端口 8081/8082，并使用 `-plaintext`；18848/19848 属于 Nacos |
| Nacos 登录或连接失败 | 区分 API、客户端 gRPC 和控制台端口，核对认证与 TLS；项目默认分别为 18848、19848、18888 |
| PostgreSQL relation does not exist | 核对根目录 `db/` 中的 SQL 是否已在目标数据库手动执行，以及 `@TableName` 的 schema/table；MyBatis-Plus 不自动建表 |
| Mapper 无法注入 | 确认有 `@Mapper`，位于应用扫描包下；跨包使用 `@MapperScan` |
| Invalid bound statement | 核对 Mapper 方法名、XML namespace、XML 路径及是否已打包到资源目录 |
| 乐观锁没有拦截更新 | 确认实体有 `@Version`、数据库版本字段非空、实体携带读取到的版本，并检查 updateById 返回值 |
| Kafka 能连接 bootstrap 却无法收发 | broker 返回的 advertised address 也必须可达；核对 SASL 配置与 topic 是否存在 |
| 修改 PostgreSQL 开发密码后认证失败 | 已存在的数据卷不会重新初始化密码，使用库内现有凭据或按数据库管理流程修改 |

当前 Java 业务服务没有 HTTP Actuator 端口。不要通过 HTTP 请求 8081/8082 判断服务是否启动；使用 gRPC 探针或启动日志。

## Go 网关开发

网关使用独立 Go Modules，与 Maven 分开构建和测试。进入 `gateway/` 后执行 `go test ./...`、`go vet ./...`，需要并发检测时执行 `CGO_ENABLED=1 go test -race ./...`（需要 C 编译器）。

新增 Java RPC 后，通过路由发布和反射或描述文件加载完成接入；只修改路由无需重编译 Go。进程级联调及配置字段见[网关指南](../gateway/README.md)。网关的 `/readyz` 返回生效 revision；无效更新在日志记录 `route update rejected` 并保留旧配置。
