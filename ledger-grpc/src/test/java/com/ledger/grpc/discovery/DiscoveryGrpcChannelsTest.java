package com.ledger.grpc.discovery;

import io.grpc.Channel;
import io.grpc.ManagedChannel;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import org.junit.jupiter.api.Test;
import org.springframework.cloud.client.DefaultServiceInstance;
import org.springframework.cloud.client.ServiceInstance;
import org.springframework.cloud.client.discovery.DiscoveryClient;

import java.time.Duration;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.junit.jupiter.api.Assertions.assertTimeoutPreemptively;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.doReturn;
import static org.mockito.Mockito.when;

class DiscoveryGrpcChannelsTest {
    private final DiscoveryClient discovery = mock(DiscoveryClient.class);

    private ServiceInstance instance(String service, int port, String protocol) {
        var instance = new DefaultServiceInstance(service + port, service, "localhost", port, false);
        instance.getMetadata().put("protocol", protocol);
        return instance;
    }

    @Test
    void selectsPerServiceRoundRobinAndReusesConnections() {
        when(discovery.getInstances("account")).thenReturn(List.of(instance("account", 31001, "grpc"),
                instance("account", 31002, "grpc"), instance("account", 31003, "http")));
        when(discovery.getInstances("trading")).thenReturn(List.of(instance("trading", 31004, "grpc")));
        try (var channels = new DiscoveryGrpcChannels(() -> discovery, true, Duration.ofSeconds(1))) {
            Channel first = channels.execute("account", channel -> channel);
            Channel second = channels.execute("account", channel -> channel);
            channels.execute("trading", channel -> channel);
            assertThat(first.authority()).isEqualTo("localhost:31001");
            assertThat(second.authority()).isEqualTo("localhost:31002");
            assertThat(channels.<Channel>execute("account", channel -> channel)).isSameAs(first);
        }
    }

    @Test
    void emptyDiscoveryRetiresCachedAddressAndRecoveryCreatesNewConnection() {
        ServiceInstance instance = instance("account", 31001, "grpc");
        when(discovery.getInstances("account")).thenReturn(List.of(instance));
        try (var channels = new DiscoveryGrpcChannels(() -> discovery, true, Duration.ofSeconds(1))) {
            ManagedChannel old = channels.execute("account", channel -> (ManagedChannel) channel);
            when(discovery.getInstances("account")).thenReturn(List.of());
            assertUnavailable(() -> channels.execute("account", channel -> channel));
            assertThat(old.isShutdown()).isTrue();
            when(discovery.getInstances("account")).thenReturn(List.of(instance));
            assertThat(channels.<Channel>execute("account", channel -> channel)).isNotSameAs(old);
        }
    }

    @Test
    void discoveryFailurePreservesKnownConnectionWithoutCallingIt() {
        when(discovery.getInstances("account")).thenReturn(List.of(instance("account", 31001, "grpc")));
        try (var channels = new DiscoveryGrpcChannels(() -> discovery, true, Duration.ofSeconds(1))) {
            ManagedChannel old = channels.execute("account", channel -> (ManagedChannel) channel);
            when(discovery.getInstances("account")).thenThrow(new IllegalStateException("registry unavailable"));
            assertUnavailable(() -> channels.execute("account", channel -> { throw new AssertionError("must not invoke"); }));
            assertThat(old.isShutdown()).isFalse();
            doReturn(List.of(instance("account", 31001, "grpc"))).when(discovery).getInstances("account");
            assertThat(channels.<Channel>execute("account", channel -> channel)).isSameAs(old);
        }
    }

    @Test
    void removedAddressDrainsLeaseBeforeClosing() throws Exception {
        when(discovery.getInstances("account")).thenReturn(List.of(instance("account", 31001, "grpc")));
        CountDownLatch entered = new CountDownLatch(1);
        CountDownLatch finish = new CountDownLatch(1);
        try (var channels = new DiscoveryGrpcChannels(() -> discovery, true, Duration.ofSeconds(1));
             var executor = Executors.newVirtualThreadPerTaskExecutor()) {
            ManagedChannel old = channels.execute("account", channel -> (ManagedChannel) channel);
            var call = executor.submit(() -> channels.execute("account", channel -> {
                entered.countDown();
                await(finish);
                return "completed";
            }));
            try {
                assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
                when(discovery.getInstances("account")).thenReturn(List.of(instance("account", 31002, "grpc")));
                assertThat(channels.<Channel>execute("account", channel -> channel)).isNotSameAs(old);
                assertThat(old.isShutdown()).isFalse();
            } finally {
                finish.countDown();
            }
            assertThat(call.get(2, TimeUnit.SECONDS)).isEqualTo("completed");
            assertThat(old.isShutdown()).isTrue();
        }
    }

