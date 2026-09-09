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

// openBuiltInOutputDeploymentBundle opens the production bundle the way a
// deployment does, with everything except the Kafka output settings held at the
// values the running environment uses.
func openBuiltInOutputDeploymentBundle(t *testing.T, kafka config.KafkaConfig) error {
	t.Helper()
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
	cfg.Kafka.Brokers = kafka.Brokers
	cfg.Kafka.ClientID = kafka.ClientID
	cfg.Kafka.BrokerVersion = kafka.BrokerVersion
	cfg.Kafka.TriggerEvent = kafka.TriggerEvent
	cfg.Kafka.AllowedOutputTopics = kafka.AllowedOutputTopics
	cfg.Kafka.LegacyAdapter = kafka.LegacyAdapter
	cfg.Redis.Address = sourceAddress
	cfg.Redis.StatePrefix = "alarmd:phase-two:builtin-output:v1"
	runtimeRedis := cfg.Redis.Connection()
	runtimeRedis.Address = runtimeAddress
	cfg.PhaseTwo.RuntimeRedis = &runtimeRedis
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL

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
	return err
}

// deployedKafkaConfig is the shape a Shadow deployment publishes with: one
// native topic, that same single entry as the allowlist, and no legacy_adapter
// section at all. Narrowing the allowlist to what it publishes is what an
// operator is supposed to do, so this shape has to keep working.
func deployedKafkaConfig() config.KafkaConfig {
	const shadowTopic = "alarmd_shadow_trigger_event"
	kafka := config.KafkaConfig{
		Brokers:             []string{"127.0.0.1:9092"},
		ClientID:            "alarmd-shadow-writer",
		BrokerVersion:       "0.10.2.0",
		AllowedOutputTopics: []string{shadowTopic},
	}
	kafka.TriggerEvent.Topic = shadowTopic
	kafka.TriggerEvent.MaxMessageBytes = 524288
	return kafka
}

// The built-in Python protocol is selected by strategy data, so its topic is a
// topic this process can publish to. A deployment that never listed that topic
// has not permitted the publish, and the allowlist is what says so. The failure
// must name the topic and say the process supplied it, because the operator
// reading the message never wrote that name anywhere.
func TestBuiltInPythonOutputTopicRejectedByDeploymentAllowlistNamesTheTopic(t *testing.T) {
	err := openBuiltInOutputDeploymentBundle(t, deployedKafkaConfig())
	if err == nil {
		t.Fatal("a deployment that allowlists no compatibility topic must not start")
	}
	if !strings.Contains(err.Error(), "alarmd_0bkmonitor_backend_event") {
		t.Fatalf("failure does not name the rejected topic: %v", err)
	}
	if !strings.Contains(err.Error(), "allowed_output_topics") {
		t.Fatalf("failure does not name the setting the operator must change: %v", err)
	}
}

// Allowlisting the topic is not enough to publish the protocol: every event
// carries a snapshot that goes to the Python service Redis first. Accepting the
// start without those coordinates is what produced the release that failed
// every event for twenty-five minutes while Slots kept completing, so the
// missing dependency has to stop the process instead of each write.
func TestBuiltInPythonOutputWithoutServiceRedisFailsAtStartup(t *testing.T) {
	kafka := deployedKafkaConfig()
	kafka.AllowedOutputTopics = append(kafka.AllowedOutputTopics, "alarmd_0bkmonitor_backend_event")
	err := openBuiltInOutputDeploymentBundle(t, kafka)
	if err == nil {
		t.Fatal("a deployment with no legacy service Redis must not reach the first event to discover it")
	}
	if !strings.Contains(err.Error(), "legacy_adapter") {
		t.Fatalf("failure does not name the missing configuration section: %v", err)
	}
}
