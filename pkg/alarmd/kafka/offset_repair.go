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
)

type managedGroupOffsets interface {
	ManagePartition(string, int32) (sarama.PartitionOffsetManager, error)
	Close() error
}

type offsetRepairClient interface {
	Partitions(string) ([]int32, error)
	GetOffset(string, int32, int64) (int64, error)
}

type groupOffsetRepairer struct {
	client        offsetRepairClient
	topics        []string
	initialOffset int64
	openOffsets   func() (managedGroupOffsets, error)
	commitOffsets func(context.Context, []OffsetReset) error
}

func newGroupOffsetRepairer(client sarama.Client, groupID string, topics []string, initialOffset int64) *groupOffsetRepairer {
	return &groupOffsetRepairer{
		client: client, topics: append([]string(nil), topics...), initialOffset: initialOffset,
		openOffsets: func() (managedGroupOffsets, error) {
			return sarama.NewOffsetManagerFromClient(groupID, client)
		},
		commitOffsets: (&groupOffsetResetCommitter{
			coordinator: clientCoordinator{client: client}, groupID: groupID,
		}).Commit,
	}
}

func (r *groupOffsetRepairer) Repair(ctx context.Context) ([]OffsetReset, error) {
	if r == nil || r.client == nil || r.openOffsets == nil || r.commitOffsets == nil {
		return nil, errors.New("kafka offset repair: initialized repairer is required")
	}
	if ctx == nil {
		return nil, errors.New("kafka offset repair: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manager, err := r.openOffsets()
	if err != nil {
		return nil, fmt.Errorf("kafka offset repair: open group offsets: %w", err)
	}
	events := make([]OffsetReset, 0)
	var scanErr error

scan:
	for _, topic := range r.topics {
		partitions, err := r.client.Partitions(topic)
		if err != nil {
			scanErr = fmt.Errorf("kafka offset repair: list partitions for %q: %w", topic, err)
			break
		}
		for _, partition := range partitions {
			if err := ctx.Err(); err != nil {
				scanErr = err
				break scan
			}
			partitionOffsets, err := manager.ManagePartition(topic, partition)
			if err != nil {
				scanErr = fmt.Errorf("kafka offset repair: manage %s/%d: %w", topic, partition, err)
				break scan
			}
			current, _ := partitionOffsets.NextOffset()
			oldest, err := r.client.GetOffset(topic, partition, sarama.OffsetOldest)
			if err != nil {
				scanErr = fmt.Errorf("kafka offset repair: read oldest %s/%d: %w", topic, partition, err)
				break scan
			}
			newest, err := r.client.GetOffset(topic, partition, sarama.OffsetNewest)
			if err != nil {
				scanErr = fmt.Errorf("kafka offset repair: read newest %s/%d: %w", topic, partition, err)
				break scan
			}
			if current >= oldest && current <= newest {
				continue
			}
			target := oldest
			if r.initialOffset == sarama.OffsetNewest {
				target = newest
			}
			events = append(events, OffsetReset{Topic: topic, Partition: partition, Offset: target})
		}
	}
	closeErr := manager.Close()
	if scanErr != nil {
		if closeErr != nil {
			return nil, fmt.Errorf("%w; close group offsets: %v", scanErr, closeErr)
		}
		return nil, scanErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("kafka offset repair: close group offsets: %w", closeErr)
	}
	if len(events) != 0 {
		if err := r.commitOffsets(ctx, events); err != nil {
			return nil, err
		}
	}
	return events, nil
}

type groupOffsetResetCommitter struct {
	coordinator coordinator
	groupID     string
}

func (c *groupOffsetResetCommitter) Commit(ctx context.Context, offsets []OffsetReset) error {
	if c == nil || c.coordinator == nil || c.groupID == "" {
		return errors.New("kafka offset repair: initialized committer is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	broker, err := c.coordinator.Coordinator(c.groupID)
	if err != nil {
		return fmt.Errorf("kafka offset repair: get group coordinator: %w", err)
	}
	if broker == nil {
		return errors.New("kafka offset repair: group coordinator is nil")
	}
	request := &sarama.OffsetCommitRequest{
		Version:                 1,
		ConsumerGroup:           c.groupID,
		ConsumerGroupGeneration: sarama.GroupGenerationUndefined,
	}
	for _, offset := range offsets {
		if offset.Topic == "" || offset.Partition < 0 || offset.Offset < 0 {
			return errors.New("kafka offset repair: invalid reset coordinate")
		}
		request.AddBlock(offset.Topic, offset.Partition, offset.Offset, sarama.ReceiveTime, "alarmd-offset-reset")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	response, err := broker.CommitOffset(request)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return fmt.Errorf("kafka offset repair: broker request: %w", err)
	}
	if response == nil {
		return errors.New("kafka offset repair: empty broker response")
	}
	for _, offset := range offsets {
		partitions, ok := response.Errors[offset.Topic]
		if !ok {
			return fmt.Errorf("kafka offset repair: response missing topic %q", offset.Topic)
		}
		commitError, ok := partitions[offset.Partition]
		if !ok {
			return fmt.Errorf("kafka offset repair: response missing partition %s/%d", offset.Topic, offset.Partition)
		}
		if commitError != sarama.ErrNoError {
			return fmt.Errorf("kafka offset repair: broker rejected %s/%d: %w", offset.Topic, offset.Partition, commitError)
		}
	}
	return nil
}
