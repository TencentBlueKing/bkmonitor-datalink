// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"encoding/json"
	"sort"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Absence values. A no-data group is a series with one point per Slot, and the
// point is the answer rather than a measurement: one for absent, zero for
// present. The trigger then counts absences the way it counts any other
// anomaly, which is what lets the whole path below this be the ordinary one.
const (
	AbsentValue  = 1
	PresentValue = 0
)

// SyntheticSeries is one no-data group as a series the ordinary evaluation can
// read: an identity, one point, and the dimensions the event will carry.
//
// Its dimensions always hold the no-data tag, which is what keeps this key away
// from the real series of the same item. They are the same dimensions the
// identity is built from, so a reader of the event and a reader of the state
// are looking at one object.
type SyntheticSeries struct {
	Group Group
	// Value is AbsentValue or PresentValue.
	Value int
	// Periods is how many periods the event says this group has been without
	// data. It is zero for a present group, which reports nothing.
	Periods int64
	// SourceTime is the point's own time: the period this Slot is deciding,
	// which is one period behind the Slot's evaluation time. The backend's
	// anomaly carries the same, and its record_id and anomaly_id are built
	// from it.
	SourceTime int64
}

// IdentityFields returns what the event carries and what its identity is hashed
// from: the group's own pairs plus the no-data tag, as the JSON values the
// backend writes.
//
// The tag is the JSON boolean true, and it has to be. count_md5 hashes str() of
// each value, so Python's True and the text "True" flatten together and hash
// alike - but the text "true" does not, and hashes to something else entirely.
// A tag reaching the hash in that form would give every no-data anomaly an
// anomaly_id that no Python-written record matches, which nothing downstream
// would report as an error; it would read as a fresh anomaly every time.
//
// Returning raw JSON rather than text is what keeps that from coming back. The
// hash input and the event payload are then the same values from the same call,
// so there is no second representation to convert between and get wrong: a
// map[string]string cannot hold a boolean, and the conversion that would bridge
// it is exactly where the text "true" came from.
func (series SyntheticSeries) IdentityFields() map[string]json.RawMessage {
	fields := make(map[string]json.RawMessage, len(series.Group.dimensions)+1)
	for _, dimension := range series.Group.dimensions {
		encoded, err := json.Marshal(dimension.Value)
		if err != nil {
			// A Go string always marshals, so this cannot happen; encoding the
			// value by hand rather than skipping it keeps a field that somehow
			// failed from silently leaving the identity.
			encoded = json.RawMessage(strconv.Quote(dimension.Value))
		}
		fields[dimension.Name] = encoded
	}
	fields[contract.NoDataDimensionTag] = json.RawMessage("true")
	return fields
}

// SyntheticInput is what turning verdicts into series needs beyond the verdicts.
type SyntheticInput struct {
	EvaluationTime int64
	PeriodSeconds  int64
	Result         AbsenceResult
	// Memory is the memory the result produced, which is where the period
	// counts are read from.
	Memory map[string]GroupMemory
	// Roster is the expected set the verdicts were made against, for the groups
	// the verdicts name.
	Roster Roster
}

// SyntheticSeriesFor turns one Slot's verdicts into the series the ordinary
// evaluation path reads, in group-key order so a retry produces the same list.
//
// An UNAVAILABLE verdict produces no series at all. That is the completeness
// gate reaching all the way out: a round that did not see the whole period must
// not advance the trigger window, and a point valued zero would advance it as a
// recovery while a point valued one would advance it as an absence. Producing
// nothing leaves the window where it was, which is the only reading that says
// "this round has no evidence".
func SyntheticSeriesFor(input SyntheticInput) []SyntheticSeries {
	keys := make([]string, 0, len(input.Result.Verdicts))
	for key, verdict := range input.Result.Verdicts {
		if verdict == VerdictUnavailable {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	series := make([]SyntheticSeries, 0, len(keys))
	sourceTime := input.EvaluationTime - input.PeriodSeconds
	for _, key := range keys {
		group, named := input.Roster.Groups[key]
		if !named {
			// Two verdicts are not roster groups: the whole item, and the one
			// NORMAL that closes an absence on a group the roster has stopped
			// expecting (A8). The second cannot come from the roster -- being
			// dropped from it is what it is -- so its group comes back out of
			// its key, the same round trip the history roster makes.
			//
			// This has to be right rather than approximately right. The point
			// carries the group's identity all the way to the event, so a
			// closing recovery built from the wrong group would end some other
			// alert and leave the one it was for standing, which is worse than
			// the verdict never having been made.
			parsed, ok := ParseGroupKey(key)
			if !ok {
				continue
			}
			group = parsed
		}
		entry := SyntheticSeries{Group: group, SourceTime: sourceTime, Value: PresentValue}
		if input.Result.Verdicts[key] == VerdictAnomaly {
			entry.Value = AbsentValue
			entry.Periods = absentPeriods(input.Memory[key], input.EvaluationTime, input.PeriodSeconds)
		}
		series = append(series, entry)
	}
	return series
}

// absentPeriods is how many periods the event says this group has been without
// data, following the backend's two counts and its choice between them.
//
// The backend keeps two checkpoints and derives a number from each: one from
// the last point it saw, one from the first round it called this group absent.
// It reports the first when that is positive and the second otherwise, which is
// the case of a group that has never been seen at all.
//
// The two differ in what they measure, and the difference is worth stating
// because it decides what a cross-read will show. The backend's last-seen
// checkpoint holds the data point's own timestamp, not the round that saw it,
// which is why it can also report "and the data is N periods late". Here it is
// the round, because a Slot is evaluated after its readiness and a point that
// arrived within that window is not late - the lateness the backend reports is
// the wait alarmd already did. So the two agree for data that arrives on time
// and differ by the lag for data that does not, and that difference is a
// property of the two designs rather than an error in either.
func absentPeriods(memory GroupMemory, evaluationTime, period int64) int64 {
	if period <= 0 {
		return 0
	}
	if memory.LastSeen > 0 {
		if since := (evaluationTime - memory.LastSeen) / period; since > 0 {
			return since
		}
	}
	if memory.FirstAbsent > 0 {
		return (evaluationTime-memory.FirstAbsent)/period + 1
	}
	return 1
}
