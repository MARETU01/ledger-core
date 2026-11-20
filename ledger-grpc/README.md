# 共享 gRPC 协议与客户端

`ledger-grpc` 是项目内部共享的通信库，不单独部署。包含版本化 Protobuf 协议、构建生成的消息与 Stub、账户和交易客户端，以及基于 Spring Cloud `DiscoveryClient` 的服务发现、连接管理和 Spring Boot 自动装配。

Nacos 实现及连接参数由使用方提供；本模块不引入 Nacos starter、数据库、Redis 或 Kafka。客户端默认关闭，单纯依赖协议不会创建客户端或连接。服务端 gRPC 注册配置仍由各服务维护。

## 模块结构

```text
src/main/
├── proto/ledger/                       # 协议源文件
├── java/com/ledger/grpc/
│   ├── client/account/AccountGrpcClient.java
│   ├── client/trading/TradingGrpcClient.java
│   ├── client/GrpcClientSupport.java    # 阻塞 unary 调用与 deadline
│   ├── discovery/DiscoveryGrpcChannels.java
│   └── config/                         # 属性与条件自动装配
└── resources/META-INF/spring/
    └── org.springframework.boot.autoconfigure.AutoConfiguration.imports
```

## 使用共享客户端

服务通过 Maven 依赖 `com.ledger:ledger-grpc`。在使用方配置中开启总开关和实际需要的客户端，例如交易服务调用账户：

```yaml
ledger:
  grpc:
    client:
      enabled: true
      plaintext: true
      shutdown-timeout: 3s
      account:
        enabled: true
        service-id: account-service
        timeout: 2s
```

注入 `AccountGrpcClient` 后调用：

```java
var response = accountClient.sayHello(request);
// 单次覆盖超时，不改变其他调用的默认值。
var responseWithTimeout = accountClient.sayHello(request, Duration.ofSeconds(1));
```

调用交易服务时开启 `ledger.grpc.client.trading.enabled=true`，注入 `TradingGrpcClient`；其 `service-id` 默认 `trading-service`，`timeout` 默认 `2s`。两个客户端各自按开关装配，支持使用方声明同类型 Bean 覆盖默认实现。当前两个客户端仅封装已有的 SayHello RPC，后续 RPC 随协议新增方法。

| 配置 | 默认值 | 作用 |
| --- | --- | --- |
| `ledger.grpc.client.enabled` | false | 是否启用共享客户端自动装配 |
| `ledger.grpc.client.account.enabled` / `trading.enabled` | false | 是否创建对应客户端 |
| `ledger.grpc.client.account.service-id` / `trading.service-id` | account-service / trading-service | Spring Cloud 发现的服务名 |
| `ledger.grpc.client.account.timeout` / `trading.timeout` | 2s | 每次 RPC 的默认时间预算，必须大于零 |
| `ledger.grpc.client.plaintext` | true | 开发环境明文；false 使用 gRPC 标准 TLS 和默认信任配置 |
| `ledger.grpc.client.shutdown-timeout` | 3s | 所有共享连接关闭时的总等待预算，必须大于零 |

交易服务已在自身 `application.yml` 启用账户客户端；账户服务默认不启用下游客户端。超时、服务名和传输配置可由使用方环境配置覆盖。启用 TLS 时需同时配置服务端 TLS，当前不提供自定义 CA、mTLS 或凭据拦截器的自动装配。

## 服务发现与连接生命周期

客户端启动不查询发现客户端，也不要求下游在线。第一次调用时才取得使用方的 `DiscoveryClient`；使用 Nacos 时，调用方与目标服务需位于可互相发现的 namespace/group。缺少发现实现、发现异常或无可用 gRPC 实例时，调用返回 `UNAVAILABLE`。

每次调用查询实例列表，筛选 `protocol=grpc` 的实例，按服务分别轮询并复用地址连接。发现的健康状态取决于使用方 `DiscoveryClient` 的实现。发现列表成功返回后，会淘汰该服务已移除地址的连接；正在执行的阻塞调用持有租约，调用结束后再关闭旧连接。发现查询失败时拒绝本次调用，保留缓存连接供后续发现恢复后复用。

