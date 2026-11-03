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
	"fmt"
	"time"
)

// WindowClearing is when a short window's holes have all slid out of it:
// the first record time at which the window is full again, provided no new
// hole opens meanwhile. That is all it says. A full window is not a trusted
// result: the per-series guard converges one round later (it asks for the
// stored history to form a full window at the last processed record, which
// is the round the window filled), a Plan-level gap guard counts on its own,
// and windows counted but not listed are outside the computation.
//
// A window is Required positions one evaluation interval apart, ending at
// End; it is full when every position holds a usable point. A hole at t
// leaves the window at the first record time past t + (Required-1) *
// interval, which is t + Required * interval. The last hole to leave is
// the newest, so the window clears at newest + Required * interval.
//
// The holes are listed oldest first and bounded, so the newest is known
// only when every hole is listed. Otherwise the answer is a range: at the
// earliest the holes are packed from the window's start, at the latest the
// newest is End itself. Exact says which of the two this is.
type WindowClearing struct {
	Key      string    `json:"key"`
	Holes    uint32    `json:"holes,omitempty"`
	At       time.Time `json:"at,omitempty"`
	Latest   time.Time `json:"latest,omitempty"`
	Exact    bool      `json:"exact"`
	Interval int64     `json:"interval_seconds,omitempty"`
	// Unnamed is how many short windows the object counted and did not
	// list; the clearing does not cover them.
	Unnamed uint32 `json:"unnamed_windows,omitempty"`
	// PlanGuards is how many Plan-level gap guards the object carries; they
	// release on their own count, not on this window.
	PlanGuards int `json:"plan_guards,omitempty"`
	// Refused is why no time could be computed, from ClearingRefusals; the
	// times are then absent.
	Refused string `json:"refused,omitempty"`
	// Line is the above in plain words, times in UTC.
	Line string `json:"line"`
}

// The reasons a clearing is not computed, closed. A window that is not short
// has nothing to clear and is skipped without one.
const (
	ClearingIntervalUnknown = "INTERVAL_UNKNOWN"
	ClearingStepMismatch    = "STEP_NOT_THE_OBJECT_INTERVAL"
	ClearingCountsDisagree  = "HOLE_COUNTS_DISAGREE"
	clearingNotShort        = ""
)

var clearingRefusalWords = map[string]string{
	ClearingIntervalUnknown: "对象的评估周期未知",
	ClearingStepMismatch:    "洞不在对象评估周期的网格上，这条窗口所属 Plan 的步长与对象周期不一致",
	ClearingCountsDisagree:  "洞数与缺口对不上",
}

// ClearingOf is the window's clearing time under the object's evaluation
// interval. ok is false with a reason when it cannot be computed, and false
// with none when the window is not short.
func ClearingOf(window WindowRow, interval time.Duration) (clearing WindowClearing, refused string, ok bool) {
	clearing.Key = window.Key
	if window.Required == 0 || window.Valid >= window.Required || window.End.IsZero() {
		return clearing, clearingNotShort, false
	}
	if interval <= 0 {
		return clearing, ClearingIntervalUnknown, false
	}
	holes := window.MissingTotal + window.UnusableTotal
	if holes != window.Required-window.Valid || uint32(len(window.Holes)) > holes {
		return clearing, ClearingCountsDisagree, false
	}
	// The interval is the object's, from the due index; the window's step is
	// its Plan's. Nothing makes every Plan of one object share a period --
	// they share one today because Query Groups are formed on queries aligned
	// by the same interval, which is implicit -- so a listed hole off the grid
	// the interval draws back from End is refused rather than computed with
	// the wrong step.
	for _, hole := range window.Holes {
		if distance := window.End.Sub(hole.At); distance < 0 || distance%interval != 0 {
			return clearing, ClearingStepMismatch, false
		}
	}
	span := time.Duration(window.Required) * interval
	clearing.Holes, clearing.Interval = holes, int64(interval/time.Second)
	if uint32(len(window.Holes)) == holes {
		clearing.At, clearing.Exact = window.Holes[len(window.Holes)-1].At.Add(span), true
		return clearing, "", true
	}
	// Some holes unlisted. The newest is no earlier than the newest listed
	// one, and no earlier than the window's start plus holes-1 positions --
	// that many holes cannot all sit before it; no later than End. Missing
	// and unusable positions are listed under separate bounds, so the
	// unlisted ones are only known to be newer than the listed ones of
	// their own kind, and packing them after the newest listed hole of
	// either kind could overstate the earliest time.
	start := window.End.Add(-time.Duration(window.Required-1) * interval)
	earliest := start.Add(time.Duration(holes-1) * interval)
	if n := len(window.Holes); n > 0 && window.Holes[n-1].At.After(earliest) {
		earliest = window.Holes[n-1].At
	}
	if earliest.After(window.End) {
		earliest = window.End
	}
	clearing.At, clearing.Latest = earliest.Add(span), window.End.Add(span)
	return clearing, "", true
}

