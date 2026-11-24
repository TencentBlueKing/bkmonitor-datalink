// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package newopens counts, per strategy, the alerts this process opened in
// each clock hour and how many distinct values each dimension took in them,
// so a strategy that opens an alert for each value of a dimension with no
// bound (a request name, an ID) is named with that dimension. It neither
// limits nor closes anything: the ruling is that alarmd keeps no
// per-strategy rate guard; the strategy's owner
// is told.
//
// The threshold is per strategy, and a strategy's opens may be split across
// replicas: its Plans sit in as many Query Groups as it has queries, each
// group owned by one replica, and a group moves to another replica when
// ownership is rebalanced. So each replica reports its own count for an
// hour once it passes ReportFloor, and the fleet adds the reports of one
// strategy and hour up before comparing the sum with Threshold
// (fleet.KindNewAlertsBurst).
//
// An open is an ABNORMAL the open-alert copy says is its episode's first
// (activecopy.Copy.Acknowledged). Plans publishing the python-compatible
// protocol carry no such signal - every gate that decides an episode skips
// them - and are not counted; nor are deployments without the link, whose
// copy decides nothing.
//
// Memory: about 30 B per distinct value held (a map[uint64]struct{} entry,
// measured), for the hour counting only - the hour before keeps its summary.
// A strategy (one dimension at about 14,000 values a hour, a few more at a
// handful) holds about 0.5 MB; one strategy at every bound (MaxDimensions
// dimensions at MaxDistinct values) about 19 MB. Nothing is in Redis: a
// restart counts from zero.
package newopens

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Threshold is the number of alerts a strategy opens in one clock hour,
// across every replica, past which it is named. Measured per
// strategy and hour: on one deployment over 24 hours, a strategy
// grouped by span_name (19,522 values) peaked at 14,249 and every other
// strategy at 182 or below; on another deployment, over its five
// largest businesses (about 55% of its strategies) for 24 hours, a no-data storm
// across every task of a heartbeat strategy peaked at about 1,053 (a lower
// bound: the alert listing caps each read), the next at 248 and the rest at
// 112 or below. 5,000 is about five times the highest peak of a strategy
// working as meant and about a third of the one that was not.
const Threshold = 5000

// ReportFloor is the count past which a replica reports its share of a
// strategy's hour: an eighth of the threshold. It guarantees the largest
// share of a strategy past the threshold is reported (with n replicas at
// least one share is past threshold/n, and n <= 8 puts that above the
// floor), not every share: each replica at or under the floor hides at most
// ReportFloor opens from the sum. A strategy whose total is within that
// hidden part above the threshold can go unnamed - 4,800 here and 600 there
// is 5,400 in all, and only the 4,800 is counted. The strategies working as
// meant (at most about 1,053 an hour) are reported only in a storm.
const ReportFloor = Threshold / 8

// Window is one count: a clock hour, so the replicas' counts of a strategy
// are of the same hour and add up.
const Window = time.Hour

// MaxDimensions is how many of a strategy's dimension names one window
// counts values of; a name past it is counted in DimensionsDropped.
const MaxDimensions = 32

// MaxDistinct is how many values of one dimension one window tells apart.
// Past it the dimension reads "at least MaxDistinct" (DistinctCapped): four
// times the threshold, so a dimension that explodes still reads as the one
// that does.
const MaxDistinct = 4 * Threshold

// MaxStrategies is how many strategies are counted at once; an open of one
// past it is not counted (OutcomeUntracked) until Run lets quiet ones go.
const MaxStrategies = 100_000

// sweepEvery is how often Run lets go the strategies with nothing to say.
const sweepEvery = time.Minute

// The outcomes of an open, closed: counted, or untracked (its strategy past
// MaxStrategies).
const (
	OutcomeCounted   = "counted"
	OutcomeUntracked = "untracked"
)

// Outcomes is the closed list.
var Outcomes = []string{OutcomeCounted, OutcomeUntracked}

// Key is one strategy.
type Key struct {
	TenantID   string
	StrategyID string
}

// Hour is one clock hour's count: how many alerts the strategy opened in
// it here, and its dimension with the most distinct values.
type Hour struct {
	Start          time.Time
	Opened         int
	Dimension      string
	Distinct       int
	DistinctCapped bool
	// DimensionsDropped is how many dimension names past MaxDimensions
	// were not counted.
	DimensionsDropped int
}

// Report is one strategy's hours past ReportFloor here: the hour counting
// and the one before, each while past it. Empty is the report that says
// none is any more, sent once after one that was not.
type Report struct {
	Hours []Hour
}

type window struct {
	start             time.Time
	opened            int
	values            map[string]map[uint64]struct{}
	dimensionsDropped int
}

type state struct {
	current window
	// last is the hour before current's summary, kept while it was past
	// the floor: the hour is named for the hour after it closes.
	last *Hour
	said bool
}

