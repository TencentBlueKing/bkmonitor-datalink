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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// The second page is served beside the first, under the same root, with
// and without the trailing slash; nothing else at the root is.
func TestTheSecondPageIsServedBesideTheFirst(t *testing.T) {
	for _, target := range []string{"/v2", "/v2/", "/v2.html"} {
		response := get(t, target)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "策略视图") {
			t.Fatalf("%s = %d, want the second page", target, response.Code)
		}
	}
	if response := get(t, "/"); !strings.Contains(response.Body.String(), "alarmd 对象与判定") {
		t.Fatal("the first page moved")
	}
	if response := get(t, "/v3"); response.Code != http.StatusNotFound {
		t.Fatalf("/v3 = %d, want 404", response.Code)
	}
}

// The page carries no vocabulary: every word it shows comes from the
// server's words, so the script holds no table mapping a code to a
// sentence. Held by the shape a table would have -- a quoted upper-case
// code as an object key -- which is how the first page's fifty tables all
// look.
func TestTheSecondPageHoldsNoWordTable(t *testing.T) {
	script := string(pageV2)
	script = script[strings.Index(script, "<script>"):]
	tableKey := regexp.MustCompile(`(?m)^\s*'?[A-Z][A-Z_]{3,}'?\s*:\s*'`)
	if hits := tableKey.FindAllString(script, -1); len(hits) > 0 {
		t.Fatalf("the second page holds a word table: %q", hits)
	}
}

