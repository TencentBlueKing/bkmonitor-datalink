// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// counters are the lookback's cumulative counts since the process started,
// keyed by their closed words joined with "|". Every cell of every closed
// set exists from New: a zero is a count, and a missing cell would read as
// one.
type counters struct {
	firstReads map[string]uint64 // source: formal first reads seen
	samples    map[string]uint64 // source|outcome
	rechecks   map[string]uint64 // source|rung|outcome
	changed    map[string]uint64 // source|rung: compared windows that changed since the read before
	changes    map[string]uint64 // source|rung|class: changed buckets
	preempted  map[string]uint64 // source|rung: reads stopped for a formal query
	completion map[string]uint64 // source|age: completed samples by when the window was complete
	probes     map[string]uint64 // source|outcome: deep rechecks
	classes    map[string]uint64 // source|class: completed samples by what their rungs found
	// source|reason: unclassified samples by why their series are not known.
	unclassified map[string]uint64
	// The directed reads: source|outcome their Slots by what they came to,
	// source|reason the unobserved ones by why, source|outcome the series
	// outcomes of the supplements that ran, and per source the points those
	// were evaluated on and the bytes the directed reads delivered.
	directedWindows    map[string]uint64
	directedUnobserved map[string]uint64
	supplementSeries   map[string]uint64
	supplementPoints   map[string]uint64
	directedBytes      map[string]uint64
	// source|bucket: how long each supplement held its Query Group's
	// flight, and per source the longest.
	supplementHold    map[string]uint64
	supplementHoldMax map[string]time.Duration
	// source|outcome: directed Slots by what their early read came to; per
	// source the (Plan, series) pairs early supplements left undecided,
	// which the read at the rung does not supplement again.
	early          map[string]uint64
	earlyUndecided map[string]uint64
	// earlyBytes is, per source, what the early reads delivered: part of
	// directedBytes, apart so the early read's own cost can be read.
	earlyBytes   map[string]uint64
	earlierReads map[string]uint64
	earlierBytes map[string]uint64
	holdIgnored  map[string]uint64
	// source|outcome and source|age: completed samples whose first read was
	// empty, and when those whose data arrived later were complete.
	emptyFirstReads map[string]uint64
	emptyCompletion map[string]uint64
	refusals        map[string]uint64 // reason: permits refused
	faults          map[string]uint64 // reason
	// Per source: the latest completion seen, the bytes rechecks read back,
	// and samples whose query's lookback could not be read.
	maxCompletion   map[string]time.Duration
	recheckBytes    map[string]uint64
	unknownLookback map[string]uint64
	// Per source: reads a waiting formal query asked to yield, how long
	// they took to give their permit back in all, and the longest.
	yieldReleases       map[string]uint64
	yieldReleaseSeconds map[string]float64
	yieldReleaseMax     map[string]time.Duration
}

