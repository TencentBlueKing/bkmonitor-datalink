// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Every other check in this file is static: field names against the Go types,
// containers, wiring, wording. None of them run the page, so none of them can
// see a ReferenceError -- and one shipped. The object table rendered nothing
// but "a is not defined" for two releases while every test here was green.
//
// This one executes the render functions. It is the only check that can fail on
// a page that throws.
//
// The fixture is built from the Go types rather than written as JSON, so the
// shapes cannot drift from what the API sends without this failing to compile.
func TestTheRenderFunctionsRunWithoutThrowing(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		// Loudly. A skipped check is no check, and the failure it exists for is
		// invisible to everything else here.
		t.Skip("node is not on PATH, so the page's logic is NOT executed by this run -- " +
			"only the static checks above ran, and a ReferenceError would pass all of them")
	}

	at := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	anomaly := func(id string, mutate func(*fleet.Anomaly)) fleet.Anomaly {
		item := fleet.Anomaly{
			QueryGroup: id, Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde",
			Kind: "DEGRADED_RUN", ReasonCode: "COMPLETED_WITH_UNAVAILABLE",
			Since: at.Add(-time.Hour), SinceFrom: fleet.SinceSnapshotContinuity,
			Strategies: []fleet.StrategyRef{{StrategyID: "1234", BusinessID: "7"}},
		}
		if mutate != nil {
			mutate(&item)
		}
		return item
	}
	// One row of every shape the renderer branches on. A row shape that is not
	// here is a branch this check does not execute.
	rows := []fleet.Anomaly{
		anomaly("qg-plain", nil),
		anomaly("qg-reason", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
		}),
		anomaly("qg-failure", func(item *fleet.Anomaly) {
			item.Failure = &fleet.FailureRef{Stage: "provider", Category: "source_backend",
				Code: "QUERY_UNAVAILABLE", Detail: "http_status=400"}
		}),
		anomaly("qg-restored", func(item *fleet.Anomaly) {
			item.SinceFrom = fleet.SinceRestoredLastFull
		}),
		anomaly("qg-preexisting", func(item *fleet.Anomaly) {
			item.SinceFrom = fleet.SinceProcessStart
		}),
		anomaly("qg-stalled", func(item *fleet.Anomaly) {
			item.Stalled, item.FailingSince = true, at.Add(-2*time.Hour)
		}),
		anomaly("qg-blocked", func(item *fleet.Anomaly) {
			item.Kind, item.ReasonCode, item.Strategies = "BLOCKED_RUN", "source_blocked", nil
		}),
		anomaly("qg-cooldown", func(item *fleet.Anomaly) {
			item.Kind = "QUERY_COOLDOWN"
			item.QueryCooldown = &observability.QueryCooldownFacts{
				Until: at.Add(time.Hour), LastQueryAt: at.Add(-time.Minute), Failures: 5}
			item.Failure = &fleet.FailureRef{Stage: "provider", Category: "source_backend",
				Code: "QUERY_UNAVAILABLE", Detail: "transport=timeout"}
		}),
		// Same pool, same code, and the backend answered: it read the query and
		// refused it. A live deployment held 350 of these filed as the backend's.
		anomaly("qg-rejected", func(item *fleet.Anomaly) {
			item.Kind = "QUERY_COOLDOWN"
			item.QueryCooldown = &observability.QueryCooldownFacts{
				Until: at.Add(time.Hour), LastQueryAt: at.Add(-time.Minute), Failures: 40}
			item.Failure = &fleet.FailureRef{Stage: "provider", Category: "source_backend",
				Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"}
		}),
		anomaly("qg-window-filling", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 3, Short: 1,
				WorstValid: 8, WorstRequired: 9, ShortRounds: 2}
		}),
		anomaly("qg-window-never", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 3, Short: 2,
				WorstValid: 2, WorstRequired: 14, ShortRounds: 40}
		}),
		anomaly("qg-window-complete", func(item *fleet.Anomaly) {
			item.Coverage = &fleet.HistoryCoverage{Levels: 3}
		}),
		// One short window among many. Same verdict as qg-window-never and a
		// completely different amount of broken -- one series inside a strategy
		// against a strategy that cannot be detected at all. Every number the
		// row used to render is identical between the two, because the worst
		// pair is by construction one window and the rounds counter does not
		// say how many windows earned it.
		// A window that is complete under a reason that says it is not.
		//
		// The reason is held over: a Level judged WARMING or GAPPED forces that
		// verdict onto every later evaluation until the window is full at the
		// last processed record, while the counts beside it stay live. This row
		// used to render 检测窗口完整 next to a cause of HISTORY_GAPPED -- the
		// page stating both halves of a contradiction and marking neither.
		anomaly("qg-window-held-complete", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED"
			item.Coverage = &fleet.HistoryCoverage{Levels: 3, Guarded: 3}
		}),
		// Held over and still short: the conclusion about the counts stands, the
		// reason under it may not.
		anomaly("qg-window-held-short", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 4, Short: 2, Guarded: 1,
				WorstValid: 2, WorstRequired: 14, ShortRounds: 40}
		}),
		// The two halves of "this window will never fill", identical in every
		// count that was ever published about them: same shortfall, same run,
		// same reason, same completeness. The page named the first as the likely
		// cause of both and sent whoever read the second to edit a strategy
		// whose aggregation dimensions were never the problem.
		anomaly("qg-window-churn", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 9, Short: 4,
				WorstValid: 2, WorstRequired: 9, ShortRounds: 40,
				Fresh: 4, ShortFresh: 4, FreshRounds: 40}
		}),
		anomaly("qg-window-stale-data", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 9, Short: 4,
				WorstValid: 2, WorstRequired: 9, ShortRounds: 40}
		}),
		// Every short window fresh for a single round, which is what the round
		// after a strategy edit looks like: a StateGeneration change re-keys
		// every series at once and they all load nothing. Reported as churn it
		// would accuse a strategy of the edit that was just made to it.
		anomaly("qg-window-rekeyed", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 9, Short: 4,
				WorstValid: 2, WorstRequired: 9, ShortRounds: 40,
				Fresh: 9, ShortFresh: 4, FreshRounds: 1}
		}),
		anomaly("qg-window-lopsided", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 999, Short: 1,
				WorstValid: 2, WorstRequired: 14, ShortRounds: 40}
		}),
		// The two rows a live page showed side by side. The GAPPED one carried
		// the churning-series wording because the row note read coverage and
		// never the reason -- the same defect as the summary count, in a
		// second place, and this fixture is what executes it.
		anomaly("qg-gapped-intermittent", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED"
			item.Coverage = &fleet.HistoryCoverage{Levels: 1, Short: 1,
				WorstValid: 5, WorstRequired: 9, ShortRounds: 29}
		}),
		anomaly("qg-drift", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "CONFIG_DRIFT", "CONFIG_DRIFT"
		}),
		anomaly("qg-offhours", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "EFFECTIVE_TIME_INACTIVE"
		}),
		anomaly("qg-skipped", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "GAP_SKIPPED"
			item.Coverage = &fleet.HistoryCoverage{Levels: 1, Short: 1,
				WorstValid: 4, WorstRequired: 9, ShortRounds: 6}
		}),
		anomaly("qg-gapped-fresh", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED"
			item.Coverage = &fleet.HistoryCoverage{Levels: 1, Short: 1,
				WorstValid: 5, WorstRequired: 9, ShortRounds: 3}
		}),
		anomaly("qg-window-starved", func(item *fleet.Anomaly) {
			item.Cause, item.CauseReason = "LEVEL_OUTCOME_UNKNOWN", "HISTORY_WARMING"
			item.Coverage = &fleet.HistoryCoverage{Levels: 3, Short: 2, Empty: 2,
				WorstRequired: 14, ShortRounds: 40, EmptyRounds: 40}
		}),
	}
	fleet.Attribute(rows)
	// The barest row the API can send: every omitempty field absent. It goes in
	// after Attribute so it keeps its empty attribution, because a fixture where
	// every row has every field cannot catch a property read on a field that is
	// sometimes not there -- which is the other half of what a render throws on.
	rows = append(rows, fleet.Anomaly{QueryGroup: "qg-bare", Replica: "r-1", Kind: "DEGRADED_RUN"})

	replicas := []fleet.ReplicaView{
		{Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-abcde", Owned: 452, Healthy: 384,
			Anomalies: 33, Demoted: 19, Undecidable: 12, ByDesign: 4, AgeSeconds: 3,
			UptimeSeconds: 7200, Ours: 5, External: 26},
		// One replica reporting no undecidable objects, so the render is
		// executed on both a present and an absent count. A fixture where every
		// row carries every field cannot catch a read on one that is sometimes
		// missing, which is half of what a render throws on.
		{Replica: "bk-monitor-alarmd-trigger-5bdb679ddf-fghij", Owned: 527, Healthy: 460,
			Anomalies: 53, Demoted: 14, AgeSeconds: 4, UptimeSeconds: 300, Ours: 8, External: 41},
	}
	lastExit := at.Add(-2 * time.Minute)
	fixture := map[string]any{
		"anomalies": rows,
		// The deployment-wide counts the governance line reads. Three owners
		// with objects and one without, so the line renders a zero as well as
		// counts -- a zero that reads as "nothing for this owner" is the value
		// a reader most needs to see printed rather than absent.
		"action_required_total": 5,
		"by_owner_total": fleet.Distribution{Top: []fleet.Count{
			{Value: "ALARMD", Count: 3}, {Value: "UNDETERMINED", Count: 2},
			{Value: "STRATEGY", Count: 1}, {Value: "DATA", Count: 4}}, Distinct: 4},
		"summary": fleet.Summary{
			ByKind:   fleet.Distribution{Top: []fleet.Count{{Value: "DEGRADED_RUN", Count: 7}}, Distinct: 1},
			ByReason: fleet.Distribution{Top: []fleet.Count{{Value: "COMPLETED_WITH_UNAVAILABLE", Count: 7}}, Distinct: 1},
			ByBusiness: fleet.Distribution{Top: []fleet.Count{{Value: "7", Count: 7}},
				Distinct: 9000, TailObjects: 40},
			ByFailureDetail: fleet.Distribution{
				Top: []fleet.Count{{Value: "http_status=400", Count: 1}}, Distinct: 1},
			Strategies: 7, Ours: 3, External: 4, Unattributed: 1, OursUnclassified: 1,
			Onset: fleet.Onset{LastHour: 2, LastDay: 3, Older: 3,
				NewestSince: at.Add(-time.Minute), OldestSince: at.Add(-40 * time.Hour)},
			WindowNeverFills: 3, WindowSeriesChurn: 1,
		},
		"window_never_fills_cases": windowNeverFillsCases(),
		// A filter narrowing the list, a replica that could not publish it, and
		// both at once. The first used to be reported as the second.
		"page_tail_cases": []map[string]any{
			{"name": "filtered", "total": 1, "objects": map[string]any{
				"anomalies_total": 16, "filtered": true,
				"summary": map[string]any{"partial": false}}},
			{"name": "truncated", "total": 50, "objects": map[string]any{
				"anomalies_total": 900, "filtered": false,
				"summary": map[string]any{"partial": true}}},
			{"name": "plain", "total": 16, "objects": map[string]any{
				"anomalies_total": 16, "filtered": false,
				"summary": map[string]any{"partial": false}}},
		},
		// A console that is configured, one that is not, and a reference with
		// no business id -- the console needs one to resolve the space, so a
		// link without it lands on an error page.
		"strategy_link_cases": []map[string]any{
			{"name": "configured", "base": "https://monitor.example",
				"strategy": fleet.StrategyRef{StrategyID: "1854", BusinessID: "7"}},
			{"name": "nobiz", "base": "https://monitor.example",
				"strategy": fleet.StrategyRef{StrategyID: "1854"}},
			{"name": "unconfigured", "base": "",
				"strategy": fleet.StrategyRef{StrategyID: "1854", BusinessID: "7"}},
		},
		// A 24-hour window and a 15-minute one. The first ends at the same
		// wall-clock time it started, which is what made it render empty.
		"range_cases": []map[string]any{
			{"name": "day", "start": at.Add(-24 * time.Hour).UnixMilli(), "end": at.UnixMilli()},
			{"name": "short", "start": at.Add(-15 * time.Minute).UnixMilli(), "end": at.UnixMilli()},
		},
		// A population restored at a rollout: every start time is a bound, and
		// the sentence over it used to name the newest as a moment.
		"onset_cases": []map[string]any{
			{"name": "bounded", "total": 5, "onset": fleet.Onset{
				LastHour: 5, NewestSince: at.Add(-2 * time.Hour), OldestSince: at.Add(-2 * time.Hour),
				NewestFrom: fleet.SinceRestoredLastFull, OldestFrom: fleet.SinceRestoredLastFull,
				Bounded: 5}},
			{"name": "measured", "total": 5, "onset": fleet.Onset{
				LastHour: 5, NewestSince: at.Add(-2 * time.Hour), OldestSince: at.Add(-3 * time.Hour),
				NewestFrom: fleet.SinceSnapshotContinuity, OldestFrom: fleet.SinceSnapshotContinuity}},
		},
		"health": fleet.HealthResponse{
			// The columns add up to Covered on purpose: the page prints that
			// equation and it is the only thing a reader has that says the
			// split is complete. A fixture that does not add up cannot tell a
			// page that dropped a column from one that is fine.
			Health: "UNKNOWN", Covered: 979, Determined: 979, Unknown: 0, Healthy: 844,
			AnomaliesTotal: 86, DemotedTotal: 33, UndecidableTotal: 12, ByDesignTotal: 4,
			DemotedDue: 2, DemotionEntries: 40, DemotionExits: 7, PerReplica: replicas,
			// The tracker writes the exit count and the exit time on adjacent
			// lines, so a deployment with exits always has this. Without it here
			// the fixture described a deployment that cannot exist -- and the page
			// rendered it as 最近一次出池 in the year 1, because omitempty does
			// nothing for a struct and the zero time is truthy.
			LastDemotionExit: &lastExit,
			// Sub-minute, because that is the range this line claims is normal
			// and the range its renderer could not express: it floored to whole
			// minutes and printed "0 分钟", which is also what it prints when
			// there is nothing overdue at all. The fixture carried no value here
			// at all before, so the clause never ran and the defect shipped.
			DemotedDueOldestSeconds: 40,
			// Unattributed beside a coverage gap. This cell used to claim the
			// verdict unconditionally, and with a gap present the banner above it
			// names a different cause -- one screen, two answers.
			Unattributed: 8,
			Gaps:         []fleet.Gap{{Kind: fleet.GapUndetermined}},
			// Spans nothing ever evaluated. In no column and in no total, which
			// is the whole difficulty -- the objects are running now and every
			// other number on the panel says so, correctly.
			PrunedSkips: []fleet.PrunedSkipRef{
				{QueryGroup: "qg-pruned-long", SpanSeconds: 5400, At: at.Add(-20 * time.Minute),
					DiscardedSlot: at.Add(-90 * time.Minute).Unix()},
				{QueryGroup: "qg-pruned-short", SpanSeconds: 180, At: at.Add(-time.Hour)},
			},
			// Nothing overdue, with the dispatch suppression that makes that zero
			// mean something. This is the branch a healthy deployment renders and
			// the one nobody had ever executed.
			Overdue:  &fleet.OverdueFacts{Total: 0},
			Dispatch: &fleet.DispatchSuppression{Parked: 1993, Skipped: map[string]uint64{}},
			// Capacity, which this check had never executed: the fixture carried
			// no capacity, so renderCapacity returned at its first guard and the
			// busiest computed panel on the page was covered by nothing. A null
			// dereference in it reached a live deployment.
			Capacity: &fleet.CapacityView{
				Replicas: 2, PermitsHeld: 3, PermitBudget: 16, PermitSeconds: 1200.5,
				Waiting: 0, QueueBudget: 256,
				// The shape a live deployment is in: nothing queued at this
				// instant, and most queries having waited at some point. The two
				// gauges alone read as headroom, which is what this fixture is
				// here to stop the page concluding.
				PermitAcquires: 40000, PermitWaits: 22400,
				MemoryUsed: 3 << 30, MemoryLimit: 16 << 30, MemorySource: "pod_limit",
				MemoryLimitKnown: true, ThrottledKnown: true, ThrottledSeconds: 0.0017,
				CPUSeconds: 4820.25, CPUCores: 8, CPUSource: "container CPU limit",
				Budgets:    map[string]uint64{"events": 65536, "series": 524288},
				Rejections: map[string]uint64{},
				Pulled:     &fleet.SeriesPull{Series: 900000, Records: 900000},
				// The rotation, which the fixture did not carry, so the two cells
				// that read it were never rendered by anything. One of them told a
				// reader to grow a queue for a number that is mostly a branch more
				// room cannot change.
				Rotation: &fleet.Rotation{
					Completed: 2069, Truncated: 4, Offered: 100000, Queued: 90000,
					Deferred: 512, DeferredQueueFull: 12, DeferredNotBetter: 500,
					LastSeconds: 0.86,
				},
			},
			// The line that answers "what is affected". Its columns are
			// truncated and some of its objects name no strategy, because both
			// are true on the deployment this page is read on and both change
			// what the counts may be said to mean.
			Impact: fleet.Impact{
				Anomalies:   fleet.ColumnImpact{Objects: 86, Strategies: 70, Businesses: 9, Partial: true},
				Ours:        fleet.ColumnImpact{Objects: 5, Strategies: 4, Businesses: 2, Partial: true},
				Demoted:     fleet.ColumnImpact{Objects: 33, Strategies: 31, Businesses: 6},
				Undecidable: fleet.ColumnImpact{Objects: 12, Strategies: 12, Businesses: 3},
				ByDesign:    fleet.ColumnImpact{Objects: 4, Strategies: 4, Businesses: 1},
				// Fewer than 31 + 70: a strategy with objects in both columns is
				// one strategy, which is why this is a field and not a sum.
				Blind:        fleet.ColumnImpact{Objects: 119, Strategies: 95, Businesses: 11, Partial: true},
				NoStrategies: 7,
			},
		},
		"per_replica": replicas,
		"coverage": fleet.Disagreement{Comparable: true, HeldNotExpected: []string{"qg-blocked"},
			HeldNotExpectedTotal: 12},
		"page": map[string]int{"offset": 0, "limit": 50, "total": len(rows)},
	}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "page.html"), page, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "smoke.js"), []byte(smokeHarness), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := exec.Command(node, filepath.Join(dir, "smoke.js"), dir).CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		t.Errorf("the page threw while rendering:\n%s", text)
		return
	}
	// The harness has to be able to fail, or a clean run means nothing. It
	// proves that by running one deliberately broken call and requiring it to
	// throw before reporting on the real ones.
	if !strings.Contains(text, "negative control threw") {
		t.Errorf("the harness did not prove it can fail; its clean result is worthless:\n%s", text)
	}
	// The one line on the page that answers "what is affected". A reader who
	// gets no answer here has to assemble it out of five object counts, which is
	// what they were doing.
	impactLine := lineStarting(text, "IMPACT ::")
	if impactLine == "" {
		t.Error("the impact line rendered nothing: the page answers how many objects and never " +
			"which alerts")
	}
	for _, want := range []string{
		// The union of the two columns. 31 + 70 is 101, and the fixture's union
		// is 95 because a strategy with objects in both is one strategy.
		"95 条策略拿不到检测结果",
		"11 个业务",
		// Whether to act, and by whom.
		"需要 alarmd 这边处理的：4 条策略",
		// What the counts cannot cover. Both are true of a live deployment and
		// both change what the numbers may be taken to mean.
		"是下界",
		"没带策略信息",
	} {
		if !strings.Contains(impactLine, want) {
			t.Errorf("the impact line does not say %q:\n%s", want, impactLine)
		}
	}
	if strings.Contains(impactLine, "101 条策略") {
		t.Errorf("the impact line added the two columns instead of taking their union, which "+
			"overstates the number a reader acts on:\n%s", impactLine)
	}
	if !strings.Contains(text, "windowNeverFills agreed on") {
		t.Errorf("the page's windowNeverFills was never run against the Go rule; the two copies "+
			"are unchecked:\n%s", text)
	}

	// The partition equation the page prints under the verdict. It is the only
	// statement on the page that says every object is accounted for, and a
	// column left out of it makes the sum quietly wrong while every cell above
	// still shows a plausible number.
	equation := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "SPLIT ") {
			equation = line
			break
		}
	}
	if equation == "" {
		t.Error("the page rendered no partition line; nothing on it says the columns account for " +
			"every object")
	} else if !strings.Contains(equation, "等于应有的") {
		t.Errorf("the partition line does not add up: %s\n(the fixture's columns sum to Covered, "+
			"so a page that drops one is the only way this fails)", equation)
	}

	// The four answers each row leads with. Same fixtures as the NOTE table:
	// the two rows that were identical in every count but the fresh ones must
	// now answer with different owners and different next steps, and the
	// undetermined one must say so rather than pick.
	for _, want := range []struct{ object, who, what, next string }{
		{"qg-window-churn", "策略侧", "序列在不断换", "去掉会变的聚合维度"},
		{"qg-window-stale-data", "数据侧", "序列一直在，数据缺点", "查采集和查询偏移"},
		{"qg-window-rekeyed", "不用处理", "序列刚重新建号", "再看 8 轮"},
		{"qg-window-starved", "待确认", "窗口一个点都没有", "先确认是指标停了还是算法判不了"},
		{"qg-window-filling", "不用处理", "序列还年轻", "还差 1 轮"},
		{"qg-gapped-intermittent", "数据侧", "数据断断续续", "查询偏移"},
		{"qg-skipped", "alarmd", "放弃了一段时间", "查为什么跟不上"},
		{"qg-drift", "待确认", "计划激活没对上", "看已持续"},
		{"qg-offhours", "不用处理", "不在生效时段", "不用管"},
		// One pool, one code, two owners -- decided on the detail.
		{"qg-cooldown", "数据侧", "后端连续失败", "查后端"},
		{"qg-rejected", "待确认", "后端拒绝了查询本身", "不是后端挂"},
	} {
		line := lineStarting(text, "ROW "+want.object+" ::")
		if line == "" {
			t.Errorf("no row was rendered for %s", want.object)
			continue
		}
		for _, part := range []string{want.who, want.what, want.next} {
			if !strings.Contains(line, part) {
				t.Errorf("%s renders %q, want it to say %q", want.object, line, part)
			}
		}
	}
	governance := lineStarting(text, "GOVERNANCE ::")
	if !strings.Contains(governance, "需要你处理：") || !strings.Contains(governance, "策略侧") {
		t.Errorf("governance line = %q, want the to-do count and the per-owner counts", governance)
	}

	// Only one column decides the verdict, and the line that says so was printed
	// on all four. A reader paging the demoted pool was told, of objects the
	// page had just finished excluding from the judgment, that alarmd is
	// answerable for them and that the judgment follows the count.
	for _, want := range []struct{ column, says, mustNotSay string }{
		{"anomalies", "判定就看这个数", "整栏不进部署判定"},
		{"demoted", "整栏不进部署判定", "判定就看这个数"},
		{"undecidable", "整栏不进部署判定", "判定就看这个数"},
		{"by_design", "整栏不进部署判定", "判定就看这个数"},
		// The settled objects split into two halves with different owners, and
		// this sentence used to name one of them as the likely cause of all of
		// them. The fixture has three never-filling objects of which one is
		// churn, so a line that reports the whole count as churn -- or that
		// stops reading the count at all -- says something the summary does not.
		{"anomalies", "其中 1 个是序列在不断换", "常见的是维度里"},
		{"anomalies", "另外 2 个的序列一直都在", "这一批全是这种"},
	} {
		line := ""
		for _, candidate := range strings.Split(text, "\n") {
			if strings.HasPrefix(candidate, "WHOSE "+want.column+" ::") {
				line = candidate
				break
			}
		}
		if line == "" {
			t.Errorf("no attribution line was rendered for column %s", want.column)
			continue
		}
		if !strings.Contains(line, want.says) {
			t.Errorf("column %s renders %q, want it to say %q", want.column, line, want.says)
		}
		if strings.Contains(line, want.mustNotSay) {
			t.Errorf("column %s renders %q, which says %q -- that is the other column's claim",
				want.column, line, want.mustNotSay)
		}
	}

	// What the capacity panel says when a refresh arrives before any counter has
	// moved. Not throwing is checked by the harness; this checks it says the
	// right thing, because the branch that threw was the one meant to explain
	// exactly this state.
	capacity := lineStarting(text, "CAPACITY ::")
	if capacity == "" {
		t.Error("the capacity panel rendered nothing; the busiest computed panel on the page is " +
			"covered by nothing again")
	} else if !strings.Contains(capacity, "这两次读取之间没有新的拉取") {
		t.Errorf("after a refresh with the counters unmoved the capacity panel does not explain "+
			"why there is no average:\n%s", capacity)
	}

	// A link is rendered only when the environment said where its console is and
	// the reference carries everything that console needs. A wrong link sends a
	// reader to an error page and costs more than no link at all.
	for _, want := range []struct{ name, says, mustNotSay string }{
		{"configured", "https://monitor.example?bizId=7#/strategy-config/detail/1854", ""},
		{"nobiz", "(none)", "http"},
		{"unconfigured", "(none)", "http"},
	} {
		line := lineStarting(text, "LINK "+want.name+" ::")
		if line == "" {
			t.Errorf("strategyLink rendered nothing for the %s case", want.name)
			continue
		}
		if !strings.Contains(line, want.says) {
			t.Errorf("strategyLink %s renders %q, want %q", want.name, line, want.says)
		}
		if want.mustNotSay != "" && strings.Contains(line, want.mustNotSay) {
			t.Errorf("strategyLink %s renders %q, which is a link it cannot know is right",
				want.name, line)
		}
	}

	// A 24-hour window starts and ends at the same wall-clock time, and a
	// time-only label printed it as a range of zero length over a chart that
	// visibly covered a day.
	if line := lineStarting(text, "RANGE day ::"); line == "" {
		t.Error("rangeLabel rendered nothing for the 24-hour window")
	} else {
		ends := strings.SplitN(strings.TrimPrefix(line, "RANGE day :: "), " – ", 2)
		if len(ends) != 2 || ends[0] == ends[1] {
			t.Errorf("the 24-hour window renders %q: both ends read the same, so the label says "+
				"the window has no length", line)
		}
	}
	if line := lineStarting(text, "RANGE short ::"); line == "" {
		t.Error("rangeLabel rendered nothing for the short window")
	}

	// A filter is not a truncated snapshot. Filtering to one strategy -- the
	// ordinary way to use this page -- announced that the replica had failed to
	// publish its list, over an answer that was complete.
	for _, want := range []struct{ name, says, mustNotSay string }{
		{"filtered", "已按条件过滤掉 15 条", "没能发布完整清单"},
		{"truncated", "没能发布完整清单", "已按条件过滤"},
		{"plain", "", "条"},
	} {
		line := lineStarting(text, "TAIL "+want.name+" ::")
		if line == "" {
			t.Errorf("pageTail rendered nothing for the %s case", want.name)
			continue
		}
		if want.says != "" && !strings.Contains(line, want.says) {
			t.Errorf("pageTail %s renders %q, want it to say %q", want.name, line, want.says)
		}
		if strings.Contains(line, want.mustNotSay) {
			t.Errorf("pageTail %s renders %q, which says %q -- a different fact about the same "+
				"two numbers", want.name, line, want.mustNotSay)
		}
	}

	// A bound is not a moment. Every row in the bounded case says in its own
	// provenance column that the moment it went wrong was never recorded, and
	// the sentence above them announced one anyway.
	for _, want := range []struct{ name, says, mustNotSay string }{
		{"bounded", "那是个界不是时刻", "前开始的"},
		{"measured", "前开始的", "那是个界不是时刻"},
	} {
		line := lineStarting(text, "ONSET "+want.name+" ::")
		if line == "" {
			t.Errorf("onsetLine rendered nothing for the %s case", want.name)
			continue
		}
		if !strings.Contains(line, want.says) {
			t.Errorf("onsetLine %s renders %q, want it to say %q", want.name, line, want.says)
		}
		if strings.Contains(line, want.mustNotSay) {
			t.Errorf("onsetLine %s renders %q, which says %q", want.name, line, want.mustNotSay)
		}
	}
	// Two ends that are both bounds are not two moments; after a restart both
	// are the restart, and the sentence would report the whole deployment as
	// having gone wrong simultaneously.
	if line := lineStarting(text, "ONSET bounded ::"); strings.Contains(line, "同时开始的") {
		t.Errorf("onsetLine bounded renders %q: it read a difference between two bounds as a "+
			"difference between two moments", line)
	}

	// What each row would actually say. Executing the render proves only that
	// it does not throw, and a live page rendered a HISTORY_GAPPED row with
	// the wording for a series too short-lived to fill its window -- a
	// different situation with a different fix, rendered without complaint.
	//
	// The wording these pin changed once, deliberately: the sustained-shortfall
	// note used to lead with "窗口永远填不满" and name the cause. What is pinned
	// is the property -- each row says its own situation and does not carry
	// another's -- and that property is why the strings are here at all.
	for _, want := range []struct{ object, says, mustNotSay string }{
		{"qg-gapped-intermittent", "数据断断续续", "持续缺点"},
		{"qg-gapped-fresh", "数据刚断", "持续缺点"},
		{"qg-window-never", "持续缺点", "数据断断续续"},
		{"qg-window-starved", "取不到数据", "持续缺点"},
		{"qg-window-filling", "窗口在填", "持续缺点"},
		{"qg-window-complete", "检测窗口完整", "短"},
		// How many windows the note is about. These two reach the same verdict
		// from 1-of-999 and 2-of-3, and until the share was rendered every
		// number on both rows was the same. Pinned on both rows and with each
		// forbidding the other's share, so a share that is printed but constant
		// does not pass.
		{"qg-window-lopsided", "999 个窗口里 1 个", "3 个窗口里"},
		{"qg-window-never", "3 个窗口里 2 个", "999"},
		// A complete window under a reason that says it is gapped. Saying
		// 检测窗口完整 here is the page agreeing with the counts and silently
		// disagreeing with the reason printed beside them on the same row.
		{"qg-window-held-complete", "窗口已经补满", "检测窗口完整"},
		// The two halves of a never-filling window, and each must not carry the
		// other's wording. Every count on these two rows is the same except the
		// fresh ones, so a row that reads them and prints the same sentence for
		// both is the defect this whole pair exists to catch.
		{"qg-window-churn", "序列在不断换", "持续缺点"},
		{"qg-window-stale-data", "持续缺点", "序列在不断换"},
		// One round of every-series-fresh is a strategy edit, not churn.
		{"qg-window-rekeyed", "持续缺点", "序列在不断换"},
		{"qg-skipped", "没被检测", "持续缺点"},
		// The wording changed with the classification: CONFIG_DRIFT is no longer
		// told to the reader as a strategy being edited, because the predicate
		// behind it is also false when a live plan's StateApplyEpoch does not
		// match the round's. What is still pinned is that this row and the
		// off-hours row below say different things.
		{"qg-drift", "计划激活没对上", "不在生效时段"},
		{"qg-offhours", "不在生效时段", "策略正在被改"},
	} {
		line := ""
		for _, candidate := range strings.Split(text, "\n") {
			if strings.HasPrefix(candidate, "NOTE "+want.object+" ::") {
				line = candidate
				break
			}
		}
		if line == "" {
			t.Errorf("no note was rendered for %s, so the check that its wording matches its "+
				"reason never ran", want.object)
			continue
		}
		if !strings.Contains(line, want.says) {
			t.Errorf("%s renders %q, want it to say %q", want.object, line, want.says)
		}
		if strings.Contains(line, want.mustNotSay) {
			t.Errorf("%s renders %q, which says %q -- that is a different situation with a "+
				"different fix, and it sends the reader nowhere", want.object, line, want.mustNotSay)
		}
	}

	// Sentences that pair a number with a conclusion about that number. Every
	// one of these shipped to a live page stating something the same screen
	// contradicted, and none of them was executed by any check: the fixture
	// carried no value for the branch, so the branch never ran.
	for _, want := range []struct{ prefix, says, mustNotSay, because string }{
		{"POOL ::", "40 秒", "0 分钟",
			"过期不足一分钟时向下取整，印出的 0 分钟正是没有对象过期时的那句话"},
		{"POOL ::", "要超过这些对象自己的一个检测周期", "这个数远大于一个检测周期",
			"读数规则被当成对这个数的判定，40 秒的过期后面跟着出池路径有问题"},
		{"UNATTR gap ::", "本次判定不是停在这里", "本次判定就是停在这里",
			"有覆盖缺口时判定由缺口决定，这一栏却声称判定停在自己身上"},
		{"UNATTR gap ::", "已经计入", "",
			"这一栏和分栏等式在同一片网格里，等式只有六项而卡片有八张"},
		{"SPLIT", "横切计数", "",
			"横切的两张卡片不在等式里，页面要说出来，否则读的人会把八张卡片相加"},
		{"PARKED ::", "别相加", "",
			"此刻被拦下的数和六栏的最近一轮结果不是同一个时刻"},

		// The one loss with nothing on the page before this. It belongs to no
		// column, so it needed a line of its own or it could not be said at all.
		{"PRUNED ::", "从来没有被检测过", "",
			"这一段的时间点没有被评估过也不会补跑，页面上必须说得出来"},
		{"PRUNED ::", "1 小时 30 分", "",
			"最长的那一段要给出跨度，它是唯一能排序的量"},
		{"PRUNED ::", "无法得知", "",
			"跨度里有多少个时间点数不出来，给个数会被当成数出来的"},
		{"PRUNED ::", "上面每一栏都会这么报", "",
			"这些对象此刻正常，不说清就会被当成页面自相矛盾"},

		// The same panel in the states one fixture cannot be in at once. Each of
		// these is a branch that renders a sentence, and a branch that never ran
		// is a sentence nothing has read.
		{"VAR nogap unattr ::", "本次判定就是停在这里", "本次判定不是停在这里",
			"没有覆盖缺口时判定确实停在这一栏，两种情况必须分别说对"},
		{"VAR pool-stuck pool ::", "40 秒", "0 分钟",
			"这正是线上那一组数：进过很多、一个没出来、还在重试，而过期不足一分钟"},
		{"VAR pool-stuck pool ::", "一个都没出来过", "",
			"有成员无出口的池子要说出来，它的占用读起来和后端还没好一模一样"},
		{"VAR pool-dead pool ::", "既没出来过也没被重试过", "还在被重试",
			"没有重试的池子是出池路径停了，和后端没好是两回事"},
		{"VAR pool-empty pool ::", "本次进程还没有对象进过降级池", "一个都没出来过",
			"空池子不能和有成员的池子说同一句话"},
		{"VAR pool-exited pool ::", "最近一次出池", "",
			"出过池要给出最近一次的时间，否则只有计数无法判断出口是否还活着"},
		{"VAR overdue-present overdue ::", "最久的 900 秒", "",
			"这个量在这一栏一直是按秒印的，降级池那一栏却按分钟取整"},
		{"VAR overdue-truncated overdue ::", "不是 0，是不知道", "",
			"截断后的计数是下界，印成 0 会被读成没有对象漏掉"},
		{"VAR dispatch-off overdue ::", "结构上只可能是 0", "",
			"到期索引没启用时这里不能读成没有对象漏掉"},
		{"VAR expected-mismatch split ::", "对不上，先别信这几个数", "等于应有的",
			"分栏之和与应有对不上时必须自己说出来，这是读者唯一能发现分栏漏了一类的途径"},
		{"VAR healthy why ::", "证据齐全", "证据不全",
			"健康且没有覆盖缺口时不能还说证据不全"},
		{"VAR degraded why ::", "存在 alarmd 自己该负责的异常", "",
			"DEGRADED 要说清是 alarmd 自己的异常，否则和数据源问题分不开"},

		// What releases a held verdict, on both kinds of row that can carry one.
		// Without it the reader cannot tell "去查数据" from "再等一轮"，而这两个
		// 是相反的动作。
		{"HELD qg-window-held-complete ::", "3/3 个窗口报的不是本轮算出来的结果", "",
			"补满的窗口配着说它有洞的原因，必须说清哪一半是这一轮的"},
		{"HELD qg-window-held-complete ::", "再跑一到两轮", "",
			"这一条的下一步是等压制解除，不是去查数据"},
		{"HELD qg-window-held-short ::", "1/4 个窗口报的不是本轮算出来的结果", "",
			"仍然短的行里，对点数的结论成立而它下面那个原因未必成立"},

		// 被挡回 by cause. The old wording named one remedy -- grow the queue --
		// for a number that is mostly the branch more room cannot change.
		{"ROT only-not-better ::", "扩队列不会改变这个数", "扩队列有用",
			"全是排序结果时不能让人去扩队列"},
		{"ROT only-queue-full ::", "这一种扩队列有用", "扩队列不会改变这个数",
			"全是没位置时扩队列确实有用，而且轮转会就地停下"},
		{"ROT mixed ::", "就绪队列没位置 12 次", "",
			"两种都有时先给出该看的那个数，不是只给总数"},
		{"ROT unsplit ::", "副本没有报告是哪一种原因", "扩队列有用",
			"副本没报告成因时说不出该不该处理，猜一个比不说更糟"},

		// Whether there are enough places, answered with an instrument that can
		// see the answer. The two gauges cannot: a caller queueing behind a full
		// budget begins and ends between two reads.
		{"PERMIT mostly-waited ::", "56%", "没有查询在排队",
			"多数查询等过位子时不能因为此刻队列空就说没有排队"},
		{"PERMIT mostly-waited ::", "位子是约束", "",
			"占比过半要直接给出结论，不能让运维自己算"},
		{"PERMIT never-waited ::", "没有一次排过队", "位子是约束",
			"一次都没等过才是真的有余量，这一支要和上一支说相反的话"},
		{"PERMIT nothing-asked ::", "无从谈起", "不是约束",
			"零次取位子里零次等待不是有余量，是什么都没量到"},
	} {
		line := lineStarting(text, want.prefix)
		if line == "" {
			t.Errorf("no %s line was rendered, so the check on its wording never ran", want.prefix)
			continue
		}
		if !strings.Contains(line, want.says) {
			t.Errorf("%s renders %q, want it to say %q -- %s", want.prefix, line, want.says, want.because)
		}
		if want.mustNotSay != "" && strings.Contains(line, want.mustNotSay) {
			t.Errorf("%s renders %q, which says %q -- %s", want.prefix, line, want.mustNotSay, want.because)
		}
	}
}

