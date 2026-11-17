# 数据库 SQL

SQL 集中保存在根目录 `db/` 下，按服务划分：

```text
db/
├── account/
│   └── 001_create_schema.sql
└── trading/
    └── 001_create_schema.sql
```

服务启动时不会自动创建 schema、执行 SQL 或记录脚本执行历史。部署前需使用数据库客户端手动执行脚本。

## 初始化

先准备 PostgreSQL 数据库和登录用户，再从项目根目录执行以下示例，按实际环境替换连接参数：

```bash
psql -X -h localhost -p 5432 -U ledger -d ledger -v ON_ERROR_STOP=1 --single-transaction -f db/account/001_create_schema.sql
psql -X -h localhost -p 5432 -U ledger -d ledger -v ON_ERROR_STOP=1 --single-transaction -f db/trading/001_create_schema.sql
```

命令同样适用于 PowerShell。`psql` 会按需提示输入密码；也可通过 PostgreSQL 密码文件配置认证。不要将 JDBC 格式的 `POSTGRES_URL` 直接传给 `psql`。

执行账户需具备相应建 schema/表权限。若由管理员执行，需为应用用户配置对应 schema 和表的访问权限。当前脚本仅创建 schema，没有业务表；两份初始化脚本均可重复执行。

也可以在数据库 GUI 客户端连接目标数据库后，依次打开并执行这些 SQL 文件。

## 后续结构变更

- 在所属服务目录新增编号递增的文件，例如 `account/002_create_account_profile.sql`，按编号顺序执行。
- SQL 使用完整的 `schema.table` 名称，例如 `account.account_profile`。
- 已执行的脚本保留原内容，后续变更新增文件；执行人员自行记录各环境已执行的编号，避免重复执行不具备幂等性的脚本。
- SQL 不会打包到应用 JAR；部署流程需单独分发并执行所需脚本，再启动对应服务。

原有数据库中的业务数据和迁移历史表无需为本次调整而删除。更多持久层用法见[数据库访问约定](../docs/persistence.md)。
