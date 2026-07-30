// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package podterminating

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

type PersistFunc func(context.Context, Snapshot) (int, error)

type MetricsSnapshot struct {
	RefreshSuccess       float64
	LastSuccessTimestamp float64
	ActiveEntries        int
	RecoveryEntries      int
	StateBytes           int
	Rows                 []MetricRow
}

type State struct {
	recoveryHold time.Duration
	staleAfter   time.Duration

	mu sync.RWMutex

	active                     map[Dimension]Observation
	recovery                   map[Dimension]RecoveryDimension
	restartExtensionCandidates map[Dimension]struct{}
	stateBytes                 int
	refreshSuccess             float64
	lastSuccess                time.Time
}

func NewState(recoveryHold, staleAfter time.Duration) *State {
	return &State{
		recoveryHold:               recoveryHold,
		staleAfter:                 staleAfter,
		active:                     make(map[Dimension]Observation),
		recovery:                   make(map[Dimension]RecoveryDimension),
		restartExtensionCandidates: make(map[Dimension]struct{}),
	}
}

func (s *State) Restore(snapshot Snapshot, stateBytes int, now time.Time) error {
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	if stateBytes < 0 || stateBytes > HardMaxStateBytes {
		return fmt.Errorf("persisted state size %d exceeds hard limit %d", stateBytes, HardMaxStateBytes)
	}
	active := make(map[Dimension]Observation, len(snapshot.Active))
	for _, dimension := range snapshot.Active {
		active[dimension] = Observation{Dimension: dimension}
	}
	recovery := make(map[Dimension]RecoveryDimension, len(snapshot.Recovery))
	candidates := make(map[Dimension]struct{})
	nowTimestamp := float64(now.UnixNano()) / float64(time.Second)
	for _, value := range snapshot.Recovery {
		if value.RestartExtensionUsed && value.ExpiresAt <= nowTimestamp {
			continue
		}
		recovery[value.Dimension] = value
		if !value.RestartExtensionUsed {
			candidates[value.Dimension] = struct{}{}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = active
	s.recovery = recovery
	s.restartExtensionCandidates = candidates
	s.stateBytes = stateBytes
	s.refreshSuccess = 0
	s.lastSuccess = time.Time{}
	return nil
}

func collapseObserved(observed map[types.UID]Observation) map[Dimension]Observation {
	active := make(map[Dimension]Observation, len(observed))
	for _, observation := range observed {
		previous, exists := active[observation.Dimension]
		if !exists || observation.DeletionStartedAt.Before(previous.DeletionStartedAt) {
			active[observation.Dimension] = observation
		}
	}
	return active
}

func cloneActive(source map[Dimension]Observation) map[Dimension]Observation {
	cloned := make(map[Dimension]Observation, len(source))
	for dimension, observation := range source {
		cloned[dimension] = observation
	}
	return cloned
}

func cloneRecovery(source map[Dimension]RecoveryDimension) map[Dimension]RecoveryDimension {
	cloned := make(map[Dimension]RecoveryDimension, len(source))
	for dimension, recovery := range source {
		cloned[dimension] = recovery
	}
	return cloned
}

func cloneSet(source map[Dimension]struct{}) map[Dimension]struct{} {
	cloned := make(map[Dimension]struct{}, len(source))
	for dimension := range source {
		cloned[dimension] = struct{}{}
	}
	return cloned
}

func (s *State) Checkpoint(
	ctx context.Context,
	observed map[types.UID]Observation,
	now time.Time,
	persist PersistFunc,
) error {
	active := collapseObserved(observed)

	s.mu.RLock()
	previousActive := cloneActive(s.active)
	recovery := cloneRecovery(s.recovery)
	candidates := cloneSet(s.restartExtensionCandidates)
	s.mu.RUnlock()

	for dimension := range active {
		delete(recovery, dimension)
		delete(candidates, dimension)
	}

	nowTimestamp := float64(now.UnixNano()) / float64(time.Second)
	holdSeconds := s.recoveryHold.Seconds()
	for dimension := range previousActive {
		if _, exists := active[dimension]; exists {
			continue
		}
		// This deadline is deliberately local to this candidate snapshot. If
		// persistence fails, the next retry computes a new full hold from its
		// own successful-candidate time.
		recovery[dimension] = RecoveryDimension{
			Dimension:            dimension,
			ExpiresAt:            nowTimestamp + holdSeconds,
			RestartExtensionUsed: false,
		}
	}

	for dimension := range candidates {
		value, exists := recovery[dimension]
		if !exists || value.RestartExtensionUsed {
			continue
		}
		value.ExpiresAt = max(value.ExpiresAt, nowTimestamp+holdSeconds)
		value.RestartExtensionUsed = true
		recovery[dimension] = value
	}

	for dimension, value := range recovery {
		if value.ExpiresAt <= nowTimestamp {
			delete(recovery, dimension)
			delete(candidates, dimension)
		}
	}

	snapshot := Snapshot{
		Version:  StateVersion,
		Active:   sortedDimensions(active),
		Recovery: sortedRecovery(recovery),
	}
	raw, err := MarshalSnapshot(snapshot)
	if err != nil {
		s.MarkFailure()
		return err
	}
	stateBytes := len(raw)
	if persist != nil {
		stateBytes, err = persist(ctx, snapshot)
		if err != nil {
			s.MarkFailure()
			return err
		}
		if stateBytes < 0 {
			s.MarkFailure()
			return fmt.Errorf("persisted state byte count must not be negative")
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = active
	s.recovery = recovery
	s.restartExtensionCandidates = make(map[Dimension]struct{})
	s.stateBytes = stateBytes
	s.refreshSuccess = 1
	s.lastSuccess = now
	return nil
}

// AcceptPersisted commits a snapshot that a readback proved was written by an
// earlier ambiguous PATCH. The caller should immediately run Checkpoint again
// when the current Watch snapshot has changed since that write.
func (s *State) AcceptPersisted(
	snapshot Snapshot,
	observed map[types.UID]Observation,
	stateBytes int,
	now time.Time,
) error {
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	current := collapseObserved(observed)
	active := make(map[Dimension]Observation, len(snapshot.Active))
	for _, dimension := range snapshot.Active {
		observation := Observation{Dimension: dimension}
		if value, exists := current[dimension]; exists {
			observation = value
		}
		active[dimension] = observation
	}
	recovery := make(map[Dimension]RecoveryDimension, len(snapshot.Recovery))
	for _, value := range snapshot.Recovery {
		recovery[value.Dimension] = value
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = active
	s.recovery = recovery
	s.restartExtensionCandidates = make(map[Dimension]struct{})
	s.stateBytes = stateBytes
	s.refreshSuccess = 1
	s.lastSuccess = now
	return nil
}

func (s *State) MarkFailure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshSuccess = 0
}

func (s *State) MarkHealthy(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshSuccess = 1
	s.lastSuccess = now
}

func (s *State) Snapshot(now time.Time) MetricsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot := MetricsSnapshot{
		RefreshSuccess:  s.refreshSuccess,
		ActiveEntries:   len(s.active),
		RecoveryEntries: len(s.recovery),
		StateBytes:      s.stateBytes,
	}
	if !s.lastSuccess.IsZero() {
		snapshot.LastSuccessTimestamp = float64(s.lastSuccess.UnixNano()) / float64(time.Second)
	}
	if s.lastSuccess.IsZero() || now.Sub(s.lastSuccess) > s.staleAfter {
		return snapshot
	}
	rows := make([]MetricRow, 0, len(s.active)+len(s.recovery))
	for dimension, observation := range s.active {
		if observation.DeletionStartedAt.IsZero() {
			continue
		}
		seconds := int64(now.Sub(observation.DeletionStartedAt) / time.Second)
		if seconds < 0 {
			seconds = 0
		}
		rows = append(rows, MetricRow{Dimension: dimension, Seconds: seconds})
	}
	for dimension, recovery := range s.recovery {
		if recovery.ExpiresAt > float64(now.UnixNano())/float64(time.Second) {
			rows = append(rows, MetricRow{Dimension: dimension, Seconds: 0})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return lessDimension(rows[i].Dimension, rows[j].Dimension)
	})
	snapshot.Rows = rows
	return snapshot
}

func (s *State) HasExpiredRecovery(now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	nowTimestamp := float64(now.UnixNano()) / float64(time.Second)
	for _, recovery := range s.recovery {
		if recovery.ExpiresAt <= nowTimestamp {
			return true
		}
	}
	return false
}