// lineStarting returns the harness line with this prefix, or "" if the render
// emitted none -- which is itself a result, and a different one from a line
// that came out empty.
func lineStarting(text, prefix string) string {
	for _, candidate := range strings.Split(text, "\n") {
		if strings.HasPrefix(candidate, prefix) {
			return candidate
		}
	}
	return ""
}

// windowNeverFillsCases is the table both copies of the rule are run over: the
// Go verdict travels with each case so the harness compares against it rather
// than against a second Go expression, which would agree with the first
// whatever the page did.
//
// Every branch of Persistent appears, including the two that are false for
// different reasons -- nothing short, and short with no requirement recorded.
// A table of only true cases passes against a rule that returns true always.
func windowNeverFillsCases() []map[string]any {
	subjects := []fleet.HistoryCoverage{
		{Levels: 3},
		{Levels: 3, Short: 1, WorstValid: 8, WorstRequired: 9, ShortRounds: 2},
		{Levels: 3, Short: 1, WorstValid: 8, WorstRequired: 9, ShortRounds: 9},
		{Levels: 3, Short: 1, WorstValid: 8, WorstRequired: 9, ShortRounds: 10},
		{Levels: 3, Short: 2, WorstValid: 2, WorstRequired: 14, ShortRounds: 40},
		{Levels: 3, Short: 1, ShortRounds: 40},
		// Empty windows. Without these the two copies of the rule agree on
		// every case whatever either of them says about Empty, so the whole
		// comparison would pass over a page that had not been updated at all.
		{Levels: 3, Short: 1, Empty: 1, WorstRequired: 14, ShortRounds: 40, EmptyRounds: 40},
		{Levels: 3, Short: 1, Empty: 1, WorstRequired: 14, ShortRounds: 40, EmptyRounds: 2},
		{Levels: 3, Short: 2, Empty: 1, WorstValid: 2, WorstRequired: 14, ShortRounds: 40, EmptyRounds: 40},
		// Fresh series. Every branch of Churning, including the three that are
		// false for different reasons: every short window fresh but not for long
		// enough (the round after a StateGeneration change looks exactly like
		// this), a mix where one short window did load history, and none fresh
		// at all. Without the false cases the comparison passes against a page
		// rule that answers true whenever anything is fresh.
		{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9, ShortRounds: 40,
			Fresh: 4, ShortFresh: 4, FreshRounds: 40},
		{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9, ShortRounds: 40,
			Fresh: 9, ShortFresh: 4, FreshRounds: 1},
		{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9, ShortRounds: 40,
			Fresh: 3, ShortFresh: 3, FreshRounds: 40},
		{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9, ShortRounds: 40},
		// Fresh and short for a long run, but still filling by the shortfall
		// rule. Churn must not be claimed here: it is a half of never-filling,
		// and on its own it would send a reader to edit a strategy whose series
		// are only young.
		{Levels: 9, Short: 4, WorstValid: 8, WorstRequired: 9, ShortRounds: 2,
			Fresh: 4, ShortFresh: 4, FreshRounds: 40},
	}
	cases := make([]map[string]any, 0, len(subjects))
	for _, subject := range subjects {
		cases = append(cases, map[string]any{
			"coverage": subject, "persistent": subject.Persistent(), "starved": subject.Starved(),
			"churning": subject.Churning(),
		})
	}
	return cases
}

