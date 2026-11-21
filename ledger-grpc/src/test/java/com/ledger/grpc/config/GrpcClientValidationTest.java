package com.ledger.grpc.config;

import com.ledger.grpc.client.account.AccountGrpcClient;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.params.ParameterizedTest;
import org.junit.jupiter.params.provider.ValueSource;
import org.springframework.boot.autoconfigure.AutoConfigurations;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.boot.validation.autoconfigure.ValidationAutoConfiguration;

import static org.assertj.core.api.Assertions.assertThat;

class GrpcClientValidationTest {
    private final ApplicationContextRunner runner = new ApplicationContextRunner()
            .withConfiguration(AutoConfigurations.of(
                    ValidationAutoConfiguration.class, GrpcClientAutoConfiguration.class));

    @Test
    void validDefaultsCreateClientWithoutConnectingToDiscovery() {
        runner.withPropertyValues("ledger.grpc.client.enabled=true",
                        "ledger.grpc.client.account.enabled=true")
                .run(context -> {
                    assertThat(context).hasNotFailed().hasSingleBean(AccountGrpcClient.class);
                    assertThat(context.getBean(GrpcClientProperties.class).getAccount().getServiceId())
                            .isEqualTo("account-service");
                });
    }

    @ParameterizedTest
    @ValueSource(strings = {
            "shutdown-timeout=0s", "shutdown-timeout=-1s", "shutdown-timeout=999999999999s",
            "account.timeout=0s", "account.timeout=-1s", "account.timeout=999999999999s",
            "account.service-id=", "trading.timeout=0s", "trading.service-id= "
    })
    void invalidActiveConfigurationFailsDuringBinding(String property) {
        runner.withPropertyValues("ledger.grpc.client.enabled=true",
                        "ledger.grpc.client.account.enabled=true",
                        "ledger.grpc.client.trading.enabled=true",
                        "ledger.grpc.client." + property)
                .run(context -> {
                    assertThat(context).hasFailed();
                    assertThat(context.getStartupFailure()).hasStackTraceContaining("must");
                });
    }

    @Test
    void disabledEndpointDoesNotRequireValidTransportSettings() {
        runner.withPropertyValues("ledger.grpc.client.enabled=true",
                        "ledger.grpc.client.account.enabled=false",
                        "ledger.grpc.client.account.service-id=",
                        "ledger.grpc.client.account.timeout=0s")
                .run(context -> assertThat(context).hasNotFailed().doesNotHaveBean(AccountGrpcClient.class));
    }

    @Test
    void disabledClientDoesNotBindUnusedSettings() {
        runner.withPropertyValues("ledger.grpc.client.enabled=false",
                        "ledger.grpc.client.shutdown-timeout=0s")
                .run(context -> assertThat(context).hasNotFailed().doesNotHaveBean(GrpcClientProperties.class));
    }
}
