package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/MARETU01/ledger-core/gateway/internal/config"
	"github.com/MARETU01/ledger-core/gateway/internal/discovery"
	gateway "github.com/MARETU01/ledger-core/gateway/internal/gateway"
	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

func run(parent context.Context) error {
	configFile := flag.String("config", "config/config.yaml", "gateway startup YAML file")
	flag.Parse()
	cfg, err := config.LoadRuntime(*configFile)
	if err != nil {
		return err
	}
	creds, err := grpcCredentials(cfg.GRPCTLS)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	param, err := nacosParameters(cfg.Nacos, cfg.StateDir)
	if err != nil {
		return err
	}
	remote, err := clients.NewConfigClient(param)
	if err != nil {
		return err
	}
	defer remote.CloseClient()
	naming, err := clients.NewNamingClient(param)
	if err != nil {
		return err
	}
	defer naming.CloseClient()
	source := discovery.Nacos{Client: naming, Group: cfg.Nacos.Group}
	engine := gateway.New(source, creds, cfg.DescriptorDir)
	defer engine.Close()
	updates := make(chan struct{}, 1)
	notify := func() {
		select {
		case updates <- struct{}{}:
		default:
		}
	}
	routeParam := vo.ConfigParam{DataId: cfg.RoutesDataID, Group: cfg.Nacos.Group}
	load := func() ([]byte, error) {
		data, err := remote.GetConfig(routeParam)
		return []byte(data), err
	}
	routeParam.OnChange = func(_, _, _, _ string) { notify() }
	if err := remote.ListenConfig(routeParam); err != nil {
		return err
	}
	defer remote.CancelListenConfig(routeParam)
	var workers sync.WaitGroup
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
	mux.Handle("/", gateway.Middleware(engine, cfg.APIToken))
	server := &http.Server{Addr: cfg.ListenAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return err
	}
	slog.Info("gateway listening", "address", listener.Addr().String(), "routes_data_id", cfg.RoutesDataID)
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
