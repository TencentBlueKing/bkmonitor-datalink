// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The snapshot is every read this deployment answers about itself, now, in
// one file an agent reads later with the same operations (--snapshot). Its
// content is fixed and its redaction is fixed: a person only runs it.

// snapshotFormat is the manifest's version of the layout.
const snapshotFormat = "alarmd-snapshot/v1"

// The bounds of one run: bytes, detail strategies, replicas read one by
// one, log lines, receipts, and the margin past the planned calls' minutes
// -- one call's longest deadline (a k8s read's 30 s), since a call admitted
// in the last slot can still run that long.
const (
	snapshotMaxDetails  = 500
	snapshotMaxReplicas = 1024
	snapshotLogLines    = 10000
	snapshotReceipts    = 100
	// snapshotReceiptPages is the most receipt pages one run reads: every
	// receipt the deployment keeps (1024 changes and 4096 accesses), a page
	// of snapshotReceipts at a time.
	snapshotReceiptPages  = (1024 + 4096 + snapshotReceipts - 1) / snapshotReceipts
	snapshotCallMargin    = 30 * time.Second
	snapshotFallbackRate  = 600
	snapshotDetailCallsBy = 2
	// snapshotRateAttempts is how many times one read is asked while the
	// session's budget refuses it: the first, the one after the minute
	// turns, and one more for the two clocks' difference.
	snapshotRateAttempts = 3
)

// snapshotMaxBytes bounds the answers a snapshot keeps, and with them what
// its reader unpacks; a test lowers it.
var snapshotMaxBytes = 512 << 20

// snapshotFixed are the reads every snapshot takes once, in order; one the
// deployment's catalog does not offer is recorded as skipped.
var snapshotFixed = []snapshotRead{
	{operation: "overview.get"},
	{operation: "lifecycle.get"},
	{operation: "maintenance.get"},
	{operation: "absent.list"},
	{operation: "lookback.get"},
	{operation: "store.info"},
	{operation: "k8s.pods"},
	{operation: "k8s.workloads"},
	{operation: "k8s.events"},
	{operation: "sre.receipts", params: map[string]any{"limit": snapshotReceipts}},
}

type snapshotRead struct {
	operation string
	key       string
	params    map[string]any
}

// snapshotClock is when a snapshot starts, which names its file; a test
// fixes it.
var snapshotClock = time.Now

// snapshotPace is the wait between two calls at the session's rate; a test
// sets it to nothing.
var snapshotPace = func(rate int) time.Duration { return time.Minute / time.Duration(rate) }

// snapshotSleep waits for the session's budget and snapshotNow reads the
// clock the time cap is held to; a test moves one with the other.
var (
	snapshotSleep = time.Sleep
	snapshotNow   = time.Now
)

// SnapshotItem is one collected read as the manifest records it.
type SnapshotItem struct {
	Operation string         `json:"operation"`
	Params    map[string]any `json:"params,omitempty"`
	File      string         `json:"file,omitempty"`
	// Result is ok; partial, the deployment answered with a partial status,
	// which is the read collected with the answer's own evidence caveat;
	// refused, no answer the channel accepted or an answer with an error
	// status; timeout; or skipped_by_bound / not_in_catalog. Every result
	// but ok and partial is a read not collected, and makes the exit 3.
	Result string `json:"result"`
	Reason string `json:"reason,omitempty"`
}

