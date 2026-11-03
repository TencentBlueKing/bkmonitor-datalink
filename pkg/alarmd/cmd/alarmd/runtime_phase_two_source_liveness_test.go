// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sourceliveness"
)

// waitSourceProbes waits for the source liveness probes the bundle's last
// round started, so the next round reads their answers. In production a
// probe that has not come back keeps its Level open for the round; a case
// that means to read an answer waits for it.
func waitSourceProbes(bundle *phaseTwoWorkerBundle) {
	if bundle == nil {
		return
	}
	if checker, ok := bundle.workerPorts.SourceLiveness.(*sourceliveness.Checker); ok {
		checker.Wait()
	}
}

// isSourceProbe is whether a query is a source liveness probe: one clause
// counted over windows not aligned to a grid, nothing grouped.
func isSourceProbe(body []byte) bool {
	return bytes.Contains(body, []byte(`"not_time_align":true`)) &&
		bytes.Contains(body, []byte(`"time_aggregation":{"function":"count_over_time"`)) &&
		!bytes.Contains(body, []byte(`"dimensions":`))
}

// The probe shapes the query service answers, and the one it does not
// answer in time.
const (
	probeAlive       = `{"series":[{"name":"_result0","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1700000000000,24]]}],"status":null,"is_partial":false}`
	probeStatus      = `{"series":[],"status":{"code":"SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS","message":"table"},"is_partial":false}`
	probeEmpty       = `{"series":[],"status":null,"is_partial":false}`
	probeNoAnswerYet = "hang"
)

// The core claim, through the production bundle: a time series written to
// end on no data (recovery-nodata), N = R = 1, hosts A and B over 80, both
// alerts open on round 1; B then stops reporting and is due to end on round
// 4, (R + T + 1) periods after its last point. Before it ends, its source -
// system.cpu, no condition - is probed. Answered with no series and a status,
// with no series and no status, or not answered in the probe's time, the
// source is unknown: B's alert stays open round after round, and each held
// end is counted. Answered with counts, B ends on the next round. Host A,
// reporting, is never ended.
func TestANoDataEndWaitsForItsSourceToBeShownAlive(t *testing.T) {
	for _, unknown := range []string{probeStatus, probeEmpty, probeNoAnswerYet} {
		strategy := lifecycleStrategy{revision: 7, noDataOff: true, statusSetter: "recovery-nodata"}
		fixture := startLifecycleFixture(t, strategy, nil)
		checker, ok := fixture.bundle.workerPorts.SourceLiveness.(*sourceliveness.Checker)
		if !ok {
			t.Fatalf("the worker asks %T, want the production checker", fixture.bundle.workerPorts.SourceLiveness)
		}
		checker.WithProbeTimeout(100 * time.Millisecond)
		answer := unknown
		probes := 0
		fixture.interceptQueries(func(writer http.ResponseWriter, request *http.Request) bool {
			body, _ := io.ReadAll(request.Body)
			request.Body = io.NopCloser(bytes.NewReader(body))
			if !isSourceProbe(body) {
				return false
			}
			if strings.Contains(string(body), `"dimensions"`) || strings.Contains(string(body), `"field_list"`) {
				t.Errorf("a probe with the strategy's grouping or conditions: %s", body)
			}
			fixture.mu.Lock()
			probes++
			current := answer
			fixture.mu.Unlock()
			if current == probeNoAnswerYet {
				<-request.Context().Done()
				return true
			}
			_, _ = writer.Write([]byte(current))
			return true
		})
		fixture.serve(false, 90, lifecycleHostA, lifecycleHostB)
		fixture.mustAttempt(1)
		openedA, openedB := fixture.thresholdEventsFor(lifecycleHostA), fixture.thresholdEventsFor(lifecycleHostB)
		if len(openedA) != 1 || len(openedB) != 1 {
			t.Fatalf("%s: round 1 sent A %+v and B %+v, want one ABNORMAL each", unknown, openedA, openedB)
		}
		fixture.publishOpenAlert(openedA[0].DedupeMD5)
		fixture.publishOpenAlert(openedB[0].DedupeMD5)
		fixture.serve(false, 90, lifecycleHostA)
		for round := int64(2); round <= 7; round++ {
			fixture.mustAttempt(round)
			if got := recoveriesOf(fixture.thresholdEventsFor(lifecycleHostB)); len(got) != 0 {
				t.Fatalf("%s: round %d ended B %+v while its source was unknown", unknown, round, got)
			}
		}
		waitSourceProbes(fixture.bundle)
		fixture.mu.Lock()
		asked := probes
		fixture.mu.Unlock()
		if asked == 0 {
			t.Fatalf("%s: no probe was asked", unknown)
		}
		if held := fixture.noDataEndOutcomes()[observability.NoDataEndSourceUnknown]; held < 3 {
			t.Fatalf("%s: held ends counted %d, want one each round from round 4", unknown, held)
		}

		fixture.mu.Lock()
		answer = probeAlive
		fixture.mu.Unlock()
		ended := 0
		for round := int64(8); round <= 10 && ended == 0; round++ {
			fixture.mustAttempt(round)
			ended = len(recoveriesOf(fixture.thresholdEventsFor(lifecycleHostB)))
		}
		ends := recoveriesOf(fixture.thresholdEventsFor(lifecycleHostB))
		if len(ends) != 1 || !ends[0].RecordRef.Absent || ends[0].DedupeMD5 != openedB[0].DedupeMD5 ||
			ends[0].LevelResults[0].DecisionWindow.Recovery.EndedBy != contract.RecoveryEndedByNoData {
			t.Fatalf("%s: B's ends once its source answered = %+v, want one absent RECOVERY ended by no data", unknown, ends)
		}
		if got := recoveriesOf(fixture.thresholdEventsFor(lifecycleHostA)); len(got) != 0 {
			t.Fatalf("%s: A, reporting over the threshold, was ended: %+v", unknown, got)
		}
		fixture.interceptQueries(nil)
		waitSourceProbes(fixture.bundle)
	}
}

