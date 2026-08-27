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
	"sync"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/consumer"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	alarmdcoordinator "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
)

// EvaluationHandler binds the v2 alarmd completion boundary to one Sarama
// assignment. Each claim owns an independent partition completion tracker.
type EvaluationHandler struct {
	router      alarmdcoordinator.MessageOutcomeRouter
	critical    alarmdcoordinator.CriticalCompletion
	offsets     OffsetCommitter
	receipts    alarmdcoordinator.ReceiptPublisher
	gate        *alarmdcoordinator.CriticalDependencyGate
	offsetRetry *alarmdcoordinator.DependencyRetry
	diagnostics EvaluationDiagnostics
	assignment  *assignmentLifecycle
	reportFatal func(error)
	fatalOnce   sync.Once
}

type RejectedMessageEvidence struct {
	Topic       string
	Partition   int32
	Offset      int64
	ReasonCodes []string
}

type EvaluationDiagnostics struct {
	OnRejected     func(RejectedMessageEvidence)
	OnOffsetMarked func(OffsetMarkEvidence)
}

type OffsetMarkEvidence struct {
	Topic      string
	Partition  int32
	NextOffset int64
	Duration   time.Duration
	Err        error
}

func NewEvaluationHandlerWithDiagnostics(
	router alarmdcoordinator.MessageOutcomeRouter,
	critical alarmdcoordinator.CriticalCompletion,
	offsets OffsetCommitter,
	receipts alarmdcoordinator.ReceiptPublisher,
	gate *alarmdcoordinator.CriticalDependencyGate,
	retryConfig alarmdcoordinator.DependencyRetryConfig,
	diagnostics EvaluationDiagnostics,
	reportFatal func(error),
) (*EvaluationHandler, error) {
	if router == nil || critical == nil || offsets == nil || receipts == nil || gate == nil {
		return nil, errors.New("kafka evaluation handler: router, completion, offsets, receipts and dependency gate are required")
	}
	if diagnostics.OnRejected == nil {
		return nil, errors.New("kafka evaluation handler: rejected evidence observer is required")
	}
	offsetRetry, err := alarmdcoordinator.NewDependencyRetry(gate, alarmdcoordinator.DependencyBlocker{
		Dependency: alarmdcoordinator.DependencyInputKafka, ReasonCode: contract.ReasonKafkaUnavailable,
	}, retryConfig)
	if err != nil {
		return nil, fmt.Errorf("kafka evaluation handler: offset dependency retry: %w", err)
	}
	return &EvaluationHandler{
		router: router, critical: critical, offsets: offsets, receipts: receipts,
		gate: gate, offsetRetry: offsetRetry, diagnostics: diagnostics,
		assignment: newAssignmentLifecycle(), reportFatal: reportFatal,
	}, nil
}

func (handler *EvaluationHandler) Setup(session sarama.ConsumerGroupSession) error {
	if handler == nil || handler.assignment == nil {
		return errors.New("kafka evaluation handler: initialized handler is required")
	}
	if err := handler.assignment.Setup(session); err != nil {
		handler.fatal(err)
		return err
	}
	return nil
}

func (handler *EvaluationHandler) Cleanup(session sarama.ConsumerGroupSession) error {
	if handler == nil || handler.assignment == nil {
		return errors.New("kafka evaluation handler: initialized handler is required")
	}
	return handler.assignment.Cleanup(session)
}

func (handler *EvaluationHandler) BeginDrain() <-chan struct{} {
	if handler == nil || handler.assignment == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return handler.assignment.BeginDrain()
}

func (handler *EvaluationHandler) Ready() bool {
	return handler != nil && handler.assignment != nil && handler.gate != nil &&
		handler.assignment.Ready() && handler.gate.Ready()
}

func (handler *EvaluationHandler) serviceSnapshot() assignmentSnapshot {
	if handler == nil || handler.assignment == nil {
		return assignmentSnapshot{}
	}
	snapshot := handler.assignment.Snapshot()
	if handler.gate == nil || !handler.gate.Ready() {
		snapshot.ready = false
	}
	return snapshot
}

