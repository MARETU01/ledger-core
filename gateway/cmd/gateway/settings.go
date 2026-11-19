package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MARETU01/ledger-core/gateway/internal/config"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

func grpcCredentials(cfg config.TLS) (credentials.TransportCredentials, error) {
	if !cfg.Enabled {
		if cfg.CAFile != "" {
			return nil, fmt.Errorf("grpc_tls.ca_file requires grpc_tls.enabled: true")
		}
		return insecure.NewCredentials(), nil
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if filename := cfg.CAFile; filename != "" {
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		if !roots.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("no CA certificates in %s", filename)
		}
	}
	return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: cfg.ServerName}), nil
}

func nacosParameters(cfg config.Nacos, state string) (vo.NacosClientParam, error) {
	servers := []constant.ServerConfig{}
	for raw := range strings.SplitSeq(cfg.ServerAddr, ",") {
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
	if state == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return vo.NacosClientParam{}, err
		}
		state = filepath.Join(cache, "ledger-core-gateway")
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		return vo.NacosClientParam{}, err
	}
	return vo.NacosClientParam{ServerConfigs: servers, ClientConfig: &constant.ClientConfig{
		NamespaceId: cfg.Namespace, Username: cfg.Username, Password: cfg.Password,
		TimeoutMs: 5000, NotLoadCacheAtStart: true, DisableUseSnapShot: true, UpdateCacheWhenEmpty: true,
		AppName: "ledger-gateway", LogLevel: "warn", CacheDir: filepath.Join(state, "cache"), LogDir: filepath.Join(state, "logs"),
		TLSCfg: constant.TLSConfig{Appointed: true, Enable: cfg.TLS.Enabled, TrustAll: false, CaFile: cfg.TLS.CAFile, ServerNameOverride: cfg.TLS.ServerName},
	}}, nil
}
