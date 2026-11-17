package com.ledger.account.interfaces.grpc;

import com.ledger.contracts.platform.v1.AccountProbeGrpc;
import com.ledger.contracts.platform.v1.ProbeReply;
import com.ledger.contracts.platform.v1.ProbeRequest;
import io.grpc.stub.StreamObserver;
import org.springframework.stereotype.Service;

@Service
public class AccountProbeEndpoint extends AccountProbeGrpc.AccountProbeImplBase {
    @Override
    public void check(ProbeRequest request, StreamObserver<ProbeReply> observer) {
        observer.onNext(ProbeReply.newBuilder().setService("account-service")
                .setRequestId(request.getRequestId()).build());
        observer.onCompleted();
    }
}
