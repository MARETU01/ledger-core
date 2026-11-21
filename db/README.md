# 数据库迁移

[返回项目首页](../README.md) · [配置与运行](../docs/configuration.md) · [数据库访问约定](../docs/persistence.md)

两个 Java 服务使用 Flyway 在启动时校验并执行各自的版本化迁移。数据库和登录角色需提前准备；schema、迁移历史及后续业务表由迁移管理。当前 V1 仅创建 schema，不包含业务表。

## 脚本与数据归属

| 服务 | SQL 目录 | 所属 schema | 迁移历史表 |
| --- | --- | --- | --- |
| account-service | [账户迁移](../account-service/src/main/resources/db/migration) | `account` | `account.flyway_schema_history` |
| trading-service | [交易迁移](../trading-service/src/main/resources/db/migration) | `trading` | `trading.flyway_schema_history` |

SQL 随所属服务的 JAR 打包。两个服务分别从 V1 编号，各自维护历史，版本号不需要跨服务统一。服务只能迁移和访问自己的 schema。

## 首次初始化

开发配置默认使用 `localhost:5432/ledger` 和登录角色 `ledger`。在新建的本地 PostgreSQL 实例上，可用具备创建角色、数据库权限的管理员执行：

```bash
psql -h localhost -p 5432 -U postgres -d postgres
```

进入 `psql` 后执行以下内容，交互设置密码，再创建数据库；角色和数据库已存在时跳过创建：

```sql
CREATE ROLE ledger LOGIN;
\password ledger
CREATE DATABASE ledger OWNER ledger;
\q
```

将密码同步到两个服务的 `spring.datasource.password`，或在启动进程中设置 `SPRING_DATASOURCE_PASSWORD`。dev 的其他连接参数位于各自的 `application-dev.yml`；test/prod 使用对应 namespace 的 Nacos 运行配置。修改客户端配置不会修改数据库密码。

用应用角色确认连接到正确数据库：

```bash
psql -h localhost -p 5432 -U ledger -d ledger -W
```

数据库所有者可创建本项目所需 schema。复用已有数据库时，迁移角色需要连接数据库、创建目标 schema 的权限；schema 已存在时，还需具备在其中创建对象及修改待迁移对象的权限。运行角色与迁移角色分离时，权限配置见下方[迁移与运行角色](#迁移与运行角色)。

从项目根目录构建并分别启动服务，命令见[本地启动](../README.md#本地启动)。Flyway 先准备历史 schema，再执行所属服务的 V1。首次接入无需手动运行 `V1__create_*_schema.sql`，也无需创建业务表。

## 检查迁移结果

两个服务均已成功迁移后，连接目标数据库执行：

```sql
SELECT current_database(), current_user;

SELECT version, description, script, success
FROM account.flyway_schema_history
ORDER BY installed_rank;

SELECT version, description, script, success
FROM trading.flyway_schema_history
ORDER BY installed_rank;
```

当前两个历史表应分别包含版本 `1`，对应 `V1__create_account_schema.sql` 和 `V1__create_trading_schema.sql`，`success` 为 true。只启动一个服务时，只检查该服务的历史表。历史表不存在时，先确认数据库、schema、服务启动日志和 Flyway 开关。

## 新增迁移

1. 在所属服务的迁移目录新增 `V<版本>__<说明>.sql`，例如 `V2__create_account_profile.sql`；双下划线不可省略，版本不得与该服务已有脚本重复。
2. SQL 显式使用 `account.table_name` 或 `trading.table_name`，不依赖连接的 `search_path`。表、索引和约束由数据所属服务维护。
3. 在独立开发数据库验证首次建库和已有版本升级，检查历史表、目标结构及受影响的查询，再提交 SQL 和相关代码。

已执行的版本化脚本保留原内容，后续调整新增版本。实体、Mapper 和 MyBatis 注解不会创建表。账户资料表的 SQL 与 Java 示例见[数据库访问约定](../docs/persistence.md#完整示例账户资料表)，示例尚未加入实际迁移。

`mvn verify` 仅构建，不执行数据库迁移；迁移发生在服务启动时。涉及结构变更的验证需使用测试数据库启动对应服务，不能只看编译结果。

## 接入已有数据库

接入前先确认目标数据库、schema、对象归属和已有 Flyway 历史，并备份需要保留的数据。根据实际状态处理：

| 目标状态 | 接入方式 |
| --- | --- |
| schema 不存在 | 授予创建权限，由服务启动时创建并迁移 |
| schema 已存在且为空，无 Flyway 历史 | 确认 schema 权限后按正常启动流程迁移；V1 使用 `CREATE SCHEMA IF NOT EXISTS` |
| 已有本项目的迁移历史 | 对照发布版本检查脚本与校验和，再启动升级；不要修改旧脚本来通过校验 |
| schema 非空且没有 Flyway 历史 | 先核对现有结构与拟接入版本，制定基线和差异迁移方案，再接入 |

项目配置 `baseline-on-migrate: false`，不会自动为非空且无历史的 schema 建立基线。基线表示此前版本对应的结构已经具备；必须确认选定版本的全部结构满足要求，不能只根据 V1 的名称判断。基线与历史修复属于数据库接入操作，仓库未提供一键脚本。

## 迁移与运行角色

默认 Flyway 复用 `spring.datasource` 的连接。开发环境两个服务共用 `ledger`，schema 归属尚无数据库权限隔离。

需要分离权限时，为每个服务准备所属 schema 的迁移角色与运行角色。迁移角色负责 DDL、迁移历史和已有对象变更；运行角色只获得所属 schema 的 `USAGE` 及所需表、序列操作权限。后续新对象的默认权限也需由对象创建者配置。

将独立迁移连接放在该服务的配置中，并填写实际地址和凭据：

```yaml
spring:
  flyway:
    url: jdbc:postgresql://db.example:5432/ledger
    user: account_migrator
    password: ${ACCOUNT_MIGRATOR_PASSWORD}
```

`spring.datasource` 继续使用运行角色；交易服务配置自己的迁移角色。保留各服务 `application.yml` 中的 `default-schema` 和 `schemas`，不要将两者合并到公共 Nacos 配置。

## 发布与失败处理

默认模式为应用启动时迁移。发布前检查 SQL 与新旧应用是否兼容；删除列、重命名或收紧约束等变更需安排兼容步骤及发布顺序。应用回退不会撤销已经执行的 SQL。

如需由独立发布任务迁移，需另外配置 Flyway 工具、数据库连接、所属 schema 和同一版本的脚本。任务完成并核对历史后，应用可设置 `spring.flyway.enabled=false`，由发布流程承担后续校验和迁移；仓库当前未提供该发布任务。

| 现象 | 处理方向 |
| --- | --- |
| 无法连接或认证失败 | 核对生效 profile、数据库地址、角色和密码 |
| 创建 schema 或修改对象被拒绝 | 检查数据库创建权限、schema 权限及对象所有者 |
| 非空 schema 缺少历史表 | 按已有数据库接入流程核对，保留自动 baseline 关闭状态 |
| 校验和不一致 | 比较已发布脚本与仓库版本，恢复误改脚本；结构变更使用新版本 |
| SQL 执行失败 | 检查失败语句、实际结构及历史状态，确认是否存在部分执行，再制定修复方案 |

项目启用迁移前校验、文件命名校验，并禁用 Flyway clean。失败时保留日志和现场；不要删除历史表、修改 success 标志，或将清库作为升级修复方式。
