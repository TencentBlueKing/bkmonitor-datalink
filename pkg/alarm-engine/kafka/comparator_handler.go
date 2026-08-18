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
	"sync"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/consumer"
)

type comparatorHandlerSession struct {
	handle      *comparatorAssignmentHandle
	initOnce    sync.Once
	initialized chan struct{}
	records     *comparatorRecordCoordinator
	barrier     *comparatorBarrierAdapter
	initErr     error
	stopOnce    sync.Once
	stopBarrier chan struct{}
	barrierDone chan struct{}
}

type comparatorHandler struct {
	assignment      *comparatorAssignmentCoordinator
	offsets         OffsetCommitter
	audits          ComparisonAuditSink
	barrierInterval time.Duration
	reportFatal     func(error)
	fatalOnce       sync.Once

	mu        sync.Mutex
	state     *comparatorHandlerSession
	ready     bool
	draining  bool
	inflight  int
	drained   chan struct{}
	drainOnce sync.Once
}

func newComparatorHandler(
	assignment *comparatorAssignmentCoordinator,
	offsets OffsetCommitter,
	audits ComparisonAuditSink,
	barrierInterval time.Duration,
	reportFatal func(error),
) (*comparatorHandler, error) {
	if assignment == nil || offsets == nil || audits == nil {
		return nil, errors.New("kafka comparator handler: assignment, offset committer and audit sink are required")
	}
	if barrierInterval <= 0 {
		return nil, errors.New("kafka comparator handler: barrier interval must be positive")
	}
	return &comparatorHandler{
		assignment: assignment, offsets: offsets, audits: audits, barrierInterval: barrierInterval,
		reportFatal: reportFatal, drained: make(chan struct{}),
	}, nil
}

func (h *comparatorHandler) Setup(session sarama.ConsumerGroupSession) error {
	if h == nil || session == nil {
		return errors.New("kafka comparator handler: initialized handler and session are required")
	}
	handle, err := h.assignment.Setup(session)
	if err != nil {
		h.fatal(err)
		return err
	}
	state := &comparatorHandlerSession{
		handle: handle, initialized: make(chan struct{}), stopBarrier: make(chan struct{}), barrierDone: make(chan struct{}),
	}
	h.mu.Lock()
	if h.draining {
		h.mu.Unlock()
		_ = h.assignment.Cleanup(handle, session)
		return errors.New("kafka comparator handler: handler is draining")
	}
	h.state = state
	h.ready = false
	h.mu.Unlock()
	return nil
}

func (h *comparatorHandler) Cleanup(session sarama.ConsumerGroupSession) error {
	if h == nil || session == nil {
		return errors.New("kafka comparator handler: initialized handler and session are required")
	}
	state := h.currentState()
	if state == nil {
		return nil
	}
	h.stopBarrier(state)
	err := h.assignment.Cleanup(state.handle, session)
	h.mu.Lock()
	if h.state == state {
		h.ready = false
		h.state = nil
	}
	h.mu.Unlock()
	return err
}

func (h *comparatorHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	if h == nil || session == nil || claim == nil {
		return errors.New("kafka comparator handler: handler, session and claim are required")
	}
	state := h.currentState()
	if state == nil {
		return errors.New("kafka comparator handler: session is not set up")
	}
	if err := h.assignment.RegisterClaim(state.handle, session, claim); err != nil {
		h.fatal(err)
		return err
	}
	if _, _, err := h.assignment.WaitReady(session.Context(), state.handle, session); err != nil {
		if session.Context().Err() != nil {
			return nil
		}
		h.fatal(err)
		return err
	}
	state.initOnce.Do(func() {
		state.records, state.initErr = newComparatorRecordCoordinator(
			h.assignment, state.handle, session, h.offsets, h.audits,
		)
		if state.initErr == nil {
			state.barrier, state.initErr = newComparatorBarrierAdapter(state.records)
		}
		if state.initErr == nil {
			h.mu.Lock()
			if h.state == state && !h.draining {
				h.ready = true
			}
			h.mu.Unlock()
			go h.runBarrier(session.Context(), state)
		} else {
			close(state.barrierDone)
		}
		close(state.initialized)
	})
	<-state.initialized
	if state.initErr != nil {
		h.fatal(state.initErr)
		return state.initErr
	}

	for {
		select {
		case <-session.Context().Done():
			return nil
		case <-h.drained:
			return nil
		case message, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			if message == nil || message.Topic != claim.Topic() || message.Partition != claim.Partition() {
				err := errors.New("kafka comparator handler: message coordinates do not match claim")
				h.fatal(err)
				return err
			}
			if err := validateOffset(message.Offset); err != nil {
				h.fatal(err)
				return err
			}
			if !h.beginRecord() {
				return nil
			}
			_, err := state.records.Process(session.Context(), consumer.Record{
				Topic: message.Topic, Partition: message.Partition, Offset: message.Offset, Key: message.Key, Value: message.Value,
			})
			h.endRecord()
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

func (h *comparatorHandler) BeginDrain() <-chan struct{} {
	if h == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	h.mu.Lock()
	h.draining = true
	h.ready = false
	state := h.state
	if h.inflight == 0 {
		h.drainOnce.Do(func() { close(h.drained) })
	}
	h.mu.Unlock()
	if state != nil {
		h.stopBarrier(state)
	}
	return h.drained
}

func (h *comparatorHandler) Ready() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ready && !h.draining
}

func (h *comparatorHandler) serviceSnapshot() assignmentSnapshot {
	if h == nil {
		return assignmentSnapshot{}
	}
	h.mu.Lock()
	snapshot := assignmentSnapshot{
		ready: h.ready && !h.draining, draining: h.draining, inflightRecords: h.inflight,
	}
	state := h.state
	h.mu.Unlock()
	if state != nil && state.handle != nil && state.handle.generation != nil {
		h.assignment.mu.Lock()
		if h.assignment.current == state.handle.generation {
			snapshot.assignedClaims = len(state.handle.generation.assignments)
		}
		h.assignment.mu.Unlock()
	}
	return snapshot
}

func (h *comparatorHandler) currentState() *comparatorHandlerSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

func (h *comparatorHandler) beginRecord() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.draining {
		return false
	}
	h.inflight++
	return true
}

func (h *comparatorHandler) endRecord() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.inflight > 0 {
		h.inflight--
	}
	if h.draining && h.inflight == 0 {
		h.drainOnce.Do(func() { close(h.drained) })
	}
}

func (h *comparatorHandler) runBarrier(ctx context.Context, state *comparatorHandlerSession) {
	var fatalErr error
	defer func() {
		close(state.barrierDone)
		if fatalErr != nil {
			h.fatal(fatalErr)
		}
	}()
	ticker := time.NewTicker(h.barrierInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-state.stopBarrier:
			return
		case <-ticker.C:
			if _, err := state.barrier.CaptureOverdue(ctx); err != nil {
				if ctx.Err() == nil {
					fatalErr = err
				}
				return
			}
		}
	}
}

func (h *comparatorHandler) stopBarrier(state *comparatorHandlerSession) {
	state.stopOnce.Do(func() { close(state.stopBarrier) })
	select {
	case <-state.barrierDone:
	case <-time.After(h.barrierInterval):
	}
}

func (h *comparatorHandler) fatal(err error) {
	if err == nil {
		return
	}
	h.BeginDrain()
	if h.reportFatal != nil {
		h.fatalOnce.Do(func() { h.reportFatal(err) })
	}
}
