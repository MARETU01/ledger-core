package com.ledger.integration;

import com.ledger.account.AccountApplication;
import com.ledger.contracts.platform.v1.ProbeRequest;
import com.ledger.contracts.platform.v1.TradingProbeGrpc;
import com.ledger.trading.TradingApplication;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import org.apache.kafka.clients.admin.AdminClient;
import org.apache.kafka.clients.admin.NewTopic;
import org.apache.kafka.common.TopicPartition;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.condition.EnabledIfEnvironmentVariable;
import org.springframework.cloud.client.discovery.DiscoveryClient;
import org.springframework.data.redis.core.StringRedisTemplate;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.kafka.core.ConsumerFactory;
import org.springframework.kafka.core.KafkaAdmin;
import org.springframework.kafka.core.KafkaTemplate;

import java.time.Duration;
import java.util.List;
import java.util.UUID;
import java.util.concurrent.TimeUnit;

import static org.assertj.core.api.Assertions.assertThat;

/** Opt-in: uses only the project's isolated development infrastructure. */
@EnabledIfEnvironmentVariable(named = "LEDGER_INFRA_TEST", matches = "true")
class MiddlewareIntegrationTest {
    @Test
    @SuppressWarnings("unchecked")
    void postgresRedisKafkaNacosAndGrpcWorkTogether() throws Exception {
        String[] accountSettings = infrastructureSettings("account-service", "account");
        String[] tradingSettings = infrastructureSettings("trading-service", "trading");
        try (var account = GrpcFrameworkTest.start(AccountApplication.class, "account-service", accountSettings);
             var trading = GrpcFrameworkTest.start(TradingApplication.class, "trading-service", tradingSettings)) {
            var discovery = trading.getBean(DiscoveryClient.class);
            long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(15);
            while (discovery.getInstances("account-service").isEmpty() && System.nanoTime() < deadline) {
                Thread.sleep(100);
            }
            assertThat(discovery.getInstances("account-service"))
                    .anySatisfy(instance -> {
                        assertThat(instance.getPort()).isEqualTo(GrpcFrameworkTest.port(account));
                        assertThat(instance.getMetadata()).containsEntry("protocol", "grpc");
                    });

            ManagedChannel channel = ManagedChannelBuilder.forAddress("127.0.0.1", GrpcFrameworkTest.port(trading))
                    .usePlaintext().build();
            try {
                var reply = TradingProbeGrpc.newBlockingStub(channel).withDeadlineAfter(5, TimeUnit.SECONDS)
                        .check(ProbeRequest.newBuilder().setRequestId("middleware-smoke").build());
                assertThat(reply.getDownstreamService()).isEqualTo("account-service");
                assertThat(reply.getRequestId()).isEqualTo("middleware-smoke");
            } finally {
                channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
            }

            for (var context : List.of(account, trading)) {
                var jdbc = context.getBean(JdbcTemplate.class);
                assertThat(jdbc.queryForObject("SELECT 1", Integer.class)).isEqualTo(1);
                assertThat(jdbc.queryForObject("SELECT count(*) FROM information_schema.tables "
                        + "WHERE table_schema = ? AND table_name = 'flyway_schema_history'", Integer.class,
                        context == account ? "account" : "trading")).isEqualTo(1);
            }

            String key = "ledger:smoke:" + UUID.randomUUID();
            var redis = account.getBean(StringRedisTemplate.class);
            try {
                redis.opsForValue().set(key, "ok", Duration.ofSeconds(30));
                assertThat(redis.opsForValue().get(key)).isEqualTo("ok");
            } finally {
                redis.delete(key);
            }

            String topic = "ledger-smoke-" + UUID.randomUUID();
            try (var admin = AdminClient.create(account.getBean(KafkaAdmin.class).getConfigurationProperties())) {
                admin.createTopics(List.of(new NewTopic(topic, 1, (short) 1))).all().get(15, TimeUnit.SECONDS);
                try {
                    KafkaTemplate<String, String> kafka = account.getBean(KafkaTemplate.class);
                    kafka.send(topic, "probe", "ok").get(15, TimeUnit.SECONDS);
                    ConsumerFactory<String, String> factory = trading.getBean(ConsumerFactory.class);
                    try (var consumer = factory.createConsumer("smoke-" + UUID.randomUUID(), "smoke")) {
                        consumer.assign(List.of(new TopicPartition(topic, 0)));
                        consumer.seekToBeginning(List.of(new TopicPartition(topic, 0)));
                        var records = consumer.poll(Duration.ofSeconds(15));
                        assertThat(records.count()).isEqualTo(1);
                        assertThat(records.iterator().next().value()).isEqualTo("ok");
                    }
                } finally {
                    admin.deleteTopics(List.of(topic)).all().get(15, TimeUnit.SECONDS);
                }
            }
        }
    }

    private static String[] infrastructureSettings(String service, String schema) {
        return new String[] {
                "--spring.config.import=classpath:ledger-defaults.yml,optional:nacos:" + service + ".yml?group=LEDGER_GROUP",
                "--spring.cloud.nacos.discovery.enabled=true",
                "--spring.cloud.nacos.config.enabled=true",
                "--spring.flyway.enabled=true",
                "--spring.flyway.default-schema=" + schema,
                "--spring.flyway.locations=classpath:db/migration/" + schema,
                "--spring.flyway.schemas=" + schema
        };
    }
}
