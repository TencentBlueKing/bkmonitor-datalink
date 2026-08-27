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
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const (
	InitialOffsetOldest = "oldest"
	InitialOffsetLatest = "latest"
	// consumerMaxRecordBytes bounds one fetched record. Every Shadow wire is
	// capped at 512 KiB by its own encoder, and the remainder covers Kafka
	// record overhead such as the partition key and headers.
	consumerMaxRecordBytes = contract.MaxDetectInputBytesV1 + 64*1024
	// consumerChannelBufferRecords bounds prefetch. Claims are processed
	// serially, so Sarama's default of 256 buffered records would let a single
	// partition hold tens of MiB before the first record is even decoded.
	consumerChannelBufferRecords = 2
)

// MaxConsumerBytesPerPartition is the worst-case fetched-record memory one
// assigned partition can hold: one in-flight fetch response plus the bounded
// prefetch channel. Deployment sizing multiplies it by the assigned partition
// count across every consumed topic.
func MaxConsumerBytesPerPartition() int {
	return consumerMaxRecordBytes * (1 + consumerChannelBufferRecords)
}

// MaxConsumerRecordBytes is the configured Sarama fetch ceiling for one
// Kafka record. Runtime configuration must not admit a larger v2 envelope.
func MaxConsumerRecordBytes() int {
	return consumerMaxRecordBytes
}

// Config contains only stable consumer coordinates. Authentication remains a
// deployment concern until the Kafka security contract is read back.
type Config struct {
	Brokers       []string
	Topic         string
	GroupID       string
	ClientID      string
	BrokerVersion string
	InitialOffset string
	Diagnostics   ConsumerDiagnostics
}

func (c Config) Validate() error {
	if len(c.Brokers) == 0 {
		return errors.New("kafka: at least one broker is required")
	}
	seen := make(map[string]struct{}, len(c.Brokers))
	for _, broker := range c.Brokers {
		if err := validateBroker(broker); err != nil {
			return err
		}
		if _, ok := seen[broker]; ok {
			return fmt.Errorf("kafka: duplicate broker %q", broker)
		}
		seen[broker] = struct{}{}
	}
	for name, value := range map[string]string{
		"topic": c.Topic, "group_id": c.GroupID, "client_id": c.ClientID, "broker_version": c.BrokerVersion,
	} {
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("kafka: %s must be non-empty canonical text", name)
		}
	}
	if _, err := sarama.ParseKafkaVersion(c.BrokerVersion); err != nil {
		return fmt.Errorf("kafka: broker_version %q: %w", c.BrokerVersion, err)
	}
	if c.InitialOffset != "" && c.InitialOffset != InitialOffsetOldest && c.InitialOffset != InitialOffsetLatest {
		return fmt.Errorf("kafka: initial_offset must be %q or %q", InitialOffsetOldest, InitialOffsetLatest)
	}
	version, _ := sarama.ParseKafkaVersion(c.BrokerVersion)
	if !version.IsAtLeast(sarama.V0_10_2_0) || !sarama.MaxVersion.IsAtLeast(version) {
		return fmt.Errorf("kafka: broker_version %q is outside consumer group range 0.10.2.0..%s", c.BrokerVersion, sarama.MaxVersion)
	}
	return nil
}

func NewSaramaConfig(coordinates Config) (*sarama.Config, error) {
	if err := coordinates.Validate(); err != nil {
		return nil, err
	}
	version, err := sarama.ParseKafkaVersion(coordinates.BrokerVersion)
	if err != nil {
		return nil, fmt.Errorf("kafka: parse broker_version: %w", err)
	}
	config := sarama.NewConfig()
	config.ClientID = coordinates.ClientID
	config.Version = version
	config.Consumer.Return.Errors = true
	config.Consumer.Offsets.AutoCommit.Enable = false
	config.Consumer.Offsets.Initial = sarama.OffsetOldest
	if coordinates.InitialOffset == InitialOffsetLatest {
		config.Consumer.Offsets.Initial = sarama.OffsetNewest
	}
	config.ChannelBufferSize = consumerChannelBufferRecords
	config.Consumer.Fetch.Default = consumerMaxRecordBytes
	config.Consumer.Fetch.Max = consumerMaxRecordBytes
	config.Net.MaxOpenRequests = 1
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("kafka: validate sarama config: %w", err)
	}
	return config, nil
}

func validateBroker(broker string) error {
	if broker == "" || strings.TrimSpace(broker) != broker {
		return fmt.Errorf("kafka: broker %q must be canonical host:port", broker)
	}
	host, port, err := net.SplitHostPort(broker)
	if err != nil {
		return fmt.Errorf("kafka: broker %q: %w", broker, err)
	}
	if host == "" {
		return fmt.Errorf("kafka: broker %q has empty host", broker)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber <= 0 || portNumber > 65535 {
		return fmt.Errorf("kafka: broker %q has invalid port", broker)
	}
	return nil
}
