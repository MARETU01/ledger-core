package com.ledger.trading.interfaces.grpc;

import com.ledger.grpc.client.account.AccountGrpcClient;
import com.ledger.pb.trading.v1.SayHelloResponse;
import com.ledger.pb.trading.v1.SayHelloRequest;
import com.ledger.pb.trading.v1.TradingServiceGrpc;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.StreamObserver;
import org.springframework.stereotype.Service;

@Service
public class TradingHelloEndpoint extends TradingServiceGrpc.TradingServiceImplBase {
    private final AccountGrpcClient accountClient;

    public TradingHelloEndpoint(AccountGrpcClient accountClient) {
        this.accountClient = accountClient;
    }

    @Override
    public void sayHello(SayHelloRequest request, StreamObserver<SayHelloResponse> observer) {
        try {
            // 两个领域拥有各自的消息类型，在边界处进行转换。
            var accountRequest = com.ledger.pb.account.v1.SayHelloRequest.newBuilder()
                    .setName(request.getName()).build();

            // 共享客户端负责服务发现、连接复用与可配置的调用超时。
            var accountResponse = accountClient.sayHello(accountRequest);

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
