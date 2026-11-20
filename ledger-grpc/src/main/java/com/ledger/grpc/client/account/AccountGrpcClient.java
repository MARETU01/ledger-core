package com.ledger.grpc.client.account;

import com.ledger.grpc.client.GrpcClientSupport;
import com.ledger.grpc.discovery.DiscoveryGrpcChannels;
import com.ledger.pb.account.v1.AccountServiceGrpc;
import com.ledger.pb.account.v1.SayHelloRequest;
import com.ledger.pb.account.v1.SayHelloResponse;

import java.time.Duration;

public final class AccountGrpcClient extends GrpcClientSupport {
    public AccountGrpcClient(DiscoveryGrpcChannels channels, String serviceId, Duration timeout) {
        super(channels, serviceId, timeout);
    }

    public SayHelloResponse sayHello(SayHelloRequest request) {
        return sayHello(request, null);
    }

    public SayHelloResponse sayHello(SayHelloRequest request, Duration timeout) {
        return invoke(timeout, AccountServiceGrpc::newBlockingStub, stub -> stub.sayHello(request));
    }
}
