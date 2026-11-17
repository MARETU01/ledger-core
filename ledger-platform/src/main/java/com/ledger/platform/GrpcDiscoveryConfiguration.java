package com.ledger.platform;

import com.alibaba.cloud.nacos.NacosDiscoveryProperties;
import com.alibaba.cloud.nacos.registry.NacosAutoServiceRegistration;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnBean;
import org.springframework.context.annotation.Bean;
import org.springframework.context.event.EventListener;
import org.springframework.grpc.server.lifecycle.GrpcServerStartedEvent;

/** Nacos's HTTP server event never fires in a gRPC-only application. */
@AutoConfiguration(afterName = "com.alibaba.cloud.nacos.registry.NacosServiceRegistryAutoConfiguration")
public class GrpcDiscoveryConfiguration {
    @Bean
    @ConditionalOnBean(NacosAutoServiceRegistration.class)
    GrpcNacosRegistration grpcNacosRegistration(NacosDiscoveryProperties properties,
                                               NacosAutoServiceRegistration registration) {
        return new GrpcNacosRegistration(properties, registration);
    }

    public static final class GrpcNacosRegistration {
        private final NacosDiscoveryProperties properties;
        private final NacosAutoServiceRegistration registration;

        GrpcNacosRegistration(NacosDiscoveryProperties properties, NacosAutoServiceRegistration registration) {
            this.properties = properties;
            this.registration = registration;
        }

        @EventListener
        public void onStarted(GrpcServerStartedEvent event) {
            properties.setPort(event.getPort());
            registration.start();
        }
    }
}
