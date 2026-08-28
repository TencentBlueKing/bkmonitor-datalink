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
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	alarmdcoordinator "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
)

func TestNewEvaluationSaramaConfigEnablesPeriodicOffsetCommit(t *testing.T) {
	t.Parallel()

	config, err := newEvaluationSaramaConfig(Config{
		Brokers: []string{"127.0.0.1:9092"}, Topic: "execution-envelope", GroupID: "alarmd-v2",
		ClientID: "alarmd-v2", BrokerVersion: "0.10.2.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !config.Consumer.Offsets.AutoCommit.Enable {
		t.Fatal("evaluation consumer must periodically commit completed session offsets")
	}
}

func TestNewOwnedEvaluationServiceBuildsV2Handler(t *testing.T) {
	t.Parallel()

	group := newFakeConsumerGroup(func(context.Context, []string, sarama.ConsumerGroupHandler) error { return nil })
	service, err := newOwnedEvaluationService(
		"execution-envelope", group, &fakeServiceClient{},
		evaluationMessageRouterFunc(func(context.Context, []byte) (alarmdcoordinator.MessageOutcome, error) {
			return alarmdcoordinator.MessageOutcome{Kind: alarmdcoordinator.MessageOutcomeRejected, Rejected: &alarmdcoordinator.RejectedOutcome{}}, nil
		}),
		evaluationCriticalCompletionFunc(func(context.Context, alarmdcoordinator.CriticalResult) error { return nil }),
		fakeSyncOffsetCommitter{},
		evaluationReceiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
		alarmdcoordinator.NewCriticalDependencyGate(nil), alarmdcoordinator.DefaultConcurrentRunnerLimits(), EvaluationDiagnostics{
			OnRejected: func(RejectedMessageEvidence) {},
		}, evaluationRetryConfig(), time.Second,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := service.handler.(*EvaluationHandler); !ok {
		t.Fatalf("service handler = %T, want *EvaluationHandler", service.handler)
	}
}

func TestNewOwnedEvaluationServiceRejectsMissingCompletionDependencies(t *testing.T) {
	t.Parallel()

	if _, err := newOwnedEvaluationService(
		"execution-envelope", newFakeConsumerGroup(func(context.Context, []string, sarama.ConsumerGroupHandler) error { return nil }),
		&fakeServiceClient{}, nil, nil, fakeSyncOffsetCommitter{},
		evaluationReceiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
		alarmdcoordinator.NewCriticalDependencyGate(nil), alarmdcoordinator.DefaultConcurrentRunnerLimits(), EvaluationDiagnostics{
			OnRejected: func(RejectedMessageEvidence) {},
		}, evaluationRetryConfig(), time.Second,
	); err == nil {
		t.Fatal("newOwnedEvaluationService() accepted missing router and critical completion")
	}
}

func TestNewOwnedEvaluationServiceRejectsMissingRejectedEvidenceObserver(t *testing.T) {
	t.Parallel()

	_, err := newOwnedEvaluationService(
		"execution-envelope",
		newFakeConsumerGroup(func(context.Context, []string, sarama.ConsumerGroupHandler) error { return nil }),
		&fakeServiceClient{},
		evaluationMessageRouterFunc(func(context.Context, []byte) (alarmdcoordinator.MessageOutcome, error) {
			return alarmdcoordinator.MessageOutcome{}, nil
		}),
		evaluationCriticalCompletionFunc(func(context.Context, alarmdcoordinator.CriticalResult) error { return nil }),
		fakeSyncOffsetCommitter{},
		evaluationReceiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
		alarmdcoordinator.NewCriticalDependencyGate(nil), alarmdcoordinator.DefaultConcurrentRunnerLimits(), EvaluationDiagnostics{}, evaluationRetryConfig(), time.Second,
	)
	if err == nil {
		t.Fatal("newOwnedEvaluationService() accepted a missing rejected evidence observer")
	}
}

func TestNewOwnedEvaluationServiceRejectsInvalidDependencyRetryConfig(t *testing.T) {
	t.Parallel()

	_, err := newOwnedEvaluationService(
		"execution-envelope",
		newFakeConsumerGroup(func(context.Context, []string, sarama.ConsumerGroupHandler) error { return nil }),
		&fakeServiceClient{},
		evaluationMessageRouterFunc(func(context.Context, []byte) (alarmdcoordinator.MessageOutcome, error) {
			return alarmdcoordinator.MessageOutcome{}, nil
		}),
		evaluationCriticalCompletionFunc(func(context.Context, alarmdcoordinator.CriticalResult) error { return nil }),
		fakeSyncOffsetCommitter{},
		evaluationReceiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
		alarmdcoordinator.NewCriticalDependencyGate(nil), alarmdcoordinator.DefaultConcurrentRunnerLimits(), EvaluationDiagnostics{
			OnRejected: func(RejectedMessageEvidence) {},
		}, alarmdcoordinator.DependencyRetryConfig{}, time.Second,
	)
	if err == nil {
		t.Fatal("newOwnedEvaluationService() accepted an invalid dependency retry config")
	}
}