// The same claim for a present series: a log count that groups by nothing,
// whose zero is no observation, keeps its one series in the answer with a
// zero every round, and is due to end on round 6, (R + T + 1) periods after
// its one document. Its source answering nothing, it does not end: rounds 6
// and 7 are NORMAL, withheld because the source is unknown, and nothing
// ends the consumer's alert. Answered with counts, round 6 ends it, by no
// data.
func TestAPresentSeriesNoDataEndWaitsForItsSource(t *testing.T) {
	held := runGroupedEventCount(t, eventSpanCase{rounds: 8, native: true, statusSetter: "recovery-nodata", logCount: true, ungrouped: true})
	if held.probes == 0 {
		t.Fatal("no source probe was asked")
	}
	for round := 6; round <= 7; round++ {
		if !slices.Contains(held.outcomes[round], "NORMAL:"+contract.RecoveryWithheldSourceUnknown) {
			t.Fatalf("round %d outcomes %v, want NORMAL withheld as source_unknown", round, held.outcomes[round])
		}
		if containsKind(held.sent[round], contract.TriggerEventRecovery) {
			t.Fatalf("round %d decided %v, want no RECOVERY while the source is unknown", round, held.sent[round])
		}
	}
	for _, event := range held.written {
		if event.EventKind == contract.TriggerEventRecovery {
			t.Fatalf("the sink took a RECOVERY %+v while the source was unknown", event)
		}
	}
	if got := sourceLivenessChecks(t, held.recorder, "log", "unknown", "empty_without_status"); got < 2 {
		t.Fatalf("log unknown empty_without_status counted %v, want one each held round", got)
	}

	alive := runGroupedEventCount(t, eventSpanCase{rounds: 8, native: true, statusSetter: "recovery-nodata", logCount: true, ungrouped: true,
		sourceAlive: true})
	if rows := sourceHeldRows(held.tracker); len(rows) != 0 {
		t.Fatalf("within the bound: %+v, want no SOURCE_UNKNOWN_HELD line", rows)
	}

	// Held past the bound: the checker's clock an hour on from round 7, and
	// the present series' Level named on the fleet like an absent one's -
	// with the consumer's protocol, whose Plans keep a no-data end index,
	// and without it, whose Plans keep none: the account is the process's
	// own, not the index's.
	for _, native := range []bool{true, false} {
		past := runGroupedEventCount(t, eventSpanCase{rounds: 8, native: native, statusSetter: "recovery-nodata", logCount: true, ungrouped: true,
			beforeRound: func(round int, bundle *phaseTwoWorkerBundle) {
				if round != 7 {
					return
				}
				checker := bundle.workerPorts.SourceLiveness.(*sourceliveness.Checker)
				checker.WithClock(func() time.Time { return time.Now().Add(sourceliveness.HeldBoundFloor + time.Minute) })
			}})
		if got := sourceLivenessChecks(t, past.recorder, "log", "unknown", "empty_without_status"); got < 2 {
			t.Fatalf("native %t: held verdicts %v, want one each round from round 6", native, got)
		}
		rows := sourceHeldRows(past.tracker)
		if len(rows) != 1 || rows[0].SourceUnknownHeld == nil || rows[0].SourceUnknownHeld.TableID != "bklog_index_set_7" ||
			rows[0].SourceUnknownHeld.SourceKind != string(sourceliveness.KindLog) || len(rows[0].Strategies) != 1 ||
			rows[0].Strategies[0].StrategyID != eventSpanStrategyID || rows[0].Finding.Owner != fleet.OwnerUndetermined {
			t.Fatalf("native %t past the bound: %+v, want the log count's strategy on one UNDETERMINED SOURCE_UNKNOWN_HELD line", native, rows)
		}
	}
	if !containsKind(alive.sent[6], contract.TriggerEventRecovery) {
		t.Fatalf("alive: round 6 decided %v, want the RECOVERY no data ended (all %v)", alive.sent[6], alive.sent)
	}
	if got := sourceLivenessChecks(t, alive.recorder, "log", "alive", ""); got < 1 {
		t.Fatalf("log alive counted %v, want the verdict it ended on", got)
	}
}

// An event source is not probed in this version: a whole event table quiet
// for two periods answers as a broken one does. Its no-data end goes ahead
// as configured - round 6, as before the probe existed
// (TestAGroupedEventCountWrittenToEndOnNoDataEndsOnItsZeros) - counted as
// unsupported, event_sparse, and no probe is sent.
func TestAnEventSourcesNoDataEndEndsAsConfiguredUnprobed(t *testing.T) {
	run := runGroupedEventCount(t, eventSpanCase{rounds: 8, native: true, statusSetter: "recovery-nodata"})
	if !containsKind(run.sent[6], contract.TriggerEventRecovery) {
		t.Fatalf("round 6 decided %v, want the RECOVERY no data ended (all %v)", run.sent[6], run.sent)
	}
	if run.probes != 0 {
		t.Fatalf("%d probes of an event source, want none", run.probes)
	}
	if got := sourceLivenessChecks(t, run.recorder, "event", "unsupported", "event_sparse"); got < 1 {
		t.Fatalf("event unsupported event_sparse counted %v, want the end it let go", got)
	}
}

