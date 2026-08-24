// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package comparator

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

var ErrRecordInFlight = errors.New("comparator: record is awaiting commit")

type StreamRole uint8

const (
	StreamInput StreamRole = iota + 1
	StreamGo
	StreamPython
)

type PartitionAssignment struct {
	Role       StreamRole
	Topic      string
	Partition  int32
	NextOffset int64
}

type StreamRecord struct {
	Epoch     string
	Role      StreamRole
	Topic     string
	Partition int32
	Offset    int64
	Key       []byte
	Value     []byte
}

// Prepared identifies the single record currently awaiting broker commit.
// Its fields are deliberately private so callers cannot fabricate a valid
// completion token.
type Prepared struct {
	owner *Run
	token uint64
}

type streamPartition struct {
	role      StreamRole
	topic     string
	partition int32
}

type preparedRecord struct {
	token          uint64
	stream         streamPartition
	offset         int64
	updates        []Update
	coverage       map[string]*coverageEntry
	observedAt     time.Time
	auditsPrepared bool
}

func (p *preparedRecord) hasPendingAuthoritative() bool {
	for _, item := range p.coverage {
		if item.pendingAuthoritative {
			return true
		}
	}
	return false
}

// Run serializes observations for one immutable assignment epoch. It exposes
// Joiner state only after the corresponding broker offset commit succeeds.
// Persistence and transport ownership are intentionally outside this
// transport-neutral core.
type Run struct {
	mu              sync.Mutex
	epoch           string
	valid           bool
	invalidErr      error
	joiner          *Joiner
	maxEntries      int
	roleTopics      map[StreamRole]string
	nextOffsets     map[streamPartition]int64
	inflight        *preparedRecord
	nextToken       uint64
	coverageTimeout time.Duration
	now             func() time.Time
	lastNow         time.Time
	coverage        map[string]*coverageEntry
	epochStartTime  *int64
}

type RunOption func(*runOptions) error

// CoverageCounts returns the in-memory work that would be discarded if the
// current assignment epoch ended now.
func (r *Run) CoverageCounts(epoch string) (entries int, authoritative int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireValidLocked(); err != nil {
		return 0, 0, err
	}
	if epoch != r.epoch {
		return 0, 0, fmt.Errorf("comparator: run epoch mismatch")
	}
	for _, item := range r.coverage {
		entries++
		if item.present[StreamInput] {
			authoritative++
		}
	}
	return entries, authoritative, nil
}

type runOptions struct {
	coverageTimeout time.Duration
	now             func() time.Time
}

func WithCoverageTimeout(timeout time.Duration) RunOption {
	return func(options *runOptions) error {
		if timeout <= 0 {
			return fmt.Errorf("comparator: coverage timeout must be positive")
		}
		options.coverageTimeout = timeout
		return nil
	}
}

func withRunClock(now func() time.Time) RunOption {
	return func(options *runOptions) error {
		if now == nil {
			return fmt.Errorf("comparator: run clock must be non-nil")
		}
		options.now = now
		return nil
	}
}

func NewRun(epoch string, maxEntries int, assignments []PartitionAssignment, options ...RunOption) (*Run, error) {
	joiner, err := NewJoiner(epoch, maxEntries)
	if err != nil {
		return nil, err
	}
	if len(assignments) == 0 {
		return nil, fmt.Errorf("comparator: assignments must be non-empty")
	}
	configured := runOptions{now: time.Now}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("comparator: run option must be non-nil")
		}
		if err := option(&configured); err != nil {
			return nil, err
		}
	}

	run := &Run{
		epoch:           epoch,
		valid:           true,
		joiner:          joiner,
		maxEntries:      maxEntries,
		roleTopics:      make(map[StreamRole]string, 3),
		nextOffsets:     make(map[streamPartition]int64, len(assignments)),
		coverageTimeout: configured.coverageTimeout,
		now:             configured.now,
		coverage:        make(map[string]*coverageEntry),
	}
	topicRoles := make(map[string]StreamRole, 3)
	for _, assignment := range assignments {
		if !validStreamRole(assignment.Role) {
			return nil, fmt.Errorf("comparator: unknown stream role")
		}
		if assignment.Topic == "" {
			return nil, fmt.Errorf("comparator: stream topic must be non-empty")
		}
		if assignment.Partition < 0 {
			return nil, fmt.Errorf("comparator: stream partition must be non-negative")
		}
		if assignment.NextOffset < 0 || assignment.NextOffset == math.MaxInt64 {
			return nil, fmt.Errorf("comparator: stream next offset is out of range")
		}
		if topic, ok := run.roleTopics[assignment.Role]; ok && topic != assignment.Topic {
			return nil, fmt.Errorf("comparator: one stream role cannot use multiple topics")
		}
		if role, ok := topicRoles[assignment.Topic]; ok && role != assignment.Role {
			return nil, fmt.Errorf("comparator: stream topics must be unique across roles")
		}
		run.roleTopics[assignment.Role] = assignment.Topic
		topicRoles[assignment.Topic] = assignment.Role
		coordinate := streamPartition{role: assignment.Role, topic: assignment.Topic, partition: assignment.Partition}
		if _, ok := run.nextOffsets[coordinate]; ok {
			return nil, fmt.Errorf("comparator: duplicate stream partition assignment")
		}
		run.nextOffsets[coordinate] = assignment.NextOffset
	}
	for _, role := range []StreamRole{StreamInput, StreamGo, StreamPython} {
		if _, ok := run.roleTopics[role]; !ok {
			return nil, fmt.Errorf("comparator: every stream role must be assigned")
		}
	}
	return run, nil
}

