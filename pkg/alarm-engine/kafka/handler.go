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
	"math"
	"sync"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/consumer"
)

type Handler struct {
	newProcessor consumer.ProcessorFactory
	offsets      OffsetCommitter
	assignment   *assignmentLifecycle
	reportFatal  func(error)
	fatalOnce    sync.Once
}

// NewHandler builds a Sarama claim adapter. Call BeginDrain synchronously to
// stop taking new records while allowing records already handed to Processors
// to finish. reportFatal, when provided, must not block.
func NewHandler(newProcessor consumer.ProcessorFactory, offsets OffsetCommitter, reportFatal func(error)) *Handler {
	handler := &Handler{
		newProcessor: newProcessor,
		offsets:      offsets,
		assignment:   newAssignmentLifecycle(),
		reportFatal:  reportFatal,
	}
	return handler
}

func (h *Handler) Setup(session sarama.ConsumerGroupSession) error {
	if h == nil || h.assignment == nil {
		return errors.New("kafka claim: initialized handler is required")
	}
	if err := h.assignment.Setup(session); err != nil {
		h.fatal(err)
		return err
	}
	return nil
}

func (h *Handler) Cleanup(session sarama.ConsumerGroupSession) error {
	if h == nil || h.assignment == nil {
		return errors.New("kafka claim: initialized handler is required")
	}
	return h.assignment.Cleanup(session)
}

func (h *Handler) BeginDrain() <-chan struct{} {
	if h == nil || h.assignment == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return h.assignment.BeginDrain()
}

func (h *Handler) Ready() bool {
	return h != nil && h.assignment != nil && h.assignment.Ready()
}

func (h *Handler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	if h == nil || session == nil || claim == nil {
		return errors.New("kafka claim: handler, session and claim are required")
	}
	claimProcessor, err := consumer.NewClaim(h.newProcessor, claimCommitter{session: session, offsets: h.offsets})
	if err != nil {
		h.fatal(err)
		return err
	}
	if err := h.assignment.ClaimInitialized(session, claim); err != nil {
		if errors.Is(err, errAssignmentInactive) {
			return h.waitForDrainOrSession(session)
		}
		h.fatal(err)
		return err
	}
	for {
		select {
		case <-session.Context().Done():
			return nil
		case <-h.assignment.Drained():
			return nil
		case message, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			if message == nil {
				err := errors.New("kafka claim: received nil message")
				h.fatal(err)
				return err
			}
			if message.Topic != claim.Topic() || message.Partition != claim.Partition() {
				err := errors.New("kafka claim: message coordinates do not match claim")
				h.fatal(err)
				return err
			}
			if err := validateOffset(message.Offset); err != nil {
				h.fatal(err)
				return err
			}
			if !h.assignment.TryBeginObservedRecord(session, claim, message.Offset) {
				return h.waitForDrainOrSession(session)
			}
			record := consumer.Record{
				Key: message.Key, Value: message.Value, Topic: message.Topic, Partition: message.Partition, Offset: message.Offset,
			}
			err := claimProcessor.Process(session.Context(), record)
			h.assignment.EndObservedRecord(session, claim, message.Offset+1, err == nil)
			if err != nil {
				if session.Context().Err() != nil && errors.Is(err, session.Context().Err()) {
					return nil
				}
				h.fatal(err)
				return err
			}
		}
	}
}

func (h *Handler) waitForDrainOrSession(session sarama.ConsumerGroupSession) error {
	select {
	case <-session.Context().Done():
		return nil
	case <-h.assignment.Drained():
		return nil
	}
}

func (h *Handler) fatal(err error) {
	if h.assignment != nil {
		h.assignment.Fail()
	}
	if h.reportFatal != nil {
		h.fatalOnce.Do(func() { h.reportFatal(err) })
	}
}

type claimCommitter struct {
	session sarama.ConsumerGroupSession
	offsets OffsetCommitter
}

func (c claimCommitter) CommitProcessed(ctx context.Context, record consumer.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateOffset(record.Offset); err != nil {
		return err
	}
	if c.offsets == nil {
		return errors.New("kafka claim: offset committer is required")
	}
	if err := c.offsets.CommitOffset(ctx, c.session, record); err != nil {
		return err
	}
	// Keep Sarama's session-local offset manager aligned with the offset already
	// acknowledged by the broker. A later session cleanup may safely recommit it.
	c.session.MarkOffset(record.Topic, record.Partition, record.Offset+1, "")
	return nil
}

func validateOffset(offset int64) error {
	if offset < 0 || offset == math.MaxInt64 {
		return fmt.Errorf("kafka claim: invalid record offset %d", offset)
	}
	return nil
}