// sourceLivenessChecks is one cell of alarmd_source_liveness_checks_total.
func sourceLivenessChecks(t *testing.T, recorder *metric.Recorder, kind, result, reason string) float64 {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "alarmd_source_liveness_checks_total" {
			continue
		}
		for _, m := range family.Metric {
			labels := map[string]string{}
			for _, label := range m.Label {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["source_kind"] == kind && labels["result"] == result && labels["reason"] == reason {
				return m.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("no cell %s %s %s", kind, result, reason)
	return 0
}

// noDataEndOutcomes sums the no-data end index's outcomes observed so far.
func (fixture *lifecycleFixture) noDataEndOutcomes() map[string]int64 {
	fixture.observationsMu.Lock()
	defer fixture.observationsMu.Unlock()
	outcomes := map[string]int64{}
	for _, observation := range fixture.observations {
		if observation.Stage == observability.StageNoDataEnd {
			outcomes[observation.NoDataEnd] += observation.Counts.Keys
		}
	}
	return outcomes
}

// A probe that does not come back does not hold the round. The probe's own
// bound is an hour here and the query service never answers it: the rounds
// from B's due one complete on their own time with B's alert open (unknown
// by timeout), and the probe is still running. Were the probe run
// inside the round, the round would wait on it and fail the deadline below.
func TestASlowSourceProbeDoesNotHoldTheRound(t *testing.T) {
	strategy := lifecycleStrategy{revision: 7, noDataOff: true, statusSetter: "recovery-nodata"}
	fixture := startLifecycleFixture(t, strategy, nil)
	checker, ok := fixture.bundle.workerPorts.SourceLiveness.(*sourceliveness.Checker)
	if !ok {
		t.Fatalf("the worker asks %T, want the production checker", fixture.bundle.workerPorts.SourceLiveness)
	}
	checker.WithProbeTimeout(time.Hour)
	release := make(chan struct{})
	fixture.interceptQueries(func(writer http.ResponseWriter, request *http.Request) bool {
		body, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(body))
		if !isSourceProbe(body) {
			return false
		}
		select {
		case <-release:
		case <-request.Context().Done():
		}
		return true
	})
	fixture.serve(false, 90, lifecycleHostA, lifecycleHostB)
	fixture.mustAttempt(1)
	openedB := fixture.thresholdEventsFor(lifecycleHostB)
	if len(openedB) != 1 {
		t.Fatalf("round 1 sent B %+v, want one ABNORMAL", openedB)
	}
	fixture.publishOpenAlert(fixture.thresholdEventsFor(lifecycleHostA)[0].DedupeMD5)
	fixture.publishOpenAlert(openedB[0].DedupeMD5)
	fixture.serve(false, 90, lifecycleHostA)
	fixture.mustAttempt(2)
	// From here no round waits for a probe: B's end comes due, its probe
	// starts (ahead or on the due round) and hangs, and every round still
	// completes on its own time with B open.
	for round := int64(3); round <= 6; round++ {
		fixture.clock.Store(roundReadAtMillis(fixture.cfg, fixture.queryGroup, fixture.evaluationAt(round), fixture.interval))
		done := make(chan error, 1)
		go func() {
			_, attempted, err := fixture.runner.RunOne(context.Background())
			if err == nil && !attempted {
				err = errors.New("not attempted")
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("round %d: %v", round, err)
			}
		case <-time.After(30 * time.Second): // fixed-wait: the round's own deadline is the claim; a round that waits on the hung probe (bounded at an hour) misses it
			close(release)
			t.Fatalf("round %d waited on the source probe", round)
		}
		if got := recoveriesOf(fixture.thresholdEventsFor(lifecycleHostB)); len(got) != 0 {
			t.Fatalf("round %d ended B %+v with its probe unanswered", round, got)
		}
	}
	if held := fixture.noDataEndOutcomes()[observability.NoDataEndSourceUnknown]; held < 2 {
		t.Fatalf("held ends counted %d, want B's on every round from its due one (outcomes %v)", held, fixture.noDataEndOutcomes())
	}
	close(release)
	checker.Wait()
	fixture.interceptQueries(nil)
}

// The probe reads the source where the round read it, over the Level's
// no-data span and nothing before it: ending where the round's primary read
// ends - the evaluation time less time_delay, never the Slot's time - and
// starting two periods after the series' last real point, one after its
// first empty period, or later where that would read more than the span
// (sourceliveness.Derive says why). A present series' end
// (the evaluator, on an ungrouped log count) and an absent one's (the
// worker, on a time series) both. time_delay is 120 s, two periods.
func TestTheProbeReadsAtTheStrategysOwnQueryOffset(t *testing.T) {
	const delay, period = int64(120), int64(60)
	run := runGroupedEventCount(t, eventSpanCase{rounds: 8, native: true, statusSetter: "recovery-nodata", timeDelay: delay,
		logCount: true, ungrouped: true})
	if !slices.Contains(run.outcomes[6], "NORMAL:"+contract.RecoveryWithheldSourceUnknown) {
		t.Fatalf("round 6 outcomes %v, want the present series' end held on its source", run.outcomes[6])
	}
	base := controlledG4Base(t)
	if len(run.probeWindows) == 0 {
		t.Fatal("no probe")
	}
	// The one document is in round 0's period, at the query offset.
	lastReal := base - period - delay
	// N = 2, R = 3: a span of six periods, which the query reads at most.
	presentSpan := 6 * period
	for _, window := range run.probeWindows {
		evaluation := base + window[0]*period
		if want := max(lastReal+2*period, evaluation-delay-presentSpan); window[2] != evaluation-delay || window[1] != want {
			t.Fatalf("round %d probed [%d, %d), want [%d, %d): from after the first empty period, at most the span, to the query offset",
				window[0], window[1], window[2], want, evaluation-delay)
		}
	}

	strategy := lifecycleStrategy{revision: 7, noDataOff: true, statusSetter: "recovery-nodata",
		edit: func(item map[string]any) { item["time_delay"] = delay }}
	fixture := startLifecycleFixture(t, strategy, nil)
	var windows [][3]int64
	current := int64(0)
	fixture.interceptQueries(func(writer http.ResponseWriter, request *http.Request) bool {
		body, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(body))
		if !isSourceProbe(body) {
			return false
		}
		var payload struct {
			Start string `json:"start_time"`
			End   string `json:"end_time"`
		}
		_ = json.Unmarshal(body, &payload)
		start, _ := strconv.ParseInt(payload.Start, 10, 64)
		end, _ := strconv.ParseInt(payload.End, 10, 64)
		fixture.mu.Lock()
		windows = append(windows, [3]int64{current, start, end})
		fixture.mu.Unlock()
		_, _ = writer.Write([]byte(probeStatus))
		return true
	})
	fixture.serve(false, 90, lifecycleHostA, lifecycleHostB)
	fixture.mustAttempt(1)
	opened := fixture.thresholdEventsFor(lifecycleHostB)
	if len(opened) != 1 {
		t.Fatalf("round 1 sent B %+v, want one ABNORMAL", opened)
	}
	fixture.publishOpenAlert(fixture.thresholdEventsFor(lifecycleHostA)[0].DedupeMD5)
	fixture.publishOpenAlert(opened[0].DedupeMD5)
	fixture.serve(false, 90, lifecycleHostA)
	for round := int64(2); round <= 6; round++ {
		// The last round's probes answer under the round that started them.
		waitSourceProbes(fixture.bundle)
		fixture.mu.Lock()
		current = round
		fixture.mu.Unlock()
		fixture.mustAttempt(round)
	}
	waitSourceProbes(fixture.bundle)
	fixture.interceptQueries(nil)
	if held := fixture.noDataEndOutcomes()[observability.NoDataEndSourceUnknown]; held == 0 || len(windows) == 0 {
		t.Fatalf("held %d, probes %v: want the absent series' end held on its probed source", held, windows)
	}
	// B's last real point is round 1's: the query service here puts each
	// series' point at the last second of the window asked.
	lastB := fixture.evaluationAt(1) - delay - 1
	for _, window := range windows {
		evaluation := fixture.evaluationAt(window[0])
		// N = R = 1: a span of three periods.
		want := max(lastB+2*fixture.interval, evaluation-delay-3*fixture.interval)
		if window[2] != evaluation-delay || window[1] != want {
			t.Fatalf("round %d probed [%d, %d), want [%d, %d)", window[0], window[1], window[2], want, evaluation-delay)
		}
	}
}

// A Level held on an unknown source past its bound is named on the fleet,
// through the bundle and the replica's tracker: B, due to end from round 4,
// is held while its probe answers with a status. Within the bound - the
// Level's no-data span is 3 minutes, so the hour's floor - no line. With the
// checker's clock past the hour, the next round's account raises
// SOURCE_UNKNOWN_HELD, owner UNDETERMINED, naming the source kind, the
// table, when B was first held and the last outcome. Once the probe answers
// with data and B ends, the line clears.
func TestALevelHeldPastItsBoundIsNamedOnTheFleet(t *testing.T) {
	strategy := lifecycleStrategy{revision: 7, noDataOff: true, statusSetter: "recovery-nodata"}
	fixture := startLifecycleFixture(t, strategy, nil)
	checker, ok := fixture.bundle.workerPorts.SourceLiveness.(*sourceliveness.Checker)
	if !ok {
		t.Fatalf("the worker asks %T, want the production checker", fixture.bundle.workerPorts.SourceLiveness)
	}
	var ahead atomic.Int64
	checker.WithClock(func() time.Time { return time.UnixMilli(fixture.clock.Load()).Add(time.Duration(ahead.Load())) })
	answer := probeStatus
	var table string
	fixture.interceptQueries(func(writer http.ResponseWriter, request *http.Request) bool {
		body, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(body))
		if !isSourceProbe(body) {
			return false
		}
		var payload struct {
			QueryList []struct {
				TableID string `json:"table_id"`
			} `json:"query_list"`
		}
		_ = json.Unmarshal(body, &payload)
		fixture.mu.Lock()
		current := answer
		if len(payload.QueryList) == 1 {
			table = payload.QueryList[0].TableID
		}
		fixture.mu.Unlock()
		_, _ = writer.Write([]byte(current))
		return true
	})
	heldRows := func() []fleet.Anomaly {
		var rows []fleet.Anomaly
		for _, row := range fixture.tracker.NoData() {
			if row.Kind == fleet.KindSourceUnknownHeld {
				rows = append(rows, row)
			}
		}
		fleet.Attribute(rows, time.UnixMilli(fixture.clock.Load()))
		return rows
	}
	fixture.serve(false, 90, lifecycleHostA, lifecycleHostB)
	fixture.mustAttempt(1)
	opened := fixture.thresholdEventsFor(lifecycleHostB)
	if len(opened) != 1 {
		t.Fatalf("round 1 sent B %+v, want one ABNORMAL", opened)
	}
	fixture.publishOpenAlert(fixture.thresholdEventsFor(lifecycleHostA)[0].DedupeMD5)
	fixture.publishOpenAlert(opened[0].DedupeMD5)
	fixture.serve(false, 90, lifecycleHostA)
	for round := int64(2); round <= 6; round++ {
		fixture.mustAttempt(round)
	}
	if held := fixture.noDataEndOutcomes()[observability.NoDataEndSourceUnknown]; held == 0 {
		t.Fatal("B's end was never held")
	}
	if rows := heldRows(); len(rows) != 0 {
		t.Fatalf("within the bound: %+v, want no SOURCE_UNKNOWN_HELD line", rows)
	}

	ahead.Store(int64(sourceliveness.HeldBoundFloor))
	fixture.mustAttempt(7)
	rows := heldRows()
	if len(rows) != 1 {
		t.Fatalf("past the bound: %d rows, want B's strategy on one SOURCE_UNKNOWN_HELD line", len(rows))
	}
	row := rows[0]
	held := row.SourceUnknownHeld
	if held == nil || row.Finding.Check != fleet.CheckSourceUnknownHeld || row.Finding.Owner != fleet.OwnerUndetermined ||
		held.SourceKind != string(sourceliveness.KindTimeSeries) || held.TableID == "" || held.TableID != table ||
		held.FirstHeld.IsZero() || held.LastResult != string(sourceliveness.ResultUnknown) ||
		held.LastReason != string(sourceliveness.ReasonStatus) || held.BoundSeconds != int64(sourceliveness.HeldBoundFloor/time.Second) ||
		held.PastBound != 1 {
		t.Fatalf("the line = %+v held %+v, want UNDETERMINED naming the time series %q, first held, last unknown by status, past its hour", row, held, table)
	}

	fixture.mu.Lock()
	answer = probeAlive
	fixture.mu.Unlock()
	for round := int64(8); round <= 10 && len(recoveriesOf(fixture.thresholdEventsFor(lifecycleHostB))) == 0; round++ {
		fixture.mustAttempt(round)
	}
	if len(recoveriesOf(fixture.thresholdEventsFor(lifecycleHostB))) != 1 {
		t.Fatal("B was not ended once its source answered")
	}
	fixture.mustAttempt(11)
	if rows := heldRows(); len(rows) != 0 {
		t.Fatalf("after B ended: %+v, want the line cleared", rows)
	}
	fixture.interceptQueries(nil)
	waitSourceProbes(fixture.bundle)
}

