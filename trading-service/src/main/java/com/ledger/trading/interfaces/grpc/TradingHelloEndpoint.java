package com.ledger.trading.interfaces.grpc;

import com.ledger.pb.account.v1.AccountServiceGrpc;
import com.ledger.pb.trading.v1.SayHelloResponse;
import com.ledger.pb.trading.v1.SayHelloRequest;
import com.ledger.pb.trading.v1.TradingServiceGrpc;
import com.ledger.trading.infrastructure.grpc.DiscoveryGrpcChannels;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.StreamObserver;
import org.springframework.stereotype.Service;

import java.util.concurrent.TimeUnit;

@Service
public class TradingHelloEndpoint extends TradingServiceGrpc.TradingServiceImplBase {
    private final DiscoveryGrpcChannels channels;

    public TradingHelloEndpoint(DiscoveryGrpcChannels channels) {
        this.channels = channels;
    }

    @Override
    public void sayHello(SayHelloRequest request, StreamObserver<SayHelloResponse> observer) {
        try {
            // 两个领域拥有各自的消息类型，在边界处进行转换。
            var accountRequest = com.ledger.pb.account.v1.SayHelloRequest.newBuilder()
                    .setName(request.getName()).build();

            // 从 Nacos 发现账户实例，复用连接；每次调用最多等待 2 秒。
            var accountResponse = AccountServiceGrpc.newBlockingStub(channels.select("account-service"))
                    .withDeadlineAfter(2, TimeUnit.SECONDS)
                    .sayHello(accountRequest);

            observer.onNext(SayHelloResponse.newBuilder()
                    .setMessage(accountResponse.getMessage())
                    .setService("trading-service")
                    .setDownstreamService(accountResponse.getService())
                    .build());
            observer.onCompleted();
        } catch (StatusRuntimeException exception) {
            // 无实例、下游超时等状态继续交给客户端，网关会转成 HTTP 状态码。
            observer.onError(exception);
        } catch (Exception exception) {
            observer.onError(Status.INTERNAL.withDescription("Account hello failed").asRuntimeException());
        }
    }
}
