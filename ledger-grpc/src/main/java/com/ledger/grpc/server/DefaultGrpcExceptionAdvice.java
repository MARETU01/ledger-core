package com.ledger.grpc.server;

import com.ledger.grpc.logging.GrpcRequestContext;
import io.grpc.Status;
import io.grpc.StatusException;
import io.grpc.StatusRuntimeException;
import org.apache.commons.logging.Log;
import org.apache.commons.logging.LogFactory;
import org.springframework.grpc.server.advice.GrpcAdvice;
import org.springframework.grpc.server.advice.GrpcExceptionHandler;

/** Shared synchronous RPC error policy; business failures use explicit gRPC statuses. */
@GrpcAdvice
public class DefaultGrpcExceptionAdvice {
    private static final Log logger = LogFactory.getLog(DefaultGrpcExceptionAdvice.class);

    @GrpcExceptionHandler
    public StatusRuntimeException handleStatusRuntimeException(StatusRuntimeException exception) {
        // Returning the exception preserves both the status and its trailers.
        return exception;
    }

    @GrpcExceptionHandler
    public StatusException handleStatusException(StatusException exception) {
        return exception;
    }

    @GrpcExceptionHandler
    public Status handleUnexpectedException(Exception exception) {
        logger.error("Unhandled gRPC server exception request_id=" + GrpcRequestContext.currentRequestId(), exception);
        return Status.INTERNAL.withDescription("Internal server error");
    }
}
