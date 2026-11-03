// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package readhold

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A lowering waits until the data has stayed on time for as long as the
// longest gap seen between two of its read-early arrivals: a source late
// every few hours is not lowered in the hours between. Which gaps count is
// bounded by the record's own life, RecordTTL: one long quiet followed by a
// late arrival is a gap that, kept forever, would hold every later lowering
// to it however the data arrives since. So the gaps are kept as the longest
// that ended on each UTC day, the days within RecordTTL, and the bar is the
// longest of those - a source still late every few hours keeps its bar, one
// whose long gap is past the window is judged by the gaps it has now. A
// source late about once a week or less often is judged so too: its gap
// leaves the window at day 7, and the bar until its next late arrival is the
// gaps since, or SingleEventQuiet.

const dayMillis = int64(24 * time.Hour / time.Millisecond)

// earlyGapDays is how many UTC days of gaps are kept: RecordTTL in days.
const earlyGapDays = int(RecordTTL / (24 * time.Hour))

// utcDay is the UTC day a time in Unix milliseconds falls on.
func utcDay(millis int64) int64 {
	return millis / dayMillis
}

// earlyGapsOf is the record's gaps, with the all-time longest gap a build
// before EarlyGaps kept read in as a gap that ended on the day of the last
// read-early arrival: no later than that is when it ended, so it ages out no
// sooner than the gap it was.
func earlyGapsOf(record Record) []EarlyGap {
	if len(record.EarlyGaps) > 0 || record.MaxEarlyIntervalMillis <= 0 || record.LastEarlyMillis <= 0 {
		return append([]EarlyGap(nil), record.EarlyGaps...)
	}
	return []EarlyGap{{Day: utcDay(record.LastEarlyMillis), Millis: record.MaxEarlyIntervalMillis}}
}

// withEarlyGap is gaps with one more gap, ended at endedAt: the longest of
// its day, and only the days within earlyGapDays of that day kept.
func withEarlyGap(gaps []EarlyGap, endedAt, gap int64) []EarlyGap {
	day := utcDay(endedAt)
	kept := make([]EarlyGap, 0, len(gaps)+1)
	found := false
	for _, entry := range gaps {
		if entry.Day <= day-int64(earlyGapDays) {
			continue
		}
		if entry.Day == day {
			entry.Millis, found = max(entry.Millis, gap), true
		}
		kept = append(kept, entry)
	}
	if !found {
		kept = append(kept, EarlyGap{Day: day, Millis: gap})
	}
	return kept
}

// longestGap is the longest of gaps, zero for none: what a record keeps as
// MaxEarlyIntervalMillis for a build that reads that field alone.
func longestGap(gaps []EarlyGap) int64 {
	longest := int64(0)
	for _, entry := range gaps {
		longest = max(longest, entry.Millis)
	}
	return longest
}

// quietBar is how long a group must stay without a read-early arrival before
// matching earlier reads lower its hold, at now: the longest gap that ended
// within earlyGapDays of the group's day, or SingleEventQuiet when none did.
// The group's day turns groupDayOffset after the UTC day does: a gap ages out
// of the window then, not at 00:00 UTC for every group at once, so the groups
// whose long gap ended on the same day are not quiet for their bar together
// and their trials' candidates are not verified in the same hour.
func quietBar(qg execution.QueryGroupIdentity, record Record, now int64) int64 {
	today, bar := utcDay(now-groupDayOffset(qg).Milliseconds()), int64(0)
	for _, entry := range earlyGapsOf(record) {
		if entry.Day > today-int64(earlyGapDays) {
			bar = max(bar, entry.Millis)
		}
	}
	if bar == 0 {
		return SingleEventQuiet.Milliseconds()
	}
	return bar
}