func newCounters(sources, refusals []string) counters {
	c := counters{firstReads: map[string]uint64{}, samples: map[string]uint64{}, rechecks: map[string]uint64{},
		changed: map[string]uint64{}, changes: map[string]uint64{}, preempted: map[string]uint64{},
		completion: map[string]uint64{}, probes: map[string]uint64{}, classes: map[string]uint64{}, unclassified: map[string]uint64{}, emptyFirstReads: map[string]uint64{},
		directedWindows: map[string]uint64{}, directedUnobserved: map[string]uint64{}, supplementSeries: map[string]uint64{},
		supplementPoints: map[string]uint64{}, directedBytes: map[string]uint64{},
		supplementHold: map[string]uint64{}, supplementHoldMax: map[string]time.Duration{},
		early: map[string]uint64{}, earlyUndecided: map[string]uint64{}, earlyBytes: map[string]uint64{},
		earlierReads: map[string]uint64{}, earlierBytes: map[string]uint64{}, holdIgnored: map[string]uint64{},
		emptyCompletion: map[string]uint64{}, refusals: map[string]uint64{RefusedOther: 0}, faults: map[string]uint64{},
		maxCompletion: map[string]time.Duration{}, recheckBytes: map[string]uint64{}, unknownLookback: map[string]uint64{},
		yieldReleases: map[string]uint64{}, yieldReleaseSeconds: map[string]float64{}, yieldReleaseMax: map[string]time.Duration{}}
	for _, reason := range refusals {
		c.refusals[reason] = 0
	}
	for _, fault := range Faults {
		c.faults[fault] = 0
	}
	for _, source := range sources {
		c.firstReads[source] = 0
		c.maxCompletion[source], c.recheckBytes[source], c.unknownLookback[source] = 0, 0, 0
		c.yieldReleases[source], c.yieldReleaseSeconds[source], c.yieldReleaseMax[source] = 0, 0, 0
		for _, outcome := range SampleOutcomes {
			c.samples[key2(source, outcome)] = 0
		}
		for _, age := range AgeBuckets {
			c.completion[key2(source, age)] = 0
			c.emptyCompletion[key2(source, age)] = 0
		}
		for _, outcome := range ProbeOutcomes {
			c.probes[key2(source, outcome)] = 0
		}
		for _, class := range SampleClasses {
			c.classes[key2(source, class)] = 0
		}
		for _, reason := range UnclassifiedReasons {
			c.unclassified[key2(source, reason)] = 0
		}
		for _, outcome := range DirectedOutcomes {
			c.directedWindows[key2(source, outcome)] = 0
		}
		for _, reason := range DirectedUnobservedReasons {
			c.directedUnobserved[key2(source, reason)] = 0
		}
		for _, outcome := range SupplementSeriesOutcomes {
			c.supplementSeries[key2(source, outcome)] = 0
		}
		c.supplementPoints[source], c.directedBytes[source] = 0, 0
		for _, bucket := range SupplementHoldBuckets {
			c.supplementHold[key2(source, bucket)] = 0
		}
		c.supplementHoldMax[source] = 0
		for _, outcome := range EarlyOutcomes {
			c.early[key2(source, outcome)] = 0
		}
		c.earlyUndecided[source], c.earlyBytes[source] = 0, 0
		for _, outcome := range EarlierReadOutcomes {
			c.earlierReads[key2(source, outcome)] = 0
		}
		c.earlierBytes[source] = 0
		for _, reason := range ReadHoldIgnoredReasons {
			c.holdIgnored[key2(source, reason)] = 0
		}
		for _, outcome := range EmptyFirstReadOutcomes {
			c.emptyFirstReads[key2(source, outcome)] = 0
		}
		for _, rung := range RungNames {
			c.changed[key2(source, rung)] = 0
			c.preempted[key2(source, rung)] = 0
			for _, outcome := range RecheckOutcomes {
				c.rechecks[key3(source, rung, outcome)] = 0
			}
			for _, class := range Changes {
				c.changes[key3(source, rung, class)] = 0
			}
		}
	}
	return c
}

func key2(a, b string) string    { return a + "|" + b }
func key3(a, b, c string) string { return a + "|" + b + "|" + c }

// Stats is the lookback as it stands.
type Stats struct {
	ReadHoldTransitions         uint64 `json:"read_hold_transition"`
	ReadHoldTransitionOvertaken uint64 `json:"read_hold_transition_overtaken"`
	// ReadHoldPredecessors is what a successor took each moved Plan's
	// previous hold from, and the links it skipped, by reason.
	ReadHoldPredecessors map[string]uint64 `json:"read_hold_predecessors,omitempty"`
	// ReadHoldClamped is the Slots frozen at the hold limit because a
	// transition asked for more: known or fallback.
	ReadHoldClamped map[string]uint64 `json:"read_hold_transition_clamped,omitempty"`
	// ReadHoldOwnCorrupt is the groups whose own record did not decode and
	// was replaced; ReadHoldRetireCloseFailed the retired groups whose
	// closing failed and that retired all the same.
	ReadHoldOwnCorrupt        uint64 `json:"read_hold_record_corrupt"`
	ReadHoldRetireCloseFailed uint64 `json:"read_hold_retire_close_failed"`
	// ReadHoldCloseSkipped is the previous Segments a prepare skipped
	// closing because the record was already past them without their
	// closing facts.
	ReadHoldCloseSkipped uint64 `json:"read_hold_close_previous_skipped"`
	// ReadHoldDegraded is the Slots frozen with the hold their group last
	// read because its own could not be prepared or written, by what failed.
	ReadHoldDegraded map[string]uint64 `json:"read_hold_degraded,omitempty"`
	// ReadHoldGroups is the Query Groups this process holds, by source and
	// read hold; nil when it holds none, which says nothing of the hold.
	ReadHoldGroups map[string]ReadHoldGroups `json:"read_hold_groups,omitempty"`
	Coverage       Coverage                  `json:"coverage"`
	// Sources: every source label, the data sources and mixed and other.
	Sources map[string]SourceStats `json:"sources"`
	// PermitRefusals: reason -> permits refused. A refused rung keeps its
	// window; one that finds no permit in it is counted as yielded.
	PermitRefusals map[string]uint64 `json:"permit_refusals"`
	// Faults: reason -> reads not kept for a defect. Always 0 in normal
	// running; anything else is a defect to fix.
	Faults map[string]uint64 `json:"faults"`
	// Pending is the samples in flight - one at most per Query Group, and
	// one waiting for its deep recheck - and PendingBytes what their
	// summaries hold: computed, not a budget.
	Pending      int `json:"pending"`
	PendingBytes int `json:"pending_bytes"`
	// Latest is the measured Query Groups whose data was complete latest,
	// latest first, at most maxLatest.
	Latest []GroupLateness `json:"latest"`
	// ReadEarly is the owned Query Groups whose window was read early in
	// readEarlyRepeat of their latest readEarlyWindow classified samples,
	// with the time_delay that
	// would have read them complete, the furthest from it first, at most
	// maxLatest.
	ReadEarly []ReadEarlyReading `json:"read_early"`
	// LatePastRound is the owned Query Groups whose late series had crossed
	// their Slots in latePastRoundRepeat supplemented windows in a row, with
	// the time_delay that would read them, and ResidualMisses the directed
	// ones with series their supplements could not recover in windows they
	// recovered part of; each by Query Group, at most maxLatest.
	LatePastRound  []LatePastRoundReading `json:"late_past_round"`
	ResidualMisses []ResidualMissReading  `json:"residual_misses"`
	// Recent is the latest rechecks that found a change, at most maxRecent.
	Recent []Recent `json:"recent"`
	// NeverCompleteFirstRead is the owned Query Groups none of whose first
	// reads has been whole, the most incomplete reads first, at most
	// maxLatest; Coverage.NeverCompleteFirstRead counts them all.
	NeverCompleteFirstRead []NeverCompleteGroup `json:"never_complete_first_read"`
	// Supplements is the owned Query Groups read directed for their late
	// series, or that were, the least covered first, at most maxLatest.
	Supplements []SupplementReading `json:"supplements"`
}

