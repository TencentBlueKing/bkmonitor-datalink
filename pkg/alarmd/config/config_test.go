// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

func TestDefaultRequiresExplicitEnvironmentCoordinates(t *testing.T) {
	cfg := Default()

	if cfg.Mode != ModeShadow {
		t.Fatalf("default mode = %q, want %q", cfg.Mode, ModeShadow)
	}
	if cfg.HTTP.Listen == "" || cfg.ShutdownTimeout.Duration() <= 0 {
		t.Fatal("default local HTTP and shutdown budgets must be usable")
	}
	if cfg.Kafka.TriggerEvent.MaxMessageBytes <= 0 || cfg.Kafka.MessageReceipt.MaxMessageBytes <= 0 {
		t.Fatal("default output byte budgets must be positive")
	}
	if cfg.Redis.Address != "" || cfg.Redis.StatePrefix != "" || len(cfg.Kafka.Brokers) != 0 {
		t.Fatal("environment Kafka and Redis coordinates must not have defaults")
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "broker") {
		t.Fatalf("default configuration error = %v, want missing Kafka broker", err)
	}
}

func TestLoadBuildsPhaseOneCoordinatesAndModuleOptions(t *testing.T) {
	cfg, err := Load(writeConfig(t, validRuntimeConfig()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	consumer := cfg.Kafka.ConsumerCoordinates()
	triggerEvent := cfg.Kafka.TriggerEventCoordinates()
	messageReceipt := cfg.Kafka.MessageReceiptCoordinates()
	if consumer.Topic != "alarmd-v2-input" || triggerEvent.InputTopic != consumer.Topic || messageReceipt.InputTopic != consumer.Topic {
		t.Fatalf("input topics consumer=%q trigger=%q receipt=%q, want one v2 input coordinate", consumer.Topic, triggerEvent.InputTopic, messageReceipt.InputTopic)
	}
	if triggerEvent.OutputTopic != "alarmd-trigger-event" || messageReceipt.OutputTopic != "alarmd-message-receipt" {
		t.Fatalf("output topics trigger=%q receipt=%q", triggerEvent.OutputTopic, messageReceipt.OutputTopic)
	}
	if triggerEvent.MaxMessageBytes != 600000 || messageReceipt.MaxMessageBytes != 120000 {
		t.Fatalf("output max bytes trigger=%d receipt=%d", triggerEvent.MaxMessageBytes, messageReceipt.MaxMessageBytes)
	}
	consumer.Brokers[0] = "mutated:9092"
	if triggerEvent.Brokers[0] != "127.0.0.1:9092" || cfg.Kafka.Brokers[0] != "127.0.0.1:9092" {
		t.Fatal("Kafka coordinate conversions did not deep-copy brokers")
	}

	redisOptions := cfg.RedisBackendOptions()
	if redisOptions.Address != "redis.test:6379" || redisOptions.Username != "alarmd" || redisOptions.Password != "secret" || redisOptions.DB != 7 {
		t.Fatalf("Redis backend options = %+v", redisOptions)
	}
	if redisOptions.DialTimeout != 2*time.Second || redisOptions.ReadTimeout != 3*time.Second || redisOptions.WriteTimeout != 4*time.Second || redisOptions.PoolSize != 24 {
		t.Fatalf("Redis transport options = %+v", redisOptions)
	}

	if cfg.ReaderLimits().MaxEnvelopeBytes <= 0 || cfg.CompilerLimits().BudgetRevision == "" || cfg.DetectLimits().MaxPlans == 0 ||
		cfg.TriggerLimits().MaxLevels == 0 || cfg.CodecLimits().MaxEncodedBytes <= 0 || cfg.StoreLimits().MaxWrittenBytes <= 0 {
		t.Fatal("phase-one module limits were not converted")
	}
	compilerLimits := cfg.CompilerLimits()
	if cfg.ReaderLimits().MaxRecordsPerMessage != 321 || compilerLimits.BudgetRevision != "deployment-v1" ||
		compilerLimits.MaxTriggerWindowSize != 123 || compilerLimits.MaxTriggerWindowSize != cfg.TriggerLimits().MaxTriggerWindowSize ||
		compilerLimits.MaxRecoveryConsecutiveWindows != 456 ||
		compilerLimits.MaxRecoveryConsecutiveWindows != cfg.TriggerLimits().MaxRecoveryConsecutiveWindows ||
		compilerLimits.MaxTriggerComputeCost != cfg.TriggerLimits().MaxComputeCost ||
		cfg.DetectLimits().MaxResultBytes != 20<<20 || cfg.TriggerLimits().MaxComputeCost != 2<<20 ||
		cfg.CodecLimits().MaxEncodedBytes != 600000 || cfg.StoreLimits().MaxWrittenBytes != 65<<20 {
		t.Fatal("phase-one YAML limit overrides were not preserved")
	}
	if retry := cfg.DependencyRetryOptions(); retry.MinDelay != 125*time.Millisecond || retry.MaxDelay != 3*time.Second {
		t.Fatalf("dependency retry = %+v", retry)
	}
	if queue := cfg.ReceiptPublisherLimits(); queue.MaxQueuedMessages != 2000 || queue.MaxQueuedBytes != 8<<20 {
		t.Fatalf("receipt queue = %+v", queue)
	}
	if runner := cfg.EvaluationRunnerLimits(); runner.PreparationWorkers != 3 || runner.StatefulWorkers != 6 ||
		runner.MaxInflightMessages != 24 || runner.MaxInflightBytes != 12<<20 ||
		runner.MaxRuntimeKeysPerMessage != 7000 || runner.MaxPendingKeyRefs != 84_000 {
		t.Fatalf("evaluation runner limits = %+v", runner)
	}

	codec, err := state.NewCodec(cfg.CodecLimits())
	if err != nil {
		t.Fatalf("NewCodec() error = %v", err)
	}
	storeOptions := cfg.StateStoreOptions(codec, staticRouter{}, nil)
	if storeOptions.Prefix != "alarmd-shadow" || storeOptions.MinTTL != time.Minute || storeOptions.MaxTTL != 24*time.Hour || storeOptions.RestartMargin != 5*time.Minute {
		t.Fatalf("state store options = %+v", storeOptions)
	}
}

func TestLoadRejectsLegacyAndPhaseTwoFields(t *testing.T) {
	for name, testCase := range map[string]struct {
		contents string
		field    string
	}{
		"legacy output": {
			contents: strings.Replace(validRuntimeConfig(), "  trigger_event:\n", "  output_topic: legacy-output\n  trigger_event:\n", 1),
			field:    "output_topic",
		},
		"worker shards": {contents: "mode: shadow\nworker_shards: 4\n", field: "worker_shards"},
		"owner epoch":   {contents: "mode: shadow\nowner_epoch: 1\n", field: "owner_epoch"},
		"state CAS":     {contents: "mode: shadow\nstate_cas: true\n", field: "state_cas"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeConfig(t, testCase.contents))
			if err == nil || !strings.Contains(err.Error(), testCase.field) {
				t.Fatalf("Load() error = %v, want strict rejection of %q", err, testCase.field)
			}
		})
	}
}

