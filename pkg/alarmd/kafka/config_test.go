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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestNewSaramaConfigForcesShadowConsumerInvariants(t *testing.T) {
	t.Parallel()

	config, err := NewSaramaConfig(Config{
		Brokers:       []string{"kafka-1.example:9092"},
		Topic:         "alarmd-trigger-input-shadow",
		GroupID:       "alarmd-trigger-shadow",
		ClientID:      "alarmd",
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
	if config.ClientID != "alarmd" {
		t.Fatalf("client id = %q", config.ClientID)
	}
}

func TestNewSaramaConfigSupportsLatestShadowEpoch(t *testing.T) {
	t.Parallel()

	config, err := NewSaramaConfig(Config{
		Brokers: []string{"kafka-1.example:9092"}, Topic: "detect-input", GroupID: "alarmd-shadow-v2",
		ClientID: "alarmd", BrokerVersion: "0.11.0.0", InitialOffset: InitialOffsetLatest,
	})
	if err != nil {
		t.Fatalf("NewSaramaConfig() error = %v", err)
	}
	if config.Consumer.Offsets.Initial != sarama.OffsetNewest {
		t.Fatalf("initial offset = %d, want newest", config.Consumer.Offsets.Initial)
	}
}

func TestNewSaramaConfigBoundsConsumerPrefetch(t *testing.T) {
	t.Parallel()

	config, err := NewSaramaConfig(Config{
		Brokers:       []string{"kafka-1.example:9092"},
		Topic:         "alarmd-trigger-input-shadow",
		GroupID:       "alarmd-trigger-shadow",
		ClientID:      "alarmd",
		BrokerVersion: "2.6.0",
	})
	if err != nil {
		t.Fatalf("NewSaramaConfig() error = %v", err)
	}
	if config.ChannelBufferSize != consumerChannelBufferRecords {
		t.Fatalf("channel buffer = %d, want the bounded prefetch %d", config.ChannelBufferSize, consumerChannelBufferRecords)
	}
	if config.Consumer.Fetch.Max != consumerMaxRecordBytes || config.Consumer.Fetch.Default != consumerMaxRecordBytes {
		t.Fatalf(
			"fetch bytes default=%d max=%d, want both bounded to %d",
			config.Consumer.Fetch.Default, config.Consumer.Fetch.Max, consumerMaxRecordBytes,
		)
	}
	if config.Net.MaxOpenRequests != 1 {
		t.Fatalf("in-flight requests = %d, want one bounded fetch per broker", config.Net.MaxOpenRequests)
	}
	// One in-flight fetch response plus the bounded prefetch channel. The
	// deployment memory budget multiplies this by the assigned partition count.
	if got, want := MaxConsumerBytesPerPartition(), 3*consumerMaxRecordBytes; got != want {
		t.Fatalf("worst-case bytes per partition = %d, want %d", got, want)
	}
	if got := MaxConsumerRecordBytes(); int32(got) != config.Consumer.Fetch.Max || int32(got) != config.Consumer.Fetch.Default {
		t.Fatalf("record bytes = %d, fetch default/max = %d/%d", got, config.Consumer.Fetch.Default, config.Consumer.Fetch.Max)
	}
	if consumerMaxRecordBytes < contract.MaxDetectInputBytesV1 {
		t.Fatalf("fetch bound %d cannot carry a maximum DetectInput record", consumerMaxRecordBytes)
	}
}

func TestConfigRejectsInvalidConsumerCoordinates(t *testing.T) {
	t.Parallel()

	valid := Config{
		Brokers:       []string{"kafka-1.example:9092"},
		Topic:         "alarmd-trigger-input-shadow",
		GroupID:       "alarmd-trigger-shadow",
		ClientID:      "alarmd",
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
