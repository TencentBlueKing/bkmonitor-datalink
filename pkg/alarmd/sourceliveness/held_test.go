// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sourceliveness_test

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sourceliveness"
)

// The bound is four no-data spans, an hour at least: the numbers the design
// note's §4b writes down.
func TestTheHeldBoundIsFourSpansAndAnHourAtLeast(t *testing.T) {
	for _, test := range []struct {
		span int64
		want time.Duration
	}{
		{180, time.Hour},        // 1 min, R = T = 1
		{900, time.Hour},        // 5 min, R = T = 1
		{1800, 2 * time.Hour},   // 10 min, R = T = 1
		{10800, 12 * time.Hour}, // 1 h, R = T = 1
		{0, sourceliveness.HeldBoundFloor},
	} {
		if got := sourceliveness.HeldBound(test.span); got != test.want {
			t.Errorf("span %d s: bound %v, want %v", test.span, got, test.want)
		}
	}
}

// A Level is held from its first unknown verdict: the account names it, and
// past its bound - not within it - counts it past. The first held time stays
// as the verdicts repeat. An alive verdict lets it go, and the account says
// so once, then nothing. A Level not asked about for two spans, ten minutes
// at least, is let go too.
func TestTheHeldAccountRaisesPastTheBoundOnly(t *testing.T) {
	now := time.Unix(1_700_124_000, 0)
	at := now
	querier := &scriptedQuerier{answer: sourceliveness.Answer{Status: "SPACE_IS_NOT_EXISTS"}}
	checker, err := sourceliveness.NewChecker(context.Background(), querier, nil)
	if err != nil {
		t.Fatal(err)
	}
	checker.WithClock(func() time.Time { return at })
	facts := timeSeriesFacts("system.cpu_summary")
	held := subject(1) // a 180 s span: the hour's floor.
	plan := held.Plan
	if _, say := checker.Held(plan); say {
		t.Fatal("an account before any verdict")
	}
	checker.Judge(held, facts, 1_700_124_000)
	checker.Wait()
	for minute := 1; minute <= 59; minute++ {
		at = now.Add(time.Duration(minute) * time.Minute)
		checker.Judge(held, facts, 1_700_124_000+int64(minute)*60)
		checker.Wait()
	}
	report, say := checker.Held(plan)
	if !say || report.Levels != 1 || report.PastBound != 0 || !report.FirstHeld.Equal(now) || report.TableID != "system.cpu_summary" ||
		report.Kind != sourceliveness.KindTimeSeries || report.Last.Result != sourceliveness.ResultUnknown || report.Bound != time.Hour {
		t.Fatalf("within the bound: %+v %t, want one held since the first verdict, none past", report, say)
	}
	at = now.Add(time.Hour)
	checker.Judge(held, facts, 1_700_127_600)
	checker.Wait()
	if report, _ := checker.Held(plan); report.PastBound != 1 || report.Last.Reason != sourceliveness.ReasonStatus {
		t.Fatalf("at the bound: %+v, want past it, last unknown by status", report)
	}

	querier.mu.Lock()
	querier.answer = sourceliveness.Answer{Points: 1}
	querier.mu.Unlock()
	checker.Ahead(facts, lastReal, 1_700_127_660, 180)
	checker.Wait()
	at = now.Add(time.Hour + 2*time.Minute)
	checker.Judge(held, facts, 1_700_127_720)
	checker.Wait()
	if report, say := checker.Held(plan); !say || report.Levels != 0 {
		t.Fatalf("after alive: %+v %t, want the account once, with none held", report, say)
	}
	if _, say := checker.Held(plan); say {
		t.Fatal("the account repeats with nothing held")
	}

	querier.mu.Lock()
	querier.answer = sourceliveness.Answer{}
	querier.mu.Unlock()
	other := subject(2)
	checker.Judge(other, timeSeriesFacts("system.mem"), 1_700_127_780)
	checker.Wait()
	if report, _ := checker.Held(plan); report.Levels != 1 || report.TableID != "system.mem" {
		t.Fatalf("another Level held: %+v, want it named", report)
	}
	at = at.Add(11 * time.Minute)
	if report, _ := checker.Held(plan); report.Levels != 0 {
		t.Fatalf("a Level not asked about for ten minutes: %+v, want it let go", report)
	}
}

// Past the held Levels' bound a newly held Level is remembered: the one
// asked about least recently makes room, counted.
func TestANewHeldLevelIsRememberedPastTheBound(t *testing.T) {
	at := time.Unix(1_700_124_000, 0)
	observer := &countingObserver{}
	checker, err := sourceliveness.NewChecker(context.Background(), &scriptedQuerier{answer: sourceliveness.Answer{}}, observer)
	if err != nil {
		t.Fatal(err)
	}
	checker.WithClock(func() time.Time { return at }).WithMaxHeld(2)
	facts := timeSeriesFacts("system.cpu_summary")
	for level := uint32(1); level <= 3; level++ {
		at = at.Add(time.Minute)
		checker.Judge(subject(level), facts, 1_700_124_000)
		checker.Wait()
	}
	report, _ := checker.Held(subject(1).Plan)
	if report.Levels != 2 || observer.heldEvicted != 1 {
		t.Fatalf("held %+v, evicted %d: want the two asked last, one let go", report, observer.heldEvicted)
	}
	// The one let go is the first: the oldest left is the second's.
	if want := time.Unix(1_700_124_000, 0).Add(2 * time.Minute); !report.FirstHeld.Equal(want) {
		t.Fatalf("oldest held %v, want the second Level's %v", report.FirstHeld, want)
	}
}

// A held Level's query reads at most its span, ending at the judged period,
// while the window's identity stays where its span began: the answer of one
// round is found on the next however long the Level is held.
func TestAHeldLevelsQueryReadsAtMostItsSpan(t *testing.T) {
	facts := timeSeriesFacts("system.cpu_summary")
	const span = int64(180)
	for rounds := int64(0); rounds <= 30; rounds++ {
		judged := lastReal + span + rounds*60
		probe, verdict, ok := sourceliveness.Derive(facts, lastReal, judged, span)
		if !ok {
			t.Fatalf("held %d rounds: %+v", rounds, verdict)
		}
		if probe.Key.Start != lastReal+120 || probe.Key.End != judged+60 {
			t.Fatalf("held %d rounds: identity [%d, %d), want from two periods after the last real point", rounds, probe.Key.Start, probe.Key.End)
		}
		if read := probe.Key.End - probe.QueryStart; read > span || probe.QueryStart < probe.Key.Start {
			t.Fatalf("held %d rounds: query [%d, %d) reads %d s, want at most the %d s span and never before %d",
				rounds, probe.QueryStart, probe.Key.End, read, span, probe.Key.Start)
		}
	}
	querier := &scriptedQuerier{answer: sourceliveness.Answer{Points: 1}}
	checker := newChecker(t, querier)
	held := subject(1)
	held.NoDataSpanSeconds = span
	checker.Judge(held, facts, lastReal+span+20*60)
	checker.Wait()
	if verdict := checker.Judge(held, facts, lastReal+span+21*60); verdict.Result != sourceliveness.ResultAlive {
		t.Fatalf("twenty rounds held: %+v, want the round before's answer found", verdict)
	}
	checker.Wait()
}
