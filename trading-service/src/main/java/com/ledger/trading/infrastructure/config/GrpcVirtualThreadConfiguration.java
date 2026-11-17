package com.ledger.trading.infrastructure.config;

import org.springframework.boot.autoconfigure.condition.ConditionalOnThreading;
import org.springframework.boot.grpc.server.autoconfigure.GrpcServerExecutorProvider;
import org.springframework.boot.thread.Threading;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/** Dispatch gRPC service callbacks on virtual threads when enabled for the application. */
@Configuration(proxyBeanMethods = false)
@ConditionalOnThreading(Threading.VIRTUAL)
public class GrpcVirtualThreadConfiguration {
    @Bean(destroyMethod = "close")
    ExecutorService grpcVirtualThreadExecutor() {
        return Executors.newThreadPerTaskExecutor(
                Thread.ofVirtual().name("grpc-virtual-", 0).factory());
    }

    @Bean
    GrpcServerExecutorProvider grpcServerExecutorProvider(ExecutorService grpcVirtualThreadExecutor) {
        return () -> grpcVirtualThreadExecutor;
    }
}
