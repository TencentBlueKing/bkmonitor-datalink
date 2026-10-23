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

// A supplemented window's late series come to one of three things, and each
// has its own remedy.
//
// Every late series had crossed its Slot (crossed_t: the series' State was at
// the Slot or past it when the supplement came): the supplement recovered
// none, because the data arrives after the next round has already been
// judged. Only a longer time_delay reads it -- the strategy's to change, the
// same as a window read early. Two such windows in a row report it, and a
// window the supplement admitted anything in, or one with nothing late, ends
// it: the rule a window read early is reported and cleared by, with no
// proportion of its own. Every series means every one: a window some of
// whose series were withheld, read incomplete, drifted in configuration or
// recorded absent was not late alone, and a longer time_delay is not what it
// asks for. With nothing admitted it neither adds to the run nor ends it.
//
// Some series were admitted and some had crossed: the supplement is doing its
// work, and a longer time_delay would slow the whole Query Group for a few
// series that are later still. Those few are a residual miss, counted with
// the latest windows as evidence, and the data's to look at.
//
// None had crossed: the supplement recovered everything late.
//
// Both reports stand while the Query Group is read directed (seriesLateState)
// and go, with what they were counted from, when its series are late no
// more: a Query Group read directed again starts both afresh, from windows
// opened since.

// LatePastRoundSample is one supplemented window every late series of which
// had crossed its Slot: the Slot, the rung its late series were read at and
// how long after the Slot's first read that was, how many series the first
// read had and how many the read at the rung found late -- series, each
// once -- and how many (Plan, series) pairs had crossed: a series of a
// query several Plans share is a pair of each.
type LatePastRoundSample struct {
	EvaluationTime execution.EvaluationTime `json:"evaluation_time"`
	Rung           string                   `json:"rung"`
	SeenAgeSeconds int64                    `json:"seen_age_seconds"`
	// ReadHoldSeconds is the hold the window's first read waited, which the
	// seen age is counted after.
	ReadHoldSeconds int64 `json:"read_hold_seconds,omitempty"`
	OnTimeSeries    int   `json:"on_time_series"`
	LateSeries      int   `json:"late_series"`
	CrossedSeries   int   `json:"crossed_series"`
}

// LatePastRoundReading is a Query Group whose late series crossed their
// Slots in latePastRoundRepeat supplemented windows in a row: the time_delay
// its query runs under, and that plus how long after the first read its late
// series were seen, aligned up to the step -- an upper bound, the series came
// between that rung and the one before it.
type LatePastRoundReading struct {
	QueryGroup            execution.QueryGroupIdentity `json:"query_group"`
	Source                string                       `json:"source"`
	StepSeconds           int64                        `json:"step_seconds"`
	CurrentDelaySeconds   int64                        `json:"current_time_delay_seconds"`
	SuggestedDelaySeconds int64                        `json:"suggested_time_delay_seconds"`
	Since                 time.Time                    `json:"since"`
	Samples               []LatePastRoundSample        `json:"samples"`
}

// ResidualMissSample is one supplemented window with series admitted and
// series that had crossed their Slot: the Slot, how many series its first
// read had on time and how many were found late -- series, each once, the
// window whole for the reader to see how much of it was late -- and how
// many (Plan, series) pairs of the late ones were admitted and had crossed.
type ResidualMissSample struct {
	EvaluationTime execution.EvaluationTime `json:"evaluation_time"`
	OnTimeSeries   int                      `json:"on_time_series"`
	LateSeries     int                      `json:"late_series"`
	AdmittedSeries int                      `json:"admitted_series"`
	CrossedSeries  int                      `json:"crossed_series"`
}

// ResidualMissReading is a directed Query Group's residual miss: the windows
// its supplement recovered part of, the series in them it could not recover,
// since when, and the latest such windows.
type ResidualMissReading struct {
	QueryGroup    execution.QueryGroupIdentity `json:"query_group"`
	Source        string                       `json:"source"`
	Windows       uint64                       `json:"windows"`
	CrossedSeries uint64                       `json:"crossed_series"`
	Since         time.Time                    `json:"since"`
	Samples       []ResidualMissSample         `json:"samples"`
}