// The render functions run over fixtures built from the Go types the API
// sends, so the shapes cannot drift; and the rendered text carries no
// English constant -- the code words stay in titles and JSON.
func TestTheSecondPageRendersWithoutThrowingOrLeakingCodes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH, so the second page's logic is NOT executed by this run")
	}
	at := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	since := at.Add(-2 * time.Hour)
	lastGood := at.Add(-90 * time.Minute)
	words := fleet.ProductWords()
	standing := fleet.Standing{State: fleet.StateDataAbsent, Action: fleet.ActionDataCheck, Check: fleet.CheckWindowUndecided, RefinedBy: fleet.RuleWindowVerdict}
	waiting := fleet.Standing{State: fleet.StateResultUntrusted, Action: fleet.ActionWatch, Watch: fleet.WatchWindowFilling, Check: fleet.CheckSeriesDataMissing, RefinedBy: fleet.RuleWatch}
	lines := []fleet.StrategyLine{
		{StrategyID: "4101", BusinessID: "7", Standing: standing, Objects: 2, Since: &since, SinceFrom: fleet.SinceProcessStart, SinceBasis: fleet.SinceAtLeast,
			LastGoodAt: &lastGood, DecidingObject: "qg-window-abcdef", Line: "策略 4101 · 2 个对象 · 数据没到 · 数据负责人查 · 最差窗口 6/9，缺的 3 分钟查询都正常返回、序列不在结果里"},
		{StrategyID: "4102", BusinessID: "7", Standing: waiting, Objects: 1, Since: &since, SinceFrom: fleet.SinceBusinessState, SinceBasis: fleet.SinceExact,
			DecidingObject: "qg-window-abcdef", Line: "策略 4102 · 1 个对象 · 检测结果不能采信 · 等着看 · 窗口在补 · 最差窗口 8/9"},
	}
	list := fleet.StrategyListResponse{Words: words, Strategies: lines, Summary: fleet.SummarizeStrategyLines(lines), Total: 2, Listed: 2}
	expected := 40
	health := fleet.HealthResponse{Health: fleet.HealthDegraded, Expected: &expected, Determined: 40,
		Builds: []fleet.BuildGroup{{Build: fleet.BuildFacts{Version: "0.2.0", Commit: "abcdef1234"}, Replicas: []string{"pod-a"}}}}
	row := fleet.Anomaly{QueryGroup: "qg-window-abcdef", Since: since, SinceFrom: fleet.SinceProcessStart, CauseReason: "HISTORY_GAPPED",
		Standing: &standing, Finding: fleet.Finding{Check: fleet.CheckWindowUndecided},
		Coverage: &fleet.HistoryCoverage{Levels: 2, Short: 1, WorstValid: 6, WorstRequired: 9, WorstWindow: "s/1", WorstWindowChanged: true,
			Windows: []fleet.WindowRow{{Key: "series-one/1", Series: "series-one", Level: 1, Valid: 6, Required: 9, End: at, GuardReason: "CONFIG_DRIFT",
				Holes: []fleet.WindowHole{{At: at.Add(-3 * time.Minute), Cause: fleet.HoleAnsweredWithoutSeries, Round: "FULL_COMPLETED"},
					{At: at.Add(-2 * time.Minute), Cause: fleet.HoleInputIncomplete, Round: "COMPLETED_WITH_UNAVAILABLE", Reason: "QUERY_TIMEOUT"},
					{At: at.Add(-time.Minute), Cause: fleet.HoleNotInMemory}},
				MissingTotal: 3, Verdict: fleet.VerdictInputIncomplete, HolesBy: fleet.WindowHoleCounts{AnsweredWithoutSeries: 1, InputIncomplete: 1, NotInMemory: 1}}}}}
	// A second object whose windows are named while its short count is
	// zero. This shape does not reach the page from production -- the
	// evaluator names a window under the condition it counts it, and
	// normalize drops a coverage with more windows than its count -- so
	// the fixture pins the page's rule, not a drift that happened: the
	// block is gated on the windows being there, never on the count.
	named := fleet.Anomaly{QueryGroup: "qg-window-fedcba", Since: since, SinceFrom: fleet.SinceBusinessState, CauseReason: "HISTORY_GAPPED",
		Standing: &standing, Finding: fleet.Finding{Check: fleet.CheckWindowUndecided},
		Coverage: &fleet.HistoryCoverage{Levels: 1,
			Windows: []fleet.WindowRow{{Key: "series-two/1", Series: "series-two", Level: 1, Valid: 8, Required: 9, End: at,
				Holes:         []fleet.WindowHole{{At: at.Add(-4 * time.Minute), Cause: fleet.HolePointUnusable, Round: "FULL_COMPLETED"}},
				UnusableTotal: 1, Verdict: fleet.VerdictPointsUnusable, HolesBy: fleet.WindowHoleCounts{Unusable: 1}}}}}
	// A third object shared with another strategy, whose words the server
	// says are that other Plan's: the page shows whose they are and does
	// not read them as this strategy's.
	neighbour := fleet.Anomaly{QueryGroup: "qg-shared-012345", Since: since, SinceFrom: fleet.SinceBusinessState,
		Standing: &fleet.Standing{State: fleet.StateDataAbsent, Action: fleet.ActionDataCheck, Check: fleet.CheckSeriesDataMissing, RefinedBy: fleet.RuleStalled,
			About: []fleet.StrategyRef{{StrategyID: "4102", BusinessID: "7"}, {StrategyID: "4103", BusinessID: "7"}}},
		Finding: fleet.Finding{Check: fleet.CheckSeriesDataMissing}}
	// A fourth object whose window reading the server refused: the line says
	// so in the server's words, where the windows would have been.
	refused := fleet.Anomaly{QueryGroup: "qg-refused-abcdef", Since: since, SinceFrom: fleet.SinceBusinessState, CauseReason: "HISTORY_GAPPED",
		Standing: &standing, Finding: fleet.Finding{Check: fleet.CheckWindowUndecided},
		CoverageRejected: &fleet.CoverageRejected{Rule: "WINDOW_HOLE_ARITHMETIC", Series: "series-three"}}
	// And a fifth refused under a rule about the whole set, which carries
	// no series: twelve of the eighteen rules are of this kind.
	refusedSet := fleet.Anomaly{QueryGroup: "qg-refused-fedcba", Since: since, SinceFrom: fleet.SinceBusinessState, CauseReason: "HISTORY_GAPPED",
		Standing: &standing, Finding: fleet.Finding{Check: fleet.CheckWindowUndecided},
		CoverageRejected: &fleet.CoverageRejected{Rule: "SHORT_OVER_LEVELS"}}
	// A sixth whose round described a fraction of the object: 22 windows
	// summarised out of 249 series, the rest already applied by an earlier
	// attempt of the same Slot. Without the two counts beside it, Levels = 22
	// on the page is the same shape as an object that has 22 Levels.
	partial := fleet.Anomaly{QueryGroup: "qg-partial-abcdef", Since: since, SinceFrom: fleet.SinceBusinessState, CauseReason: "HISTORY_GAPPED",
		Standing: &standing, Finding: fleet.Finding{Check: fleet.CheckWindowUndecided},
		Coverage: &fleet.HistoryCoverage{Levels: 22, Resumed: 227}}
	card := fleet.StrategyStanding{StrategyID: "4101", AnsweredBy: "pod-a", Publication: fleet.StrategyPublication{SnapshotRevision: "53b81c9bbb2e", Epoch: 9},
		Standing: fleet.StandingDetecting, Found: true, Line: "策略 4101：已生效，1 个对象在检测：qg-window-abc（pod-a 持有，数据没到·数据负责人查）",
		Plans: []fleet.StrategyPlanStanding{{StrategyPlanRef: fleet.StrategyPlanRef{QueryGroup: "qg-window-abcdef", Business: "7"}, Replica: "pod-a", Existence: "active", Rows: []fleet.Anomaly{row, named, refused, refusedSet, partial, neighbour},
			Config: &fleet.StrategyPlanConfigs{Redacted: true, Items: []fleet.StrategyPlanConfig{{
				PlanID:   "4101",
				Schedule: fleet.StrategyScheduleConfig{IntervalSeconds: 60},
				Query:    fleet.StrategyQueryConfig{Clauses: []fleet.StrategyQueryClause{{TableID: "system.cpu_summary", Field: "usage", TimeAggregation: &fleet.StrategyQueryFunction{Method: "avg_over_time", Window: "60s"}}}},
				Target:   fleet.StrategyTargetConfig{Kind: "SCOPE", Scope: &fleet.StrategyTargetScopeInfo{Groups: 1, Conditions: []fleet.StrategyTargetScopeCondition{{Field: "HOST", Method: "EQ", Keys: 36}}}},
				Levels: []fleet.StrategyLevelConfig{{LevelID: 1,
					Trigger:  fleet.StrategyTypedPlanConfig{Type: "N_OF_M", WindowSize: ptrU32(5), RequiredAnomalies: ptrU32(1)},
					Recovery: fleet.StrategyTypedPlanConfig{Type: "CONTINUOUS_TRIGGER_MISS", ConsecutiveWindows: ptrU32(5)}}},
			}}}}}}
	fixture := map[string]any{"list": list, "health": health, "card": card}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, body := range map[string][]byte{"page.html": pageV2, "fixture.json": encoded, "smoke.js": []byte(v2SmokeHarness)} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command(node, filepath.Join(dir, "smoke.js"), dir)
	command.Env = append(os.Environ(), "TZ=Asia/Shanghai", "LANG=en_GB.UTF-8", "LC_ALL=en_GB.UTF-8")
	output, err := command.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if err != nil {
		t.Fatalf("the second page threw while rendering:\n%s", text)
	}
	if !strings.Contains(text, "negative control threw") {
		t.Fatalf("the harness did not prove it can fail:\n%s", text)
	}
	rendered := text[strings.Index(text, "RENDERED ::"):]
	t.Log(rendered)
	for _, want := range []string{
		"降级", "现在要做的：策略 4101 · 2 个对象 · 数据没到 · 数据负责人查",
		"数据负责人查", "等着看", "策略 4102 · 1 个对象",
		"自 2 小时前（只会更久）", "最近一次正常 2 小时前",
		"查询正常返回，这条序列不在结果里", "本侧那一轮没查全", "超出本进程记忆", "本侧没查全",
		"每 60 秒检测一次", "查 system.cpu_summary 的 usage，avg_over_time 60s", "目标 36 个", "级别 1：5 个周期内 1 次异常触发，连续 5 个周期正常恢复",
		"最差的换了一条序列",
		// The named window of the object whose short count is zero.
		"序列 series-t：8/9，记录检测用不了；缺 ", "记录到了，检测用不了",
		// The neighbours named, with no state or action word of this row's own.
		"本策略在检测；同对象上策略 4102、4103 有它们自己的问题（见该策略）",
		// The refused readings, in the server's words: one about a window,
		// one about the whole set.
		"这一轮的窗口读数服务端判定不自洽（缺的分钟数与窗口缺口对不上），未展示",
		"这一轮的窗口读数服务端判定不自洽（短窗数多于窗口数），未展示",
		// The round that described a fraction of its object says so beside
		// the count, so 22 is not read as the object's size.
		"这一轮汇总了 22 扇窗；另有已应用过、本轮未重算 227 条——上面的窗口读数只说这一轮算过的那些，不是这个对象的全部",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendering lacks %q:\n%s", want, rendered)
		}
	}
	// The neighbour's line carries no action word: the thing to do is on the
	// neighbour's own card. The card's own action word appears in the head,
	// the lead line, the object's own line and the next step -- never on the
	// neighbour's line.
	if neighbour := rendered[strings.Index(rendered, "本策略在检测；"):]; strings.Contains(neighbour[:strings.Index(neighbour, "关键配置")], "数据负责人查") {
		t.Errorf("the neighbour's line carries an action word:\n%s", neighbour)
	}
	// The summary sentence is the count's alone: one object counts a short
	// window, so it is written once, not once per object with windows.
	if got := strings.Count(rendered, "短窗 "); got != 1 {
		t.Errorf("the short-window summary is written %d times, want once:\n%s", got, rendered)
	}
	// No code word in what a reader sees: the check, the rule, the reason,
	// the verdict code all stay in titles and the folded coordinates.
	visible := strings.TrimPrefix(rendered[:strings.Index(rendered, "COORDINATES ::")], "RENDERED ::")
	if hits := regexp.MustCompile(`[A-Z][A-Z_]{3,}`).FindAllString(visible, -1); len(hits) > 0 {
		t.Errorf("code words reached the visible text: %q", hits)
	}
}

