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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// RenewFrozenRuntime keeps alive the Runtime State keys of series this Slot
// read and is not going to write.
//
// A key's life is set by its write and by nothing else, and a frozen series
// produces no write. So the state of a series whose Levels stay frozen for
// longer than the TTL is deleted by Redis while the Plan is still evaluating
// it every minute. The three candidates that were considered and rejected are
// worth keeping here, because each fails in a way that is easy to talk oneself
// back into:
//
//   - Renew on every read, unconditionally. Doubles the command rate of the
//     read path -- the deployment this was measured on reads about 3,200 keys
//     a second -- to keep 30 of them alive.
//   - Give every key a longer TTL. Expiry times are not spread out: the TTL is
//     a whole number of minutes and a Slot runs at a fixed offset within its
//     minute, so a longer TTL moves the expiry to a later minute at the same
//     offset, which is still inside some Slot's read-to-write window. And a
//     freeze longer than the new TTL still loses the state.
//   - Let a write whose key vanished create it. That contradicts the contract
//     the CAS enforces -- a missing key means the state is gone and the series
//     restarts -- and it would silently undo an operator's deletion. It also
//     does nothing for the freezes that outlast the TTL without a write.
//
// What is done instead costs one command per frozen key that is actually
// running out. Two gates keep it there: the age gate below, which answers from
// the blob this Slot already read, and the ask gate, which remembers that a key
// asked about recently has a guaranteed remaining life to spend.
func (store *ExecutionStore) RenewFrozenRuntime(
	ctx context.Context, request execution.FrozenStateRenewalRequest,
) (execution.FrozenStateRenewalResult, error) {
	if store == nil {
		return execution.FrozenStateRenewalResult{}, errors.New("state: execution store is required")
	}
	if err := request.Validate(); err != nil {
		return execution.FrozenStateRenewalResult{}, err
	}
	if len(request.Items) > store.options.MaxItemsPerCall {
		return execution.FrozenStateRenewalResult{}, errors.New("state: too many frozen series in one renewal request")
	}
	ttl, err := store.runtimeTTL(request.Retention, request.HorizonSeconds)
	if err != nil {
		// A retention no configured TTL can serve is the apply path's refusal
		// to make, and it makes it per Plan with a named reason. Repeating it
		// here would fail the renewal of keys the apply path is about to
		// refuse anyway, and would report the refusal twice under two
		// different names.
		return execution.FrozenStateRenewalResult{}, err
	}
	threshold := GenerationScopedRenewalThreshold(ttl)
	result := execution.FrozenStateRenewalResult{Items: make([]execution.FrozenStateRenewalItem, len(request.Items))}

	// Grouped by storage target, because the renewal for each group goes out
	// as one pipeline and a target is what a pipeline can be sent on. Order
	// within a group follows the request, and every item keeps its request
	// position in the result.
	type pending struct {
		target    StorageTarget
		positions []int
		keys      []string
	}
	groups := make(map[string]*pending)
	order := make([]string, 0, 1)
	for index, item := range request.Items {
		result.Items[index] = execution.FrozenStateRenewalItem{Identity: item.Identity}
		if !frozenKeyIsRunningOut(item, request.Now, ttl) {
			result.Items[index].Outcome = execution.FrozenRenewalFresh
			continue
		}
		// The key the record was read from. A framed record lives under
		// runtime3 and an envelope under runtime; renewing the other key
		// keeps nothing alive, and a frozen series is never written, so the
		// key it was read from is the key it stays under.
		key, err := frozenRecordKey(store.options.Prefix, item)
		if err == nil {
			var target StorageTarget
			target, err = store.options.Router.Route(item.Identity.Plan.TenantID, item.Identity.Plan.StrategyID)
			if err == nil {
				if !store.frozenRenewals.Ask(key) {
					result.Items[index].Outcome = execution.FrozenRenewalFresh
					continue
				}
				group, known := groups[target.Name]
				if !known {
					group = &pending{target: target}
					groups[target.Name] = group
					order = append(order, target.Name)
				}
				group.positions = append(group.positions, index)
				group.keys = append(group.keys, key)
				continue
			}
		}
		result.Items[index].Outcome = execution.FrozenRenewalFailed
		result.Items[index].ReasonCode = execution.ReasonCode(contract.ReasonStateCorrupt)
	}

	for _, name := range order {
		group := groups[name]
		outcomes, err := renewFrozenGroup(ctx, group.target, group.keys, ttl, threshold)
		for position, index := range group.positions {
			if err != nil {
				result.Items[index].Outcome = execution.FrozenRenewalFailed
				result.Items[index].ReasonCode = execution.ReasonCode(contract.ReasonRedisUnavailable)
				continue
			}
			switch outcomes[position] {
			case RenewalRenewed:
				result.Items[index].Outcome = execution.FrozenRenewalRenewed
				// Only an answered ask may be spent against. A failed one
				// leaves the key's remaining life unknown, so it is not
				// recorded and the next Slot asks again.
				store.frozenRenewals.Answered(group.keys[position], RenewalAskInterval(ttl))
			case RenewalMissing:
				// Deliberately not recorded in the ask gate. The key is gone;
				// the next Slot will read it as missing and start the series
				// again, and there is no remaining life to spend.
				result.Items[index].Outcome = execution.FrozenRenewalMissing
			default:
				result.Items[index].Outcome = execution.FrozenRenewalFresh
				store.frozenRenewals.Answered(group.keys[position], RenewalAskInterval(ttl))
			}
		}
	}
	return result, execution.ValidateFrozenStateRenewal(request, result)
}

