package com.ledger.integration;

import com.baomidou.mybatisplus.core.toolkit.Wrappers;
import com.baomidou.mybatisplus.extension.plugins.pagination.Page;
import com.ledger.integration.persistence.PersistenceProbeMapper;
import com.ledger.integration.persistence.PersistenceProbeRow;
import org.apache.ibatis.annotations.Mapper;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.mybatis.spring.annotation.MapperScan;
import org.springframework.boot.autoconfigure.EnableAutoConfiguration;
import org.springframework.context.annotation.Configuration;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

import static org.assertj.core.api.Assertions.assertThat;

@EnabledIfEnvironmentVariable(named = "LEDGER_INFRA_TEST", matches = "true")
class MybatisPlusPostgresTest {
    @Test
    void baseMapperSupportsCrudPaginationAndOptimisticLocking() {
        try (var context = GrpcFrameworkTest.start(TestApplication.class, "mybatis-plus-test",
                "--spring.grpc.server.enabled=false")) {
            var jdbc = context.getBean(JdbcTemplate.class);
            var mapper = context.getBean(PersistenceProbeMapper.class);
            var transaction = new TransactionTemplate(context.getBean(PlatformTransactionManager.class));
            transaction.executeWithoutResult(status -> {
                // JDBC and MyBatis must share the same transaction/connection to see this table.
                jdbc.execute("CREATE TEMP TABLE mybatis_plus_probe (id bigint PRIMARY KEY, "
                        + "display_name varchar(100) NOT NULL, version integer NOT NULL) ON COMMIT DROP");
                for (long id = 1; id <= 2; id++) {
                    var row = new PersistenceProbeRow();
                    row.setId(id);
                    row.setDisplayName("probe-" + id);
                    row.setVersion(0);
                    assertThat(mapper.insert(row)).isEqualTo(1);
                }

                var page = mapper.selectPage(new Page<>(1, 1), Wrappers.<PersistenceProbeRow>lambdaQuery()
                        .orderByAsc(PersistenceProbeRow::getId));
                assertThat(page.getTotal()).isEqualTo(2);
                assertThat(page.getRecords()).singleElement().satisfies(row -> {
                    assertThat(row.getId()).isEqualTo(1L);
                    assertThat(row.getDisplayName()).isEqualTo("probe-1");
                });
                var cappedPage = mapper.selectPage(new Page<>(1, 1000), Wrappers.emptyWrapper());
                assertThat(cappedPage.getSize()).isEqualTo(500);

                var current = mapper.selectById(1L);
                // A second select in this session may return the same cached Java object.
                // Detach a snapshot to model a different caller holding an older version.
                var stale = new PersistenceProbeRow();
                stale.setId(current.getId());
                stale.setDisplayName(current.getDisplayName());
                stale.setVersion(current.getVersion());
                current.setDisplayName("updated");
                assertThat(mapper.updateById(current)).isEqualTo(1);
                assertThat(current.getVersion()).isEqualTo(1);
                stale.setDisplayName("stale");
                assertThat(mapper.updateById(stale)).isZero();
                assertThat(mapper.selectOne(Wrappers.<PersistenceProbeRow>lambdaQuery()
                        .eq(PersistenceProbeRow::getDisplayName, "updated")).getVersion()).isEqualTo(1);

                assertThat(mapper.deleteById(2L)).isEqualTo(1);
                assertThat(mapper.selectCount(Wrappers.emptyWrapper())).isEqualTo(1);
                status.setRollbackOnly();
            });
        }
    }

    @Configuration(proxyBeanMethods = false)
    @EnableAutoConfiguration
    @MapperScan(basePackageClasses = PersistenceProbeMapper.class, annotationClass = Mapper.class)
    static class TestApplication {
    }
}
