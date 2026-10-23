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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	model "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// outageWorld drives one replica's tracker through the shapes an outage
// leaves: rounds failing or retried on the store, and the Slots behind them
// given up, as the runtime reports both.
type outageWorld struct {
	t       *testing.T
	at      *clock
	tracker *Tracker
	started time.Time
}

func newOutageWorld(t *testing.T) *outageWorld {
	t.Helper()
	at := &clock{at: time.Date(2026, 10, 22, 2, 0, 0, 0, time.UTC)}
	return &outageWorld{t: t, at: at, tracker: newTracker(t, at), started: at.at.Add(-time.Hour)}
}

// failed is a round of the object failing, or retried, with code at offset
// from the world's start.
func (w *outageWorld) failed(queryGroup, strategy, code string, offset time.Duration, slot int64) {
	w.at.at = time.Date(2026, 10, 22, 2, 0, 0, 0, time.UTC).Add(offset)
	w.tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted,
		ExecuteOutcome: "error", Result: observability.ResultRetrying, ReasonCode: observability.ReasonCode(code),
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: strategy, BusinessID: "2", EvaluationTime: slot},
	})
}

// skipped is the object's Slot given up at offset.
func (w *outageWorld) skipped(queryGroup, strategy string, offset time.Duration, slot int64) {
	w.at.at = time.Date(2026, 10, 22, 2, 0, 0, 0, time.UTC).Add(offset)
	w.tracker.Observe(context.Background(), observability.Observation{
		ProgressCompletionKind: "GAP_SKIPPED", ProgressCompletionReason: "GAP_SKIPPED",
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: strategy, BusinessID: "2", EvaluationTime: slot},
	})
}

// finalized is the object's Slot finalized without a query, SNAPSHOT_UNAVAILABLE
// under cause, as the coordinator's completion reports it.
func (w *outageWorld) finalized(queryGroup, strategy string, cause model.CompletionCause, offset time.Duration, slot int64) {
	w.at.at = time.Date(2026, 10, 22, 2, 0, 0, 0, time.UTC).Add(offset)
	w.tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
		Result: observability.ResultDegraded, ReasonCode: observability.ReasonCode("SNAPSHOT_UNAVAILABLE"),
		ProgressCompletionKind: "SNAPSHOT_UNAVAILABLE", ProgressCompletionCause: string(cause),
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: strategy, BusinessID: "2", EvaluationTime: slot},
	})
}

// queryFailed is the object's Slot failing its query, as the access stage
// reports it.
func (w *outageWorld) queryFailed(queryGroup, strategy string, offset time.Duration, slot int64) {
	w.at.at = time.Date(2026, 10, 22, 2, 0, 0, 0, time.UTC).Add(offset)
	w.tracker.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
		QueryFailure: &observability.QueryFailureFacts{Stage: "query", Category: "timeout", Code: "QUERY_TIMEOUT"},
		Trace:        observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: strategy, BusinessID: "2", EvaluationTime: slot},
	})
}

// snapshot is the replica's publish: its spans with the strategies behind
// each, as the runtime's publisher attaches them.
func (w *outageWorld) snapshot(started time.Time) Snapshot {
	skips := w.tracker.GapSkips()
	for queryGroup, skip := range skips {
		skip.Strategies = w.tracker.StrategiesFor(queryGroup)
		skips[queryGroup] = skip
	}
	return Snapshot{Replica: "pod-a", TakenAt: w.at.at, StartedAt: started, GapSkips: skips}
}

func getOutages(t *testing.T, handler http.Handler, query string) (int, StoreOutagesResponse, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/outages"+query, nil))
	var response StoreOutagesResponse
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return recorder.Code, response, recorder.Body.String()
}

