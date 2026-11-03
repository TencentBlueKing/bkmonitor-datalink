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
	"context"
	"log/slog"
)

// The paths that finalise a Slot as skipped, one word each.
const (
	// SlotSkippedQueryFree is one Slot finalised without a query: an expired
	// replay (by age, by distance, or by a readiness wait that outlasts the
	// distance) as GAP_SKIPPED, or as SNAPSHOT_UNAVAILABLE once past its
	// recovery bound, and a Slot whose Snapshot is missing or corrupt past
	// that bound. The cause says which.
	SlotSkippedQueryFree = "query_free"
	// SlotSkippedExpiredRange is a run of expired Slots finalised as one
	// range: GAP_SKIPPED for a distance-expired range, SNAPSHOT_UNAVAILABLE
	// for an age-expired one.
	SlotSkippedExpiredRange = "expired_range"
	// SlotSkippedProgressStore is an unfinished Slot the Progress store
	// dropped because a later Segment moved the cursor past it.
	SlotSkippedProgressStore = "progress_store"
	// SlotSkippedCursorSkip is a span the cursor was moved over because the
	// timeline under it is gone or was rewritten.
	SlotSkippedCursorSkip = "cursor_skip"
	// SlotSkippedOther is a producer this build does not name.
	SlotSkippedOther = "other"
)

// SlotSkippedPaths is every value the path takes.
var SlotSkippedPaths = []string{SlotSkippedQueryFree, SlotSkippedExpiredRange, SlotSkippedProgressStore,
	SlotSkippedCursorSkip, SlotSkippedOther}

// What a skip leaves behind for the Levels after it.
const (
	// SkipGapPlanMarkers is a whole-Plan gap marker on every due Plan of the
	// Slot: each of their Levels is held until its required full rounds.
	SkipGapPlanMarkers = "plan_gap_markers"
	// SkipGapProgressOnly is the recent gap on the Progress record alone; no
	// Plan marker is written by the skip itself.
	SkipGapProgressOnly = "progress_recent_gap"
)

// slotSkippedPlanSample bounds the strategy ids a line names.
const slotSkippedPlanSample = 4

// LookbackBusyFacts is what the lookback had of the Query Group in flight or
// reserved at the moment the skip was reported, read under its lock. The
// lookback samples a group about once an hour and its reads take the same
// permits as the group's own, so a skip that lands inside one is the case
// this reading exists to show.
type LookbackBusyFacts struct {
	Capturing        bool
	SamplePending    bool
	ProbePending     bool
	ProbationRereads int
	DirectedPending  int
	ReadsInFlight    int
	TrialPrepared    bool
}

// Held says the lookback had anything of the group in flight or reserved.
func (facts LookbackBusyFacts) Held() bool {
	return facts.Capturing || facts.SamplePending || facts.ProbePending || facts.ProbationRereads > 0 ||
		facts.DirectedPending > 0 || facts.ReadsInFlight > 0 || facts.TrialPrepared
}

// SlotSkipReplayFacts is the expiry decision as the slot source took it:
// both values come from that call, on its clock, not recomputed here.
type SlotSkipReplayFacts struct {
	Reason string
	// AgeMillis is the decision instant less the Slot's evaluation time;
	// AgePastDeadlineMillis what the age rule compares, the decision instant
	// less the Slot's query deadline, against AgeLimitMillis.
	AgeMillis             int64
	AgePastDeadlineMillis int64
	AgeLimitMillis        int64
	// DistanceSlots is the grid points counted since the Slot, compared
	// against DistanceLimitSlots; zero when the age rule decided first.
	DistanceSlots      uint32
	DistanceLimitSlots uint32
}

// SlotSkipShareFacts is the share refusal a RESOURCE_SHARE_EXCEEDED skip was
// decided on: the phase it was refused in, what the Slot held and asked for,
// the share it was measured against, and how many identical refusals the
// Runner took before giving the Slot up. The bytes are the last refusal's.
type SlotSkipShareFacts struct {
	Phase          string
	OwnUsedBytes   uint64
	RequestedBytes uint64
	LimitBytes     uint64
	Refusals       uint32
}

// SlotSkippedFacts is one Slot, or one range of them, finalised as skipped.
type SlotSkippedFacts struct {
	Path      string
	FirstSlot int64
	LastSlot  int64
	SlotCount uint32
	Reason    string
	Cause     string
	GapGuard  string
	GapPlans  int
	// GapStrategies is the first few due Plans' strategy ids.
	GapStrategies []string
	Replay        *SlotSkipReplayFacts
	// Share is set on a Slot finalized as RESOURCE_SHARE_EXCEEDED.
	Share *SlotSkipShareFacts
	// Range is the distance a range was sized by, when it was built this
	// round; rendered as the range_distance_expired line renders it.
	Range *RangeDistanceFacts
	// Lookback is nil when no lookback reading was available.
	Lookback *LookbackBusyFacts
}

