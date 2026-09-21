// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"errors"
	"time"
)

// FrozenSeriesState is one series this Slot read and is not going to write.
//
// Its Levels all froze -- the inputs were incomplete, or the trigger held the
// series where it was -- so the evaluation produced no mutation for it. That
// is the ordinary outcome for a series whose data source is having a bad hour,
// and it is invisible: the key is read every Slot and written by none of them.
//
// The key's life, though, is set only by a write. Nothing else has ever
// touched it: one PSETEX per apply, no read-time refresh. So a series that
// stays frozen for longer than the TTL loses its state, and the loss is silent
// unless the expiry happens to land between a Slot's read and its write, in
// which case it surfaces as a version conflict and the whole Slot retries.
// Both readings are the same fact, and this type is what carries it to the
// store that can act on it.
type FrozenSeriesState struct {
	Identity StateKeyIdentity
	// LastApplied is the evaluation time of the write that stored what was
	// read, in Unix seconds. It is the only thing anyone knows about the key's
	// age without asking Redis, and it is exact: the TTL was set by that same
	// write.
	LastApplied EvaluationTime
}

func (state FrozenSeriesState) Validate() error {
	if err := state.Identity.Plan.Validate(); err != nil {
		return err
	}
	if state.Identity.StateGeneration == "" || state.Identity.SeriesIdentityDigest == "" {
		return errors.New("alarmd execution: frozen series state requires a complete state key identity")
	}
	if state.LastApplied <= 0 {
		return errors.New("alarmd execution: frozen series state requires the evaluation time it was applied at")
	}
	return nil
}

// FrozenStateRenewalRequest asks the store to keep alive the keys of one Plan's
// frozen series.
//
// Retention is the Plan's own, derived from the Plan frozen with this Slot, and
// it is required for the same reason the apply path requires it: the life a key
// is given has to be computed from the retention the stored window was built
// from, and deriving it from anywhere else can size a TTL against a window that
// never existed.
type FrozenStateRenewalRequest struct {
	Contract  FrozenExecutionContractRef
	Retention []StateRetentionRequirement
	Items     []FrozenSeriesState
	// Now is when the ages in Items are measured against. Passed rather than
	// read from the clock inside the store so a test can put a key past half
	// its life without waiting out half its life.
	Now time.Time
}

func (request FrozenStateRenewalRequest) Validate() error {
	if err := request.Contract.Validate(); err != nil {
		return err
	}
	if len(request.Items) == 0 {
		return errors.New("alarmd execution: frozen state renewal requires at least one series")
	}
	if len(request.Retention) == 0 {
		return errors.New("alarmd execution: frozen state renewal requires the Plan retention")
	}
	if request.Now.IsZero() {
		return errors.New("alarmd execution: frozen state renewal requires the time to measure ages against")
	}
	seen := make(map[StateKeyIdentity]struct{}, len(request.Items))
	for _, item := range request.Items {
		if err := item.Validate(); err != nil {
			return err
		}
		if _, duplicate := seen[item.Identity]; duplicate {
			return errors.New("alarmd execution: duplicate frozen series in one renewal request")
		}
		seen[item.Identity] = struct{}{}
	}
	return nil
}

// FrozenRenewalOutcome is what happened to one frozen series' key.
type FrozenRenewalOutcome string

const (
	// FrozenRenewalRenewed is a key that was past the threshold and now has a
	// full life again.
	FrozenRenewalRenewed FrozenRenewalOutcome = "RENEWED"
	// FrozenRenewalFresh is a key with life to spare. It may have been decided
	// without a round trip -- see the age gate and the ask gate -- which is
	// the point: the cost of this mechanism is the asking, not the writing.
	FrozenRenewalFresh FrozenRenewalOutcome = "FRESH"
	// FrozenRenewalMissing is a key that this Slot read and that was already
	// gone when the renewal asked about it.
	//
	// This is the reading the whole change exists to produce. Until now the
	// same event either cost a Slot a retry, when the expiry landed inside the
	// read-to-write window, or cost the series its history with nothing
	// recording it at all, when the expiry landed anywhere else. The second is
	// the common case and the one that had no name.
	FrozenRenewalMissing FrozenRenewalOutcome = "MISSING"
	// FrozenRenewalFailed is a key whose renewal could not be attempted or
	// answered. The Slot goes on: a renewal is bookkeeping about a key that
	// was already read, and a Slot must not fail over it.
	FrozenRenewalFailed FrozenRenewalOutcome = "FAILED"
)

// FrozenRenewalOutcomes is every outcome, in the order a reader wants them:
// the mechanism working, the cheap case, the loss it exists to name, and the
// failure. Exported so the metric can create all four at startup -- two of the
// four are read as zeros, and a zero only reads as one if the series exists.
var FrozenRenewalOutcomes = []FrozenRenewalOutcome{
	FrozenRenewalRenewed, FrozenRenewalFresh, FrozenRenewalMissing, FrozenRenewalFailed,
}

// FrozenStateRenewalItem is one series' outcome.
type FrozenStateRenewalItem struct {
	Identity StateKeyIdentity
	Outcome  FrozenRenewalOutcome
	// ReasonCode names a FAILED outcome and is empty otherwise.
	ReasonCode ReasonCode
}

// FrozenStateRenewalResult carries one item per requested series, in request
// order. Every series gets an answer: a renewal that reports only what it did
// cannot be checked against what it was asked to do.
type FrozenStateRenewalResult struct {
	Items []FrozenStateRenewalItem
}

// Tally counts the outcomes, which is how the Slot reports them.
func (result FrozenStateRenewalResult) Tally() (renewed, fresh, missing, failed int) {
	for _, item := range result.Items {
		switch item.Outcome {
		case FrozenRenewalRenewed:
			renewed++
		case FrozenRenewalMissing:
			missing++
		case FrozenRenewalFailed:
			failed++
		default:
			fresh++
		}
	}
	return renewed, fresh, missing, failed
}

// ValidateFrozenStateRenewal checks the store answered for exactly the series
// it was asked about, in order.
func ValidateFrozenStateRenewal(request FrozenStateRenewalRequest, result FrozenStateRenewalResult) error {
	if len(result.Items) != len(request.Items) {
		return errors.New("alarmd execution: frozen state renewal did not answer for every series")
	}
	for index, item := range result.Items {
		if item.Identity != request.Items[index].Identity {
			return errors.New("alarmd execution: frozen state renewal answered for a different series")
		}
		switch item.Outcome {
		case FrozenRenewalRenewed, FrozenRenewalFresh, FrozenRenewalMissing:
			if item.ReasonCode != "" {
				return errors.New("alarmd execution: only a failed frozen state renewal carries a reason")
			}
		case FrozenRenewalFailed:
			if item.ReasonCode == "" {
				return errors.New("alarmd execution: a failed frozen state renewal has to name its reason")
			}
		default:
			return errors.New("alarmd execution: unknown frozen state renewal outcome")
		}
	}
	return nil
}
