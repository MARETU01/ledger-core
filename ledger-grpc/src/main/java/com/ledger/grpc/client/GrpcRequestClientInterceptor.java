package com.ledger.grpc.client;

import com.ledger.grpc.logging.GrpcCallLog;
import com.ledger.grpc.logging.GrpcRequestContext;
import io.grpc.CallOptions;
import io.grpc.Channel;
import io.grpc.ClientCall;
import io.grpc.ClientInterceptor;
import io.grpc.Context;
import io.grpc.ForwardingClientCall;
import io.grpc.ForwardingClientCallListener;
import io.grpc.Metadata;
import io.grpc.MethodDescriptor;
import io.grpc.Status;

/** Propagates request identity without copying caller identity or arbitrary metadata. */
public final class GrpcRequestClientInterceptor implements ClientInterceptor {
    @Override
    public <ReqT, RespT> ClientCall<ReqT, RespT> interceptCall(
            MethodDescriptor<ReqT, RespT> method, CallOptions options, Channel next) {
        String requestId = GrpcRequestContext.resolveRequestId(GrpcRequestContext.currentRequestId());
        var log = new GrpcCallLog("client", method.getFullMethodName(), requestId);
        Context context = GrpcRequestContext.withRequestId(requestId);
        ClientCall<ReqT, RespT> delegate;
        Context previous = context.attach();
        try (var ignored = GrpcRequestContext.withMdc(requestId)) {
            try {
                delegate = next.newCall(method, options);
            } catch (RuntimeException | Error exception) {
                log.complete(Status.fromThrowable(exception));
                throw exception;
            }
        } finally {
            context.detach(previous);
        }
        return new ForwardingClientCall.SimpleForwardingClientCall<>(delegate) {
            @Override
            public void start(Listener<RespT> listener, Metadata headers) {
                headers.discardAll(GrpcRequestContext.HEADER);
                headers.put(GrpcRequestContext.HEADER, requestId);
                var contextualListener = new ForwardingClientCallListener.SimpleForwardingClientCallListener<>(listener) {
                    @Override
                    public void onHeaders(Metadata responseHeaders) {
                        try (var ignored = GrpcRequestContext.withMdc(requestId)) {
                            super.onHeaders(responseHeaders);
                        }
                    }

                    @Override
                    public void onMessage(RespT message) {
                        try (var ignored = GrpcRequestContext.withMdc(requestId)) {
                            super.onMessage(message);
                        }
                    }

                    @Override
                    public void onReady() {
                        try (var ignored = GrpcRequestContext.withMdc(requestId)) {
                            super.onReady();
                        }
                    }

                    @Override
                    public void onClose(Status status, Metadata trailers) {
                        try (var ignored = GrpcRequestContext.withMdc(requestId)) {
                            log.complete(status);
                            super.onClose(status, trailers);
                        }
                    }
                };
                try (var ignored = GrpcRequestContext.withMdc(requestId)) {
                    try {
                        super.start(contextualListener, headers);
                    } catch (RuntimeException | Error exception) {
                        log.complete(Status.fromThrowable(exception));
                        throw exception;
                    }
                }
            }
        };
    }
}
