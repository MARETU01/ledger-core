# Protobuf 协议

协议按业务领域和版本组织，不单独部署。当前仅保留协议骨架，没有业务 RPC 或消息。

```text
src/main/proto/ledger/
├── account/v1/
│   └── account_service.proto   # 账户服务 RPC、请求和响应
└── trading/v1/
    └── trading_service.proto   # 交易服务 RPC、请求和响应
```

模型变多时，在同目录按需添加 `account.proto`、`order.proto` 等文件，通过 proto `import` 引用；无需提前创建空模型文件。

| 目录 | Protobuf package | 生成的 Java package |
| --- | --- | --- |
| `ledger/account/v1` | `ledger.account.v1` | `com.ledger.pb.account.v1` |
| `ledger/trading/v1` | `ledger.trading.v1` | `com.ledger.pb.trading.v1` |

构建命令：`mvn -pl ledger-pb -am compile`。生成代码位于 `target/generated-sources/protobuf`，不要手动修改或提交。

移动文件后同步调整 proto import；修改 Java package 后同步调整 Java import；修改 Protobuf package、service 或 rpc 名称后，还需同步修改网关的完整 gRPC 方法名和调用方。删除或移动协议后使用 `mvn clean verify`，清理旧生成代码和 JAR。
