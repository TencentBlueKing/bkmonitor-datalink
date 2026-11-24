// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// KindSourceUnknownHeld: an object with a Level whose no-data end has been
// held longer than its bound because the source it reads could not be shown
// alive (sourceliveness.HeldBound). The alert stays open, which is the
// recovery ruling - no data from a source that may be down is not "it
// stopped happening" - but an alert open on a retired source or a broken
// route is not left without a line.
const KindSourceUnknownHeld = "SOURCE_UNKNOWN_HELD"

// SourceUnknownHeld is a SOURCE_UNKNOWN_HELD row's held Levels, for one
// strategy of the object: how many, how many past the bound, and the oldest
// one's source kind, table, first held time, last probe outcome and bound.
type SourceUnknownHeld struct {
	Strategy     StrategyRef `json:"strategy"`
	Levels       int         `json:"levels"`
	PastBound    int         `json:"past_bound"`
	SourceKind   string      `json:"source_kind"`
	TableID      string      `json:"table_id"`
	FirstHeld    time.Time   `json:"first_held"`
	LastResult   string      `json:"last_result"`
	LastReason   string      `json:"last_reason"`
	BoundSeconds int64       `json:"bound_seconds"`
	// ObservedAt is when this process last read the account; one not read
	// again for an hour is the Plan gone from this replica, and not listed.
	ObservedAt time.Time `json:"observed_at"`
}

// sourceHeldStale is how long an account read no more is kept on the page:
// a held Plan's account is written every round, so an hour without one is a
// Plan this replica no longer runs.
const sourceHeldStale = time.Hour

// noteSourceHeld keeps a source_liveness line's account for its strategy:
// replaced while any Level is held, removed when none is. Called with the
// lock held.
func (tracker *Tracker) noteSourceHeld(queryGroup string, trace observability.TraceFields, fact *observability.SourceLivenessHeldFact) {
	if fact == nil || trace.StrategyID == "" {
		return
	}
	strategy := StrategyRef{StrategyID: trace.StrategyID, BusinessID: trace.BusinessID}
	state := tracker.groups[queryGroup]
	if fact.Levels <= 0 {
		if state != nil {
			delete(state.sourceHeld, strategy)
		}
		return
	}
	if state == nil {
		if len(tracker.groups) >= tracker.maxTracked {
			tracker.noteDropped(TrackerDroppedObservation, queryGroup)
			return
		}
		state = &queryGroupState{strategies: map[StrategyRef]struct{}{}}
		tracker.groups[queryGroup] = state
	}
	if state.sourceHeld == nil {
		state.sourceHeld = map[StrategyRef]SourceUnknownHeld{}
	}
	state.strategies[strategy] = struct{}{}
	held := SourceUnknownHeld{Strategy: strategy, Levels: fact.Levels, PastBound: fact.PastBound, SourceKind: fact.SourceKind,
		TableID: fact.TableID, LastResult: fact.LastResult, LastReason: fact.LastReason, BoundSeconds: fact.BoundSeconds,
		ObservedAt: tracker.now()}
	if fact.FirstHeldUnix > 0 {
		held.FirstHeld = time.Unix(fact.FirstHeldUnix, 0).UTC()
	}
	state.sourceHeld[strategy] = held
}

// sourceHeldAnomalies is a SOURCE_UNKNOWN_HELD row per strategy with a
// Level held past its bound, read again within the hour. Called with the
// lock held.
func (tracker *Tracker) sourceHeldAnomalies() []Anomaly {
	var anomalies []Anomaly
	now := tracker.now()
	for queryGroup, state := range tracker.groups {
		for strategy, held := range state.sourceHeld {
			if now.Sub(held.ObservedAt) > sourceHeldStale {
				delete(state.sourceHeld, strategy)
				continue
			}
			if held.PastBound <= 0 {
				continue
			}
			held := held
			anomalies = append(anomalies, Anomaly{QueryGroup: queryGroup, Kind: KindSourceUnknownHeld,
				ReasonCode: "SOURCE_UNKNOWN", Since: held.FirstHeld, SinceFrom: SinceSnapshotContinuity, Replica: tracker.replica,
				Strategies: []StrategyRef{strategy}, SourceUnknownHeld: &held})
		}
	}
	sort.Slice(anomalies, func(left, right int) bool {
		if anomalies[left].QueryGroup != anomalies[right].QueryGroup {
			return anomalies[left].QueryGroup < anomalies[right].QueryGroup
		}
		return anomalies[left].Strategies[0].StrategyID < anomalies[right].Strategies[0].StrategyID
	})
	return anomalies
}
