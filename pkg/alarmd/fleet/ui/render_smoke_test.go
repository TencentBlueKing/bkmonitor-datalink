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
	// The first screen, folded from the same rows by the same Go code the route
	// uses, so the sentences the page renders are checked against counts that
	// cannot drift from what the server sends. Three objects nobody can speak
	// for and a stale replica, so the observation-gap line has both a fold with
	// objects and one without.
	checks := fleet.ReportChecks([][]fleet.Anomaly{rows}, nil,
		&fleet.View{Unknown: 3, Gaps: []fleet.Gap{{Kind: fleet.GapSnapshotStale, Replica: "pod-b"}}})
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
		"checks":    checks,
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

	// The first screen. Checks this reader acts on, in one sentence each with
	// the counts substituted; the ones already somebody else's under the fold.
	// The fixture has one stalled object, two refused queries (one in the
	// cooldown pool, one not), a backend timing out, a churning strategy, and
	// four objects nobody can speak for: three the view holds undetermined and
	// one restored without its cause, under three folds with the stale replica.
	todo := lineStarting(text, "CHECKS ::")
	for _, want := range []string{"1 个对象的轮次不再结束", "alarmd", "后端拒绝了 2 个对象的查询", "待确认",
		"1 个对象因 alarmd 自己的容量限制放弃了检测", "4 个对象现在说不出结论（3 种原因）"} {
		if !strings.Contains(todo, want) {
			t.Errorf("the checks do not say %q:\n%s", want, todo)
		}
	}
	for _, mustNot := range []string{"数据侧", "策略侧", "查询后端没有应答"} {
		if strings.Contains(todo, mustNot) {
			t.Errorf("the to-do checks say %q: that is somebody else's confirmed work, and it belongs "+
				"under the governance fold, not on the reader's list:\n%s", mustNot, todo)
		}
	}
	governance := lineStarting(text, "GOV ::")
	for _, want := range []string{"查询后端没有应答，1 个对象受影响", "数据侧", "序列活不过检测窗口", "策略侧",
		"老序列在缺点"} {
		if !strings.Contains(governance, want) {
			t.Errorf("the governance fold does not say %q:\n%s", want, governance)
		}
	}
	brief := lineStarting(text, "BRIEF ::")
	for _, want := range []string{"执行情况：没有对象到期没跑", "需要处理：", "类问题，影响", "观测完整性：1 处覆盖缺口"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief does not say %q:\n%s", want, brief)
		}
	}
	// Opening a check lists its folds with their counts; a fold with no objects
	// says so rather than offering an empty table.
	groups := lineStarting(text, "GROUPS ::")
	for _, want := range []string{"UNDETERMINED · 3 个对象", "RESTORED_WITHOUT_CAUSE · 1 个对象",
		"SNAPSHOT_STALE · 副本级缺口，没有可列的对象"} {
		if !strings.Contains(groups, want) {
			t.Errorf("the groups of OBSERVATION_GAP do not say %q:\n%s", want, groups)
		}
	}

	// The four dimensions on each row, as values. No sentence on the row
	// decides anything; the row says what the object is doing, how its last
	// round ended, since when, and what its windows hold.
	for _, want := range []struct{ object, now, result, window string }{
		{"qg-stalled", "轮次不结束", "完成 · COMPLETED_WITH_UNAVAILABLE", "—"},
		{"qg-cooldown", "冷却中", "失败 · QUERY_UNAVAILABLE", "—"},
		{"qg-rejected", "冷却中", "被拒绝 · QUERY_UNAVAILABLE", "—"},
		{"qg-blocked", "在跑", "失败 · source_blocked", "—"},
		{"qg-offhours", "生效时段外", "完成 · EFFECTIVE_TIME_INACTIVE", "—"},
		{"qg-window-churn", "在跑", "完成 · HISTORY_WARMING", "9 个窗口 · 短 4 · 空 0 · 新 4 · 连续 40 轮"},
		{"qg-window-starved", "在跑", "完成 · HISTORY_WARMING", "3 个窗口 · 短 2 · 空 2 · 新 0 · 连续 40 轮"},
		{"qg-plain", "在跑", "完成 · COMPLETED_WITH_UNAVAILABLE", "—"},
	} {
		line := lineStarting(text, "ROW "+want.object+" ::")
		if line == "" {
			t.Errorf("no row was rendered for %s", want.object)
			continue
		}
		cells := strings.Split(strings.TrimPrefix(line, "ROW "+want.object+" :: "), " | ")
		if len(cells) < 4 {
			t.Errorf("%s renders %d cells, want at least 4: %q", want.object, len(cells), line)
			continue
		}
		for index, part := range []string{want.now, want.result, "", want.window} {
			if part != "" && cells[index] != part {
				t.Errorf("%s cell %d = %q, want %q", want.object, index, cells[index], part)
			}
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
		// The first sentence of the brief, in the states the overdue cell has.
		{"VAR overdue-present brief ::", "3 个对象到期没跑，最久 900 秒", "",
			"有到期没跑的对象时第一句要给出个数和最久多少秒"},
		{"VAR overdue-truncated brief ::", "数不出来", "没有对象到期没跑",
			"截断时不能说没有"},
		{"VAR dispatch-off brief ::", "说不出是否按时", "没有对象到期没跑",
			"到期索引没启用时不能说按时"},

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
  vm.runInContext('objectRow(undefinedRowVariable)', ctx);
  console.error('negative control did not throw; a clean result here would mean nothing');
  process.exit(1);
} catch (e) { console.log('negative control threw ' + e.constructor.name); }

const calls = [
  ['objectRow (every row shape)', () => data.anomalies.forEach(r => ctx.objectRow(r))],
  ['renderDeployment', () => ctx.renderDeployment(data.health)],
  ['renderChecks', () => ctx.renderChecks(data.checks)],
  ['renderReplicas', () => ctx.renderReplicas(data.per_replica)],
  ['renderCoverage', () => ctx.renderCoverage(data.coverage)],
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

// The first screen and the fold under it, as rendered.
console.log('CHECKS :: ' + textOf(store['checkRows']));
console.log('GOV :: ' + textOf(store['govRows']));
console.log('BRIEF :: ' + ['briefSchedule', 'briefTodo', 'briefBlind'].map(id => textOf(store[id])).join(' | '));
// Opening a line renders its folds.
ctx.openCheck = 'OBSERVATION_GAP';
ctx.renderChecks(data.checks);
console.log('GROUPS :: ' + textOf(store['groups']));
ctx.openCheck = '';

// The four dimensions each row shows, read off the rendered cells.
for (const row of data.anomalies) {
  let tr;
  try { tr = ctx.objectRow(row); }
  catch (e) { console.error('objectRow threw on ' + row.query_group + ': ' + e.message); failed++; continue; }
  const cells = tr.children.map(textOf);
  console.log('ROW ' + row.query_group + ' :: ' + cells.slice(1, 5).join(' | '));
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
const variantCells = ['why', 'poolFlowHint', 'unattributedHint', 'splitBasis', 'overdueHint', 'briefSchedule'];
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
                             ['overdue', 'overdueHint'], ['brief', 'briefSchedule']]) {
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

process.exit(failed ? 1 : 0);
`
