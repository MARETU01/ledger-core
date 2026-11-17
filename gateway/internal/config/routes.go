package config

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const MaxConfigBytes = 1 << 20

type Routes struct {
	Version  int     `yaml:"version"`
	Revision string  `yaml:"revision"`
	Routes   []Route `yaml:"routes"`
}

type Route struct {
	ID            string            `yaml:"id"`
	HTTPMethod    string            `yaml:"http_method"`
	HTTPPath      string            `yaml:"http_path"`
	Service       string            `yaml:"target_service"`
	GRPCMethod    string            `yaml:"grpc_method"`
	Timeout       string            `yaml:"timeout"`
	DescriptorSet string            `yaml:"descriptor_set"`
	PathFields    map[string]string `yaml:"path_fields"`
	QueryFields   map[string]string `yaml:"query_fields"`
}

var serviceName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var methodName = regexp.MustCompile(`^/([A-Za-z_][A-Za-z0-9_]*\.)*[A-Za-z_][A-Za-z0-9_]*/[A-Za-z_][A-Za-z0-9_]*$`)

func Parse(data []byte) (*Routes, error) {
	if len(data) > MaxConfigBytes {
		return nil, fmt.Errorf("route configuration exceeds 1 MiB")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var cfg Routes
	if err := dec.Decode(&cfg); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one YAML document")
	}
	if cfg.Version != 1 || strings.TrimSpace(cfg.Revision) == "" {
		return nil, fmt.Errorf("version: 1 and nonempty revision are required")
	}
	if len(cfg.Routes) == 0 || len(cfg.Routes) > 256 {
		return nil, fmt.Errorf("expected 1..256 routes")
	}
	ids := map[string]bool{}
	patterns := map[string]bool{}
	for i := range cfg.Routes {
		r := &cfg.Routes[i]
		if r.ID == "" || ids[r.ID] {
			return nil, fmt.Errorf("missing or duplicate route ID %q", r.ID)
		}
		ids[r.ID] = true
		switch r.HTTPMethod {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return nil, fmt.Errorf("route %s: unsupported HTTP method", r.ID)
		}
		if !strings.HasPrefix(r.HTTPPath, "/api/") || strings.ContainsAny(r.HTTPPath, " ?#\t\r\n") {
			return nil, fmt.Errorf("route %s: path must start with /api/ and contain no query or whitespace", r.ID)
		}
		if !serviceName.MatchString(r.Service) || !methodName.MatchString(r.GRPCMethod) {
			return nil, fmt.Errorf("route %s: invalid target service or full RPC method", r.ID)
		}
		if r.Timeout == "" {
			r.Timeout = "2s"
		}
		d, err := time.ParseDuration(r.Timeout)
		if err != nil || d <= 0 || d > 30*time.Second {
			return nil, fmt.Errorf("route %s: timeout must be >0 and <=30s", r.ID)
		}
		pattern := r.HTTPMethod + " " + r.HTTPPath
		if patterns[pattern] {
			return nil, fmt.Errorf("duplicate HTTP route %s", pattern)
		}
		patterns[pattern] = true
	}
	return &cfg, nil
}
