package com.ledger.grpc.client;

import com.ledger.grpc.client.account.AccountGrpcClient;
import com.ledger.grpc.client.trading.TradingGrpcClient;
import com.ledger.grpc.discovery.DiscoveryGrpcChannels;
import com.ledger.pb.account.v1.AccountServiceGrpc;
import com.ledger.pb.account.v1.SayHelloRequest;
import com.ledger.pb.account.v1.SayHelloResponse;
import com.ledger.pb.trading.v1.TradingServiceGrpc;
import io.grpc.Context;
import io.grpc.Server;
import io.grpc.ServerBuilder;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.StreamObserver;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.cloud.client.DefaultServiceInstance;
import org.springframework.cloud.client.discovery.DiscoveryClient;

import java.time.Duration;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

class GrpcClientsTest {
    private final AtomicInteger calls = new AtomicInteger();
    private volatile CountDownLatch entered;
    private Server server;
    private DiscoveryGrpcChannels channels;
    private AccountGrpcClient account;

    @BeforeEach
    void start() throws Exception {
        server = ServerBuilder.forPort(0)
                .addService(new AccountServiceGrpc.AccountServiceImplBase() {
                    @Override
                    public void sayHello(SayHelloRequest request, StreamObserver<SayHelloResponse> observer) {
                        calls.incrementAndGet();
                        if (request.getName().equals("slow")) {
                            entered.countDown();
                            return; // Keep the RPC open until its deadline or caller cancellation.
                        }
                        if (request.getName().equals("error")) {
                            observer.onError(Status.FAILED_PRECONDITION.withDescription("account disabled").asRuntimeException());
                            return;
                        }
                        observer.onNext(SayHelloResponse.newBuilder().setMessage("Hello, " + request.getName())
                                .setService("account-service").build());
                        observer.onCompleted();
                    }
                })
                .addService(new TradingServiceGrpc.TradingServiceImplBase() {
                    @Override
                    public void sayHello(com.ledger.pb.trading.v1.SayHelloRequest request,
                                         StreamObserver<com.ledger.pb.trading.v1.SayHelloResponse> observer) {
                        observer.onNext(com.ledger.pb.trading.v1.SayHelloResponse.newBuilder()
                                .setMessage(request.getName()).setService("trading-service").build());
                        observer.onCompleted();
                    }
                }).build().start();
        DiscoveryClient discovery = mock(DiscoveryClient.class);
        var instance = new DefaultServiceInstance("backend", "custom-account", "localhost", server.getPort(), false);
        instance.getMetadata().put("protocol", "grpc");
        when(discovery.getInstances("custom-account")).thenReturn(List.of(instance));
        when(discovery.getInstances("custom-trading")).thenReturn(List.of(instance));
        channels = new DiscoveryGrpcChannels(() -> discovery, true, Duration.ofSeconds(1));
        account = new AccountGrpcClient(channels, "custom-account", Duration.ofSeconds(2));
    }

    @AfterEach
    void stop() throws Exception {
        if (channels != null) { channels.close(); }
        if (server != null) { server.shutdownNow().awaitTermination(3, TimeUnit.SECONDS); }
    }

    @Test
    void bothClientsInvokeRealGrpcServicesUsingConfiguredServiceIds() {
        var response = account.sayHello(request("Alice"));
        assertThat(response.getMessage()).isEqualTo("Hello, Alice");
        assertThat(response.getService()).isEqualTo("account-service");
        var trading = new TradingGrpcClient(channels, "custom-trading", Duration.ofSeconds(2));
        assertThat(trading.sayHello(com.ledger.pb.trading.v1.SayHelloRequest.newBuilder().setName("Bob").build())
                .getService()).isEqualTo("trading-service");
    }

    @Test
    void preservesGrpcErrorWithoutRetrying() {
        assertStatus(Status.Code.FAILED_PRECONDITION, () -> account.sayHello(request("error")));
        assertThat(calls).hasValue(1);
    }

    @Test
    void appliesDefaultAndPerCallDeadlines() {
        entered = new CountDownLatch(1);
        account.sayHello(request("warmup"));
        var shortDefault = new AccountGrpcClient(channels, "custom-account", Duration.ofMillis(100));
        assertStatus(Status.Code.DEADLINE_EXCEEDED, () -> shortDefault.sayHello(request("slow")));
        assertStatus(Status.Code.DEADLINE_EXCEEDED,
                () -> account.sayHello(request("slow"), Duration.ofMillis(100)));
        assertThatThrownBy(() -> account.sayHello(request("Alice"), Duration.ZERO))
                .isInstanceOf(IllegalArgumentException.class);
    }

    @Test
    void callerCancellationReachesBlockingCall() throws Exception {
        account.sayHello(request("warmup"));
        entered = new CountDownLatch(1);
        try (var context = Context.current().withCancellation();
             var executor = Executors.newVirtualThreadPerTaskExecutor()) {
            var result = executor.submit(context.wrap(() -> {
                try {
                    account.sayHello(request("slow"));
                    return Status.Code.OK;
                } catch (StatusRuntimeException exception) {
                    return exception.getStatus().getCode();
                }
            }));
            assertThat(entered.await(2, TimeUnit.SECONDS)).isTrue();
            context.cancel(null);
            assertThat(result.get(2, TimeUnit.SECONDS)).isEqualTo(Status.Code.CANCELLED);
        }
    }

    private static SayHelloRequest request(String name) {
        return SayHelloRequest.newBuilder().setName(name).build();
    }

    private static void assertStatus(Status.Code code, Runnable operation) {
        assertThatThrownBy(operation::run).isInstanceOfSatisfying(StatusRuntimeException.class,
                exception -> assertThat(exception.getStatus().getCode()).isEqualTo(code));
    }
}