const (
	// latePastRoundRepeat and latePastRoundKept are readEarlyRepeat and
	// readEarlyKept: the same evidence for the same kind of report.
	latePastRoundRepeat = readEarlyRepeat
	latePastRoundKept   = readEarlyKept
	residualMissKept    = readEarlyKept
)

// latePastRoundState is a Query Group's run of supplemented windows every
// late series of which had crossed its Slot.
type latePastRoundState struct {
	consecutive  int
	since        time.Time
	delaySeconds int64
	samples      []LatePastRoundSample
}

// residualMissState is what a directed Query Group's supplements could not
// recover in the windows they recovered part of.
type residualMissState struct {
	windows uint64
	crossed uint64
	since   time.Time
	samples []ResidualMissSample
}

// noteLateSeriesLocked files one directed window's end on the Query Group's
// late-past-round run and residual miss. A window opened in directed reads
// that have since ended -- in flight when the series were late no more, and
// ending after, or after the group was read directed again -- is filed on
// neither. Caller holds engine.mu.
func (engine *Engine) noteLateSeriesLocked(state *group, slot *directedSlot, outcome string, facts *execution.SupplementFacts) {
	if slot.period != state.seriesLate {
		return
	}
	switch {
	case outcome == DirectedNothingLate:
		state.latePastRound = nil
	case outcome != DirectedSupplemented || facts == nil:
		// Not read, or read and not supplemented: says nothing either way.
	case everyLateSeriesCrossed(*facts):
		run := state.latePastRound
		if run == nil {
			run = &latePastRoundState{since: engine.options.Now()}
			state.latePastRound = run
		}
		run.consecutive++
		if len(slot.queries) > 0 {
			run.delaySeconds = slot.queries[0].spec.PlanFacts.QueryDelaySeconds
		}
		run.samples = keepLast(append(run.samples, LatePastRoundSample{EvaluationTime: slot.evaluation,
			Rung: RungNames[slot.rung], SeenAgeSeconds: int64(slot.seenAgeOrRung() / time.Second), ReadHoldSeconds: holdSeconds(slot.readHold),
			OnTimeSeries: onTimeSeries(slot), LateSeries: slot.late, CrossedSeries: facts.CrossedT}), latePastRoundKept)
	case facts.Admitted > 0:
		state.latePastRound = nil
		if facts.CrossedT == 0 {
			return
		}
		residual := state.residualMiss
		if residual == nil {
			residual = &residualMissState{since: engine.options.Now()}
			state.residualMiss = residual
		}
		residual.windows++
		residual.crossed += uint64(facts.CrossedT)
		residual.samples = keepLast(append(residual.samples, ResidualMissSample{EvaluationTime: slot.evaluation,
			OnTimeSeries: onTimeSeries(slot), LateSeries: slot.late, AdmittedSeries: facts.Admitted, CrossedSeries: facts.CrossedT}),
			residualMissKept)
	default:
		// Nothing admitted and not every series crossed: some were not late
		// alone. It says nothing either way.
	}
}

// everyLateSeriesCrossed reports whether every series of a supplemented
// window had crossed its Slot: each one given was decided, and decided
// crossed.
func everyLateSeriesCrossed(facts execution.SupplementFacts) bool {
	return facts.CrossedT > 0 && facts.CrossedT == facts.Decided() && facts.Decided() == facts.Candidates
}

// endLateSeries drops what a Query Group's supplemented windows were counted
// into, when it is read directed no more. Caller holds engine.mu.
func endLateSeries(state *group) {
	state.seriesLate, state.latePastRound, state.residualMiss = nil, nil, nil
}

// seenAgeOrRung is how long after its first read the series that had crossed
// the Slot were seen: as filed, or its rung's moment for a Slot filed
// without one.
func (slot *directedSlot) seenAgeOrRung() time.Duration {
	if slot.seenAge > 0 {
		return slot.seenAge
	}
	return rungDelay(slot.rung, slot.step)
}

