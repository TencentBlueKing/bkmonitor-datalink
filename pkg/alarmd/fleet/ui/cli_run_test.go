package ui

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The authorization page's checks above read its source. This one runs its
// script against a stubbed document and fetch, so the three behaviours an
// operator depends on are executed rather than spelled: the origin check
// before the button, the fallback to the server's own sentence, and the
// answer when whatever replied was not alarmd.
func TestTheAuthorizationPageRunsItsRefusalHandling(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH, so the authorization page's script is NOT executed by this run -- " +
			"only its source was read")
	}
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, httptest.NewRequest("GET", "/cli", nil))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cli.html"), w.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.js"), []byte(cliPageHarness), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(node, filepath.Join(dir, "run.js"), filepath.Join(dir, "cli.html")).CombinedOutput()
	if err != nil {
		t.Fatalf("the page's script failed: %v\n%s", err, output)
	}
	var runs map[string]cliPageRun
	if err := json.Unmarshal(output, &runs); err != nil {
		t.Fatalf("harness output: %v\n%s", err, output)
	}
	var loops map[string]json.RawMessage
	_ = json.Unmarshal(output, &loops)
	checkLoopback(t, loops)
	run := func(name string) cliPageRun {
		t.Helper()
		result, ok := runs[name]
		if !ok {
			t.Fatalf("no run %q", name)
		}
		return result
	}

	const entry = "http://apps.example.test/alarmd/"
	if got := run("same_origin"); got.IssueDisabled || got.Error {
		t.Errorf("opened from the configured entry, generation is offered: %+v", got)
	}
	// The browser's own origin form: a default port and an upper-case host in
	// the address bar are the same origin as the entry.
	if got := run("same_origin_other_spelling"); got.IssueDisabled || got.Error {
		t.Errorf("the same origin spelled with :80 and upper case is still the entry: %+v", got)
	}
	got := run("other_origin")
	if !got.IssueDisabled || !got.Error || !strings.Contains(got.Status, "http://192.0.2.10:8080") ||
		!strings.Contains(got.Status, entry+"cli") || got.Requests != 1 {
		t.Errorf("opened from another origin, the page says where to open it and offers nothing to press: %+v", got)
	}
	if got := run("refused_after_preview"); !strings.Contains(got.Status, entry+"cli") || !got.Error {
		t.Errorf("a generation refused for its origin says the entry the preview named: %+v", got)
	}

	for _, code := range []string{"admin_unauthorized", "admin_not_configured", "auth_rate_limited",
		"auth_busy", "auth_store_unavailable", "not_found"} {
		got := run("code:" + code)
		if !got.Error || got.Status == "" || got.Status == "server sentence for "+code {
			t.Errorf("%s reaches the operator as what to do, not the server's sentence: %+v", code, got)
		}
		if got.KeyKept {
			t.Errorf("%s: a refused key stays in the field", code)
		}
	}
	// Codes the page has no line for, including ones that name a property
	// every object inherits, fall back to the server's sentence.
	for _, code := range []string{"something_new", "constructor", "toString", "__proto__"} {
		if got := run("code:" + code); got.Status != "server sentence for "+code || !got.Error {
			t.Errorf("%s falls back to the server's sentence: %+v", code, got)
		}
	}
	if got := run("code_without_sentence"); got.Status == "" || !got.Error {
		t.Errorf("a refusal with neither a known code nor a sentence still says something: %+v", got)
	}

	// A body that is not JSON and a request that never got an answer are the
	// same fact for the operator: alarmd's route did not answer.
	notJSON, network := run("not_json"), run("network_failure")
	if notJSON.Status == "" || notJSON.Status != network.Status || !notJSON.Error ||
		!strings.Contains(notJSON.Status, "/api/cli/") {
		t.Errorf("an answer that is not alarmd's names the route to check: %+v / %+v", notJSON, network)
	}
}

type cliPageRun struct {
	Status        string `json:"status"`
	Error         bool   `json:"error"`
	IssueDisabled bool   `json:"issue_disabled"`
	KeyKept       bool   `json:"key_kept"`
	Requests      int    `json:"requests"`
}

