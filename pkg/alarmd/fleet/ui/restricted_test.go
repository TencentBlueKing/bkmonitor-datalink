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
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/publicsurface"
)

// Words the page may only say about data it was sent. On a restricted public
// surface the lists they are read from arrive empty because they were
// withheld, and each of these then states a fact nobody observed.
var inferredFromWithheld = []string{"[object Object]", "容量或架构", "没有副本被计入", "没有副本带到期索引", "证据齐全",
	"异常明细完整", "当前没有策略因为"}

// The blocks that read withheld data, each of which must carry the notice
// and the login link in its place.
var restrictedBlocks = map[string][]string{
	"index": {"why", "impact", "buildLine", "briefSchedule", "briefBlocked", "briefBlind", "coverBasis", "repRows",
		"depBasis", "expectedHint", "noDataTrackingHint", "overdueHint", "prunedSkips", "loadLines", "capNote",
		"objErr", "trendErr"},
	"v2": {"lead", "card"},
}

// The page on a restricted public surface, driven through its own loads: the
// health route answers the real summary (fleet.PublicHealth of a full
// response carrying every withheld kind of fact), the windows route answers,
// and every other API route answers the real refusal. No block may say what
// only withheld data could say, none may print an object as text, and every
// block that reads withheld data shows the notice with the login link. The
// verdict itself is the server's and is shown as sent.
func TestTheRestrictedSurfaceShowsNoticesNotInferences(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH, so the page's logic is NOT executed by this run")
	}
	full := fleet.HealthResponse{Health: fleet.HealthDegraded, Covered: 51, Determined: 51, Healthy: 49,
		AnomaliesTotal: 2, DemotedTotal: 2, DemotionEntries: 2,
		Builds:       []fleet.BuildGroup{{Replicas: []string{"replica-a"}}},
		PerReplica:   []fleet.ReplicaView{{Replica: "replica-a"}},
		Degradations: []fleet.Degradation{{Kind: fleet.DegradationMetricsUnexported, Replica: "replica-a"}},
		Dependencies: []fleet.Endpoint{{Role: "runtime", Kind: "redis", Address: "redis.internal.example:6379"}},
		Gaps:         []fleet.Gap{{Kind: fleet.GapListTruncated, Replica: "replica-a"}},
	}
	summary, err := json.Marshal(fleet.PublicHealth(full))
	if err != nil {
		t.Fatal(err)
	}
	refused := httptest.NewRecorder()
	publicsurface.Refuse(refused, httptest.NewRequest(http.MethodGet, "/api/objects", nil))
	fixture, err := json.Marshal(map[string]json.RawMessage{
		"health":  summary,
		"refusal": refused.Body.Bytes(),
		// Any other structured error -- here the windows route, which the
		// restricted surface still serves -- reads as its words, not as an
		// object and not as the restricted notice.
		"windows": json.RawMessage(`{"status":"error","error":{"code":"window_store_unavailable","message":"window store unavailable"}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, page := range map[string][]byte{"index": page, "v2": pageV2} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for file, body := range map[string][]byte{"page.html": page, "fixture.json": fixture, "run.js": []byte(restrictedHarness)} {
				if err := os.WriteFile(filepath.Join(dir, file), body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path := "/alarmd/"
			if name == "v2" {
				path = "/alarmd/v2"
			}
			output, err := exec.Command(node, filepath.Join(dir, "run.js"), dir, path).CombinedOutput()
			if err != nil {
				t.Fatalf("the page did not run: %v\n%s", err, output)
			}
			var result struct {
				Texts map[string]string   `json:"texts"`
				Links map[string][]string `json:"links"`
				Badge string              `json:"badge"`
			}
			if err := json.Unmarshal(output[strings.LastIndex(string(output), "\n{")+1:], &result); err != nil {
				t.Fatalf("no result: %v\n%s", err, output)
			}
			for id, text := range result.Texts {
				for _, forbidden := range inferredFromWithheld {
					if strings.Contains(text, forbidden) {
						t.Errorf("%s: block %s says %q, which only withheld data could say: %s", name, id, forbidden, text)
					}
				}
			}
			for _, id := range restrictedBlocks[name] {
				if !strings.Contains(result.Texts[id], "公开面已收紧，完整数据请用 CLI 读") {
					t.Errorf("%s: block %s lacks the notice: %q", name, id, result.Texts[id])
				}
				if links := result.Links[id]; len(links) == 0 || links[0] != "/alarmd/cli" {
					t.Errorf("%s: block %s links %v, want the login page /alarmd/cli", name, id, links)
				}
			}
			if name == "index" {
				if result.Badge != "DEGRADED" || !strings.Contains(result.Texts["why"], "判定 DEGRADED 来自服务端") {
					t.Errorf("the verdict is not the server's as sent: badge %q, why %q", result.Badge, result.Texts["why"])
				}
				if !strings.Contains(result.Texts["poolExits"], "0 / 2") {
					t.Errorf("the pool flow, which is sent, did not render: %q", result.Texts["poolExits"])
				}
				if result.Texts["winErr"] != "这一块读不到：window store unavailable" {
					t.Errorf("a structured error that is not the refusal reads %q, want its words", result.Texts["winErr"])
				}
			}
		})
	}
}

const restrictedHarness = `
const fs = require('fs'), vm = require('vm'), path = require('path');
const dir = process.argv[2], pathname = process.argv[3];
const html = fs.readFileSync(path.join(dir, 'page.html'), 'utf8');
const data = JSON.parse(fs.readFileSync(path.join(dir, 'fixture.json'), 'utf8'));
const script = html.slice(html.indexOf('<script>') + 8, html.lastIndexOf('</script>'));
function el(tag) {
  const n = {tag, className: '', title: '', value: '', hidden: false, style: {}, children: [], _t: ''};
  Object.defineProperty(n, 'textContent', {get() { return n._t + n.children.map(c => c.textContent).join(''); },
    set(v) { n._t = String(v); n.children = []; }});
  n.appendChild = c => { n.children.push(c); return c; };
  n.addEventListener = () => {}; n.setAttribute = () => {}; n.removeAttribute = () => {}; n.remove = () => {};
  n.querySelector = () => el('div'); n.querySelectorAll = () => [];
  n.classList = {add(){}, remove(){}, toggle(){}, contains(){return false;}};
  n.focus = () => {}; n.click = () => {};
  return n;
}
function links(node, out) {
  if (node.tag === 'a') out.push(node.href);
  (node.children || []).forEach(c => links(c, out));
  return out;
}
const store = {};
const document = {getElementById: id => store[id] || (store[id] = el('div')), createElement: el,
  createTextNode: t => { const n = el('#text'); n.textContent = t; return n; },
  createElementNS: () => el('svg'), querySelector: () => el('div'), querySelectorAll: () => [],
  addEventListener: () => {}, body: el('body'), documentElement: el('html'), readyState: 'complete'};
function answer(url) {
  const u = String(url);
  let status = 200, body = null;
  if (u.indexOf('api/health') >= 0) body = data.health;
  else if (u.indexOf('api/windows') >= 0) { status = 503; body = data.windows; }
  else if (u.indexOf('api/') >= 0) { status = 403; body = data.refusal; }
  else status = 404;
  return Promise.resolve({ok: status < 300, status,
    text: () => Promise.resolve(body === null ? '' : JSON.stringify(body)), json: () => Promise.resolve(body)});
}
const ctx = {document, console, JSON, Date, Math, Object, Array, String, Number, Boolean, RegExp,
  Error, Promise, isFinite, isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
  setInterval: () => 0, clearInterval: () => {}, setTimeout: () => 0, clearTimeout: () => {},
  fetch: answer,
  location: {href: 'http://x' + pathname + '#s=1', search: '', hash: '#s=1', pathname, origin: 'http://x'},
  localStorage: {getItem: () => null, setItem: () => {}, removeItem: () => {}},
  history: {replaceState(){}, pushState(){}}, URL, URLSearchParams, navigator: {userAgent: 'node'}};
ctx.addEventListener = () => {};
ctx.window = ctx; ctx.globalThis = ctx; ctx.self = ctx;
vm.createContext(ctx);
vm.runInContext(script, ctx, {filename: 'page.js'});
// The second page opens a strategy's card on a hash change too, apart from the
// list; that read is driven here as the browser would drive it.
if (typeof ctx.openSelected === 'function') ctx.openSelected();
(async () => {
  for (let i = 0; i < 50; i++) { await new Promise(r => setImmediate(r)); }
  const texts = {}, found = {};
  for (const id of Object.keys(store)) { texts[id] = store[id].textContent; found[id] = links(store[id], []); }
  console.log('\n' + JSON.stringify({texts, links: found, badge: store['health'] ? store['health'].textContent : ''}));
})();
`
