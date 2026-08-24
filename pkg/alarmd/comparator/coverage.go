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
	"fmt"
	"math"
	"sort"
	"time"
)

type CoveragePhase uint8

const (
	CoveragePending CoveragePhase = iota + 1
	CoverageOverdue
	CoverageMissingAtBarrier
	CoverageComplete
)

type PartitionBarrier struct {
	Role      StreamRole
	Topic     string
	Partition int32
	HighWater int64
}

type BarrierSnapshot struct {
	CaptureStartedAt time.Time
	Partitions       []PartitionBarrier
}

type CoverageSnapshot struct {
	InputID               string
	Authoritative         bool
	Phase                 CoveragePhase
	BarrierFrozen         bool
	FirstSeenAt           time.Time
	DeadlineAt            time.Time
	MissingRoles          []StreamRole
	MissingAtBarrierRoles []StreamRole
	LateRoles             []StreamRole
}

type coverageEntry struct {
	firstSeen time.Time
	// authoritativeAt is the deadline anchor and is only set once the
	// authoritative TriggerInput record has a broker acknowledgement.
	authoritativeAt      time.Time
	pendingAuthoritative bool
	present              map[StreamRole]bool
	barriers             map[streamPartition]int64
	barrierCaptured      time.Time
	missingAtBarrier     map[StreamRole]bool
	late                 map[StreamRole]bool
}

func (r *Run) Coverage(epoch, inputID string) (CoverageSnapshot, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireCoverageLocked(epoch); err != nil {
		return CoverageSnapshot{}, false, err
	}
	item, ok := r.coverage[inputID]
	if !ok {
		return CoverageSnapshot{}, false, nil
	}
	now, err := r.nowLocked()
	if err != nil {
		return CoverageSnapshot{}, false, r.invalidateLocked(err)
	}
	return r.coverageSnapshotLocked(inputID, item, now), true, nil
}

func (r *Run) SweepCoverage(epoch string) ([]CoverageSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireCoverageLocked(epoch); err != nil {
		return nil, err
	}
	now, err := r.nowLocked()
	if err != nil {
		return nil, r.invalidateLocked(err)
	}
	inputIDs := make([]string, 0, len(r.coverage))
	for inputID := range r.coverage {
		inputIDs = append(inputIDs, inputID)
	}
	sort.Strings(inputIDs)
	snapshots := make([]CoverageSnapshot, 0, len(inputIDs))
	for _, inputID := range inputIDs {
		snapshots = append(snapshots, r.coverageSnapshotLocked(inputID, r.coverage[inputID], now))
	}
	return snapshots, nil
}

// BeginBarrierCapture returns the Run's monotonic capture start before the
// transport reads any high-water offsets.
func (r *Run) BeginBarrierCapture(epoch string) (time.Time, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireCoverageLocked(epoch); err != nil {
		return time.Time{}, err
	}
	now, err := r.nowLocked()
	if err != nil {
		return time.Time{}, r.invalidateLocked(err)
	}
	return now, nil
}

func (r *Run) FreezeBarrier(epoch, inputID string, snapshot BarrierSnapshot) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireCoverageLocked(epoch); err != nil {
		return err
	}
	item, ok := r.coverage[inputID]
	if !ok {
		return fmt.Errorf("comparator: coverage input is unknown")
	}
	now, err := r.nowLocked()
	if err != nil {
		return r.invalidateLocked(err)
	}
	deadline := r.coverageDeadlineLocked(item)
	if deadline.IsZero() {
		return r.invalidateLocked(fmt.Errorf("comparator: coverage deadline is unavailable"))
	}
	if snapshot.CaptureStartedAt.IsZero() {
		return r.invalidateLocked(fmt.Errorf("comparator: barrier capture start must be non-zero"))
	}
	if snapshot.CaptureStartedAt.Before(deadline) {
		return fmt.Errorf("comparator: coverage deadline has not elapsed")
	}
	if snapshot.CaptureStartedAt.After(now) {
		return r.invalidateLocked(fmt.Errorf("comparator: barrier capture start is in the future"))
	}
	frozen := make(map[streamPartition]int64, len(snapshot.Partitions))
	for _, barrier := range snapshot.Partitions {
		coordinate := streamPartition{role: barrier.Role, topic: barrier.Topic, partition: barrier.Partition}
		if _, ok := r.nextOffsets[coordinate]; !ok {
			return r.invalidateLocked(fmt.Errorf("comparator: barrier is outside the assigned stream partitions"))
		}
		if barrier.HighWater < 0 || barrier.HighWater == math.MaxInt64 {
			return r.invalidateLocked(fmt.Errorf("comparator: barrier high water is out of range"))
		}
		if _, ok := frozen[coordinate]; ok {
			return r.invalidateLocked(fmt.Errorf("comparator: duplicate stream partition barrier"))
		}
		frozen[coordinate] = barrier.HighWater
	}
	if item.barriers != nil {
		if !item.barrierCaptured.Equal(snapshot.CaptureStartedAt) || !equalBarriers(item.barriers, frozen) {
			return r.invalidateLocked(fmt.Errorf("comparator: coverage barrier is immutable"))
		}
		return nil
	}
	for coordinate, highWater := range frozen {
		if highWater < r.nextOffsets[coordinate] {
			return r.invalidateLocked(fmt.Errorf("comparator: barrier high water is behind committed progress"))
		}
	}
	missing := r.missingRolesLocked(item)
	if len(missing) == 0 {
		return fmt.Errorf("comparator: coverage is already complete")
	}
	expected := make(map[streamPartition]struct{})
	missingSet := make(map[StreamRole]struct{}, len(missing))
	for _, role := range missing {
		missingSet[role] = struct{}{}
	}
	for coordinate := range r.nextOffsets {
		if _, ok := missingSet[coordinate.role]; ok {
			expected[coordinate] = struct{}{}
		}
	}
	for coordinate := range frozen {
		if _, ok := expected[coordinate]; !ok {
			return r.invalidateLocked(fmt.Errorf("comparator: barrier includes a stream that is not missing"))
		}
	}
	if len(frozen) != len(expected) {
		return r.invalidateLocked(fmt.Errorf("comparator: barrier does not cover every missing stream partition"))
	}
	item.barriers = frozen
	item.barrierCaptured = snapshot.CaptureStartedAt
	r.refreshMissingAtBarrierLocked(item)
	return nil
}

