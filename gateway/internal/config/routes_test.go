package config

import (
	"os"
	"strings"
	"testing"
)

func TestServiceConfiguration(t *testing.T) {
	data, err := os.ReadFile("../../config/routes.example.yml")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Services["account"].ProtoServices["v1"] != "ledger.account.v1.AccountService" {
		t.Fatal("wrong account mapping")
	}
	valid := "version: 1\nrevision: test\nservices:\n  account:\n    target_service: account-service\n    proto_services:\n      v1: ledger.account.v1.AccountService\n"
	cfg, err = Parse([]byte(valid))
	if err != nil || cfg.Services["account"].Timeout != "2s" {
		t.Fatalf("default timeout: %v", err)
	}
	for _, bad := range []string{
		"version: 1\nrevision: old\nroutes: []", valid + "    unknown: true\n", valid + "---\n{}",
		strings.ReplaceAll(valid, "account:\n", "Account:\n"),
		strings.ReplaceAll(valid, "v1:", "v0:"),
		strings.ReplaceAll(valid, "ledger.account.v1.AccountService", "invalid/service"),
		valid + "    timeout: 31s\n", valid + "    get_methods:\n      v2: [SayHello]\n",
		valid + "    get_methods:\n      v1: [SayHello, SayHello]\n",
		valid + "    descriptor_sets:\n      v2: missing.pb\n",
		valid + "    descriptor_sets:\n      v1: ''\n",
		strings.Repeat("x", MaxConfigBytes+1),
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Fatalf("invalid configuration accepted: %.120s", bad)
		}
	}
}
