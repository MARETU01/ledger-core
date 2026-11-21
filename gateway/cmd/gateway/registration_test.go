package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MARETU01/ledger-core/gateway/internal/config"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

type registryStub struct {
	register   func(vo.RegisterInstanceParam) (bool, error)
	deregister func(vo.DeregisterInstanceParam) (bool, error)
}

func (s registryStub) RegisterInstance(p vo.RegisterInstanceParam) (bool, error) {
	return s.register(p)
}

func (s registryStub) DeregisterInstance(p vo.DeregisterInstanceParam) (bool, error) {
	return s.deregister(p)
}

func registrationConfig() *config.Runtime {
	return &config.Runtime{
		Nacos:        config.Nacos{Group: "LEDGER_GROUP"},
		Registration: config.Registration{Enabled: true, ServiceName: "ledger-gateway", Cluster: "DEFAULT"},
	}
}

func TestGatewayInstanceAddress(t *testing.T) {
	cfg := registrationConfig()
	param, err := gatewayInstance(cfg, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 43210})
	if err != nil {
		t.Fatal(err)
	}
	if param.Ip != "127.0.0.1" || param.Port != 43210 || param.ServiceName != "ledger-gateway" ||
		param.GroupName != "LEDGER_GROUP" || param.ClusterName != "DEFAULT" || !param.Ephemeral ||
		!param.Enable || !param.Healthy || param.Weight <= 0 || param.Metadata["protocol"] != "http" ||
		param.Metadata["metrics_enabled"] != "false" {
		t.Fatalf("unexpected gateway registration: %+v", param)
	}
	cfg.Registration.IP, cfg.Registration.Port = "2001:db8::10", 18080
	param, err = gatewayInstance(cfg, &net.TCPAddr{IP: net.IPv4zero, Port: 43210})
	if err != nil || param.Ip != "2001:db8::10" || param.Port != 18080 {
		t.Fatalf("advertised address override not applied: %+v, %v", param, err)
	}
	for _, ip := range []string{"0.0.0.0", "::", "invalid", "224.0.0.1", "fe80::1%eth0", "169.254.1.1", "255.255.255.255"} {
		cfg.Registration.IP = ip
		if _, err := gatewayInstance(cfg, &net.TCPAddr{IP: net.IPv4zero, Port: 43210}); err == nil {
			t.Fatalf("invalid advertised IP accepted: %s", ip)
		}
	}
}

func TestRegistrationCleanupPreservesIdentityAndRunsOnce(t *testing.T) {
	param, err := gatewayInstance(registrationConfig(), &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 43210})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client := registryStub{
		register: func(p vo.RegisterInstanceParam) (bool, error) { return true, nil },
		deregister: func(p vo.DeregisterInstanceParam) (bool, error) {
			calls++
			if p.Ip != param.Ip || p.Port != param.Port || p.ServiceName != param.ServiceName ||
				p.GroupName != param.GroupName || p.Cluster != param.ClusterName || !p.Ephemeral {
				t.Errorf("deregistration identity changed: %+v", p)
			}
			return true, nil
		},
	}
	cleanup, err := registerGateway(client, param)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("cleanup called %d times", calls)
	}
}

func TestGatewayServingAndRegistrationLifecycle(t *testing.T) {
	for _, outcome := range []string{"success", "rejected", "error", "deregister-error", "disabled"} {
		t.Run(outcome, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			address := listener.Addr().String()
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, "ready")
			})}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			checkHTTP := func() {
				httpClient := &http.Client{Timeout: time.Second}
				response, err := httpClient.Get("http://" + address + "/readyz")
				if err != nil {
					t.Errorf("HTTP server unavailable: %v", err)
					return
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK {
					t.Errorf("HTTP server not ready")
				}
			}
			registered, deregistered := 0, 0
			client := registryStub{
				register: func(p vo.RegisterInstanceParam) (bool, error) {
					registered++
					checkHTTP()
					cancel()
					if outcome == "error" {
						return false, errors.New("registration unavailable")
					}
					return outcome != "rejected", nil
				},
				deregister: func(p vo.DeregisterInstanceParam) (bool, error) {
					deregistered++
					// Deregistration happens before stopping HTTP admission/draining.
					checkHTTP()
					if outcome == "deregister-error" {
						return false, errors.New("deregistration unavailable")
					}
					return true, nil
				},
			}
			cfg := registrationConfig()
			if outcome == "disabled" {
				cfg.Registration.Enabled = false
				go func() {
					checkHTTP()
					cancel()
				}()
			}
			err = serveGateway(ctx, server, listener, client, cfg)
			wantError := outcome != "success" && outcome != "disabled"
			if (err != nil) != wantError {
				t.Fatalf("unexpected result: %v", err)
			}
			if outcome == "disabled" {
				if registered != 0 || deregistered != 0 {
					t.Fatal("disabled registration called Nacos")
				}
			} else if registered != 1 || deregistered != 1 {
				t.Fatalf("lifecycle calls: register=%d deregister=%d", registered, deregistered)
			}
			if outcome == "rejected" && !strings.Contains(err.Error(), "rejected") {
				t.Fatal(err)
			}
			connection, err := net.DialTimeout("tcp", address, time.Second)
			if err == nil {
				connection.Close()
				t.Fatal("listener left open after shutdown")
			}
		})
	}
}

func TestCanceledStartupDoesNotRegister(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A nil registry must never be touched after startup cancellation.
	if err := serveGateway(ctx, &http.Server{}, listener, nil, registrationConfig()); err != nil {
		t.Fatal(err)
	}
}