// sourceHeldRows is a tracker's SOURCE_UNKNOWN_HELD rows, attributed.
func sourceHeldRows(tracker *fleet.Tracker) []fleet.Anomaly {
	var rows []fleet.Anomaly
	for _, row := range tracker.NoData() {
		if row.Kind == fleet.KindSourceUnknownHeld {
			rows = append(rows, row)
		}
	}
	fleet.Attribute(rows, time.Now())
	return rows
}

// A held Level whose no-data end index cannot be read stays on its line for
// as long as the index is unreadable: the account is this process's, and a
// round that cannot read the index is no sign the Level ended. B is held past
// its bound and named; its Plan's index key then holds a value of another
// type, which every read refuses, for 65 rounds - more than the hour after
// which the tracker drops an account it no longer hears, and far more than
// the ten minutes after which the checker lets go of a Level nobody asks
// about. The line is still up, B still open.
func TestAHeldLevelStaysNamedWhileItsIndexIsUnreadable(t *testing.T) {
	strategy := lifecycleStrategy{revision: 7, noDataOff: true, statusSetter: "recovery-nodata"}
	fixture := startLifecycleFixture(t, strategy, nil)
	checker := fixture.bundle.workerPorts.SourceLiveness.(*sourceliveness.Checker)
	var ahead atomic.Int64
	checker.WithClock(func() time.Time { return time.UnixMilli(fixture.clock.Load()).Add(time.Duration(ahead.Load())) })
	fixture.interceptQueries(func(writer http.ResponseWriter, request *http.Request) bool {
		body, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(body))
		if !isSourceProbe(body) {
			return false
		}
		_, _ = writer.Write([]byte(probeStatus))
		return true
	})
	fixture.serve(false, 90, lifecycleHostA, lifecycleHostB)
	fixture.mustAttempt(1)
	opened := fixture.thresholdEventsFor(lifecycleHostB)
	if len(opened) != 1 {
		t.Fatalf("round 1 sent B %+v, want one ABNORMAL", opened)
	}
	fixture.publishOpenAlert(fixture.thresholdEventsFor(lifecycleHostA)[0].DedupeMD5)
	fixture.publishOpenAlert(opened[0].DedupeMD5)
	fixture.serve(false, 90, lifecycleHostA)
	for round := int64(2); round <= 5; round++ {
		fixture.mustAttempt(round)
	}
	ahead.Store(int64(sourceliveness.HeldBoundFloor))
	fixture.mustAttempt(6)
	named := func() []fleet.Anomaly {
		var rows []fleet.Anomaly
		for _, row := range fixture.tracker.NoData() {
			if row.Kind == fleet.KindSourceUnknownHeld {
				rows = append(rows, row)
			}
		}
		return rows
	}
	if rows := named(); len(rows) != 1 {
		t.Fatalf("held past the bound: %+v, want B's line", rows)
	}
	keys := fixture.noDataEndKeys()
	if len(keys) != 1 {
		t.Fatalf("index keys %v, want the Plan's one", keys)
	}
	ctx := context.Background()
	if err := fixture.redis.Del(ctx, keys[0]).Err(); err != nil {
		t.Fatal(err)
	}
	if err := fixture.redis.Set(ctx, keys[0], "not a hash", 0).Err(); err != nil {
		t.Fatal(err)
	}
	for round := int64(7); round <= 71; round++ {
		fixture.mustAttempt(round)
	}
	if unread := fixture.noDataEndOutcomes()[observability.NoDataEndUnavailable]; unread < 60 {
		t.Fatalf("unreadable rounds %d, want every round since", unread)
	}
	if rows := named(); len(rows) != 1 || rows[0].SourceUnknownHeld == nil || rows[0].SourceUnknownHeld.PastBound != 1 {
		t.Fatalf("after 65 unreadable rounds: %+v, want B's line still up", rows)
	}
	if got := recoveriesOf(fixture.thresholdEventsFor(lifecycleHostB)); len(got) != 0 {
		t.Fatalf("B was ended with its index unreadable: %+v", got)
	}
	fixture.interceptQueries(nil)
	waitSourceProbes(fixture.bundle)
}

