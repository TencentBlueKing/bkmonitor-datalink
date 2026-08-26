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
	"context"
	"errors"
)

// MessageOutcomeRouter converts one raw Kafka value into either a completed
// business result or a message-level rejection.
type MessageOutcomeRouter interface {
	Route(context.Context, []byte) (MessageOutcome, error)
}

// RoutedPartitionRunner owns the complete boundary for one Kafka partition.
// A rejected message has no trustworthy business receipt, but it is complete
// once the typed rejection has been recorded by the adapter/router path.
type RoutedPartitionRunner struct {
	router    MessageOutcomeRouter
	critical  CriticalCompletion
	tracker   *PartitionCompletionTracker
	committer *PartitionCommitter
}

func NewRoutedPartitionRunner(
	router MessageOutcomeRouter,
	critical CriticalCompletion,
	offsets PartitionOffsetCommitter,
	receipts ReceiptPublisher,
) (*RoutedPartitionRunner, error) {
	if router == nil || critical == nil {
		return nil, errors.New("alarmd coordinator: router and critical completion are required")
	}
	tracker := NewPartitionCompletionTracker()
	committer, err := NewPartitionCommitter(tracker, offsets, receipts)
	if err != nil {
		return nil, err
	}
	return &RoutedPartitionRunner{router: router, critical: critical, tracker: tracker, committer: committer}, nil
}

func (runner *RoutedPartitionRunner) Process(ctx context.Context, offset int64, payload []byte) error {
	if runner == nil || runner.router == nil || runner.critical == nil || runner.tracker == nil || runner.committer == nil {
		return errors.New("alarmd coordinator: initialized routed partition runner is required")
	}
	if err := runner.tracker.Register(offset); err != nil {
		return err
	}
	return runner.processRegistered(ctx, offset, payload)
}

func (runner *RoutedPartitionRunner) Retry(ctx context.Context, offset int64, payload []byte) error {
	if runner == nil || runner.router == nil || runner.critical == nil || runner.tracker == nil || runner.committer == nil {
		return errors.New("alarmd coordinator: initialized routed partition runner is required")
	}
	if err := runner.tracker.requireRetryable(offset); err != nil {
		return err
	}
	return runner.processRegistered(ctx, offset, payload)
}

func (runner *RoutedPartitionRunner) processRegistered(ctx context.Context, offset int64, payload []byte) error {
	outcome, err := runner.router.Route(ctx, payload)
	if err != nil {
		return err
	}
	switch outcome.Kind {
	case MessageOutcomeRejected:
		if outcome.Message != nil || outcome.Rejected == nil {
			return errors.New("alarmd coordinator: invalid rejected message outcome")
		}
		if err := runner.tracker.Complete(offset, nil); err != nil {
			return err
		}
	case MessageOutcomeCompleted:
		if outcome.Message == nil || outcome.Rejected != nil || outcome.Message.Receipt == nil {
			return errors.New("alarmd coordinator: invalid completed message outcome")
		}
		if err := runner.critical.Complete(ctx, outcome.Message.CriticalResult); err != nil {
			return err
		}
		if err := runner.tracker.Complete(offset, outcome.Message.Receipt); err != nil {
			return err
		}
	default:
		return errors.New("alarmd coordinator: unsupported message outcome")
	}
	return runner.committer.CommitReady(ctx)
}

func (runner *RoutedPartitionRunner) CommitReady(ctx context.Context) error {
	if runner == nil || runner.committer == nil {
		return errors.New("alarmd coordinator: initialized routed partition runner is required")
	}
	return runner.committer.CommitReady(ctx)
}