const cliPageHarness = `
'use strict';
const fs = require('fs');
const html = fs.readFileSync(process.argv[2], 'utf8');
const script = html.slice(html.indexOf('<script>') + 8, html.lastIndexOf('</script>'));

function load(pageURL, respond, loop) {
  const elements = {};
  const element = id => elements[id] || (elements[id] = {
    id, textContent: '', value: '', disabled: false, hidden: false, className: '', href: '', listeners: {},
    addEventListener(type, listener) { this.listeners[type] = listener; }, focus() {}, select() {},
  });
  let requests = 0;
  const calls = [];
  const ticks = [];
  const context = {
    document: { getElementById: element },
    location: new URL(pageURL),
    addEventListener() {},
    navigator: { clipboard: { writeText: async () => {} } },
    crypto: require('crypto').webcrypto,
    btoa: s => Buffer.from(s, 'binary').toString('base64'),
    confirm: () => true,
    setInterval: fn => { ticks.push(fn); return 0; },
    fetch: async (url, options) => {
      const target = String(url);
      calls.push({ url: target, method: (options && options.method) || 'GET', body: options && options.body });
      // The CLI on the loopback address: nothing listens unless the run says so.
      if (target.startsWith('http://127.0.0.1:')) {
        if (!loop) throw new TypeError('Failed to fetch');
        return loop(new URL(target), options);
      }
      requests++;
      return respond(url, options);
    },
  };
  new Function(...Object.keys(context), script)(...Object.values(context));
  return { elements, requests: () => requests, calls, tick: async () => { for (const fn of ticks) await fn(); } };
}

const answer = (status, body) => ({ ok: status < 400, status, json: async () => body });
const preview = { environment_id: 'ns/release', environment_name: 'ns/release',
  public_base_url: 'http://apps.example.test/alarmd/' };
const entryPage = 'http://apps.example.test/alarmd/cli';

async function inspect(pageURL, respond, then) {
  const page = load(pageURL, respond);
  const e = page.elements;
  e['admin-key'].value = 'k'.repeat(40);
  await e.inspect.listeners.click();
  if (then) await then(e);
  return { status: e.status.textContent, error: e.status.className === 'error', issue_disabled: e.issue.disabled,
    key_kept: e['admin-key'].value !== '', requests: page.requests() };
}

(async () => {
  const runs = {};
  runs.same_origin = await inspect(entryPage, () => answer(200, preview));
  runs.same_origin_other_spelling = await inspect('http://APPS.example.test:80/alarmd/cli', () => answer(200, preview));
  runs.other_origin = await inspect('http://192.0.2.10:8080/alarmd/cli', () => answer(200, preview));
  runs.refused_after_preview = await inspect(entryPage,
    (url, options) => options.method === 'GET' ? answer(200, preview)
      : answer(403, { status: 'error', error: { code: 'origin_denied', message: 'server sentence for origin_denied' } }),
    e => e.issue.listeners.click());
  for (const code of ['admin_unauthorized', 'admin_not_configured', 'auth_rate_limited', 'auth_busy',
    'auth_store_unavailable', 'not_found', 'something_new', 'constructor', 'toString', '__proto__']) {
    runs['code:' + code] = await inspect(entryPage,
      () => answer(403, { status: 'error', error: { code, message: 'server sentence for ' + code } }));
  }
  runs.code_without_sentence = await inspect(entryPage, () => answer(500, { status: 'error', error: { code: 'something_new' } }));
  runs.not_json = await inspect(entryPage,
    () => ({ ok: false, status: 404, json: async () => { throw new SyntaxError('Unexpected token <'); } }));
  runs.network_failure = await inspect(entryPage, () => { throw new TypeError('Failed to fetch'); });
  // The loopback login. The page's own state and port are read back from the
  // command it shows, as an operator would copy them.
  const settle = () => new Promise(resolve => setImmediate(resolve));
  async function loopbackRun(cli, grantAnswer) {
    let seen;
    const page = load(entryPage, (url, options) => {
      if (String(url).endsWith('/grants') && options.method === 'POST') { seen = JSON.parse(options.body); return grantAnswer(); }
      if (String(url).endsWith('/revoke-all')) return answer(200, { revoked_pairings: 3, sessions_revoked: true });
      return answer(200, preview);
    }, cli);
    const e = page.elements;
    await settle();
    const command = e['listen-command'].textContent;
    const beforePreview = e.authorize.disabled;
    e['admin-key'].value = 'k'.repeat(40);
    await e.inspect.listeners.click();
    await settle();
    const offered = !e.authorize.disabled;
    if (offered) { await e.authorize.listeners.click(); await settle(); }
    return { command, before_preview_disabled: beforePreview, offered, grant: seen || null, status: e.status.textContent,
      error: e.status.className === 'error', probe: e.probe.textContent, key_kept: e['admin-key'].value !== '',
      after_disabled: e.authorize.disabled, calls: page.calls.filter(c => c.url.startsWith('http://127.0.0.1:')) };
  }
  const challenge = 'C'.repeat(43);
  const commandState = url => url.searchParams.get('state');
  const grantOK = () => answer(200, { authorization_code: 'alarmd-login-v1.abc', environment_id: 'ns/release', grant_expires_at: '2026-09-23T09:00:00Z' });
  let callbackBody;
  runs.loopback_ok = await loopbackRun(async (url, options) => {
    if (url.pathname === '/ready') return answer(200, { state: commandState(url), code_challenge: challenge });
    callbackBody = JSON.parse(options.body);
    return answer(200, { status: 'ok', environment_id: 'ns/release' });
  }, grantOK);
  runs.loopback_ok.callback = callbackBody;
  runs.loopback_wrong_state = await loopbackRun(async url => answer(200, { state: 'someone-else', code_challenge: challenge }), grantOK);
  runs.loopback_bad_challenge = await loopbackRun(async url => answer(200, { state: commandState(url), code_challenge: 'short' }), grantOK);
  runs.loopback_none = await loopbackRun(null, grantOK);
  runs.loopback_refused = await loopbackRun(async (url) => url.pathname === '/ready'
    ? answer(200, { state: commandState(url), code_challenge: challenge })
    : answer(409, { status: 'error', error: { code: 'already_completed', message: 'server sentence' } }), grantOK);
  runs.loopback_gone = await loopbackRun(async (url) => { if (url.pathname === '/ready') return answer(200, { state: commandState(url), code_challenge: challenge }); throw new TypeError('Failed to fetch'); }, grantOK);
  runs.loopback_grant_refused = await loopbackRun(async (url) => url.pathname === '/ready'
    ? answer(200, { state: commandState(url), code_challenge: challenge }) : answer(200, {}),
    () => answer(403, { status: 'error', error: { code: 'admin_unauthorized', message: 'server sentence' } }));
  {
    // Nothing answers on the loopback address: after five probes the page
    // says the browser may be blocking it and opens the copy fallback.
    const page = load(entryPage, () => answer(200, preview), null);
    await settle();
    // An element the script never touched keeps the page's own state: hidden.
    const hidden = () => !(page.elements.manual && page.elements.manual.hidden === false);
    const early = { probe: page.elements.probe.textContent, manual_hidden: hidden() };
    for (let i = 0; i < 4; i++) await page.tick();
    runs.blocked = { early, probe: page.elements.probe.textContent, manual_hidden: hidden() };
  }
  {
    const page = load(entryPage, (url, options) => String(url).endsWith('/revoke-all')
      ? (runs.revoke_body = JSON.parse(options.body), answer(200, { revoked_pairings: 3, sessions_revoked: true }))
      : answer(200, preview));
    const e = page.elements;
    e['admin-key'].value = 'k'.repeat(40);
    await e.inspect.listeners.click();
    await e.revoke.listeners.click();
    runs.revoke = { status: e.status.textContent, error: e.status.className === 'error' };
  }
  process.stdout.write(JSON.stringify(runs));
})().catch(error => { console.error(error); process.exit(1); });
`

