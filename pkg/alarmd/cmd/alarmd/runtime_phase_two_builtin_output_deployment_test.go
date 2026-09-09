// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The built-in Python protocol is selected by strategy data, so its topic is a
// topic this process can publish to. A deployment that narrowed the allowlist to
// what it publishes today has not permitted it, and the allowlist is what says
// so. The failure has to name the topic, because the operator reading the
// message never wrote that name anywhere.
func TestBuiltInPythonOutputTopicRejectedByAllowlistNamesTheTopic(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	cfg.Kafka.AllowedOutputTopics = []string{cfg.Kafka.TriggerEvent.Topic}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("a deployment that allowlists no compatibility topic must not validate")
	}
	if !strings.Contains(err.Error(), cfg.Kafka.LegacyAdapter.Topic) {
		t.Fatalf("failure does not name the rejected topic: %v", err)
	}
	if !strings.Contains(err.Error(), "allowed_output_topics") {
		t.Fatalf("failure does not name the setting the operator must change: %v", err)
	}
}

// Allowlisting the topic is not enough to publish the protocol: every converted
// event writes a snapshot to the service Redis first. Accepting a deployment
// without those coordinates is what produced the release that failed every
// event for twenty-five minutes while Slots kept completing, so the missing
// dependency has to be refused before the process runs, not at the first write.
// Validating it here also means --check-config covers it; the preflight used to
// stop at decoding, and both incidents landed in that blind spot.
func TestBuiltInPythonOutputWithoutServiceRedisIsRejected(t *testing.T) {
	for name, breakIt := range map[string]func(*config.Config){
		"no service Redis":   func(cfg *config.Config) { cfg.Kafka.LegacyAdapter.ServiceRedis = config.RedisConnectionConfig{} },
		"no snapshot prefix": func(cfg *config.Config) { cfg.Kafka.LegacyAdapter.SnapshotPrefix = "" },
		"no service address": func(cfg *config.Config) { cfg.Kafka.LegacyAdapter.ServiceRedis.Address = "" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validGoAccessRuntimeConfig()
			breakIt(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("an incomplete compatibility output must not reach the first event to be discovered")
			}
			if !strings.Contains(err.Error(), "legacy_adapter") {
				t.Fatalf("failure does not name the missing configuration section: %v", err)
			}
		})
	}
}

// Configuration alone cannot prove the service Redis answers. The bundle opens
// it before any Slot runs, so an unreachable node stops the process at startup
// rather than at the first converted event.
func TestBuiltInPythonOutputUnreachableServiceRedisStopsStartup(t *testing.T) {
	sourceAddress, sourceClient := startPhaseTwoRedis(t)
	runtimeAddress, _ := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := sourceClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unused","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = sourceAddress
	withCompatibilityOutput(&cfg, sourceAddress)
	cfg.Redis.StatePrefix = "alarmd:phase-two:builtin-output:v1"
	runtimeRedis := cfg.Redis.Connection()
	runtimeRedis.Address = runtimeAddress
	cfg.PhaseTwo.RuntimeRedis = &runtimeRedis
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	// A port nothing listens on: the configuration is complete and only the
	// dependency is absent.
	cfg.Kafka.LegacyAdapter.ServiceRedis.Address = "127.0.0.1:1"
	cfg.Kafka.LegacyAdapter.ServiceRedis.DialTimeout = config.Duration(200 * time.Millisecond)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the configuration itself must stay valid: %v", err)
	}

	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: uqServer.Client(),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
	if bundle != nil {
		t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
	}
	if err == nil {
		t.Fatal("an unreachable compatibility service Redis must stop startup")
	}
}
