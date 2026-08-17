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
	"regexp"
	"strings"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
)

const (
	decisionProducerRetryMax           = 3
	decisionProducerRetryBackoff       = 100 * time.Millisecond
	decisionProducerTimeout            = 10 * time.Second
	decisionProducerMessageOverheadCap = 64 * 1024
)

var kafkaTopicNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// DecisionSinkConfig contains the immutable coordinates and application-level
// output policy for the isolated Shadow decision producer.
type DecisionSinkConfig struct {
	Brokers             []string
	InputTopic          string
	OutputTopic         string
	AllowedOutputTopics []string
	ClientID            string
	BrokerVersion       string
}

func (c DecisionSinkConfig) Validate() error {
	if len(c.Brokers) == 0 {
		return errors.New("kafka decision producer: at least one broker is required")
	}
	seenBrokers := make(map[string]struct{}, len(c.Brokers))
	for _, broker := range c.Brokers {
		if err := validateBroker(broker); err != nil {
			return err
		}
		if _, exists := seenBrokers[broker]; exists {
			return fmt.Errorf("kafka decision producer: duplicate broker %q", broker)
		}
		seenBrokers[broker] = struct{}{}
	}
	if err := validateKafkaTopicName("input_topic", c.InputTopic); err != nil {
		return err
	}
	if err := validateKafkaTopicName("output_topic", c.OutputTopic); err != nil {
		return err
	}
	if c.InputTopic == c.OutputTopic {
		return errors.New("kafka decision producer: input_topic and output_topic must differ")
	}
	if len(c.AllowedOutputTopics) == 0 {
		return errors.New("kafka decision producer: output topic allowlist must be non-empty")
	}
	seenTopics := make(map[string]struct{}, len(c.AllowedOutputTopics))
	for _, topic := range c.AllowedOutputTopics {
		if err := validateKafkaTopicName("allowed_output_topics", topic); err != nil {
			return err
		}
		if topic == c.InputTopic {
			return errors.New("kafka decision producer: input topic must not appear in output allowlist")
		}
		if _, exists := seenTopics[topic]; exists {
			return fmt.Errorf("kafka decision producer: duplicate allowed output topic %q", topic)
		}
		seenTopics[topic] = struct{}{}
	}
	if _, allowed := seenTopics[c.OutputTopic]; !allowed {
		return fmt.Errorf("kafka decision producer: output topic %q is not allowlisted", c.OutputTopic)
	}
	for name, value := range map[string]string{"client_id": c.ClientID, "broker_version": c.BrokerVersion} {
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("kafka decision producer: %s must be non-empty canonical text", name)
		}
	}
	version, err := sarama.ParseKafkaVersion(c.BrokerVersion)
	if err != nil {
		return fmt.Errorf("kafka decision producer: broker_version %q: %w", c.BrokerVersion, err)
	}
	if !version.IsAtLeast(sarama.V0_11_0_0) || !sarama.MaxVersion.IsAtLeast(version) {
		return fmt.Errorf("kafka decision producer: broker_version %q is outside idempotent producer range 0.11.0.0..%s", c.BrokerVersion, sarama.MaxVersion)
	}
	return nil
}

// NewDecisionProducerConfig fixes all acknowledgement, retry and timeout
// invariants required by the isolated Shadow decision sink.
func NewDecisionProducerConfig(coordinates DecisionSinkConfig) (*sarama.Config, error) {
	if err := coordinates.Validate(); err != nil {
		return nil, err
	}
	version, err := sarama.ParseKafkaVersion(coordinates.BrokerVersion)
	if err != nil {
		return nil, fmt.Errorf("kafka decision producer: parse broker_version: %w", err)
	}
	config := sarama.NewConfig()
	config.ClientID = coordinates.ClientID
	config.Version = version
	config.Net.MaxOpenRequests = 1
	config.Net.DialTimeout = decisionProducerTimeout
	config.Net.ReadTimeout = decisionProducerTimeout
	config.Net.WriteTimeout = decisionProducerTimeout
	config.Metadata.Full = false
	config.Metadata.Timeout = decisionProducerTimeout
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Timeout = decisionProducerTimeout
	config.Producer.Idempotent = true
	config.Producer.Return.Successes = true
	config.Producer.Return.Errors = true
	config.Producer.Retry.Max = decisionProducerRetryMax
	config.Producer.Retry.Backoff = decisionProducerRetryBackoff
	config.Producer.MaxMessageBytes = contract.MaxTriggerDecisionBytesV1 + decisionProducerMessageOverheadCap
	config.Producer.Compression = sarama.CompressionNone
	config.Producer.Partitioner = sarama.NewHashPartitioner
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("kafka decision producer: validate sarama config: %w", err)
	}
	return config, nil
}

func validateKafkaTopicName(field, topic string) error {
	if topic == "" || strings.TrimSpace(topic) != topic {
		return fmt.Errorf("kafka decision producer: %s must be non-empty canonical text", field)
	}
	if len(topic) > 249 || topic == "." || topic == ".." || !kafkaTopicNamePattern.MatchString(topic) {
		return fmt.Errorf("kafka decision producer: %s %q is not a valid Kafka topic name", field, topic)
	}
	return nil
}