func (r *Run) Prepare(record StreamRecord) (Prepared, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireValidLocked(); err != nil {
		return Prepared{}, err
	}
	if record.Epoch != r.epoch {
		return Prepared{}, fmt.Errorf("comparator: run epoch mismatch")
	}
	if r.inflight != nil {
		return Prepared{}, ErrRecordInFlight
	}
	coordinate := streamPartition{role: record.Role, topic: record.Topic, partition: record.Partition}
	nextOffset, ok := r.nextOffsets[coordinate]
	if !ok {
		return Prepared{}, r.invalidateLocked(fmt.Errorf("comparator: record is outside the assigned stream partitions"))
	}
	if record.Offset < nextOffset {
		return Prepared{}, r.invalidateLocked(fmt.Errorf("comparator: offset rewind: got %d, committed next %d", record.Offset, nextOffset))
	}
	if record.Offset == math.MaxInt64 {
		return Prepared{}, r.invalidateLocked(fmt.Errorf("comparator: record offset is out of range"))
	}
	// Keep a bounded recent replay window and reserve one maximum wire batch.
	// Only fully joined entries are compacted; partial joins remain fail-closed.
	compactTarget := contract.MaxDetectInputRecordsV1
	if reserveTarget := r.maxEntries - contract.MaxDetectInputRecordsV1; reserveTarget < compactTarget {
		compactTarget = reserveTarget
	}
	for _, inputID := range r.joiner.compact(compactTarget) {
		delete(r.coverage, inputID)
	}

	var (
		updates []Update
		err     error
	)
	switch record.Role {
	case StreamInput:
		updates, err = r.joiner.ObserveDetectInput(r.epoch, record.Key, record.Value)
	case StreamGo:
		updates, err = r.joiner.ObserveDecisionBatch(r.epoch, DecisionSideGo, record.Key, record.Value)
	case StreamPython:
		updates, err = r.joiner.ObserveDecisionBatch(r.epoch, DecisionSidePython, record.Key, record.Value)
	default:
		err = fmt.Errorf("comparator: unknown stream role")
	}
	if err != nil {
		return Prepared{}, r.invalidateLocked(fmt.Errorf("comparator: prepare record: %w", err))
	}
	r.nextToken++
	if r.nextToken == 0 {
		return Prepared{}, r.invalidateLocked(fmt.Errorf("comparator: prepared token overflow"))
	}
	inflight := &preparedRecord{
		token:   r.nextToken,
		stream:  coordinate,
		offset:  record.Offset,
		updates: append([]Update(nil), updates...),
	}
	if err := r.prepareCoverageLocked(inflight); err != nil {
		return Prepared{}, r.invalidateLocked(err)
	}
	if coordinate.role == StreamInput && r.epochStartTime == nil {
		startedAt, ok, err := r.joiner.firstSourceTime(r.epoch, inputIDsFromUpdates(updates))
		if err != nil {
			return Prepared{}, r.invalidateLocked(err)
		}
		if ok {
			r.epochStartTime = &startedAt
		}
	}
	r.inflight = inflight
	return Prepared{owner: r, token: r.nextToken}, nil
}