const v2SmokeHarness = `
const fs = require('fs'), vm = require('vm'), path = require('path');
const dir = process.argv[2];
const html = fs.readFileSync(path.join(dir, 'page.html'), 'utf8');
const data = JSON.parse(fs.readFileSync(path.join(dir, 'fixture.json'), 'utf8'));
const script = html.slice(html.indexOf('<script>') + 8, html.lastIndexOf('</script>'));
function el(tag) {
  const n = {tag, className: '', title: '', hidden: false, href: '', target: '', rel: '', children: [], _t: ''};
  Object.defineProperty(n, 'textContent', {get() { return n._t; }, set(v) { n._t = String(v); n.children = []; }});
  n.appendChild = c => { n.children.push(c); return c; };
  n.addEventListener = () => {};
  return n;
}
function textOf(node, folded) {
  if (!node) return '';
  if (node.tag === 'details' && !folded) return '';
  return (node._t || '') + (node.children || []).map(c => textOf(c, folded)).join(' ');
}
function detailsOf(node) {
  if (!node) return '';
  if (node.tag === 'details') return textOf(node, true);
  return (node.children || []).map(detailsOf).join(' ');
}
const store = {};
const document = {getElementById: id => store[id] || (store[id] = el(id)), createElement: el, body: el('body')};
let clockMs = Date.UTC(2026, 8, 22, 10, 0, 0);
class FakeDate extends Date { constructor(...a) { if (a.length === 0) super(clockMs); else super(...a); } static now() { return clockMs; } }
const ctx = {document, console, JSON, Date: FakeDate, Math, Object, Array, String, Number, Boolean, RegExp, Error, Promise,
  encodeURIComponent, decodeURIComponent, setInterval: () => 0, setTimeout: () => 0, fetch: () => new Promise(() => {}),
  location: {pathname: '/alarmd/v2', hash: '#s=4101'}, window: null};
ctx.window = ctx; ctx.window.addEventListener = () => {};
vm.createContext(ctx);
try { vm.runInContext(script, ctx, {filename: 'v2.js'}); }
catch (e) { console.error('the page did not load: ' + e.message); process.exit(1); }
if (ctx.BASE !== '/alarmd/') { console.error('BASE = ' + ctx.BASE + ', want /alarmd/'); process.exit(1); }
try { vm.runInContext('renderCard(undefinedThing.x)', ctx); console.error('negative control did not throw'); process.exit(1); }
catch (e) { console.log('negative control threw ' + e.constructor.name); }
ctx.LATEST = data.list; ctx.WORDS = data.list.words;
let failed = 0;
for (const [name, fn] of [['renderTop', () => ctx.renderTop(data.list, data.health)], ['renderColumns', () => ctx.renderColumns(data.list)], ['renderCard', () => ctx.renderCard(data.card)], ['renderCard(null)', () => ctx.renderCard(null)], ['renderCard again', () => ctx.renderCard(data.card)]]) {
  try { fn(); } catch (e) { failed++; console.error(name + ': ' + e.constructor.name + ': ' + e.message); }
}
if (failed) process.exit(1);
console.log('RENDERED :: ' + [textOf(store.health), textOf(store.lead), textOf(store.meta), textOf(store.columns), textOf(store.card)].join(' | '));
console.log('COORDINATES :: ' + detailsOf(store.card));
`

func ptrU32(value uint32) *uint32 { return &value }
