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
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// emptyRound is one empty completion whose primary was, or was not, emptied
// by the target.
func emptyRound(queryGroup string, slot time.Time, byTarget bool) observability.Observation {
	observed := emptyAt(queryGroup, "4101", slot)
	observed.PrimaryInput = &observability.PrimaryInputFacts{Completeness: "FULL", DataState: "EMPTY", EmptiedByTarget: byTarget}
	return observed
}

func emptyRunCheck(t *testing.T, tracker *Tracker, queryGroup string) (string, Check) {
	t.Helper()
	row, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)[queryGroup]
	if !listed || row.EmptyEveryRound == nil {
		t.Fatalf("%s is not on the empty line: %+v", queryGroup, tracker.NoData())
	}
	list := []Anomaly{row}
	Attribute(list, now)
	return row.EmptyEveryRound.Cause, list[0].Finding.Check
}

// An hour of rounds whose query returned series the target selected none of
// is the same empty run, under the cause and the line that send its owner to
// the target rather than the source; the latest round decides, both ways.
func TestAnEmptyRunTheTargetEmptiedIsTheTargetsLineNotTheSources(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for elapsed := time.Duration(0); elapsed <= 61*time.Minute; elapsed += time.Minute {
		tracker.Observe(context.Background(), emptyRound("qg-target", at.at, true))
		at.at = at.at.Add(time.Minute)
	}
	if cause, check := emptyRunCheck(t, tracker, "qg-target"); cause != EmptyEveryRoundCauseOutsideTarget || check != CheckEmptyAfterTarget {
		t.Fatalf("cause %s check %s, want OUTSIDE_TARGET on EMPTY_AFTER_TARGET", cause, check)
	}
	tracker.Observe(context.Background(), emptyRound("qg-target", at.at, false))
	at.at = at.at.Add(time.Minute)
	if cause, check := emptyRunCheck(t, tracker, "qg-target"); cause != EmptyEveryRoundCauseUnknown || check != CheckEmptyEveryRound {
		t.Fatalf("after a round the target did not empty: cause %s check %s, want CAUSE_UNKNOWN on EMPTY_EVERY_ROUND", cause, check)
	}
	tracker.Observe(context.Background(), emptyRound("qg-target", at.at, true))
	if cause, check := emptyRunCheck(t, tracker, "qg-target"); cause != EmptyEveryRoundCauseOutsideTarget || check != CheckEmptyAfterTarget {
		t.Fatalf("after the target emptied it again: cause %s check %s, want OUTSIDE_TARGET on EMPTY_AFTER_TARGET", cause, check)
	}
}

// The new line is the strategy's: it names the target, not the data.
func TestTheEmptyAfterTargetLineIsTheStrategysToEditAndNotDataAbsent(t *testing.T) {
	words, answered := checkWords[CheckEmptyAfterTarget]
	if !answered || words.Action != ActionStrategyEdit || words.State == StateDataAbsent {
		t.Fatalf("EMPTY_AFTER_TARGET reads %+v, want STRATEGY_EDIT and a state other than DATA_ABSENT: the data arrived", words)
	}
	if answer := checkAnswers[CheckEmptyAfterTarget]; answer.Owner != OwnerStrategy {
		t.Fatalf("EMPTY_AFTER_TARGET is owned by %v, want the strategy: the target is the strategy's", answer.Owner)
	}
}

// A primary that was not empty carries no such claim, whatever it said.
func TestOnlyAnEmptyPrimaryCanBeEmptiedByTheTarget(t *testing.T) {
	for _, state := range []string{"DATA", "EMPTY"} {
		observed := observability.NormalizeObservation(observability.Observation{
			PrimaryInput: &observability.PrimaryInputFacts{Completeness: "FULL", DataState: state, EmptiedByTarget: true}})
		if got := observed.PrimaryInput.EmptiedByTarget; got != (state == "EMPTY") {
			t.Fatalf("data state %s: emptied by the target = %v", state, got)
		}
	}
}

// The number beside EMPTY_EVERY_ROUND counts the rows under that line: a run
// the target emptied is on EMPTY_AFTER_TARGET's line and not in it.
func TestTheEmptyEveryRoundTotalLeavesOutTheRunsTheTargetEmptied(t *testing.T) {
	row := func(queryGroup, cause string) Anomaly {
		return Anomaly{QueryGroup: queryGroup, Kind: KindEmptyEveryRound, EmptyEveryRound: &EmptyEveryRoundFacts{Cause: cause}}
	}
	rows := []Anomaly{row("qg-unknown", EmptyEveryRoundCauseUnknown), row("qg-target", EmptyEveryRoundCauseOutsideTarget),
		row("qg-unknown", EmptyEveryRoundCauseUnknown), {QueryGroup: "qg-stopped", Kind: KindNoData}}
	if got := countEmptyEveryRound(rows); got != 1 {
		t.Fatalf("empty_every_round_total = %d, want 1: only qg-unknown is on that line", got)
	}
}
