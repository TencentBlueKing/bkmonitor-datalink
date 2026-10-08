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
	loops := runLoginPage(t)
	checkLoopback(t, loops)
	runs := map[string]cliPageRun{}
	for name, raw := range loops {
		var result cliPageRun
		// Runs that are not a page's state, such as the way back's address,
		// have no messages to count.
		if json.Unmarshal(raw, &result) == nil {
			runs[name] = result
		}
	}
	run := func(name string) cliPageRun {
		t.Helper()
		result, ok := runs[name]
		if !ok {
			t.Fatalf("no run %q", name)
		}
		return result
	}

	// Each message sits beside the control it is about, and a new one
	// replaces the last wherever that was: never two at once.
	for name, got := range runs {
		if got.Shown > 1 {
			t.Errorf("%s: %d messages on show at once", name, got.Shown)
		}
	}
	const entry = "http://apps.example.test/alarmd/"
	if got := run("same_origin"); got.IssueDisabled || got.Error || got.At != "inspect-status" {
		t.Errorf("opened from the configured entry, generation is offered: %+v", got)
	}
	// The browser's own origin form: a default port and an upper-case host in
	// the address bar are the same origin as the entry.
	if got := run("same_origin_other_spelling"); got.IssueDisabled || got.Error {
		t.Errorf("the same origin spelled with :80 and upper case is still the entry: %+v", got)
	}
	got := run("other_origin")
	if !got.Error || !strings.Contains(got.Status, "http://192.0.2.10:8080") ||
		!strings.Contains(got.Status, entry+"cli") || got.Requests != 1 || got.At != "inspect-status" {
		t.Errorf("opened from another origin, the page says where to open it: %+v", got)
	}
	if got := run("other_origin_pressed"); !got.Error || !strings.Contains(got.Status, entry+"cli") || got.Requests != 1 ||
		got.At != "inspect-status" {
		t.Errorf("opened from another origin, generating says where to open the page and sends nothing: %+v", got)
	}
	if got := run("refused_after_preview"); !strings.Contains(got.Status, entry+"cli") || !got.Error || got.At != "issue-status" {
		t.Errorf("a generation refused for its origin says the entry the preview named: %+v", got)
	}

	for _, code := range []string{"admin_unauthorized", "admin_not_configured", "auth_rate_limited",
		"auth_busy", "auth_store_unavailable", "not_found"} {
		got := run("code:" + code)
		if !got.Error || got.Status == "" || got.Status == "server sentence for "+code || got.At != "inspect-status" {
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
		!strings.Contains(notJSON.Status, "/api/cli/") || notJSON.At != "inspect-status" {
		t.Errorf("an answer that is not alarmd's names the route to check: %+v / %+v", notJSON, network)
	}
}

// Copying works on a page served over plain http, which has no clipboard:
// through the copy command, and when the browser refuses that too, by
// selecting the text so one key press copies it. The fallback buttons are
// pressable before the environment is checked: pressed, they send the
// operator to the check and bring them back to the button once it passed.
// The way back leads to the first observability page.
func TestTheAuthorizationPageCopiesAndLeadsToTheCheck(t *testing.T) {
	raw := runLoginPage(t)
	type copyRun struct {
		Status   string `json:"status"`
		Error    bool   `json:"error"`
		At       string `json:"at"`
		Copied   string `json:"copied"`
		Command  string `json:"command"`
		Selected string `json:"selected"`
		Focused  string `json:"focused"`
	}
	read := func(name string, into any) {
		t.Helper()
		if err := json.Unmarshal(raw[name], into); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, name := range []string{"copy_clipboard", "copy_command"} {
		var got copyRun
		read(name, &got)
		if got.Error || got.Copied == "" || got.Copied != got.Command || !strings.Contains(got.Status, "已复制") || got.At != "command-status" {
			t.Errorf("%s: the command is copied: %+v", name, got)
		}
	}
	var refused copyRun
	read("copy_refused", &refused)
	if refused.Copied != "" || refused.Selected != "listen-command" || !strings.Contains(refused.Status, "已替你选中") || refused.Error {
		t.Errorf("neither copy allowed, the command is selected for a key press: %+v", refused)
	}
	var code copyRun
	read("copy_code_refused", &code)
	if code.Selected != "code" || code.Focused != "code" || !strings.Contains(code.Status, "已替你选中") || code.At != "issue-status" {
		t.Errorf("neither copy allowed, the code is selected for a key press: %+v", code)
	}
	for _, action := range []string{"issue", "revoke"} {
		var got struct {
			Enabled bool `json:"enabled"`
			Sent    struct {
				Status  string `json:"status"`
				At      string `json:"at"`
				Focused string `json:"focused"`
				Steps   string `json:"steps"`
				Posts   int    `json:"posts"`
			} `json:"sent"`
			Typed struct {
				Focused string `json:"focused"`
			} `json:"typed"`
			Back struct {
				Status  string `json:"status"`
				At      string `json:"at"`
				Focused string `json:"focused"`
				Posts   int    `json:"posts"`
			} `json:"back"`
			Posts int `json:"posts"`
		}
		read("gate_"+action, &got)
		if !got.Enabled || got.Sent.Posts != 0 || got.Sent.Focused != "admin-key" || got.Sent.At != "inspect-status" ||
			!strings.Contains(got.Sent.Status, "核对环境") || !strings.HasPrefix(got.Sent.Steps, "later current") {
			t.Errorf("%s pressed before the check leads to the check and sends nothing: %+v", action, got)
		}
		if got.Typed.Focused != "inspect" {
			t.Errorf("%s pressed with the key typed leads to the check button: %+v", action, got.Typed)
		}
		if got.Back.Focused != action || got.Back.At != action+"-status" || !strings.Contains(got.Back.Status, "环境已核对") ||
			got.Back.Posts != 0 || got.Posts != 1 {
			t.Errorf("%s: once checked, back to the button, and pressed again it acts: %+v", action, got)
		}
	}
	var back string
	read("back", &back)
	if back != "http://apps.example.test/alarmd/" {
		t.Errorf("the way back leads to %q, not the first observability page", back)
	}
}

// runLoginPage runs the authorization page's script against a stubbed
// document and fetch, and returns every run's result by name.
func runLoginPage(t *testing.T) map[string]json.RawMessage {
	t.Helper()
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
	var runs map[string]json.RawMessage
	if err := json.Unmarshal(output, &runs); err != nil {
		t.Fatalf("harness output: %v\n%s", err, output)
	}
	return runs
}

type cliPageRun struct {
	Status        string `json:"status"`
	Error         bool   `json:"error"`
	At            string `json:"at"`
	Shown         int    `json:"shown"`
	IssueDisabled bool   `json:"issue_disabled"`
	KeyKept       bool   `json:"key_kept"`
	Requests      int    `json:"requests"`
}

const cliPageHarness = `
'use strict';
const fs = require('fs');
const html = fs.readFileSync(process.argv[2], 'utf8');
const script = html.slice(html.indexOf('<script>') + 8, html.lastIndexOf('</script>'));

// env.clipboard false is a page over plain http, with no clipboard; then
// env.copyCommand says whether the browser lets the copy command copy.
function load(pageURL, respond, loop, env = { clipboard: true }) {
  const elements = {};
  let focused = '', selected = '', copied = null;
  // The page's own hidden state: only these elements start hidden.
  const startsHidden = ['check-cli', 'grant'];
  const element = id => elements[id] || (elements[id] = {
    id, textContent: '', value: '', disabled: false, hidden: startsHidden.includes(id), className: '', href: '', listeners: {},
    addEventListener(type, listener) { this.listeners[type] = listener; }, focus() { focused = id; },
    scrollIntoView() {},
    // Only the text fields select themselves; the command blocks are selected
    // through a range, as a <pre> is.
    select: ['code', 'admin-key'].includes(id) ? () => { selected = id; } : undefined,
  });
  let requests = 0;
  const calls = [];
  const ticks = [];
  let scratch;
  const documentListeners = {};
  const context = {
    document: {
      getElementById: element,
      addEventListener(type, listener) { documentListeners[type] = listener; },
      createElement: () => (scratch = { value: '', style: {}, setAttribute() {}, select() {}, remove() {} }),
      body: { appendChild() {} },
      execCommand: () => { if (!env.copyCommand) return false; copied = scratch.value; return true; },
      createRange: () => ({ selectNodeContents(node) { this.node = node; } }),
    },
    getSelection: () => ({ removeAllRanges() {}, addRange(range) { selected = range.node.id; },
      containsNode(node) { return node.id === selected; } }),
    location: new URL(pageURL),
    addEventListener() {},
    navigator: env.clipboard ? { clipboard: { writeText: async text => { copied = text; } } } : {},
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
  return { elements, requests: () => requests, calls, tick: async () => { for (const fn of ticks) await fn(); },
    focused: () => focused, selected: () => selected, copied: () => copied,
    // What an operator does by hand: select an element's text, and copy.
    selectByHand: id => { selected = id; }, copyByHand: () => documentListeners.copy() };
}

const answer = (status, body) => ({ ok: status < 400, status, json: async () => body });
// The message on show and where: every control has its own line, and at
// most one of them says anything at a time.
const NOTES = ['command-status', 'admin-key-status', 'inspect-status', 'authorize-status', 'issue-status', 'revoke-status'];
function shown(e) {
  const on = NOTES.filter(id => e[id] && e[id].textContent);
  const at = on[0] || '';
  return { status: at ? e[at].textContent : '', error: !!at && e[at].className === 'error', at, shown: on.length };
}
const stepsOf = e => ['step-command', 'step-inspect', 'step-authorize'].map(id => (e[id] && e[id].className) || '').join(' ');
const preview = { environment_id: 'ns/release', environment_name: 'ns/release',
  public_base_url: 'http://apps.example.test/alarmd/' };
const entryPage = 'http://apps.example.test/alarmd/cli';

async function inspect(pageURL, respond, then) {
  const page = load(pageURL, respond);
  const e = page.elements;
  e['admin-key'].value = 'k'.repeat(40);
  await e.inspect.listeners.click();
  if (then) await then(e);
  return { ...shown(e), issue_disabled: e.issue.disabled, key_kept: e['admin-key'].value !== '', requests: page.requests() };
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
    const stepsBefore = stepsOf(e);
    e['admin-key'].value = 'k'.repeat(40);
    await e.inspect.listeners.click();
    await settle();
    const stepsChecked = stepsOf(e), waitsFor = e['authorize-hint'].textContent;
    const offered = !e.authorize.disabled;
    if (offered) { await e.authorize.listeners.click(); await settle(); }
    return { command, before_preview_disabled: beforePreview, offered, grant: seen || null, ...shown(e),
      probe: e.probe.textContent, key_kept: e['admin-key'].value !== '',
      after_disabled: e.authorize.disabled, calls: page.calls.filter(c => c.url.startsWith('http://127.0.0.1:')),
      steps_before: stepsBefore, steps_checked: stepsChecked, waits_for: waitsFor, steps_after: stepsOf(e) };
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
    // Nothing answers on the loopback address. The probes in the background
    // only ever say waiting, however many; once the command is copied a
    // check button is offered, and asked, it says the browser may be
    // blocking the local address and opens the copy fallback.
    const page = load(entryPage, () => answer(200, preview), null);
    const e = page.elements;
    await settle();
    // An element the script never touched keeps the page's own state: closed.
    const hidden = () => !(e.manual && e.manual.open === true);
    for (let i = 0; i < 9; i++) await page.tick();
    const waiting = { probe: e.probe.textContent, error: e.probe.className === 'error', manual_hidden: hidden(), check_hidden: e['check-cli'].hidden };
    await e['copy-command'].listeners.click();
    const offered = { check_hidden: e['check-cli'].hidden };
    await e['check-cli'].listeners.click();
    runs.blocked = { waiting, offered, probe: e.probe.textContent, error: e.probe.className === 'error', manual_hidden: hidden() };
  }
  {
    // A command typed by hand is never copied: the check is offered after
    // fifteen probes unanswered, and nothing is said to be wrong.
    const page = load(entryPage, () => answer(200, preview), null);
    const e = page.elements;
    await settle();
    for (let i = 0; i < 13; i++) await page.tick();
    const at14 = e['check-cli'].hidden;
    await page.tick();
    runs.typed_by_hand = { hidden_at_14: at14, hidden_at_15: e['check-cli'].hidden, probe: e.probe.textContent,
      error: e.probe.className === 'error', manual_open: !!(e.manual && e.manual.open) };
  }
  {
    // A command copied by hand offers the check too; a CLI that answers
    // takes the check away.
    let up = false;
    const page = load(entryPage, () => answer(200, preview),
      async url => { if (!up) throw new TypeError('Failed to fetch'); return answer(200, { state: url.searchParams.get('state'), code_challenge: challenge }); });
    const e = page.elements;
    await settle();
    page.selectByHand('preview');
    page.copyByHand();
    const otherText = !e['check-cli'].hidden;
    page.selectByHand('listen-command');
    page.copyByHand();
    const offered = !otherText && !e['check-cli'].hidden;
    up = true;
    await e['check-cli'].listeners.click();
    runs.check_ready = { offered, check_hidden: e['check-cli'].hidden, probe: e.probe.textContent, error: e.probe.className === 'error' };
  }
  // Copying: the clipboard on a secure page; the copy command on a page over
  // plain http; neither allowed, the text selected for a key press.
  for (const [name, env] of Object.entries({ copy_clipboard: { clipboard: true }, copy_command: { clipboard: false, copyCommand: true },
    copy_refused: { clipboard: false, copyCommand: false } })) {
    const page = load(entryPage, () => answer(200, preview), null, env);
    const e = page.elements;
    await settle();
    await e['copy-command'].listeners.click();
    runs[name] = { ...shown(e), copied: page.copied(), command: e['listen-command'].textContent, selected: page.selected() };
  }
  {
    const page = load(entryPage, (url, options) => options.method === 'POST' ? grantOK() : answer(200, preview), null,
      { clipboard: false, copyCommand: false });
    const e = page.elements;
    e['admin-key'].value = 'k'.repeat(40);
    await e.inspect.listeners.click();
    await e.issue.listeners.click();
    await e.copy.listeners.click();
    runs.copy_code_refused = { ...shown(e), selected: page.selected(), focused: page.focused() };
  }
  // The fallback buttons are pressable before the check: pressed, they send
  // the operator to the check and, once checked, back to the button.
  for (const action of ['issue', 'revoke']) {
    const posts = [];
    const page = load(entryPage, (url, options) => {
      if (options.method === 'POST') { posts.push(String(url)); return String(url).endsWith('/revoke-all')
        ? answer(200, { revoked_pairings: 3, sessions_revoked: true }) : grantOK(); }
      return answer(200, preview);
    }, null);
    const e = page.elements;
    await settle();
    const enabled = !e[action].disabled;
    await e[action].listeners.click();
    const sent = { ...shown(e), focused: page.focused(), steps: stepsOf(e), posts: posts.length };
    e['admin-key'].value = 'k'.repeat(40);
    await e[action].listeners.click();
    const typed = { focused: page.focused() };
    await e.inspect.listeners.click();
    const back = { ...shown(e), focused: page.focused(), posts: posts.length };
    await e[action].listeners.click();
    runs['gate_' + action] = { enabled, sent, typed, back, posts: posts.length };
  }
  {
    // Opened from another origin, a pressed fallback says where to open the
    // page and sends nothing.
    const page = load('http://192.0.2.10:8080/alarmd/cli', () => answer(200, preview), null);
    const e = page.elements;
    e['admin-key'].value = 'k'.repeat(40);
    await e.inspect.listeners.click();
    await e.issue.listeners.click();
    runs.other_origin_pressed = { ...shown(e), requests: page.requests() };
  }
  runs.back = load(entryPage, () => answer(200, preview), null).elements.back.href;
  {
    const page = load(entryPage, (url, options) => String(url).endsWith('/revoke-all')
      ? (runs.revoke_body = JSON.parse(options.body), answer(200, { revoked_pairings: 3, sessions_revoked: true }))
      : answer(200, preview));
    const e = page.elements;
    e['admin-key'].value = 'k'.repeat(40);
    await e.inspect.listeners.click();
    await e.revoke.listeners.click();
    runs.revoke = shown(e);
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
	At       string `json:"at"`
	Shown    int    `json:"shown"`
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
	// The steps' classes, first to third, on load, after the check and at
	// the end; and what the authorize step said it waits for after the check.
	StepsBefore  string `json:"steps_before"`
	StepsChecked string `json:"steps_checked"`
	WaitsFor     string `json:"waits_for"`
	StepsAfter   string `json:"steps_after"`
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
	if ok.Error || !strings.Contains(ok.Status, "ns/release") || ok.KeyKept || !ok.After || !strings.Contains(ok.Probe, "已登录") ||
		ok.At != "authorize-status" {
		t.Errorf("after the CLI logged in: %+v", ok)
	}
	// One step is current at a time, the first one not done: the command
	// until the CLI answers, then the check, then the button.
	if ok.StepsBefore != "done current later" || ok.StepsChecked != "done done current" || ok.StepsAfter != "done done done" {
		t.Errorf("the current step follows the login: %q -> %q -> %q", ok.StepsBefore, ok.StepsChecked, ok.StepsAfter)
	}
	if none := run("loopback_none"); none.StepsChecked != "current done later" || !strings.Contains(none.WaitsFor, "第 1 步") {
		t.Errorf("checked but no CLI: the command is the step to do, and the button says it waits for it: %q %q",
			none.StepsChecked, none.WaitsFor)
	}
	for _, name := range []string{"loopback_wrong_state", "loopback_bad_challenge", "loopback_none"} {
		if got := run(name); got.Offered || got.Grant != nil || strings.Contains(got.Probe, "已就绪") {
			t.Errorf("%s: a CLI that did not answer with this page's state and a challenge is not offered: %+v", name, got)
		}
	}
	for name, says := range map[string]string{"loopback_refused": "重新执行上面的命令", "loopback_gone": "同一台机器",
		"loopback_grant_refused": "管理凭据不对"} {
		got := run(name)
		if !got.Error || !strings.Contains(got.Status, says) || strings.Contains(got.Status, "server sentence") ||
			got.At != "authorize-status" || got.Shown != 1 {
			t.Errorf("%s says what to do, beside the button: %+v", name, got)
		}
	}
	var blocked struct {
		Waiting struct {
			Probe        string `json:"probe"`
			Error        bool   `json:"error"`
			ManualHidden bool   `json:"manual_hidden"`
			CheckHidden  bool   `json:"check_hidden"`
		} `json:"waiting"`
		Offered struct {
			CheckHidden bool `json:"check_hidden"`
		} `json:"offered"`
		Probe        string `json:"probe"`
		Error        bool   `json:"error"`
		ManualHidden bool   `json:"manual_hidden"`
	}
	_ = json.Unmarshal(raw["blocked"], &blocked)
	// Ten probes unanswered in the background - a command slow to start - say
	// only that the page waits: no error, nothing opened, no check offered
	// before the command was copied.
	if w := blocked.Waiting; w.Error || !strings.Contains(w.Probe, "等待") || !w.ManualHidden || !w.CheckHidden || blocked.Offered.CheckHidden {
		t.Errorf("unanswered background probes stay quiet, and copying offers the check: %+v", blocked)
	}
	// Asked, the check probes once more and says what the silence may mean,
	// counting every probe in a row, and opens the fallback.
	if !blocked.Error || !strings.Contains(blocked.Probe, "已连续 11 次（约 22 秒）") || !strings.Contains(blocked.Probe, "拦截") ||
		!strings.Contains(blocked.Probe, "复制授权码") || blocked.ManualHidden {
		t.Errorf("the check says the browser may block the local address and opens the fallback: %+v", blocked)
	}
	var typed struct {
		HiddenAt14 bool   `json:"hidden_at_14"`
		HiddenAt15 bool   `json:"hidden_at_15"`
		Probe      string `json:"probe"`
		Error      bool   `json:"error"`
		ManualOpen bool   `json:"manual_open"`
	}
	_ = json.Unmarshal(raw["typed_by_hand"], &typed)
	if !typed.HiddenAt14 || typed.HiddenAt15 || typed.Error || typed.ManualOpen || !strings.Contains(typed.Probe, "等待") {
		t.Errorf("a command typed by hand is offered the check after fifteen probes, quietly: %+v", typed)
	}
	var ready struct {
		Offered     bool   `json:"offered"`
		CheckHidden bool   `json:"check_hidden"`
		Probe       string `json:"probe"`
		Error       bool   `json:"error"`
	}
	_ = json.Unmarshal(raw["check_ready"], &ready)
	if !ready.Offered || !ready.CheckHidden || ready.Error || !strings.Contains(ready.Probe, "已就绪") {
		t.Errorf("a command copied by hand offers the check, and a CLI that answers it takes it away: %+v", ready)
	}
	var revoke struct {
		Status string `json:"status"`
		Error  bool   `json:"error"`
		At     string `json:"at"`
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	_ = json.Unmarshal(raw["revoke"], &revoke)
	_ = json.Unmarshal(raw["revoke_body"], &body)
	if revoke.Error || !strings.Contains(revoke.Status, "配对 3 个") || !body.Confirm || revoke.At != "revoke-status" {
		t.Errorf("revoking every pairing: %+v %+v", revoke, body)
	}
}
