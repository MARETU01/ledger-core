package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MARETU01/ledger-core/gateway/internal/config"
	"github.com/MARETU01/ledger-core/gateway/internal/discovery"
	gateway "github.com/MARETU01/ledger-core/gateway/internal/gateway"
	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context) error {
	address := flag.String("listen", env("GATEWAY_LISTEN_ADDR", "127.0.0.1:8080"), "HTTP listen address")
	routeFile := flag.String("routes", env("GATEWAY_ROUTES_FILE", "config/routes.yml"), "local routing YAML file")
	routeSource := flag.String("routes-source", env("GATEWAY_ROUTES_SOURCE", "file"), "file or nacos")
	staticTargets := flag.String("static-targets", os.Getenv("GATEWAY_STATIC_TARGETS"), "service=host:port;service=host:port (empty uses Nacos)")
	descriptorDir := flag.String("descriptor-dir", env("GATEWAY_DESCRIPTOR_DIR", "."), "base directory for descriptor_set files")
	flag.Parse()
	if *routeSource != "file" && *routeSource != "nacos" {
		return fmt.Errorf("routes-source must be file or nacos")
	}
	creds, err := grpcCredentials()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var naming naming_client.INamingClient
	var remote config_client.IConfigClient
	if *staticTargets == "" || *routeSource == "nacos" {
		param, err := nacosParameters()
		if err != nil {
			return err
		}
		if *staticTargets == "" {
			naming, err = clients.NewNamingClient(param)
			if err != nil {
				return err
			}
			defer naming.CloseClient()
		}
		if *routeSource == "nacos" {
			remote, err = clients.NewConfigClient(param)
			if err != nil {
				return err
			}
			defer remote.CloseClient()
		}
	}
	var source discovery.Source
	if *staticTargets != "" {
		source, err = discovery.ParseStatic(*staticTargets)
		if err != nil {
			return err
		}
	} else {
		source = discovery.Nacos{Client: naming, Group: env("NACOS_GROUP", "LEDGER_GROUP")}
	}
	engine := gateway.New(source, creds, *descriptorDir)
	defer engine.Close()
	updates := make(chan struct{}, 1)
	notify := func() {
		select {
		case updates <- struct{}{}:
		default:
		}
	}
	var load func() ([]byte, error)
	var workers sync.WaitGroup
	if remote != nil {
		param := vo.ConfigParam{DataId: env("GATEWAY_NACOS_DATA_ID", "gateway-routes.yml"), Group: env("NACOS_GROUP", "LEDGER_GROUP")}
		load = func() ([]byte, error) { data, err := remote.GetConfig(param); return []byte(data), err }
		param.OnChange = func(_, _, _, _ string) { notify() }
		if err := remote.ListenConfig(param); err != nil {
			return err
		}
		defer remote.CancelListenConfig(param)
	} else {
		load = func() ([]byte, error) { return readRoutes(*routeFile) }
	}
	data, err := load()
	if err != nil {
		return fmt.Errorf("load initial routes: %w", err)
	}
	apply := func(data []byte) error {
		applyCtx, stop := context.WithTimeout(ctx, 30*time.Second)
		defer stop()
		return engine.Apply(applyCtx, data)
	}
	if err := apply(data); err != nil {
		return fmt.Errorf("initial routes: %w", err)
	}
	if remote == nil {
		workers.Go(func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			previous := sha256.Sum256(data)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					current, err := load()
					if err != nil {
						slog.Warn("read routes failed; retaining active routes", "error", err)
						continue
					}
					hash := sha256.Sum256(current)
					if hash != previous {
						previous = hash
						notify()
					}
				}
			}
		})
	}
	workers.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-updates:
				data, err := load()
				if err == nil {
					err = apply(data)
				}
				if err != nil {
					slog.Warn("route update rejected; retaining active routes", "error", err)
				}
			}
		}
	})
	// Stop the reload workers before closing engine or SDK clients, even on listen failure.
	defer func() { cancel(); workers.Wait() }()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"UP"}`)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		revision, ready := engine.Ready()
		w.Header().Set("Content-Type", "application/json")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ready": ready, "revision": revision})
	})
	mux.Handle("/", gateway.Middleware(engine, os.Getenv("GATEWAY_API_TOKEN")))
	server := &http.Server{Addr: *address, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		return err
	}
	slog.Info("gateway listening", "address", listener.Addr().String(), "routes_source", *routeSource)
	failed := make(chan error, 1)
	go func() { failed <- server.Serve(listener) }()
	select {
	case err = <-failed:
	case <-parent.Done():
	}
	shutdownCtx, stop := context.WithTimeout(context.Background(), 35*time.Second)
	defer stop()
	if shutdownErr := server.Shutdown(shutdownCtx); shutdownErr != nil {
		_ = server.Close()
		return shutdownErr
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func readRoutes(filename string) ([]byte, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, config.MaxConfigBytes+1))
	if len(data) > config.MaxConfigBytes {
		return nil, fmt.Errorf("route file exceeds 1 MiB")
	}
	return data, err
}

func grpcCredentials() (credentials.TransportCredentials, error) {
	enabled, err := strconv.ParseBool(env("GATEWAY_GRPC_TLS", "false"))
	if err != nil {
		return nil, err
	}
	if !enabled {
		if os.Getenv("GATEWAY_GRPC_CA_FILE") != "" {
			return nil, fmt.Errorf("GATEWAY_GRPC_CA_FILE requires GATEWAY_GRPC_TLS=true")
		}
		return insecure.NewCredentials(), nil
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if filename := os.Getenv("GATEWAY_GRPC_CA_FILE"); filename != "" {
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		if !roots.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("no CA certificates in %s", filename)
		}
	}
	return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: os.Getenv("GATEWAY_GRPC_SERVER_NAME")}), nil
}

func nacosParameters() (vo.NacosClientParam, error) {
	servers := []constant.ServerConfig{}
	for raw := range strings.SplitSeq(env("NACOS_SERVER_ADDR", "localhost:18848"), ",") {
		if !strings.Contains(raw, "://") {
			raw = "http://" + raw
		}
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return vo.NacosClientParam{}, fmt.Errorf("invalid Nacos address")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return vo.NacosClientParam{}, fmt.Errorf("Nacos scheme must be http or https")
		}
		port := uint64(8848)
		if u.Port() != "" {
			port, err = strconv.ParseUint(u.Port(), 10, 16)
			if err != nil || port == 0 {
				return vo.NacosClientParam{}, fmt.Errorf("invalid Nacos port")
			}
		}
		servers = append(servers, constant.ServerConfig{Scheme: u.Scheme, IpAddr: u.Hostname(), Port: port, ContextPath: "/nacos"})
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return vo.NacosClientParam{}, err
	}
	state := env("GATEWAY_STATE_DIR", filepath.Join(cache, "ledger-core-gateway"))
	if err := os.MkdirAll(state, 0700); err != nil {
		return vo.NacosClientParam{}, err
	}
	tlsEnabled, err := strconv.ParseBool(env("NACOS_TLS_ENABLED", "false"))
	if err != nil {
		return vo.NacosClientParam{}, err
	}
	return vo.NacosClientParam{ServerConfigs: servers, ClientConfig: &constant.ClientConfig{
		NamespaceId: os.Getenv("NACOS_NAMESPACE"), Username: os.Getenv("NACOS_USERNAME"), Password: os.Getenv("NACOS_PASSWORD"),
		TimeoutMs: 5000, NotLoadCacheAtStart: true, DisableUseSnapShot: true, UpdateCacheWhenEmpty: true,
		AppName: "ledger-gateway", LogLevel: "warn", CacheDir: filepath.Join(state, "cache"), LogDir: filepath.Join(state, "logs"),
		TLSCfg: constant.TLSConfig{Appointed: true, Enable: tlsEnabled, TrustAll: false, CaFile: os.Getenv("NACOS_CA_FILE"), ServerNameOverride: os.Getenv("NACOS_SERVER_NAME")},
	}}, nil
}