// The probe never reads the last period that had data, at either end point,
// with time_delay at two periods and the shortest span there is: N = R = 1,
// so a Level ends (R + T + 1) = 3 periods after its last real point. The
// store answers as one whose count reaches one period back from each
// instant, and the source holds nothing after the outage began but what was
// written in the last period before it. A window that reaches back to that
// period reads the source alive and ends the alert during the outage;
// this one reads nothing, and holds.
func TestTheProbeNeverReadsTheLastPeriodWithData(t *testing.T) {
	const delay, period = int64(120), int64(60)
	// The present series: an ungrouped log count's one document, then
	// zeros.
	present := runGroupedEventCount(t, eventSpanCase{rounds: 8, native: true, statusSetter: "recovery-nodata", logCount: true, ungrouped: true,
		levels: [][2]int{{1, 1}}, timeDelay: delay, probeReadsBack: true})
	if present.probes == 0 {
		t.Fatal("present: no probe")
	}
	for round, kinds := range present.sent {
		if containsKind(kinds, contract.TriggerEventRecovery) {
			t.Fatalf("present: round %d ended the alert on a window holding the last period with data (all %v)", round, present.sent)
		}
	}
	if got := sourceLivenessChecks(t, present.recorder, "log", "alive", ""); got != 0 {
		t.Fatalf("present: %v alive verdicts, want none", got)
	}

}