func TestLoadRejectsUnsafeKafkaTopicTopology(t *testing.T) {
	tests := map[string]func(*Config){
		"same output topics": func(cfg *Config) { cfg.Kafka.MessageReceipt.Topic = cfg.Kafka.TriggerEvent.Topic },
		"input allowlisted": func(cfg *Config) {
			cfg.Kafka.AllowedOutputTopics = append(cfg.Kafka.AllowedOutputTopics, cfg.Kafka.InputTopic)
		},
		"trigger event not allowlisted": func(cfg *Config) {
			cfg.Kafka.AllowedOutputTopics = []string{cfg.Kafka.MessageReceipt.Topic}
		},
		"receipt not allowlisted": func(cfg *Config) {
			cfg.Kafka.AllowedOutputTopics = []string{cfg.Kafka.TriggerEvent.Topic}
		},
		"zero trigger event bytes": func(cfg *Config) { cfg.Kafka.TriggerEvent.MaxMessageBytes = 0 },
		"zero receipt bytes":       func(cfg *Config) { cfg.Kafka.MessageReceipt.MaxMessageBytes = 0 },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := validConfigObject()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate() accepted %s", name)
			}
		})
	}
}

func TestValidateRejectsInvalidRedisAndRuntimeBudgets(t *testing.T) {
	tests := map[string]func(*Config){
		"missing Redis address": func(cfg *Config) { cfg.Redis.Address = "" },
		"negative Redis DB":     func(cfg *Config) { cfg.Redis.DB = -1 },
		"zero Redis pool":       func(cfg *Config) { cfg.Redis.PoolSize = 0 },
		"blank state prefix":    func(cfg *Config) { cfg.Redis.StatePrefix = " " },
		"reversed state TTL":    func(cfg *Config) { cfg.Redis.MaxTTL = cfg.Redis.MinTTL - 1 },
		"negative restart margin": func(cfg *Config) {
			cfg.Redis.RestartMargin = Duration(-time.Second)
		},
		"zero reader budget": func(cfg *Config) { cfg.Limits.Reader.MaxEnvelopeBytes = 0 },
		"reader exceeds Kafka fetch": func(cfg *Config) {
			cfg.Limits.Reader.MaxEnvelopeBytes = enginekafka.MaxConsumerRecordBytes() + 1
		},
		"zero compiler budget": func(cfg *Config) { cfg.Limits.Compiler.MaxPlanBytes = 0 },
		"zero detect budget":   func(cfg *Config) { cfg.Limits.Detect.MaxPlans = 0 },
		"zero trigger budget":  func(cfg *Config) { cfg.Limits.Trigger.MaxLevels = 0 },
		"compiler levels exceed trigger event": func(cfg *Config) {
			cfg.Limits.Trigger.MaxLevelResultsPerEvent = uint32(cfg.Limits.Compiler.MaxLevelsPerPlan - 1)
		},
		"zero codec budget": func(cfg *Config) { cfg.Limits.Codec.MaxLevels = 0 },
		"zero store budget": func(cfg *Config) { cfg.Limits.Store.MaxKeysPerBatch = 0 },
		"reversed retry": func(cfg *Config) {
			cfg.DependencyRetry.MaxDelay = cfg.DependencyRetry.MinDelay - 1
		},
		"zero receipt queue":      func(cfg *Config) { cfg.ReceiptQueue.MaxQueuedMessages = 0 },
		"zero evaluation workers": func(cfg *Config) { cfg.EvaluationRunner.MaxStatefulWorkers = 0 },
		"runtime keys cannot admit maximum reader message": func(cfg *Config) {
			cfg.EvaluationRunner.MaxRuntimeKeysPerMessage =
				cfg.Limits.Reader.MaxPlansPerMessage*cfg.Limits.Reader.MaxRecordsPerMessage - 1
		},
		"inflight bytes cannot admit maximum reader envelope": func(cfg *Config) {
			cfg.EvaluationRunner.MaxInflightBytes = cfg.Limits.Reader.MaxEnvelopeBytes - 1
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := validConfigObject()
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate() accepted %s", name)
			}
		})
	}
}