type loopbackRun struct {
	Command               string `json:"command"`
	BeforePreviewDisabled bool   `json:"before_preview_disabled"`
	Offered               bool   `json:"offered"`
	Grant                 *struct {
		Confirm             bool   `json:"confirm"`
		CodeChallenge       string `json:"code_challenge"`
		CodeChallengeMethod string `json:"code_challenge_method"`
	} `json:"grant"`
	Status   string `json:"status"`
	Error    bool   `json:"error"`
	Probe    string `json:"probe"`
	KeyKept  bool   `json:"key_kept"`
	After    bool   `json:"after_disabled"`
	Callback *struct {
		State string `json:"state"`
		Code  string `json:"code"`
	} `json:"callback"`
	Calls []struct {
		URL    string `json:"url"`
		Method string `json:"method"`
	} `json:"calls"`
}

// The loopback login as the page runs it: the command carries a port and a
// state of this page's; the button is offered only when the CLI on that port
// answers with that state and a well-formed challenge, and after the
// environment is checked; the grant is asked for bound to the CLI's challenge
// and handed to the CLI with the state; every way the CLI or the server
// refuses reaches the operator as what to do.
func checkLoopback(t *testing.T, raw map[string]json.RawMessage) {
	t.Helper()
	run := func(name string) loopbackRun {
		t.Helper()
		var got loopbackRun
		if err := json.Unmarshal(raw[name], &got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return got
	}
	ok := run("loopback_ok")
	fields := strings.Fields(ok.Command)
	if len(fields) != 9 || strings.Join(fields[:3], " ") != "alarmd-cli auth listen" || fields[3] != "--url" ||
		fields[4] != "http://apps.example.test/alarmd/" || fields[5] != "--port" || fields[7] != "--state" || len(fields[8]) != 43 {
		t.Fatalf("the command: %q", ok.Command)
	}
	port, state := fields[6], fields[8]
	if !ok.BeforePreviewDisabled || !ok.Offered || ok.Grant == nil || !ok.Grant.Confirm ||
		ok.Grant.CodeChallenge != strings.Repeat("C", 43) || ok.Grant.CodeChallengeMethod != "S256" {
		t.Errorf("the grant is asked for bound to the CLI's challenge, after the check: %+v", ok)
	}
	if ok.Callback == nil || ok.Callback.State != state || ok.Callback.Code != "alarmd-login-v1.abc" {
		t.Errorf("the code reaches the CLI with the page's state: %+v", ok.Callback)
	}
	for _, call := range ok.Calls {
		if !strings.HasPrefix(call.URL, "http://127.0.0.1:"+port+"/") {
			t.Errorf("a loopback call to another port: %s", call.URL)
		}
	}
	if ok.Error || !strings.Contains(ok.Status, "ns/release") || ok.KeyKept || !ok.After || !strings.Contains(ok.Probe, "已登录") {
		t.Errorf("after the CLI logged in: %+v", ok)
	}
	for _, name := range []string{"loopback_wrong_state", "loopback_bad_challenge", "loopback_none"} {
		if got := run(name); got.Offered || got.Grant != nil || strings.Contains(got.Probe, "已就绪") {
			t.Errorf("%s: a CLI that did not answer with this page's state and a challenge is not offered: %+v", name, got)
		}
	}
	for name, says := range map[string]string{"loopback_refused": "重新执行上面的命令", "loopback_gone": "同一台机器",
		"loopback_grant_refused": "管理凭据不对"} {
		got := run(name)
		if !got.Error || !strings.Contains(got.Status, says) || strings.Contains(got.Status, "server sentence") {
			t.Errorf("%s says what to do: %+v", name, got)
		}
	}
	var blocked struct {
		Early struct {
			Probe        string `json:"probe"`
			ManualHidden bool   `json:"manual_hidden"`
		} `json:"early"`
		Probe        string `json:"probe"`
		ManualHidden bool   `json:"manual_hidden"`
	}
	_ = json.Unmarshal(raw["blocked"], &blocked)
	if strings.Contains(blocked.Early.Probe, "拦截") || !blocked.Early.ManualHidden ||
		!strings.Contains(blocked.Probe, "拦截") || !strings.Contains(blocked.Probe, "复制授权码") || blocked.ManualHidden {
		t.Errorf("five unanswered probes say the browser may block the local address and open the fallback: %+v", blocked)
	}
	var revoke struct {
		Status string `json:"status"`
		Error  bool   `json:"error"`
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	_ = json.Unmarshal(raw["revoke"], &revoke)
	_ = json.Unmarshal(raw["revoke_body"], &body)
	if revoke.Error || !strings.Contains(revoke.Status, "配对 3 个") || !body.Confirm {
		t.Errorf("revoking every pairing: %+v %+v", revoke, body)
	}
}
