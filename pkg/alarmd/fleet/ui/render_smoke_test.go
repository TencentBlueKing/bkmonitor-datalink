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
		"health": fleet.HealthResponse{
			// The columns add up to Covered on purpose: the page prints that
			// equation and it is the only thing a reader has that says the
			// split is complete. A fixture that does not add up cannot tell a
			// page that dropped a column from one that is fine.
			Health: "HEALTHY", Covered: 979, Determined: 979, Unknown: 0, Healthy: 844,
			AnomaliesTotal: 86, DemotedTotal: 33, UndecidableTotal: 12, ByDesignTotal: 4,
			DemotedDue: 2, DemotionEntries: 40, DemotionExits: 7, PerReplica: replicas,
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

	// What each row would actually say. Executing the render proves only that
	// it does not throw, and a live page rendered a HISTORY_GAPPED row with
	// the wording for a series too short-lived to fill its window -- a
	// different situation with a different fix, rendered without complaint.
	for _, want := range []struct{ object, says, mustNotSay string }{
		{"qg-gapped-intermittent", "数据断断续续", "窗口永远填不满"},
		{"qg-gapped-fresh", "数据刚断", "窗口永远填不满"},
		{"qg-window-never", "窗口永远填不满", "数据断断续续"},
		{"qg-window-starved", "取不到数据", "窗口永远填不满"},
		{"qg-window-filling", "窗口在填", "窗口永远填不满"},
		{"qg-window-complete", "检测窗口完整", "短"},
		{"qg-skipped", "没被检测", "窗口永远填不满"},
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
