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

	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
)

type MessageEvaluator interface {
	EvaluateMessage(context.Context, *inputv2.EvaluationInput) (MessageResult, error)
}

type CriticalCompletion interface {
	Complete(context.Context, CriticalResult) error
}

type PartitionRunner struct {
	evaluator MessageEvaluator
	critical  CriticalCompletion
	tracker   *PartitionCompletionTracker
	committer *PartitionCommitter
}

func NewPartitionRunner(
	evaluator MessageEvaluator,
	critical CriticalCompletion,
	offsets PartitionOffsetCommitter,
	receipts ReceiptPublisher,
) (*PartitionRunner, error) {
	if evaluator == nil || critical == nil {
		return nil, errors.New("alarmd coordinator: evaluator and critical completion are required")
	}
	tracker := NewPartitionCompletionTracker()
	committer, err := NewPartitionCommitter(tracker, offsets, receipts)
	if err != nil {
		return nil, err
	}
	return &PartitionRunner{evaluator: evaluator, critical: critical, tracker: tracker, committer: committer}, nil
}

func (runner *PartitionRunner) Process(ctx context.Context, offset int64, input *inputv2.EvaluationInput) error {
	if runner == nil || runner.evaluator == nil || runner.critical == nil || runner.tracker == nil || runner.committer == nil {
		return errors.New("alarmd coordinator: initialized partition runner is required")
	}
	if err := runner.tracker.Register(offset); err != nil {
		return err
	}
	result, err := runner.evaluator.EvaluateMessage(ctx, input)
	if err != nil {
		return err
	}
	if err := runner.critical.Complete(ctx, result.CriticalResult); err != nil {
		return err
	}
	if err := runner.tracker.Complete(offset, result.Receipt); err != nil {
		return err
	}
	return runner.committer.CommitReady(ctx)
}

func (runner *PartitionRunner) CommitReady(ctx context.Context) error {
	if runner == nil || runner.committer == nil {
		return errors.New("alarmd coordinator: initialized partition runner is required")
	}
	return runner.committer.CommitReady(ctx)
}
