// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"sync"
	"time"
)

type HealthState string

const (
	HealthStarting HealthState = "starting"
	HealthReady    HealthState = "ready"
	HealthDegraded HealthState = "degraded"
	HealthNotReady HealthState = "not_ready"
	HealthDraining HealthState = "draining"
	HealthFatal    HealthState = "fatal"
)

type HealthSnapshot struct {
	State             HealthState  `json:"state"`
	Ready             bool         `json:"ready"`
	Reasons           []ReasonCode `json:"reasons,omitempty"`
	ConfigLoaded      bool         `json:"config_loaded"`
	SchemaReady       bool         `json:"schema_ready"`
	AssignmentReady   bool         `json:"assignment_ready"`
	RuntimeStateReady bool         `json:"runtime_state_ready"`
	OutputSinkReady   bool         `json:"output_sink_ready"`
	PhaseTwo          bool         `json:"phase_two,omitempty"`
	SnapshotReady     bool         `json:"snapshot_ready,omitempty"`
	LastRecoveryAt    time.Time    `json:"last_recovery_at,omitempty"`
	Draining          bool         `json:"draining"`
}

type HealthSource interface {
	HealthSnapshot() HealthSnapshot
}

type HealthTracker struct {
	mu       sync.RWMutex
	snapshot HealthSnapshot
}

func NewHealthTracker(initial HealthSnapshot) *HealthTracker {
	tracker := &HealthTracker{}
	tracker.Update(initial)
	return tracker
}

func (t *HealthTracker) Update(snapshot HealthSnapshot) {
	if t == nil {
		return
	}
	snapshot = NormalizeHealthSnapshot(snapshot)
	t.mu.Lock()
	t.snapshot = snapshot
	t.mu.Unlock()
}

func (t *HealthTracker) HealthSnapshot() HealthSnapshot {
	if t == nil {
		return NormalizeHealthSnapshot(HealthSnapshot{})
	}
	t.mu.RLock()
	snapshot := t.snapshot
	snapshot.Reasons = append([]ReasonCode(nil), t.snapshot.Reasons...)
	t.mu.RUnlock()
	return snapshot
}

func NormalizeHealthSnapshot(snapshot HealthSnapshot) HealthSnapshot {
	snapshot.State = normalizeHealthState(snapshot.State)
	if (snapshot.State == HealthReady || snapshot.State == HealthDegraded) && !healthPrerequisitesReady(snapshot) {
		snapshot.State = HealthNotReady
		snapshot.Reasons = append(snapshot.Reasons, ReasonInternalUnknown)
	}
	snapshot.Ready = snapshot.State == HealthReady || snapshot.State == HealthDegraded
	snapshot.Draining = snapshot.State == HealthDraining
	snapshot.Reasons = normalizeReasons(snapshot.Reasons)
	return snapshot
}

func healthPrerequisitesReady(snapshot HealthSnapshot) bool {
	ready := snapshot.ConfigLoaded && snapshot.SchemaReady && snapshot.AssignmentReady &&
		snapshot.RuntimeStateReady && snapshot.OutputSinkReady
	if snapshot.PhaseTwo {
		ready = ready && snapshot.SnapshotReady
	}
	return ready
}

func normalizeHealthState(state HealthState) HealthState {
	switch state {
	case HealthReady, HealthDegraded, HealthNotReady, HealthDraining, HealthFatal:
		return state
	case HealthStarting, "":
		return HealthStarting
	default:
		return HealthNotReady
	}
}
