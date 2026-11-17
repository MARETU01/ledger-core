package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runtimeFile(t *testing.T, data string) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(filename, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestRuntimeDefaultsAndPaths(t *testing.T) {
	filename := runtimeFile(t, "{}")
	cfg, err := LoadRuntime(filename)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(filename)
	if cfg.ListenAddr != "127.0.0.1:8080" || cfg.RoutesSource != "file" || cfg.Nacos.ServerAddr != "localhost:8848" || cfg.Nacos.Namespace != "ledger-dev" || cfg.Nacos.Group != "LEDGER_GROUP" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.RoutesFile != filepath.Join(base, "routes.yml") || cfg.DescriptorDir != filepath.Dir(base) || cfg.StateDir != "" {
		t.Fatalf("paths are not relative to config file: %+v", cfg)
	}
}

func TestRuntimeSettings(t *testing.T) {
	filename := runtimeFile(t, `listen_addr: 127.0.0.1:9090
routes_source: nacos
nacos_data_id: custom-routes.yaml
descriptor_dir: descriptors
static_targets: 'account-service=localhost:8081'
api_token: secret
state_dir: state
grpc_tls:
  enabled: true
  ca_file: grpc.pem
  server_name: backend.example
nacos:
  server_addr: https://nacos.example:8848
  namespace: dev-id
  group: DEV
  username: user
  password: pass
  tls:
    enabled: true
    ca_file: nacos.pem
    server_name: nacos.example
`)
	cfg, err := LoadRuntime(filename)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Dir(filename)
	if cfg.ListenAddr != "127.0.0.1:9090" || cfg.RoutesSource != "nacos" || cfg.NacosDataID != "custom-routes.yaml" || cfg.APIToken != "secret" || cfg.StaticTargets != "account-service=localhost:8081" {
		t.Fatalf("settings not loaded: %+v", cfg)
	}
	if !cfg.GRPCTLS.Enabled || cfg.GRPCTLS.CAFile != filepath.Join(base, "grpc.pem") || cfg.GRPCTLS.ServerName != "backend.example" || cfg.StateDir != filepath.Join(base, "state") {
		t.Fatalf("TLS or paths not loaded: %+v", cfg)
	}
	n := cfg.Nacos
	if n.ServerAddr != "https://nacos.example:8848" || n.Namespace != "dev-id" || n.Group != "DEV" || n.Username != "user" || n.Password != "pass" || !n.TLS.Enabled || n.TLS.CAFile != filepath.Join(base, "nacos.pem") || n.TLS.ServerName != "nacos.example" {
		t.Fatalf("Nacos settings not loaded: %+v", n)
	}
}

func TestRuntimeRejectsInvalidConfig(t *testing.T) {
	for _, data := range []string{
		"", "listen_adrr: localhost:8080", "{}\n---\n{}", "routes_source: other",
		"listen_addr: ''", "routes_file: ''", "routes_source: nacos\nnacos_data_id: ''",
		"grpc_tls: {enabled: invalid}", "grpc_tls: {ca_file: ca.pem}",
		"nacos: {unknown: value}", strings.Repeat(" ", MaxConfigBytes+1),
	} {
		t.Run(data[:min(len(data), 50)], func(t *testing.T) {
			if _, err := LoadRuntime(runtimeFile(t, data)); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
	if _, err := LoadRuntime(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("missing config accepted")
	}
}

func TestRepositoryRuntimeConfig(t *testing.T) {
	cfg, err := LoadRuntime("../../config/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.RoutesFile); err != nil {
		t.Fatalf("default route file cannot be resolved: %v", err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DescriptorDir != root {
		t.Fatalf("descriptor directory changed: got %s, want %s", cfg.DescriptorDir, root)
	}
}