// TestTheProbeNeverReadsTheLastPeriodWithData for an absent series: both
// hosts of a time series stop at once, the whole table quiet.
func TestTheAbsentProbeNeverReadsTheLastPeriodWithData(t *testing.T) {
	const delay, period = int64(120), int64(60)
	strategy := lifecycleStrategy{revision: 7, noDataOff: true, statusSetter: "recovery-nodata",
		edit: func(item map[string]any) { item["time_delay"] = delay }}
	fixture := startLifecycleFixture(t, strategy, nil)
	var written atomic.Int64
	fixture.interceptQueries(func(writer http.ResponseWriter, request *http.Request) bool {
		body, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(body))
		var payload struct {
			Start string `json:"start_time"`
			End   string `json:"end_time"`
		}
		_ = json.Unmarshal(body, &payload)
		start, _ := strconv.ParseInt(payload.Start, 10, 64)
		end, _ := strconv.ParseInt(payload.End, 10, 64)
		if !isSourceProbe(body) {
			// The query service as it labels a bucket: by the bucket's
			// start, the samples in it half a period later. A series' last
			// real point is then the start of its last period with data,
			// as it was live.
			fixture.mu.Lock()
			reporting := append([]string(nil), fixture.reporting...)
			fixture.mu.Unlock()
			series := make([]string, 0, len(reporting))
			for _, host := range reporting {
				series = append(series, `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],`+
					`"group_keys":["bk_target_cloud_id","bk_target_ip"],"group_values":["0","`+host+`"],"values":[[`+
					strconv.FormatInt((end-period)*1000, 10)+`,90]]}`)
			}
			if len(reporting) > 0 {
				written.Store(end - period/2)
			}
			_, _ = writer.Write([]byte(`{"series":[` + strings.Join(series, ",") + `],"status":null,"is_partial":false,` +
				`"result_table_id":["system.cpu"]}`))
			return true
		}
		if last := written.Load(); last > start-period && last < end {
			_, _ = writer.Write([]byte(probeAlive))
		} else {
			_, _ = writer.Write([]byte(probeEmpty))
		}
		return true
	})
	fixture.serve(false, 90, lifecycleHostA, lifecycleHostB)
	fixture.mustAttempt(1)
	openedA, openedB := fixture.thresholdEventsFor(lifecycleHostA), fixture.thresholdEventsFor(lifecycleHostB)
	if len(openedA) != 1 || len(openedB) != 1 {
		t.Fatalf("round 1 sent A %+v and B %+v, want one ABNORMAL each", openedA, openedB)
	}
	fixture.publishOpenAlert(openedA[0].DedupeMD5)
	fixture.publishOpenAlert(openedB[0].DedupeMD5)
	fixture.serve(false, 90)
	for round := int64(2); round <= 8; round++ {
		fixture.mustAttempt(round)
		for _, host := range []string{lifecycleHostA, lifecycleHostB} {
			if got := recoveriesOf(fixture.thresholdEventsFor(host)); len(got) != 0 {
				t.Fatalf("absent: round %d ended %s on a window holding the last period with data: %+v", round, host, got)
			}
		}
	}
	if held := fixture.noDataEndOutcomes()[observability.NoDataEndSourceUnknown]; held == 0 {
		t.Fatal("absent: no end was held")
	}
	fixture.interceptQueries(nil)
	waitSourceProbes(fixture.bundle)
}

