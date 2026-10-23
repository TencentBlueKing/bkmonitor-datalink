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
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// effectiveTimeSeenTTL is how long an entry of effectiveTimeSeen outlives the
// last Slot that touched it: a Plan that left this process, by its strategy
// going or its Query Group moving, stops touching its entry and is dropped
// after this. Longer than any Plan's interval, so a Plan that runs once a day
// keeps its entry between runs.
const effectiveTimeSeenTTL = 48 * time.Hour

// effectiveTimeSeen remembers, per Plan and effective-time requirement, the
// Slot with which this process first committed that requirement. A
// window hole is judged against the schedule only from then on: before it,
// the schedule that held may have been another one -- an edit, a calendar
// changed, or a time this process never ran the Plan -- and judging an old
// minute by today's schedule could call a minute that was in hours out of
// them. Those holes stay unknown, which keeps a window in its line.
//
// Its size is the Plans this process committed in the last
// effectiveTimeSeenTTL times their requirements, one or two each: an entry
// is replaced when its Plan's digest changes and dropped when it is not
// touched for the TTL.
type effectiveTimeSeen struct {
	mu      sync.Mutex
	entries map[execution.PlanIdentity]map[string]effectiveTimeSince
	swept   time.Time
}

type effectiveTimeSince struct {
	since   int64
	touched time.Time
}

// note records the requirements a Plan has this Slot, keeping each one's
// first Slot and dropping the Plan's requirements it no longer has, and
// returns when each came into effect for this process.
func (seen *effectiveTimeSeen) note(plan execution.PlanIdentity, digests []string, slot int64, now time.Time) map[string]int64 {
	seen.mu.Lock()
	defer seen.mu.Unlock()
	if seen.entries == nil {
		seen.entries = map[execution.PlanIdentity]map[string]effectiveTimeSince{}
	}
	if now.Sub(seen.swept) >= effectiveTimeSeenTTL/4 {
		for identity, byDigest := range seen.entries {
			for digest, entry := range byDigest {
				if now.Sub(entry.touched) >= effectiveTimeSeenTTL {
					delete(byDigest, digest)
				}
			}
			if len(byDigest) == 0 {
				delete(seen.entries, identity)
			}
		}
		seen.swept = now
	}
	previous := seen.entries[plan]
	current := make(map[string]effectiveTimeSince, len(digests))
	since := make(map[string]int64, len(digests))
	for _, digest := range digests {
		// Set once, when the requirement is first seen, and never moved back:
		// a replayed or catch-up Slot for a time before an edit, or before
		// this process started, runs under today's requirement, and letting
		// it lower the bound would judge those older holes by today's
		// schedule. Their holes stay below the bound, unknown.
		entry, kept := previous[digest]
		if !kept {
			entry.since = slot
		}
		entry.touched = now
		current[digest] = entry
		since[digest] = entry.since
	}
	seen.entries[plan] = current
	return since
}

// markOutOfHours sets, on each named window of the round, the listed holes
// whose own evaluation time the window's Level schedule says is outside its
// effective time.
//
// The fact is the schedule's, asked for the hole's minute -- not a word some
// round happened to carry. A round's reason says EFFECTIVE_TIME_INACTIVE only
// when it suppressed a Level on whole inputs; a round out of hours that
// answered empty, could not query, was given up or was warming carries
// another word or none, and a window of such minutes read as the data's.
//
// A hole is judged only when everything that maps it to a time is known:
// its evaluation time is the minute plus this Slot's distance from its
// window's end, which holds within one schedule segment and from when this
// process first ran the Plan with this requirement. A hole before either,
// or one the schedule cannot answer for, is left unknown and keeps the window
// in its line.
//
// The cost is bounded by the round's own bounds: at most MaxCoverageWindows
// windows of MaxWindowHolesListed listed holes each, resolved once per
// distinct (requirement, second) and only for a requirement that is not
// ALWAYS.
func (coordinator *SlotExecutionCoordinator) markOutOfHours(
	ctx context.Context, header execution.InternalExecutionHeader, coverage *execution.HistoryCoverage,
) {
	slot := int64(header.Contract.Slot.EvaluationTime)
	now := time.Now()
	type levelKey struct {
		plan  execution.PlanIdentity
		level uint32
	}
	requirements := make(map[levelKey]strategy.EffectiveTimeRequirement)
	plans := make(map[execution.PlanIdentity]execution.DuePlan, len(header.DuePlans))
	since := make(map[execution.PlanIdentity]map[string]int64, len(header.DuePlans))
	for _, due := range header.DuePlans {
		if due.CompiledPlan == nil {
			continue
		}
		plans[due.Identity] = due
		var digests []string
		for _, level := range levelsNeedingEffectiveTime(due) {
			requirement := level.EffectiveTimeRequirement()
			if requirement.Kind() == strategy.EffectiveTimeAlways {
				continue
			}
			requirements[levelKey{due.Identity, level.Definition().LevelID}] = requirement
			digests = append(digests, requirement.Digest())
		}
		if len(digests) > 0 {
			since[due.Identity] = coordinator.effectiveSeen.note(due.Identity, digests, slot, now)
		}
	}
	// A round that left a short window unnamed cannot read as out of hours,
	// and neither can a window with a hole it did not list: the reading needs
	// every one. Their holes are not resolved; the schedule is asked only
	// where the answer can decide something.
	if coverage == nil || len(coverage.Windows) == 0 || coverage.Short > uint32(len(coverage.Windows)) {
		for index := range coverageWindows(coverage) {
			coverage.Windows[index].Inactive = nil
		}
		return
	}
	type resolved struct {
		digest string
		at     int64
	}
	inactive := make(map[resolved]bool)
	segmentStart := int64(header.Contract.ScheduleSegmentStart)
	for index := range coverage.Windows {
		window := &coverage.Windows[index]
		window.Inactive = nil
		requirement, scheduled := requirements[levelKey{window.Plan, window.LevelID}]
		listed := uint32(len(window.Missing) + len(window.Unusable))
		if !scheduled || listed != window.MissingTotal+window.UnusableTotal {
			continue
		}
		due := plans[window.Plan]
		from := since[window.Plan][requirement.Digest()]
		offset := slot - window.End
		var marked []int64
		for _, minute := range append(append([]int64(nil), window.Missing...), window.Unusable...) {
			at := minute + offset
			if at < from || at < segmentStart {
				continue
			}
			key := resolved{requirement.Digest(), at}
			outside, known := inactive[key]
			if !known {
				fact, err := due.CompiledPlan.ResolveEffectiveTimeRequirement(ctx, strategy.EffectiveTimeRequest{
					TenantID: due.Identity.TenantID, BusinessID: due.Identity.BusinessID, EvaluationTime: at, Requirement: requirement,
				}, coordinator.ports.EffectiveTime)
				outside = err == nil && fact.Status() == strategy.EffectiveTimeInactive
				inactive[key] = outside
			}
			if outside {
				marked = append(marked, minute)
			}
		}
		sort.Slice(marked, func(i, j int) bool { return marked[i] < marked[j] })
		window.Inactive = marked
	}
}

// coverageWindows is the round's named windows, none for no coverage.
func coverageWindows(coverage *execution.HistoryCoverage) []execution.WindowCoverage {
	if coverage == nil {
		return nil
	}
	return coverage.Windows
}
