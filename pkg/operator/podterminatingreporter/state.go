// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package podterminatingreporter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

const (
	StateVersion      = 2
	HardMaxStateBytes = 900000
)

type MetricRow struct {
	Namespace string
	Pod       string
	Node      string
	Seconds   int64
}

type Dimension struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Node      string `json:"node"`
}

type RecoveryDimension struct {
	Dimension
	ExpiresAt            float64 `json:"expires_at"`
	RestartExtensionUsed bool    `json:"restart_extension_used"`
}

type Snapshot struct {
	Version  int                 `json:"version"`
	Active   []Dimension         `json:"active"`
	Recovery []RecoveryDimension `json:"recovery"`
}

type PersistFunc func(context.Context, Snapshot) (int, error)

type MetricsSnapshot struct {
	RefreshSuccess         float64
	LastSuccessTimestamp   float64
	RefreshDurationSeconds float64
	ActiveEntries          int
	RecoveryEntries        int
	StateBytes             int
	KubernetesAPIErrors    map[string]float64
	Rows                   []MetricRow
}

type State struct {
	recoveryHold time.Duration
	staleAfter   time.Duration

	mu sync.RWMutex

	current                    map[Dimension]MetricRow
	active                     map[Dimension]MetricRow
	recovery                   map[Dimension]RecoveryDimension
	restartExtensionCandidates map[Dimension]struct{}
	pendingRecoveryDeadlines   map[Dimension]float64

	persistedActiveEntries   int
	persistedRecoveryEntries int
	persistedStateBytes      int
	refreshSuccess           float64
	lastSuccess              time.Time
	refreshDurationSeconds   float64
	kubernetesAPIErrors      map[string]uint64
}

func NewState(recoveryHold, staleAfter time.Duration) *State {
	return &State{
		recoveryHold:               recoveryHold,
		staleAfter:                 staleAfter,
		current:                    make(map[Dimension]MetricRow),
		active:                     make(map[Dimension]MetricRow),
		recovery:                   make(map[Dimension]RecoveryDimension),
		restartExtensionCandidates: make(map[Dimension]struct{}),
		pendingRecoveryDeadlines:   make(map[Dimension]float64),
		kubernetesAPIErrors: map[string]uint64{
			OperationListPods:   0,
			OperationGetState:   0,
			OperationPatchState: 0,
		},
	}
}

func dimensionForRow(row MetricRow) Dimension {
	return Dimension{Namespace: row.Namespace, Pod: row.Pod, Node: row.Node}
}

func rowForDimension(dimension Dimension, seconds int64) MetricRow {
	return MetricRow{
		Namespace: dimension.Namespace,
		Pod:       dimension.Pod,
		Node:      dimension.Node,
		Seconds:   seconds,
	}
}

func lessDimension(left, right Dimension) bool {
	if left.Namespace != right.Namespace {
		return left.Namespace < right.Namespace
	}
	if left.Pod != right.Pod {
		return left.Pod < right.Pod
	}
	return left.Node < right.Node
}

func sortedDimensions(values map[Dimension]MetricRow) []Dimension {
	dimensions := make([]Dimension, 0, len(values))
	for dimension := range values {
		dimensions = append(dimensions, dimension)
	}
	sort.Slice(dimensions, func(i, j int) bool {
		return lessDimension(dimensions[i], dimensions[j])
	})
	return dimensions
}

func sortedRecovery(values map[Dimension]RecoveryDimension) []RecoveryDimension {
	recovery := make([]RecoveryDimension, 0, len(values))
	for _, value := range values {
		recovery = append(recovery, value)
	}
	sort.Slice(recovery, func(i, j int) bool {
		return lessDimension(recovery[i].Dimension, recovery[j].Dimension)
	})
	return recovery
}

