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
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sourceliveness"
)

// scriptedQuerier answers every probe with answer, after release is closed
// when it is set, and counts the probes by key.
type scriptedQuerier struct {
	mu      sync.Mutex
	answer  sourceliveness.Answer
	release chan struct{}
	asked   map[sourceliveness.Key]int
	// waitDeadline answers only when the probe's own context ends.
	waitDeadline bool
}

func (querier *scriptedQuerier) ProbeSourceLiveness(ctx context.Context, probe sourceliveness.Probe) sourceliveness.Answer {
	querier.mu.Lock()
	if querier.asked == nil {
		querier.asked = map[sourceliveness.Key]int{}
	}
	querier.asked[probe.Key]++
	release, answer, waitDeadline := querier.release, querier.answer, querier.waitDeadline
	querier.mu.Unlock()
	if waitDeadline {
		<-ctx.Done()
		return sourceliveness.Answer{Err: ctx.Err()}
	}
	if release != nil {
		<-release
	}
	return answer
}

func (querier *scriptedQuerier) probes() int {
	querier.mu.Lock()
	defer querier.mu.Unlock()
	total := 0
	for _, count := range querier.asked {
		total += count
	}
	return total
}

// timeSeriesFacts is a time series query's facts as the compiler shapes
// them, on table.
func timeSeriesFacts(table string) execution.QueryPlanFacts {
	return execution.QueryPlanFacts{TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2", StepMillis: 60_000,
		QueryList: []execution.QueryClause{{DataSource: "bkmonitor", Driver: "influxdb", TableID: table, FieldName: "usage",
			TimeField: "time", ReferenceName: "a"}}}
}

// lastReal is the subjects' last real point: three periods before the
// period most cases judge, so their no-data span begins after it.
const lastReal = 1_700_123_820

func subject(level uint32) sourceliveness.Subject {
	return sourceliveness.Subject{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "14"},
		LevelID: level, Series: "series", NoDataSpanSeconds: 180, LastRealPoint: lastReal}
}

func newChecker(t *testing.T, querier sourceliveness.Querier) *sourceliveness.Checker {
	t.Helper()
	checker, err := sourceliveness.NewChecker(context.Background(), querier, nil)
	if err != nil {
		t.Fatal(err)
	}
	return checker
}

// A probe that does not come back does not hold the round: the verdict is
// unknown by timeout (the Level stays open) at once, and the answer, when it comes,
// serves a later round. Were the probe run inside the verdict, this would
// wait on the probe and fail on the deadline below.
func TestASlowProbeDoesNotHoldTheRound(t *testing.T) {
	querier := &scriptedQuerier{answer: sourceliveness.Answer{Points: 1}, release: make(chan struct{})}
	checker := newChecker(t, querier)
	facts := timeSeriesFacts("system.cpu_summary")
	decided := make(chan sourceliveness.Verdict, 1)
	go func() { decided <- checker.Judge(subject(1), facts, 1_700_124_000) }()
	select {
	case verdict := <-decided:
		if verdict.Result != sourceliveness.ResultUnknown || verdict.Reason != sourceliveness.ReasonTimeout {
			t.Fatalf("verdict %+v while the probe runs, want unknown by timeout", verdict)
		}
	case <-time.After(5 * time.Second): // fixed-wait: an in-process verdict returns in microseconds; one that waits on the held probe never returns before release
		close(querier.release)
		t.Fatal("the verdict waited on the probe")
	}
	close(querier.release)
	checker.Wait()
	if verdict := checker.Judge(subject(1), facts, 1_700_124_060); verdict.Result != sourceliveness.ResultAlive {
		t.Fatalf("the round after, verdict %+v, want the probe's alive", verdict)
	}
}

// One probe per source and period, whichever Levels, series or Plans ask;
// a verdict serves its own period and the next, not one further. A probe
// started ahead serves the round its end is due.
func TestOneProbePerSourceAndPeriod(t *testing.T) {
	querier := &scriptedQuerier{answer: sourceliveness.Answer{Points: 2}}
	checker := newChecker(t, querier)
	facts := timeSeriesFacts("system.cpu_summary")
	checker.Ahead(facts, lastReal, 1_700_124_000, 180)
	checker.Ahead(facts, lastReal, 1_700_124_000, 180)
	checker.Wait()
	if probes := querier.probes(); probes != 1 {
		t.Fatalf("probes ahead %d, want one", probes)
	}
	// Due the round after: the verdict of the period before serves it.
	for level := uint32(1); level <= 3; level++ {
		if verdict := checker.Judge(subject(level), facts, 1_700_124_060); verdict.Result != sourceliveness.ResultAlive {
			t.Fatalf("level %d verdict %+v, want alive from the probe ahead", level, verdict)
		}
	}
	checker.Wait()
	if probes := querier.probes(); probes != 2 {
		t.Fatalf("probes %d, want the one ahead and one of the due period", probes)
	}
	// Two periods on without a newer answer: not served.
	querier.mu.Lock()
	querier.release = make(chan struct{})
	querier.mu.Unlock()
	if verdict := checker.Judge(subject(1), facts, 1_700_124_180); verdict.Reason != sourceliveness.ReasonTimeout {
		t.Fatalf("two periods after the newest answer, verdict %+v, want unknown by timeout", verdict)
	}
	close(querier.release)
	checker.Wait()
	// Another table is another source.
	if verdict := checker.Judge(subject(1), timeSeriesFacts("system.mem"), 1_700_124_180); verdict.Reason != sourceliveness.ReasonTimeout {
		t.Fatalf("another table, verdict %+v, want unknown by timeout on its own probe", verdict)
	}
	checker.Wait()
}

