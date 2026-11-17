package com.ledger.integration;

import com.ledger.account.AccountApplication;
import com.ledger.contracts.platform.v1.ProbeRequest;
import com.ledger.contracts.platform.v1.TradingProbeGrpc;
import com.ledger.trading.TradingApplication;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import org.junit.jupiter.api.Test;
import org.springframework.boot.builder.SpringApplicationBuilder;
import org.springframework.context.ConfigurableApplicationContext;
import org.springframework.grpc.server.lifecycle.GrpcServerLifecycle;

import java.util.concurrent.TimeUnit;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

class GrpcFrameworkTest {
    static ConfigurableApplicationContext start(Class<?> application, String name, String... extra) {
        String[] common = {
                "--spring.config.name=ledger-test",
                "--spring.config.import=classpath:ledger-defaults.yml",
                "--spring.application.name=" + name,
                "--spring.grpc.server.port=0",
                "--spring.cloud.nacos.discovery.enabled=false",
                "--spring.cloud.nacos.config.enabled=false",
                "--spring.cloud.nacos.config.import-check.enabled=false",
                "--spring.flyway.enabled=false"
        };
        var arguments = new java.util.LinkedHashMap<String, String>();
        for (String argument : java.util.stream.Stream.concat(java.util.Arrays.stream(common),
                java.util.Arrays.stream(extra)).toList()) {
            arguments.put(argument.substring(0, argument.indexOf('=')), argument);
        }
        return new SpringApplicationBuilder(application).run(arguments.values().toArray(String[]::new));
    }

    static int port(ConfigurableApplicationContext context) {
        return context.getBean(GrpcServerLifecycle.class).getPort();
    }

    @Test
    void tradingCallsAccountOverRealGrpcWithDiscovery() throws Exception {
        try (var account = start(AccountApplication.class, "account-service");
             var trading = start(TradingApplication.class, "trading-service",
                     "--spring.cloud.discovery.client.simple.instances.account-service[0].uri=http://127.0.0.1:" + port(account),
                     "--spring.cloud.discovery.client.simple.instances.account-service[0].metadata.protocol=grpc")) {
            ManagedChannel channel = ManagedChannelBuilder.forAddress("127.0.0.1", port(trading)).usePlaintext().build();
            try {
                var reply = TradingProbeGrpc.newBlockingStub(channel).withDeadlineAfter(5, TimeUnit.SECONDS)
                        .check(ProbeRequest.newBuilder().setRequestId("framework-smoke").build());
                assertThat(reply.getService()).isEqualTo("trading-service");
                assertThat(reply.getDownstreamService()).isEqualTo("account-service");
                assertThat(reply.getRequestId()).isEqualTo("framework-smoke");
            } finally {
                channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
            }
        }
    }

    @Test
    void absentAccountIsReportedAsUnavailable() throws Exception {
        try (var trading = start(TradingApplication.class, "trading-service")) {
            ManagedChannel channel = ManagedChannelBuilder.forAddress("127.0.0.1", port(trading)).usePlaintext().build();
            try {
                assertThatThrownBy(() -> TradingProbeGrpc.newBlockingStub(channel)
                        .withDeadlineAfter(5, TimeUnit.SECONDS).check(ProbeRequest.getDefaultInstance()))
                        .isInstanceOfSatisfying(StatusRuntimeException.class,
                                exception -> assertThat(exception.getStatus().getCode()).isEqualTo(Status.Code.UNAVAILABLE));
            } finally {
                channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
            }
        }
    }
}