func TestLoadRejectsModesThatCanProduceAuthoritativeOutput(t *testing.T) {
	for _, mode := range []string{"owner", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			_, err := Load(writeConfig(t, "mode: "+mode+"\n"))
			if err == nil || !strings.Contains(err.Error(), "mode") {
				t.Fatalf("Load() error = %v, want unsafe mode rejection", err)
			}
		})
	}
}

func TestLoadRejectsInvalidHTTPAndTimeout(t *testing.T) {
	tests := map[string]string{
		"listen":         "mode: shadow\nhttp:\n  listen: invalid\n",
		"empty host":     "mode: shadow\nhttp:\n  listen: :8080\n",
		"zero port":      "mode: shadow\nhttp:\n  listen: 127.0.0.1:0\n",
		"port too large": "mode: shadow\nhttp:\n  listen: 127.0.0.1:65536\n",
		"timeout":        "mode: shadow\nshutdown_timeout: 0s\n",
	}

	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, contents)); err == nil {
				t.Fatalf("Load() accepted invalid %s", name)
			}
		})
	}
}

func TestDurationRejectsInvalidText(t *testing.T) {
	var duration Duration
	if err := duration.UnmarshalText([]byte("forever")); err == nil {
		t.Fatal("UnmarshalText() accepted an invalid duration")
	}
	if err := duration.UnmarshalText([]byte("3s")); err != nil {
		t.Fatalf("UnmarshalText() rejected a valid duration: %v", err)
	}
	if got := duration.Duration(); got != 3*time.Second {
		t.Fatalf("Duration() = %s, want 3s", got)
	}
}