// smokeHarness stubs just enough DOM for the render functions and calls them.
// It is deliberately small: a fuller emulator would be a second implementation
// to maintain, and the failure being caught here needs nothing more than a real
// call stack.
// It also runs the one rule the page deliberately duplicates -- "can this
// window ever fill" -- against the verdicts the Go side computed, so the two
// copies are checked by execution rather than by a substring.
const smokeHarness = `
const fs = require('fs'), vm = require('vm'), path = require('path');
const dir = process.argv[2];
const html = fs.readFileSync(path.join(dir, 'page.html'), 'utf8');
const data = JSON.parse(fs.readFileSync(path.join(dir, 'fixture.json'), 'utf8'));
const script = html.slice(html.indexOf('<script>') + 8, html.lastIndexOf('</script>'));

function el(tag) {
  const n = {tag, className: '', title: '', value: '', hidden: false, style: {}, children: [], _t: ''};
  Object.defineProperty(n, 'textContent', {get() { return n._t; }, set(v) { n._t = String(v); n.children = []; }});
  n.appendChild = c => { n.children.push(c); return c; };
  n.addEventListener = () => {}; n.setAttribute = () => {}; n.removeAttribute = () => {}; n.remove = () => {};
  n.querySelector = () => el('div'); n.querySelectorAll = () => [];
  n.classList = {add(){}, remove(){}, toggle(){}, contains(){return false;}};
  n.focus = () => {}; n.click = () => {};
  return n;
}
// The full rendered text of a node, children included. A node's textContent
// here is only what was assigned to it directly, and every line the impact
// block builds is assembled out of appended children.
function textOf(node) {
  if (!node) { return ''; }
  return (node._t || '') + (node.children || []).map(textOf).join('');
}
const store = {};
const document = {getElementById: id => store[id] || (store[id] = el('div')), createElement: el,
  createTextNode: t => { const n = el('#text'); n.textContent = t; return n; },
  createElementNS: () => el('svg'), querySelector: () => el('div'), querySelectorAll: () => [],
  addEventListener: () => {}, body: el('body'), documentElement: el('html'), readyState: 'complete'};
// A clock the harness advances on purpose.
//
// Every cumulative figure in the capacity panel becomes a rate from two reads,
// and which branch runs depends entirely on how far apart those reads are:
// zero apart gives no rate at all, far enough apart with the counter unmoved
// gives a rate of zero. Those are different code paths and only the second one
// has ever thrown. Left on the real clock, three renders inside one millisecond
// take the first path and the check passes without executing the branch it was
// written for -- which is what happened when this was first written.
let clockMs = Date.UTC(2026, 8, 14, 10, 0, 0);
class FakeDate extends Date {
  constructor(...args) { if (args.length === 0) { super(clockMs); } else { super(...args); } }
  static now() { return clockMs; }
}
const ctx = {document, console, JSON, Date: FakeDate, Math, Object, Array, String, Number, Boolean, RegExp,
  Error, Promise, isFinite, isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
  setInterval: () => 0, clearInterval: () => {}, setTimeout: () => 0, clearTimeout: () => {},
  fetch: () => new Promise(() => {}),
  location: {href: 'http://x/alarmd/', search: '', pathname: '/alarmd/', origin: 'http://x'},
  localStorage: {getItem: () => null, setItem: () => {}, removeItem: () => {}},
  history: {replaceState(){}, pushState(){}}, URL, URLSearchParams, navigator: {userAgent: 'node'}};
ctx.window = ctx; ctx.globalThis = ctx; ctx.self = ctx;
vm.createContext(ctx);

// The whole script, including its top-level wiring. Nothing is stripped: a
// filter that drops lines cannot see that a statement spans several of them,
// and stripping the first line of one leaves its closing brace behind.
try { vm.runInContext(script, ctx, {filename: 'page.js'}); }
catch (e) { console.error('the page did not even load: ' + e.constructor.name + ': ' + e.message); process.exit(1); }

// Prove the harness can fail before trusting that it did not.
try {
  vm.runInContext('anomalyRow(undefinedRowVariable)', ctx);
  console.error('negative control did not throw; a clean result here would mean nothing');
  process.exit(1);
} catch (e) { console.log('negative control threw ' + e.constructor.name); }

const calls = [
  ['anomalyRow (every row shape)', () => data.anomalies.forEach(r => ctx.anomalyRow(r))],
  ['renderDeployment', () => ctx.renderDeployment(data.health)],
  ['renderSummary', () => ctx.renderSummary(data.summary, data.page.total)],
  ['renderGovernance', () => ctx.renderGovernance(data.action_required_total, data.by_owner_total, 'action_required')],
  ['renderRollup', () => ctx.renderRollup(data.summary, data.page.total)],
  ['renderReplicas', () => ctx.renderReplicas(data.per_replica)],
  ['renderCoverage', () => ctx.renderCoverage(data.coverage)],
  // Twice, with the counters unmoved between the two.
  //
  // Every cumulative figure in this panel becomes a rate from two reads, so one
  // call only ever exercises the "no previous read" path -- which is why a
  // divide-by-a-null-rate shipped. A second call with identical counters is
  // what a fast refresh is: the counter has not moved, the rate is zero, and
  // the code that divides by it runs.

  ['continuityLine', () => ctx.continuityLine(data.per_replica)],
  ['attributionLine', () => ctx.attributionLine(data.summary, data.per_replica)],
  ['onsetLine', () => ctx.onsetLine(data.summary.onset, data.page.total)],
];
let failed = 0;
for (const [name, fn] of calls) {
  try { fn(); } catch (e) { failed++; console.error(name + ': ' + e.constructor.name + ': ' + e.message); }
}

// The page's own copy of the rule, run against the verdicts Go computed. A
// field renamed on one side makes the page read undefined, the comparison
// false, and every permanently short window render as "still filling" -- with
// no error anywhere. Only running both can see it.
console.log('SPLIT ' + (store['splitBasis'] ? store['splitBasis'].textContent : '(not rendered)'));

// Three sentences a reader acts on that no check executed. Each of them is
// assembled from a count plus a conclusion about that count, and each of them
// shipped saying something the same screen contradicted.
console.log('POOL :: ' + (store['poolFlowHint'] ? store['poolFlowHint'].textContent : '(not rendered)'));
console.log('UNATTR gap :: ' + (store['unattributedHint'] ? store['unattributedHint'].textContent : '(not rendered)'));
console.log('WHY gap :: ' + (store['why'] ? store['why'].textContent : '(not rendered)'));
console.log('PARKED :: ' + (store['overdueHint'] ? store['overdueHint'].textContent : '(not rendered)'));
console.log('PRUNED :: ' + (store['prunedSkips'] ? store['prunedSkips'].textContent : '(not rendered)'));

// The note each row would actually render, emitted for the Go side to check.
// Executing anomalyRow only proves the page does not throw; the wording is
// what a reader acts on, and a row can render the wrong explanation
// perfectly happily.
for (const row of data.anomalies) {
  // Every row, not only the ones carrying coverage. The note is decided by the
  // reason first and the counts second, so a filter on coverage skips exactly
  // the rows whose wording comes from the reason alone -- and the check then
  // reports "no note rendered" for a row that renders one perfectly well.
  let note;
  try { note = ctx.coverageNote(row); }
  catch (e) { console.error('coverageNote threw on ' + row.query_group + ': ' + e.message); failed++; continue; }
  console.log('NOTE ' + row.query_group + ' :: ' + (note ? note.text : '(none)'));
  // The four answers the row leads with, read off the rendered cells. This is
  // what a reader acts on now; the note above is one click further in.
  let tr;
  try { tr = ctx.anomalyRow(row); }
  catch (e) { console.error('anomalyRow threw on ' + row.query_group + ': ' + e.message); failed++; continue; }
  const cells = tr.children.map(textOf);
  console.log('ROW ' + row.query_group + ' :: ' + cells.slice(1, 5).join(' | '));
  // The tooltip too, for rows carrying a held verdict. The headline cannot fit
  // what releases the hold, and that is the part that says whether to go and
  // look at the data or wait a round -- opposite actions off one row.
  if (note && row.coverage && row.coverage.guarded) {
    console.log('HELD ' + row.query_group + ' :: ' + note.title.replace(/\n/g, ' '));
  }
}

// What the "whose problem is this" line says on each column. Three of the four
// columns are held out of the verdict, and this sentence used to tell a reader
// the opposite on all three -- served the demoted pool it said "判定就看这个数"
// under a heading explaining that the pool does not reach the judgment at all.
for (const column of ['anomalies', 'demoted', 'undecidable', 'by_design']) {
  let line;
  try { line = ctx.attributionLine(data.summary, data.per_replica, column); }
  catch (e) { console.error('attributionLine threw on ' + column + ': ' + e.message); failed++; continue; }
  console.log('WHOSE ' + column + ' :: ' + line);
}

// The capacity panel on a refresh that arrives after a real interval with the
// counters unmoved -- which is what "clicked refresh too fast" produces once
// the page has been open a while: the interval is real, nothing was pulled in
// it, and the rate is zero rather than absent.
//
// renderDeployment above already took the first read. The clock is advanced so
// the second one has an interval to divide by; without that the rate is absent
// instead of zero and the branch that threw never runs.
clockMs += 30000;
try { ctx.renderCapacity(data.health.capacity, []); }
catch (e) { console.error('renderCapacity (refresh, counters unmoved): ' + e.constructor.name + ': ' + e.message); failed++; }
console.log('CAPACITY :: ' + textOf(store['capCards']));
console.log('GOVERNANCE :: ' + textOf(store['governance']));

// The same panel over the rotation shapes a deployment is actually in.
//
// 被挡回 is one number produced by two branches with opposite answers: a ready
// queue with no room, which more room fixes, and a recovery queue whose objects
// are all due sooner, which the dispatcher itself calls a decision rather than a
// lack of room. Which one dominates decides whether there is anything to do, so
// each shape has to be rendered and read.
const permitShapes = {
  'mostly-waited': {permit_acquires: 40000, permit_waits: 22400, waiting: 0},
  'never-waited': {permit_acquires: 40000, permit_waits: 0, waiting: 0},
  // A replica that has not run a query yet. Zero waits out of zero asks is not
  // "there is room", it is nothing measured.
  'nothing-asked': {permit_acquires: 0, permit_waits: 0, waiting: 0},
};
for (const [name, override] of Object.entries(permitShapes)) {
  store['capCards'].textContent = '';
  try { ctx.renderCapacity(Object.assign({}, data.health.capacity, override), []); }
  catch (e) { console.error('renderCapacity (' + name + '): ' + e.message); failed++; continue; }
  console.log('PERMIT ' + name + ' :: ' + textOf(store['capCards']));
}

const rotations = {
  'mixed': {deferred: 512, deferred_queue_full: 12, deferred_not_better: 500},
  'only-not-better': {deferred: 500, deferred_queue_full: 0, deferred_not_better: 500},
  'only-queue-full': {deferred: 12, deferred_queue_full: 12, deferred_not_better: 0},
  // A replica that has not been upgraded past the split. Saying nothing about
  // the cause is right; saying the old sentence would be a guess.
  'unsplit': {deferred: 512, deferred_queue_full: 0, deferred_not_better: 0},
};
for (const [name, rotation] of Object.entries(rotations)) {
  store['capCards'].textContent = '';
  const capacity = Object.assign({}, data.health.capacity,
    {rotation: Object.assign({}, data.health.capacity.rotation, rotation)});
  try { ctx.renderCapacity(capacity, []); }
  catch (e) { console.error('renderCapacity (' + name + '): ' + e.message); failed++; continue; }
  console.log('ROT ' + name + ' :: ' + textOf(store['capCards']));
}

// The impact line -- the only thing on the page that answers "what is affected"
// rather than "how many objects".
console.log('IMPACT :: ' + textOf(store['impact']));

// The same panel in the other states a deployment is actually in.
//
// One fixture renders one branch of each sentence here, and every sentence on
// this panel is a count plus a conclusion about that count. Four of them
// shipped to a live page saying something that page contradicted, and all four
// were unexecuted for the same reason: the single fixture left the field at
// zero, so the branch never ran and nothing ever read the wording.
//
// The overrides are JSON field names on purpose. A Go-side rename that the page
// does not follow shows up here as a branch that stops rendering.
const variants = {
  // Nothing took the verdict first, so the unattributed cell is the answer.
  'nogap': {gaps: []},
  // The live state this panel exists for: a pool with members, no exits, and
  // retries still happening. The fixture's own pool has exits, so this branch
  // had never run.
  // No exits clears the exit time with it. The two are written together, so a
  // variant that zeroed one and kept the other would describe a deployment the
  // tracker cannot produce -- and a check run against an impossible state
  // proves nothing about the page.
  'pool-stuck': {demotion_entries: 309, demotion_exits: 0, demotion_extensions: 40,
                 demoted_due: 6, demoted_due_oldest_seconds: 40, last_demotion_exit: null},
  // Members, no exits, and nothing retrying either -- the way out has stopped.
  'pool-dead': {demotion_entries: 309, demotion_exits: 0, demotion_extensions: 0,
                demoted_due: 0, last_demotion_exit: null},
  'pool-empty': {demotion_entries: 0, demotion_exits: 0, demoted_due: 0, last_demotion_exit: null},
  'pool-exited': {demotion_entries: 40, demotion_exits: 7, demoted_due: 0},
  // An overdue that is minutes old rather than seconds, so the two renderers of
  // this same quantity can be compared.
  'overdue-present': {overdue: {total: 3, oldest_seconds: 900}},
  'overdue-truncated': {overdue: {total: 0, truncated: true}},
  // No dispatch suppression at all: a zero that cannot mean anything yet.
  'dispatch-off': {dispatch: null},
  // A denominator that disagrees with the columns. The page has a sentence for
  // this and nothing had ever produced it.
  'expected-mismatch': {expected: 2075},
  'healthy': {health: 'HEALTHY', gaps: [], unattributed: 0},
  'degraded': {health: 'DEGRADED', gaps: [], unattributed: 0},
};
const variantCells = ['why', 'poolFlowHint', 'unattributedHint', 'splitBasis', 'overdueHint'];
for (const [name, override] of Object.entries(variants)) {
  // Cleared first. A cell whose branch does not run this time keeps whatever
  // the previous render wrote, and the emitted line would then report the
  // previous variant's wording as this one's -- the check reading a stale value
  // and calling it a result.
  for (const id of variantCells) { document.getElementById(id).textContent = ''; }
  let view;
  try {
    view = Object.assign({}, data.health, override);
    ctx.renderDeployment(view);
  } catch (e) {
    console.error('renderDeployment (' + name + '): ' + e.constructor.name + ': ' + e.message);
    failed++;
    continue;
  }
  for (const [label, id] of [['why', 'why'], ['pool', 'poolFlowHint'],
                             ['unattr', 'unattributedHint'], ['split', 'splitBasis'],
                             ['overdue', 'overdueHint']]) {
    const said = store[id] ? store[id].textContent : '';
    console.log('VAR ' + name + ' ' + label + ' :: ' + (said || '(not rendered)'));
  }
}

// The strategy link, in the three states it has: configured and complete,
// configured but the reference carries no business id, and not configured at
// all. A wrong link is worse than none, so two of the three must render none.
for (const c of data.strategy_link_cases || []) {
  ctx.consoleBase = c.base;
  let href;
  try { href = ctx.strategyLink(c.strategy); }
  catch (e) { console.error('strategyLink threw on ' + c.name + ': ' + e.message); failed++; continue; }
  console.log('LINK ' + c.name + ' :: ' + (href || '(none)'));
}
ctx.consoleBase = '';

// The window label, on a range whose two ends are the same wall-clock time on
// two different days -- which is every 24-hour window, and rendered as a range
// of zero length.
for (const c of data.range_cases || []) {
  let label;
  try { label = ctx.rangeLabel(c.start, c.end); }
  catch (e) { console.error('rangeLabel threw on ' + c.name + ': ' + e.message); failed++; continue; }
  console.log('RANGE ' + c.name + ' :: ' + label);
}

// The count line, on the three states it has to tell apart. A filter narrowing
// the list is not a replica failing to publish it, and this used to report the
// second whenever the first happened.
for (const c of data.page_tail_cases || []) {
  let tail;
  try { tail = ctx.pageTail(c.objects, c.total); }
  catch (e) { console.error('pageTail threw on ' + c.name + ': ' + e.message); failed++; continue; }
  console.log('TAIL ' + c.name + ' :: ' + tail);
}

// The onset sentence, on a population whose start times are bounds. It used to
// read the newest of them back as a moment.
for (const c of data.onset_cases || []) {
  let line;
  try { line = ctx.onsetLine(c.onset, c.total); }
  catch (e) { console.error('onsetLine threw on ' + c.name + ': ' + e.message); failed++; continue; }
  console.log('ONSET ' + c.name + ' :: ' + line);
}

const cases = data.window_never_fills_cases || [];
if (cases.length === 0) {
  console.error('no windowNeverFills cases were sent; the comparison would pass vacuously');
  failed++;
} else {
  let disagreed = 0;
  for (const c of cases) {
    let page, starved, churn;
    try {
      page = ctx.windowNeverFills(c.coverage);
      starved = ctx.windowIsStarved(c.coverage);
      churn = ctx.windowSeriesChurn(c.coverage);
    }
    catch (e) { console.error('rule threw: ' + e.message); failed++; break; }
    if (!!churn !== !!c.churning) {
      disagreed++;
      console.error('windowSeriesChurn disagrees with Churning on ' + JSON.stringify(c.coverage) +
        ': page ' + !!churn + ', Go ' + !!c.churning);
    }
    // Churn is a half of never-filling, never a third state beside it. A page
    // that reported churn on a window still filling would tell a reader to
    // edit a strategy whose series are merely young.
    if (churn && !page) {
      disagreed++;
      console.error('a window is reported as churning without being one that never fills: ' +
        JSON.stringify(c.coverage));
    }
    if (!!page !== !!c.persistent) {
      disagreed++;
      console.error('windowNeverFills disagrees with Persistent on ' + JSON.stringify(c.coverage) +
        ': page ' + !!page + ', Go ' + !!c.persistent);
    }
    if (!!starved !== !!c.starved) {
      disagreed++;
      console.error('windowIsStarved disagrees with Starved on ' + JSON.stringify(c.coverage) +
        ': page ' + !!starved + ', Go ' + !!c.starved);
    }
    if (page && starved) {
      disagreed++;
      console.error('a window is reported as both never-filling and starved: ' + JSON.stringify(c.coverage));
    }
  }
  if (disagreed) { failed++; }
  else { console.log('windowNeverFills agreed on ' + cases.length + ' cases'); }
}
process.exit(failed ? 1 : 0);
`
