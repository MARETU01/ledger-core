package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Runtime contains startup settings. Routing rules are loaded separately.
type Runtime struct {
	ListenAddr    string `yaml:"listen_addr"`
	RoutesDataID  string `yaml:"routes_data_id"`
	DescriptorDir string `yaml:"descriptor_dir"`

	APIToken string `yaml:"api_token"`
	StateDir string `yaml:"state_dir"`
	GRPCTLS  TLS    `yaml:"grpc_tls"`
	Nacos    Nacos  `yaml:"nacos"`
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
		ListenAddr:   "0.0.0.0:8080",
		RoutesDataID: "gateway-routes.yml", DescriptorDir: "..",
		Nacos: Nacos{ServerAddr: "localhost:8848", Namespace: "ledger-dev", Group: "LEDGER_GROUP"},
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxConfigBytes {
		return nil, fmt.Errorf("gateway config exceeds 1 MiB")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := dec.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode gateway config: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("gateway config must contain exactly one YAML document")
	}
	if err := expandRuntimeEnv(&document); err != nil {
		return nil, err
	}
	data, err = yaml.Marshal(&document)
	if err != nil {
		return nil, fmt.Errorf("encode expanded gateway config: %w", err)
	}
	if len(data) > MaxConfigBytes {
		return nil, fmt.Errorf("expanded gateway config exceeds 1 MiB")
	}
	dec = yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decode gateway config: %w", err)
	}
	if cfg.ListenAddr == "" {
		return nil, fmt.Errorf("listen_addr is required")
	}
	if cfg.RoutesDataID == "" {
		return nil, fmt.Errorf("routes_data_id is required")
	}
	if cfg.GRPCTLS.CAFile != "" && !cfg.GRPCTLS.Enabled {
		return nil, fmt.Errorf("grpc_tls.ca_file requires grpc_tls.enabled: true")
	}
	base, err := filepath.Abs(filepath.Dir(filename))
	if err != nil {
		return nil, err
	}
	for _, path := range []*string{&cfg.DescriptorDir, &cfg.StateDir, &cfg.GRPCTLS.CAFile, &cfg.Nacos.TLS.CAFile} {
		if *path != "" && !filepath.IsAbs(*path) {
			*path = filepath.Join(base, *path)
		}
	}
	return &cfg, nil
}

// Expand scalar values after parsing so environment values cannot inject YAML.
func expandRuntimeEnv(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" && strings.Contains(node.Value, "${") {
		var result strings.Builder
		rest := node.Value
		for {
			start := strings.Index(rest, "${")
			if start < 0 {
				result.WriteString(rest)
				break
			}
			result.WriteString(rest[:start])
			rest = rest[start+2:]
			end := strings.IndexByte(rest, '}')
			if end < 0 {
				return fmt.Errorf("unterminated environment placeholder at line %d", node.Line)
			}
			if strings.Contains(rest[:end], "${") {
				return fmt.Errorf("nested environment placeholders are not supported (line %d)", node.Line)
			}
			name, fallback, hasDefault := strings.Cut(rest[:end], ":")
			if name == "" || strings.ContainsAny(name, "${ \t\r\n") {
				return fmt.Errorf("invalid environment placeholder at line %d", node.Line)
			}
			value, exists := os.LookupEnv(name)
			if !exists {
				if !hasDefault {
					return fmt.Errorf("environment variable %q is not set (line %d)", name, node.Line)
				}
				value = fallback
			}
			result.WriteString(value)
			if result.Len() > MaxConfigBytes {
				return fmt.Errorf("expanded gateway config exceeds 1 MiB")
			}
			rest = rest[end+1:]
		}
		node.Value = result.String()
		if node.Value == "true" || node.Value == "false" {
			node.Tag = "!!bool"
			node.Style = 0
		}
	}
	for i, child := range node.Content {
		if node.Kind == yaml.MappingNode && i%2 == 0 {
			continue
		}
		if err := expandRuntimeEnv(child); err != nil {
			return err
		}
	}
	return nil
}
