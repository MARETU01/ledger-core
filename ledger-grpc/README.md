# 共享 gRPC 协议与通信支持

[返回项目首页](../README.md) · [架构说明](../docs/architecture.md) · [开发与排错](../docs/development.md)

`ledger-grpc` 是项目内部共享的通信库，不单独部署。包含版本化 Protobuf 协议、构建生成的消息与 Stub、共享客户端、默认异常处理、请求标识与调用日志，以及服务发现、连接管理和 Spring Boot 自动装配。

Nacos 实现及连接参数由使用方提供；本模块不引入 Nacos starter、数据库、Redis 或 Kafka。客户端默认关闭，单纯依赖协议不会创建客户端或连接。服务端 gRPC 注册配置仍由各服务维护。

`spring-grpc-core` 作为 optional 依赖用于编译服务端 advice 与全局拦截器；运行服务通过自身的 gRPC server starter 提供它。仅使用协议或共享客户端的应用不会因此自动引入服务端 starter。日志使用 SLF4J API，具体日志实现由使用方提供。

## 模块结构

```text
src/main/
├── proto/ledger/                       # 协议源文件
├── java/com/ledger/grpc/
│   ├── client/account/AccountGrpcClient.java
│   ├── client/trading/TradingGrpcClient.java
│   ├── client/GrpcClientSupport.java    # 阻塞 unary 调用与 deadline
│   ├── client/GrpcRequestClientInterceptor.java
│   ├── discovery/DiscoveryGrpcChannels.java
│   ├── logging/                        # 请求上下文、MDC 作用域与完成日志
│   ├── server/DefaultGrpcExceptionAdvice.java
│   ├── server/GrpcRequestLoggingServerInterceptor.java
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

在业务服务的组件扫描包内，通过构造器注入 `AccountGrpcClient`。例如下面的调用类使用 Lombok 生成构造器：

```java
import com.ledger.grpc.client.account.AccountGrpcClient;
import com.ledger.pb.account.v1.SayHelloRequest;
import com.ledger.pb.account.v1.SayHelloResponse;
import lombok.RequiredArgsConstructor;
import org.springframework.stereotype.Service;

@Service
@RequiredArgsConstructor
public class AccountCaller {
    private final AccountGrpcClient accountClient;

