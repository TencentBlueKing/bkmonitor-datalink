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
	"cmp"
	"context"
	"slices"
	"sync"
	"time"
)

const (
	// warmingPeakGroups bounds the groups one minute counts; past it the
	// group with the fewest writes gives its place up and its count, as
	// overcount, to the newcomer (a space-saving count). A burst is a few
	// groups writing far more than the rest, which this always keeps.
	warmingPeakGroups = 256
	// warmingPeakTop is how many of the peak minute's groups a line names.
	warmingPeakTop = 8
	// minutesPerHour is what a median minute is taken over, the quiet
	// minutes included.
	minutesPerHour = 60
)

// WarmingGroup is one Query Group's writes to series still warming their
// history in the peak minute. Overcount is how many of them may be another
// group's, one that held the place before this one took it; zero when the
// minute never ran out of places.
//
// A group and not a strategy: the reuse facts are counted once per Slot
// apply, over all of the Slot's Plans, so which strategy's series were
// warming is not known here. The group's strategies are a strategy.list or
// object.get away.
type WarmingGroup struct {
	QueryGroup string `json:"query_group"`
	Writes     int64  `json:"writes"`
	Overcount  int64  `json:"overcount,omitempty"`
}

// WarmingPeakFacts is one hour of this replica's state writes to series still
// warming their history (StateWriteReuseStoredWarming): the total, the
// minutes that had any, the median minute over all sixty, and the busiest
// minute with the Query Groups that made it. state_write_reuse_total says a burst
// happened; this says whose it was.
type WarmingPeakFacts struct {
	HourStart          time.Time      `json:"hour_start"`
	Writes             int64          `json:"writes"`
	MinutesWithWrites  int            `json:"minutes_with_writes"`
	MedianMinuteWrites int64          `json:"median_minute_writes"`
	PeakMinute         time.Time      `json:"peak_minute"`
	PeakMinuteWrites   int64          `json:"peak_minute_writes"`
	PeakGroups         []WarmingGroup `json:"peak_groups"`
}

type warmingCount struct{ writes, overcount int64 }

// WarmingPeak writes one line an hour naming the groups behind the hour's
// busiest minute of warming writes. It keeps no history past the hour and
// writes nothing for an hour without any. Its clock moves on the state
// applies it observes, which run every round on a replica that detects, so
// the line for an hour is written by the first apply after it.
type WarmingPeak struct {
	now  func() time.Time
	emit Observer

	mu          sync.Mutex
	minute      time.Time
	hour        time.Time
	counts      map[string]*warmingCount
	minuteTotal int64
	hourTotal   int64
	minutes     []int64
	peak        WarmingPeakFacts
}

func NewWarmingPeak(now func() time.Time) *WarmingPeak {
	if now == nil {
		now = time.Now
	}
	return &WarmingPeak{now: now, counts: make(map[string]*warmingCount, warmingPeakGroups)}
}

// SetEmit is where the hour's line goes: the runtime's observer, so it is
// logged as any other stage is. Set before the first observation.
func (p *WarmingPeak) SetEmit(emit Observer) { p.emit = emit }

func (p *WarmingPeak) Observe(ctx context.Context, o Observation) {
	if p == nil || o.StateWriteReuse == nil {
		return
	}
	var warming int64
	for key, count := range o.StateWriteReuse.Counts {
		if key.Stored == StateWriteReuseStoredWarming {
			warming += count
		}
	}
	var queryGroup string
	if warming > 0 {
		queryGroup = mergeTraceFields(o.Trace, TraceFieldsFromContext(ctx)).QueryGroupKey
	}
	p.mu.Lock()
	line := p.advance(p.now().UTC())
	if warming > 0 {
		p.add(queryGroup, warming)
	}
	p.mu.Unlock()
	if line != nil && p.emit != nil {
		p.emit.Observe(ctx, Observation{Component: ComponentState, Stage: StageWarmingPeak,
			Result: ResultSuccess, Direction: DirectionInternal, ReasonCode: ReasonNone, WarmingPeak: line})
	}
}

// advance closes the minute and the hour now has left, and returns the
// hour's line when one ended with writes in it.
func (p *WarmingPeak) advance(now time.Time) *WarmingPeakFacts {
	minute, hour := now.Truncate(time.Minute), now.Truncate(time.Hour)
	if p.minute.IsZero() {
		p.minute, p.hour = minute, hour
		return nil
	}
	if minute.After(p.minute) {
		p.closeMinute()
		p.minute = minute
	}
	if !hour.After(p.hour) {
		return nil
	}
	var line *WarmingPeakFacts
	if p.hourTotal > 0 {
		facts := p.peak
		facts.HourStart, facts.Writes, facts.MinutesWithWrites = p.hour, p.hourTotal, len(p.minutes)
		facts.MedianMinuteWrites = medianMinute(p.minutes)
		line = &facts
	}
	p.hour, p.hourTotal, p.minutes, p.peak = hour, 0, p.minutes[:0], WarmingPeakFacts{}
	return line
}

func (p *WarmingPeak) closeMinute() {
	if p.minuteTotal > 0 {
		p.hourTotal += p.minuteTotal
		if len(p.minutes) < minutesPerHour {
			p.minutes = append(p.minutes, p.minuteTotal)
		}
		if p.minuteTotal > p.peak.PeakMinuteWrites {
			p.peak.PeakMinute, p.peak.PeakMinuteWrites, p.peak.PeakGroups = p.minute, p.minuteTotal, p.top()
		}
	}
	clear(p.counts)
	p.minuteTotal = 0
}

func (p *WarmingPeak) add(key string, writes int64) {
	p.minuteTotal += writes
	if count, found := p.counts[key]; found {
		count.writes += writes
		return
	}
	if len(p.counts) < warmingPeakGroups {
		p.counts[key] = &warmingCount{writes: writes}
		return
	}
	var smallest string
	var least *warmingCount
	for candidate, count := range p.counts {
		if least == nil || count.writes < least.writes {
			smallest, least = candidate, count
		}
	}
	delete(p.counts, smallest)
	p.counts[key] = &warmingCount{writes: least.writes + writes, overcount: least.writes}
}

func (p *WarmingPeak) top() []WarmingGroup {
	groups := make([]WarmingGroup, 0, len(p.counts))
	for key, count := range p.counts {
		groups = append(groups, WarmingGroup{QueryGroup: key, Writes: count.writes, Overcount: count.overcount})
	}
	slices.SortFunc(groups, func(a, b WarmingGroup) int {
		if order := cmp.Compare(b.Writes, a.Writes); order != 0 {
			return order
		}
		return cmp.Compare(a.QueryGroup, b.QueryGroup)
	})
	return groups[:min(len(groups), warmingPeakTop)]
}

// medianMinute is the median of an hour's sixty minutes, of which only the
// ones with writes are listed: the others are zero.
func medianMinute(listed []int64) int64 {
	sorted := slices.Clone(listed)
	slices.Sort(sorted)
	zeros := minutesPerHour - len(sorted)
	if index := minutesPerHour / 2; index >= zeros {
		return sorted[index-zeros]
	}
	return 0
}
