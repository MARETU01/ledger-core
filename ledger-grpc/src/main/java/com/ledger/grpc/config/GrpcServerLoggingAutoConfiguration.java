package com.ledger.grpc.config;

import com.ledger.grpc.server.GrpcRequestLoggingServerInterceptor;
import io.grpc.BindableService;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnClass;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.context.annotation.Bean;
import org.springframework.core.annotation.Order;
import org.springframework.grpc.server.GlobalServerInterceptor;

@AutoConfiguration(beforeName = "org.springframework.boot.grpc.server.autoconfigure.GrpcServerAutoConfiguration")
@ConditionalOnClass(GlobalServerInterceptor.class)
@ConditionalOnBean(BindableService.class)
@ConditionalOnProperty(name = {"spring.grpc.server.enabled", "ledger.grpc.server.request-logging.enabled"},
        havingValue = "true", matchIfMissing = true)
public class GrpcServerLoggingAutoConfiguration {
    @Bean
    @GlobalServerInterceptor
    @Order(-100)
    @ConditionalOnMissingBean
    GrpcRequestLoggingServerInterceptor grpcRequestLoggingServerInterceptor() {
        return new GrpcRequestLoggingServerInterceptor();
    }
}