func cloneActive(source map[Dimension]MetricRow) map[Dimension]MetricRow {
	cloned := make(map[Dimension]MetricRow, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneRecovery(source map[Dimension]RecoveryDimension) map[Dimension]RecoveryDimension {
	cloned := make(map[Dimension]RecoveryDimension, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneSet(source map[Dimension]struct{}) map[Dimension]struct{} {
	cloned := make(map[Dimension]struct{}, len(source))
	for key := range source {
		cloned[key] = struct{}{}
	}
	return cloned
}

func cloneDeadlines(source map[Dimension]float64) map[Dimension]float64 {
	cloned := make(map[Dimension]float64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func (s *State) Restore(snapshot Snapshot, stateBytes int, now time.Time) error {
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	if stateBytes < 0 || stateBytes > HardMaxStateBytes {
		return fmt.Errorf("persisted state size %d exceeds hard limit %d", stateBytes, HardMaxStateBytes)
	}

	active := make(map[Dimension]MetricRow, len(snapshot.Active))
	for _, dimension := range snapshot.Active {
		active[dimension] = rowForDimension(dimension, 0)
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
	for dimension := range active {
		delete(recovery, dimension)
		delete(candidates, dimension)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = make(map[Dimension]MetricRow)
	s.active = active
	s.recovery = recovery
	s.restartExtensionCandidates = candidates
	s.pendingRecoveryDeadlines = make(map[Dimension]float64)
	s.persistedActiveEntries = len(snapshot.Active)
	s.persistedRecoveryEntries = len(snapshot.Recovery)
	s.persistedStateBytes = stateBytes
	s.refreshSuccess = 0
	s.lastSuccess = time.Time{}
	return nil
}

func (s *State) ApplySuccess(
	ctx context.Context,
	rows []MetricRow,
	now time.Time,
	persist PersistFunc,
) error {
	active := make(map[Dimension]MetricRow)
	for _, row := range rows {
		dimension := dimensionForRow(row)
		active[dimension] = row
	}

	s.mu.RLock()
	previousActive := cloneActive(s.active)
	recovery := cloneRecovery(s.recovery)
	candidates := cloneSet(s.restartExtensionCandidates)
	pending := cloneDeadlines(s.pendingRecoveryDeadlines)
	s.mu.RUnlock()

	for dimension := range active {
		delete(recovery, dimension)
		delete(candidates, dimension)
		delete(pending, dimension)
	}

	nowTimestamp := float64(now.UnixNano()) / float64(time.Second)
	holdSeconds := s.recoveryHold.Seconds()
	for dimension := range previousActive {
		if _, exists := active[dimension]; exists {
			continue
		}
		expiresAt, exists := pending[dimension]
		if !exists {
			expiresAt = nowTimestamp + holdSeconds
			pending[dimension] = expiresAt
		}
		recovery[dimension] = RecoveryDimension{
			Dimension:            dimension,
			ExpiresAt:            expiresAt,
			RestartExtensionUsed: false,
		}
	}

	for dimension := range candidates {
		value, exists := recovery[dimension]
		if !exists || value.RestartExtensionUsed {
			continue
		}
		expiresAt, exists := pending[dimension]
		if !exists {
			expiresAt = max(value.ExpiresAt, nowTimestamp+holdSeconds)
			pending[dimension] = expiresAt
		}
		value.ExpiresAt = expiresAt
		value.RestartExtensionUsed = true
		recovery[dimension] = value
	}

	for dimension, value := range recovery {
		if value.ExpiresAt <= nowTimestamp {
			delete(recovery, dimension)
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

	s.mu.Lock()
	s.pendingRecoveryDeadlines = pending
	s.mu.Unlock()

	persistedBytes := len(raw)
	if persist != nil {
		persistedBytes, err = persist(ctx, snapshot)
		if err != nil {
			s.MarkFailure()
			return err
		}
		if persistedBytes < 0 {
			s.MarkFailure()
			return fmt.Errorf("persisted state byte count must not be negative")
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = active
	s.active = active
	s.recovery = recovery
	if persist != nil {
		s.restartExtensionCandidates = make(map[Dimension]struct{})
		s.pendingRecoveryDeadlines = make(map[Dimension]float64)
		s.persistedActiveEntries = len(snapshot.Active)
		s.persistedRecoveryEntries = len(snapshot.Recovery)
		s.persistedStateBytes = persistedBytes
	} else {
		s.restartExtensionCandidates = candidates
	}
	s.refreshSuccess = 1
	s.lastSuccess = now
	return nil
}

func (s *State) MarkFailure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshSuccess = 0
}

func (s *State) SetRefreshDuration(duration time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshDurationSeconds = max(0, duration.Seconds())
}

func (s *State) ObserveKubernetesAPIError(err error) {
	var apiError *KubernetesAPIError
	if !errors.As(err, &apiError) {
		return
	}
	switch apiError.Operation {
	case OperationListPods, OperationGetState, OperationPatchState:
	default:
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kubernetesAPIErrors[apiError.Operation]++
}

func (s *State) IsFresh(now time.Time) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.lastSuccess.IsZero() && now.Sub(s.lastSuccess) <= s.staleAfter
}

func (s *State) MetricsSnapshot(now time.Time) MetricsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	fresh := !s.lastSuccess.IsZero() && now.Sub(s.lastSuccess) <= s.staleAfter
	snapshot := MetricsSnapshot{
		RefreshSuccess:         s.refreshSuccess,
		LastSuccessTimestamp:   float64(s.lastSuccess.UnixNano()) / float64(time.Second),
		RefreshDurationSeconds: s.refreshDurationSeconds,
		ActiveEntries:          s.persistedActiveEntries,
		RecoveryEntries:        s.persistedRecoveryEntries,
		StateBytes:             s.persistedStateBytes,
		KubernetesAPIErrors:    make(map[string]float64, len(s.kubernetesAPIErrors)),
	}
	for operation, count := range s.kubernetesAPIErrors {
		snapshot.KubernetesAPIErrors[operation] = float64(count)
	}
	if s.lastSuccess.IsZero() {
		snapshot.LastSuccessTimestamp = 0
	}
	if !fresh {
		snapshot.RefreshSuccess = 0
		return snapshot
	}

	for _, row := range s.current {
		snapshot.Rows = append(snapshot.Rows, row)
	}
	nowTimestamp := float64(now.UnixNano()) / float64(time.Second)
	for _, recovery := range s.recovery {
		if recovery.ExpiresAt > nowTimestamp {
			snapshot.Rows = append(snapshot.Rows, rowForDimension(recovery.Dimension, 0))
		}
	}
	sort.Slice(snapshot.Rows, func(i, j int) bool {
		return lessDimension(dimensionForRow(snapshot.Rows[i]), dimensionForRow(snapshot.Rows[j]))
	})
	return snapshot
}

type strictSnapshot struct {
	Version  *int               `json:"version"`
	Active   *[]strictDimension `json:"active"`
	Recovery *[]strictRecovery  `json:"recovery"`
}

type strictDimension struct {
	Namespace *string `json:"namespace"`
	Pod       *string `json:"pod"`
	Node      *string `json:"node"`
}

type strictRecovery struct {
	Namespace            *string  `json:"namespace"`
	Pod                  *string  `json:"pod"`
	Node                 *string  `json:"node"`
	ExpiresAt            *float64 `json:"expires_at"`
	RestartExtensionUsed *bool    `json:"restart_extension_used"`
}

func validateSnapshot(snapshot Snapshot) error {
	if snapshot.Version != StateVersion {
		return fmt.Errorf("unsupported persisted state version %d", snapshot.Version)
	}
	seen := make(map[Dimension]string, len(snapshot.Active)+len(snapshot.Recovery))
	for _, dimension := range snapshot.Active {
		if previous, exists := seen[dimension]; exists {
			return fmt.Errorf("duplicate dimension in active (already in %s)", previous)
		}
		seen[dimension] = "active"
	}
	for _, recovery := range snapshot.Recovery {
		if previous, exists := seen[recovery.Dimension]; exists {
			return fmt.Errorf("duplicate dimension in recovery (already in %s)", previous)
		}
		seen[recovery.Dimension] = "recovery"
	}
	return nil
}

func UnmarshalSnapshot(raw []byte, maxBytes int) (Snapshot, error) {
	if maxBytes <= 0 || maxBytes > HardMaxStateBytes {
		return Snapshot{}, fmt.Errorf("state max bytes %d exceeds hard limit %d or is not positive", maxBytes, HardMaxStateBytes)
	}
	if len(raw) > maxBytes {
		return Snapshot{}, fmt.Errorf("persisted state size %d exceeds limit %d", len(raw), maxBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var strict strictSnapshot
	if err := decoder.Decode(&strict); err != nil {
		return Snapshot{}, fmt.Errorf("decode persisted state: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Snapshot{}, err
	}
	if strict.Version == nil || *strict.Version != StateVersion {
		return Snapshot{}, fmt.Errorf("unsupported persisted state")
	}
	if strict.Active == nil || strict.Recovery == nil {
		return Snapshot{}, fmt.Errorf("persisted state active and recovery must be arrays")
	}

	snapshot := Snapshot{
		Version:  StateVersion,
		Active:   make([]Dimension, 0, len(*strict.Active)),
		Recovery: make([]RecoveryDimension, 0, len(*strict.Recovery)),
	}
	for index, rawDimension := range *strict.Active {
		if rawDimension.Namespace == nil || rawDimension.Pod == nil || rawDimension.Node == nil {
			return Snapshot{}, fmt.Errorf("persisted active dimension %d is incomplete", index)
		}
		snapshot.Active = append(snapshot.Active, Dimension{
			Namespace: *rawDimension.Namespace,
			Pod:       *rawDimension.Pod,
			Node:      *rawDimension.Node,
		})
	}
	for index, rawRecovery := range *strict.Recovery {
		if rawRecovery.Namespace == nil || rawRecovery.Pod == nil || rawRecovery.Node == nil ||
			rawRecovery.ExpiresAt == nil || rawRecovery.RestartExtensionUsed == nil {
			return Snapshot{}, fmt.Errorf("persisted recovery dimension %d is incomplete", index)
		}
		snapshot.Recovery = append(snapshot.Recovery, RecoveryDimension{
			Dimension: Dimension{
				Namespace: *rawRecovery.Namespace,
				Pod:       *rawRecovery.Pod,
				Node:      *rawRecovery.Node,
			},
			ExpiresAt:            *rawRecovery.ExpiresAt,
			RestartExtensionUsed: *rawRecovery.RestartExtensionUsed,
		})
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode trailing persisted state: %w", err)
	}
	return fmt.Errorf("persisted state contains trailing JSON")
}

func MarshalSnapshot(snapshot Snapshot) ([]byte, error) {
	if err := validateSnapshot(snapshot); err != nil {
		return nil, err
	}
	normalized := Snapshot{
		Version:  StateVersion,
		Active:   append([]Dimension(nil), snapshot.Active...),
		Recovery: append([]RecoveryDimension(nil), snapshot.Recovery...),
	}
	if normalized.Active == nil {
		normalized.Active = []Dimension{}
	}
	if normalized.Recovery == nil {
		normalized.Recovery = []RecoveryDimension{}
	}
	sort.Slice(normalized.Active, func(i, j int) bool {
		return lessDimension(normalized.Active[i], normalized.Active[j])
	})
	sort.Slice(normalized.Recovery, func(i, j int) bool {
		return lessDimension(normalized.Recovery[i].Dimension, normalized.Recovery[j].Dimension)
	})
	return json.Marshal(normalized)
}
