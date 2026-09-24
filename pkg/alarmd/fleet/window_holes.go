// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"sort"
	"strconv"
	"time"

	model "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// RecentRoundsKept bounds the completions the tracker remembers per object.
// Sixteen covers a nine-position window at any period with room for the
// window to slide; a window longer than that reads its older holes as
// NOT_IN_MEMORY, and the row says how many rounds it could read against.
const RecentRoundsKept = 16

// roundMark is one remembered completion: the Slot, the record minute the
// round evaluated (zero when the round carried no record, then inferred from
// the object's Slot offset when one is known), the completion's kind and
// reason, and what the primary query answered.
type roundMark struct {
	slot        int64
	end         int64
	endInferred bool
	kind        string
	reason      string
	primary     *observability.PrimaryInputFacts
}

// rememberRound files one completion on the object's ring and keeps the
// Slot-to-minute offset current from any round that reported a minute.
func rememberRound(state *queryGroupState, slot int64, kind, reason string, coverage *observability.HistoryCoverageFacts, primary *observability.PrimaryInputFacts) {
	mark := roundMark{slot: slot, kind: kind, reason: reason}
	if primary != nil {
		copied := *primary
		mark.primary = &copied
	}
	if coverage != nil && coverage.End > 0 {
		mark.end = coverage.End
		if slot > 0 {
			state.slotOffset, state.slotOffsetKnown = slot-coverage.End, true
		}
	} else if state.slotOffsetKnown && slot > 0 {
		mark.end, mark.endInferred = slot-state.slotOffset, true
	}
	state.rounds = append(state.rounds, mark)
	if len(state.rounds) > RecentRoundsKept {
		state.rounds = state.rounds[len(state.rounds)-RecentRoundsKept:]
	}
}

// roundAt finds the remembered round that evaluated the minute, a round that
// reported the minute before one whose minute was inferred.
func roundAt(rounds []roundMark, minute int64) (roundMark, bool) {
	var inferred roundMark
	inferredFound := false
	for i := len(rounds) - 1; i >= 0; i-- {
		mark := rounds[i]
		if mark.end != minute {
			continue
		}
		if !mark.endInferred {
			return mark, true
		}
		if !inferredFound {
			inferred, inferredFound = mark, true
		}
	}
	return inferred, inferredFound
}

// queryFreeCompletion reports whether the completion kind is one that never
// reached the query stage, from the module's own list rather than a copy.
func queryFreeCompletion(kind string) bool {
	for _, queryFree := range model.QueryFreeCompletionKinds {
		if kind == string(queryFree) {
			return true
		}
	}
	return false
}

// windowKey is the identity a reader follows across rounds.
func windowKey(strategy, series string, level uint32) string {
	key := series + "/" + strconv.FormatUint(uint64(level), 10)
	if strategy == "" {
		return key
	}
	return strategy + "/" + key
}

// windowRows reads every named window of the round against the object's
// remembered rounds: one WindowRow per fact, each listed hole with whose
// minute it is, the unlisted holes counted as beyond memory, and the verdict
// decided from the counts.
func windowRows(rounds []roundMark, facts *observability.HistoryCoverageFacts) []WindowRow {
	if facts == nil || len(facts.Windows) == 0 {
		return nil
	}
	rows := make([]WindowRow, 0, len(facts.Windows))
	for _, window := range facts.Windows {
		row := WindowRow{
			Key:      windowKey(window.Strategy, window.Series, window.Level),
			Strategy: window.Strategy, Business: window.Business, Series: window.Series, Level: window.Level,
			Valid: window.Valid, Required: window.Required, End: time.Unix(window.End, 0).UTC(),
			Guarded: window.Guarded, GuardReason: window.GuardReason, Fresh: window.Fresh,
			MissingTotal: window.MissingTotal, UnusableTotal: window.UnusableTotal,
		}
		for _, minute := range window.Missing {
			hole := WindowHole{At: time.Unix(minute, 0).UTC()}
			if mark, found := roundAt(rounds, minute); found {
				hole.Round, hole.Reason, hole.Inferred = mark.kind, mark.reason, mark.endInferred
				switch {
				case mark.primary.PrimaryAnsweredWhole():
					hole.Cause = HoleAnsweredWithoutSeries
					row.HolesBy.AnsweredWithoutSeries++
				case mark.primary != nil && mark.primary.Completeness == "FULL":
					hole.Cause = HoleAnsweredEmpty
					row.HolesBy.AnsweredEmpty++
				case mark.primary != nil || queryFreeCompletion(mark.kind):
					// PARTIAL or UNAVAILABLE, or a Slot given up without a
					// query -- the kind itself says no primary was asked
					// for: the minute was not seen whole by this side.
					hole.Cause = HoleInputIncomplete
					row.HolesBy.InputIncomplete++
				default:
					// A round that ran a query and did not record what it
					// answered: not this side's by default, and not the
					// data's either.
					hole.Cause = HolePrimaryUnrecorded
					row.HolesBy.PrimaryUnrecorded++
				}
			} else {
				hole.Cause = HoleNotInMemory
				row.HolesBy.NotInMemory++
			}
			row.Holes = append(row.Holes, hole)
		}
		for _, minute := range window.Unusable {
			hole := WindowHole{At: time.Unix(minute, 0).UTC(), Cause: HolePointUnusable}
			if mark, found := roundAt(rounds, minute); found {
				hole.Round, hole.Reason, hole.Inferred = mark.kind, mark.reason, mark.endInferred
			}
			row.HolesBy.Unusable++
			row.Holes = append(row.Holes, hole)
		}
		// Holes beyond the listing bound are counted and not named, and a
		// hole nobody named is one nobody can speak for.
		if unlisted := window.MissingTotal - uint32(len(window.Missing)); unlisted > 0 {
			row.HolesBy.NotInMemory += unlisted
		}
		if unlisted := window.UnusableTotal - uint32(len(window.Unusable)); unlisted > 0 {
			row.HolesBy.Unusable += unlisted
		}
		sort.SliceStable(row.Holes, func(i, j int) bool { return row.Holes[i].At.Before(row.Holes[j].At) })
		row.Verdict = verdictOf(row.HolesBy)
		rows = append(rows, row)
	}
	return rows
}
