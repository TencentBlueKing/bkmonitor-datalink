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
	"errors"
	"fmt"
	"sync"

	"github.com/Shopify/sarama"
)

var errAssignmentInactive = errors.New("kafka assignment: session is not active")

type assignmentID struct {
	generation int32
	member     string
}

type assignmentClaim struct {
	topic     string
	partition int32
}

type claimCursor struct {
	nextOffset       int64
	minimumHighWater int64
	source           sarama.ConsumerGroupClaim
}

type assignmentSnapshot struct {
	ready              bool
	assignedClaims     int
	draining           bool
	inflightRecords    int
	consumerLagRecords int64
	consumerLagKnown   bool
}

type assignmentLifecycle struct {
	mu sync.Mutex

	current     assignmentID
	active      bool
	expected    map[assignmentClaim]struct{}
	initialized map[assignmentClaim]struct{}
	cursors     map[assignmentClaim]claimCursor
	inflight    int
	ready       bool
	draining    bool
	fatal       bool
	drained     chan struct{}
	drainedOnce sync.Once
}

func newAssignmentLifecycle() *assignmentLifecycle {
	return &assignmentLifecycle{drained: make(chan struct{})}
}

func (l *assignmentLifecycle) Setup(session sarama.ConsumerGroupSession) error {
	if l == nil || session == nil {
		return errors.New("kafka assignment: lifecycle and session are required")
	}
	expected, err := expectedClaims(session.Claims())
	if err != nil {
		return err
	}
	id := sessionAssignmentID(session)
	l.mu.Lock()
	l.current = id
	l.active = true
	l.expected = expected
	l.initialized = make(map[assignmentClaim]struct{}, len(expected))
	l.cursors = make(map[assignmentClaim]claimCursor, len(expected))
	l.ready = len(expected) == 0 && !l.draining && !l.fatal
	l.mu.Unlock()

	if done := session.Context().Done(); done != nil {
		go func() {
			<-done
			l.endAssignment(id)
		}()
	}
	return nil
}

func (l *assignmentLifecycle) Cleanup(session sarama.ConsumerGroupSession) error {
	if l == nil || session == nil {
		return errors.New("kafka assignment: lifecycle and session are required")
	}
	l.endAssignment(sessionAssignmentID(session))
	return nil
}

func (l *assignmentLifecycle) ClaimInitialized(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	if l == nil || session == nil || claim == nil {
		return errors.New("kafka assignment: lifecycle, session and claim are required")
	}
	id := sessionAssignmentID(session)
	claimID := assignmentClaim{topic: claim.Topic(), partition: claim.Partition()}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.draining || l.fatal {
		return errAssignmentInactive
	}
	if !l.active {
		return errAssignmentInactive
	}
	if l.current != id {
		return errAssignmentInactive
	}
	if _, ok := l.expected[claimID]; !ok {
		return fmt.Errorf("kafka assignment: claim %s/%d is not assigned", claimID.topic, claimID.partition)
	}
	l.initialized[claimID] = struct{}{}
	l.ready = len(l.initialized) == len(l.expected)
	return nil
}

func (l *assignmentLifecycle) TryBeginRecord(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) bool {
	return l.tryBeginRecord(session, claim, 0, false)
}

func (l *assignmentLifecycle) TryBeginObservedRecord(
	session sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim,
	nextOffset int64,
) bool {
	if nextOffset < 0 || claim == nil {
		return false
	}
	return l.tryBeginRecord(session, claim, nextOffset, true)
}

func (l *assignmentLifecycle) tryBeginRecord(
	session sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim,
	nextOffset int64,
	observeCursor bool,
) bool {
	if l == nil || session == nil || claim == nil {
		return false
	}
	id := sessionAssignmentID(session)
	claimID := assignmentClaim{topic: claim.Topic(), partition: claim.Partition()}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.draining || l.fatal {
		return false
	}
	if !l.active || l.current != id {
		return false
	}
	if _, ok := l.initialized[claimID]; !ok {
		return false
	}
	if observeCursor {
		l.cursors[claimID] = claimCursor{
			nextOffset: nextOffset, minimumHighWater: nextOffset + 1, source: claim,
		}
	}
	l.inflight++
	return true
}