func normalizeSlotSkippedFacts(facts *SlotSkippedFacts) *SlotSkippedFacts {
	if facts == nil {
		return nil
	}
	normalized := *facts
	known := false
	for _, path := range SlotSkippedPaths {
		known = known || normalized.Path == path
	}
	if !known {
		normalized.Path = SlotSkippedOther
	}
	if len(normalized.GapStrategies) > slotSkippedPlanSample {
		normalized.GapStrategies = normalized.GapStrategies[:slotSkippedPlanSample]
	}
	return &normalized
}

type lookbackBusyKey struct{}

// ContextWithLookbackBusy carries the reading of the Query Group's lookback
// into the round, so whichever site finalises a Slot as skipped -- the
// Runner, the slot source or the Progress store -- reads it at that moment.
func ContextWithLookbackBusy(ctx context.Context, read func() (LookbackBusyFacts, bool)) context.Context {
	if read == nil {
		return ctx
	}
	return context.WithValue(ctx, lookbackBusyKey{}, read)
}

// LookbackBusyFromContext reads the lookback now; nil without a reading.
func LookbackBusyFromContext(ctx context.Context) *LookbackBusyFacts {
	if ctx == nil {
		return nil
	}
	read, _ := ctx.Value(lookbackBusyKey{}).(func() (LookbackBusyFacts, bool))
	if read == nil {
		return nil
	}
	facts, known := read()
	if !known {
		return nil
	}
	return &facts
}

// ObserveSlotSkipped reports one Slot, or range, finalised as skipped: the
// line every gap guard of GAP_SKIPPED goes back to. It is never sampled
// (mandatoryLogStage) -- its volume is the skips themselves -- and carries
// what held the round before it from the context, and what the lookback has
// of the group read now.
func ObserveSlotSkipped(ctx context.Context, observer Observer, trace TraceFields, operation Operation, facts SlotSkippedFacts) {
	if observer == nil {
		return
	}
	// Observability is a fail-open side channel.
	defer func() { _ = recover() }()
	if facts.Lookback == nil {
		facts.Lookback = LookbackBusyFromContext(ctx)
	}
	observer.Observe(ctx, Observation{
		Component: ComponentScheduler, Stage: StageSlotSkipped, Operation: operation,
		Result: ResultDegraded, ReasonCode: ReasonCode(facts.Reason),
		Trace: trace, HeldBy: HeldByFromContext(ctx), SlotSkipped: &facts, RangeDistance: facts.Range,
	})
}

// appendSlotSkippedAttributes renders the line; held_by is rendered with every
// other line's, and is never absent (HeldByFromContext).
func appendSlotSkippedAttributes(attributes []slog.Attr, facts *SlotSkippedFacts) []slog.Attr {
	if facts == nil {
		return attributes
	}
	attributes = append(attributes,
		slog.String("finalizing_path", facts.Path),
		slog.Int64("skipped_first_slot", facts.FirstSlot),
		slog.Int64("skipped_last_slot", facts.LastSlot),
		slog.Uint64("skipped_slot_count", uint64(facts.SlotCount)),
		slog.String("skip_reason", facts.Reason),
		slog.String("skip_cause", facts.Cause),
		slog.String("skip_gap_guard", facts.GapGuard),
		slog.Int("skip_gap_plans", facts.GapPlans),
		slog.Any("skip_gap_strategies", facts.GapStrategies),
	)
	if busy := facts.Lookback; busy != nil {
		attributes = append(attributes,
			slog.Bool("held_by_lookback", busy.Held()),
			slog.Bool("lookback_capturing", busy.Capturing),
			slog.Bool("lookback_sample_pending", busy.SamplePending),
			slog.Bool("lookback_probe_pending", busy.ProbePending),
			slog.Int("lookback_probation_rereads", busy.ProbationRereads),
			slog.Int("lookback_directed_pending", busy.DirectedPending),
			slog.Int("lookback_reads_in_flight", busy.ReadsInFlight),
			slog.Bool("lookback_trial_prepared", busy.TrialPrepared),
		)
	} else {
		attributes = append(attributes, slog.Bool("held_by_lookback_known", false))
	}
	if replay := facts.Replay; replay != nil {
		attributes = append(attributes,
			slog.String("replay_expiry_reason", replay.Reason),
			slog.Int64("replay_age_ms", replay.AgeMillis),
			slog.Int64("replay_age_past_deadline_ms", replay.AgePastDeadlineMillis),
			slog.Int64("replay_age_limit_ms", replay.AgeLimitMillis),
			slog.Uint64("replay_distance_slots", uint64(replay.DistanceSlots)),
			slog.Uint64("replay_distance_limit_slots", uint64(replay.DistanceLimitSlots)),
		)
	}
	if share := facts.Share; share != nil {
		attributes = append(attributes,
			slog.String("share_refusal_phase", share.Phase),
			slog.Uint64("share_own_used_bytes", share.OwnUsedBytes),
			slog.Uint64("share_requested_bytes", share.RequestedBytes),
			slog.Uint64("share_limit_bytes", share.LimitBytes),
			slog.Uint64("share_refusals", uint64(share.Refusals)),
		)
	}
	return attributes
}