// Spans given up after the same object's rounds failed on the store, within
// one replay window of each other, are one outage; a span whose object
// failed on another dependency is not, nor one whose store failure was more
// than a replay window before its first skip; a later outage is its own.
// Each row keeps the span's own query failure beside the store failure.
func TestStoreOutagesFoldTheSpansTheStoreFailuresGaveUp(t *testing.T) {
	w := newOutageWorld(t)
	// One outage at 02:00: qg-a and qg-b failed on the store, their Slots
	// behind given up a few minutes later; qg-b's skipped Slot had also
	// failed its query.
	w.failed("qg-a", "11", "REDIS_UNAVAILABLE", 0, 1000)
	w.failed("qg-b", "12", "STATE_WRITE_RETRYABLE", time.Minute, 1000)
	w.skipped("qg-a", "11", 2*time.Minute, 1060)
	w.queryFailed("qg-b", "12", 2*time.Minute+30*time.Second, 1060)
	w.skipped("qg-b", "12", 3*time.Minute, 1060)
	// Not this outage: qg-c failed on Kafka, and qg-d's store failure was
	// thirty minutes before its first skip.
	w.failed("qg-c", "13", "KAFKA_UNAVAILABLE", time.Minute, 1000)
	w.skipped("qg-c", "13", 3*time.Minute, 1060)
	w.failed("qg-d", "14", "REDIS_UNAVAILABLE", 4*time.Minute, 1000)
	w.skipped("qg-d", "14", 34*time.Minute, 2860)
	// qg-f failed on the store, then on Kafka, before its skip: the store
	// failure is still the one in the window, and the span is this outage's.
	w.failed("qg-f", "16", "REDIS_UNAVAILABLE", 0, 1000)
	w.failed("qg-f", "16", "KAFKA_UNAVAILABLE", time.Minute, 1000)
	w.skipped("qg-f", "16", 2*time.Minute+10*time.Second, 1060)
	// A second outage an hour on.
	w.failed("qg-e", "15", "REDIS_UNAVAILABLE", 60*time.Minute, 4600)
	w.skipped("qg-e", "15", 61*time.Minute, 4660)

	handler := handlerWith(t, []Snapshot{w.snapshot(w.started)}, Expectation{}, []string{"pod-a"})
	code, response, body := getOutages(t, handler, "")
	if code != http.StatusOK || response.State != StoreOutageStateOutages || len(response.Outages) != 2 {
		t.Fatalf("answer %d %s", code, body)
	}
	later, first := response.Outages[0], response.Outages[1]
	if later.Objects != 1 || later.Strategies != 1 || later.Rows[0].QueryGroup != "qg-e" {
		t.Fatalf("newest outage %+v, want qg-e alone", later)
	}
	if first.Objects != 3 || first.Strategies != 3 || first.Slots != 3 ||
		first.Reasons["REDIS_UNAVAILABLE"] != 2 || first.Reasons["STATE_WRITE_RETRYABLE"] != 1 {
		t.Fatalf("first outage %+v, want qg-a, qg-b and qg-f", first)
	}
	if !first.From.Equal(time.Date(2026, 10, 22, 2, 2, 0, 0, time.UTC)) || !first.To.Equal(time.Date(2026, 10, 22, 2, 3, 0, 0, time.UTC)) {
		t.Fatalf("first outage %v-%v, want its first skip to its last", first.From, first.To)
	}
	if first.Line != "Redis 不可用 2026-10-22 02:02:00Z–2026-10-22 02:03:00Z：3 个对象、3 条策略丢了这些窗口" {
		t.Fatalf("line %q", first.Line)
	}
	rows := map[string]StoreOutageRow{}
	for _, outage := range response.Outages {
		for _, row := range outage.Rows {
			rows[row.QueryGroup] = row
		}
	}
	if _, listed := rows["qg-c"]; listed {
		t.Fatal("a span after a Kafka failure was filed as a store outage")
	}
	if _, listed := rows["qg-d"]; listed {
		t.Fatal("a span whose store failure was thirty minutes before it was filed as that outage")
	}
	if f := rows["qg-f"]; f.DependencyReason != "REDIS_UNAVAILABLE" {
		t.Fatalf("qg-f row %+v, want the store failure before its Kafka one", f)
	}
	if b := rows["qg-b"]; b.DependencyReason != "STATE_WRITE_RETRYABLE" || b.Reason != "QUERY_TIMEOUT" || b.FirstSlot != 1060 {
		t.Fatalf("qg-b row %+v, want its store failure beside its own query failure", b)
	}
	if response.Coverage.Since == nil || !response.Coverage.Since.Equal(w.started) || len(response.Coverage.Limitations) == 0 {
		t.Fatalf("coverage %+v, want the replica's start and its limitations", response.Coverage)
	}
	if !slices.Contains(response.Codes, "REDIS_UNAVAILABLE") || slices.Contains(response.Codes, "KAFKA_UNAVAILABLE") {
		t.Fatalf("codes %v", response.Codes)
	}
	// One strategy's spans alone; rows bounded; a bound out of range is
	// refused by name.
	if _, only, _ := getOutages(t, handler, "?strategy_id=12"); len(only.Outages) != 1 || only.Outages[0].Rows[0].QueryGroup != "qg-b" {
		t.Fatalf("strategy 12's outages %+v", only.Outages)
	}
	if _, cut, _ := getOutages(t, handler, "?rows=1"); len(cut.Outages[1].Rows) != 1 || !cut.Outages[1].RowsCut || cut.Outages[1].Objects != 3 {
		t.Fatalf("rows=1 %+v, want one row, cut, still counting every object", cut.Outages[1])
	}
	if code, _, body := getOutages(t, handler, "?rows=0"); code != http.StatusBadRequest || !strings.Contains(body, "invalid_rows") {
		t.Fatalf("rows=0 answered %d %s, want invalid_rows", code, body)
	}
}

