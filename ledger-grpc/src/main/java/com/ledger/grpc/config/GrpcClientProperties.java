package com.ledger.grpc.config;

import org.springframework.boot.context.properties.ConfigurationProperties;

import java.time.Duration;

/** Client activation and transport settings belong to the consuming application. */
@ConfigurationProperties("ledger.grpc.client")
public class GrpcClientProperties {
    private boolean enabled;
    private boolean plaintext = true;
    private Duration shutdownTimeout = Duration.ofSeconds(3);
    private final Endpoint account = new Endpoint("account-service");
    private final Endpoint trading = new Endpoint("trading-service");

    public boolean isEnabled() { return enabled; }
    public void setEnabled(boolean enabled) { this.enabled = enabled; }
    public boolean isPlaintext() { return plaintext; }
    public void setPlaintext(boolean plaintext) { this.plaintext = plaintext; }
    public Duration getShutdownTimeout() { return shutdownTimeout; }
    public void setShutdownTimeout(Duration shutdownTimeout) { this.shutdownTimeout = shutdownTimeout; }
    public Endpoint getAccount() { return account; }
    public Endpoint getTrading() { return trading; }

    public static class Endpoint {
        private boolean enabled;
        private String serviceId;
        private Duration timeout = Duration.ofSeconds(2);

        public Endpoint(String serviceId) { this.serviceId = serviceId; }
        public boolean isEnabled() { return enabled; }
        public void setEnabled(boolean enabled) { this.enabled = enabled; }
        public String getServiceId() { return serviceId; }
        public void setServiceId(String serviceId) { this.serviceId = serviceId; }
        public Duration getTimeout() { return timeout; }
        public void setTimeout(Duration timeout) { this.timeout = timeout; }
    }
}
