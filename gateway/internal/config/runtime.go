package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Runtime contains startup settings. Routing rules are loaded separately.
type Runtime struct {
	ListenAddr    string `yaml:"listen_addr"`
	RoutesSource  string `yaml:"routes_source"`
	RoutesFile    string `yaml:"routes_file"`
	NacosDataID   string `yaml:"nacos_data_id"`
	DescriptorDir string `yaml:"descriptor_dir"`
	StaticTargets string `yaml:"static_targets"`
	APIToken      string `yaml:"api_token"`
	StateDir      string `yaml:"state_dir"`
	GRPCTLS       TLS    `yaml:"grpc_tls"`
	Nacos         Nacos  `yaml:"nacos"`
}

type TLS struct {
	Enabled    bool   `yaml:"enabled"`
	CAFile     string `yaml:"ca_file"`
	ServerName string `yaml:"server_name"`
}

type Nacos struct {
	ServerAddr string `yaml:"server_addr"`
	Namespace  string `yaml:"namespace"`
	Group      string `yaml:"group"`
	Username   string `yaml:"username"`
	Password   string `yaml:"password"`
	TLS        TLS    `yaml:"tls"`
}

func LoadRuntime(filename string) (*Runtime, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open gateway config: %w", err)
	}
	defer f.Close()
	cfg := Runtime{
		ListenAddr: "127.0.0.1:8080", RoutesSource: "file", RoutesFile: "routes.yml",
		NacosDataID: "gateway-routes.yml", DescriptorDir: "..",
		Nacos: Nacos{ServerAddr: "localhost:18848", Group: "LEDGER_GROUP"},
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxConfigBytes {
		return nil, fmt.Errorf("gateway config exceeds 1 MiB")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode gateway config: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("gateway config must contain exactly one YAML document")
	}
	if cfg.ListenAddr == "" || (cfg.RoutesSource != "file" && cfg.RoutesSource != "nacos") {
		return nil, fmt.Errorf("listen_addr is required and routes_source must be file or nacos")
	}
	if cfg.RoutesSource == "file" && cfg.RoutesFile == "" {
		return nil, fmt.Errorf("routes_file is required for file routes")
	}
	if cfg.RoutesSource == "nacos" && cfg.NacosDataID == "" {
		return nil, fmt.Errorf("nacos_data_id is required for nacos routes")
	}
	if cfg.GRPCTLS.CAFile != "" && !cfg.GRPCTLS.Enabled {
		return nil, fmt.Errorf("grpc_tls.ca_file requires grpc_tls.enabled: true")
	}
	base, err := filepath.Abs(filepath.Dir(filename))
	if err != nil {
		return nil, err
	}
	for _, path := range []*string{&cfg.RoutesFile, &cfg.DescriptorDir, &cfg.StateDir, &cfg.GRPCTLS.CAFile, &cfg.Nacos.TLS.CAFile} {
		if *path != "" && !filepath.IsAbs(*path) {
			*path = filepath.Join(base, *path)
		}
	}
	return &cfg, nil
}