func (handler *EvaluationHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	if handler == nil || handler.assignment == nil || handler.gate == nil || session == nil || claim == nil {
		return errors.New("kafka evaluation handler: handler, session and claim are required")
	}
	offsets := evaluationPartitionOffsetCommitter{
		session: session, offsets: handler.offsets, topic: claim.Topic(), partition: claim.Partition(),
		retry: handler.offsetRetry, onMarked: handler.diagnostics.OnOffsetMarked,
	}
	runner, err := alarmdcoordinator.NewRoutedPartitionRunnerWithObserver(
		handler.router, handler.critical, offsets, handler.receipts,
		evaluationRejectedObserver{handler: handler, topic: claim.Topic(), partition: claim.Partition()},
	)
	if err != nil {
		handler.fatal(err)
		return err
	}
	if err := handler.assignment.ClaimInitialized(session, claim); err != nil {
		if errors.Is(err, errAssignmentInactive) {
			return handler.waitForDrainOrSession(session)
		}
		handler.fatal(err)
		return err
	}

	for {
		if err := handler.gate.WaitAdmission(session.Context()); err != nil {
			if session.Context().Err() != nil && errors.Is(err, session.Context().Err()) {
				return nil
			}
			handler.fatal(err)
			return err
		}
		select {
		case <-session.Context().Done():
			return nil
		case <-handler.assignment.Drained():
			return nil
		case message, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			if message == nil || message.Topic != claim.Topic() || message.Partition != claim.Partition() {
				err := errors.New("kafka evaluation handler: message coordinates do not match claim")
				handler.fatal(err)
				return err
			}
			if err := validateOffset(message.Offset); err != nil {
				handler.fatal(err)
				return err
			}
			if !handler.assignment.TryBeginObservedRecord(session, claim, message.Offset) {
				return handler.waitForDrainOrSession(session)
			}
			err := runner.Process(session.Context(), message.Offset, message.Value)
			handler.assignment.EndObservedRecord(session, claim, message.Offset+1, err == nil)
			if err != nil {
				if session.Context().Err() != nil && errors.Is(err, session.Context().Err()) {
					return nil
				}
				handler.fatal(err)
				return err
			}
		}
	}
}

type evaluationRejectedObserver struct {
	handler   *EvaluationHandler
	topic     string
	partition int32
}

func (observer evaluationRejectedObserver) ObserveRejected(offset int64, rejected alarmdcoordinator.RejectedOutcome) {
	if observer.handler == nil {
		return
	}
	reasons := make([]string, len(rejected.Terminals))
	for index := range rejected.Terminals {
		reasons[index] = rejected.Terminals[index].ReasonCode
	}
	observer.handler.diagnostics.OnRejected(RejectedMessageEvidence{
		Topic: observer.topic, Partition: observer.partition, Offset: offset, ReasonCodes: reasons,
	})
}

func (handler *EvaluationHandler) waitForDrainOrSession(session sarama.ConsumerGroupSession) error {
	select {
	case <-session.Context().Done():
		return nil
	case <-handler.assignment.Drained():
		return nil
	}
}

func (handler *EvaluationHandler) fatal(err error) {
	if handler.assignment != nil {
		handler.assignment.Fail()
	}
	if handler.reportFatal != nil {
		handler.fatalOnce.Do(func() { handler.reportFatal(err) })
	}
}

type evaluationPartitionOffsetCommitter struct {
	session   sarama.ConsumerGroupSession
	offsets   OffsetCommitter
	topic     string
	partition int32
	retry     *alarmdcoordinator.DependencyRetry
	onMarked  func(OffsetMarkEvidence)
}

// evaluationSessionOffsetCommitter leaves the broker exchange to Sarama's
// periodic consumer-group commit. evaluationPartitionOffsetCommitter performs
// the MarkOffset call only after TriggerEvent and Redis state have completed.
type evaluationSessionOffsetCommitter struct{}

func (evaluationSessionOffsetCommitter) CommitOffset(
	ctx context.Context,
	session sarama.ConsumerGroupSession,
	record consumer.Record,
) error {
	if session == nil {
		return errors.New("kafka evaluation handler: consumer group session is required")
	}
	if err := validateOffset(record.Offset); err != nil {
		return err
	}
	return ctx.Err()
}

func (committer evaluationPartitionOffsetCommitter) CommitThrough(ctx context.Context, nextOffset int64) (returnErr error) {
	started := time.Now()
	defer func() {
		if committer.onMarked != nil {
			committer.onMarked(OffsetMarkEvidence{
				Topic: committer.topic, Partition: committer.partition, NextOffset: nextOffset,
				Duration: time.Since(started), Err: returnErr,
			})
		}
	}()
	if committer.session == nil || committer.offsets == nil || committer.topic == "" ||
		committer.retry == nil || nextOffset <= 0 {
		return errors.New("kafka evaluation handler: initialized partition offset committer is required")
	}
	record := consumer.Record{Topic: committer.topic, Partition: committer.partition, Offset: nextOffset - 1}
	err := committer.retry.Do(ctx, func(ctx context.Context) error {
		err := committer.offsets.CommitOffset(ctx, committer.session, record)
		if !isRetryableOffsetCommitDependency(err) {
			return err
		}
		return &alarmdcoordinator.RetryableDependencyError{Err: err}
	})
	if err != nil {
		return fmt.Errorf("kafka evaluation handler: commit through %d: %w", nextOffset, err)
	}
	committer.session.MarkOffset(committer.topic, committer.partition, nextOffset, "")
	return nil
}

func isRetryableOffsetCommitDependency(err error) bool {
	if err == nil {
		return false
	}
	var dependencyErr interface{ RetryableOffsetCommitDependency() }
	return errors.As(err, &dependencyErr) && dependencyErr != nil
}