当前没有后台发现轮询，实例移除后的清理在该服务下一次调用时触发；应用关闭时释放所有连接，在总等待预算耗尽后强制关闭。连接管理器的 `execute` 仅用于完整的阻塞调用，不能在回调返回后继续启动异步调用或持有连接发起请求。

每次调用独立设置 deadline，并遵循 gRPC 上下文中的上游 deadline 与取消。调用保留 gRPC 错误状态，关闭可配置的 gRPC 重试，不增加业务重试或补偿。资金写操作仍需独立的幂等与结果查询设计。当前也未自动转发服务端收到的身份或追踪 metadata。

## Protobuf 协议

协议按业务领域和版本组织。账户与交易协议各自提供 SayHello 示例 RPC，分别拥有自己的请求和响应类型。

```text
src/main/proto/ledger/
├── account/v1/
│   └── account_service.proto   # 账户服务 RPC、请求和响应
└── trading/v1/
    └── trading_service.proto   # 交易服务 RPC、请求和响应
```

模型变多时，在同目录按需添加 `account.proto`、`order.proto` 等文件，通过 proto `import` 引用；无需提前创建空模型文件。

RPC 请求和响应统一使用 `<方法名>Request`、`<方法名>Response`，例如 `SayHello(SayHelloRequest) returns (SayHelloResponse)`。账户和交易领域在各自的 package 中定义消息。

| 目录 | Protobuf package | 生成的 Java package |
| --- | --- | --- |
| `ledger/account/v1` | `ledger.account.v1` | `com.ledger.pb.account.v1` |
| `ledger/trading/v1` | `ledger.trading.v1` | `com.ledger.pb.trading.v1` |

构建命令：`mvn -pl ledger-grpc -am compile`。生成代码位于 `target/generated-sources/protobuf`，不要手动修改或提交。

移动文件后同步调整 proto import；修改 Java package 后同步调整 Java import；修改 Protobuf package 或 service 名称后，同步修改网关服务映射中的 `proto_services` 和调用方。修改 rpc 名称会改变网关 URL 中的方法名，GET 方法还需同步更新 `get_methods`。删除或移动协议后使用 `mvn clean verify`，清理旧生成代码和 JAR。

协议新增字段使用新编号；删除字段时通过 `reserved` 保留原编号和名称。对外与内部 RPC 按 protobuf service 分开组织，当前网关会对已配置 service 中的全部 unary 方法开放 POST。滚动发布时需验证新旧协议兼容及网关缓存刷新行为，详见[网关指南](../gateway/README.md#协议缓存发现与热更新)。

模块目录和 Maven artifact 已统一为 `ledger-grpc`，生成 Java 包仍为 `com.ledger.pb.*`，Protobuf package、RPC 路径与网关服务映射保持兼容。IDE 需重新导入根 Maven 项目；其他仓库中的使用方需要更新 Maven 坐标。

## 测试与扩展

从项目根目录执行 `mvn -pl ledger-grpc -am verify`。测试覆盖按需装配、属性校验、两个客户端的真实本地 gRPC 调用、错误与取消传播、默认和单次超时、轮询和连接复用、发现失败及空列表恢复、旧连接排空与关闭超时。测试使用替代的发现客户端，不需要 Nacos 或数据库。

新增 RPC 后在对应客户端提供方法，通过 `GrpcClientSupport.invoke` 共用调用策略；领域规则、事务和跨服务补偿继续放在业务服务。通信库与协议共同发布，升级前需验证使用方兼容性。

相关机制参考：[Spring Boot 自动装配](https://docs.spring.io/spring-boot/reference/features/developing-auto-configuration.html)、[gRPC deadline](https://grpc.io/docs/guides/deadlines/)、[ManagedChannel 生命周期](https://grpc.github.io/grpc-java/javadoc/io/grpc/ManagedChannel.html)。
