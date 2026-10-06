// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
)

func TestProductionRedisOptionsPreserveStandaloneContract(t *testing.T) {
	connection := config.RedisConnectionConfig{
		Mode: config.RedisModeStandalone, Address: "redis.test:6379", Username: "worker", Password: "secret", DB: 8,
		DialTimeout: config.Duration(time.Second), ReadTimeout: config.Duration(2 * time.Second),
		WriteTimeout: config.Duration(3 * time.Second), PoolSize: 12,
	}
	options := productionRedisOptions(connection)

	if !reflect.DeepEqual(options.Addrs, []string{"redis.test:6379"}) || options.MasterName != "" ||
		options.Username != "worker" || options.Password != "secret" || options.DB != 8 || options.PoolSize != 12 ||
		options.DialTimeout != time.Second || options.ReadTimeout != 2*time.Second || options.WriteTimeout != 3*time.Second {
		t.Fatalf("standalone universal options = %+v", options)
	}
}

func TestProductionRedisOptionsPreserveSentinelContract(t *testing.T) {
	connection := config.RedisConnectionConfig{
		Mode: config.RedisModeSentinel, SentinelAddress: []string{"sentinel-a:26379", "sentinel-b:26379"},
		MasterName: "monitor-master", Username: "worker", Password: "secret",
		SentinelUsername: "sentinel-worker", SentinelPassword: "sentinel-secret", DB: 8,
		DialTimeout: config.Duration(time.Second), ReadTimeout: config.Duration(2 * time.Second),
		WriteTimeout: config.Duration(3 * time.Second), PoolSize: 12,
	}
	options := productionRedisOptions(connection)

	if !reflect.DeepEqual(options.Addrs, connection.SentinelAddress) || options.MasterName != "monitor-master" ||
		options.Username != "worker" || options.Password != "secret" || options.SentinelUsername != "sentinel-worker" ||
		options.SentinelPassword != "sentinel-secret" || options.DB != 8 || options.PoolSize != 12 {
		t.Fatalf("sentinel universal options = %+v", options)
	}
	options.Addrs[0] = "mutated:26379"
	if connection.SentinelAddress[0] != "sentinel-a:26379" {
		t.Fatal("universal options alias configuration sentinel addresses")
	}
}