// LastClearing is the latest clearing over the listed windows, since the
// object's windows are all full only once the last of them is. When none
// could be computed and one was refused, the first refusal is the answer,
// so the reader is told why there is no time rather than given none.
// Windows beyond the listed ones are counted in short and not named;
// Unnamed says how many.
func LastClearing(coverage *HistoryCoverage, interval time.Duration) (last WindowClearing, ok bool) {
	if coverage == nil {
		return WindowClearing{}, false
	}
	var refusal WindowClearing
	for _, window := range coverage.Windows {
		clearing, refused, computed := ClearingOf(window, interval)
		if !computed {
			if refused != clearingNotShort && refusal.Refused == "" {
				refusal, refusal.Refused = clearing, refused
			}
			continue
		}
		if !ok || clearing.bound().After(last.bound()) {
			last, ok = clearing, true
		}
	}
	if !ok {
		if refusal.Refused == "" {
			return WindowClearing{}, false
		}
		last = refusal
	}
	if int(coverage.Short) > len(coverage.Windows) {
		last.Unnamed = coverage.Short - uint32(len(coverage.Windows))
	}
	return last, true
}

// bound is the time the clearing is certain by.
func (clearing WindowClearing) bound() time.Time {
	if clearing.Exact {
		return clearing.At
	}
	return clearing.Latest
}

// text says only what the computation proves: when the window is full, and
// that the per-series guard converges the round after. It never says the
// result is trusted.
func (clearing WindowClearing) text() string {
	const layout = "2006-01-02 15:04Z"
	var line string
	switch {
	case clearing.Refused != "":
		line = fmt.Sprintf("窗口 %s 算不出何时满：%s", clearing.Key, clearingRefusalWords[clearing.Refused])
	case clearing.Exact:
		line = fmt.Sprintf("窗口 %s 的 %d 个洞在 %s 全部滑出，这个窗口届时满；逐序列的守卫在其后一轮（%s）收敛（前提是其间不再出新洞）",
			clearing.Key, clearing.Holes, clearing.At.UTC().Format(layout),
			clearing.At.Add(time.Duration(clearing.Interval)*time.Second).UTC().Format(layout))
	default:
		line = fmt.Sprintf("窗口 %s 的 %d 个洞最早 %s、最迟 %s 全部滑出（未全部列出，只能给区间），这个窗口届时满；逐序列的守卫在其后一轮收敛（前提是其间不再出新洞）",
			clearing.Key, clearing.Holes, clearing.At.UTC().Format(layout), clearing.Latest.UTC().Format(layout))
	}
	if clearing.Unnamed > 0 {
		line += fmt.Sprintf("；另有 %d 个未满窗口没有列出，不在此推算内", clearing.Unnamed)
	}
	if clearing.PlanGuards > 0 {
		line += fmt.Sprintf("；另有 %d 个 Plan 级守卫，另行计数", clearing.PlanGuards)
	}
	return line
}
