package com.ledger.integration.persistence;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import com.baomidou.mybatisplus.annotation.Version;

/** Test-only mapping; never introduces a business table or migration. */
@TableName(value = "mybatis_plus_probe", schema = "pg_temp")
public class PersistenceProbeRow {
    @TableId
    private Long id;
    private String displayName;
    @Version
    private Integer version;

    public Long getId() { return id; }
    public void setId(Long id) { this.id = id; }
    public String getDisplayName() { return displayName; }
    public void setDisplayName(String displayName) { this.displayName = displayName; }
    public Integer getVersion() { return version; }
    public void setVersion(Integer version) { this.version = version; }
}
