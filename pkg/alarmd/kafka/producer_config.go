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
)

const (
	decisionProducerRetryMax           = 3
	decisionProducerRetryBackoff       = 100 * time.Millisecond
	decisionProducerTimeout            = 10 * time.Second
	decisionProducerMessageOverheadCap = 64 * 1024
)

// OutputBatchBound is how long one output batch is allowed to take to land
// before the sink refuses to start it against a lease with less life left
// (decision-016 per-batch admission): the producer's per-request timeout,
// the time one attempt at the broker may take. Not the retried worst case:
// retries follow a failure, and a failure is reported as an unknown ACK
// whether or not the lease outlives it; what admission decides is whether a
// batch that goes well lands inside the lease. It is the same constant the
// producer is built with, so the two cannot drift.
const OutputBatchBound = decisionProducerTimeout

// OutputAdmissionMargin is the allowance between the admission check and
// the first byte on the wire, and for the spread between the clock the lease
// deadline was carried over on and the one this process reads now.
const OutputAdmissionMargin = time.Second

var kafkaTopicNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// DecisionSinkConfig contains the immutable coordinates and application-level
// output policy for the isolated Shadow decision producer.
type DecisionSinkConfig struct {
	Brokers         []string
	InputTopic      string
	OutputTopic     string
	ClientID        string
	BrokerVersion   string
	MaxMessageBytes int
}

func (c DecisionSinkConfig) Validate() error {
	if err := c.ValidateProducerOnly(); err != nil {
		return err
	}
	if err := validateKafkaTopicName("input_topic", c.InputTopic); err != nil {
		return err
	}
	if c.InputTopic == c.OutputTopic {
		return errors.New("kafka decision producer: input_topic and output_topic must differ")
	}
	return nil
}

// ValidateProducerOnly validates the output producer without requiring the
// phase-one input topic. Input topology remains the responsibility of Validate.
func (c DecisionSinkConfig) ValidateProducerOnly() error {
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
	if err := validateKafkaTopicName("output_topic", c.OutputTopic); err != nil {
		return err
	}
	for name, value := range map[string]string{"client_id": c.ClientID, "broker_version": c.BrokerVersion} {
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("kafka decision producer: %s must be non-empty canonical text", name)
		}
	}
	if c.MaxMessageBytes <= 0 {
		return errors.New("kafka decision producer: max_message_bytes must be positive")
	}
	if _, err := ValidateBrokerVersion("kafka decision producer", c.BrokerVersion); err != nil {
		return err
	}
	return nil
}

// NewDecisionProducerConfig fixes all acknowledgement, retry and timeout
// invariants required by the isolated Shadow decision sink.
func NewDecisionProducerConfig(coordinates DecisionSinkConfig) (*sarama.Config, error) {
	if err := coordinates.Validate(); err != nil {
		return nil, err
	}
	return newDecisionProducerConfig(coordinates)
}

// NewDecisionProducerOnlyConfig builds the synchronous output producer used
// by phase-two TriggerEvent without inventing a phase-one input coordinate.
func NewDecisionProducerOnlyConfig(coordinates DecisionSinkConfig) (*sarama.Config, error) {
	if err := coordinates.ValidateProducerOnly(); err != nil {
		return nil, err
	}
	return newDecisionProducerConfig(coordinates)
}

func newDecisionProducerConfig(coordinates DecisionSinkConfig) (*sarama.Config, error) {
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
	// Shadow output is intentionally at-least-once. Stable decision and audit
	// identifiers make replays observable without depending on the broker's
	// InitProducerID path.
	config.Producer.Idempotent = false
	config.Producer.Return.Successes = true
	config.Producer.Return.Errors = true
	config.Producer.Retry.Max = decisionProducerRetryMax
	config.Producer.Retry.Backoff = decisionProducerRetryBackoff
	config.Producer.MaxMessageBytes = coordinates.MaxMessageBytes + decisionProducerMessageOverheadCap
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