// Coverage is how many of the Query Groups this process owns have a
// measurement that is fresh - a sample in flight, or one finished within
// twice the time a sample of it takes - out of how many it owns. The aim is
// every one.
//
// NeverCompleteFirstRead is the owned Query Groups none of whose first reads
// has been whole yet: nothing to compare a recheck against, so never
// measured. They are left out of CoverableRatio's denominator and named on
// Stats.NeverCompleteFirstRead - a group whose read is never whole is a
// finding of its own, not a group the lookback has not got to.
type Coverage struct {
	Owned                  int     `json:"owned"`
	Covered                int     `json:"covered"`
	Ratio                  float64 `json:"ratio"`
	NeverCompleteFirstRead int     `json:"never_complete_first_read"`
	CoverableRatio         float64 `json:"coverable_ratio"`
}

// NeverCompleteGroup is an owned Query Group none of whose first reads has
// been whole: its source and how many first reads were not.
type NeverCompleteGroup struct {
	QueryGroup           execution.QueryGroupIdentity `json:"query_group"`
	Source               string                       `json:"source"`
	IncompleteFirstReads uint64                       `json:"incomplete_first_reads"`
}

// SourceStats is one source's Query Groups summed: what their data does,
// how they are rechecked, and what the rechecking costs. A source does not
// decide how its groups are rechecked; each group learns that from its own
// samples.
type SourceStats struct {
	// FirstReads is the formal first reads seen, FirstReadBytes the bytes
	// they delivered; Rechecks and RecheckBytes over them are the query
	// volume the lookback adds, counted the same way.
	FirstReads     uint64 `json:"first_reads"`
	FirstReadBytes uint64 `json:"first_read_bytes"`
	RecheckBytes   uint64 `json:"recheck_bytes"`
	// YieldReleases is the reads a waiting formal query asked to yield, and
	// YieldReleaseSeconds and YieldReleaseMaxSeconds how long they took to
	// give their permits back, in all and at most: the wait a formal query
	// can owe the lookback, the moment it has to wait at all.
	YieldReleases          uint64  `json:"yield_releases"`
	YieldReleaseSeconds    float64 `json:"yield_release_seconds"`
	YieldReleaseMaxSeconds float64 `json:"yield_release_max_seconds"`
	// Samples: outcome -> count. UnobservedRatio is the unobserved samples -
	// their last rungs not read - over all finished ones, and
	// ProbeChangedRatio the probe_changed ones: windows later than the rungs
	// their groups read, which only the deep recheck saw and Completion does
	// not hold.
	Samples           map[string]uint64 `json:"samples"`
	UnobservedRatio   float64           `json:"unobserved_ratio"`
	ProbeChangedRatio float64           `json:"probe_changed_ratio"`
	// Probes: outcome -> deep rechecks. Changed over clean and changed is
	// how often a group's data was later than the rungs it read.
	Probes map[string]uint64 `json:"probes"`
	// Classes: class -> completed samples by what their rungs found against
	// the first read (see ClassWindowReadEarly), and Unclassified: reason ->
	// those of them whose series are not known. ReadEarlyGroups is its
	// Query Groups now reported as read early, SeriesLateGroups those whose
	// late series were seen.
	Classes          map[string]uint64 `json:"classes"`
	Unclassified     map[string]uint64 `json:"unclassified"`
	ReadEarlyGroups  int               `json:"read_early_groups"`
	SeriesLateGroups int               `json:"series_late_groups"`
	// The directed reads of its series_late Query Groups: their Slots by
	// outcome (DirectedOutcomes) and the unobserved ones by why, the series
	// outcomes of the supplements that ran and the points those were
	// evaluated on, and the bytes the directed reads delivered - a Slot's
	// frozen query each, so read against FirstReadBytes over FirstReads.
	SupplementWindows    map[string]uint64 `json:"supplement_windows"`
	SupplementUnobserved map[string]uint64 `json:"supplement_unobserved"`
	SupplementSeries     map[string]uint64 `json:"supplement_series"`
	SupplementPoints     uint64            `json:"supplement_points"`
	DirectedReadBytes    uint64            `json:"directed_read_bytes"`
	// SupplementHold is how long the supplements held their Query Groups'
	// flights, by SupplementHoldBuckets, and SupplementHoldMaxSeconds the
	// longest: what a Query Group's own Slot waited behind one.
	SupplementHold           map[string]uint64 `json:"supplement_hold"`
	SupplementHoldMaxSeconds float64           `json:"supplement_hold_max_seconds"`
	// EarlyReads is its directed Slots by what their early read came to
	// (EarlyOutcomes), and EarlyUndecided the (Plan, series) pairs early
	// supplements left undecided, which are not supplemented again.
	EarlyReads     map[string]uint64 `json:"early_reads"`
	EarlyUndecided uint64            `json:"early_undecided"`
	// EarlyReadBytes is what the early reads delivered, part of
	// DirectedReadBytes: the bytes reading early added.
	EarlyReadBytes uint64 `json:"early_read_bytes"`
	// EarlierReads compares h/2 samples only with their formal first reads.
	EarlierReads     map[string]uint64 `json:"earlier_reads"`
	EarlierReadBytes uint64            `json:"earlier_read_bytes"`
	ReadHoldIgnored  map[string]uint64 `json:"read_hold_ignored"`
	// EmptyFirstReads: outcome -> completed samples whose first read was
	// complete and held no point - arrived when data came at a later rung,
	// stayed_empty when none did - and EmptyFirstReadCompletion when those
	// that arrived were complete. Arrived over completed samples is the
	// share of windows empty when first read that were not empty.
	EmptyFirstReads          map[string]uint64 `json:"empty_first_reads"`
	EmptyFirstReadCompletion map[string]uint64 `json:"empty_first_read_completion"`
	// UnknownLookback is the samples whose query's lookback could not be
	// read, rechecked from a step before their tail.
	UnknownLookback uint64 `json:"unknown_lookback"`
	// Rechecks: rung -> outcome -> count. Only compared is a window observed.
	Rechecks map[string]map[string]uint64 `json:"rechecks"`
	// ChangedWindows: rung -> compared windows that changed since the read
	// before; over Rechecks[rung][compared] it is the share of windows still
	// arriving at that rung.
	ChangedWindows map[string]uint64 `json:"changed_windows"`
	// Changes: rung -> class -> changed buckets.
	Changes map[string]map[string]uint64 `json:"changes"`
	// Preempted: rung -> reads stopped for a formal query and tried again.
	Preempted map[string]uint64 `json:"preempted"`
	// Completion: age past the window's end -> finished samples whose data
	// was complete by then; MaxCompletionSeconds the latest seen.
	Completion           map[string]uint64 `json:"completion"`
	MaxCompletionSeconds int64             `json:"max_completion_seconds"`
	// Groups is its Query Groups this process has seen; DepthGroups how
	// many of them read how many rungs now, by depth "1" to "6"; and
	// MeanRestSeconds how long they rest between samples on average.
	Groups          int               `json:"groups"`
	DepthGroups     map[string]uint64 `json:"depth_groups"`
	MeanRestSeconds float64           `json:"mean_rest_seconds"`
}

