package com.ledger.grpc.server;

import com.ledger.grpc.logging.GrpcCallLog;
import com.ledger.grpc.logging.GrpcRequestContext;
import io.grpc.Context;
import io.grpc.Contexts;
import io.grpc.ForwardingServerCall;
import io.grpc.ForwardingServerCallListener;
import io.grpc.Metadata;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.grpc.Status;

/** Installed outside the exception interceptor, so completion logs see mapped statuses. */
public class GrpcRequestLoggingServerInterceptor implements ServerInterceptor {
    @Override
    public <ReqT, RespT> ServerCall.Listener<ReqT> interceptCall(
            ServerCall<ReqT, RespT> call, Metadata headers, ServerCallHandler<ReqT, RespT> next) {
        String requestId = GrpcRequestContext.resolveRequestId(headers.get(GrpcRequestContext.HEADER));
        Context context = GrpcRequestContext.withRequestId(requestId);
        var log = new GrpcCallLog("server", call.getMethodDescriptor().getFullMethodName(), requestId);
        var identifiedCall = new ForwardingServerCall.SimpleForwardingServerCall<>(call) {
            @Override
            public void sendHeaders(Metadata responseHeaders) {
                responseHeaders.discardAll(GrpcRequestContext.HEADER);
                responseHeaders.put(GrpcRequestContext.HEADER, requestId);
                super.sendHeaders(responseHeaders);
            }

            @Override
            public void close(Status status, Metadata trailers) {
                trailers.discardAll(GrpcRequestContext.HEADER);
                trailers.put(GrpcRequestContext.HEADER, requestId);
                try (var ignored = GrpcRequestContext.withMdc(requestId)) {
                    try {
                        super.close(status, trailers);
                    } finally {
                        log.complete(status);
                    }
                }
            }
        };
        ServerCall.Listener<ReqT> listener;
        try (var ignored = GrpcRequestContext.withMdc(requestId)) {
            try {
                listener = Contexts.interceptCall(context, identifiedCall, headers, next);
            } catch (RuntimeException | Error exception) {
                log.complete(Status.fromThrowable(exception));
                throw exception;
            }
        }
        return new ForwardingServerCallListener.SimpleForwardingServerCallListener<>(listener) {
            @Override
            public void onMessage(ReqT message) {
                try (var ignored = GrpcRequestContext.withMdc(requestId)) {
                    super.onMessage(message);
                }
            }

            @Override
            public void onHalfClose() {
                try (var ignored = GrpcRequestContext.withMdc(requestId)) {
                    super.onHalfClose();
                }
            }

            @Override
            public void onReady() {
                try (var ignored = GrpcRequestContext.withMdc(requestId)) {
                    super.onReady();
                }
            }

            @Override
            public void onCancel() {
                try (var ignored = GrpcRequestContext.withMdc(requestId)) {
                    try {
                        super.onCancel();
                    } finally {
                        Status status = Contexts.statusFromCancelled(context);
                        log.complete(status == null ? Status.CANCELLED : status);
                    }
                }
            }

            @Override
            public void onComplete() {
                try (var ignored = GrpcRequestContext.withMdc(requestId)) {
                    super.onComplete();
                }
            }
        };
    }
}
