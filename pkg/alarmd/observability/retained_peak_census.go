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
	"context"
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// RetainedPeakCensusMaxGroups bounds the census. It is far above any
// replica's owned count - the roster prunes the census to what is owned on
// every report - and exists so a replica that is never asked to report
// cannot grow it without limit.
const RetainedPeakCensusMaxGroups = 65536

// RetainedPeakCensus is every Query Group's retained-byte peak and compute
// wall over the last two windows, for the heartbeat that reports them to the
// Leader's byte-feasibility moves (decision-020 section 5.7).
//
// A census, not a ranking. The cost summary holds the same numbers for the
// Query Groups it tracks, but what it tracks is a roster admitted up to a
// capacity derived from the observation memory share - 327 groups on a 4 GiB
// replica at one percent - and taken in directory order, not by need. A
// replica owning 600 Query Groups reported the peaks of the 327 the summary
// happened to admit and nothing for the rest, and the Leader, which does not
// move to a Worker with an unread object, could never move anything: the
// reading source was a ranking and the decision needed a census. This
// structure keeps one small entry per Query Group the replica has completed a
// Slot for, whatever the summary admitted, and the roster prunes it to what
// is owned.
//
// The numbers are taken exactly as the summary takes them - the larger of
// the two windows, the evaluation and state wall and not the query or run
// wall - so a Query Group the summary does track reads the same on both.
type RetainedPeakCensus struct {
	mu        sync.Mutex
	window    time.Duration
	replayAge time.Duration
	now       func() time.Time
	groups    map[string]*censusGroup
	overflow  uint64
}

type censusGroup struct {
	epoch             int64
	current, previous censusWindow
	slot              censusSlot
}

// censusSlot is the retained-byte peak of the Query Group's latest Slot,
// held until its next Slot rather than for the two windows: a Query Group
// on an hourly or daily period completes one Slot per period, and read from
// the windows alone it was a reading for ten minutes of each hour and absent
// for the rest, so the Leader's byte-feasibility moves read an overloaded
// Worker as light. at is when this Slot's peak was first read, and the hold
// counts from it.
type censusSlot struct {
	evaluation int64
	peak       uint64
	interval   time.Duration
	at         time.Time
}

type censusWindow struct {
	retainedBytesPeak  uint64
	computeWallNS      int64
	computeWallUnknown uint64
}

// NewRetainedPeakCensus rotates its windows every window; the summary's
// window is what to pass, so the two agree about what "the last two
// windows" spans.
func NewRetainedPeakCensus(window time.Duration, now func() time.Time) *RetainedPeakCensus {
	if now == nil {
		now = time.Now
	}
	if window <= 0 {
		window = 5 * time.Minute
	}
	return &RetainedPeakCensus{window: window, now: now, groups: make(map[string]*censusGroup)}
}

// SetReplayAge is how long past its next Slot a Query Group's last Slot can
// still be replayed; a held peak lasts two of its periods and this beyond,
// so a Slot late by up to the replay age does not lose the reading in
// between.
func (c *RetainedPeakCensus) SetReplayAge(age time.Duration) {
	if c == nil || age < 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.replayAge = age
}

// holdFor is how long a held Slot peak stays a reading: two of its period
// and the replay age, or the two windows while its period is not known.
func (c *RetainedPeakCensus) holdFor(s censusSlot) time.Duration {
	if s.interval <= 0 {
		return 2 * c.window
	}
	return 2*s.interval + c.replayAge
}

// heldPeak is the held Slot peak when it is still a reading at now.
func (c *RetainedPeakCensus) heldPeak(s censusSlot, now time.Time) uint64 {
	if s.at.IsZero() || now.Sub(s.at) > c.holdFor(s) {
		return 0
	}
	return s.peak
}

// holdSlot records one retained-byte reading against the Slot it is of. A
// later Slot replaces the held one; the same Slot, or an earlier one being
// replayed, keeps the larger. A Slot's period is the distance to the Slot
// after it, from the completion when it carries it and otherwise learned
// from two Slots read in turn. A learned period is not corrected: two Slots
// that straddle a skipped round learn a multiple of it and hold the peak
// longer, which reads a Worker heavier for a while rather than lighter - the
// direction that does not send a move to it. Any completion carrying the
// Slot after it sets the period exactly.
func (c *RetainedPeakCensus) holdSlot(s *censusSlot, evaluation, following int64, peak uint64, now time.Time) {
	// A hold that has run out reads nothing more, but its Slot still says
	// where the next one is from, so the period can be learned across it.
	expired := s.at.IsZero() || c.heldPeak(*s, now) == 0
	switch {
	case expired || evaluation > s.evaluation:
		if s.evaluation > 0 && evaluation > s.evaluation && s.interval <= 0 {
			s.interval = time.Duration(evaluation-s.evaluation) * time.Second
		}
		s.evaluation, s.peak, s.at = max(s.evaluation, evaluation), peak, now
	default:
		s.peak = max(s.peak, peak)
	}
	if following > evaluation && evaluation > 0 {
		s.interval = time.Duration(following-evaluation) * time.Second
	}
}

