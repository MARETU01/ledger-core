package com.ledger.grpc.logging;

import io.grpc.Context;
import io.grpc.Metadata;
import org.slf4j.MDC;

import java.util.UUID;
import java.util.regex.Pattern;

/** Request identity follows gRPC Context; MDC is scoped to individual callbacks. */
public final class GrpcRequestContext {
    public static final Metadata.Key<String> HEADER = Metadata.Key.of("x-request-id", Metadata.ASCII_STRING_MARSHALLER);
    public static final String MDC_KEY = "requestId";
    private static final Context.Key<String> REQUEST_ID = Context.key("ledger.request-id");
    private static final Pattern VALID_ID = Pattern.compile("[A-Za-z0-9._:-]{1,128}");

    private GrpcRequestContext() { }

    public static String currentRequestId() {
        return REQUEST_ID.get();
    }

    public static String resolveRequestId(String candidate) {
        return candidate != null && VALID_ID.matcher(candidate).matches() ? candidate : UUID.randomUUID().toString();
    }

    public static Context withRequestId(String requestId) {
        return Context.current().withValue(REQUEST_ID, requestId);
    }

    public static Scope withMdc(String requestId) {
        String previous = MDC.get(MDC_KEY);
        MDC.put(MDC_KEY, requestId);
        return () -> {
            if (previous == null) {
                MDC.remove(MDC_KEY);
            } else {
                MDC.put(MDC_KEY, previous);
            }
        };
    }

    @FunctionalInterface
    public interface Scope extends AutoCloseable {
        @Override
        void close();
    }
}