    public SayHelloResponse sayHello(String name) {
        var request = SayHelloRequest.newBuilder().setName(name).build();
        return accountClient.sayHello(request);
    }
}
```

需要单次覆盖超时时，调用 `accountClient.sayHello(request, Duration.ofSeconds(1))`，不会改变其他调用的默认值。现有交易入口见 [TradingHelloEndpoint](../trading-service/src/main/java/com/ledger/trading/interfaces/grpc/TradingHelloEndpoint.java)。

调用交易服务时开启 `ledger.grpc.client.trading.enabled=true`，注入 `TradingGrpcClient`；其 `service-id` 默认 `trading-service`，`timeout` 默认 `2s`。两个客户端各自按开关装配，支持使用方声明同类型 Bean 覆盖默认实现。当前两个客户端仅封装已有的 SayHello RPC，后续 RPC 随协议新增方法。

以下字段均位于 `ledger.grpc.client` 下：

| 字段 | 默认值 | 作用 |
| --- | --- | --- |
| `enabled` | false | 是否启用共享客户端自动装配 |
| `account.enabled` / `trading.enabled` | false | 是否创建对应客户端 |
| `account.service-id` / `trading.service-id` | account-service / trading-service | Spring Cloud 发现的服务名，启用的客户端不可为空白 |
| `account.timeout` / `trading.timeout` | 2s | 每次 RPC 的默认时间预算，必须大于零 |
| `plaintext` | true | 开发环境明文；false 使用 gRPC 标准 TLS 和默认信任配置 |
| `shutdown-timeout` | 3s | 所有共享连接关闭时的总等待预算，必须大于零 |

交易服务已在自身 `application.yml` 启用账户客户端；账户服务默认不启用下游客户端。超时、服务名和传输配置可由使用方环境配置覆盖。启用 TLS 时需同时配置服务端 TLS，当前不提供自定义 CA、mTLS 或凭据拦截器的自动装配。

`GrpcClientProperties` 通过 `@ConfigurationProperties` 绑定属性，Lombok 的 `@Getter` / `@Setter` 生成访问方法。客户端与连接管理器在构造时校验必要参数；未启用的客户端不会创建。属性变更后重启使用方服务验证，配置来源见[配置与运行](../docs/configuration.md#共享-grpc-客户端)。

## 服务发现与连接生命周期

客户端启动不查询发现客户端，也不要求下游在线。第一次调用时才取得使用方的 `DiscoveryClient`；使用 Nacos 时，调用方与目标服务需位于可互相发现的 namespace/group。缺少发现实现、发现异常或无可用 gRPC 实例时，调用返回 `UNAVAILABLE`。

每次调用查询实例列表，筛选 `protocol=grpc` 的实例，按服务分别轮询并复用地址连接。发现的健康状态取决于使用方 `DiscoveryClient` 的实现。发现列表成功返回后，会淘汰该服务已移除地址的连接；正在执行的阻塞调用持有租约，调用结束后再关闭旧连接。发现查询失败时拒绝本次调用，保留缓存连接供后续发现恢复后复用。

当前没有后台发现轮询，实例移除后的清理在该服务下一次调用时触发；应用关闭时释放所有连接，在总等待预算耗尽后强制关闭。连接管理器的 `execute` 仅用于完整的阻塞调用，不能在回调返回后继续启动异步调用或持有连接发起请求。

每次调用独立设置 deadline，并遵循 gRPC 上下文中的上游 deadline 与取消。调用保留 gRPC 错误状态，关闭可配置的 gRPC 重试，不增加业务重试或补偿。资金写操作仍需独立的幂等与结果查询设计。共享客户端自动传递请求 ID；身份和追踪 metadata 仍需单独设计传递规则。

## 请求标识与调用日志

启用 Spring gRPC 服务端且存在 `BindableService` Bean 时，自动注册 `@GlobalServerInterceptor` 全局请求拦截器，顺序为 `@Order(-100)`，位于异常处理器外层，因此日志记录映射后的状态。两个业务服务默认启用，客户端开关不影响服务端拦截器。

请求标识使用 metadata `x-request-id`，校验规则与 Go 网关一致：允许字母、数字及 `._:-`，长度 1～128。合法值原样保留，缺失或非法时生成 UUID。服务端将标识写入 gRPC Context，并通过响应 headers 和 trailers 返回；即使没有响应 headers，错误 trailers 也携带标识。

`AccountGrpcClient` 和 `TradingGrpcClient` 自动附加客户端拦截器，将当前请求 ID 传给下游；独立发起调用时生成新 ID。网关传入的请求 ID 可以贯穿交易到账户的调用。直接使用生成的 Stub 时，需要自行附加 `GrpcRequestClientInterceptor` 才具备相同的客户端行为。

每个被拦截的调用在完成时记录一条日志，包含以下字段，不记录请求体、响应体、认证 metadata 或异常描述：

| 字段 | 含义 |
| --- | --- |
| `side` | server 或 client |
| `method` | 完整 protobuf service / RPC 方法名 |
| `request_id` | 校验或生成后的请求 ID |
| `status` | 最终观测到的 gRPC 状态；服务端客户端断连时记录取消状态 |
| `duration_ms` | 从进入拦截器到完成的单调时钟耗时；客户端不包含此前的服务发现查询 |

成功及取消使用 INFO，其他非 OK 状态使用 WARN；未知异常的堆栈由统一异常处理器另行记录。关闭与取消竞争时只记录一次完成日志。

RPC 回调期间 MDC 的 `requestId` 与当前请求一致，回调结束后恢复原值。业务日志可在日志格式中使用 `%X{requestId}`，或通过 `GrpcRequestContext.currentRequestId()` 获取标识。自行启动的异步任务需显式传播 gRPC Context，并按需设置 MDC；框架不会为任意新线程自动复制 MDC。

设置 `ledger.grpc.server.request-logging.enabled=false` 可关闭默认服务端请求拦截器，包括服务端 ID 建立、回传和完成日志；共享客户端的 ID 传递逻辑仍保留。只想停止完成日志时，将 `logging.level.com.ledger.grpc.logging.GrpcCallLog` 设为 `OFF`，上下文传递仍生效。自定义同类型拦截器 Bean 可替换默认实现，使用方需负责将其标为全局拦截器。

## 服务端统一异常处理

应用中存在 `BindableService` Bean 且启用 Spring gRPC 服务端时，`GrpcServerExceptionAutoConfiguration` 自动提供 `DefaultGrpcExceptionAdvice`，与客户端启用开关无关。账户与交易服务共用该策略，Endpoint 可直接抛出异常，由框架拦截并交给 `@GrpcAdvice` 中的 `@GrpcExceptionHandler` 方法。

| 异常 | 默认处理 |
| --- | --- |
| `StatusRuntimeException` / `StatusException` | 保留已有 gRPC 状态、描述和 trailers |
| 其他 `Exception` | 在服务端记录异常堆栈，向调用方返回 `INTERNAL`，描述固定为 `Internal server error` |

业务参数错误等预期失败需显式使用相应状态，例如 `Status.INVALID_ARGUMENT.withDescription("Invalid request").asRuntimeException()`。默认不会将所有 `IllegalArgumentException` 解释为用户输入错误，内部异常消息也不会直接写入响应。显式 gRPC 状态中的描述和 trailers 由抛出方负责定义。

默认策略在 Spring Boot 的 gRPC advice 基础设施之前注册。使用方声明自己的 `@GrpcAdvice` Bean 时，默认 advice 整体退出，由使用方完整定义异常映射。也可设置 `ledger.grpc.server.exception-handling.enabled=false` 关闭默认策略，此时沿用框架和使用方的处理方式。

统一处理适用于 RPC 回调中抛出的异常。自行启动的异步任务应通过 `observer.onError(exception)` 将异常交回 RPC 管道，不能依赖后台线程直接抛异常来结束请求。详情见 [Spring gRPC advice](https://docs.spring.io/spring-grpc/reference/api/java/org/springframework/grpc/server/advice/GrpcAdvice.html)。

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

模块目录和 Maven artifact 为 `ledger-grpc`，生成 Java 包为 `com.ledger.pb.*`。模块名与协议包名分别维护，修改目录名不要求改变 RPC 路径。

## 构建与扩展

从项目根目录执行 `mvn -pl ledger-grpc -am verify`，编译协议、共享客户端并打包，无需运行 Nacos 或数据库。当前模块未包含自动化测试及测试依赖；真实服务发现、RPC 调用与连接生命周期需在联调环境中验证。

新增 RPC 的改动顺序：

1. 修改对应 `.proto` 并重新生成 Stub。
2. 在业务服务的 Endpoint 中实现 RPC，以 `@GrpcService` 注册；同一 protobuf service 的多个 RPC 放在同一个实现类中。
3. 在对应共享客户端增加方法，通过 `GrpcClientSupport.invoke` 共用超时策略。
4. 重新构建使用方并验证实际调用；如需 GET 访问，同步更新网关的只读方法清单。

领域规则、事务和跨服务补偿继续放在业务服务。通信库与协议共同发布，升级前需验证使用方兼容性。注解处理和 IDE 设置见[开发指南](../docs/development.md#lombok-与注解处理)。

相关机制参考：[Spring Boot 自动装配](https://docs.spring.io/spring-boot/reference/features/developing-auto-configuration.html)、[gRPC deadline](https://grpc.io/docs/guides/deadlines/)、[ManagedChannel 生命周期](https://grpc.github.io/grpc-java/javadoc/io/grpc/ManagedChannel.html)。
