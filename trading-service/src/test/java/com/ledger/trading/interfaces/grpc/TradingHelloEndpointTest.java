package com.ledger.trading.interfaces.grpc;

import com.ledger.grpc.config.GrpcClientAutoConfiguration;
import com.ledger.pb.account.v1.AccountServiceGrpc;
import com.ledger.pb.account.v1.SayHelloResponse;
import com.ledger.pb.trading.v1.SayHelloRequest;
import com.ledger.pb.trading.v1.TradingServiceGrpc;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Server;
import io.grpc.ServerBuilder;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.StreamObserver;
import org.junit.jupiter.api.Test;
import org.springframework.boot.autoconfigure.AutoConfigurations;
import org.springframework.boot.test.context.runner.ApplicationContextRunner;
import org.springframework.cloud.client.DefaultServiceInstance;
import org.springframework.cloud.client.discovery.DiscoveryClient;

import java.util.List;
import java.util.concurrent.TimeUnit;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;
import static org.mockito.Mockito.mock;
import static org.mockito.Mockito.when;

class TradingHelloEndpointTest {
    @Test
    void configuredSharedClientPreservesCrossServiceResponseAndErrors() throws Exception {
        Server account = ServerBuilder.forPort(0).addService(new AccountServiceGrpc.AccountServiceImplBase() {
            @Override
            public void sayHello(com.ledger.pb.account.v1.SayHelloRequest request,
                                 StreamObserver<SayHelloResponse> observer) {
                if (request.getName().equals("error")) {
                    observer.onError(Status.FAILED_PRECONDITION.asRuntimeException());
                    return;
                }
                observer.onNext(SayHelloResponse.newBuilder().setMessage("Hello, " + request.getName() + "!")
                        .setService("account-service").build());
                observer.onCompleted();
            }
        }).build().start();
        try {
            DiscoveryClient discovery = mock(DiscoveryClient.class);
            var instance = new DefaultServiceInstance("account-1", "account-service", "localhost", account.getPort(), false);
            instance.getMetadata().put("protocol", "grpc");
            when(discovery.getInstances("account-service")).thenReturn(List.of(instance));
            new ApplicationContextRunner()
                    .withConfiguration(AutoConfigurations.of(GrpcClientAutoConfiguration.class))
                    .withBean(DiscoveryClient.class, () -> discovery)
                    .withBean(TradingHelloEndpoint.class)
                    .withPropertyValues("ledger.grpc.client.enabled=true", "ledger.grpc.client.account.enabled=true")
                    .run(context -> {
                        assertThat(context).hasNotFailed();
                        Server trading = ServerBuilder.forPort(0).addService(context.getBean(TradingHelloEndpoint.class))
                                .build().start();
                        var channel = ManagedChannelBuilder.forAddress("localhost", trading.getPort()).usePlaintext().build();
                        try {
                            var stub = TradingServiceGrpc.newBlockingStub(channel).withDeadlineAfter(5, TimeUnit.SECONDS);
                            var result = stub.sayHello(SayHelloRequest.newBuilder().setName("Alice").build());
                            assertThat(result.getMessage()).isEqualTo("Hello, Alice!");
                            assertThat(result.getService()).isEqualTo("trading-service");
                            assertThat(result.getDownstreamService()).isEqualTo("account-service");
                            assertThatThrownBy(() -> stub.sayHello(SayHelloRequest.newBuilder().setName("error").build()))
                                    .isInstanceOfSatisfying(StatusRuntimeException.class,
                                            error -> assertThat(error.getStatus().getCode()).isEqualTo(Status.Code.FAILED_PRECONDITION));
                        } finally {
                            channel.shutdownNow().awaitTermination(3, TimeUnit.SECONDS);
                            trading.shutdownNow().awaitTermination(3, TimeUnit.SECONDS);
                        }
                    });
        } finally {
            account.shutdownNow().awaitTermination(3, TimeUnit.SECONDS);
        }
    }
}