    @Test
    void closeUsesBoundedBudgetForLeasedConnectionsAndRejectsNewCalls() throws Exception {
        when(discovery.getInstances("account")).thenReturn(List.of(instance("account", 31001, "grpc")));
        var channels = new DiscoveryGrpcChannels(() -> discovery, true, Duration.ofMillis(50));
        CountDownLatch entered = new CountDownLatch(1);
        CountDownLatch finish = new CountDownLatch(1);
        try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
            ManagedChannel old = channels.execute("account", channel -> (ManagedChannel) channel);
            var call = executor.submit(() -> channels.execute("account", channel -> {
                entered.countDown();
                await(finish);
                return null;
            }));
            try {
                assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
                assertTimeoutPreemptively(Duration.ofSeconds(2), channels::close);
                assertThat(old.isShutdown()).isTrue();
                assertUnavailable(() -> channels.execute("account", channel -> channel));
            } finally {
                finish.countDown();
                channels.close();
            }
            call.get(2, TimeUnit.SECONDS);
        }
    }

    @Test
    void lateDiscoveryResultCannotRestoreRemovedAddressOrBlockOtherCalls() throws Exception {
        CountDownLatch entered = new CountDownLatch(1);
        CountDownLatch finish = new CountDownLatch(1);
        AtomicInteger lookups = new AtomicInteger();
        when(discovery.getInstances("account")).thenAnswer(ignored -> {
            if (lookups.getAndIncrement() == 0) {
                entered.countDown();
                await(finish);
                return List.of(instance("account", 31001, "grpc"));
            }
            return List.of(instance("account", 31002, "grpc"));
        });
        try (var channels = new DiscoveryGrpcChannels(() -> discovery, true, Duration.ofSeconds(1));
             var executor = Executors.newVirtualThreadPerTaskExecutor()) {
            var oldLookup = executor.submit(() -> channels.execute("account", channel -> channel));
            Channel current;
            try {
                assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
                current = executor.submit(() -> channels.execute("account", channel -> channel)).get(2, TimeUnit.SECONDS);
                assertThat(current.authority()).isEqualTo("localhost:31002");
            } finally {
                finish.countDown();
            }
            assertThat(oldLookup.get(2, TimeUnit.SECONDS)).isSameAs(current);
        }
    }

    @Test
    void shutdownDoesNotWaitForDiscoveryOrAllowLateConnections() throws Exception {
        CountDownLatch entered = new CountDownLatch(1);
        CountDownLatch finish = new CountDownLatch(1);
        when(discovery.getInstances("account")).thenAnswer(ignored -> {
            entered.countDown();
            await(finish);
            return List.of(instance("account", 31001, "grpc"));
        });
        try (var channels = new DiscoveryGrpcChannels(() -> discovery, true, Duration.ofMillis(50));
             var executor = Executors.newVirtualThreadPerTaskExecutor()) {
            var lookup = executor.submit(() -> assertUnavailable(() -> channels.execute("account", channel -> channel)));
            try {
                assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
                assertTimeoutPreemptively(Duration.ofSeconds(2), channels::close);
            } finally {
                finish.countDown();
            }
            lookup.get(2, TimeUnit.SECONDS);
        }
    }

    private static void assertUnavailable(Runnable action) {
        assertThatThrownBy(action::run).isInstanceOfSatisfying(StatusRuntimeException.class,
                exception -> assertThat(exception.getStatus().getCode()).isEqualTo(Status.Code.UNAVAILABLE));
    }

    private static void await(CountDownLatch latch) {
        try {
            if (!latch.await(5, TimeUnit.SECONDS)) {
                throw new AssertionError("Test lease was not released");
            }
        } catch (InterruptedException exception) {
            Thread.currentThread().interrupt();
            throw new AssertionError(exception);
        }
    }
}
