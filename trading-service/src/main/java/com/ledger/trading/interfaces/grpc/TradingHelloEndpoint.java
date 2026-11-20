package com.ledger.trading.interfaces.grpc;

import com.ledger.grpc.client.account.AccountGrpcClient;
import com.ledger.pb.trading.v1.SayHelloResponse;
import com.ledger.pb.trading.v1.SayHelloRequest;
import com.ledger.pb.trading.v1.TradingServiceGrpc;
import io.grpc.stub.StreamObserver;
import lombok.RequiredArgsConstructor;
import org.springframework.grpc.server.service.GrpcService;

@GrpcService
@RequiredArgsConstructor
public class TradingHelloEndpoint extends TradingServiceGrpc.TradingServiceImplBase {
    private final AccountGrpcClient accountClient;

    @Override
    public void sayHello(SayHelloRequest request, StreamObserver<SayHelloResponse> observer) {
        // 两个领域拥有各自的消息类型，在边界处进行转换。
        var accountRequest = com.ledger.pb.account.v1.SayHelloRequest.newBuilder()
                .setName(request.getName()).build();

        // 共享客户端负责服务发现、连接复用与超时；异常交给统一 gRPC advice。
        var accountResponse = accountClient.sayHello(accountRequest);

        observer.onNext(SayHelloResponse.newBuilder()
                .setMessage(accountResponse.getMessage())
                .setService("trading-service")
                .setDownstreamService(accountResponse.getService())
                .build());
        observer.onCompleted();
    }
}
