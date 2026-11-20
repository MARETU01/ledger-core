package com.ledger.grpc.discovery;

import io.grpc.Channel;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Status;
import org.springframework.cloud.client.ServiceInstance;
import org.springframework.cloud.client.discovery.DiscoveryClient;

import java.time.Duration;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.HashSet;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Set;
import java.util.concurrent.TimeUnit;
import java.util.function.Function;
import java.util.function.Supplier;

/** Per-service round robin with leases that protect calls while obsolete channels drain. */
public final class DiscoveryGrpcChannels implements AutoCloseable {
    private final Supplier<DiscoveryClient> discovery;
    private final boolean plaintext;
    private final long shutdownNanos;
    private final Map<String, Pool> pools = new HashMap<>();
    private final Set<Entry> entries = new HashSet<>();
    private boolean closed;

    public DiscoveryGrpcChannels(Supplier<DiscoveryClient> discovery, boolean plaintext, Duration shutdownTimeout) {
        this.discovery = Objects.requireNonNull(discovery, "discovery");
        this.plaintext = plaintext;
        if (shutdownTimeout == null || shutdownTimeout.isNegative() || shutdownTimeout.isZero()) {
            throw new IllegalArgumentException("gRPC shutdown-timeout must be positive");
        }
        this.shutdownNanos = shutdownTimeout.toNanos();
    }

    /** Execute a complete blocking call while holding a lease; do not return asynchronous work. */
    public <T> T execute(String serviceId, Function<Channel, T> operation) {
        Entry entry = acquire(serviceId);
        try {
            return operation.apply(entry.channel);
        } finally {
            release(entry);
        }
    }

    private Entry acquire(String serviceId) {
        Pool pool;
        long refresh;
        synchronized (this) {
            ensureOpen();
            pool = pools.computeIfAbsent(serviceId, ignored -> new Pool());
            refresh = ++pool.requestedRefresh;
        }
        // Never hold the shared lock while invoking a discovery implementation.
        // A slow registry lookup must not block unrelated services or shutdown.
        Set<Address> instances = new LinkedHashSet<>();
        try {
            for (ServiceInstance instance : discovery.get().getInstances(serviceId)) {
                if ("grpc".equals(instance.getMetadata().get("protocol"))) {
                    instances.add(new Address(instance.getHost(), instance.getPort()));
                }
            }
        } catch (RuntimeException exception) {
            // Discovery errors do not imply that every previously known instance was removed.
            throw Status.UNAVAILABLE.withDescription("gRPC discovery failed for " + serviceId)
                    .withCause(exception).asRuntimeException();
        }
        synchronized (this) {
            ensureOpen();
            // Concurrent lookups may return out of order. An older result must
            // not restore an address removed by a newer successful lookup.
            if (refresh > pool.appliedRefresh) {
                pool.channels.entrySet().removeIf(existing -> {
                    if (instances.contains(existing.getKey())) {
                        return false;
                    }
                    retire(existing.getValue());
                    return true;
                });
                pool.addresses = List.copyOf(instances);
                pool.appliedRefresh = refresh;
            }
            entries.removeIf(entry -> entry.retired && entry.channel.isTerminated());
            if (pool.addresses.isEmpty()) {
                throw Status.UNAVAILABLE.withDescription("No gRPC instances for " + serviceId).asRuntimeException();
            }
            Address address = pool.addresses.get(Math.floorMod(pool.sequence++, pool.addresses.size()));
            Entry entry = pool.channels.computeIfAbsent(address, selected -> {
                var builder = ManagedChannelBuilder.forAddress(selected.host(), selected.port()).disableRetry();
                if (plaintext) {
                    builder.usePlaintext();
                } else {
                    builder.useTransportSecurity();
                }
                Entry created = new Entry(builder.build());
                entries.add(created);
                return created;
            });
            entry.leases++;
            return entry;
        }
    }

    private void ensureOpen() {
        if (closed) {
            throw Status.UNAVAILABLE.withDescription("gRPC client is closed").asRuntimeException();
        }
    }

    private synchronized void release(Entry entry) {
        entry.leases--;
        if (entry.retired && entry.leases == 0) {
            entry.channel.shutdown();
        }
    }

    private void retire(Entry entry) {
        entry.retired = true;
        if (entry.leases == 0) {
            entry.channel.shutdown();
        }
    }

    @Override
    public void close() {
        ArrayList<Entry> pending;
        synchronized (this) {
            if (closed) {
                return;
            }
            closed = true;
            pending = new ArrayList<>(entries);
            pending.forEach(this::retire);
            pools.clear();
        }
        // A single timeout bounds the whole shutdown, regardless of the number of addresses.
        long start = System.nanoTime();
        try {
            for (Entry entry : pending) {
                long remaining = shutdownNanos - (System.nanoTime() - start);
                if (remaining <= 0 || !entry.channel.awaitTermination(remaining, TimeUnit.NANOSECONDS)) {
                    break;
                }
            }
        } catch (InterruptedException exception) {
            Thread.currentThread().interrupt();
        } finally {
            pending.forEach(entry -> {
                if (!entry.channel.isTerminated()) {
                    entry.channel.shutdownNow();
                }
            });
            synchronized (this) {
                entries.clear();
            }
        }
    }

    private record Address(String host, int port) { }

    private static final class Pool {
        final Map<Address, Entry> channels = new HashMap<>();
        List<Address> addresses = List.of();
        int sequence;
        long requestedRefresh;
        long appliedRefresh;
    }

    private static final class Entry {
        final ManagedChannel channel;
        int leases;
        boolean retired;

        Entry(ManagedChannel channel) { this.channel = channel; }
    }
}
