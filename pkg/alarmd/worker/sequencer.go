// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// KeyedSideEffectSequencer is the single process-local ordering boundary for
// state and Plan gap side effects. One instance must be shared by every Slot
// execution in a Worker process so maxReservations remains a real process
// bound rather than a per-claim or per-call bound.
type KeyedSideEffectSequencer struct {
	gate      *coordinator.OrderedKeyGate
	admission chan struct{}

	registerMu sync.Mutex
	sequence   uint64
}

var _ execution.SideEffectSequencer = (*KeyedSideEffectSequencer)(nil)

// NewKeyedSideEffectSequencer creates one process-local sequencer. Capacity
// includes both granted callbacks and reservations waiting for conflicting
// keys; callers waiting to enter admission allocate no keyed reservation.
func NewKeyedSideEffectSequencer(maxReservations int) (*KeyedSideEffectSequencer, error) {
	if maxReservations <= 0 {
		return nil, errors.New("alarmd worker: positive side-effect sequencer capacity is required")
	}
	return &KeyedSideEffectSequencer{
		gate:      coordinator.NewOrderedKeyGate(),
		admission: make(chan struct{}, maxReservations),
	}, nil
}

func (sequencer *KeyedSideEffectSequencer) Sequence(
	ctx context.Context,
	scope execution.SequencingScope,
	run func(context.Context) error,
) error {
	if sequencer == nil || sequencer.gate == nil || sequencer.admission == nil {
		return errors.New("alarmd worker: initialized side-effect sequencer is required")
	}
	if ctx == nil {
		return errors.New("alarmd worker: side-effect sequencing context is required")
	}
	if run == nil {
		return errors.New("alarmd worker: side-effect callback is required")
	}
	keys, err := runtimeKeys(scope)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	select {
	case sequencer.admission <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	releaseAdmission := func() { <-sequencer.admission }

	sequencer.registerMu.Lock()
	sequencer.sequence++
	reservation, err := sequencer.gate.Register(sequencer.sequence, keys)
	sequencer.registerMu.Unlock()
	if err != nil {
		releaseAdmission()
		return fmt.Errorf("alarmd worker: reserve side-effect keys: %w", err)
	}
	if err := reservation.Wait(ctx); err != nil {
		reservation.Cancel()
		releaseAdmission()
		return err
	}
	defer releaseAdmission()
	defer reservation.Release()
	return run(ctx)
}

func runtimeKeys(scope execution.SequencingScope) ([]coordinator.RuntimeKey, error) {
	if scope.Slot.QueryGroup == "" || scope.Slot.ScheduleRevision == "" || scope.Slot.EvaluationTime <= 0 {
		return nil, errors.New("alarmd worker: complete side-effect sequencing Slot is required")
	}
	keys := make([]coordinator.RuntimeKey, 0, len(scope.StateKeys)+len(scope.GapKeys))
	for _, identity := range scope.StateKeys {
		if err := identity.Plan.Validate(); err != nil {
			return nil, fmt.Errorf("alarmd worker: invalid side-effect state Plan: %w", err)
		}
		if identity.StateGeneration == "" || identity.SeriesIdentityDigest == "" {
			return nil, errors.New("alarmd worker: complete side-effect state identity is required")
		}
		keys = append(keys, coordinator.RuntimeKey{
			TenantID:                identity.Plan.TenantID,
			BusinessID:              identity.Plan.BusinessID,
			StrategyID:              identity.Plan.StrategyID,
			StateCompatibilityHash:  string(identity.StateGeneration),
			DimensionIdentityDigest: string(identity.SeriesIdentityDigest),
		})
	}
	for _, identity := range scope.GapKeys {
		if err := identity.Plan.Validate(); err != nil {
			return nil, fmt.Errorf("alarmd worker: invalid side-effect gap Plan: %w", err)
		}
		if identity.StateGeneration == "" {
			return nil, errors.New("alarmd worker: complete side-effect gap identity is required")
		}
		keys = append(keys, coordinator.RuntimeKey{
			TenantID:               identity.Plan.TenantID,
			BusinessID:             identity.Plan.BusinessID,
			StrategyID:             identity.Plan.StrategyID,
			StateCompatibilityHash: string(identity.StateGeneration),
		})
	}
	return keys, nil
}
