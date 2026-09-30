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
	"context"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ErrLifetimeUnsupported is a routed backend that cannot renew a key's life.
//
// It is an error rather than a skip. A renewal nobody performs restores exactly
// the leak this mechanism exists to close, and it does so invisibly: the keys
// keep working, the Slot keeps passing, and the only symptom arrives as a Redis
// instance that has been growing for months.
var ErrLifetimeUnsupported = errors.New("state: routed backend cannot renew a key lifetime")

// ErrSlotAppliedUnsupported is a routed backend that cannot hold the mark a
// failed attempt leaves behind. It is named rather than reported as the store
// being down, because nothing about a Slot depends on it: a deployment whose
// backend cannot do this keeps exactly the behaviour it had before the mark
// existed, which is to record such a Slot as a gap.
var ErrSlotAppliedUnsupported = errors.New("state: routed backend cannot hold a slot-applied mark")

// RenewGenerationKey extends the life of a generation-scoped key that is being
// loaded, when it is running out.
//
// Renewal hangs on the load rather than on the write, and that is the whole
// design. A Plan still being evaluated loads its key every Slot, so it renews
// whether or not anything was written - and a memory that has not changed
// writes nothing, so a write-driven renewal would let a stable long absence,
// the one record that most needs to survive, be the one that expires. A Plan
// whose execution content changed stops loading the old generation's key
// entirely, and that key then ages out on its own.
//
// The threshold makes the write cheap. It does not make the ask cheap: the
// script decides inside Redis, so every load that reaches it has already spent
// a round trip, and only the ones below half go on to set a new expiry. That
// is one EVAL per generation-scoped key per Slot, which was assumed to be small
// beside the Slot's own series reads and is not - measured, it was 38.6 EVAL/s
// at 7.1ms each on a deployment of around 2,100 Plans, or 0.27 seconds of
// waiting on Redis every second.
//
// So the gate answers first, from what this process already asked. See
// renewalGate: it spends half of the guarantee an answered ask leaves behind,
// and every way it can be wrong sends the ask.
// RenewalAttempt is what one renewal did, for a caller that reports it.
//
// Asked is false when the gate answered from what this process already knows,
// which is most loads: renewal hangs on the load and a Plan loads every Slot.
// Only an attempt that reached the backend is worth reporting -- a signal that
// counted the skips would be dominated by them and would say nothing about
// whether renewal works.
type RenewalAttempt struct {
	Asked   bool
	Renewed bool
	TTL     time.Duration
}

// RenewGenerationKey renews a key and discards what it learned, for the callers
// that only need the error.
func RenewGenerationKey(
	ctx context.Context, target StorageTarget, key string, retention []execution.StateRetentionRequirement,
	restartMargin, minimum, maximum time.Duration, gate *renewalGate,
) error {
	_, err := RenewGenerationKeyReporting(ctx, target, key, retention, restartMargin, minimum, maximum, gate)
	return err
}

// RenewGenerationKeyReporting is the same renewal, and says what it did.
func RenewGenerationKeyReporting(
	ctx context.Context, target StorageTarget, key string, retention []execution.StateRetentionRequirement,
	restartMargin, minimum, maximum time.Duration, gate *renewalGate,
) (RenewalAttempt, error) {
	backend, ok := target.Backend.(LifetimeBackend)
	if !ok {
		return RenewalAttempt{}, ErrLifetimeUnsupported
	}
	requirements := make([]LevelRequirement, len(retention))
	for index, level := range retention {
		// Window facts left empty: the TTL formula reads only the retention,
		// and it must read exactly the retention the window was built from.
		requirements[index] = NewLevelRequirement(level, "", 0)
	}
	ttl, err := GenerationScopedTTL(requirements, restartMargin, minimum, maximum)
	if err != nil {
		return RenewalAttempt{}, err
	}
	// The capability check stays ahead of the gate. A backend that cannot renew
	// has to say so on every load, not on the first one and then once every
	// interval: that error is what stops a Slot from running against a store
	// where the keys leak, and a gate that hid it would restore the leak and
	// the silence together.
	if !gate.Ask(key) {
		return RenewalAttempt{}, nil
	}
	// Only RENEWED counts as renewed. MISSING reads as not renewed here, as it
	// did when the backend answered with a bool: this path renews the memory a
	// Plan is still loading, and a key that went missing between the read and
	// the renewal is the runtime state renewal's subject, not this one's.
	outcome, err := backend.RenewIfBelow(ctx, key, ttl, GenerationScopedRenewalThreshold(ttl))
	if err != nil {
		return RenewalAttempt{Asked: true, TTL: ttl}, err
	}
	gate.Answered(key, RenewalAskInterval(ttl))
	return RenewalAttempt{Asked: true, Renewed: outcome == RenewalRenewed, TTL: ttl}, nil
}
