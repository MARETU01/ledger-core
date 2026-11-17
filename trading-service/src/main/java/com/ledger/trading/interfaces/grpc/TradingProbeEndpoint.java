package com.ledger.trading.interfaces.grpc;

import com.ledger.pb.platform.v1.AccountProbeGrpc;
import com.ledger.pb.platform.v1.ProbeReply;
import com.ledger.pb.platform.v1.ProbeRequest;
import com.ledger.pb.platform.v1.TradingProbeGrpc;
import com.ledger.trading.infrastructure.grpc.DiscoveryGrpcChannels;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.StreamObserver;
import org.springframework.stereotype.Service;

import java.util.concurrent.TimeUnit;

@Service
public class TradingProbeEndpoint extends TradingProbeGrpc.TradingProbeImplBase {
    private final DiscoveryGrpcChannels channels;

    public TradingProbeEndpoint(DiscoveryGrpcChannels channels) {
        this.channels = channels;
    }

    @Override
    public void check(ProbeRequest request, StreamObserver<ProbeReply> observer) {
        try {
            ProbeReply downstream = AccountProbeGrpc.newBlockingStub(channels.select("account-service"))
                    .withDeadlineAfter(2, TimeUnit.SECONDS).check(request);
            observer.onNext(ProbeReply.newBuilder().setService("trading-service")
                    .setRequestId(downstream.getRequestId()).setDownstreamService(downstream.getService()).build());
            observer.onCompleted();
        } catch (StatusRuntimeException exception) {
            observer.onError(exception);
        } catch (Exception exception) {
            observer.onError(Status.INTERNAL.withDescription("Account probe failed").asRuntimeException());
        }
    }
}
