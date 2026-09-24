// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestTargetGroupConfiguration(t *testing.T) {
	cfg := validGoAccessConfigObject()
	if _, configured := cfg.TargetGroupRedis(); configured {
		t.Fatal("absent prefix must disable group reads")
	}
	prefix := "groups:"
	cfg.PlatformCache.DynamicGroupKeyPrefix = &prefix
	// The CMDB cache must live somewhere other than the state Redis here,
	// or "preserve the CMDB location" and "fall back to the state Redis"
	// read the same address and this assertion cannot tell them apart.
	cmdb := RedisConnectionConfig{Mode: RedisModeStandalone, Address: "cmdb-cache:6379", DB: 5}
	cfg.PlatformCache.CMDB = &cmdb
	legacy, configured := cfg.TargetGroupRedis()
	if !configured || !reflect.DeepEqual(legacy, cfg.CMDBCacheRedis()) || legacy.Address != cmdb.Address || legacy.DB != cmdb.DB {
		t.Fatalf("prefix-only configuration must preserve the CMDB location, got %+v", legacy)
	}
	if legacy.Address == cfg.Redis.Address {
		t.Fatal("fixture: CMDB and state Redis must differ for this assertion to discriminate")
	}
	cfg.PlatformCache.CMDB = nil
	connection := RedisConnectionConfig{Mode: RedisModeStandalone, Address: "groups:6379", DB: 3}
	cfg.PlatformCache.TargetGroup = &connection
	cfg.resolvePlatformCacheRedis()
	resolved, _ := cfg.TargetGroupRedis()
	if resolved.Address != connection.Address || resolved.DB != 3 || resolved.DialTimeout != cfg.Redis.DialTimeout || resolved.ReadTimeout != cfg.Redis.ReadTimeout || resolved.WriteTimeout != cfg.Redis.WriteTimeout {
		t.Fatalf("target group connection/inheritance: %+v", resolved)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	pooled := cfg.WithResolvedRedisPoolSize()
	if pooled.PlatformCache.TargetGroup.PoolSize <= 0 || cfg.PlatformCache.TargetGroup.PoolSize != cfg.Redis.PoolSize {
		t.Fatal("pool derivation must resolve a copy")
	}
	for name, mutate := range map[string]func(*Config){
		"missing prefix":   func(c *Config) { c.PlatformCache.DynamicGroupKeyPrefix = nil },
		"invalid database": func(c *Config) { c.PlatformCache.TargetGroup.DB = -1 },
		"invalid address":  func(c *Config) { c.PlatformCache.TargetGroup.Address = "" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := cfg
			connection := *cfg.PlatformCache.TargetGroup
			copy.PlatformCache.TargetGroup = &connection
			mutate(&copy)
			if err := copy.Validate(); err == nil || !strings.Contains(err.Error(), "platform_cache.target_group") {
				t.Fatalf("validation = %v", err)
			}
		})
	}
}

func TestTargetGroupYAMLUsesExplicitConnection(t *testing.T) {
	cfg, err := Load(writeConfig(t, platformCacheConfigContents("\nplatform_cache:\n  target_group:\n    mode: standalone\n    address: groups:6379\n    db: 4\n  dynamic_group_key_prefix: 'groups:'\n")))
	if err != nil {
		t.Fatal(err)
	}
	connection, enabled := cfg.TargetGroupRedis()
	if !enabled || connection.Address != "groups:6379" || connection.DB != 4 {
		t.Fatalf("resolved connection = %+v, enabled=%v", connection, enabled)
	}
}
