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
	"context"
	"errors"
	"fmt"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// ComparisonAuditSinkConfig freezes the three comparator inputs and the only
// permitted isolated audit output.
type ComparisonAuditSinkConfig struct {
	Brokers             []string
	InputTopics         []string
	OutputTopic         string
	AllowedOutputTopics []string
	ClientID            string
	BrokerVersion       string
}

func (c ComparisonAuditSinkConfig) Validate() error {
	if len(c.InputTopics) != 3 {
		return errors.New("kafka comparison audit sink: exactly three input topics are required")
	}
	seen := make(map[string]struct{}, len(c.InputTopics))
	for _, topic := range c.InputTopics {
		if err := validateKafkaTopicName("input_topics", topic); err != nil {
			return err
		}
		if _, exists := seen[topic]; exists {
			return errors.New("kafka comparison audit sink: input topics must be unique")
		}
		seen[topic] = struct{}{}
		if topic == c.OutputTopic {
			return errors.New("kafka comparison audit sink: input and output topics must differ")
		}
	}
	for _, topic := range c.AllowedOutputTopics {
		if _, input := seen[topic]; input {
			return errors.New("kafka comparison audit sink: input topic must not appear in output allowlist")
		}
	}
	return c.decisionCoordinates().Validate()
}

func (c ComparisonAuditSinkConfig) decisionCoordinates() DecisionSinkConfig {
	inputTopic := ""
	if len(c.InputTopics) > 0 {
		inputTopic = c.InputTopics[0]
	}
	return DecisionSinkConfig{
		Brokers:             append([]string(nil), c.Brokers...),
		InputTopic:          inputTopic,
		OutputTopic:         c.OutputTopic,
		AllowedOutputTopics: append([]string(nil), c.AllowedOutputTopics...),
		ClientID:            c.ClientID,
		BrokerVersion:       c.BrokerVersion,
		MaxMessageBytes:     contract.MaxComparisonAuditBytesV1,
	}
}

func NewComparisonAuditProducerConfig(coordinates ComparisonAuditSinkConfig) (*sarama.Config, error) {
	if err := coordinates.Validate(); err != nil {
		return nil, err
	}
	config, err := NewDecisionProducerConfig(coordinates.decisionCoordinates())
	if err != nil {
		return nil, err
	}
	return config, nil
}

// OpenComparisonAuditSink creates the isolated synchronous audit producer.
func OpenComparisonAuditSink(coordinates ComparisonAuditSinkConfig) (*ComparisonAuditKafkaSink, error) {
	config, err := NewComparisonAuditProducerConfig(coordinates)
	if err != nil {
		return nil, err
	}
	client, err := sarama.NewClient(coordinates.Brokers, config)
	if err != nil {
		return nil, fmt.Errorf("kafka comparison audit sink: open client: %w", err)
	}
	producer, err := newSyncProducerForOutput(client, coordinates.OutputTopic)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("kafka comparison audit sink: open producer: %w", err), client.Close())
	}
	sink, err := newComparisonAuditSink(coordinates.OutputTopic, producer, client)
	if err != nil {
		return nil, errors.Join(err, producer.Close(), client.Close())
	}
	return sink, nil
}

// ComparisonAuditKafkaSink publishes only the official comparison audit wire
// and reuses the decision producer's acknowledgement and shutdown boundary.
type ComparisonAuditKafkaSink struct {
	core *DecisionSink
}

func newComparisonAuditSink(
	outputTopic string,
	producer syncMessageProducer,
	client closeableClient,
) (*ComparisonAuditKafkaSink, error) {
	core, err := newDecisionSink(outputTopic, producer, client)
	if err != nil {
		return nil, err
	}
	return &ComparisonAuditKafkaSink{core: core}, nil
}

func (s *ComparisonAuditKafkaSink) WriteBatch(ctx context.Context, batch *contract.ComparisonAuditBatch) error {
	if s == nil || s.core == nil {
		return ErrDecisionSinkClosed
	}
	payload, err := contract.EncodeComparisonAuditBatch(batch)
	if err != nil {
		return fmt.Errorf("kafka comparison audit sink: encode batch: %w", err)
	}
	key, err := batch.PartitionKey()
	if err != nil {
		return fmt.Errorf("kafka comparison audit sink: derive partition key: %w", err)
	}
	return s.core.writeEncoded(ctx, key, payload)
}

func (s *ComparisonAuditKafkaSink) Shutdown(ctx context.Context) error {
	if s == nil || s.core == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("kafka comparison audit sink: context is required")
	}
	return s.core.Shutdown(ctx)
}

func (s *ComparisonAuditKafkaSink) Close() error {
	if s == nil || s.core == nil {
		return nil
	}
	return s.core.Close()
}