// No outage is a statement about an interval: since the latest publishing
// replica's start when every one says when it started, and "cannot say"
// when one does not.
func TestNoStoreOutageSaysSinceWhenNotNever(t *testing.T) {
	w := newOutageWorld(t)
	w.failed("qg-c", "13", "KAFKA_UNAVAILABLE", time.Minute, 1000)
	w.skipped("qg-c", "13", 3*time.Minute, 1060)
	_, seen, _ := getOutages(t, handlerWith(t, []Snapshot{w.snapshot(w.started)}, Expectation{}, []string{"pod-a"}), "")
	if seen.State != StoreOutageStateNoneSeen || len(seen.Outages) != 0 || !strings.Contains(seen.Line, "2026-10-22 01:00:00Z") {
		t.Fatalf("none seen %+v", seen)
	}
	// The Kafka span is a window given up that no store failure explains:
	// "none seen" is said beside it, not instead of it.
	if seen.Coverage.Unattributed != 1 || !strings.HasSuffix(seen.Line, "；另有 1 段被放弃的窗口未归因到存储") {
		t.Fatalf("none seen %q with %d unattributed, want the Kafka span said beside it", seen.Line, seen.Coverage.Unattributed)
	}
	other := w.snapshot(w.started.Add(30 * time.Minute))
	other.Replica = "pod-b"
	_, both, _ := getOutages(t, handlerWith(t, []Snapshot{w.snapshot(w.started), other}, Expectation{}, []string{"pod-a", "pod-b"}), "")
	if both.Coverage.Since == nil || !both.Coverage.Since.Equal(w.started.Add(30*time.Minute)) {
		t.Fatalf("two replicas %+v, want the later start: the earlier one's failures are not all on record", both.Coverage)
	}
	_, unknown, _ := getOutages(t, handlerWith(t, []Snapshot{w.snapshot(time.Time{})}, Expectation{}, []string{"pod-a"}), "")
	if unknown.State != StoreOutageStateCoverageUnknown || unknown.Coverage.Since != nil {
		t.Fatalf("no start %+v, want coverage unknown", unknown)
	}
	unsaid := w.snapshot(time.Time{})
	unsaid.Replica = "pod-b"
	_, partial, _ := getOutages(t, handlerWith(t, []Snapshot{w.snapshot(w.started), unsaid}, Expectation{}, []string{"pod-a", "pod-b"}), "")
	if partial.State != StoreOutageStateCoverageUnknown || partial.Coverage.Since != nil {
		t.Fatalf("one replica without a start %+v, want coverage unknown, not the other's start", partial)
	}
}

