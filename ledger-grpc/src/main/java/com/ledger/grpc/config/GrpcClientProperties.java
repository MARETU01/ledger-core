package com.ledger.grpc.config;

import lombok.Getter;
import lombok.Setter;
import org.springframework.boot.context.properties.ConfigurationProperties;

import java.time.Duration;

/** Client activation and transport settings belong to the consuming application. */
@Getter
@Setter
@ConfigurationProperties("ledger.grpc.client")
public class GrpcClientProperties {
    private boolean enabled;
    private boolean plaintext = true;
    private Duration shutdownTimeout = Duration.ofSeconds(3);
    private final Endpoint account = new Endpoint("account-service");
    private final Endpoint trading = new Endpoint("trading-service");

    @Getter
    @Setter
    public static class Endpoint {
        private boolean enabled;
        private String serviceId;
        private Duration timeout = Duration.ofSeconds(2);

        public Endpoint(String serviceId) { this.serviceId = serviceId; }
    }
}
