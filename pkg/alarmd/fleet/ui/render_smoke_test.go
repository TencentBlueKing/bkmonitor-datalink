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
	fixture := map[string]any{
		"anomalies": rows,
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
			WindowNeverFills: 1,
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
			Health: "HEALTHY", Covered: 979, Determined: 979, Unknown: 0, Healthy: 844,
			AnomaliesTotal: 86, DemotedTotal: 33, UndecidableTotal: 12, ByDesignTotal: 4,
			DemotedDue: 2, DemotionEntries: 40, DemotionExits: 7, PerReplica: replicas,
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

	// Only one column decides the verdict, and the line that says so was printed
	// on all four. A reader paging the demoted pool was told, of objects the
	// page had just finished excluding from the judgment, that alarmd is
	// answerable for them and that the judgment follows the count.
	for _, want := range []struct{ column, says, mustNotSay string }{
		{"anomalies", "判定就看这个数", "整栏不进部署判定"},
		{"demoted", "整栏不进部署判定", "判定就看这个数"},
		{"undecidable", "整栏不进部署判定", "判定就看这个数"},
		{"by_design", "整栏不进部署判定", "判定就看这个数"},
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
		{"qg-skipped", "没被检测", "持续缺点"},
		{"qg-drift", "策略正在被改", "不在生效时段"},
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
	}
	cases := make([]map[string]any, 0, len(subjects))
	for _, subject := range subjects {
		cases = append(cases, map[string]any{
			"coverage": subject, "persistent": subject.Persistent(), "starved": subject.Starved(),
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
const ctx = {document, console, JSON, Date, Math, Object, Array, String, Number, Boolean, RegExp,
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
  ['renderRollup', () => ctx.renderRollup(data.summary, data.page.total)],
  ['renderReplicas', () => ctx.renderReplicas(data.per_replica)],
  ['renderCoverage', () => ctx.renderCoverage(data.coverage)],
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

// The impact line -- the only thing on the page that answers "what is affected"
// rather than "how many objects".
console.log('IMPACT :: ' + textOf(store['impact']));

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
    let page, starved;
    try { page = ctx.windowNeverFills(c.coverage); starved = ctx.windowIsStarved(c.coverage); }
    catch (e) { console.error('rule threw: ' + e.message); failed++; break; }
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