// The overview carries the same outages as the read, as lines and counts
// with the interval, and nothing that names an object or a replica.
func TestTheOverviewSaysTheStoreOutagesWithoutRows(t *testing.T) {
	w := newOutageWorld(t)
	w.failed("qg-a", "11", "REDIS_UNAVAILABLE", 0, 1000)
	w.skipped("qg-a", "11", 2*time.Minute, 1060)
	handler := handlerWith(t, []Snapshot{w.snapshot(w.started)}, Expectation{}, []string{"pod-a"})
	_, listed, _ := getOutages(t, handler, "")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	var overview struct {
		StoreOutages json.RawMessage `json:"store_outages"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	var summary StoreOutageSummary
	if err := json.Unmarshal(overview.StoreOutages, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.State != StoreOutageStateOutages || summary.Line != listed.Line || len(summary.Outages) != 1 || summary.Since == nil {
		t.Fatalf("overview %s, want the read's line %q", overview.StoreOutages, listed.Line)
	}
	for _, key := range []string{"query_group", "replica", "rows"} {
		if strings.Contains(string(overview.StoreOutages), `"`+key+`"`) {
			t.Fatalf("the overview's outages name %q: %s", key, overview.StoreOutages)
		}
	}
}

// A strategy's drawer folds only that strategy's spans.
func TestAStrategysStoreOutagesAreItsOwnSpans(t *testing.T) {
	w := newOutageWorld(t)
	w.failed("qg-a", "11", "REDIS_UNAVAILABLE", 0, 1000)
	w.skipped("qg-a", "11", 2*time.Minute, 1060)
	w.failed("qg-b", "12", "REDIS_UNAVAILABLE", 0, 1000)
	w.skipped("qg-b", "12", 2*time.Minute, 1060)
	view := Aggregate(Expectation{}, []Snapshot{w.snapshot(w.started)}, []string{"pod-a"}, w.at.at, time.Minute)
	byStrategy := strategyStoreOutages(&view, w.at.at, 0)
	if len(byStrategy) != 2 || byStrategy["11"].Outages[0].Objects != 1 || byStrategy["11"].Outages[0].Strategies != 1 {
		t.Fatalf("by strategy %+v", byStrategy)
	}
	if none := noStrategyStoreOutage(&view, w.at.at); none.State != StoreOutageStateNoneSeen {
		t.Fatalf("a strategy that lost nothing reads %+v", none)
	}
}

// Every DEPENDENCY_DOWN code says whether it is this deployment's own store,
// so a code added to the catalogue cannot land on the dependency line without
// saying whether an outage of it is a store outage. The set holds the store
// codes and none of the others.
func TestEveryDependencyCodeSaysWhetherItIsTheStore(t *testing.T) {
	for code, decided := range codeChecks {
		if decided.check == CheckDependencyDown && decided.store == storeUndecided {
			t.Errorf("DEPENDENCY_DOWN code %s does not say whether it is this deployment's store (storeDown or otherDown)", code)
		}
		if decided.check != CheckDependencyDown && decided.store != storeUndecided {
			t.Errorf("code %s says it is a store outage without landing on DEPENDENCY_DOWN", code)
		}
	}
	for _, definition := range contract.ReasonCatalogV2() {
		if strings.HasPrefix(definition.Code, "REDIS_") && !storeOutageCode(definition.Code) {
			t.Errorf("catalogue code %s names Redis and is not a store outage code", definition.Code)
		}
	}
	codes := StoreOutageCodes()
	for _, want := range []string{"REDIS_UNAVAILABLE", "STATE_WRITE_RETRYABLE", "GAP_WRITE_RETRYABLE", "PROGRESS_BEGIN_FAILED", "SNAPSHOT_UNAVAILABLE"} {
		if !slices.Contains(codes, want) {
			t.Errorf("store outage codes %v lack %s", codes, want)
		}
	}
	// A read too large for its deadline is a defect, not an outage - the
	// lesson that once filed 163 of them a day as a Redis incident - and
	// Kafka is not this deployment's store.
	for _, other := range []string{"STATE_READ_TIMEOUT", "STATE_READ_DEADLINE", "KAFKA_UNAVAILABLE"} {
		if slices.Contains(codes, other) {
			t.Errorf("store outage codes %v include %s", codes, other)
		}
	}
}

// The strategy page's read says the outages its own objects lost windows in,
// from the board's computation, and a strategy that lost none reads as none
// seen since the replicas' start.
func TestTheStrategyPageSaysItsStoreOutages(t *testing.T) {
	snapshots := healthySnapshots()
	failedAt := now.Add(-20 * time.Minute)
	for index := range snapshots {
		snapshots[index].StartedAt = now.Add(-2 * time.Hour)
	}
	snapshots[0].GapSkips = map[string]SkippedSpan{"qg-4101-a": {FirstSlot: 1000, LastSlot: 1060, Slots: 2,
		FirstAt: now.Add(-18 * time.Minute), At: now.Add(-17 * time.Minute), Replica: "pod-a",
		Strategies: []StrategyRef{{StrategyID: "4101", BusinessID: "2"}}, DependencyReason: "REDIS_UNAVAILABLE", DependencyAt: &failedAt}}
	rig := newDiagnosisRigFrom(t, diagnosisFacts(), nil, snapshots)
	rig.universe = []string{"4101", "4102"}
	if code, _, _ := rig.board(t, "board=1"); code != http.StatusOK {
		t.Fatalf("board status %d", code)
	}
	read := func(strategy string) StoreOutageSummary {
		recorder := httptest.NewRecorder()
		rig.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/diagnose?view=1&strategy_id="+strategy, nil))
		var view StrategyViewActual
		if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &view) != nil {
			t.Fatalf("view %s: %d %s", strategy, recorder.Code, recorder.Body.String())
		}
		return view.StoreOutages
	}
	if lost := read("4101"); lost.State != StoreOutageStateOutages || len(lost.Outages) != 1 || lost.Outages[0].Slots != 2 ||
		!strings.HasPrefix(lost.Line, "Redis 不可用 ") {
		t.Fatalf("4101 reads %+v, want its outage", lost)
	}
	if none := read("4102"); none.State != StoreOutageStateNoneSeen || none.Since == nil || !none.Since.Equal(now.Add(-2*time.Hour)) {
		t.Fatalf("4102 reads %+v, want none seen since the start", none)
	}
}

// The strategy page folds by the deployment's replay window, as the outage
// read does: two spans a quarter of an hour apart are one outage under a
// half-hour window.
func TestTheStrategyPageFoldsByTheReplayWindow(t *testing.T) {
	snapshots := healthySnapshots()
	failedAt := now.Add(-50 * time.Minute)
	ref := []StrategyRef{{StrategyID: "4101", BusinessID: "2"}}
	snapshots[0].GapSkips = map[string]SkippedSpan{
		"qg-4101-a": {FirstSlot: 1000, LastSlot: 1000, Slots: 1, FirstAt: now.Add(-45 * time.Minute), At: now.Add(-45 * time.Minute),
			Replica: "pod-a", Strategies: ref, DependencyReason: "REDIS_UNAVAILABLE", DependencyAt: &failedAt},
		"qg-other": {FirstSlot: 1900, LastSlot: 1900, Slots: 1, FirstAt: now.Add(-30 * time.Minute), At: now.Add(-30 * time.Minute),
			Replica: "pod-a", Strategies: ref, DependencyReason: "REDIS_UNAVAILABLE", DependencyAt: &failedAt},
	}
	rig := newDiagnosisRigWithin(t, diagnosisFacts(), nil, snapshots, 30*time.Minute)
	rig.universe = []string{"4101"}
	if code, _, _ := rig.board(t, "board=1"); code != http.StatusOK {
		t.Fatalf("board status %d", code)
	}
	recorder := httptest.NewRecorder()
	strategy := "4101"
	rig.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/diagnose?view=1&strategy_id="+strategy, nil))
	var view StrategyViewActual
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &view) != nil {
		t.Fatalf("view: %d %s", recorder.Code, recorder.Body.String())
	}
	if len(view.StoreOutages.Outages) != 1 || view.StoreOutages.Outages[0].Objects != 2 {
		t.Fatalf("4101 reads %+v, want one outage of both objects under the half-hour window", view.StoreOutages)
	}
}

// The overview folds the spans of every replica's part: an object handed
// over mid-outage, with a span given up on each replica, is one object of
// the outage, and a part that cut its spans says the merge is cut.
func TestMergedPartsCountAHandedOverObjectOnce(t *testing.T) {
	failedAt := now.Add(-20 * time.Minute)
	row := func(replica string, at time.Time) StoreOutageRow {
		return StoreOutageRow{QueryGroup: "qg-a", Replica: replica, Strategies: []StrategyRef{{StrategyID: "11", BusinessID: "2"}},
			FirstSlot: at.Unix(), LastSlot: at.Unix(), Slots: 1, FirstAt: at, At: at, DependencyReason: "REDIS_UNAVAILABLE", DependencyAt: &failedAt}
	}
	merged := MergeReplicaParts(ReplicaPart{StoreOutageSpans: []StoreOutageRow{row("pod-b", now.Add(-10*time.Minute))}},
		ReplicaPart{StoreOutageSpans: []StoreOutageRow{row("pod-a", now.Add(-15*time.Minute))}, StoreOutageSpansCut: true})
	if len(merged.StoreOutageSpans) != 2 || merged.StoreOutageSpans[0].Replica != "pod-a" || !merged.StoreOutageSpansCut {
		t.Fatalf("merged spans %+v cut %v, want both oldest first, cut", merged.StoreOutageSpans, merged.StoreOutageSpansCut)
	}
	view := View{PerReplica: []ReplicaView{{Replica: "pod-a", StartedAt: now.Add(-time.Hour)}}}
	summary := StoreOutagesOverview(&view, merged, now, 0)
	if len(summary.Outages) != 1 || summary.Outages[0].Objects != 1 || summary.Outages[0].Slots != 2 {
		t.Fatalf("overview %+v, want one object losing two Slots", summary)
	}
	if !summary.Outages[0].AtLeast || !strings.Contains(summary.Outages[0].Line, "至少 1 个对象、至少 1 条策略") {
		t.Fatalf("outage %+v, want its counts said as floors: a part cut its spans", summary.Outages[0])
	}
	if !slices.ContainsFunc(summary.Limitations, func(line string) bool { return strings.Contains(line, "只折算了最新的部分") }) {
		t.Fatalf("limitations %v, want the cut named", summary.Limitations)
	}
}

// A Snapshot the store did not answer until the Slot's recovery bound is a
// store failure: an outage whose Slots only ever failed that way is still
// attributed. A corrupt Snapshot finalizes under the same word and is a
// defect, not an outage.
func TestASnapshotTheStoreDidNotAnswerAttributesTheSpan(t *testing.T) {
	w := newOutageWorld(t)
	w.finalized("qg-a", "11", model.CauseSnapshotUnavailablePastBound, 0, 1000)
	w.skipped("qg-a", "11", 2*time.Minute, 1060)
	w.finalized("qg-b", "12", model.CauseSnapshotCorrupt, 0, 1000)
	w.skipped("qg-b", "12", 2*time.Minute, 1060)
	_, response, body := getOutages(t, handlerWith(t, []Snapshot{w.snapshot(w.started)}, Expectation{}, []string{"pod-a"}), "")
	if response.State != StoreOutageStateOutages || len(response.Outages) != 1 || response.Outages[0].Objects != 1 ||
		response.Outages[0].Rows[0].QueryGroup != "qg-a" || response.Outages[0].Rows[0].DependencyReason != "SNAPSHOT_UNAVAILABLE" {
		t.Fatalf("answer %s, want qg-a alone attributed to SNAPSHOT_UNAVAILABLE", body)
	}
}

// The overview and a strategy's page say the windows given up that no store
// failure explains, each counting its own: a strategy that lost windows only
// to another cause reads none seen with its count, not the shared answer.
func TestUnattributedSpansAreCountedBesideNoneSeen(t *testing.T) {
	w := newOutageWorld(t)
	w.failed("qg-a", "11", "KAFKA_UNAVAILABLE", time.Minute, 1000)
	w.skipped("qg-a", "11", 3*time.Minute, 1060)
	w.skipped("qg-b", "11", 4*time.Minute, 1060)
	w.skipped("qg-c", "12", 4*time.Minute, 1060)
	handler := handlerWith(t, []Snapshot{w.snapshot(w.started)}, Expectation{}, []string{"pod-a"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/overview", nil))
	var overview struct {
		StoreOutages StoreOutageSummary `json:"store_outages"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &overview); err != nil {
		t.Fatal(err)
	}
	if overview.StoreOutages.State != StoreOutageStateNoneSeen || overview.StoreOutages.Unattributed != 3 ||
		!strings.Contains(overview.StoreOutages.Line, "另有 3 段") {
		t.Fatalf("overview %+v, want none seen beside 3 unattributed", overview.StoreOutages)
	}
	view := Aggregate(Expectation{}, []Snapshot{w.snapshot(w.started)}, []string{"pod-a"}, w.at.at, time.Minute)
	byStrategy := strategyStoreOutages(&view, w.at.at, 0)
	if own := byStrategy["11"]; own.State != StoreOutageStateNoneSeen || own.Unattributed != 2 {
		t.Fatalf("strategy 11 reads %+v, want none seen beside its 2", own)
	}
	if own := byStrategy["12"]; own.Unattributed != 1 {
		t.Fatalf("strategy 12 reads %+v, want its 1", own)
	}
	if _, only, _ := getOutages(t, handler, "?strategy_id=12"); only.Coverage.Unattributed != 1 {
		t.Fatalf("outage.list for 12 counts %d, want 1", only.Coverage.Unattributed)
	}
}
