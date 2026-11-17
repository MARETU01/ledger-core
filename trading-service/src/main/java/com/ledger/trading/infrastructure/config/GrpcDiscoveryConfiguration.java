package com.ledger.trading.infrastructure.config;

import com.alibaba.cloud.nacos.NacosDiscoveryProperties;
import com.alibaba.cloud.nacos.registry.NacosAutoServiceRegistration;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.context.annotation.Configuration;
import org.springframework.context.event.EventListener;
import org.springframework.grpc.server.lifecycle.GrpcServerStartedEvent;

/** Register the actual gRPC port once the server has started. */
@Configuration(proxyBeanMethods = false)
public class GrpcDiscoveryConfiguration {
    private final ObjectProvider<NacosDiscoveryProperties> properties;
    private final ObjectProvider<NacosAutoServiceRegistration> registrations;

    public GrpcDiscoveryConfiguration(ObjectProvider<NacosDiscoveryProperties> properties,
                                      ObjectProvider<NacosAutoServiceRegistration> registrations) {
        this.properties = properties;
        this.registrations = registrations;
    }

    @EventListener
    public void onStarted(GrpcServerStartedEvent event) {
        // Resolve at event time, after auto-configuration has defined the Nacos beans.
        NacosAutoServiceRegistration registration = registrations.getIfAvailable();
        if (registration != null) {
            properties.getObject().setPort(event.getPort());
            registration.start();
        }
    }
}
