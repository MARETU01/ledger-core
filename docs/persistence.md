# 数据库访问约定

[返回项目首页](../README.md)

公共模块使用 `mybatis-plus-spring-boot4-starter`，版本通过根 POM 的 MyBatis-Plus BOM 统一管理。参考[官方安装文档](https://baomidou.com/getting-started/install/)。服务继承 `ledger-platform` 后即可使用，无需单独添加 MyBatis starter 或手动创建 SqlSessionFactory。

## 添加业务持久层

1. 在服务自己的 `db/migration/account/` 或 `db/migration/trading/` 下新增 Flyway 版本迁移。
2. 实体放在 `com.ledger.<服务>.infrastructure.persistence.entity`，通过 `@TableName(value = "表名", schema = "所属服务 schema")` 明确表归属。
3. Mapper 放在 `com.ledger.<服务>.infrastructure.persistence.mapper`，添加 `@Mapper` 并继承 `BaseMapper<实体>`。starter 会扫描 Spring Boot 应用包下的 `@Mapper`；跨包扫描时显式配置 `@MapperScan`。
4. 业务事务边界放在 application 层的 Spring Bean，通过 `@Transactional` 管理多个 Mapper 操作。复杂查询仍可使用 Mapper XML，路径为 `src/main/resources/mapper/**/*.xml`。

示意代码如下，`AccountRow` 对应的实体和业务表需要随业务实现：

```java
@Mapper
public interface AccountMapper extends BaseMapper<AccountRow> {
}

// 无需手写普通增删改查 SQL。
accountMapper.insert(row);
accountMapper.selectById(id);
accountMapper.updateById(row);
accountMapper.selectPage(new Page<>(1, 20),
        Wrappers.<AccountRow>lambdaQuery()
                .eq(AccountRow::getStatus, "ACTIVE")
                .orderByAsc(AccountRow::getId));
```

## 完整示例：账户资料表

以下仅演示持久层接入方式，尚未写入代码仓库或数据库，不代表完整的资金账户模型。示例不包含余额或记账操作。

新增 `account-service/src/main/resources/db/migration/account/V2__create_account_profile.sql`：

```sql
CREATE TABLE account.account_profile (
    id uuid PRIMARY KEY,
    status varchar(16) NOT NULL,
    version integer NOT NULL DEFAULT 0
);
```

实体使用显式 UUID 主键和可更新版本字段：

```java
package com.ledger.account.infrastructure.persistence.entity;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import com.baomidou.mybatisplus.annotation.Version;
import java.util.UUID;

@TableName(value = "account_profile", schema = "account")
public class AccountRow {
    @TableId
    private UUID id;
    private String status;
    @Version
    private Integer version;

    public UUID getId() { return id; }
    public void setId(UUID id) { this.id = id; }
    public String getStatus() { return status; }
    public void setStatus(String status) { this.status = status; }
    public Integer getVersion() { return version; }
    public void setVersion(Integer version) { this.version = version; }
}
```

Mapper 只需要声明接口：

```java
package com.ledger.account.infrastructure.persistence.mapper;

import com.baomidou.mybatisplus.core.mapper.BaseMapper;
import com.ledger.account.infrastructure.persistence.entity.AccountRow;
import org.apache.ibatis.annotations.Mapper;

@Mapper
public interface AccountMapper extends BaseMapper<AccountRow> {
}
```

新建实体时显式赋值 ID、状态和初始版本，再调用 `insert`：

```java
var row = new AccountRow();
row.setId(UUID.randomUUID());
row.setStatus("ACTIVE");
row.setVersion(0);
accountMapper.insert(row);
```

事务用例示例：

```java
package com.ledger.account.application;

import com.ledger.account.infrastructure.persistence.mapper.AccountMapper;
import java.util.UUID;
import org.springframework.stereotype.Service;
import org.springframework.transaction.annotation.Transactional;

@Service
public class AccountProfileService {
    private final AccountMapper accountMapper;

    public AccountProfileService(AccountMapper accountMapper) {
        this.accountMapper = accountMapper;
    }

    @Transactional
    public void freezeProfile(UUID id) {
        var row = accountMapper.selectById(id);
        if (row == null) {
            throw new IllegalArgumentException("Account profile does not exist");
        }
        row.setStatus("FROZEN");
        if (accountMapper.updateById(row) != 1) {
            throw new IllegalStateException("Account profile changed concurrently");
        }
    }
}
```

调用方通过注入的 Spring Bean 调用该方法，使事务代理生效。示例直接引用 Mapper，适合验证接入；复杂领域用例可在 domain 层定义仓储接口，由 infrastructure 实现。业务状态迁移规则和 gRPC 错误映射需随具体用例补充。

## 自定义 SQL 与迁移维护

需要复杂 SQL 时，在 Mapper 声明方法、用 `@Param` 命名参数，并在 `src/main/resources/mapper/AccountMapper.xml` 中维护 SQL。XML 的 namespace 必须匹配 Mapper 全限定名：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE mapper PUBLIC "-//mybatis.org//DTD Mapper 3.0//EN"
        "https://mybatis.org/dtd/mybatis-3-mapper.dtd">
<mapper namespace="com.ledger.account.infrastructure.persistence.mapper.AccountMapper">
    <select id="countByStatus" resultType="long">
        SELECT count(*) FROM account.account_profile WHERE status = #{status}
    </select>
</mapper>
```

对应声明为 `long countByStatus(@Param("status") String status);`，使用 `org.apache.ibatis.annotations.Param`。参数值使用 `#{...}` 绑定；列名、排序字段等不能直接拼接外部输入。

Flyway 迁移版本在同一服务内递增；已执行的文件保留原内容，结构调整新增后续版本。服务目前分别扫描 `classpath:db/migration/account` 和 `classpath:db/migration/trading`。数据库初始化由 Flyway 完成，不由 Mapper 或实体注解完成。

## 已配置的行为

- `display_name` 等下划线列名自动映射 `displayName`。
- 主键策略为 `INPUT`，由业务显式提供。自增表或其他策略可在实体的 `@TableId(type = ...)` 上覆盖。
- 分页使用 PostgreSQL 方言，单页最多 500 条；分页插件排在乐观锁插件之后。
- 乐观锁通过实体字段 `@Version` 启用，表中对应字段需初始化版本号。调用 `updateById` 后检查受影响行数：0 表示记录已删除或版本不匹配，由业务决定如何处理。
- 不统一开启逻辑删除，不自动生成业务表，不引入通用实体父类。

金额字段使用 PostgreSQL `numeric` 和 Java `BigDecimal`。资金变更的余额约束、幂等和冲突处理仍由业务明确实现；普通 `BaseMapper.updateById` 不会自动保证这些约束。

MyBatis 会话缓存可能让同一事务内两次相同查询返回同一个 Java 对象。测试并发版本冲突时，应使用独立对象或独立事务模拟旧快照；不要把同一对象引用误当成两个并发调用者。
