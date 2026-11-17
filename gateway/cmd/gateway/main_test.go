package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MARETU01/ledger-core/gateway/internal/config"
	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"gopkg.in/yaml.v3"
)

var javaTest = flag.Bool("java-test", false, "run integration test against Java services and Nacos")

// Opt-in process-level test against the real Java services and project Nacos.
func TestJavaGatewayHotReload(t *testing.T) {
	if !*javaTest {
		t.Skip("requires Java account/trading services and Nacos; see gateway/README.md")
	}
	binary := filepath.Join(t.TempDir(), "gateway")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build gateway: %v: %s", err, output)
	}
	cfg, err := config.LoadRuntime("../../config/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	param, err := nacosParameters(cfg.Nacos, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := clients.NewConfigClient(param)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.CloseClient()
	for _, mode := range []string{"file-static", "nacos"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			routeFile := filepath.Join(directory, "routes.yml")
			dataID := fmt.Sprintf("gateway-test-%d.yml", time.Now().UnixNano())
			group := cfg.Nacos.Group
			publish := func(data string) {
				t.Helper()
				if mode == "nacos" {
					ok, err := publisher.PublishConfig(vo.ConfigParam{DataId: dataID, Group: group, Content: data, Type: "yaml"})
					if err != nil || !ok {
						t.Fatalf("publish config: %v", err)
					}
				} else {
					// Rename models an editor's atomic file replacement.
					temporary := routeFile + ".tmp"
					if err := os.WriteFile(temporary, []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(temporary, routeFile); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "nacos" {
				defer publisher.DeleteConfig(vo.ConfigParam{DataId: dataID, Group: group})
			}
			route := func(revision, path, service, method string) string {
				return fmt.Sprintf("version: 1\nrevision: %s\nroutes:\n  - id: java\n    http_method: GET\n    http_path: %s\n    target_service: %s\n    grpc_method: %s\n    timeout: 3s\n    query_fields:\n      requestId: request_id\n", revision, path, service, method)
			}
			publish(route("first", "/api/v1/account", "account-service", "/ledger.platform.v1.AccountProbe/Check"))
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			_ = listener.Close()
			logPath := filepath.Join(directory, "gateway.log")
			log, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			source, static := "file", "account-service=127.0.0.1:19091;trading-service=127.0.0.1:19082"
			if mode == "nacos" {
				source, static = "nacos", ""
			}
			runtimeConfig := *cfg
			runtimeConfig.ListenAddr, runtimeConfig.RoutesFile = address, routeFile
			runtimeConfig.RoutesSource, runtimeConfig.StaticTargets = source, static
			runtimeConfig.NacosDataID, runtimeConfig.APIToken = dataID, ""
			runtimeConfig.StateDir = filepath.Join(directory, "state")
			settings, err := yaml.Marshal(runtimeConfig)
			if err != nil {
				t.Fatal(err)
			}
			configFile := filepath.Join(directory, "config.yaml")
			if err := os.WriteFile(configFile, settings, 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(binary, "-config", configFile)
			command.Stdout, command.Stderr = log, log
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			defer func() {
				_ = command.Process.Signal(syscall.SIGTERM)
				select {
				case err := <-done:
					if err != nil {
						output, _ := os.ReadFile(logPath)
						t.Errorf("gateway exit: %v: %s", err, output)
					}
				case <-time.After(40 * time.Second):
					_ = command.Process.Kill()
					<-done
					t.Error("gateway did not stop gracefully")
				}
			}()
			client := &http.Client{Timeout: 5 * time.Second}
			fetch := func(path string) (int, string) {
				response, err := client.Get("http://" + address + path)
				if err != nil {
					return 0, err.Error()
				}
				defer response.Body.Close()
				data, _ := io.ReadAll(response.Body)
				return response.StatusCode, string(data)
			}
			await := func(path string, code int, fragment string) {
				t.Helper()
				deadline := time.Now().Add(30 * time.Second)
				for {
					got, body := fetch(path)
					if got == code && strings.Contains(body, fragment) {
						return
					}
					if time.Now().After(deadline) {
						output, _ := os.ReadFile(logPath)
						t.Fatalf("%s: got %d %s; log: %s", path, got, body, output)
					}
					time.Sleep(100 * time.Millisecond)
				}
			}
			await("/readyz", 200, `"first"`)
			await("/api/v1/account?requestId=java-one", 200, `"requestId":"java-one"`)
			// A different service and method are exposed without rebuilding the process.
			publish(route("second", "/api/v1/trading", "trading-service", "/ledger.platform.v1.TradingProbe/Check"))
			await("/readyz", 200, `"second"`)
			await("/api/v1/trading?requestId=java-two", 200, `"downstreamService":"account-service"`)
			if code, _ := fetch("/api/v1/account"); code != 404 {
				t.Fatalf("old route retained: %d", code)
			}
			publish(route("invalid", "/api/v1/trading", "trading-service", "/ledger.platform.v1.TradingProbe/DoesNotExist"))
			deadline := time.Now().Add(20 * time.Second)
			for {
				data, _ := os.ReadFile(logPath)
				if strings.Contains(string(data), "route update rejected") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("invalid update not observed: %s", data)
				}
				time.Sleep(100 * time.Millisecond)
			}
			code, body := fetch("/readyz")
			var ready struct {
				Revision string `json:"revision"`
			}
			_ = json.Unmarshal([]byte(body), &ready)
			if code != 200 || ready.Revision != "second" {
				t.Fatalf("invalid update damaged active snapshot: %d %s", code, body)
			}
			await("/api/v1/trading?requestId=still-working", 200, "still-working")
		})
	}
}
