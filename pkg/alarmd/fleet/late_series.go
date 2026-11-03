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
	"sort"
	"time"
)

// MaxLateSeriesSamples is how many windows a row of KindLatePastRound or
// KindLateSeriesMissed carries as evidence, newest last: the lookback's own
// bound.
const MaxLateSeriesSamples = 3

// LatePastRoundSample is one supplemented window every late series of which
// had crossed its Slot: the Slot, the rung its late series were read at and
// how long after the first read, how many series the first read had and how
// many were found late -- series, each once -- and how many (Plan, series)
// pairs had crossed.
type LatePastRoundSample struct {
	EvaluationTime int64  `json:"evaluation_time"`
	Rung           string `json:"rung,omitempty"`
	SeenAgeSeconds int64  `json:"seen_age_seconds"`
	// ReadHoldSeconds is the hold the window's first read waited; the seen
	// age is counted after it.
	ReadHoldSeconds int64 `json:"read_hold_seconds,omitempty"`
	OnTimeSeries    int   `json:"on_time_series"`
	LateSeries      int   `json:"late_series"`
	CrossedSeries   int   `json:"crossed_series"`
}

// LatePastRoundFacts is what a row of KindLatePastRound says for itself: the
// object's data step, the time_delay its query runs under, and that plus the
// hold its first read waited plus how long after that read its late series
// were seen, aligned up to the step -- an upper bound, since they came
// between that read and the one before -- with the windows it rests on.
type LatePastRoundFacts struct {
	StepSeconds           int64 `json:"step_seconds"`
	CurrentDelaySeconds   int64 `json:"current_time_delay_seconds"`
	SuggestedDelaySeconds int64 `json:"suggested_time_delay_seconds"`
	ReadHoldMillis        int64 `json:"read_hold_ms,omitempty"`
	// ReadHoldUnknown: as ReadEarlyFacts.ReadHoldUnknown.
	ReadHoldUnknown bool                  `json:"read_hold_unknown,omitempty"`
	Since           time.Time             `json:"since"`
	Samples         []LatePastRoundSample `json:"samples,omitempty"`
}

// LateSeriesMissedSample is one supplemented window with series recovered
// and series that had crossed their Slot: the Slot, how many series its
// first read had on time and how many were found late -- series, each once
// -- and how many (Plan, series) pairs of the late ones were recovered and
// not.
type LateSeriesMissedSample struct {
	EvaluationTime int64 `json:"evaluation_time"`
	OnTimeSeries   int   `json:"on_time_series"`
	LateSeries     int   `json:"late_series"`
	AdmittedSeries int   `json:"admitted_series"`
	CrossedSeries  int   `json:"crossed_series"`
}

// LateSeriesMissedFacts is what a row of KindLateSeriesMissed says for
// itself: in how many windows the supplement recovered part and not the
// rest, how many series it could not recover in them, since when, and the
// latest such windows.
type LateSeriesMissedFacts struct {
	Windows       uint64                   `json:"windows"`
	CrossedSeries uint64                   `json:"crossed_series"`
	Since         time.Time                `json:"since"`
	Samples       []LateSeriesMissedSample `json:"samples,omitempty"`
}

func lastSamples[T any](samples []T) []T {
	if len(samples) > MaxLateSeriesSamples {
		samples = samples[len(samples)-MaxLateSeriesSamples:]
	}
	return append([]T(nil), samples...)
}

// LateSeries is a row for every object the lookback's supplements could not
// recover late series of, by kind: past its round, and missed in part. An
// object this process never saw evaluate has no strategy to name it under
// and gets no row.
func (tracker *Tracker) LateSeries(pastRound map[string]LatePastRoundFacts, missed map[string]LateSeriesMissedFacts) []Anomaly {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	anomalies := make([]Anomaly, 0, len(pastRound)+len(missed))
	row := func(queryGroup, kind string, since time.Time) (Anomaly, bool) {
		state := tracker.groups[queryGroup]
		if state == nil {
			return Anomaly{}, false
		}
		anomaly := Anomaly{QueryGroup: queryGroup, Kind: kind, Since: since, SinceFrom: SinceSnapshotContinuity,
			Replica: tracker.replica, LastHealthyAt: state.lastHealthyAt}
		for strategy := range state.strategies {
			anomaly.Strategies = append(anomaly.Strategies, strategy)
		}
		sortStrategies(anomaly.Strategies)
		return anomaly, true
	}
	for queryGroup, facts := range pastRound {
		if anomaly, known := row(queryGroup, KindLatePastRound, facts.Since); known {
			facts.Samples = lastSamples(facts.Samples)
			anomaly.LatePastRound = &facts
			anomalies = append(anomalies, anomaly)
		}
	}
	for queryGroup, facts := range missed {
		if anomaly, known := row(queryGroup, KindLateSeriesMissed, facts.Since); known {
			facts.Samples = lastSamples(facts.Samples)
			anomaly.LateSeriesMissed = &facts
			anomalies = append(anomalies, anomaly)
		}
	}
	sort.Slice(anomalies, func(left, right int) bool {
		if anomalies[left].Kind != anomalies[right].Kind {
			return anomalies[left].Kind < anomalies[right].Kind
		}
		return anomalies[left].QueryGroup < anomalies[right].QueryGroup
	})
	return anomalies
}
