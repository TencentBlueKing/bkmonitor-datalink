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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/consumer"
)

type OffsetCommitter interface {
	CommitOffset(context.Context, sarama.ConsumerGroupSession, consumer.Record) error
}

// offsetCommitDependencyError marks only failures from the Kafka coordinator
// or offset commit broker exchange. Local validation and lifecycle invariants
// remain ordinary errors and must not enter dependency retry.
type offsetCommitDependencyError struct {
	err error
}

func (err *offsetCommitDependencyError) Error() string {
	if err == nil || err.err == nil {
		return "kafka offset commit: input dependency failure"
	}
	return err.err.Error()
}

func (err *offsetCommitDependencyError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.err
}

func (err *offsetCommitDependencyError) RetryableOffsetCommitDependency() {}

type coordinator interface {
	Coordinator(string) (offsetBroker, error)
}

type offsetBroker interface {
	CommitOffset(*sarama.OffsetCommitRequest) (*sarama.OffsetCommitResponse, error)
}

type clientCoordinator struct {
	client sarama.Client
}

func (c clientCoordinator) Coordinator(groupID string) (offsetBroker, error) {
	return c.client.Coordinator(groupID)
}

type SaramaOffsetCommitter struct {
	coordinator coordinator
	groupID     string
}

func NewSaramaOffsetCommitter(client sarama.Client, groupID string) (*SaramaOffsetCommitter, error) {
	if client == nil || groupID == "" {
		return nil, errors.New("kafka offset commit: client and group_id are required")
	}
	return &SaramaOffsetCommitter{coordinator: clientCoordinator{client: client}, groupID: groupID}, nil
}

func (c *SaramaOffsetCommitter) CommitOffset(ctx context.Context, session sarama.ConsumerGroupSession, record consumer.Record) error {
	if c == nil || c.coordinator == nil || c.groupID == "" || session == nil {
		return errors.New("kafka offset commit: initialized committer and session are required")
	}
	if err := validateOffset(record.Offset); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	broker, err := c.coordinator.Coordinator(c.groupID)
	if err != nil {
		return markOffsetCommitDependencyError(ctx, fmt.Errorf("kafka offset commit: get group coordinator: %w", err))
	}
	if broker == nil {
		return errors.New("kafka offset commit: group coordinator is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	request := &sarama.OffsetCommitRequest{
		Version:                 1,
		ConsumerGroup:           c.groupID,
		ConsumerGroupGeneration: session.GenerationID(),
		ConsumerID:              session.MemberID(),
	}
	request.AddBlock(record.Topic, record.Partition, record.Offset+1, sarama.ReceiveTime, "")
	response, err := broker.CommitOffset(request)
	if err != nil {
		return markOffsetCommitDependencyError(ctx, fmt.Errorf("kafka offset commit: broker request: %w", err))
	}
	if response == nil {
		return markOffsetCommitDependencyError(ctx, errors.New("kafka offset commit: empty broker response"))
	}
	partitions, ok := response.Errors[record.Topic]
	if !ok {
		return markOffsetCommitDependencyError(ctx, errors.New("kafka offset commit: response is missing topic"))
	}
	commitError, ok := partitions[record.Partition]
	if !ok {
		return markOffsetCommitDependencyError(ctx, errors.New("kafka offset commit: response is missing partition"))
	}
	if commitError != sarama.ErrNoError {
		return markOffsetCommitDependencyError(ctx, fmt.Errorf("kafka offset commit: broker rejected offset: %w", commitError))
	}
	return nil
}

func markOffsetCommitDependencyError(ctx context.Context, err error) error {
	if contextErr := ctx.Err(); contextErr != nil {
		err = errors.Join(contextErr, err)
	}
	return &offsetCommitDependencyError{err: err}
}