func (l *assignmentLifecycle) EndRecord() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight > 0 {
		l.inflight--
	}
	l.closeDrainedIfIdle()
}

func (l *assignmentLifecycle) EndObservedRecord(
	session sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim,
	nextOffset int64,
	committed bool,
) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight > 0 {
		l.inflight--
	}
	if session != nil && claim != nil && l.active && l.current == sessionAssignmentID(session) {
		claimID := assignmentClaim{topic: claim.Topic(), partition: claim.Partition()}
		if cursor, ok := l.cursors[claimID]; ok {
			if committed {
				cursor.nextOffset = nextOffset
				cursor.minimumHighWater = nextOffset
			}
			l.cursors[claimID] = cursor
		}
	}
	l.closeDrainedIfIdle()
}

func (l *assignmentLifecycle) BeginDrain() <-chan struct{} {
	if l == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	l.mu.Lock()
	l.draining = true
	l.ready = false
	l.closeDrainedIfIdle()
	drained := l.drained
	l.mu.Unlock()
	return drained
}

func (l *assignmentLifecycle) Fail() <-chan struct{} {
	if l == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	l.mu.Lock()
	l.fatal = true
	l.draining = true
	l.ready = false
	l.closeDrainedIfIdle()
	drained := l.drained
	l.mu.Unlock()
	return drained
}

func (l *assignmentLifecycle) Ready() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ready
}

func (l *assignmentLifecycle) Snapshot() assignmentSnapshot {
	if l == nil {
		return assignmentSnapshot{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapshotLocked()
}

func (l *assignmentLifecycle) Drained() <-chan struct{} {
	if l == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return l.drained
}

func (l *assignmentLifecycle) endAssignment(id assignmentID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.active || l.current != id {
		return
	}
	l.active = false
	l.ready = false
	l.expected = nil
	l.initialized = nil
	l.cursors = nil
}

func (l *assignmentLifecycle) snapshotLocked() assignmentSnapshot {
	snapshot := assignmentSnapshot{
		ready:           l.ready,
		draining:        l.draining,
		inflightRecords: l.inflight,
	}
	if !l.active {
		return snapshot
	}
	snapshot.assignedClaims = len(l.expected)
	if len(l.expected) == 0 {
		snapshot.consumerLagKnown = true
		return snapshot
	}
	for claim := range l.expected {
		cursor, ok := l.cursors[claim]
		if !ok || cursor.source == nil {
			return snapshot
		}
		highWater := cursor.source.HighWaterMarkOffset()
		if highWater < cursor.minimumHighWater {
			return snapshot
		}
		if highWater > cursor.nextOffset {
			snapshot.consumerLagRecords += highWater - cursor.nextOffset
		}
	}
	snapshot.consumerLagKnown = true
	return snapshot
}

func (l *assignmentLifecycle) closeDrainedIfIdle() {
	if l.draining && l.inflight == 0 {
		l.drainedOnce.Do(func() { close(l.drained) })
	}
}

func sessionAssignmentID(session sarama.ConsumerGroupSession) assignmentID {
	return assignmentID{generation: session.GenerationID(), member: session.MemberID()}
}

func expectedClaims(claims map[string][]int32) (map[assignmentClaim]struct{}, error) {
	expected := make(map[assignmentClaim]struct{})
	for topic, partitions := range claims {
		for _, partition := range partitions {
			claim := assignmentClaim{topic: topic, partition: partition}
			if _, ok := expected[claim]; ok {
				return nil, fmt.Errorf("kafka assignment: duplicate claim %s/%d", topic, partition)
			}
			expected[claim] = struct{}{}
		}
	}
	return expected, nil
}
