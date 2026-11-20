package config

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const MaxConfigBytes = 1 << 20

type Services struct {
	Version  int                `yaml:"version"`
	Revision string             `yaml:"revision"`
	Services map[string]Service `yaml:"services"`
}

type Service struct {
	TargetService  string              `yaml:"target_service"`
	ProtoServices  map[string]string   `yaml:"proto_services"`
	GetMethods     map[string][]string `yaml:"get_methods"`
	Timeout        string              `yaml:"timeout"`
	DescriptorSets map[string]string   `yaml:"descriptor_sets"`
}

var aliasName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var versionName = regexp.MustCompile(`^v[1-9][0-9]*$`)
var serviceName = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
var protoServiceName = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*\.)+[A-Za-z_][A-Za-z0-9_]*$`)
var MethodName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func Parse(data []byte) (*Services, error) {
	if len(data) > MaxConfigBytes {
		return nil, fmt.Errorf("service configuration exceeds 1 MiB")
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var cfg Services
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
	if len(cfg.Services) == 0 || len(cfg.Services) > 256 {
		return nil, fmt.Errorf("expected 1..256 services")
	}
	for alias, svc := range cfg.Services {
		if !aliasName.MatchString(alias) || !serviceName.MatchString(svc.TargetService) {
			return nil, fmt.Errorf("invalid service alias or target: %s", alias)
		}
		if len(svc.ProtoServices) == 0 || len(svc.ProtoServices) > 32 {
			return nil, fmt.Errorf("service %s: expected 1..32 versions", alias)
		}
		for version, name := range svc.ProtoServices {
			if !versionName.MatchString(version) || !protoServiceName.MatchString(name) {
				return nil, fmt.Errorf("service %s: invalid version or protobuf service", alias)
			}
		}
		for version, methods := range svc.GetMethods {
			if _, ok := svc.ProtoServices[version]; !ok {
				return nil, fmt.Errorf("service %s: GET version is undeclared", alias)
			}
			seen := map[string]bool{}
			if len(methods) > 256 {
				return nil, fmt.Errorf("too many GET methods")
			}
			for _, method := range methods {
				if !MethodName.MatchString(method) || seen[method] {
					return nil, fmt.Errorf("invalid or duplicate GET method %q", method)
				}
				seen[method] = true
			}
		}
		for version, filename := range svc.DescriptorSets {
			if _, ok := svc.ProtoServices[version]; !ok || strings.TrimSpace(filename) == "" {
				return nil, fmt.Errorf("service %s: invalid descriptor version or filename", alias)
			}
		}
		if svc.Timeout == "" {
			svc.Timeout = "2s"
		}
		d, err := time.ParseDuration(svc.Timeout)
		if err != nil || d <= 0 || d > 30*time.Second {
			return nil, fmt.Errorf("service %s: timeout must be >0 and <=30s", alias)
		}
		cfg.Services[alias] = svc
	}
	return &cfg, nil
}
