package com.ledger.grpc.config;

import com.ledger.grpc.server.DefaultGrpcExceptionAdvice;
import io.grpc.BindableService;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnClass;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.grpc.server.advice.GrpcAdvice;

// Advice must be defined before Spring Boot evaluates its advice infrastructure condition.
@AutoConfiguration(beforeName = "org.springframework.boot.grpc.server.autoconfigure.GrpcServerAutoConfiguration")
@ConditionalOnClass(GrpcAdvice.class)
@ConditionalOnBean(BindableService.class)
@ConditionalOnProperty(name = {"spring.grpc.server.enabled", "ledger.grpc.server.exception-handling.enabled"},
        havingValue = "true", matchIfMissing = true)
public class GrpcServerExceptionAutoConfiguration {
    @Bean
    @ConditionalOnMissingBean(annotation = GrpcAdvice.class)
    DefaultGrpcExceptionAdvice defaultGrpcExceptionAdvice() {
        return new DefaultGrpcExceptionAdvice();
    }
}
