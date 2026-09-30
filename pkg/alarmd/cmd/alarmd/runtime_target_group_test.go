// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestTargetGroupProductionConnection(t *testing.T) {
	for _, mode := range []string{"separate_server", "separate_database", "shared_cmdb", "shared_dynamic", "legacy", "absent", "initialization_failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			address, source := startPhaseTwoRedis(t)
			cfg := validGoAccessRuntimeConfig()
			cfg.Redis.Address = address
			withCompatibilityOutput(&cfg, address)
			prefix := "groups:"
			if mode != "absent" {
				cfg.PlatformCache.DynamicGroupKeyPrefix = &prefix
			}
			connection := cfg.Redis.Connection()
			writer := source
			if mode == "separate_server" || mode == "shared_dynamic" || mode == "initialization_failure" {
				connection.Address, writer = startPhaseTwoRedis(t)
			}
			if mode == "separate_database" {
				connection.DB = 3
				writer = redis.NewClient(&redis.Options{Addr: address, DB: 3})
				t.Cleanup(func() { _ = writer.Close() })
			}
			if mode != "legacy" && mode != "absent" {
				cfg.PlatformCache.TargetGroup = &connection
			}
			if mode == "shared_dynamic" {
				cfg.PlatformCache.DynamicConfig = &connection
			}
			if writer != source {
				if err := source.Set(ctx, prefix+"dynamic_group:1001", `{"model_id":"host","model_inst_ids":["202"],"member_list":[{"bk_host_id":202,"model_id":"host","model_inst_id":"202"}]}`, 0).Err(); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Set(ctx, prefix+"dynamic_group:1001", `{"model_id":"host","model_inst_ids":["101"],"member_list":[{"bk_host_id":101,"model_id":"host","model_inst_id":"101"}]}`, 0).Err(); err != nil {
				t.Fatal(err)
			}
			recorder := metric.NewRecorder(metric.BuildInfo{})
			bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, recorder,
				observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
				func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
					if mode == "initialization_failure" {
						return nil, errors.New("injected source initialization failure")
					}
					return controlplane.NewLegacyRedisStrategySource(client, prefix)
				}, phaseTwoProductionExternalDependencies{Now: time.Now, HTTPClient: http.DefaultClient,
					OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
						return &recordingPhaseTwoEventSink{}, nil
					},
				})
			if mode == "initialization_failure" {
				if err == nil || !strings.Contains(err.Error(), "injected source initialization failure") {
					t.Fatalf("initialization error = %v", err)
				}
				clients, err := writer.ClientList(ctx).Result()
				if err != nil || len(strings.Split(strings.TrimSpace(clients), "\n")) != 1 {
					t.Fatalf("failed initialization leaked connections: %s (%v)", clients, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = bundle.Shutdown(ctx) })
			plan := &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "host", Rule: contract.TargetPlanRuleHostID,
				Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, DynamicGroups: []string{"1001"}}
			resolution := bundle.workerPorts.Targets.Resolve(ctx, plan, time.Minute)
			if resolution.Contains("101") != (mode != "absent") || resolution.Contains("202") {
				t.Fatalf("resolution = %+v", resolution)
			}
			_, groupHealth := recorder.RedisClientHealth("target_group")
			if groupHealth != (mode == "separate_server" || mode == "separate_database") {
				t.Fatalf("dedicated target group client = %v for %s", groupHealth, mode)
			}
			// A duplicate Close on a shared go-redis client returns ErrClosed.
			// Normal shutdown must close every owned connection exactly once.
			if err := bundle.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			// Redis CLIENT LIST is independent of the resolver's cached answer:
			// only this test's writer remains connected to an isolated server/DB.
			if mode == "separate_server" || mode == "shared_dynamic" || mode == "separate_database" {
				clients, err := writer.ClientList(ctx).Result()
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, line := range strings.Split(strings.TrimSpace(clients), "\n") {
					if mode != "separate_database" || strings.Contains(line, " db=3 ") {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("connections leaked after shutdown: %s", clients)
				}
			}
		})
	}
}

func TestTargetGroupEndpointAndProfileAreCredentialFree(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	prefix := "groups:"
	cfg.PlatformCache.DynamicGroupKeyPrefix = &prefix
	connection := cfg.Redis.Connection()
	connection.Address, connection.DB, connection.Password = "groups:6379", 3, "secret-target-password"
	cfg.PlatformCache.TargetGroup = &connection
	entries := resolveEndpoints(cfg, endpointSharing{targetGroupSharedWith: fleet.EndpointDynamicConfig})
	var target fleet.Endpoint
	for _, entry := range entries {
		if entry.Role == fleet.EndpointTargetGroup {
			target = entry
		}
	}
	if !target.Configured || target.Address != connection.Address || target.DB == nil || *target.DB != 3 || target.Prefix != prefix || target.SharedWith != fleet.EndpointDynamicConfig {
		t.Fatalf("endpoint = %+v", target)
	}
	profile, err := phaseTwoRuntimeProfile(cfg, "test", 1)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal([]any{entries, profile})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), connection.Password) || profile.Storage.TargetGroup != "standalone groups:6379/3" || profile.Storage.DynamicGroupKeyPrefix != prefix {
		t.Fatalf("profile/endpoint = %s", encoded)
	}
	sharing := endpointSharing{targetGroupSharedWith: fleet.EndpointDynamicConfig, dynamicSharedWith: fleet.EndpointCMDBCache, cmdbSharedWith: fleet.EndpointStrategyCache}
	if sharing.redisClientForRole(fleet.EndpointTargetGroup) != "source" {
		t.Fatal("shared health did not follow the connection")
	}
}
