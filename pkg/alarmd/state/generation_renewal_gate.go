// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"sync"
	"time"
)

// RenewalAskInterval is how long a process may go without asking Redis about a
// key it has already asked about.
//
// The whole interval rests on one guarantee: an ask that returns without an
// error leaves the key with at least the renewal threshold remaining. If the
// script renewed, the key has a full ttl; if it declined, it declined because
// at least the threshold was left. So after an ask, remaining >= threshold in
// both outcomes, and that is what may be spent on skipping.
//
// Half of it is spent, not all of it. Skipping for the whole threshold would
// consume the entire guarantee and arrive at the next ask with nothing left --
// a key whose remaining life was exactly the threshold would expire on the
// boundary. Half leaves as much margin as it spends.
//
// It does not make the renewal script's branches unreachable, which would be
// the sign the gate had been set too wide: at a 24h life the ask is every 6h
// and the threshold is 12h, so an ask declines twice and renews on the third.
func RenewalAskInterval(ttl time.Duration) time.Duration {
	return GenerationScopedRenewalThreshold(ttl) / 2
}

// renewalGate is what a process remembers about the keys it has renewed, so
// that a load inside the interval spends no round trip.
//
// Renewal hangs on the load, and a Plan loads its generation-scoped key every
// Slot: at a one-minute period that is one EVAL per Plan per minute, forever,
// to decide something that changes twice a day. Measured on a deployment with
// around 2,100 Plans it was 38.6 EVAL/s at 7.1ms each, which is 0.27 seconds
// of waiting on Redis every second. The script was written to make the write
// cheap and the ask was assumed to be small beside the Slot's own reads; it is
// the asking that costs.
//
// What is stored is the moment a key may be asked about again, not the moment
// it was asked about. The interval is derived from the key's own retention, so
// two keys asked about in the same Slot can come due at different times, and a
// table of ask times cannot say which entries have come due without knowing
// each one's interval. Holding the answer instead makes both the decision and
// the sweep below exact.
//
// Everything about it fails toward asking. An ask that returned an error is
// not recorded, a key the gate has forgotten is asked about, and a gate with
// no room forgets everything rather than choosing what to keep. The cost of
// asking is a round trip; the cost of wrongly skipping is a key that expires
// while a Plan is still being evaluated.
type renewalGate struct {
	mu     sync.Mutex
	expiry map[string]time.Time
	// sweepAt is the size at which the table is swept of entries that have
	// come due. It tracks the live working set rather than being set in
	// advance: after each sweep it is twice what survived, so sweeps stay
	// rare as a deployment grows and cost nothing on a small one.
	sweepAt int
	// resets counts the times the table was cleared wholesale because a sweep
	// could not make room. That is the gate failing, not working, and it has
	// to be visible: see RenewalGateResets.
	resets uint64
	now    func() time.Time
}

const (
	// renewalGateSweepFloor is the smallest table worth sweeping. Below it the
	// sweep costs more than the entries save.
	renewalGateSweepFloor = 4096

	// renewalGateCeiling is the most keys one process will remember.
	//
	// A worker holds one entry per generation-scoped key of every Plan it owns
	// -- two per Plan, the gap marker and the no-data memory -- plus, for up to
	// one interval, the keys of the generations a rollout just replaced. At
	// around 100 bytes of key plus the map's own overhead this is roughly 30MB
	// held at the ceiling, and it covers every Plan of the largest deployment
	// this runs on landing on a single worker.
	//
	// Reaching it is not a bound doing its job. Everything in the table is
	// live at that point, so there is nothing to drop that would not change
	// behaviour, and the gate clears and starts again -- which is the whole
	// cost it exists to avoid, paid at once. That is why it is counted.
	renewalGateCeiling = 200000
)

func newRenewalGate() *renewalGate {
	return &renewalGate{expiry: make(map[string]time.Time), sweepAt: renewalGateSweepFloor, now: time.Now}
}

// Ask reports whether key should be asked about now. A nil gate always asks,
// so a store built without one behaves exactly as it did before the gate
// existed.
func (gate *renewalGate) Ask(key string) bool {
	if gate == nil {
		return true
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	due, known := gate.expiry[key]
	return !known || !gate.now().Before(due)
}

// Answered records that an ask came back without an error, which is what makes
// the guarantee this gate spends. A failed ask is not recorded by its caller,
// so the next load asks again.
func (gate *renewalGate) Answered(key string, interval time.Duration) {
	if gate == nil || interval <= 0 {
		return
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if len(gate.expiry) >= gate.sweepAt {
		gate.makeRoom()
	}
	gate.expiry[key] = gate.now().Add(interval)
}

// makeRoom drops the entries that have come due, and only clears the table
// when that frees nothing and the ceiling has been reached.
//
// Dropping a due entry changes no behaviour at all: the next load was going to
// ask about it anyway, which is the difference between this and evicting by
// age. Keys of a generation nothing loads any more leave here, one interval
// after the rollout that replaced them.
//
// Caller holds the lock.
func (gate *renewalGate) makeRoom() {
	now := gate.now()
	for key, due := range gate.expiry {
		if !now.Before(due) {
			delete(gate.expiry, key)
		}
	}
	if len(gate.expiry) >= renewalGateCeiling {
		// Everything left is live, so there is nothing to drop that would not
		// change behaviour. Forgetting all of it is the honest failure: it
		// costs one round of asking and no wrong answers, where keeping some
		// of it would mean deciding which Plans matter -- a judgement this has
		// no way to make and no way to be caught making wrongly.
		gate.expiry = make(map[string]time.Time)
		gate.resets++
		gate.sweepAt = renewalGateSweepFloor
		return
	}
	// Sweep again once the live set has doubled, so the cost of sweeping is
	// spread over as many inserts as the table holds rather than falling on
	// every insert once the table is large. Without this a full table sweeps
	// on every single Answered, which is how a gate goes from saving every
	// round trip to saving none while still looking like it is working.
	gate.sweepAt = 2 * len(gate.expiry)
	if gate.sweepAt < renewalGateSweepFloor {
		gate.sweepAt = renewalGateSweepFloor
	}
	if gate.sweepAt > renewalGateCeiling {
		gate.sweepAt = renewalGateCeiling
	}
}

// Resets is how many times this gate has forgotten everything it knew.
func (gate *renewalGate) Resets() uint64 {
	if gate == nil {
		return 0
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.resets
}

// RenewalGateResets is how many times this store has forgotten every key life
// it remembered, because the keys it is being asked about no longer fit.
//
// A steady zero is the expected reading and the only healthy one. Every reset
// puts the whole fleet's renewal traffic back on Redis until the table fills
// again, and nothing else says so: the Slots keep passing, the keys keep being
// renewed, and the only symptom is the EVAL rate returning to where it was
// before the gate existed.
func (store *ExecutionStore) RenewalGateResets() uint64 {
	if store == nil {
		return 0
	}
	return store.renewals.Resets()
}