// DepthLabels label the depths a Query Group can read to.
var DepthLabels = []string{"1", "2", "3", "4", "5", "6"}

// GroupLateness is one Query Group's last measurement.
type GroupLateness struct {
	QueryGroup        execution.QueryGroupIdentity `json:"query_group"`
	Source            string                       `json:"source"`
	CompletionSeconds int64                        `json:"completion_seconds"`
	StepSeconds       int64                        `json:"step_seconds"`
	MeasuredAt        time.Time                    `json:"measured_at"`
}

// GroupReadHoldClasses split comparisons by the hold frozen for the sample,
// rather than the controller's hold when its later rungs happen to read.
var GroupReadHoldClasses = []string{"h0", "h_positive"}

// groupCounts has only the closed dimensions: two hold classes, six rungs,
// five sample classes, two ignored reasons and nine earlier-read outcomes.
// It belongs to the live group and is discarded by Forget, like supplements.
type groupCounts struct {
	since        time.Time
	compared     [2][6]uint64
	changed      [2][6]uint64
	classes      [5]uint64
	ignored      [2]uint64
	earlier      [9]uint64
	earlierBytes uint64
}

func groupHoldClass(holdMillis int64) int {
	if holdMillis > 0 {
		return 1
	}
	return 0
}

func wordIndex(words []string, wanted string) int {
	for index, word := range words {
		if word == wanted {
			return index
		}
	}
	panic("alarmd lookback: counter outside its closed set")
}

