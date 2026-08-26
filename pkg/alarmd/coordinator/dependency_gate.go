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
	"sort"
	"sync"
)

type CriticalDependency string

const (
	DependencyInputKafka  CriticalDependency = "input_kafka"
	DependencyOutputKafka CriticalDependency = "output_kafka"
	DependencyRedis       CriticalDependency = "redis"
	DependencyProvider    CriticalDependency = "provider"
)

type DependencyReasonCode string

type DependencyGateState string

const (
	DependencyGateReady  DependencyGateState = "READY"
	DependencyGatePaused DependencyGateState = "PAUSED"
)

type DependencyBlocker struct {
	Dependency CriticalDependency
	ReasonCode DependencyReasonCode
}

type DependencyGateSnapshot struct {
	State    DependencyGateState
	Revision uint64
	Blockers []DependencyBlocker
}

type DependencyGateTransition struct {
	Previous DependencyGateSnapshot
	Current  DependencyGateSnapshot
}

type DependencyGateObserver func(DependencyGateTransition)

// CriticalDependencyGate stops only new admission. A task that passed
// WaitAdmission has already started and is not canceled when the gate pauses.
type CriticalDependencyGate struct {
	mu sync.Mutex

	blockers map[CriticalDependency]DependencyBlocker
	revision uint64
	ready    chan struct{}
	observer DependencyGateObserver
}

func NewCriticalDependencyGate(observer DependencyGateObserver) *CriticalDependencyGate {
	ready := make(chan struct{})
	close(ready)
	return &CriticalDependencyGate{
		blockers: make(map[CriticalDependency]DependencyBlocker),
		ready:    ready,
		observer: observer,
	}
}

func (gate *CriticalDependencyGate) Ready() bool {
	return gate.Snapshot().State == DependencyGateReady
}

func (gate *CriticalDependencyGate) Snapshot() DependencyGateSnapshot {
	if gate == nil {
		return DependencyGateSnapshot{State: DependencyGatePaused}
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.snapshotLocked()
}

func (gate *CriticalDependencyGate) Pause(blocker DependencyBlocker) (DependencyGateSnapshot, error) {
	if gate == nil {
		return DependencyGateSnapshot{}, errors.New("alarmd coordinator: critical dependency gate is required")
	}
	if blocker.Dependency == "" || blocker.ReasonCode == "" {
		return gate.Snapshot(), errors.New("alarmd coordinator: dependency and reason are required to pause intake")
	}

	gate.mu.Lock()
	previous := gate.snapshotLocked()
	if current, ok := gate.blockers[blocker.Dependency]; ok && current == blocker {
		gate.mu.Unlock()
		return previous, nil
	}
	if len(gate.blockers) == 0 {
		gate.ready = make(chan struct{})
	}
	gate.blockers[blocker.Dependency] = blocker
	gate.revision++
	current := gate.snapshotLocked()
	observer := gate.observer
	gate.mu.Unlock()

	if observer != nil {
		observer(DependencyGateTransition{Previous: previous, Current: current})
	}
	return current, nil
}

func (gate *CriticalDependencyGate) Resume(dependency CriticalDependency) (DependencyGateSnapshot, error) {
	if gate == nil {
		return DependencyGateSnapshot{}, errors.New("alarmd coordinator: critical dependency gate is required")
	}
	if dependency == "" {
		return gate.Snapshot(), errors.New("alarmd coordinator: dependency is required to resume intake")
	}

	gate.mu.Lock()
	previous := gate.snapshotLocked()
	if _, ok := gate.blockers[dependency]; !ok {
		gate.mu.Unlock()
		return previous, nil
	}
	delete(gate.blockers, dependency)
	gate.revision++
	if len(gate.blockers) == 0 {
		close(gate.ready)
	}
	current := gate.snapshotLocked()
	observer := gate.observer
	gate.mu.Unlock()

	if observer != nil {
		observer(DependencyGateTransition{Previous: previous, Current: current})
	}
	return current, nil
}

func (gate *CriticalDependencyGate) WaitAdmission(ctx context.Context) error {
	if gate == nil {
		return errors.New("alarmd coordinator: critical dependency gate is required")
	}
	if ctx == nil {
		return errors.New("alarmd coordinator: admission context is required")
	}
	for {
		gate.mu.Lock()
		if len(gate.blockers) == 0 {
			gate.mu.Unlock()
			return nil
		}
		ready := gate.ready
		gate.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ready:
		}
	}
}

func (gate *CriticalDependencyGate) snapshotLocked() DependencyGateSnapshot {
	blockers := make([]DependencyBlocker, 0, len(gate.blockers))
	for _, blocker := range gate.blockers {
		blockers = append(blockers, blocker)
	}
	sort.Slice(blockers, func(i, j int) bool {
		return blockers[i].Dependency < blockers[j].Dependency
	})
	state := DependencyGateReady
	if len(blockers) > 0 {
		state = DependencyGatePaused
	}
	return DependencyGateSnapshot{State: state, Revision: gate.revision, Blockers: blockers}
}