// Within the empty span the source has data - the same table, another
// series the strategy's condition does not match, or a host still
// reporting - and the end goes ahead as configured: the shape the live run
// ended correctly on. Present: the log index set gets other documents after
// the count's own went quiet. Absent: host A keeps reporting while B stops.
func TestADataBearingSpanEndsAsConfigured(t *testing.T) {
	const delay = int64(120)
	present := runGroupedEventCount(t, eventSpanCase{rounds: 8, native: true, statusSetter: "recovery-nodata", logCount: true, ungrouped: true,
		levels: [][2]int{{1, 1}}, timeDelay: delay, sourceAlive: true})
	ended := false
	for _, kinds := range present.sent {
		ended = ended || containsKind(kinds, contract.TriggerEventRecovery)
	}
	if !ended {
		t.Fatalf("present: no end with the source alive in the span (all %v)", present.sent)
	}

	strategy := lifecycleStrategy{revision: 7, noDataOff: true, statusSetter: "recovery-nodata",
		edit: func(item map[string]any) { item["time_delay"] = delay }}
	fixture := startLifecycleFixture(t, strategy, nil)
	fixture.interceptQueries(func(writer http.ResponseWriter, request *http.Request) bool {
		body, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(body))
		if !isSourceProbe(body) {
			return false
		}
		// A reports every period, so every window of the span holds its samples.
		_, _ = writer.Write([]byte(probeAlive))
		return true
	})
	fixture.serve(false, 90, lifecycleHostA, lifecycleHostB)
	fixture.mustAttempt(1)
	openedB := fixture.thresholdEventsFor(lifecycleHostB)
	if len(openedB) != 1 {
		t.Fatalf("round 1 sent B %+v, want one ABNORMAL", openedB)
	}
	fixture.publishOpenAlert(fixture.thresholdEventsFor(lifecycleHostA)[0].DedupeMD5)
	fixture.publishOpenAlert(openedB[0].DedupeMD5)
	fixture.serve(false, 90, lifecycleHostA)
	for round := int64(2); round <= 8 && len(recoveriesOf(fixture.thresholdEventsFor(lifecycleHostB))) == 0; round++ {
		fixture.mustAttempt(round)
	}
	if ends := recoveriesOf(fixture.thresholdEventsFor(lifecycleHostB)); len(ends) != 1 ||
		ends[0].LevelResults[0].DecisionWindow.Recovery.EndedBy != contract.RecoveryEndedByNoData {
		t.Fatalf("absent: B's ends %+v, want one ended by no data", ends)
	}
	fixture.interceptQueries(nil)
	waitSourceProbes(fixture.bundle)
}

