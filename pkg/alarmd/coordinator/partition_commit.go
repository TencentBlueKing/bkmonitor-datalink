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
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type PartitionOffsetCommitter interface {
	CommitThrough(context.Context, int64) error
}

type ReceiptPublisher interface {
	TryEnqueue(*contract.MessageReceiptV1) bool
}

type PartitionCommitter struct {
	tracker  *PartitionCompletionTracker
	offsets  PartitionOffsetCommitter
	receipts ReceiptPublisher
}

func NewPartitionCommitter(
	tracker *PartitionCompletionTracker,
	offsets PartitionOffsetCommitter,
	receipts ReceiptPublisher,
) (*PartitionCommitter, error) {
	if tracker == nil || offsets == nil || receipts == nil {
		return nil, errors.New("alarmd coordinator: tracker, offset committer and receipt publisher are required")
	}
	return &PartitionCommitter{tracker: tracker, offsets: offsets, receipts: receipts}, nil
}

func (committer *PartitionCommitter) CommitReady(ctx context.Context) error {
	if committer == nil || committer.tracker == nil || committer.offsets == nil || committer.receipts == nil {
		return errors.New("alarmd coordinator: initialized partition committer is required")
	}
	nextOffset, receipts, ok := committer.tracker.NextCommit()
	if !ok {
		return nil
	}
	if err := committer.offsets.CommitThrough(ctx, nextOffset); err != nil {
		return fmt.Errorf("alarmd coordinator: commit input offset %d: %w", nextOffset, err)
	}
	if err := committer.tracker.CommitACK(nextOffset); err != nil {
		return err
	}
	for _, receipt := range receipts {
		if receipt != nil {
			if !committer.receipts.TryEnqueue(receipt) {
				// Receipt is a fail-open audit. The publisher owns drop evidence;
				// a rejection must not invalidate the committed input offset.
				continue
			}
		}
	}
	return nil
}