// A probe the query service does not answer within the probe's own bound
// is unknown, by timeout.
func TestAProbePastItsBoundIsUnknownByTimeout(t *testing.T) {
	querier := &scriptedQuerier{waitDeadline: true}
	checker := newChecker(t, querier).WithProbeTimeout(10 * time.Millisecond)
	facts := timeSeriesFacts("system.cpu_summary")
	checker.Ahead(facts, lastReal, 1_700_124_000, 180)
	checker.Wait()
	verdict := checker.Judge(subject(1), facts, 1_700_124_060)
	if verdict.Result != sourceliveness.ResultUnknown || verdict.Reason != sourceliveness.ReasonTimeout || verdict.Ends() {
		t.Fatalf("verdict %+v, want unknown by timeout, which ends nothing", verdict)
	}
	checker.Wait()
}

// An unsupported source is refused by name without a probe, and ends as it
// did; facts that are not there are unknown, and end nothing.
func TestAnUnsupportedSourceEndsWithoutAProbe(t *testing.T) {
	querier := &scriptedQuerier{answer: sourceliveness.Answer{Points: 1}}
	checker := newChecker(t, querier)
	promql := execution.QueryPlanFacts{TenantID: "tenant", SpaceScope: "bkcc__2", StepMillis: 60_000, PromQL: &execution.PromQLQuery{Expression: "up"}}
	verdict := checker.Judge(subject(1), promql, 1_700_124_000)
	if verdict.Result != sourceliveness.ResultUnsupported || verdict.Reason != sourceliveness.ReasonPromQL || !verdict.Ends() {
		t.Fatalf("verdict %+v, want unsupported promql, which ends", verdict)
	}
	if verdict := checker.Judge(subject(1), execution.QueryPlanFacts{}, 1_700_124_000); verdict.Ends() || verdict.Result != sourceliveness.ResultUnknown {
		t.Fatalf("no facts: verdict %+v, want unknown", verdict)
	}
	checker.Wait()
	if probes := querier.probes(); probes != 0 {
		t.Fatalf("probes %d, want none", probes)
	}
}

// countingObserver counts what the Checker tells it.
type countingObserver struct {
	mu                        sync.Mutex
	checked, probed, deferred int
	evicted, heldEvicted      int
	verdicts                  []sourceliveness.Verdict
}

func (observer *countingObserver) SourceLivenessChecked(_ sourceliveness.Kind, verdict sourceliveness.Verdict) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.checked++
	observer.verdicts = append(observer.verdicts, verdict)
}

func (observer *countingObserver) SourceLivenessProbed(sourceliveness.Kind, time.Duration) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.probed++
}

func (observer *countingObserver) SourceLivenessHeldEvicted() {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.heldEvicted++
}

func (observer *countingObserver) SourceLivenessEvicted() {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.evicted++
}

func (observer *countingObserver) SourceLivenessDeferred(sourceliveness.Kind) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.deferred++
}

// A probe the in-flight cap does not let start is deferred, counted, and its
// round reads timeout; once a slot frees, the next round starts it.
func TestAProbePastTheInFlightCapIsDeferredAndCounted(t *testing.T) {
	querier := &scriptedQuerier{answer: sourceliveness.Answer{Points: 1}, release: make(chan struct{})}
	observer := &countingObserver{}
	checker, err := sourceliveness.NewChecker(context.Background(), querier, observer)
	if err != nil {
		t.Fatal(err)
	}
	checker.WithMaxInFlight(1)
	checker.Ahead(timeSeriesFacts("system.cpu_summary"), lastReal, 1_700_124_000, 180)
	verdict := checker.Judge(subject(1), timeSeriesFacts("system.mem"), 1_700_124_000)
	if verdict.Result != sourceliveness.ResultUnknown || verdict.Reason != sourceliveness.ReasonTimeout || observer.deferred != 1 {
		t.Fatalf("verdict %+v, deferred %d: want timeout, one deferred", verdict, observer.deferred)
	}
	close(querier.release)
	checker.Wait()
	checker.Judge(subject(1), timeSeriesFacts("system.mem"), 1_700_124_000)
	checker.Wait()
	if probes := querier.probes(); probes != 2 || observer.deferred != 1 {
		t.Fatalf("probes %d, deferred %d: want the deferred source probed once the slot freed", probes, observer.deferred)
	}
}

// Past the answers' bound a new source's answer is kept and readable: the
// oldest answer makes room, counted, and its source is probed again when
// next asked.
func TestANewSourcesAnswerIsKeptPastTheBound(t *testing.T) {
	querier := &scriptedQuerier{answer: sourceliveness.Answer{Points: 1}}
	observer := &countingObserver{}
	checker, err := sourceliveness.NewChecker(context.Background(), querier, observer)
	if err != nil {
		t.Fatal(err)
	}
	checker.WithMaxAnswers(2)
	for index, table := range []string{"system.a", "system.b", "system.c"} {
		// Each a period later than the one before: system.a's ends first.
		checker.Ahead(timeSeriesFacts(table), lastReal, 1_700_124_000+int64(index)*60, 180)
		checker.Wait()
	}
	if verdict := checker.Judge(subject(1), timeSeriesFacts("system.c"), 1_700_124_180); verdict.Result != sourceliveness.ResultAlive {
		t.Fatalf("the newest source past the bound: %+v, want its answer, alive", verdict)
	}
	if observer.evicted != 1 {
		t.Fatalf("evicted %d, want one", observer.evicted)
	}
	if verdict := checker.Judge(subject(1), timeSeriesFacts("system.a"), 1_700_124_060); verdict.Reason != sourceliveness.ReasonTimeout {
		t.Fatalf("the evicted source: %+v, want asked again (timeout this round)", verdict)
	}
	checker.Wait()
}