// frozenKeyIsRunningOut answers, from what this Slot already read, whether the
// key can still be assumed to have life left.
//
// The blob names the evaluation time of the write that stored it, and that
// write is what set the TTL, so the remaining life is ttl minus the age with
// no round trip at all. Below the threshold there is nothing to ask about.
//
// It cannot be the only gate. Age is measured from the last write, and a
// renewal is not a write: once a frozen key crosses half its life the age keeps
// growing and this returns true on every Slot thereafter, forever. What stops
// that is the ask gate, which spends the guarantee an answered ask leaves
// behind. The two are not redundant -- this one is free and covers the keys a
// fresh process has never asked about, and the other covers the keys that have
// crossed and stay crossed.
func frozenKeyIsRunningOut(item execution.FrozenSeriesState, now time.Time, ttl time.Duration) bool {
	age := now.Sub(time.Unix(int64(item.LastApplied), 0))
	// A negative age is a write stamped in the future, by a clock ahead of
	// this one. Asking is the safe direction, as everywhere else here.
	return age >= GenerationScopedRenewalThreshold(ttl) || age < 0
}

// renewFrozenGroup sends one target's renewals.
func renewFrozenGroup(
	ctx context.Context, target StorageTarget, keys []string, ttl, threshold time.Duration,
) ([]RenewalOutcome, error) {
	backend, ok := target.Backend.(LifetimeBackend)
	if !ok {
		return nil, ErrLifetimeUnsupported
	}
	outcomes, err := backend.RenewManyIfBelow(ctx, keys, ttl, threshold)
	if err != nil {
		return nil, err
	}
	if len(outcomes) != len(keys) {
		return nil, errors.New("state: renewal backend answered for a different number of keys")
	}
	return outcomes, nil
}

// FrozenRenewalGateResets is how many times this store has forgotten every
// Runtime State key life it remembered. See RenewalGateResets: a steady zero is
// the only healthy reading, and this table is the one that can overflow first,
// because it holds one entry per frozen series rather than two per Plan.
func (store *ExecutionStore) FrozenRenewalGateResets() uint64 {
	if store == nil {
		return 0
	}
	return store.frozenRenewals.Resets()
}

// frozenRecordKey names the key a frozen series' record lives under, by the
// representation it was read in. An item that does not say is refused by
// name rather than renewed under a guessed key: a guess that lands on the
// other key returns "renewed" while the record it meant to keep alive runs
// out, which is a loss nothing reports. Every view LoadRuntime finds carries
// the representation, and frozen items are built from found views only.
func frozenRecordKey(prefix string, item execution.FrozenSeriesState) (string, error) {
	switch item.Representation {
	case execution.StateRepresentationFramed:
		return RuntimeStateKeyV3(prefix, item.Identity)
	case execution.StateRepresentationEnvelope:
		return RuntimeStateKeyV2(prefix, item.Identity)
	}
	return "", identityError("frozen series state does not say which representation its record is in")
}
