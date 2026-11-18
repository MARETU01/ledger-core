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
	if cfg.ListenAddr != "0.0.0.0:8080" || cfg.RoutesDataID != "gateway-routes.yml" || cfg.Nacos.ServerAddr != "localhost:8848" || cfg.Nacos.Namespace != "ledger-dev" || cfg.Nacos.Group != "LEDGER_GROUP" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.DescriptorDir != filepath.Dir(base) || cfg.StateDir != "" {
		t.Fatalf("paths are not relative to config file: %+v", cfg)
	}
}

func TestRuntimeSettings(t *testing.T) {
	filename := runtimeFile(t, `listen_addr: 127.0.0.1:9090
routes_data_id: custom-routes.yaml
descriptor_dir: descriptors
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
	if cfg.ListenAddr != "127.0.0.1:9090" || cfg.RoutesDataID != "custom-routes.yaml" || cfg.APIToken != "secret" {
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
		"", "listen_adrr: localhost:8080", "{}\n---\n{}", "routes_source: file", "routes_source: nacos",
		"listen_addr: ''", "routes_file: routes.yml", "routes_data_id: ''",
		"nacos_data_id: gateway-routes.yml", "static_targets: 'account-service=localhost:8081'",
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
	example, err := os.ReadFile("../../config/routes.example.yml")
	if err != nil {
		t.Fatalf("route example is missing: %v", err)
	}
	if _, err := Parse(example); err != nil {
		t.Fatalf("route example is invalid: %v", err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DescriptorDir != root {
		t.Fatalf("descriptor directory changed: got %s, want %s", cfg.DescriptorDir, root)
	}
}

func TestRuntimeEnvironment(t *testing.T) {
	t.Setenv("LEDGER_TEST_TOKEN", "secret: # value\nnext: injected\n${LITERAL}")
	t.Setenv("LEDGER_TEST_TLS", "true")
	t.Setenv("LEDGER_TEST_EMPTY", "")
	// Ensure fallback tests do not depend on the caller's environment.
	t.Setenv("LEDGER_TEST_MISSING", "temporary")
	if err := os.Unsetenv("LEDGER_TEST_MISSING"); err != nil {
		t.Fatal(err)
	}
	filename := runtimeFile(t, `listen_addr: "${LEDGER_TEST_MISSING:0.0.0.0:8080}"
routes_data_id: "${LEDGER_TEST_MISSING:custom-routes.yml}"
api_token: "${LEDGER_TEST_TOKEN}"
state_dir: "${LEDGER_TEST_MISSING:state}/cache"
grpc_tls:
  enabled: "${LEDGER_TEST_TLS:false}"
nacos:
  namespace: "${LEDGER_TEST_EMPTY:ledger-dev}"
  username: "prefix-${LEDGER_TEST_MISSING:user}-${LEDGER_TEST_EMPTY:ignored}"
  password: "${LEDGER_TEST_MISSING:}"
`)
	cfg, err := LoadRuntime(filename)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "0.0.0.0:8080" || cfg.RoutesDataID != "custom-routes.yml" || cfg.APIToken != os.Getenv("LEDGER_TEST_TOKEN") || !cfg.GRPCTLS.Enabled {
		t.Fatal("environment values or defaults were not preserved")
	}
	if cfg.Nacos.Namespace != "" || cfg.Nacos.Username != "prefix-user-" || cfg.Nacos.Password != "" {
		t.Fatal("empty values or multiple placeholders were not handled")
	}
	if cfg.StateDir != filepath.Join(filepath.Dir(filename), "state", "cache") {
		t.Fatal("expanded paths must remain relative to config file")
	}
	for _, data := range []string{
		`api_token: "${LEDGER_TEST_MISSING}"`,
		`api_token: "${LEDGER_TEST_MISSING"`,
		`api_token: "${:default}"`,
		`api_token: "${LEDGER_TEST_MISSING:${LEDGER_TEST_EMPTY}}"`,
		`grpc_tls: {enabled: "${LEDGER_TEST_MISSING:invalid}"}`,
		`unknown: "${LEDGER_TEST_MISSING:value}"`,
	} {
		if _, err := LoadRuntime(runtimeFile(t, data)); err == nil {
			t.Fatalf("invalid configuration accepted: %s", data)
		}
	}
}
