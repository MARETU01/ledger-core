package com.ledger.grpc.client;

import com.ledger.grpc.discovery.DiscoveryGrpcChannels;
import io.grpc.Channel;
import io.grpc.Deadline;
import io.grpc.stub.AbstractStub;

import java.time.Duration;
import java.util.Objects;
import java.util.concurrent.TimeUnit;
import java.util.function.Function;

/** Shared blocking unary call policy; domain workflows remain in the consuming service. */
public abstract class GrpcClientSupport {
    private final DiscoveryGrpcChannels channels;
    private final String serviceId;
    private final Duration timeout;

    protected GrpcClientSupport(DiscoveryGrpcChannels channels, String serviceId, Duration timeout) {
        this.channels = Objects.requireNonNull(channels, "channels");
        if (serviceId == null || serviceId.isBlank()) {
            throw new IllegalArgumentException("gRPC service-id must not be blank");
        }
        this.serviceId = serviceId;
        this.timeout = positive(timeout);
    }

    protected final <S extends AbstractStub<S>, T> T invoke(
            Duration override, Function<Channel, S> stubFactory, Function<S, T> operation) {
        Duration budget = positive(override == null ? timeout : override);
        Deadline deadline = Deadline.after(budget.toNanos(), TimeUnit.NANOSECONDS);
        return channels.execute(serviceId, channel -> operation.apply(stubFactory.apply(channel).withDeadline(deadline)));
    }

    private static Duration positive(Duration value) {
        if (value == null || value.isNegative() || value.isZero()) {
            throw new IllegalArgumentException("gRPC timeout must be positive");
        }
        // Reject durations that cannot be expressed by the gRPC deadline API.
        value.toNanos();
        return value;
    }
}
