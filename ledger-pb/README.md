# Protobuf 协议

协议按业务领域和版本组织，不单独部署。账户与交易协议各自提供 SayHello 示例 RPC，分别拥有自己的请求和响应类型。

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

构建命令：`mvn -pl ledger-pb -am compile`。生成代码位于 `target/generated-sources/protobuf`，不要手动修改或提交。

移动文件后同步调整 proto import；修改 Java package 后同步调整 Java import；修改 Protobuf package 或 service 名称后，同步修改网关服务映射中的 `proto_services` 和调用方。修改 rpc 名称会改变网关 URL 中的方法名，GET 方法还需同步更新 `get_methods`。删除或移动协议后使用 `mvn clean verify`，清理旧生成代码和 JAR。

协议新增字段使用新编号；删除字段时通过 `reserved` 保留原编号和名称。对外与内部 RPC 按 protobuf service 分开组织，当前网关会对已配置 service 中的全部 unary 方法开放 POST。滚动发布时需验证新旧协议兼容及网关缓存刷新行为，详见[网关指南](../gateway/README.md#协议缓存发现与热更新)。
