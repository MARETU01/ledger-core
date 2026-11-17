package main

import (
	"path/filepath"
	"testing"

	"github.com/MARETU01/ledger-core/gateway/internal/config"
)

func TestNacosParametersFromConfig(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	cfg := config.Nacos{
		ServerAddr: "https://nacos.example:18848,http://localhost:8848",
		Namespace:  "dev", Username: "user", Password: "pass",
		TLS: config.TLS{Enabled: true, CAFile: "nacos.pem", ServerName: "nacos.example"},
	}
	param, err := nacosParameters(cfg, state)
	if err != nil {
		t.Fatal(err)
	}
	if len(param.ServerConfigs) != 2 || param.ServerConfigs[0].Scheme != "https" || param.ServerConfigs[0].IpAddr != "nacos.example" || param.ServerConfigs[0].Port != 18848 {
		t.Fatalf("unexpected Nacos servers: %+v", param.ServerConfigs)
	}
	c := param.ClientConfig
	if c.NamespaceId != "dev" || c.Username != "user" || c.Password != "pass" || c.CacheDir != filepath.Join(state, "cache") || c.LogDir != filepath.Join(state, "logs") {
		t.Fatalf("Nacos client config not applied")
	}
	if !c.TLSCfg.Enable || c.TLSCfg.CaFile != "nacos.pem" || c.TLSCfg.ServerNameOverride != "nacos.example" {
		t.Fatal("Nacos TLS config not applied")
	}
}

func TestGRPCCredentialsFromConfig(t *testing.T) {
	creds, err := grpcCredentials(config.TLS{})
	if err != nil || creds.Info().SecurityProtocol != "insecure" {
		t.Fatalf("plaintext credentials: %v", err)
	}
	creds, err = grpcCredentials(config.TLS{Enabled: true, ServerName: "backend.example"})
	if err != nil || creds.Info().SecurityProtocol != "tls" || creds.Info().ServerName != "backend.example" {
		t.Fatalf("TLS credentials: %v", err)
	}
	if _, err := grpcCredentials(config.TLS{CAFile: "ca.pem"}); err == nil {
		t.Fatal("CA without TLS accepted")
	}
	if _, err := grpcCredentials(config.TLS{Enabled: true, CAFile: filepath.Join(t.TempDir(), "missing.pem")}); err == nil {
		t.Fatal("missing CA accepted")
	}
}
