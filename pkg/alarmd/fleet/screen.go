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

// Screen is the first screen, decided from one read of the view: the four
// columns the checks are drawn from, which of them are samples, the check
// lines and the arithmetic under them.
//
// The page and the metric collector both build it, through the same two
// calls, so a line the page shows is the line the metric exports. They used
// to be two paths: the page marked stalling on every column and reported the
// checks, the collector marked it on the anomaly list alone and reported no
// checks at all -- and so a deployment executing a stale publication had a
// first line on the page and nothing on any metric.
type Screen struct {
	Columns   [][]Anomaly
	Truncated map[string]bool
	Checks    []CheckReport
	Todo      Todo
}

// Decide marks stalling on every column and settles the verdict with that
// known. Stalling can only move an object towards ours, so a verdict decided
// before the marking would call a deployment with nothing but stuck objects
// healthy. Marked on every column rather than only the one a reader asked
// for: an object that has stopped ending rounds is stuck whether or not the
// request happens to be about its column.
func Decide(view *View, now time.Time, stallAfter time.Duration) {
	for _, list := range viewColumns(view) {
		MarkStalled(list, now, stallAfter)
	}
	Settle(view)
}

// Report draws the check lines and their arithmetic from the view as it is.
// A replica publishes at most what fits its byte budget, so on a bad enough
// deployment a column is already a sample; a check is drawn from all four and
// is a sample if any of them is, which is why the columns' truncation is
// decided here, once, beside the lines.
func Report(view *View, now time.Time) Screen {
	columns := viewColumns(view)
	truncated := map[string]bool{
		ColumnAnomalies:   view.AnomaliesTotal > len(view.Anomalies),
		ColumnDemoted:     view.DemotedTotal > len(view.Demoted),
		ColumnUndecidable: view.UndecidableTotal > len(view.Undecidable),
		ColumnByDesign:    view.ByDesignTotal > len(view.ByDesign),
	}
	checks := ReportChecks(columns, truncated, view, now)
	return Screen{Columns: columns, Truncated: truncated, Checks: checks,
		Todo: SummarizeTodo(checks, columns, view, now)}
}

// viewColumns is the four lists a check is drawn from, in the order
// columnNames indexes them.
func viewColumns(view *View) [][]Anomaly {
	return [][]Anomaly{view.Anomalies, view.Demoted, view.Undecidable, view.ByDesign}
}

// LineCount is the number the line prints, and the one its metric carries:
// objects currently under an object check, replicas under one of the
// standings, which have no objects. Zero is a line that is down. Records of
// past loss kept under DETECTION_ABANDONED and TIMELINE_PRUNED are not
// current and do not count -- a line held up by an hour-old record would
// read as work to do now.
func (report CheckReport) LineCount() int {
	if !report.Code.Standing() {
		return report.Current
	}
	replicas := map[string]struct{}{}
	for _, group := range report.Groups {
		for _, replica := range group.Replicas {
			replicas[replica] = struct{}{}
		}
	}
	return len(replicas)
}
