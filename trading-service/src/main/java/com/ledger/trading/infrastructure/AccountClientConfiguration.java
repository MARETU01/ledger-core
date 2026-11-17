package com.ledger.trading.infrastructure;

import com.ledger.common.DiscoveryGrpcChannels;
import org.springframework.cloud.client.discovery.DiscoveryClient;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration(proxyBeanMethods = false)
public class AccountClientConfiguration {
    @Bean(destroyMethod = "close")
    DiscoveryGrpcChannels discoveryGrpcChannels(DiscoveryClient discoveryClient) {
        return new DiscoveryGrpcChannels(discoveryClient);
    }
}