type staticRouter struct{}

func (staticRouter) Route(_, _ string) (state.StorageTarget, error) {
	return state.StorageTarget{Name: "primary", Backend: nil}, nil
}

func validConfigObject() Config {
	cfg := Default()
	cfg.Kafka.Brokers = []string{"127.0.0.1:9092"}
	cfg.Kafka.InputTopic = "alarmd-v2-input"
	cfg.Kafka.TriggerEvent.Topic = "alarmd-trigger-event"
	cfg.Kafka.MessageReceipt.Topic = "alarmd-message-receipt"
	cfg.Kafka.AllowedOutputTopics = []string{"alarmd-trigger-event", "alarmd-message-receipt"}
	cfg.Kafka.GroupID = "alarmd-shadow"
	cfg.Kafka.ClientID = "alarmd"
	cfg.Kafka.BrokerVersion = "2.6.0"
	cfg.Redis.Address = "redis.test:6379"
	cfg.Redis.StatePrefix = "alarmd-shadow"
	return cfg
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func validRuntimeConfig() string {
	return `mode: shadow
http:
  listen: 127.0.0.1:8080
shutdown_timeout: 11s
kafka:
  brokers:
    - 127.0.0.1:9092
  input_topic: alarmd-v2-input
  trigger_event:
    topic: alarmd-trigger-event
    max_message_bytes: 600000
  message_receipt:
    topic: alarmd-message-receipt
    max_message_bytes: 120000
  allowed_output_topics:
    - alarmd-trigger-event
    - alarmd-message-receipt
  group_id: alarmd-shadow
  client_id: alarmd
  broker_version: 2.6.0
redis:
  address: redis.test:6379
  username: alarmd
  password: secret
  db: 7
  dial_timeout: 2s
  read_timeout: 3s
  write_timeout: 4s
  pool_size: 24
  state_prefix: alarmd-shadow
  min_ttl: 1m
  max_ttl: 24h
  restart_margin: 5m
dependency_retry:
  min_delay: 125ms
  max_delay: 3s
receipt_queue:
  max_queued_messages: 2000
  max_queued_bytes: 8388608
evaluation_runner:
  max_preparation_workers: 3
  max_stateful_workers: 6
  max_inflight_messages: 24
  max_inflight_bytes: 12582912
  max_runtime_keys_per_message: 7000
  max_pending_key_refs: 84000
limits:
  reader:
    max_records_per_message: 321
  compiler:
    budget_revision: deployment-v1
  detect:
    max_result_bytes: 20971520
  trigger:
    max_trigger_window_size: 123
    max_recovery_consecutive_windows: 456
    max_compute_cost: 2097152
  codec:
    max_encoded_bytes: 600000
  store:
    max_written_bytes: 68157440
`
}