func (r *Run) prepareCoverageLocked(inflight *preparedRecord) error {
	var observedAt time.Time
	if r.coverageTimeout > 0 {
		var err error
		observedAt, err = r.nowLocked()
		if err != nil {
			return err
		}
	}
	inflight.observedAt = observedAt
	inflight.coverage = make(map[string]*coverageEntry)
	for _, update := range inflight.updates {
		if update.Disposition == DispositionCapacityDropped {
			continue
		}
		item := inflight.coverage[update.InputID]
		if item == nil {
			item = cloneCoverageEntry(r.coverage[update.InputID])
			if item == nil {
				item = &coverageEntry{
					firstSeen:        observedAt,
					present:          make(map[StreamRole]bool, 3),
					missingAtBarrier: make(map[StreamRole]bool, 3),
					late:             make(map[StreamRole]bool, 3),
				}
			}
			inflight.coverage[update.InputID] = item
		}
		role := inflight.stream.role
		if !item.present[role] {
			if highWater, ok := item.barriers[inflight.stream]; ok && inflight.offset >= highWater {
				item.missingAtBarrier[role] = true
			}
		}
		r.refreshMissingAtBarrierBeforeRecordLocked(item, inflight)
		if !item.present[role] && item.missingAtBarrier[role] {
			item.late[role] = true
		}
		item.present[role] = true
		if role == StreamInput && item.authoritativeAt.IsZero() {
			item.pendingAuthoritative = true
		}
	}
	return nil
}

// commitCoverageLocked anchors the coverage deadline at the moment the
// authoritative TriggerInput offset is acknowledged, not when it was prepared.
// Audit and offset acknowledgements can outlast the coverage timeout, and an
// anchor taken at prepare time would report a just-acknowledged input as overdue.
func (r *Run) commitCoverageLocked(inflight *preparedRecord) error {
	var authoritativeAt time.Time
	if r.coverageTimeout > 0 && inflight.hasPendingAuthoritative() {
		var err error
		authoritativeAt, err = r.nowLocked()
		if err != nil {
			return err
		}
	}
	for inputID, item := range inflight.coverage {
		if item.pendingAuthoritative {
			item.pendingAuthoritative = false
			item.authoritativeAt = authoritativeAt
		}
		r.coverage[inputID] = item
	}
	return nil
}

func cloneCoverageEntry(source *coverageEntry) *coverageEntry {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.present = cloneRoleBools(source.present)
	cloned.barriers = cloneBarriers(source.barriers)
	cloned.missingAtBarrier = cloneRoleBools(source.missingAtBarrier)
	cloned.late = cloneRoleBools(source.late)
	return &cloned
}

func cloneRoleBools(source map[StreamRole]bool) map[StreamRole]bool {
	if source == nil {
		return nil
	}
	cloned := make(map[StreamRole]bool, len(source))
	for role, value := range source {
		cloned[role] = value
	}
	return cloned
}

func cloneBarriers(source map[streamPartition]int64) map[streamPartition]int64 {
	if source == nil {
		return nil
	}
	cloned := make(map[streamPartition]int64, len(source))
	for coordinate, value := range source {
		cloned[coordinate] = value
	}
	return cloned
}

func (r *Run) coverageComparableLocked(inputID string) bool {
	return coverageEntryComparable(r.coverage[inputID])
}

func coverageEntryComparable(item *coverageEntry) bool {
	if !coverageEntryComplete(item) {
		return false
	}
	for _, late := range item.late {
		if late {
			return false
		}
	}
	return true
}

func coverageEntryComplete(item *coverageEntry) bool {
	return item != nil && item.present[StreamInput] && item.present[StreamGo] && item.present[StreamPython]
}

