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
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/comparator"
)

// ComparatorServiceConfig contains the immutable coordinates for one
// single-member, three-stream comparison run.
type ComparatorServiceConfig struct {
	Brokers             []string
	TriggerInputTopic   string
	GoDecisionTopic     string
	PythonDecisionTopic string
	GroupID             string
	ClientID            string
	BrokerVersion       string
	MaxEntries          int
	CoverageTimeout     time.Duration
	BarrierInterval     time.Duration
}

func (c ComparatorServiceConfig) Topics() []string {
	return []string{c.TriggerInputTopic, c.GoDecisionTopic, c.PythonDecisionTopic}
}

func (c ComparatorServiceConfig) Validate() error {
	if c.MaxEntries <= 0 {
		return errors.New("kafka comparator service: max entries must be positive")
	}
	if c.CoverageTimeout <= 0 || c.BarrierInterval <= 0 {
		return errors.New("kafka comparator service: coverage timeout and barrier interval must be positive")
	}
	topics := c.Topics()
	seen := make(map[string]struct{}, len(topics))
	for _, topic := range topics {
		if err := validateKafkaTopicName("comparator_topics", topic); err != nil {
			return err
		}
		if _, exists := seen[topic]; exists {
			return errors.New("kafka comparator service: input topics must be unique")
		}
		seen[topic] = struct{}{}
	}
	return (Config{
		Brokers:       append([]string(nil), c.Brokers...),
		Topic:         c.TriggerInputTopic,
		GroupID:       c.GroupID,
		ClientID:      c.ClientID,
		BrokerVersion: c.BrokerVersion,
	}).Validate()
}

func (c ComparatorServiceConfig) consumerCoordinates() Config {
	return Config{
		Brokers:       append([]string(nil), c.Brokers...),
		Topic:         c.TriggerInputTopic,
		GroupID:       c.GroupID,
		ClientID:      c.ClientID,
		BrokerVersion: c.BrokerVersion,
	}
}

// OpenComparatorService creates the three-stream consumer. The assignment
// coordinator rejects any group member that does not own every partition of
// all three topics.
func OpenComparatorService(
	config ComparatorServiceConfig,
	audits ComparisonAuditSink,
	drainTimeout time.Duration,
) (*Service, error) {
	if audits == nil {
		return nil, errors.New("kafka comparator service: audit sink is required")
	}
	if drainTimeout <= 0 {
		return nil, errors.New("kafka comparator service: drain timeout must be positive")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	saramaConfig, err := NewSaramaConfig(config.consumerCoordinates())
	if err != nil {
		return nil, err
	}
	client, err := sarama.NewClient(config.Brokers, saramaConfig)
	if err != nil {
		return nil, fmt.Errorf("kafka comparator service: open client: %w", err)
	}
	group, err := sarama.NewConsumerGroupFromClient(config.GroupID, client)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("kafka comparator service: open consumer group: %w", err), client.Close())
	}
	offsets, err := NewSaramaOffsetCommitter(client, config.GroupID)
	if err != nil {
		return nil, errors.Join(err, group.Close(), client.Close())
	}
	assignment, err := newComparatorAssignmentCoordinator(
		client,
		map[comparator.StreamRole]string{
			comparator.StreamInput:  config.TriggerInputTopic,
			comparator.StreamGo:     config.GoDecisionTopic,
			comparator.StreamPython: config.PythonDecisionTopic,
		},
		config.MaxEntries,
		config.CoverageTimeout,
	)
	if err != nil {
		return nil, errors.Join(err, group.Close(), client.Close())
	}
	service, err := newOwnedGroupService(
		config.Topics(), group, client,
		func(reportFatal func(error)) (serviceHandler, error) {
			return newComparatorHandler(assignment, offsets, audits, config.BarrierInterval, reportFatal)
		},
		drainTimeout,
	)
	if err != nil {
		return nil, errors.Join(err, group.Close(), client.Close())
	}
	return service, nil
}