// onTimeSeries is how many series the window's first read had: the ones on
// time, beside the late ones its supplement read.
func onTimeSeries(slot *directedSlot) int {
	onTime := 0
	for _, query := range slot.queries {
		if query.first != nil {
			onTime += len(query.first.set)
		}
	}
	return onTime
}

// holdSeconds is a read hold in the whole seconds a sample states it in,
// rounded up: a part of a second counts as the second it reaches into, so
// the advice it goes into is never short of what the data needed.
func holdSeconds(millis int64) int64 {
	return (millis + 999) / 1000
}

// keepLast is the last n of a list, in a slice of its own.
func keepLast[T any](list []T, n int) []T {
	if len(list) <= n {
		return list
	}
	return append([]T(nil), list[len(list)-n:]...)
}

// latePastRoundOf is the Query Group's report when its run is long enough
// and it is still read directed.
func latePastRoundOf(queryGroup execution.QueryGroupIdentity, state *group) (LatePastRoundReading, bool) {
	run := state.latePastRound
	if run == nil || state.seriesLate == nil || run.consecutive < latePastRoundRepeat {
		return LatePastRoundReading{}, false
	}
	step := int64(state.step / time.Second)
	// How much later than the time_delay the late series came: after the
	// first read, which waited its hold, plus that hold. Without the hold a
	// held group was advised a time_delay that still needed it.
	later := int64(0)
	for _, sample := range run.samples {
		later = max(later, sample.SeenAgeSeconds+sample.ReadHoldSeconds)
	}
	suggested := execution.SuggestedTimeDelaySeconds(time.Duration(run.delaySeconds+later)*time.Second,
		time.Duration(run.delaySeconds)*time.Second, state.delayUnit)
	return LatePastRoundReading{QueryGroup: queryGroup, Source: state.source, StepSeconds: step,
		CurrentDelaySeconds: run.delaySeconds, SuggestedDelaySeconds: suggested, Since: run.since,
		Samples: append([]LatePastRoundSample(nil), run.samples...)}, true
}

// residualMissOf is the Query Group's residual miss while it is read
// directed.
func residualMissOf(queryGroup execution.QueryGroupIdentity, state *group) (ResidualMissReading, bool) {
	residual := state.residualMiss
	if residual == nil || state.seriesLate == nil {
		return ResidualMissReading{}, false
	}
	return ResidualMissReading{QueryGroup: queryGroup, Source: state.source, Windows: residual.windows,
		CrossedSeries: residual.crossed, Since: residual.since,
		Samples: append([]ResidualMissSample(nil), residual.samples...)}, true
}

// LatePastRound is every Query Group this process owns whose late series
// crossed their Slots in latePastRoundRepeat supplemented windows in a row,
// by Query Group.
func (engine *Engine) LatePastRound() []LatePastRoundReading {
	if engine == nil {
		return nil
	}
	engine.mu.Lock()
	readings := make([]LatePastRoundReading, 0)
	for queryGroup, state := range engine.groups {
		if reading, reported := latePastRoundOf(queryGroup, state); reported {
			readings = append(readings, reading)
		}
	}
	engine.mu.Unlock()
	owned := readings[:0]
	for _, reading := range readings {
		if engine.options.Owns(reading.QueryGroup) {
			owned = append(owned, reading)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].QueryGroup < owned[j].QueryGroup })
	return owned
}

// ResidualMisses is every directed Query Group this process owns with a
// residual miss, by Query Group.
func (engine *Engine) ResidualMisses() []ResidualMissReading {
	if engine == nil {
		return nil
	}
	engine.mu.Lock()
	readings := make([]ResidualMissReading, 0)
	for queryGroup, state := range engine.groups {
		if reading, reported := residualMissOf(queryGroup, state); reported {
			readings = append(readings, reading)
		}
	}
	engine.mu.Unlock()
	owned := readings[:0]
	for _, reading := range readings {
		if engine.options.Owns(reading.QueryGroup) {
			owned = append(owned, reading)
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].QueryGroup < owned[j].QueryGroup })
	return owned
}