// Two series of one field whose last real points are a period apart, and
// then the whole source stops: B's last report a period before A's. B comes
// due first, and its window - from two periods after B's last point - holds
// A's last samples: the source was still delivering in B's span, so B ends.
// A comes due a round later, and its own window - from two periods after
// A's last point - holds nothing: A is held. A verdict is the window's it was
// read over; B's alive is not A's.
func TestASeriesEndsOnlyOnItsOwnSpansVerdict(t *testing.T) {
	const period = int64(60)
	strategy := lifecycleStrategy{revision: 7, noDataOff: true, statusSetter: "recovery-nodata"}
	fixture := startLifecycleFixture(t, strategy, nil)
	// Each host's samples, as the query service labels and stores them: a
	// read's point at its bucket's start, its samples half a period later.
	samples := map[string]int64{}
	fixture.interceptQueries(func(writer http.ResponseWriter, request *http.Request) bool {
		body, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(body))
		var payload struct {
			Start string `json:"start_time"`
			End   string `json:"end_time"`
		}
		_ = json.Unmarshal(body, &payload)
		start, _ := strconv.ParseInt(payload.Start, 10, 64)
		end, _ := strconv.ParseInt(payload.End, 10, 64)
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if !isSourceProbe(body) {
			series := make([]string, 0, len(fixture.reporting))
			for _, host := range fixture.reporting {
				samples[host] = end - period/2
				series = append(series, `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],`+
					`"group_keys":["bk_target_cloud_id","bk_target_ip"],"group_values":["0","`+host+`"],"values":[[`+
					strconv.FormatInt((end-period)*1000, 10)+`,90]]}`)
			}
			_, _ = writer.Write([]byte(`{"series":[` + strings.Join(series, ",") + `],"status":null,"is_partial":false,` +
				`"result_table_id":["system.cpu"]}`))
			return true
		}
		// A count reaching one period back from each instant.
		alive := false
		for _, last := range samples {
			alive = alive || last > start-period && last < end
		}
		// B's own window - from two periods after its last point - answers
		// last: a verdict kept per source alone, not per window, would then
		// be B's alive when A's end comes due, every run, not when the two
		// probes happen to finish in that order.
		if bLast, stopped := samples[lifecycleHostB]; stopped && start == bLast-period/2+2*period {
			fixture.mu.Unlock()
			time.Sleep(100 * time.Millisecond) // fixed-wait: only orders B's answer after A's; A's probe answers in-process in microseconds, so 100 ms puts B last every run
			fixture.mu.Lock()
		}
		if alive {
			_, _ = writer.Write([]byte(probeAlive))
		} else {
			_, _ = writer.Write([]byte(probeEmpty))
		}
		return true
	})
	fixture.serve(false, 90, lifecycleHostA, lifecycleHostB)
	fixture.mustAttempt(1)
	openedA, openedB := fixture.thresholdEventsFor(lifecycleHostA), fixture.thresholdEventsFor(lifecycleHostB)
	if len(openedA) != 1 || len(openedB) != 1 {
		t.Fatalf("round 1 sent A %+v and B %+v, want one ABNORMAL each", openedA, openedB)
	}
	fixture.publishOpenAlert(openedA[0].DedupeMD5)
	fixture.publishOpenAlert(openedB[0].DedupeMD5)
	fixture.serve(false, 90, lifecycleHostA) // B's last report was round 1.
	fixture.mustAttempt(2)
	fixture.serve(false, 90) // A's was round 2: the whole source stops.
	for round := int64(3); round <= 9; round++ {
		fixture.mustAttempt(round)
	}
	if ends := recoveriesOf(fixture.thresholdEventsFor(lifecycleHostB)); len(ends) != 1 {
		t.Fatalf("B's ends %+v, want one: A still delivered within B's span", ends)
	}
	if ends := recoveriesOf(fixture.thresholdEventsFor(lifecycleHostA)); len(ends) != 0 {
		t.Fatalf("A was ended mid-outage %+v: its own span holds nothing", ends)
	}
	if held := fixture.noDataEndOutcomes()[observability.NoDataEndSourceUnknown]; held == 0 {
		t.Fatal("A's end was never held")
	}
	fixture.interceptQueries(nil)
	waitSourceProbes(fixture.bundle)
}

// Across many held rounds the probe's query reads at most the Level's span,
// (R + T + 1) = 3 periods, ending at the round's read end, and never before
// two periods after B's last real point.
func TestAHeldLevelsProbeReadsAtMostItsSpan(t *testing.T) {
	strategy := lifecycleStrategy{revision: 7, noDataOff: true, statusSetter: "recovery-nodata"}
	fixture := startLifecycleFixture(t, strategy, nil)
	var windows [][2]int64
	fixture.interceptQueries(func(writer http.ResponseWriter, request *http.Request) bool {
		body, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(bytes.NewReader(body))
		if !isSourceProbe(body) {
			return false
		}
		var payload struct {
			Start string `json:"start_time"`
			End   string `json:"end_time"`
		}
		_ = json.Unmarshal(body, &payload)
		start, _ := strconv.ParseInt(payload.Start, 10, 64)
		end, _ := strconv.ParseInt(payload.End, 10, 64)
		fixture.mu.Lock()
		windows = append(windows, [2]int64{start, end})
		fixture.mu.Unlock()
		_, _ = writer.Write([]byte(probeStatus))
		return true
	})
	fixture.serve(false, 90, lifecycleHostA, lifecycleHostB)
	fixture.mustAttempt(1)
	opened := fixture.thresholdEventsFor(lifecycleHostB)
	if len(opened) != 1 {
		t.Fatalf("round 1 sent B %+v, want one ABNORMAL", opened)
	}
	fixture.publishOpenAlert(fixture.thresholdEventsFor(lifecycleHostA)[0].DedupeMD5)
	fixture.publishOpenAlert(opened[0].DedupeMD5)
	fixture.serve(false, 90, lifecycleHostA)
	for round := int64(2); round <= 15; round++ {
		fixture.mustAttempt(round)
	}
	waitSourceProbes(fixture.bundle)
	fixture.interceptQueries(nil)
	span := 3 * fixture.interval
	lastB := fixture.evaluationAt(1) - 1 // the query service here puts a point at the window's last second
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(windows) < 10 {
		t.Fatalf("%d probes, want one a round over ten held rounds", len(windows))
	}
	for _, window := range windows {
		if window[1]-window[0] > span || window[0] < lastB+2*fixture.interval {
			t.Fatalf("a probe read [%d, %d): %d s, want at most the %d s span, from %d on (all %v)",
				window[0], window[1], window[1]-window[0], span, lastB+2*fixture.interval, windows)
		}
	}
}
