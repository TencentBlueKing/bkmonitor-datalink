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

	alarmdcoordinator "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
)

// OpenEvaluationService creates the phase-one v2 consumer. The evaluation
// dependencies are built by the runtime and shared across claim-local
// partition runners; the Kafka client remains owned by Service.
func OpenEvaluationService(
	config Config,
	router alarmdcoordinator.MessageOutcomeRouter,
	critical alarmdcoordinator.CriticalCompletion,
	receipts alarmdcoordinator.ReceiptPublisher,
	gate *alarmdcoordinator.CriticalDependencyGate,
	runnerLimits alarmdcoordinator.ConcurrentRunnerLimits,
	diagnostics EvaluationDiagnostics,
	retryConfig alarmdcoordinator.DependencyRetryConfig,
	drainTimeout time.Duration,
) (*Service, error) {
	if router == nil || critical == nil || receipts == nil || gate == nil {
		return nil, errors.New("kafka evaluation service: router, completion, receipts and dependency gate are required")
	}
	if diagnostics.OnRejected == nil {
		return nil, errors.New("kafka evaluation service: rejected evidence observer is required")
	}
	if drainTimeout <= 0 {
		return nil, errors.New("kafka evaluation service: drain timeout must be positive")
	}
	saramaConfig, err := newEvaluationSaramaConfig(config)
	if err != nil {
		return nil, err
	}
	client, err := sarama.NewClient(config.Brokers, saramaConfig)
	if err != nil {
		return nil, fmt.Errorf("kafka evaluation service: open client: %w", err)
	}
	group, err := sarama.NewConsumerGroupFromClient(config.GroupID, client)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("kafka evaluation service: open consumer group: %w", err), client.Close())
	}
	service, err := newOwnedEvaluationService(
		config.Topic, group, client, router, critical, evaluationSessionOffsetCommitter{}, receipts, gate,
		runnerLimits, diagnostics, retryConfig, drainTimeout,
	)
	if err != nil {
		return nil, errors.Join(err, group.Close(), client.Close())
	}
	service.diagnostics = config.Diagnostics
	service.repairOffsets = newGroupOffsetRepairer(
		client, config.GroupID, []string{config.Topic}, saramaConfig.Consumer.Offsets.Initial,
	).Repair
	return service, nil
}

func newEvaluationSaramaConfig(config Config) (*sarama.Config, error) {
	saramaConfig, err := NewSaramaConfig(config)
	if err != nil {
		return nil, err
	}
	// Once TriggerEvent and Redis state complete, the v2 input offset is only a
	// recovery cursor. Let Sarama batch broker commits; a crash may replay a
	// bounded interval, but cannot lose a completed event or state write.
	saramaConfig.Consumer.Offsets.AutoCommit.Enable = true
	return saramaConfig, nil
}

func newOwnedEvaluationService(
	topic string,
	group consumerGroup,
	client serviceClient,
	router alarmdcoordinator.MessageOutcomeRouter,
	critical alarmdcoordinator.CriticalCompletion,
	offsets OffsetCommitter,
	receipts alarmdcoordinator.ReceiptPublisher,
	gate *alarmdcoordinator.CriticalDependencyGate,
	runnerLimits alarmdcoordinator.ConcurrentRunnerLimits,
	diagnostics EvaluationDiagnostics,
	retryConfig alarmdcoordinator.DependencyRetryConfig,
	drainTimeout time.Duration,
) (*Service, error) {
	if router == nil || critical == nil || offsets == nil || receipts == nil || gate == nil {
		return nil, errors.New("kafka evaluation service: router, completion, offsets, receipts and dependency gate are required")
	}
	if diagnostics.OnRejected == nil {
		return nil, errors.New("kafka evaluation service: rejected evidence observer is required")
	}
	return newOwnedGroupService(
		[]string{topic}, group, client,
		func(reportFatal func(error)) (serviceHandler, error) {
			return NewEvaluationHandlerWithRunnerLimits(
				router, critical, offsets, receipts, gate, retryConfig, diagnostics, runnerLimits, drainTimeout, reportFatal,
			)
		},
		drainTimeout,
	)
}
