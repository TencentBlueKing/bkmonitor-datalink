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

	"github.com/Shopify/sarama"
)

func TestNewSaramaConfigForcesShadowConsumerInvariants(t *testing.T) {
	t.Parallel()

	config, err := NewSaramaConfig(Config{
		Brokers:       []string{"kafka-1.example:9092"},
		Topic:         "alarm-engine-trigger-input-shadow",
		GroupID:       "alarm-engine-trigger-shadow",
		ClientID:      "alarm-engine",
		BrokerVersion: "2.6.0",
	})
	if err != nil {
		t.Fatalf("NewSaramaConfig() error = %v", err)
	}
	if config.Consumer.Offsets.AutoCommit.Enable {
		t.Fatal("auto commit must be disabled")
	}
	if !config.Consumer.Return.Errors {
		t.Fatal("consumer errors must be returned")
	}
	if config.Consumer.Offsets.Initial != sarama.OffsetOldest {
		t.Fatalf("initial offset = %d, want oldest", config.Consumer.Offsets.Initial)
	}
	if config.ClientID != "alarm-engine" {
		t.Fatalf("client id = %q", config.ClientID)
	}
}

func TestConfigRejectsInvalidConsumerCoordinates(t *testing.T) {
	t.Parallel()

	valid := Config{
		Brokers:       []string{"kafka-1.example:9092"},
		Topic:         "alarm-engine-trigger-input-shadow",
		GroupID:       "alarm-engine-trigger-shadow",
		ClientID:      "alarm-engine",
		BrokerVersion: "2.6.0",
	}
	tests := map[string]func(*Config){
		"missing brokers": func(config *Config) { config.Brokers = nil },
		"empty broker":    func(config *Config) { config.Brokers = []string{""} },
		"duplicate broker": func(config *Config) {
			config.Brokers = []string{"kafka-1.example:9092", "kafka-1.example:9092"}
		},
		"missing topic":                        func(config *Config) { config.Topic = "" },
		"missing group":                        func(config *Config) { config.GroupID = "" },
		"missing client":                       func(config *Config) { config.ClientID = "" },
		"missing version":                      func(config *Config) { config.BrokerVersion = "" },
		"invalid version":                      func(config *Config) { config.BrokerVersion = "not-a-version" },
		"version below consumer group minimum": func(config *Config) { config.BrokerVersion = "0.8.2.0" },
		"version above client maximum":         func(config *Config) { config.BrokerVersion = "9.9.9" },
	}
	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := valid
			config.Brokers = append([]string(nil), valid.Brokers...)
			mutate(&config)
			if _, err := NewSaramaConfig(config); err == nil {
				t.Fatal("NewSaramaConfig() accepted invalid configuration")
			}
		})
	}
}
