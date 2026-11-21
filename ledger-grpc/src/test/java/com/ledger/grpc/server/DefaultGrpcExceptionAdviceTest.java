package com.ledger.grpc.server;

import io.grpc.Metadata;
import io.grpc.Status;
import jakarta.validation.ConstraintViolationException;
import jakarta.validation.Validation;
import jakarta.validation.constraints.NotBlank;
import org.junit.jupiter.api.Test;

import static org.assertj.core.api.Assertions.assertThat;

class DefaultGrpcExceptionAdviceTest {
    private final DefaultGrpcExceptionAdvice advice = new DefaultGrpcExceptionAdvice();

    @Test
    void inputViolationReturnsInvalidArgumentWithoutLeakingDetails() throws Exception {
        try (var factory = Validation.buildDefaultValidatorFactory()) {
            var method = Contract.class.getMethod("accept", String.class);
            var violations = factory.getValidator().forExecutables()
                    .validateParameters(new Contract(), method, new Object[]{""});
            assertThat(violations).isNotEmpty();
            var status = advice.handleConstraintViolation(new ConstraintViolationException(violations));
            assertThat(status.getCode()).isEqualTo(Status.Code.INVALID_ARGUMENT);
            assertThat(status.getDescription()).isEqualTo("Invalid request");
        }
    }

    @Test
    void returnValueViolationRemainsInternal() throws Exception {
        try (var factory = Validation.buildDefaultValidatorFactory()) {
            var method = Contract.class.getMethod("result");
            var violations = factory.getValidator().forExecutables()
                    .validateReturnValue(new Contract(), method, "");
            assertThat(violations).isNotEmpty();
            var status = advice.handleConstraintViolation(new ConstraintViolationException(violations));
            assertThat(status.getCode()).isEqualTo(Status.Code.INTERNAL);
            assertThat(status.getDescription()).isEqualTo("Internal server error");
        }
    }

    @Test
    void explicitGrpcErrorsPreserveStatusAndTrailers() {
        var trailers = new Metadata();
        trailers.put(Metadata.Key.of("error-id", Metadata.ASCII_STRING_MARSHALLER), "sample");
        var runtime = Status.NOT_FOUND.withDescription("Account not found").asRuntimeException(trailers);
        var checked = Status.PERMISSION_DENIED.asException(trailers);
        assertThat(advice.handleStatusRuntimeException(runtime)).isSameAs(runtime);
        assertThat(advice.handleStatusException(checked)).isSameAs(checked);
    }

    @Test
    void programmingErrorIsNotReportedAsInvalidInput() {
        var status = advice.handleUnexpectedException(new IllegalArgumentException("private detail"));
        assertThat(status.getCode()).isEqualTo(Status.Code.INTERNAL);
        assertThat(status.getDescription()).isEqualTo("Internal server error");
    }

    public static class Contract {
        public void accept(@NotBlank String value) { }

        @NotBlank
        public String result() { return ""; }
    }
}
