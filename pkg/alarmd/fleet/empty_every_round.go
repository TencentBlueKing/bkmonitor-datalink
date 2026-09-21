// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "time"

// EmptyEveryRoundFacts is what a row of KindEmptyEveryRound says for itself:
// how many rounds have completed empty in a row, since when, at what period,
// that this process has never seen the object return records, and what it can
// say about why -- which for now is nothing, said in so many words.
//
// The rows this exists for were five strategies aggregating at fifteen seconds
// over a source that reports every thirty: every window is empty whatever the
// data does, the rounds complete, the completion is a healthy one, and the
// page read HEALTHY for a day while a PromQL query on the side found them.
// The tracker sees the completions and not the Plan, so it cannot read the
// aggregation window or the source's own period; it names the gap rather
// than guessing across it.
type EmptyEveryRoundFacts struct {
	// Rounds is how many consecutive rounds completed with no records, and
	// Since when that run began. Rounds is a count of completions, not of
	// periods: a period the object was not due for counts nothing.
	Rounds int       `json:"rounds"`
	Since  time.Time `json:"since"`
	// IntervalSeconds is the object's evaluation period from the due index,
	// copied onto the row by the publisher; zero when the index has no entry.
	// It is the number a reader compares the source's reporting period
	// against, which is the first thing to check.
	IntervalSeconds int64 `json:"interval_seconds,omitempty"`
	// NeverSawData is always true on this row and is written out so a reader
	// of the JSON does not have to know that from the kind: the row's whole
	// claim is that records were never seen, and it is the one fact that
	// separates it from a NO_DATA row.
	NeverSawData bool `json:"never_saw_data"`
	// Cause is one of EmptyEveryRoundCauses. Only CAUSE_UNKNOWN is produced:
	// the two explanations a reader should check -- the source has no data,
	// or the source reports less often than the aggregation period -- need
	// the Plan's window and the source's resolution, and the tracker has
	// neither. A word for the second (an aggregation below the source's
	// resolution) is deliberately not in the list until something can read
	// the resolution and produce it; a word nothing produces would read as a
	// mechanism that is wired.
	Cause string `json:"cause"`
}

// EmptyEveryRoundCauseUnknown is the one cause this build produces: the
// evidence at hand does not decide between the candidates.
const EmptyEveryRoundCauseUnknown = "CAUSE_UNKNOWN"

// EmptyEveryRoundCauses is the closed list of causes a row may carry, for the
// page's wording table.
var EmptyEveryRoundCauses = []string{EmptyEveryRoundCauseUnknown}

// countEmptyEveryRound is the distinct objects of KindEmptyEveryRound in the
// no-data column: the first screen's one number for this line.
func countEmptyEveryRound(rows []Anomaly) int {
	seen := map[string]struct{}{}
	for _, row := range rows {
		if row.Kind == KindEmptyEveryRound {
			seen[row.QueryGroup] = struct{}{}
		}
	}
	return len(seen)
}
