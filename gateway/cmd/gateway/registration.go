package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/MARETU01/ledger-core/gateway/internal/config"
	"github.com/nacos-group/nacos-sdk-go/v2/util"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

type instanceRegistry interface {
	RegisterInstance(vo.RegisterInstanceParam) (bool, error)
	DeregisterInstance(vo.DeregisterInstanceParam) (bool, error)
}

func gatewayInstance(cfg *config.Runtime, address net.Addr) (vo.RegisterInstanceParam, error) {
	host, portText, err := net.SplitHostPort(address.String())
	if err != nil {
		return vo.RegisterInstanceParam{}, fmt.Errorf("gateway listener address: %w", err)
	}
	ip := cfg.Registration.IP
	if ip == "" {
		ip = host
		if parsed, err := netip.ParseAddr(ip); err != nil || parsed.IsUnspecified() {
			ip = util.LocalIP()
		}
	}
	parsed, err := netip.ParseAddr(ip)
	if err != nil || parsed.Zone() != "" || (!parsed.IsGlobalUnicast() && !parsed.IsLoopback()) {
		return vo.RegisterInstanceParam{}, fmt.Errorf("cannot determine gateway registration IP; set registration.ip explicitly")
	}
	port := uint64(cfg.Registration.Port)
	if port == 0 {
		port, err = strconv.ParseUint(portText, 10, 16)
		if err != nil || port == 0 {
			return vo.RegisterInstanceParam{}, fmt.Errorf("invalid gateway registration port")
		}
	}
	return vo.RegisterInstanceParam{
		Ip: parsed.Unmap().String(), Port: port, ServiceName: cfg.Registration.ServiceName,
		GroupName: cfg.Nacos.Group, ClusterName: cfg.Registration.Cluster,
		Weight: 1, Enable: true, Healthy: true, Ephemeral: true,
		Metadata: map[string]string{"protocol": "http", "metrics_enabled": "false"},
	}, nil
}

func registerGateway(client instanceRegistry, param vo.RegisterInstanceParam) (func() error, error) {
	// Keep the exact instance identity, and remove any registration accepted by
	// Nacos even when its acknowledgement was lost or the SDK cached a retry.
	cleanup := sync.OnceValue(func() error {
		ok, err := client.DeregisterInstance(vo.DeregisterInstanceParam{
			Ip: param.Ip, Port: param.Port, ServiceName: param.ServiceName,
			GroupName: param.GroupName, Cluster: param.ClusterName, Ephemeral: param.Ephemeral,
		})
		if err != nil {
			return fmt.Errorf("deregister gateway: %w", err)
		}
		if !ok {
			return fmt.Errorf("gateway deregistration was rejected")
		}
		return nil
	})
	ok, err := client.RegisterInstance(param)
	if err != nil || !ok {
		if err == nil {
			err = fmt.Errorf("registration was rejected")
		}
		return nil, errors.Join(fmt.Errorf("register gateway: %w", err), cleanup())
	}
	slog.Info("gateway registered", "service", param.ServiceName, "ip", param.Ip,
		"port", param.Port, "group", param.GroupName, "cluster", param.ClusterName)
	return cleanup, nil
}

// The caller has already loaded routing and bound the HTTP listener. Keep the
// naming client alive until deregistration and request draining have finished.
func serveGateway(ctx context.Context, server *http.Server, listener net.Listener,
	client instanceRegistry, cfg *config.Runtime) error {
	defer listener.Close()
	defer server.Close()
	if ctx.Err() != nil {
		return nil
	}
	var param vo.RegisterInstanceParam
	var err error
	if cfg.Registration.Enabled {
		param, err = gatewayInstance(cfg, listener.Addr())
		if err != nil {
			return err
		}
	}
	failed := make(chan error, 1)
	go func() { failed <- server.Serve(listener) }()
	var deregister func() error
	if cfg.Registration.Enabled {
		deregister, err = registerGateway(client, param)
	}
	if err == nil {
		select {
		case err = <-failed:
		case <-ctx.Done():
		}
	}
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	// Remove the discovery target before stopping admission and draining HTTP.
	if deregister != nil {
		err = errors.Join(err, deregister())
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	return errors.Join(err, server.Shutdown(shutdownCtx))
}
