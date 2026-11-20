package com.ledger.account.interfaces.grpc;

import com.ledger.pb.account.v1.AccountServiceGrpc;
import com.ledger.pb.account.v1.SayHelloResponse;
import com.ledger.pb.account.v1.SayHelloRequest;
import io.grpc.stub.StreamObserver;
import org.springframework.grpc.server.service.GrpcService;

/** gRPC 接口入口，职责类似 MVC 的 Controller。 */
@GrpcService
public class AccountHelloEndpoint extends AccountServiceGrpc.AccountServiceImplBase {
    @Override
    public void sayHello(SayHelloRequest request, StreamObserver<SayHelloResponse> observer) {
        String name = request.getName().strip();
        if (name.isEmpty()) {
            name = "World";
        }

        // 简单示例直接生成响应；复杂业务再交给 application 层。
        observer.onNext(SayHelloResponse.newBuilder()
                .setMessage("Hello, " + name + "!")
                .setService("account-service")
                .build());
        observer.onCompleted();
    }
}
