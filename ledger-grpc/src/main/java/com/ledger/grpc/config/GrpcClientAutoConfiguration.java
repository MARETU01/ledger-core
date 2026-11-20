package com.ledger.grpc.config;

import com.ledger.grpc.client.account.AccountGrpcClient;
import com.ledger.grpc.client.trading.TradingGrpcClient;
import com.ledger.grpc.discovery.DiscoveryGrpcChannels;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnProperty;
import org.springframework.boot.context.properties.EnableConfigurationProperties;
import org.springframework.cloud.client.discovery.DiscoveryClient;
import org.springframework.context.annotation.Bean;

@AutoConfiguration
@ConditionalOnProperty(prefix = "ledger.grpc.client", name = "enabled", havingValue = "true")
@EnableConfigurationProperties(GrpcClientProperties.class)
public class GrpcClientAutoConfiguration {
    @Bean(destroyMethod = "close")
    @ConditionalOnMissingBean
    DiscoveryGrpcChannels discoveryGrpcChannels(ObjectProvider<DiscoveryClient> discovery,
                                               GrpcClientProperties properties) {
        // Resolve on first use: client activation does not require an online backend,
        // and discovery auto-configuration may be registered after this configuration.
        return new DiscoveryGrpcChannels(discovery::getObject, properties.isPlaintext(),
                properties.getShutdownTimeout());
    }

    @Bean
    @ConditionalOnMissingBean
    @ConditionalOnProperty(prefix = "ledger.grpc.client.account", name = "enabled", havingValue = "true")
    AccountGrpcClient accountGrpcClient(DiscoveryGrpcChannels channels, GrpcClientProperties properties) {
        var endpoint = properties.getAccount();
        return new AccountGrpcClient(channels, endpoint.getServiceId(), endpoint.getTimeout());
    }

    @Bean
    @ConditionalOnMissingBean
    @ConditionalOnProperty(prefix = "ledger.grpc.client.trading", name = "enabled", havingValue = "true")
    TradingGrpcClient tradingGrpcClient(DiscoveryGrpcChannels channels, GrpcClientProperties properties) {
        var endpoint = properties.getTrading();
        return new TradingGrpcClient(channels, endpoint.getServiceId(), endpoint.getTimeout());
    }
}