// Observe takes what the summary's addCost takes for the peak and the
// compute wall, and nothing else: the Slot completion's retained bytes, the
// pool refusal's own-plus-requested, the evaluation and state walls.
func (c *RetainedPeakCensus) Observe(ctx context.Context, o Observation) {
	if c == nil {
		return
	}
	switch o.Stage {
	case StageSlotCompleted, StageEvaluationCompleted, StageStatePreflight, StageStateApplied:
	case StageResourceHard:
		// The two refusals of the retained-byte pool the summary counts
		// (addRetainedStop): the pool full and this object over its share.
		// A Slot refused for its own size is not a reading of the pool.
		if o.Component != ComponentResource || o.CapacityBudget != CapacityBudgetRetainedBytes ||
			(string(o.ReasonCode) != contract.ReasonResourceHardStop && string(o.ReasonCode) != contract.ReasonQGBudgetShareExceeded) {
			return
		}
	default:
		return
	}
	trace := mergeTraceFields(o.Trace, TraceFieldsFromContext(ctx))
	key := trace.QueryGroupKey
	if key == "" {
		return
	}
	now := c.now()
	epoch := now.UnixNano() / int64(c.window)
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.groups[key]
	if g == nil {
		if len(c.groups) >= RetainedPeakCensusMaxGroups {
			c.overflow++
			return
		}
		g = &censusGroup{epoch: epoch}
		c.groups[key] = g
	}
	g.rotate(epoch)
	w := &g.current
	switch o.Stage {
	case StageSlotCompleted:
		if usage := o.SlotBudgetUsage; usage != nil {
			w.retainedBytesPeak = max(w.retainedBytesPeak, usage.RetainedBytes)
			c.holdSlot(&g.slot, trace.EvaluationTime, o.FollowingSlot, usage.RetainedBytes, now)
		}
	case StageResourceHard:
		if f := o.CapacityRejection; f != nil && f.OwnUsed != nil {
			w.retainedBytesPeak = max(w.retainedBytesPeak, *f.OwnUsed+f.Requested)
			c.holdSlot(&g.slot, trace.EvaluationTime, o.FollowingSlot, *f.OwnUsed+f.Requested, now)
		}
	default:
		if o.Duration >= 0 && (o.DurationKnown || o.Duration > 0) {
			w.computeWallNS += max(0, int64(o.Duration))
		} else {
			w.computeWallUnknown++
		}
	}
}

// rotate brings the group's windows to epoch: the window just past becomes
// the previous one, anything older is gone. The summary rotates every group
// on Publish; the census rotates a group when it is observed and when it is
// read, which comes to the same thing - a group that stopped completing
// Slots ages out of its two windows on the read rather than never. An epoch
// older than the one held leaves the windows as they are: the observer and
// the reader read the clock before they take the lock, and one that waited
// behind a caller already in the next window must not clear both windows.
func (g *censusGroup) rotate(epoch int64) {
	if epoch <= g.epoch {
		return
	}
	if g.epoch+1 == epoch {
		g.previous = g.current
	} else {
		g.previous = censusWindow{}
	}
	g.current = censusWindow{}
	g.epoch = epoch
}

// RetainedPeaks is every Query Group's reading, in key order: the larger of
// the two windows' peaks and its latest Slot's held peak, and the sum of the
// windows' walls, as the summary reads them. A Query Group with neither a
// peak nor any wall is not a reading: one that stopped completing Slots for
// two of its periods and the replay age reports nothing, rather than the
// last thing it did for as long as it is owned.
func (c *RetainedPeakCensus) RetainedPeaks() []CostRetainedPeak {
	if c == nil {
		return nil
	}
	now := c.now()
	epoch := now.UnixNano() / int64(c.window)
	c.mu.Lock()
	defer c.mu.Unlock()
	peaks := make([]CostRetainedPeak, 0, len(c.groups))
	for key, g := range c.groups {
		g.rotate(epoch)
		reading := CostRetainedPeak{QueryGroupKey: key,
			RetainedBytesPeak:  max(g.current.retainedBytesPeak, g.previous.retainedBytesPeak, c.heldPeak(g.slot, now)),
			ComputeWallNS:      g.current.computeWallNS + g.previous.computeWallNS,
			ComputeWallUnknown: g.current.computeWallUnknown + g.previous.computeWallUnknown}
		if reading.RetainedBytesPeak > 0 || reading.ComputeWallNS > 0 {
			peaks = append(peaks, reading)
		}
	}
	sort.Slice(peaks, func(i, j int) bool { return peaks[i].QueryGroupKey < peaks[j].QueryGroupKey })
	return peaks
}

// Retain drops every Query Group not in owned: a Query Group this replica
// let go is not its reading to report, and the Leader carries the last
// holder's number over to the new one itself.
func (c *RetainedPeakCensus) Retain(owned []string) {
	if c == nil {
		return
	}
	keep := make(map[string]struct{}, len(owned))
	for _, key := range owned {
		keep[key] = struct{}{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.groups {
		if _, ok := keep[key]; !ok {
			delete(c.groups, key)
		}
	}
}

// Overflow is how many observations were dropped because the census was
// full. Non-zero is a replica no roster has pruned.
func (c *RetainedPeakCensus) Overflow() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.overflow
}