// SnapshotManifest is the snapshot's own account, at a fixed path.
type SnapshotManifest struct {
	Format          string    `json:"format"`
	Notice          string    `json:"notice"`
	CLIVersion      string    `json:"cli_version"`
	EnvironmentID   string    `json:"environment_id"`
	CatalogRevision string    `json:"catalog_revision"`
	Builds          []string  `json:"builds"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	RatePerMinute   int       `json:"rate_per_minute"`
	RateSource      string    `json:"rate_source"`
	PlannedCalls    int       `json:"planned_calls"`
	TimeCapSeconds  int       `json:"time_cap_seconds"`
	// BudgetWaitSeconds is how long the run waited for the session's
	// budget; the time cap grew by as much, up to the cap again.
	BudgetWaitSeconds int               `json:"budget_wait_seconds"`
	MaxBytes          int               `json:"max_bytes"`
	Bytes             int               `json:"bytes"`
	Redactions        []SnapshotRedacts `json:"redactions"`
	Items             []SnapshotItem    `json:"items"`
}

// SnapshotRedacts is what the second redaction pass replaced in one file:
// the rule and how many times, never what.
type SnapshotRedacts struct {
	File  string `json:"file"`
	Rule  string `json:"rule"`
	Count int    `json:"count"`
}

// snapshotRules are the credential shapes the second pass looks for. The
// deployment already redacts what it answers; a hit here is a leak there,
// reported as a defect.
var snapshotRules = []struct {
	name    string
	pattern *regexp.Regexp
	keep    string
}{
	// In encoded JSON a key's newlines are \n: the key ends with its string.
	{"private_key", regexp.MustCompile(`-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----[^"]*`), "<redacted-private-key>"},
	{"bearer", regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "${1} <redacted>"},
	{"credential_field", regexp.MustCompile(`(?i)((?:password|passwd|secret|api[_-]?key|access[_-]?token)["']?\s*[:=]\s*["']?)[^"'\s,;}<]{4,}`), "${1}<redacted>"},
	{"url_userinfo", regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^\s/@"]+:[^\s/@"]*@`), "${1}<redacted>@"},
}

type snapshotRun struct {
	a        *App
	p        Profile
	revision string
	manifest SnapshotManifest
	files    map[string][]byte
	log      bytes.Buffer
	deadline time.Time
	// waitRoom is how much more the deadline may move for budget waits, and
	// waited how long the run has waited for the budget.
	waitRoom time.Duration
	waited   time.Duration
	pace     time.Duration
	last     time.Time
	calls    int
}

func (a *App) snapshot(p Profile, out string) int {
	run := &snapshotRun{a: a, p: p, files: map[string][]byte{}, manifest: SnapshotManifest{Format: snapshotFormat,
		Notice: "This file holds this deployment's own readings, with credentials redacted; host addresses, host names " +
			"and business IDs are kept because diagnosis needs them. Whoever receives it receives those.",
		CLIVersion: a.Version, EnvironmentID: p.EnvironmentID, StartedAt: snapshotClock().UTC(), MaxBytes: snapshotMaxBytes,
		Redactions: []SnapshotRedacts{}, Items: []SnapshotItem{}, Builds: []string{}}}
	fmt.Fprintln(a.Err, "Reading the operation catalog...")
	discovered, status, err := a.channelRequest("discover", "", nil, "", p)
	if err != nil || status < 200 || status >= 300 || stringField(discovered, "status") != "ok" {
		message := "the deployment did not answer discover"
		if err != nil {
			message = err.Error()
		}
		return a.fail("snapshot_failed", message+"; nothing was written", 1)
	}
	run.revision = stringField(objectField(discovered, "meta"), "catalog_revision")
	run.manifest.CatalogRevision = run.revision
	run.keep("discover", "", nil, discovered, "ok", "")
	offered := map[string]bool{}
	operations, _ := objectField(discovered, "result")["operations"].([]any)
	for _, raw := range operations {
		if row, ok := raw.(map[string]any); ok {
			offered[stringField(row, "operation")] = true
		}
	}
	run.manifest.RatePerMinute, run.manifest.RateSource = snapshotFallbackRate, "fallback: the deployment did not state its session rate"
	if rate := number(objectField(objectField(discovered, "result"), "budget")["invokes_per_session_per_minute"]); rate > 0 {
		run.manifest.RatePerMinute, run.manifest.RateSource = rate, "discover"
	}
	run.pace = snapshotPace(run.manifest.RatePerMinute)

	fleet := run.invoke(snapshotRead{operation: "fleet.get"}, offered)
	pages := 0
	firstPage := map[string]any{}
	if offered[diagnoseOperation] {
		m, ok := run.call(diagnoseOperation, map[string]any{})
		firstPage = objectField(m, "result")
		run.keepAnswer(diagnoseOperation, "page-0001", map[string]any{}, m, ok)
		pages = 1
	}
	troubled := snapshotTroubledReplicas(objectField(fleet, "result"))
	details := snapshotDetailStrategies(firstPage)
	// The plan is fixed before anything past the first reads: its calls and
	// the time they take at the session's rate, with one call's deadline as
	// the margin, are in the manifest whatever the run reaches.
	estimatedPages := snapshotEstimatedPages(firstPage)
	planned := run.calls + len(snapshotFixed) + snapshotReceiptPages - 1 + 4*len(troubled) + (estimatedPages - pages) + snapshotDetailCallsBy*len(details) + len(offered)
	run.manifest.PlannedCalls = planned
	minutes := (planned + run.manifest.RatePerMinute - 1) / run.manifest.RatePerMinute
	capacity := time.Duration(minutes)*time.Minute + snapshotCallMargin
	run.manifest.TimeCapSeconds = int(capacity / time.Second)
	run.deadline = run.manifest.StartedAt.Add(capacity)
	run.waitRoom = capacity
	for _, build := range snapshotBuilds(objectField(fleet, "result")) {
		run.manifest.Builds = append(run.manifest.Builds, build)
	}

	// The rest of the diagnosis, page by page, as diagnose reads it.
	cursor := stringField(firstPage, "next_cursor")
	for page := 2; cursor != "" && page <= diagnosisMaxPages; page++ {
		params := map[string]any{"cursor": cursor}
		m, ok := run.call(diagnoseOperation, params)
		run.keepAnswer(diagnoseOperation, fmt.Sprintf("page-%04d", page), params, m, ok)
		cursor = stringField(objectField(m, "result"), "next_cursor")
		if !ok {
			break
		}
	}
	for _, read := range snapshotFixed {
		got := run.invoke(read, offered)
		// Every receipt the deployment keeps, page by page.
		for page := 2; read.operation == "sre.receipts" && page <= snapshotReceiptPages; page++ {
			next, ok := objectField(got, "result")["next_offset"].(json.Number)
			offset, err := next.Int64()
			if !ok || err != nil || offset <= 0 {
				break
			}
			got = run.invoke(snapshotRead{operation: read.operation, key: fmt.Sprintf("page-%04d", page),
				params: map[string]any{"limit": snapshotReceipts, "offset": offset}}, offered)
		}
	}
	for _, replica := range troubled {
		run.invoke(snapshotRead{operation: "runtime.get", key: replica, params: map[string]any{"replica": replica}}, offered)
		listed := run.invoke(snapshotRead{operation: "metrics.list", key: replica, params: map[string]any{"replica": replica}}, offered)
		if names := snapshotMetricNames(objectField(listed, "result")); len(names) > 0 {
			run.invoke(snapshotRead{operation: "metrics.get", key: replica, params: map[string]any{"replica": replica, "names": names}}, offered)
		}
		run.invoke(snapshotRead{operation: "k8s.logs", key: replica, params: map[string]any{"pod": replica, "lines": snapshotLogLines}}, offered)
	}
	for _, id := range details {
		got := run.invoke(snapshotRead{operation: "strategy.get", key: id, params: map[string]any{"strategy_id": id}}, offered)
		if group := snapshotFirstQueryGroup(objectField(got, "result")); group != "" {
			run.invoke(snapshotRead{operation: "object.get", key: id, params: map[string]any{"query_group": group}}, offered)
		}
	}
	// What each read operation takes, as the catalog answered it: an agent
	// reading the snapshot describes from it as it would live.
	described := map[string]bool{}
	for _, item := range run.manifest.Items {
		if item.Operation == "discover" || described[item.Operation] || !offered[item.Operation] {
			continue
		}
		described[item.Operation] = true
		if !run.allowed(item.Operation, nil) {
			continue
		}
		run.wait()
		m, status, err := a.channelRequest("describe", item.Operation, nil, "", p)
		run.calls++
		run.keepAnswer("describe/"+item.Operation, "", nil, m, err == nil && status >= 200 && status < 300)
	}
	return run.finish(out)
}

// invoke reads one operation the catalog offers and keeps its answer.
func (run *snapshotRun) invoke(read snapshotRead, offered map[string]bool) map[string]any {
	params := read.params
	if params == nil {
		params = map[string]any{}
	}
	if !offered[read.operation] {
		run.record(SnapshotItem{Operation: read.operation, Params: params, Result: "not_in_catalog"})
		return nil
	}
	if !run.allowed(read.operation, params) {
		return nil
	}
	m, ok := run.call(read.operation, params)
	run.keepAnswer(read.operation, read.key, params, m, ok)
	return m
}

// allowed says whether one more call fits the run's time and bytes; one
// that does not is recorded as skipped.
func (run *snapshotRun) allowed(operation string, params map[string]any) bool {
	reason := ""
	switch {
	case !run.deadline.IsZero() && snapshotNow().After(run.deadline):
		reason = "time cap reached"
	case run.manifest.Bytes >= snapshotMaxBytes:
		reason = "byte cap reached"
	}
	if reason == "" {
		return true
	}
	run.record(SnapshotItem{Operation: operation, Params: params, Result: "skipped_by_bound", Reason: reason})
	return false
}

func (run *snapshotRun) wait() {
	if !run.last.IsZero() {
		if rest := run.pace - time.Since(run.last); rest > 0 {
			time.Sleep(rest)
		}
	}
	run.last = time.Now()
}

// call asks one read. The session's budget is shared with every call made
// under the same login, so the snapshot's own pace does not keep it from
// being spent: a read refused for it executed nothing, and is asked again
// when the refusal says to. The cap was planned from the calls at the full
// rate, and a wait is time no call could use, so the cap grows by each wait
// taken, by no more than the cap again in all; a wait past that is not
// taken.
func (run *snapshotRun) call(operation string, params map[string]any) (map[string]any, bool) {
	var m map[string]any
	var status int
	var err error
	for attempt := 1; ; attempt++ {
		run.wait()
		started := time.Now()
		m, status, err = run.a.channelRequest("invoke", operation, params, run.revision, run.p)
		run.calls++
		entry := map[string]any{"at": started.UTC().Format(time.RFC3339Nano), "operation": operation, "elapsed_ms": time.Since(started).Milliseconds(), "http_status": status}
		if err != nil {
			entry["error"] = err.Error()
		}
		retry := time.Duration(0)
		if err == nil && stringField(objectField(m, "error"), "code") == "rate_limited" && attempt < snapshotRateAttempts {
			retry = snapshotRetryAfter(m)
			if !run.deadline.IsZero() {
				if retry > run.waitRoom {
					retry = 0
				} else {
					run.waitRoom -= retry
					run.deadline = run.deadline.Add(retry)
				}
			}
		}
		if retry > 0 {
			entry["retry_after_ms"] = retry.Milliseconds()
		}
		line, _ := json.Marshal(entry)
		run.log.Write(append(line, '\n'))
		if retry <= 0 {
			break
		}
		snapshotSleep(retry)
		run.waited += retry
	}
	if err != nil || m == nil {
		return map[string]any{"status": "error", "error": map[string]any{"code": "request_failed", "message": fmt.Sprint(err)}, "meta": map[string]any{"http_status": status}}, false
	}
	return m, status >= 200 && status < 300
}

// snapshotRetryAfter is how long a refusal for the session's budget says to
// wait: its Retry-After, else until a second past the next minute by this
// clock, the budget's window being the clock minute.
func snapshotRetryAfter(m map[string]any) time.Duration {
	if seconds, ok := objectField(m, "meta")["client_retry_after_seconds"].(int); ok {
		return time.Duration(max(seconds, 1)) * time.Second
	}
	now := snapshotNow()
	return now.Truncate(time.Minute).Add(time.Minute + time.Second).Sub(now)
}

// keepAnswer files one answer with the request it answered.
func (run *snapshotRun) keepAnswer(operation, key string, params map[string]any, m map[string]any, ok bool) {
	result := "ok"
	reason := ""
	switch {
	case !ok:
		result, reason = "refused", stringField(objectField(m, "error"), "code")
		if strings.Contains(reason, "timeout") {
			result = "timeout"
		}
	case stringField(m, "status") == "partial":
		// Collected: the answer names its own limitations, and several
		// operations' standing ones mark every answer partial.
		result, reason = "partial", firstLimitation(m)
	case stringField(m, "status") != "ok":
		result, reason = "refused", stringField(objectField(m, "error"), "code")
		if reason == "" {
			reason = stringField(m, "status")
		}
	}
	run.keep(operation, key, params, m, result, reason)
}

// firstLimitation is the first limitation an answer names of its evidence,
// or its status when it names none.
func firstLimitation(m map[string]any) string {
	if limitations, ok := objectField(m, "evidence")["limitations"].([]any); ok && len(limitations) > 0 {
		if text, ok := limitations[0].(string); ok && text != "" {
			return text
		}
	}
	return stringField(m, "status")
}

func (run *snapshotRun) keep(operation, key string, params map[string]any, m map[string]any, result, reason string) {
	name := "ops/" + operation + ".json"
	if key != "" {
		name = "ops/" + operation + "/" + snapshotFileKey(key) + ".json"
	}
	record := redact(m, []string{run.p.AccessToken, run.p.RefreshToken}).(map[string]any)
	record["request"] = map[string]any{"operation": strings.TrimPrefix(operation, "describe/"), "params": params, "mode": snapshotMode(operation)}
	data, _ := json.MarshalIndent(record, "", " ")
	data = run.scan(name, data)
	// The scan replaces inside JSON strings; an answer it left unreadable is
	// kept as a placeholder and is not counted as whole.
	if !json.Valid(data) {
		data, _ = json.Marshal(map[string]any{"status": "error", "request": record["request"],
			"error": map[string]any{"code": "redaction_broke_json", "message": "the second redaction pass left this answer unreadable; it is not kept"}})
		result, reason = "redaction_broke_json", "the second redaction pass left the answer unreadable"
	}
	// The bound counts the answer about to be kept, so a snapshot never holds
	// more than its reader unpacks.
	if run.manifest.Bytes+len(data) > snapshotMaxBytes {
		run.record(SnapshotItem{Operation: operation, Params: params, Result: "skipped_by_bound", Reason: "byte cap reached"})
		return
	}
	run.files[name] = data
	run.manifest.Bytes += len(data)
	run.record(SnapshotItem{Operation: operation, Params: params, File: name, Result: result, Reason: reason})
}

func (run *snapshotRun) record(item SnapshotItem) {
	run.manifest.Items = append(run.manifest.Items, item)
}

// scan replaces what looks like a credential and counts it by rule.
func (run *snapshotRun) scan(name string, data []byte) []byte {
	for _, rule := range snapshotRules {
		count := len(rule.pattern.FindAll(data, -1))
		if count == 0 {
			continue
		}
		data = rule.pattern.ReplaceAll(data, []byte(rule.keep))
		run.manifest.Redactions = append(run.manifest.Redactions, SnapshotRedacts{File: name, Rule: rule.name, Count: count})
	}
	return data
}

// finish writes the archive and its checksum, and answers 0 when every
// read was collected, 3 when some were not: a partial answer was collected.
func (run *snapshotRun) finish(out string) int {
	run.manifest.FinishedAt = time.Now().UTC()
	run.manifest.BudgetWaitSeconds = int(run.waited.Round(time.Second) / time.Second)
	whole := true
	missing := []string{}
	for _, item := range run.manifest.Items {
		if item.Result != "ok" && item.Result != "partial" {
			whole = false
			missing = append(missing, item.Operation)
		}
	}
	slug := strings.NewReplacer("/", "-", " ", "-").Replace(run.p.EnvironmentID)
	stem := "alarmd-snapshot-" + slug + "-" + run.manifest.StartedAt.Format("20060102T150405Z")
	manifest, _ := json.MarshalIndent(run.manifest, "", " ")
	run.files["manifest.json"] = manifest
	run.files["collector.log.jsonl"] = run.log.Bytes()
	names := make([]string, 0, len(run.files))
	for name := range run.files {
		names = append(names, name)
	}
	sort.Strings(names)
	var sums bytes.Buffer
	for _, name := range names {
		sum := sha256.Sum256(run.files[name])
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	run.files["SHA256SUMS"] = sums.Bytes()
	names = append(names, "SHA256SUMS")
	if out == "" {
		out = "."
	}
	archive := filepath.Join(out, stem+".tar.gz")
	if _, err := os.Stat(archive); err == nil {
		return run.a.fail("snapshot_failed", "the snapshot file already exists: "+archive, 1)
	}
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	tarred := tar.NewWriter(zipped)
	for _, name := range names {
		data := run.files[name]
		header := &tar.Header{Name: path.Join(stem, name), Mode: 0o600, Size: int64(len(data)), ModTime: run.manifest.FinishedAt}
		if tarred.WriteHeader(header) != nil {
			return run.a.fail("snapshot_failed", "cannot write the archive", 1)
		}
		if _, err := tarred.Write(data); err != nil {
			return run.a.fail("snapshot_failed", "cannot write the archive", 1)
		}
	}
	if tarred.Close() != nil || zipped.Close() != nil {
		return run.a.fail("snapshot_failed", "cannot write the archive", 1)
	}
	if err := os.WriteFile(archive, buffer.Bytes(), 0o600); err != nil {
		return run.a.fail("snapshot_failed", "cannot write the snapshot file", 1)
	}
	sum := sha256.Sum256(buffer.Bytes())
	digest := hex.EncodeToString(sum[:])
	if err := os.WriteFile(archive+".sha256", []byte(digest+"  "+filepath.Base(archive)+"\n"), 0o600); err != nil {
		return run.a.fail("snapshot_failed", "cannot write the checksum file", 1)
	}
	status, code, summary := "ok", 0, "Snapshot written with every read collected."
	if !whole {
		status, code, summary = "partial", 3, fmt.Sprintf("Snapshot written; %d reads were not collected, each named in manifest.json.", len(missing))
	}
	if len(run.manifest.Redactions) > 0 {
		summary += " The second redaction pass replaced credential-like text; manifest.json names the files and rules. Report it: the deployment should have redacted it."
	}
	run.a.print(map[string]any{"status": status, "summary": summary, "result": map[string]any{"file": archive, "sha256": digest,
		"planned_calls": run.manifest.PlannedCalls, "calls": run.calls, "time_cap_seconds": run.manifest.TimeCapSeconds,
		"bytes": run.manifest.Bytes, "not_whole": missing, "redactions": len(run.manifest.Redactions)}})
	return code
}

func snapshotMode(operation string) string {
	switch {
	case operation == "discover":
		return "discover"
	case strings.HasPrefix(operation, "describe/"):
		return "describe"
	}
	return "invoke"
}

// snapshotFileKey keeps a file name to the characters a path holds safely.
func snapshotFileKey(key string) string { return snapshotUnsafeKey.ReplaceAllString(key, "_") }

var snapshotUnsafeKey = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// snapshotTroubledReplicas are the replicas fleet.get does not show well:
// not ready, or named by a degradation. Healthy ones are in fleet.get's own
// summary and are not read one by one.
func snapshotTroubledReplicas(health map[string]any) []string {
	degraded := map[string]bool{}
	list, _ := health["degradations"].([]any)
	for _, raw := range list {
		if row, ok := raw.(map[string]any); ok {
			degraded[stringField(row, "replica")] = true
		}
	}
	var troubled []string
	replicas, _ := health["per_replica"].([]any)
	for _, raw := range replicas {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := stringField(row, "replica")
		ready, _ := objectField(row, "readiness")["ready"].(bool)
		if name != "" && (!ready || degraded[name]) && len(troubled) < snapshotMaxReplicas {
			troubled = append(troubled, name)
		}
	}
	return troubled
}

func snapshotBuilds(health map[string]any) []string {
	var builds []string
	groups, _ := health["builds"].([]any)
	for _, raw := range groups {
		if group, ok := raw.(map[string]any); ok {
			build := objectField(group, "build")
			builds = append(builds, stringField(build, "version")+"/"+stringField(build, "commit"))
		}
	}
	return builds
}

// snapshotDetailStrategies are the strategies on the first page not
// detecting, up to the detail bound.
func snapshotDetailStrategies(page map[string]any) []string {
	var ids []string
	rows, _ := page["strategies"].([]any)
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok || stringField(row, "strategy_id") == "" {
			continue
		}
		if stringField(row, "verdict") != "DETECTING" && len(ids) < snapshotMaxDetails {
			ids = append(ids, stringField(row, "strategy_id"))
		}
	}
	return ids
}

func snapshotEstimatedPages(page map[string]any) int {
	count := number(objectField(page, "universe")["count"])
	rows := number(objectField(page, "page")["rows"])
	if count <= 0 || rows <= 0 {
		return 1
	}
	return (count + rows - 1) / rows
}

// snapshotKeyMetrics are the families a troubled replica's metrics.get
// reads, one call's worth (the channel takes twenty names): its flow, its
// resources and its pool. metrics.list keeps the name of every family it
// has, for a reader that needs one of the others live.
var snapshotKeyMetrics = []string{
	"alarmd_worker_work_total", "alarmd_access_query_failure_total", "alarmd_access_query_duration_seconds",
	"alarmd_progress_completed_total", "alarmd_slot_decision_lag_seconds", "alarmd_executions_past_deadline",
	"alarmd_state_admission_total", "alarmd_state_version_conflict_total", "alarmd_redis_command_failure_total",
	"alarmd_output_events_by_kind_total", "alarmd_output_events_rejected_all_total",
	"alarmd_container_memory_used_bytes", "alarmd_container_memory_limit_bytes", "alarmd_container_cpu_throttled_seconds_total",
	"alarmd_heap_inuse_live_bytes", "alarmd_worker_query_permits_held", "alarmd_worker_query_permit_budget",
	"alarmd_fleet_query_cooldown_objects", "alarmd_query_downstream_distress_total", "alarmd_capacity_budget",
}

// snapshotMetricNames are the key families the replica has.
func snapshotMetricNames(listed map[string]any) []string {
	has := map[string]bool{}
	families, _ := listed["families"].([]any)
	if families == nil {
		families, _ = listed["metrics"].([]any)
	}
	for _, raw := range families {
		switch family := raw.(type) {
		case map[string]any:
			has[stringField(family, "name")] = true
		case string:
			has[family] = true
		}
	}
	var names []string
	for _, name := range snapshotKeyMetrics {
		if has[name] {
			names = append(names, name)
		}
	}
	return names
}

func snapshotFirstQueryGroup(strategy map[string]any) string {
	plans, _ := strategy["plans"].([]any)
	for _, raw := range plans {
		if plan, ok := raw.(map[string]any); ok {
			if group := stringField(plan, "query_group"); group != "" {
				return group
			}
		}
	}
	return ""
}
