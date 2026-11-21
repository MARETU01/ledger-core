package com.ledger.grpc.config;

import jakarta.validation.Valid;
import jakarta.validation.constraints.AssertTrue;
import lombok.Getter;
import lombok.Setter;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.validation.annotation.Validated;

import java.time.Duration;

/** Client activation and transport settings belong to the consuming application. */
@Getter
@Setter
@ConfigurationProperties("ledger.grpc.client")
@Validated
public class GrpcClientProperties {
    private boolean enabled;
    private boolean plaintext = true;
    private Duration shutdownTimeout = Duration.ofSeconds(3);
    @Valid
    private final Endpoint account = new Endpoint("account-service");
    @Valid
    private final Endpoint trading = new Endpoint("trading-service");

    @AssertTrue(message = "shutdown-timeout must be positive and fit in a signed 64-bit nanosecond value")
    public boolean isShutdownTimeoutValid() {
        return validDuration(shutdownTimeout);
    }

    private static boolean validDuration(Duration value) {
        if (value == null || value.isNegative() || value.isZero()) {
            return false;
        }
        try {
            value.toNanos();
            return true;
        } catch (ArithmeticException exception) {
            return false;
        }
    }

    @Getter
    @Setter
    public static class Endpoint {
        private boolean enabled;
        private String serviceId;
        private Duration timeout = Duration.ofSeconds(2);

        public Endpoint(String serviceId) { this.serviceId = serviceId; }

        @AssertTrue(message = "service-id must not be blank when the endpoint is enabled")
        public boolean isServiceIdValid() {
            return !enabled || (serviceId != null && !serviceId.isBlank());
        }

        @AssertTrue(message = "timeout must be positive and fit in a signed 64-bit nanosecond value when the endpoint is enabled")
        public boolean isTimeoutValid() {
            return !enabled || validDuration(timeout);
        }
    }
}