// Counter counts opens per strategy. Safe for concurrent use.
type Counter struct {
	mu     sync.Mutex
	states map[Key]*state
	now    func() time.Time
	// maxStrategies is MaxStrategies; smaller in tests.
	maxStrategies int
	counted       atomic.Uint64
	untracked     atomic.Uint64
}

// New is a Counter reading the time from now (time.Now when nil).
func New(now func() time.Time) *Counter {
	if now == nil {
		now = time.Now
	}
	return &Counter{states: map[Key]*state{}, now: now, maxStrategies: MaxStrategies}
}

// Stats is the opens by outcome, every outcome present.
func (c *Counter) Stats() map[string]uint64 {
	if c == nil {
		return map[string]uint64{OutcomeCounted: 0, OutcomeUntracked: 0}
	}
	return map[string]uint64{OutcomeCounted: c.counted.Load(), OutcomeUntracked: c.untracked.Load()}
}

// Run lets go, once a minute, the strategies with nothing to say, so a
// strategy that went quiet or was deleted is not held for the life of the
// process. Off the ACK path: Note never walks the strategies.
func (c *Counter) Run(ctx context.Context) {
	if c == nil {
		return
	}
	ticker := time.NewTicker(sweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sweep(c.now())
		}
	}
}

// Note counts the opens among changed: the events the open-alert copy says
// changed, of which an ABNORMAL is an episode's first.
func (c *Counter) Note(changed []contract.TriggerEventV1) {
	if c == nil || len(changed) == 0 {
		return
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range changed {
		event := &changed[i]
		if event.EventKind != contract.TriggerEventAbnormal || event.DedupeMD5 == "" || event.LegacyOutput != nil {
			continue
		}
		key := Key{TenantID: event.TenantID, StrategyID: event.PlanRef.StrategyID}
		s := c.states[key]
		if s == nil {
			if len(c.states) >= c.maxStrategies {
				c.untracked.Add(1)
				continue
			}
			s = &state{current: newWindow(now.Truncate(Window))}
			c.states[key] = s
		}
		s.roll(now)
		s.current.note(event.RecordRef.Dimensions)
		c.counted.Add(1)
	}
}

// Report is the strategy's hours past the floor, and whether to send it:
// while any is, and once more when none is.
func (c *Counter) Report(key Key) (Report, bool) {
	if c == nil {
		return Report{}, false
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.states[key]
	if s == nil {
		return Report{}, false
	}
	s.roll(now)
	var report Report
	if s.last != nil {
		report.Hours = append(report.Hours, *s.last)
	}
	if s.current.opened > ReportFloor {
		report.Hours = append(report.Hours, s.current.summary())
	}
	if len(report.Hours) == 0 {
		if !s.said {
			return Report{}, false
		}
		s.said = false
		return report, true
	}
	s.said = true
	return report, true
}

func newWindow(start time.Time) window {
	return window{start: start, values: map[string]map[uint64]struct{}{}}
}

// roll starts the hour now is in once the current one has ended, keeping
// the ended one's summary as last when it was past the floor and is the
// hour just before, and lets last go an hour after it closed.
func (s *state) roll(now time.Time) {
	hour := now.Truncate(Window)
	if s.last != nil && !hour.Before(s.last.Start.Add(2*Window)) {
		s.last = nil
	}
	if !s.current.start.Before(hour) {
		return
	}
	if s.current.opened > ReportFloor && s.current.start.Add(Window).Equal(hour) {
		summary := s.current.summary()
		s.last = &summary
	}
	s.current = newWindow(hour)
}

func (w *window) note(dimensions map[string]json.RawMessage) {
	w.opened++
	for name, value := range dimensions {
		values, ok := w.values[name]
		if !ok {
			if len(w.values) >= MaxDimensions {
				w.dimensionsDropped++
				continue
			}
			values = map[uint64]struct{}{}
			w.values[name] = values
		}
		if len(values) >= MaxDistinct {
			continue
		}
		hash := fnv.New64a()
		_, _ = hash.Write(value)
		values[hash.Sum64()] = struct{}{}
	}
}

// summary is the window's count and its dimension with the most distinct
// values, the first by name among equals.
func (w *window) summary() Hour {
	names := make([]string, 0, len(w.values))
	for name := range w.values {
		names = append(names, name)
	}
	sort.Strings(names)
	hour := Hour{Start: w.start, Opened: w.opened, DimensionsDropped: w.dimensionsDropped}
	for _, name := range names {
		if n := len(w.values[name]); n > hour.Distinct {
			hour.Dimension, hour.Distinct = name, n
		}
	}
	hour.DistinctCapped = hour.Distinct >= MaxDistinct
	return hour
}

// sweep lets go the strategies with nothing to say: no open this hour or
// the one before, and no hour kept. One that had said something stops
// writing; the fleet lets its row go when no write comes.
func (c *Counter) sweep(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, s := range c.states {
		s.roll(now)
		if s.last == nil && s.current.opened == 0 {
			delete(c.states, key)
		}
	}
}

// Tracked is how many strategies are counted now.
func (c *Counter) Tracked() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.states)
}
