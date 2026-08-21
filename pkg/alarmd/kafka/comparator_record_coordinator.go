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

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/comparator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/consumer"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type ComparisonAuditSink interface {
	WriteBatch(context.Context, *contract.ComparisonAuditBatch) error
}

// comparatorRecordCoordinator serializes every claim in one assignment around
// the Run's single Prepare-to-commit boundary.
type comparatorRecordCoordinator struct {
	mu sync.Mutex

	assignment *comparatorAssignmentCoordinator
	handle     *comparatorAssignmentHandle
	session    sarama.ConsumerGroupSession
	offsets    OffsetCommitter
	audits     ComparisonAuditSink
	run        *comparator.Run
	epoch      string
	failed     error
}

func newComparatorRecordCoordinator(
	assignment *comparatorAssignmentCoordinator,
	handle *comparatorAssignmentHandle,
	session sarama.ConsumerGroupSession,
	offsets OffsetCommitter,
	audits ComparisonAuditSink,
) (*comparatorRecordCoordinator, error) {
	if assignment == nil || handle == nil || handle.generation == nil || session == nil || offsets == nil || audits == nil {
		return nil, errors.New("kafka comparator record: assignment, handle, session, offset committer and audit sink are required")
	}
	id := sessionAssignmentID(session)
	assignment.mu.Lock()
	generation, err := assignment.currentGenerationLocked(handle, id)
	if err == nil {
		switch {
		case !generation.active:
			err = generationError(generation)
		case generation.run == nil:
			err = errors.New("kafka comparator record: assignment Run is not ready")
		case generation.recordOwner:
			err = errors.New("kafka comparator record: assignment already has a record coordinator")
		}
	}
	if err != nil {
		assignment.mu.Unlock()
		return nil, err
	}
	run, epoch := generation.run, generation.epoch
	generation.recordOwner = true
	assignment.mu.Unlock()
	return &comparatorRecordCoordinator{
		assignment: assignment,
		handle:     handle,
		session:    session,
		offsets:    offsets,
		audits:     audits,
		run:        run,
		epoch:      epoch,
	}, nil
}

func (c *comparatorRecordCoordinator) Process(ctx context.Context, record consumer.Record) ([]comparator.Update, error) {
	if c == nil || ctx == nil {
		return nil, errors.New("kafka comparator record: initialized coordinator and context are required")
	}
	contextErrBeforeLock := ctx.Err()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failed != nil {
		return nil, c.failed
	}
	sessionContext := c.session.Context()
	if sessionContext == nil {
		return nil, c.fail(errors.New("kafka comparator record: session context is required"))
	}
	if err := sessionContext.Err(); err != nil {
		return nil, c.fail(err)
	}
	if contextErrBeforeLock != nil {
		return nil, c.fail(contextErrBeforeLock)
	}
	if err := ctx.Err(); err != nil {
		return nil, c.fail(err)
	}
	if err := validateOffset(record.Offset); err != nil {
		return nil, c.fail(err)
	}
	role, err := c.acquireRecord(record)
	if err != nil {
		return nil, c.fail(err)
	}
	defer c.releaseOperation()
	prepared, err := c.run.Prepare(comparator.StreamRecord{
		Epoch:     c.epoch,
		Role:      role,
		Topic:     record.Topic,
		Partition: record.Partition,
		Offset:    record.Offset,
		Key:       record.Key,
		Value:     record.Value,
	})
	if err != nil {
		return nil, c.fail(err)
	}
	auditBatches, err := c.run.PreviewAudits(prepared, comparator.Gates{StableEpoch: true})
	if err != nil {
		return nil, c.fail(err)
	}
	for _, auditBatch := range auditBatches {
		if err := c.audits.WriteBatch(ctx, auditBatch); err != nil {
			failure := errors.Join(err, c.run.Abort(prepared, err))
			return nil, c.fail(failure)
		}
	}
	if err := c.offsets.CommitOffset(ctx, c.session, record); err != nil {
		failure := errors.Join(err, c.run.Abort(prepared, err))
		return nil, c.fail(failure)
	}
	updates, err := c.run.CommitSucceeded(prepared)
	if err != nil {
		return nil, c.fail(err)
	}
	c.session.MarkOffset(record.Topic, record.Partition, record.Offset+1, "")
	return updates, nil
}

func (c *comparatorRecordCoordinator) acquireRecord(record consumer.Record) (comparator.StreamRole, error) {
	id := sessionAssignmentID(c.session)
	c.assignment.mu.Lock()
	defer c.assignment.mu.Unlock()
	generation, err := c.assignment.currentGenerationLocked(c.handle, id)
	if err != nil {
		return 0, err
	}
	if !generation.active || generation.run != c.run || generation.epoch != c.epoch {
		return 0, generationError(generation)
	}
	role, ok := generation.expected[assignmentClaim{topic: record.Topic, partition: record.Partition}]
	if !ok {
		return 0, errors.New("kafka comparator record: record is outside the frozen assignment")
	}
	generation.inflight++
	return role, nil
}

func (c *comparatorRecordCoordinator) acquireBarrierOperation() ([]comparator.PartitionAssignment, error) {
	id := sessionAssignmentID(c.session)
	c.assignment.mu.Lock()
	defer c.assignment.mu.Unlock()
	generation, err := c.assignment.currentGenerationLocked(c.handle, id)
	if err != nil {
		return nil, err
	}
	if !generation.active || generation.run != c.run || generation.epoch != c.epoch {
		return nil, generationError(generation)
	}
	assignments := make([]comparator.PartitionAssignment, 0, len(generation.assignments))
	for _, assignment := range generation.assignments {
		assignments = append(assignments, assignment)
	}
	generation.inflight++
	return assignments, nil
}

func (c *comparatorRecordCoordinator) releaseOperation() {
	c.assignment.mu.Lock()
	defer c.assignment.mu.Unlock()
	generation := c.handle.generation
	if generation.inflight <= 0 {
		return
	}
	generation.inflight--
	if !generation.active {
		c.assignment.invalidateGenerationIfIdleLocked(generation)
	}
}

func (c *comparatorRecordCoordinator) fail(cause error) error {
	if cause == nil {
		cause = errors.New("kafka comparator record: assignment failed without a cause")
	}
	c.failed = fmt.Errorf("kafka comparator record: %w", cause)
	c.assignment.mu.Lock()
	if c.assignment.current == c.handle.generation && c.handle.generation.active {
		c.assignment.failGenerationLocked(c.handle.generation, c.failed)
	}
	c.assignment.mu.Unlock()
	return c.failed
}
