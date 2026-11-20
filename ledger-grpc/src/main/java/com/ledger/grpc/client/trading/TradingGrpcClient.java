package com.ledger.grpc.client.trading;

import com.ledger.grpc.client.GrpcClientSupport;
import com.ledger.grpc.discovery.DiscoveryGrpcChannels;
import com.ledger.pb.trading.v1.SayHelloRequest;
import com.ledger.pb.trading.v1.SayHelloResponse;
import com.ledger.pb.trading.v1.TradingServiceGrpc;

import java.time.Duration;

public final class TradingGrpcClient extends GrpcClientSupport {
    public TradingGrpcClient(DiscoveryGrpcChannels channels, String serviceId, Duration timeout) {
        super(channels, serviceId, timeout);
    }

    public SayHelloResponse sayHello(SayHelloRequest request) {
        return sayHello(request, null);
    }

    public SayHelloResponse sayHello(SayHelloRequest request, Duration timeout) {
        return invoke(timeout, TradingServiceGrpc::newBlockingStub, stub -> stub.sayHello(request));
    }
}
