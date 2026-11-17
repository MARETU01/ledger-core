package com.ledger.trading.infrastructure.grpc;

import io.grpc.Channel;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Status;
import org.springframework.cloud.client.ServiceInstance;
import org.springframework.cloud.client.discovery.DiscoveryClient;

import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

/** Development plaintext channels, shared per endpoint and closed with the application. */
public final class DiscoveryGrpcChannels implements AutoCloseable {
    private final DiscoveryClient discoveryClient;
    private final Map<String, ManagedChannel> channels = new ConcurrentHashMap<>();
    private final AtomicInteger sequence = new AtomicInteger();

    public DiscoveryGrpcChannels(DiscoveryClient discoveryClient) {
        this.discoveryClient = discoveryClient;
    }

    public Channel select(String serviceId) {
        List<ServiceInstance> instances = discoveryClient.getInstances(serviceId).stream()
                .filter(instance -> "grpc".equals(instance.getMetadata().get("protocol")))
                .toList();
        if (instances.isEmpty()) {
            throw Status.UNAVAILABLE.withDescription("No gRPC instances for " + serviceId).asRuntimeException();
        }
        ServiceInstance instance = instances.get(Math.floorMod(sequence.getAndIncrement(), instances.size()));
        String endpoint = instance.getHost() + ":" + instance.getPort();
        return channels.computeIfAbsent(endpoint, ignored -> ManagedChannelBuilder
                .forAddress(instance.getHost(), instance.getPort()).usePlaintext().build());
    }

    @Override
    public void close() {
        channels.values().forEach(ManagedChannel::shutdown);
        for (ManagedChannel channel : channels.values()) {
            try {
                if (!channel.awaitTermination(3, TimeUnit.SECONDS)) {
                    channel.shutdownNow();
                }
            } catch (InterruptedException exception) {
                channel.shutdownNow();
                Thread.currentThread().interrupt();
            }
        }
        channels.clear();
    }
}