// ReleaseTerminalCoverage makes barrier-proven missing entries eligible for
// bounded compaction. Authoritative entries are released only after their
// missing audit is acknowledged; decision-only orphans have no audit payload
// and are released after the same broker high-water proof.
func (r *Run) ReleaseTerminalCoverage(epoch string, inputIDs []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.requireCoverageLocked(epoch); err != nil {
		return err
	}
	now, err := r.nowLocked()
	if err != nil {
		return r.invalidateLocked(err)
	}
	releasable := make([]string, 0, len(inputIDs))
	for _, inputID := range inputIDs {
		item := r.coverage[inputID]
		if item == nil {
			return r.invalidateLocked(fmt.Errorf("comparator: terminal coverage input is unknown"))
		}
		snapshot := r.coverageSnapshotLocked(inputID, item, now)
		if !snapshot.BarrierFrozen || snapshot.Phase != CoverageMissingAtBarrier {
			return r.invalidateLocked(fmt.Errorf("comparator: coverage is not terminal at barrier"))
		}
		releasable = append(releasable, inputID)
	}
	r.joiner.markReleasable(releasable)
	return nil
}

func (r *Run) requireCoverageLocked(epoch string) error {
	if err := r.requireEpochAndCommittedLocked(epoch); err != nil {
		return err
	}
	if r.coverageTimeout <= 0 {
		return fmt.Errorf("comparator: coverage timeout is not configured")
	}
	return nil
}

func (r *Run) nowLocked() (time.Time, error) {
	now := r.now()
	if now.IsZero() {
		return time.Time{}, fmt.Errorf("comparator: run clock returned zero time")
	}
	if !r.lastNow.IsZero() && now.Before(r.lastNow) {
		return time.Time{}, fmt.Errorf("comparator: run clock moved backwards")
	}
	r.lastNow = now
	return now, nil
}

func (r *Run) coverageSnapshotLocked(inputID string, item *coverageEntry, now time.Time) CoverageSnapshot {
	r.refreshMissingAtBarrierLocked(item)
	missing := r.missingRolesLocked(item)
	missingAtBarrier := rolesWhere(func(role StreamRole) bool {
		return !item.present[role] && item.missingAtBarrier[role]
	})
	phase := CoveragePending
	deadline := r.coverageDeadlineLocked(item)
	switch {
	case len(missing) == 0:
		phase = CoverageComplete
	case len(missingAtBarrier) > 0:
		phase = CoverageMissingAtBarrier
	case !deadline.IsZero() && !now.Before(deadline):
		phase = CoverageOverdue
	}
	return CoverageSnapshot{
		InputID:               inputID,
		Authoritative:         item.present[StreamInput],
		Phase:                 phase,
		BarrierFrozen:         item.barriers != nil,
		FirstSeenAt:           item.firstSeen,
		DeadlineAt:            deadline,
		MissingRoles:          missing,
		MissingAtBarrierRoles: missingAtBarrier,
		LateRoles: rolesWhere(func(role StreamRole) bool {
			return item.late[role]
		}),
	}
}

func (r *Run) coverageDeadlineLocked(item *coverageEntry) time.Time {
	if item == nil {
		return time.Time{}
	}
	anchor := item.authoritativeAt
	if anchor.IsZero() {
		anchor = item.firstSeen
	}
	if anchor.IsZero() {
		return time.Time{}
	}
	return anchor.Add(r.coverageTimeout)
}

func (r *Run) refreshMissingAtBarrierLocked(item *coverageEntry) {
	r.refreshMissingAtBarrierWithRecordLocked(item, nil)
}

func (r *Run) refreshMissingAtBarrierBeforeRecordLocked(item *coverageEntry, inflight *preparedRecord) {
	r.refreshMissingAtBarrierWithRecordLocked(item, inflight)
}

func (r *Run) refreshMissingAtBarrierWithRecordLocked(item *coverageEntry, inflight *preparedRecord) {
	if item.barriers == nil {
		return
	}
	for _, role := range []StreamRole{StreamInput, StreamGo, StreamPython} {
		if item.present[role] || item.missingAtBarrier[role] {
			continue
		}
		hasTarget := false
		reached := true
		for coordinate, highWater := range item.barriers {
			if coordinate.role != role {
				continue
			}
			hasTarget = true
			progress := r.nextOffsets[coordinate]
			if inflight != nil && coordinate == inflight.stream && inflight.offset > progress {
				progress = inflight.offset
			}
			if progress < highWater {
				reached = false
			}
		}
		if hasTarget && reached {
			item.missingAtBarrier[role] = true
		}
	}
}

func (r *Run) missingRolesLocked(item *coverageEntry) []StreamRole {
	return rolesWhere(func(role StreamRole) bool { return !item.present[role] })
}

func rolesWhere(include func(StreamRole) bool) []StreamRole {
	roles := make([]StreamRole, 0, 3)
	for _, role := range []StreamRole{StreamInput, StreamGo, StreamPython} {
		if include(role) {
			roles = append(roles, role)
		}
	}
	return roles
}

func equalBarriers(left, right map[streamPartition]int64) bool {
	if len(left) != len(right) {
		return false
	}
	for coordinate, highWater := range left {
		value, ok := right[coordinate]
		if !ok || value != highWater {
			return false
		}
	}
	return true
}
