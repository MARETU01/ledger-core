package com.ledger.grpc.server;

import com.ledger.grpc.logging.GrpcRequestContext;
import io.grpc.Status;
import io.grpc.StatusException;
import io.grpc.StatusRuntimeException;
import jakarta.validation.ConstraintViolationException;
import jakarta.validation.ElementKind;
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
    public Status handleConstraintViolation(ConstraintViolationException exception) {
        // A return-value violation is a server contract failure, not invalid input.
        for (var violation : exception.getConstraintViolations()) {
            for (var node : violation.getPropertyPath()) {
                if (node.getKind() == ElementKind.RETURN_VALUE) {
                    return handleUnexpectedException(exception);
                }
            }
        }
        // Do not expose rejected values or validator messages to remote callers.
        return Status.INVALID_ARGUMENT.withDescription("Invalid request");
    }

    @GrpcExceptionHandler
    public Status handleUnexpectedException(Exception exception) {
        logger.error("Unhandled gRPC server exception request_id=" + GrpcRequestContext.currentRequestId(), exception);
        return Status.INTERNAL.withDescription("Internal server error");
    }
}
