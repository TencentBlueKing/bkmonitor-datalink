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

// MetricRows is what the verdict scrape counts from rows, counted on the
// replica that holds them so the scrape reads summaries and not snapshots.
// Every count adds across replicas and a moment keeps the earliest, as the
// rest of a part does. Kinds and failure categories are kept as the rows
// carry them: the scrape bounds them to its closed labels, as it bounded
// them when it read the rows.
type MetricRows struct {
	// Kinds is the anomaly column's rows by kind.
	Kinds map[string]KindRows `json:"kinds,omitempty"`
	// Stalled is the anomaly column's rows marked stalled.
	Stalled int `json:"stalled,omitempty"`
	// Failures is the anomaly column's rows by failure category, the rows
	// whose failure has no category left out.
	Failures map[string]int `json:"failures,omitempty"`
	// Cooldown is the anomaly and demoted columns' rows holding a query
	// cooldown.
	Cooldown int `json:"cooldown,omitempty"`
	// Checks is the rows' half of each check line's current count
	// (checkTally.current); the line's count adds the view's half (CheckLines).
	Checks map[Check]int `json:"checks,omitempty"`
	// Losses and GraceUnknown are LossCensus of the replica's records.
	Losses       map[Loss]int `json:"losses,omitempty"`
	GraceUnknown int          `json:"grace_unknown,omitempty"`
}

// KindRows is one kind's rows: how many, and the earliest Since among them,
// which the scrape reads its oldest age from.
type KindRows struct {
	Count int       `json:"count"`
	Since time.Time `json:"since"`
}

// metricRowsOf counts a view's rows for the scrape. checks is the rows' half
// of the check lines the part already folded (checkRowsOf), and the loss
// census is the view's records at now.
func metricRowsOf(view *View, checks checkTallies, census map[Loss]int, graceUnknown int) *MetricRows {
	rows := &MetricRows{GraceUnknown: graceUnknown}
	for index := range view.Anomalies {
		anomaly := &view.Anomalies[index]
		if anomaly.QueryCooldown != nil {
			rows.Cooldown++
		}
		if anomaly.Stalled {
			rows.Stalled++
		}
		if rows.Kinds == nil {
			rows.Kinds = map[string]KindRows{}
		}
		kind, seen := rows.Kinds[anomaly.Kind]
		if !seen || anomaly.Since.Before(kind.Since) {
			kind.Since = anomaly.Since
		}
		kind.Count++
		rows.Kinds[anomaly.Kind] = kind
		if anomaly.Failure != nil && anomaly.Failure.Category != "" {
			if rows.Failures == nil {
				rows.Failures = map[string]int{}
			}
			rows.Failures[anomaly.Failure.Category]++
		}
	}
	for index := range view.Demoted {
		if view.Demoted[index].QueryCooldown != nil {
			rows.Cooldown++
		}
	}
	for check, tally := range checks {
		if tally.current > 0 {
			if rows.Checks == nil {
				rows.Checks = map[Check]int{}
			}
			rows.Checks[check] = tally.current
		}
	}
	for loss, count := range census {
		if count > 0 {
			if rows.Losses == nil {
				rows.Losses = map[Loss]int{}
			}
			rows.Losses[loss] = count
		}
	}
	return rows
}

// mergeMetricRows adds one replica's rows into another's.
func mergeMetricRows(into, from *MetricRows) {
	for name, kind := range from.Kinds {
		if into.Kinds == nil {
			into.Kinds = map[string]KindRows{}
		}
		merged, seen := into.Kinds[name]
		if !seen || kind.Since.Before(merged.Since) {
			merged.Since = kind.Since
		}
		merged.Count += kind.Count
		into.Kinds[name] = merged
	}
	into.Stalled += from.Stalled
	into.Failures = addCounts(into.Failures, from.Failures)
	into.Cooldown += from.Cooldown
	into.Checks = addCounts(into.Checks, from.Checks)
	into.Losses = addCounts(into.Losses, from.Losses)
	into.GraceUnknown += from.GraceUnknown
}

func addCounts[K comparable](into, from map[K]int) map[K]int {
	for key, count := range from {
		if into == nil {
			into = map[K]int{}
		}
		into[key] += count
	}
	return into
}

// CheckLines is every check line's count, the whole closed table, as the
// first screen's lines print it (LineCount): the rows' half from the part's
// metric rows and what the replicas' facts on view add. An object line's
// count is its current objects, which is all the rows' half needs to carry;
// a standing's is the view's alone.
func CheckLines(view *View, part ReplicaPart, now time.Time) map[Check]int {
	rows := checkTallies{}
	if part.Metrics != nil {
		for check, current := range part.Metrics.Checks {
			rows.ensure(check).current = current
		}
	}
	lines := make(map[Check]int, len(checkOrder))
	for _, check := range checkOrder {
		lines[check] = 0
	}
	for _, report := range reportChecksFrom(rows, part.Truncated, view, now) {
		lines[report.Code] = report.LineCount()
	}
	return lines
}

// HandoverObjects is how many objects more than one replica holds, and
// whether that is known: it is when every counted replica's owned set was
// read whole. Unknown is not none -- nothing was compared, or some set was
// cut or unread -- and the counts a view reads from parts may count each of
// these objects once per holder (ReplicaPart).
func HandoverObjects(view *View) (int, bool) {
	if view == nil || view.Coverage == nil || !view.Coverage.setsWhole {
		return 0, false
	}
	return view.Coverage.HeldBySeveralTotal, true
}
