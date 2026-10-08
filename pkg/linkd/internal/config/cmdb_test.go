// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBluekingConfigurationValidationAndRedaction(t *testing.T) {
	valid := BluekingConfig{APIURL: "https://apigw.example", AppCode: "linkd", AppSecret: "synthetic-value"}
	if err := (BluekingConfig{}).Validate(false); err != nil {
		t.Fatal(err)
	}
	if err := (BluekingConfig{}).Validate(true); err == nil {
		t.Fatal("CMDB accepted absent application")
	}
	for _, bad := range []BluekingConfig{{APIURL: valid.APIURL}, {AppCode: valid.AppCode}, {APIURL: valid.APIURL, AppCode: "linkd", AppSecret: redactedSecret}, {APIURL: valid.APIURL + "?", AppCode: "linkd", AppSecret: "value"}} {
		if err := bad.Validate(false); err == nil {
			t.Fatal("invalid application configuration accepted")
		}
	}
	cfg := Default()
	cfg.Blueking = valid
	cfg.Resources.CMDB = &CMDBResource{}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	redacted := cfg.Redacted()
	raw, err := json.Marshal(redacted)
	if err != nil || strings.Contains(string(raw), valid.AppSecret) || cfg.Blueking.AppSecret != valid.AppSecret {
		t.Fatal("redaction leaked or changed original", err)
	}
	for _, url := range []string{"https://user:secret@host", "https://host?token=secret", "https://host#private", "https://host:99999", "https://host/a/../b"} {
		if err := (CMDBResource{BaseURL: url}).Validate(); err == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
}

func TestBluekingLoadRejectsOldCMDBConfiguration(t *testing.T) {
	for _, fields := range []string{"mode: apigw", "identities: []"} {
		path := filepath.Join(t.TempDir(), "linkd.yaml")
		if err := os.WriteFile(path, []byte("resources:\n  cmdb:\n    "+fields+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path, Overrides{}); err == nil {
			t.Fatal("legacy CMDB fields accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "linkd.yaml")
	raw := []byte("blueking:\n  api_url: https://apigw.example\n  app_code: linkd\n  app_secret: synthetic-value\nresources:\n  cmdb: {}\n")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path, Overrides{})
	if err != nil || got.Blueking.EnableMultiTenantMode || got.Resources.CMDB == nil || got.Blueking.AppCode != "linkd" {
		t.Fatal("global config lost or default mode changed", err)
	}
	raw = append([]byte("blueking:\n  enable_multi_tenant_mode: true\n"), []byte("  api_url: https://apigw.example\n  app_code: linkd\n  app_secret: synthetic-value\nresources:\n  cmdb: {}\n")...)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	got, err = Load(path, Overrides{})
	if err != nil || !got.Blueking.EnableMultiTenantMode {
		t.Fatal("multi-tenant mode lost", err)
	}
}
