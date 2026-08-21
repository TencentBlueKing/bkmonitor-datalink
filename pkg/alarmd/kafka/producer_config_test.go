// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestNewDecisionProducerConfigForcesAcknowledgementAndBounds(t *testing.T) {
	t.Parallel()

	config, err := NewDecisionProducerConfig(validDecisionSinkConfig())
	if err != nil {
		t.Fatalf("NewDecisionProducerConfig() error = %v", err)
	}
	if config.Producer.RequiredAcks != sarama.WaitForAll {
		t.Fatalf("required acks = %d, want WaitForAll", config.Producer.RequiredAcks)
	}
	if config.Producer.Idempotent {
		t.Fatal("Shadow producer must not require the broker InitProducerID path")
	}
	if !config.Producer.Return.Successes || !config.Producer.Return.Errors {
		t.Fatal("sync producer acknowledgement channels must be enabled")
	}
	if config.Producer.Retry.Max != decisionProducerRetryMax || config.Producer.Retry.Backoff != decisionProducerRetryBackoff {
		t.Fatalf("producer retry = %d/%s", config.Producer.Retry.Max, config.Producer.Retry.Backoff)
	}
	if config.Net.MaxOpenRequests != 1 {
		t.Fatalf("max open requests = %d, want 1", config.Net.MaxOpenRequests)
	}
	if config.Metadata.Full {
		t.Fatal("decision producer must not fetch full-cluster topic metadata")
	}
	for name, got := range map[string]time.Duration{
		"producer": config.Producer.Timeout,
		"dial":     config.Net.DialTimeout,
		"read":     config.Net.ReadTimeout,
		"write":    config.Net.WriteTimeout,
		"metadata": config.Metadata.Timeout,
	} {
		if got != decisionProducerTimeout {
			t.Fatalf("%s timeout = %s, want %s", name, got, decisionProducerTimeout)
		}
	}
	if config.Producer.MaxMessageBytes <= contract.MaxTriggerDecisionBytesV1+32 {
		t.Fatalf("max message bytes = %d, must include key and record overhead", config.Producer.MaxMessageBytes)
	}
	if config.Producer.Compression != sarama.CompressionNone {
		t.Fatalf("compression = %d, want none", config.Producer.Compression)
	}
	partitioner := config.Producer.Partitioner("shadow-output")
	message := &sarama.ProducerMessage{Key: sarama.ByteEncoder([]byte("stable-key"))}
	first, err := partitioner.Partition(message, 7)
	if err != nil {
		t.Fatalf("Partition() error = %v", err)
	}
	second, err := partitioner.Partition(message, 7)
	if err != nil {
		t.Fatalf("Partition() second error = %v", err)
	}
	if first != second || !partitioner.RequiresConsistency() {
		t.Fatal("partitioner must hash the stable key consistently")
	}
}

func TestNewDecisionProducerConfigSupportsDatalinkKafkaBaseline(t *testing.T) {
	t.Parallel()

	coordinates := validDecisionSinkConfig()
	coordinates.BrokerVersion = "0.10.2.0"
	config, err := NewDecisionProducerConfig(coordinates)
	if err != nil {
		t.Fatalf("NewDecisionProducerConfig() error = %v", err)
	}
	if config.Version != sarama.V0_10_2_0 {
		t.Fatalf("broker version = %s, want %s", config.Version, sarama.V0_10_2_0)
	}
	if config.Producer.Idempotent {
		t.Fatal("0.10.2-compatible Shadow producer must not require InitProducerID")
	}
	if config.Producer.RequiredAcks != sarama.WaitForAll {
		t.Fatalf("required acks = %d, want WaitForAll", config.Producer.RequiredAcks)
	}
}

func TestDecisionSinkConfigRejectsInvalidCoordinatesAndPolicy(t *testing.T) {
	t.Parallel()

	valid := validDecisionSinkConfig()
	tests := map[string]func(*DecisionSinkConfig){
		"missing brokers": func(config *DecisionSinkConfig) { config.Brokers = nil },
		"duplicate broker": func(config *DecisionSinkConfig) {
			config.Brokers = []string{"kafka-1.example:9092", "kafka-1.example:9092"}
		},
		"missing input topic":  func(config *DecisionSinkConfig) { config.InputTopic = "" },
		"missing output topic": func(config *DecisionSinkConfig) { config.OutputTopic = "" },
		"same input and output": func(config *DecisionSinkConfig) {
			config.OutputTopic = config.InputTopic
			config.AllowedOutputTopics = []string{config.InputTopic}
		},
		"missing allowlist": func(config *DecisionSinkConfig) { config.AllowedOutputTopics = nil },
		"output not allowed": func(config *DecisionSinkConfig) {
			config.AllowedOutputTopics = []string{"another-shadow-output"}
		},
		"allowlist contains input": func(config *DecisionSinkConfig) {
			config.AllowedOutputTopics = append(config.AllowedOutputTopics, config.InputTopic)
		},
		"duplicate allowlist entry": func(config *DecisionSinkConfig) {
			config.AllowedOutputTopics = append(config.AllowedOutputTopics, config.OutputTopic)
		},
		"non-canonical topic": func(config *DecisionSinkConfig) {
			config.OutputTopic = " shadow-output"
			config.AllowedOutputTopics = []string{config.OutputTopic}
		},
		"invalid topic characters": func(config *DecisionSinkConfig) {
			config.OutputTopic = "shadow/output"
			config.AllowedOutputTopics = []string{config.OutputTopic}
		},
		"missing client":        func(config *DecisionSinkConfig) { config.ClientID = "" },
		"missing version":       func(config *DecisionSinkConfig) { config.BrokerVersion = "" },
		"invalid version":       func(config *DecisionSinkConfig) { config.BrokerVersion = "invalid" },
		"version below minimum": func(config *DecisionSinkConfig) { config.BrokerVersion = "0.10.1.0" },
		"version above maximum": func(config *DecisionSinkConfig) { config.BrokerVersion = "9.9.9" },
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			config := cloneDecisionSinkConfig(valid)
			mutate(&config)
			if _, err := NewDecisionProducerConfig(config); err == nil {
				t.Fatal("NewDecisionProducerConfig() accepted invalid configuration")
			}
		})
	}
}

func validDecisionSinkConfig() DecisionSinkConfig {
	return DecisionSinkConfig{
		Brokers:             []string{"kafka-1.example:9092"},
		InputTopic:          "alarmd-trigger-input-shadow",
		OutputTopic:         "alarmd-trigger-decision-shadow",
		AllowedOutputTopics: []string{"alarmd-trigger-decision-shadow"},
		ClientID:            "alarmd",
		BrokerVersion:       "2.6.0",
	}
}

func cloneDecisionSinkConfig(config DecisionSinkConfig) DecisionSinkConfig {
	config.Brokers = append([]string(nil), config.Brokers...)
	config.AllowedOutputTopics = append([]string(nil), config.AllowedOutputTopics...)
	return config
}
