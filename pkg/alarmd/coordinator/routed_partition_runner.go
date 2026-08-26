// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"bytes"
	"context"
	"errors"
)

// MessageOutcomeRouter converts one raw Kafka value into either a completed
// business result or a message-level rejection.
type MessageOutcomeRouter interface {
	Route(context.Context, []byte) (MessageOutcome, error)
}

type RejectedMessageObserver interface {
	ObserveRejected(offset int64, rejected RejectedOutcome)
}

// RoutedPartitionRunner owns the complete boundary for one Kafka partition.
// A rejected message has no trustworthy business receipt, but it is complete
// once the typed rejection has been recorded by the adapter/router path.
type RoutedPartitionRunner struct {
	router    MessageOutcomeRouter
	critical  CriticalCompletion
	rejected  RejectedMessageObserver
	tracker   *PartitionCompletionTracker
	committer *PartitionCommitter
	tasks     map[int64]*routedMessageTask
}

type routedMessageTask struct {
	payload      []byte
	outcome      *MessageOutcome
	eventsDone   bool
	stateDone    bool
	criticalDone bool
}

func NewRoutedPartitionRunner(
	router MessageOutcomeRouter,
	critical CriticalCompletion,
	offsets PartitionOffsetCommitter,
	receipts ReceiptPublisher,
) (*RoutedPartitionRunner, error) {
	return NewRoutedPartitionRunnerWithObserver(router, critical, offsets, receipts, nil)
}

func NewRoutedPartitionRunnerWithObserver(
	router MessageOutcomeRouter,
	critical CriticalCompletion,
	offsets PartitionOffsetCommitter,
	receipts ReceiptPublisher,
	rejected RejectedMessageObserver,
) (*RoutedPartitionRunner, error) {
	if router == nil || critical == nil {
		return nil, errors.New("alarmd coordinator: router and critical completion are required")
	}
	tracker := NewPartitionCompletionTracker()
	committer, err := NewPartitionCommitter(tracker, offsets, receipts)
	if err != nil {
		return nil, err
	}
	return &RoutedPartitionRunner{
		router: router, critical: critical, rejected: rejected, tracker: tracker, committer: committer,
		tasks: make(map[int64]*routedMessageTask),
	}, nil
}

func (runner *RoutedPartitionRunner) Process(ctx context.Context, offset int64, payload []byte) error {
	if runner == nil || runner.router == nil || runner.critical == nil || runner.tracker == nil || runner.committer == nil {
		return errors.New("alarmd coordinator: initialized routed partition runner is required")
	}
	if err := runner.tracker.Register(offset); err != nil {
		return err
	}
	runner.tasks[offset] = &routedMessageTask{payload: append([]byte(nil), payload...)}
	return runner.processRegistered(ctx, offset)
}

func (runner *RoutedPartitionRunner) Retry(ctx context.Context, offset int64, payload []byte) error {
	if runner == nil || runner.router == nil || runner.critical == nil || runner.tracker == nil || runner.committer == nil {
		return errors.New("alarmd coordinator: initialized routed partition runner is required")
	}
	if err := runner.tracker.requireRetryable(offset); err != nil {
		return err
	}
	task, ok := runner.tasks[offset]
	if !ok {
		return errors.New("alarmd coordinator: retry task is missing")
	}
	if !bytes.Equal(task.payload, payload) {
		return errors.New("alarmd coordinator: retry payload changed")
	}
	return runner.processRegistered(ctx, offset)
}

func (runner *RoutedPartitionRunner) processRegistered(ctx context.Context, offset int64) error {
	task, ok := runner.tasks[offset]
	if !ok {
		return errors.New("alarmd coordinator: registered task is missing")
	}
	if task.outcome == nil {
		outcome, err := runner.router.Route(ctx, task.payload)
		if err != nil {
			return err
		}
		task.outcome = &outcome
	}
	outcome := *task.outcome
	switch outcome.Kind {
	case MessageOutcomeRejected:
		if outcome.Message != nil || outcome.Rejected == nil {
			return errors.New("alarmd coordinator: invalid rejected message outcome")
		}
		if runner.rejected != nil {
			runner.rejected.ObserveRejected(offset, *outcome.Rejected)
		}
		if err := runner.tracker.Complete(offset, nil); err != nil {
			return err
		}
	case MessageOutcomeCompleted:
		if outcome.Message == nil || outcome.Rejected != nil || outcome.Message.Receipt == nil {
			return errors.New("alarmd coordinator: invalid completed message outcome")
		}
		if phased, ok := runner.critical.(CriticalPhaseCompletion); ok {
			if !task.eventsDone {
				if err := phased.CompleteEvents(ctx, outcome.Message.Events); err != nil {
					return err
				}
				task.eventsDone = true
			}
			if !task.stateDone {
				if err := phased.CompleteState(ctx, outcome.Message.StateWrite); err != nil {
					return err
				}
				task.stateDone = true
			}
		} else if !task.criticalDone {
			if err := runner.critical.Complete(ctx, outcome.Message.CriticalResult); err != nil {
				return err
			}
			task.criticalDone = true
		}
		if err := runner.tracker.Complete(offset, outcome.Message.Receipt); err != nil {
			return err
		}
	default:
		return errors.New("alarmd coordinator: unsupported message outcome")
	}
	delete(runner.tasks, offset)
	return runner.committer.CommitReady(ctx)
}

func (runner *RoutedPartitionRunner) CommitReady(ctx context.Context) error {
	if runner == nil || runner.committer == nil {
		return errors.New("alarmd coordinator: initialized routed partition runner is required")
	}
	return runner.committer.CommitReady(ctx)
}
