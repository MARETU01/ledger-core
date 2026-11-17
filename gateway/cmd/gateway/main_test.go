package main

import (
	"encoding/json"
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

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

// Opt-in process-level test against the real Java services and project Nacos.
func TestJavaGatewayHotReload(t *testing.T) {
	if os.Getenv("GATEWAY_JAVA_TEST") != "true" {
		t.Skip("requires Java account/trading services and Nacos; see gateway/README.md")
	}
	binary := filepath.Join(t.TempDir(), "gateway")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build gateway: %v: %s", err, output)
	}
	t.Setenv("GATEWAY_STATE_DIR", t.TempDir())
	param, err := nacosParameters()
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
			group := env("NACOS_GROUP", "LEDGER_GROUP")
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
			command := exec.Command(binary, "-listen", address, "-routes", routeFile)
			source, static := "file", "account-service=127.0.0.1:19091;trading-service=127.0.0.1:19082"
			if mode == "nacos" {
				source, static = "nacos", ""
			}
			command.Env = append(os.Environ(), "GATEWAY_ROUTES_SOURCE="+source, "GATEWAY_NACOS_DATA_ID="+dataID, "GATEWAY_STATIC_TARGETS="+static, "GATEWAY_STATE_DIR="+filepath.Join(directory, "state"), "GATEWAY_API_TOKEN=")
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
