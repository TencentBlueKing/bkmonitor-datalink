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

// RuntimeKey is the process-local ordering identity of one runtime window. It
// deliberately contains no Kafka or worker ownership fields.
type RuntimeKey struct {
	TenantID                string
	BusinessID              string
	StrategyID              string
	StateCompatibilityHash  string
	DimensionIdentityDigest string
}

// OrderedKeyGate serializes tasks that touch the same RuntimeKey while
// allowing disjoint tasks to enter their stateful phase concurrently.
type OrderedKeyGate struct {
	mu sync.Mutex

	queues       map[RuntimeKey][]*KeyReservation
	held         map[RuntimeKey]*KeyReservation
	reservations map[uint64]*KeyReservation
	lastSequence uint64
	hasSequence  bool
}

type KeyReservation struct {
	gate     *OrderedKeyGate
	sequence uint64
	keys     []RuntimeKey
	ready    chan struct{}
	canceled chan struct{}

	granted bool
	done    bool
}

func NewOrderedKeyGate() *OrderedKeyGate {
	return &OrderedKeyGate{
		queues: make(map[RuntimeKey][]*KeyReservation), held: make(map[RuntimeKey]*KeyReservation),
		reservations: make(map[uint64]*KeyReservation),
	}
}

func (gate *OrderedKeyGate) Register(sequence uint64, keys []RuntimeKey) (*KeyReservation, error) {
	if gate == nil {
		return nil, errors.New("alarmd coordinator: OrderedKeyGate is required")
	}
	keys = canonicalRuntimeKeys(keys)
	reservation := &KeyReservation{
		gate: gate, sequence: sequence, keys: keys, ready: make(chan struct{}), canceled: make(chan struct{}),
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.hasSequence && sequence <= gate.lastSequence {
		return nil, errors.New("alarmd coordinator: key reservations must be registered once in receive order")
	}
	gate.lastSequence = sequence
	gate.hasSequence = true
	gate.reservations[sequence] = reservation
	for _, key := range keys {
		gate.queues[key] = append(gate.queues[key], reservation)
	}
	gate.tryGrantLocked()
	return reservation, nil
}

func (reservation *KeyReservation) Wait(ctx context.Context) error {
	if reservation == nil || reservation.gate == nil {
		return errors.New("alarmd coordinator: key reservation is required")
	}
	if ctx == nil {
		return errors.New("alarmd coordinator: key reservation context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-reservation.ready:
		return ctx.Err()
	case <-reservation.canceled:
		return errors.New("alarmd coordinator: key reservation canceled")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (reservation *KeyReservation) Release() {
	if reservation == nil || reservation.gate == nil {
		return
	}
	reservation.gate.finish(reservation, false)
}

func (reservation *KeyReservation) Cancel() {
	if reservation == nil || reservation.gate == nil {
		return
	}
	reservation.gate.finish(reservation, true)
}

func (gate *OrderedKeyGate) finish(reservation *KeyReservation, canceled bool) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if reservation.done || gate.reservations[reservation.sequence] != reservation {
		return
	}
	reservation.done = true
	delete(gate.reservations, reservation.sequence)
	for _, key := range reservation.keys {
		queue := gate.queues[key]
		for index, queued := range queue {
			if queued != reservation {
				continue
			}
			copy(queue[index:], queue[index+1:])
			queue = queue[:len(queue)-1]
			break
		}
		if len(queue) == 0 {
			delete(gate.queues, key)
		} else {
			gate.queues[key] = queue
		}
		if gate.held[key] == reservation {
			delete(gate.held, key)
		}
	}
	if canceled {
		close(reservation.canceled)
	}
	gate.tryGrantLocked()
}

func (gate *OrderedKeyGate) tryGrantLocked() {
	sequences := make([]uint64, 0, len(gate.reservations))
	for sequence := range gate.reservations {
		sequences = append(sequences, sequence)
	}
	sort.Slice(sequences, func(left, right int) bool { return sequences[left] < sequences[right] })
	for _, sequence := range sequences {
		reservation := gate.reservations[sequence]
		if reservation == nil || reservation.done || reservation.granted || !gate.canGrantLocked(reservation) {
			continue
		}
		reservation.granted = true
		for _, key := range reservation.keys {
			gate.held[key] = reservation
		}
		close(reservation.ready)
	}
}

func (gate *OrderedKeyGate) canGrantLocked(reservation *KeyReservation) bool {
	for _, key := range reservation.keys {
		queue := gate.queues[key]
		if len(queue) == 0 || queue[0] != reservation || gate.held[key] != nil {
			return false
		}
	}
	return true
}

func canonicalRuntimeKeys(keys []RuntimeKey) []RuntimeKey {
	result := append([]RuntimeKey(nil), keys...)
	sort.Slice(result, func(left, right int) bool { return runtimeKeyLess(result[left], result[right]) })
	write := 0
	for _, key := range result {
		if write > 0 && result[write-1] == key {
			continue
		}
		result[write] = key
		write++
	}
	return result[:write]
}

func runtimeKeyLess(left, right RuntimeKey) bool {
	if left.TenantID != right.TenantID {
		return left.TenantID < right.TenantID
	}
	if left.BusinessID != right.BusinessID {
		return left.BusinessID < right.BusinessID
	}
	if left.StrategyID != right.StrategyID {
		return left.StrategyID < right.StrategyID
	}
	if left.StateCompatibilityHash != right.StateCompatibilityHash {
		return left.StateCompatibilityHash < right.StateCompatibilityHash
	}
	return left.DimensionIdentityDigest < right.DimensionIdentityDigest
}