// GroupReading is one owned group's cumulative in-memory facts since this
// engine first saw it. It is neither durable history nor a source aggregate.
// Compared and ChangedWindows use hold class -> rung -> count; Classes count
// only closed, classified samples. Refused earlier reads count as unobserved
// outcomes, and their delivered bytes are counted even when comparison fails.
type GroupReading struct {
	QueryGroup       execution.QueryGroupIdentity `json:"query_group"`
	Source           string                       `json:"source"`
	Since            time.Time                    `json:"since"`
	Compared         map[string]map[string]uint64 `json:"compared"`
	ChangedWindows   map[string]map[string]uint64 `json:"changed_windows"`
	Classes          map[string]uint64            `json:"classes"`
	ReadHoldIgnored  map[string]uint64            `json:"read_hold_ignored"`
	EarlierReads     map[string]uint64            `json:"earlier_reads"`
	EarlierReadBytes uint64                       `json:"earlier_read_bytes"`
}

// ReadHoldGroups is one source's Query Groups this process holds by their
// read hold: how many hold more than none, how many are at their limit, and
// how many have a hold not yet known; MaxMillis is the largest known hold,
// MaxKnown whether any is known.
type ReadHoldGroups struct {
	Held      int   `json:"held"`
	AtLimit   int   `json:"at_limit"`
	Unknown   int   `json:"unknown"`
	MaxMillis int64 `json:"max_ms"`
	MaxKnown  bool  `json:"max_known"`
}

