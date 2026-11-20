package com.ledger.grpc.logging;

import io.grpc.Status;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;

/** Exactly one bounded completion record per intercepted call, including cancellation. */
public final class GrpcCallLog {
    private static final Logger logger = LoggerFactory.getLogger(GrpcCallLog.class);
    private static final String FORMAT = "gRPC call side={} method={} request_id={} status={} duration_ms={}";
    private final String side;
    private final String method;
    private final String requestId;
    private final long started = System.nanoTime();
    private final AtomicBoolean completed = new AtomicBoolean();

    public GrpcCallLog(String side, String method, String requestId) {
        this.side = side;
        this.method = method;
        this.requestId = requestId;
    }

    public void complete(Status status) {
        if (!completed.compareAndSet(false, true)) {
            return;
        }
        long millis = TimeUnit.NANOSECONDS.toMillis(System.nanoTime() - started);
        if (status.isOk() || status.getCode() == Status.Code.CANCELLED) {
            logger.info(FORMAT, side, method, requestId, status.getCode(), millis);
        } else {
            logger.warn(FORMAT, side, method, requestId, status.getCode(), millis);
        }
    }
}
