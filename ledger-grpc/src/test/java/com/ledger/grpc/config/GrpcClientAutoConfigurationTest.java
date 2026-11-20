package com.ledger.grpc.config;

import com.ledger.grpc.client.account.AccountGrpcClient;
import com.ledger.grpc.client.trading.TradingGrpcClient;
import com.ledger.grpc.discovery.DiscoveryGrpcChannels;
import org.junit.jupiter.api.Test;
import org.springframework.boot.autoconfigure.AutoConfigurations;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.cloud.client.discovery.DiscoveryClient;

import static org.assertj.core.api.Assertions.assertThat;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.verifyNoInteractions;

class GrpcClientAutoConfigurationTest {
    private final ApplicationContextRunner runner = new ApplicationContextRunner()
            .withConfiguration(AutoConfigurations.of(GrpcClientAutoConfiguration.class));

    @Test
    void protocolDependencyDoesNotActivateClients() {
        runner.run(context -> {
            assertThat(context).doesNotHaveBean(DiscoveryGrpcChannels.class);
            assertThat(context).doesNotHaveBean(AccountGrpcClient.class);
            assertThat(context).doesNotHaveBean(TradingGrpcClient.class);
        });
    }

    @Test
    void enablesOnlyRequestedClientWithoutDiscoveringOrConnecting() {
        DiscoveryClient discovery = mock(DiscoveryClient.class);
        runner.withBean(DiscoveryClient.class, () -> discovery)
                .withPropertyValues("ledger.grpc.client.enabled=true", "ledger.grpc.client.account.enabled=true",
                        "ledger.grpc.client.account.service-id=custom-account", "ledger.grpc.client.account.timeout=750ms")
                .run(context -> {
                    assertThat(context).hasSingleBean(AccountGrpcClient.class);
                    assertThat(context).doesNotHaveBean(TradingGrpcClient.class);
                    assertThat(context.getBean(GrpcClientProperties.class).getAccount().getServiceId()).isEqualTo("custom-account");
                    assertThat(context.getBean(GrpcClientProperties.class).getAccount().getTimeout()).hasMillis(750);
                    verifyNoInteractions(discovery);
                });
    }

    @Test
    void tradingClientCanBeEnabledIndependentlyAndStartupDoesNotNeedDiscovery() {
        runner.withPropertyValues("ledger.grpc.client.enabled=true", "ledger.grpc.client.trading.enabled=true")
                .run(context -> {
                    assertThat(context).hasSingleBean(TradingGrpcClient.class);
                    assertThat(context).doesNotHaveBean(AccountGrpcClient.class);
                });
    }

    @Test
    void consumerProvidedClientIsPreserved() {
        AccountGrpcClient custom = mock(AccountGrpcClient.class);
        runner.withBean(AccountGrpcClient.class, () -> custom)
                .withPropertyValues("ledger.grpc.client.enabled=true", "ledger.grpc.client.account.enabled=true")
                .run(context -> assertThat(context.getBean(AccountGrpcClient.class)).isSameAs(custom));
    }

    @Test
    void rejectsInvalidEnabledClientSettings() {
        runner.withPropertyValues("ledger.grpc.client.enabled=true", "ledger.grpc.client.account.enabled=true",
                        "ledger.grpc.client.account.timeout=0s")
                .run(context -> assertThat(context).hasFailed());
        runner.withPropertyValues("ledger.grpc.client.enabled=true", "ledger.grpc.client.trading.enabled=true",
                        "ledger.grpc.client.trading.service-id= ")
                .run(context -> assertThat(context).hasFailed());
        runner.withPropertyValues("ledger.grpc.client.enabled=true", "ledger.grpc.client.shutdown-timeout=-1s")
                .run(context -> assertThat(context).hasFailed());
    }
}
