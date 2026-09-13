// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

func TestLoadTemporaryLegacyDrainingCleanupRequestIsStrict(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
	}{
		{name: "unknown field", content: `{"unknown":true}`},
		{name: "trailing value", content: `{}` + "\n" + `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "request.json")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadTemporaryLegacyDrainingCleanupRequest(path); err == nil || !strings.Contains(err.Error(), "decode") {
				t.Fatalf("loadTemporaryLegacyDrainingCleanupRequest(%s) error=%v", test.name, err)
			}
		})
	}
}

func TestTemporaryLegacyDrainingRuntimeScopeDigestIsCanonicalAndCredentialFree(t *testing.T) {
	base := config.RedisConnectionConfig{
		Mode: config.RedisModeSentinel, SentinelAddress: []string{"sentinel-b:26379", "sentinel-a:26379"},
		MasterName: "monitor-master", Username: "worker-a", Password: "secret-a",
		SentinelUsername: "sentinel-a", SentinelPassword: "sentinel-secret-a", DB: 8,
		DialTimeout: config.Duration(time.Second), ReadTimeout: config.Duration(time.Second),
		WriteTimeout: config.Duration(time.Second), PoolSize: 2,
	}
	want, err := temporaryLegacyDrainingRuntimeScopeDigest(base, "alarmd:runtime")
	if err != nil || len(want) != 64 {
		t.Fatalf("temporaryLegacyDrainingRuntimeScopeDigest(base)=(%q,%v)", want, err)
	}
	reorderedWithOtherCredentials := base
	reorderedWithOtherCredentials.SentinelAddress = []string{"sentinel-a:26379", "sentinel-b:26379"}
	reorderedWithOtherCredentials.Username = "worker-b"
	reorderedWithOtherCredentials.Password = "secret-b"
	reorderedWithOtherCredentials.SentinelUsername = "sentinel-b"
	reorderedWithOtherCredentials.SentinelPassword = "sentinel-secret-b"
	got, err := temporaryLegacyDrainingRuntimeScopeDigest(reorderedWithOtherCredentials, "alarmd:runtime")
	if err != nil || got != want {
		t.Fatalf("canonical credential-free digest=(%q,%v), want %q", got, err, want)
	}

	for _, test := range []struct {
		name       string
		connection config.RedisConnectionConfig
		prefix     string
	}{
		{name: "DB", connection: func() config.RedisConnectionConfig { value := base; value.DB = 9; return value }(), prefix: "alarmd:runtime"},
		{name: "master", connection: func() config.RedisConnectionConfig { value := base; value.MasterName = "other-master"; return value }(), prefix: "alarmd:runtime"},
		{name: "prefix", connection: base, prefix: "other:runtime"},
		{name: "standalone", connection: config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: "redis:6379", DB: 8}, prefix: "alarmd:runtime"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := temporaryLegacyDrainingRuntimeScopeDigest(test.connection, test.prefix)
			if err != nil || got == want {
				t.Fatalf("temporaryLegacyDrainingRuntimeScopeDigest(%s)=(%q,%v), want distinct digest", test.name, got, err)
			}
		})
	}
}

func TestTemporaryLegacyDrainingCleanupPlanOutputContainsScopeDigestWithoutCredentials(t *testing.T) {
	plan := controlplane.TemporaryLegacyDrainingCleanupPlan{
		RuntimeScopeDigest: strings.Repeat("d", 64),
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if !strings.Contains(text, `"runtime_scope_digest"`) || strings.Contains(text, "username") || strings.Contains(text, "password") {
		t.Fatalf("temporary cleanup plan output exposes unexpected scope fields: %s", text)
	}
}