func (r *Run) CommitSucceeded(prepared Prepared) ([]Update, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireValidLocked(); err != nil {
		return nil, err
	}
	if !r.matchesInflightLocked(prepared) {
		return nil, r.invalidateLocked(fmt.Errorf("comparator: prepared record mismatch"))
	}
	inflight := r.inflight
	if err := r.commitCoverageLocked(inflight); err != nil {
		return nil, r.invalidateLocked(err)
	}
	if inflight.auditsPrepared {
		releasable := make([]string, 0, len(inflight.coverage))
		for inputID, item := range inflight.coverage {
			if coverageEntryComplete(item) {
				releasable = append(releasable, inputID)
			}
		}
		r.joiner.markReleasable(releasable)
	}
	r.nextOffsets[inflight.stream] = inflight.offset + 1
	r.inflight = nil
	return append([]Update(nil), inflight.updates...), nil
}

func (r *Run) CommitFailed(prepared Prepared, cause error) error {
	if cause == nil {
		cause = errors.New("broker commit failed without a cause")
	}
	return r.abort(prepared, fmt.Errorf("broker commit failed: %w", cause))
}

// Abort permanently rejects the current assignment epoch after an external
// audit or transport failure before the prepared record becomes visible.
func (r *Run) Abort(prepared Prepared, cause error) error {
	if cause == nil {
		cause = errors.New("prepared record aborted without a cause")
	}
	return r.abort(prepared, fmt.Errorf("prepared record aborted: %w", cause))
}

func (r *Run) abort(prepared Prepared, cause error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireValidLocked(); err != nil {
		return err
	}
	if !r.matchesInflightLocked(prepared) {
		return r.invalidateLocked(fmt.Errorf("comparator: prepared record mismatch"))
	}
	return r.invalidateLocked(cause)
}

// Invalidate permanently rejects an assignment epoch after rebalance,
// transport failure or another external lifecycle boundary.
func (r *Run) Invalidate(epoch string, cause error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireValidLocked(); err != nil {
		return err
	}
	if epoch != r.epoch {
		return fmt.Errorf("comparator: run epoch mismatch")
	}
	if cause == nil {
		return fmt.Errorf("comparator: invalidation cause must be non-nil")
	}
	r.valid = false
	r.invalidErr = cause
	r.inflight = nil
	return nil
}

func (r *Run) Assess(epoch, inputID string, gates Gates) (Assessment, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireEpochAndCommittedLocked(epoch); err != nil {
		return Assessment{}, false, err
	}
	gates.CoverageComplete = r.coverageComparableLocked(inputID)
	return r.joiner.Assess(r.epoch, inputID, gates)
}

func (r *Run) NextOffset(epoch string, role StreamRole, partition int32) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireEpochAndCommittedLocked(epoch); err != nil {
		return 0, err
	}
	topic, ok := r.roleTopics[role]
	if !ok {
		return 0, fmt.Errorf("comparator: unknown stream role")
	}
	nextOffset, ok := r.nextOffsets[streamPartition{role: role, topic: topic, partition: partition}]
	if !ok {
		return 0, fmt.Errorf("comparator: stream partition is not assigned")
	}
	return nextOffset, nil
}

func (r *Run) Valid() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.valid
}

func (r *Run) requireEpochAndCommittedLocked(epoch string) error {
	if err := r.requireValidLocked(); err != nil {
		return err
	}
	if epoch != r.epoch {
		return fmt.Errorf("comparator: run epoch mismatch")
	}
	if r.inflight != nil {
		return ErrRecordInFlight
	}
	return nil
}

func (r *Run) requireValidLocked() error {
	if r == nil {
		return fmt.Errorf("comparator: run is nil")
	}
	if !r.valid {
		return fmt.Errorf("comparator: run is invalid: %w", r.invalidErr)
	}
	return nil
}

func (r *Run) matchesInflightLocked(prepared Prepared) bool {
	return prepared.owner == r && prepared.token != 0 && r.inflight != nil && prepared.token == r.inflight.token
}

func (r *Run) invalidateLocked(err error) error {
	if r.valid {
		r.valid = false
		r.invalidErr = err
		r.inflight = nil
	}
	return fmt.Errorf("comparator: run invalidated: %w", r.invalidErr)
}

func validStreamRole(role StreamRole) bool {
	return role == StreamInput || role == StreamGo || role == StreamPython
}

func inputIDsFromUpdates(updates []Update) []string {
	inputIDs := make([]string, 0, len(updates))
	seen := make(map[string]struct{}, len(updates))
	for _, update := range updates {
		if _, ok := seen[update.InputID]; ok {
			continue
		}
		seen[update.InputID] = struct{}{}
		inputIDs = append(inputIDs, update.InputID)
	}
	return inputIDs
}
