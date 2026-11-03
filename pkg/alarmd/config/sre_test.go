// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/uq"
)

func TestSREReachesTheCurrentUQ(t *testing.T) {
	first := uq.Egress{Name: "current", Tenants: []string{"tenant-fixture"}, Spaces: []string{"scope-fixture"}, Endpoints: []uq.Endpoint{{Path: "/query/ts/raw", StartField: "start_time", EndField: "end_time", TimeUnit: "s"}}}
	second := first
	second.Name, second.URL = "other", "http://other.example.test/prefix"
	original := SREConfig{UQ: SREUQConfig{Egresses: []uq.Egress{first, second}}}
	effective := original.WithUQEndpoint("http://current.example.test")
	if err := effective.Validate(); err != nil {
		t.Fatal(err)
	}
	if effective.UQ.Egresses[0].URL != "http://current.example.test" || effective.UQ.Egresses[1].URL != second.URL || original.UQ.Egresses[0].URL != "" {
		t.Fatal("current endpoint inheritance changed explicit routing or the original configuration")
	}
	if err := original.WithUQEndpoint("").Validate(); err == nil {
		t.Fatal("accepted a scope without either an explicit or current UQ endpoint")
	}
	// None registered: one egress reaching the current endpoint, for the
	// tenant and space each request names.
	if egresses := (SREConfig{}).WithUQEndpoint("http://current.example.test").UQ.Egresses; len(egresses) != 1 || egresses[0].Name != DefaultSREEgress ||
		egresses[0].URL != "http://current.example.test" || egresses[0].Tenants[0] != "*" || egresses[0].Spaces[0] != "*" || len(egresses[0].Endpoints) != len(DefaultSREEndpoints) {
		t.Fatalf("no default egress for the current endpoint: %+v", egresses)
	}
	if err := (SREConfig{}).WithUQEndpoint("http://current.example.test").Validate(); err != nil {
		t.Fatalf("the default egress does not validate: %v", err)
	}
	if egresses := (SREConfig{}).WithUQEndpoint("").UQ.Egresses; len(egresses) != 0 {
		t.Fatalf("an egress without an endpoint: %+v", egresses)
	}
	cfg := Default()
	cfg.Roles = roles.Set{roles.Channel}
	cfg.Redis.Address = "runtime.example.test:6379"
	cfg.PhaseTwo.Worker.ID = "channel-fixture"
	cfg.PhaseTwo.Access.UQEndpoint = "http://current.example.test"
	cfg.SRE = original
	if err := cfg.Validate(); err != nil {
		t.Fatalf("configuration validation did not resolve the current endpoint: %v", err)
	}
	if cfg.SRE.UQ.Egresses[0].URL != "" {
		t.Fatal("validation mutated the declared configuration")
	}
}

// A configuration rendered for an earlier build, still carrying declared Pod
// scopes, loads under strict decoding; the module label key, when given, must
// be a label key.
func TestSREConfigLoadsEarlierPodScopesAndChecksTheModuleLabel(t *testing.T) {
	var cfg Config
	decoder := yaml.NewDecoder(strings.NewReader("sre:\n  pods:\n  - namespace: tenant\n    match_labels:\n      app: web\n  module_label: example.com/module\n"))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		t.Fatalf("earlier Pod scopes refused: %v", err)
	}
	if cfg.SRE.ModuleLabel != "example.com/module" || cfg.SRE.Validate() != nil {
		t.Fatalf("module label %q", cfg.SRE.ModuleLabel)
	}
	if err := (SREConfig{ModuleLabel: "not a key"}).Validate(); err == nil {
		t.Fatal("accepted a module label that is not a label key")
	}
}