// GroupSources is the source label of each given Query Group the lookback
// has seen, read under one lock; a group it has not seen is left out.
func (engine *Engine) GroupSources(queryGroups []execution.QueryGroupIdentity) map[execution.QueryGroupIdentity]string {
	sources := make(map[execution.QueryGroupIdentity]string, len(queryGroups))
	if engine == nil {
		return sources
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	for _, queryGroup := range queryGroups {
		if state := engine.groups[queryGroup]; state != nil && state.source != "" {
			sources[queryGroup] = state.source
		}
	}
	return sources
}

// GroupReading returns an independent snapshot. Ownership is asked outside
// Engine.mu, because its answer may hold the Runner lock that calls Forget.
func (engine *Engine) GroupReading(queryGroup execution.QueryGroupIdentity) (GroupReading, bool) {
	if engine == nil {
		return GroupReading{}, false
	}
	engine.mu.Lock()
	state := engine.groups[queryGroup]
	if state == nil {
		engine.mu.Unlock()
		return GroupReading{}, false
	}
	counts, source := state.reading, state.source
	engine.mu.Unlock()
	if !engine.options.Owns(queryGroup) {
		return GroupReading{}, false
	}
	reading := GroupReading{QueryGroup: queryGroup, Source: source, Since: counts.since,
		Compared: map[string]map[string]uint64{}, ChangedWindows: map[string]map[string]uint64{},
		Classes: map[string]uint64{}, ReadHoldIgnored: map[string]uint64{}, EarlierReads: map[string]uint64{},
		EarlierReadBytes: counts.earlierBytes}
	for hold, name := range GroupReadHoldClasses {
		reading.Compared[name], reading.ChangedWindows[name] = map[string]uint64{}, map[string]uint64{}
		for rung, word := range RungNames {
			reading.Compared[name][word] = counts.compared[hold][rung]
			reading.ChangedWindows[name][word] = counts.changed[hold][rung]
		}
	}
	for index, word := range SampleClasses {
		reading.Classes[word] = counts.classes[index]
	}
	for index, word := range ReadHoldIgnoredReasons {
		reading.ReadHoldIgnored[word] = counts.ignored[index]
	}
	for index, word := range EarlierReadOutcomes {
		reading.EarlierReads[word] = counts.earlier[index]
	}
	return reading, true
}

// Stats reads the lookback now. Ownership is asked outside the engine's
// lock: the Runner set that answers it calls Forget while holding its own.
func (engine *Engine) Stats() Stats {
	stats := Stats{Sources: map[string]SourceStats{}, PermitRefusals: map[string]uint64{}, Faults: map[string]uint64{},
		Latest: []GroupLateness{}, ReadEarly: []ReadEarlyReading{}, Recent: []Recent{}, NeverCompleteFirstRead: []NeverCompleteGroup{},
		Supplements: []SupplementReading{}, LatePastRound: []LatePastRoundReading{}, ResidualMisses: []ResidualMissReading{}}
	if engine == nil {
		return stats
	}
	now := engine.options.Now()
	type candidate struct {
		queryGroup    execution.QueryGroupIdentity
		fresh         bool
		lateness      *GroupLateness
		readEarly     *ReadEarlyReading
		latePastRound *LatePastRoundReading
		residualMiss  *ResidualMissReading
		neverComplete *NeverCompleteGroup
		supplement    *SupplementReading
	}
	type groupSums struct {
		groups     int
		depths     map[string]uint64
		rest       time.Duration
		readEarly  int
		seriesLate int
	}
	sums := map[string]*groupSums{}
	engine.mu.Lock()
	candidates := make([]candidate, 0, len(engine.groups))
	for queryGroup, state := range engine.groups {
		if trial := state.prepared; trial != nil {
			stats.Pending++
			stats.PendingBytes += trial.summary.bytes()
		}
		entry := candidate{queryGroup: queryGroup}
		rest := min(time.Duration(state.rest*float64(state.step)), restCap)
		switch {
		case state.capturing || state.sample != nil || state.probe != nil:
			entry.fresh = true
		case state.measured:
			// Fresh within twice the longest a sample of it can take: its
			// rest at the most spread, its deepest rung, and the wait for
			// its next first read.
			cycle := rest*5/4 + rungDelay(state.depth-1, state.step) + state.period
			entry.fresh = now.Sub(state.measuredAt) <= 2*cycle
		}
		if state.source != "" {
			sum := sums[state.source]
			if sum == nil {
				sum = &groupSums{depths: map[string]uint64{}}
				sums[state.source] = sum
			}
			sum.groups++
			sum.depths[DepthLabels[state.depth-1]]++
			sum.rest += rest
			if reading, reported := readingOf(queryGroup, state); reported {
				sum.readEarly++
				entry.readEarly = &reading
			}
			if reading, reported := latePastRoundOf(queryGroup, state); reported {
				entry.latePastRound = &reading
			}
			if reading, reported := residualMissOf(queryGroup, state); reported {
				entry.residualMiss = &reading
			}
			if state.seriesLate != nil {
				sum.seriesLate++
			}
		}
		if state.incompleteFirstReads > 0 && !state.completeFirstRead {
			// A read in flight is not a measurement for a group none of
			// whose reads has been whole.
			entry.fresh = false
			entry.neverComplete = &NeverCompleteGroup{QueryGroup: queryGroup, Source: state.source,
				IncompleteFirstReads: state.incompleteFirstReads}
		}
		if reading, directed := engine.supplementReadingLocked(queryGroup, state); directed {
			entry.supplement = &reading
		}
		for _, slot := range state.directed {
			for _, captured := range slot.queries {
				stats.PendingBytes += len(captured.first.set) * seriesSetEntryBytes
			}
		}
		if state.measured {
			entry.lateness = &GroupLateness{QueryGroup: queryGroup, Source: state.source,
				CompletionSeconds: int64(state.completion / time.Second), StepSeconds: int64(state.step / time.Second),
				MeasuredAt: state.measuredAt}
		}
		for _, pending := range [...]*sample{state.sample, state.probe} {
			if pending != nil {
				stats.Pending++
				stats.PendingBytes += pending.last.bytes() + len(pending.first)*seriesEntryBytes
			}
		}
		candidates = append(candidates, entry)
	}
	for _, source := range Sources {
		entry := SourceStats{FirstReads: engine.counts.firstReads[source], FirstReadBytes: engine.firstReadBytes[source].Load(),
			RecheckBytes: engine.counts.recheckBytes[source], UnknownLookback: engine.counts.unknownLookback[source],
			YieldReleases: engine.counts.yieldReleases[source], YieldReleaseSeconds: engine.counts.yieldReleaseSeconds[source],
			YieldReleaseMaxSeconds: engine.counts.yieldReleaseMax[source].Seconds(),
			Samples:                map[string]uint64{}, Rechecks: map[string]map[string]uint64{}, ChangedWindows: map[string]uint64{},
			Changes: map[string]map[string]uint64{}, Preempted: map[string]uint64{}, Completion: map[string]uint64{},
			MaxCompletionSeconds: int64(engine.counts.maxCompletion[source] / time.Second), DepthGroups: map[string]uint64{},
			Probes: map[string]uint64{}, EmptyFirstReads: map[string]uint64{}, EmptyFirstReadCompletion: map[string]uint64{},
			Classes: map[string]uint64{}, Unclassified: map[string]uint64{},
			SupplementWindows: map[string]uint64{}, SupplementUnobserved: map[string]uint64{}, SupplementSeries: map[string]uint64{},
			SupplementPoints: engine.counts.supplementPoints[source], DirectedReadBytes: engine.counts.directedBytes[source],
			SupplementHold: map[string]uint64{}, SupplementHoldMaxSeconds: engine.counts.supplementHoldMax[source].Seconds(),
			EarlyReads: map[string]uint64{}, EarlyUndecided: engine.counts.earlyUndecided[source],
			EarlyReadBytes: engine.counts.earlyBytes[source], EarlierReads: map[string]uint64{}, EarlierReadBytes: engine.counts.earlierBytes[source], ReadHoldIgnored: map[string]uint64{}}
		for _, reason := range ReadHoldIgnoredReasons {
			entry.ReadHoldIgnored[reason] = engine.counts.holdIgnored[key2(source, reason)]
		}
		for _, outcome := range EarlierReadOutcomes {
			entry.EarlierReads[outcome] = engine.counts.earlierReads[key2(source, outcome)]
		}
		for _, outcome := range EarlyOutcomes {
			entry.EarlyReads[outcome] = engine.counts.early[key2(source, outcome)]
		}
		for _, bucket := range SupplementHoldBuckets {
			entry.SupplementHold[bucket] = engine.counts.supplementHold[key2(source, bucket)]
		}
		for _, depth := range DepthLabels {
			entry.DepthGroups[depth] = 0
		}
		if sum := sums[source]; sum != nil {
			entry.Groups = sum.groups
			for depth, n := range sum.depths {
				entry.DepthGroups[depth] = n
			}
			entry.MeanRestSeconds = sum.rest.Seconds() / float64(sum.groups)
			entry.ReadEarlyGroups, entry.SeriesLateGroups = sum.readEarly, sum.seriesLate
		}
		for _, class := range SampleClasses {
			entry.Classes[class] = engine.counts.classes[key2(source, class)]
		}
		for _, reason := range UnclassifiedReasons {
			entry.Unclassified[reason] = engine.counts.unclassified[key2(source, reason)]
		}
		for _, outcome := range DirectedOutcomes {
			entry.SupplementWindows[outcome] = engine.counts.directedWindows[key2(source, outcome)]
		}
		for _, reason := range DirectedUnobservedReasons {
			entry.SupplementUnobserved[reason] = engine.counts.directedUnobserved[key2(source, reason)]
		}
		for _, outcome := range SupplementSeriesOutcomes {
			entry.SupplementSeries[outcome] = engine.counts.supplementSeries[key2(source, outcome)]
		}
		for _, outcome := range SampleOutcomes {
			entry.Samples[outcome] = engine.counts.samples[key2(source, outcome)]
		}
		if finished := entry.Samples[OutcomeCompleted] + entry.Samples[OutcomeUnobserved] + entry.Samples[OutcomeProbeChanged]; finished > 0 {
			entry.UnobservedRatio = float64(entry.Samples[OutcomeUnobserved]) / float64(finished)
			entry.ProbeChangedRatio = float64(entry.Samples[OutcomeProbeChanged]) / float64(finished)
		}
		for _, age := range AgeBuckets {
			entry.Completion[age] = engine.counts.completion[key2(source, age)]
			entry.EmptyFirstReadCompletion[age] = engine.counts.emptyCompletion[key2(source, age)]
		}
		for _, outcome := range ProbeOutcomes {
			entry.Probes[outcome] = engine.counts.probes[key2(source, outcome)]
		}
		for _, outcome := range EmptyFirstReadOutcomes {
			entry.EmptyFirstReads[outcome] = engine.counts.emptyFirstReads[key2(source, outcome)]
		}
		for _, rung := range RungNames {
			entry.ChangedWindows[rung] = engine.counts.changed[key2(source, rung)]
			entry.Preempted[rung] = engine.counts.preempted[key2(source, rung)]
			entry.Rechecks[rung] = map[string]uint64{}
			for _, outcome := range RecheckOutcomes {
				entry.Rechecks[rung][outcome] = engine.counts.rechecks[key3(source, rung, outcome)]
			}
			entry.Changes[rung] = map[string]uint64{}
			for _, class := range Changes {
				entry.Changes[rung][class] = engine.counts.changes[key3(source, rung, class)]
			}
		}
		stats.Sources[source] = entry
	}
	for reason, n := range engine.counts.refusals {
		stats.PermitRefusals[reason] = n
	}
	for reason, n := range engine.counts.faults {
		stats.Faults[reason] = n
	}
	stats.Recent = append(stats.Recent, engine.recent...)
	engine.mu.Unlock()

	stats.Coverage.Owned = engine.options.Owned()
	for _, entry := range candidates {
		if !engine.options.Owns(entry.queryGroup) {
			continue
		}
		if entry.fresh {
			stats.Coverage.Covered++
		}
		if entry.neverComplete != nil {
			stats.Coverage.NeverCompleteFirstRead++
			stats.NeverCompleteFirstRead = append(stats.NeverCompleteFirstRead, *entry.neverComplete)
		}
		if entry.lateness != nil {
			stats.Latest = append(stats.Latest, *entry.lateness)
		}
		if entry.readEarly != nil {
			stats.ReadEarly = append(stats.ReadEarly, *entry.readEarly)
		}
		if entry.latePastRound != nil {
			stats.LatePastRound = append(stats.LatePastRound, *entry.latePastRound)
		}
		if entry.residualMiss != nil {
			stats.ResidualMisses = append(stats.ResidualMisses, *entry.residualMiss)
		}
		if entry.supplement != nil {
			stats.Supplements = append(stats.Supplements, *entry.supplement)
		}
	}
	sort.Slice(stats.Supplements, func(i, j int) bool {
		if stats.Supplements[i].Coverage != stats.Supplements[j].Coverage {
			return stats.Supplements[i].Coverage < stats.Supplements[j].Coverage
		}
		return stats.Supplements[i].QueryGroup < stats.Supplements[j].QueryGroup
	})
	if len(stats.Supplements) > maxLatest {
		stats.Supplements = stats.Supplements[:maxLatest]
	}
	sort.Slice(stats.ReadEarly, func(i, j int) bool {
		left := stats.ReadEarly[i].SuggestedDelaySeconds - stats.ReadEarly[i].CurrentDelaySeconds
		right := stats.ReadEarly[j].SuggestedDelaySeconds - stats.ReadEarly[j].CurrentDelaySeconds
		if left != right {
			return left > right
		}
		return stats.ReadEarly[i].QueryGroup < stats.ReadEarly[j].QueryGroup
	})
	if len(stats.ReadEarly) > maxLatest {
		stats.ReadEarly = stats.ReadEarly[:maxLatest]
	}
	sort.Slice(stats.LatePastRound, func(i, j int) bool { return stats.LatePastRound[i].QueryGroup < stats.LatePastRound[j].QueryGroup })
	stats.LatePastRound = stats.LatePastRound[:min(len(stats.LatePastRound), maxLatest)]
	sort.Slice(stats.ResidualMisses, func(i, j int) bool {
		if stats.ResidualMisses[i].CrossedSeries != stats.ResidualMisses[j].CrossedSeries {
			return stats.ResidualMisses[i].CrossedSeries > stats.ResidualMisses[j].CrossedSeries
		}
		return stats.ResidualMisses[i].QueryGroup < stats.ResidualMisses[j].QueryGroup
	})
	stats.ResidualMisses = stats.ResidualMisses[:min(len(stats.ResidualMisses), maxLatest)]
	if stats.Coverage.Owned > 0 {
		stats.Coverage.Ratio = float64(stats.Coverage.Covered) / float64(stats.Coverage.Owned)
	}
	if coverable := stats.Coverage.Owned - stats.Coverage.NeverCompleteFirstRead; coverable > 0 {
		stats.Coverage.CoverableRatio = float64(stats.Coverage.Covered) / float64(coverable)
	}
	sort.Slice(stats.NeverCompleteFirstRead, func(i, j int) bool {
		left, right := stats.NeverCompleteFirstRead[i], stats.NeverCompleteFirstRead[j]
		if left.IncompleteFirstReads != right.IncompleteFirstReads {
			return left.IncompleteFirstReads > right.IncompleteFirstReads
		}
		return left.QueryGroup < right.QueryGroup
	})
	if len(stats.NeverCompleteFirstRead) > maxLatest {
		stats.NeverCompleteFirstRead = stats.NeverCompleteFirstRead[:maxLatest]
	}
	sort.Slice(stats.Latest, func(i, j int) bool {
		if stats.Latest[i].CompletionSeconds != stats.Latest[j].CompletionSeconds {
			return stats.Latest[i].CompletionSeconds > stats.Latest[j].CompletionSeconds
		}
		return stats.Latest[i].QueryGroup < stats.Latest[j].QueryGroup
	})
	if len(stats.Latest) > maxLatest {
		stats.Latest = stats.Latest[:maxLatest]
	}
	return stats
}
