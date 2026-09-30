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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// diagnose runs diagnose.environment to its last page and proves across the
// pages what no single page can: every id of the universe written exactly
// once, the rows summing to the universe, and the verdicts summing to the
// rows. The server proves each page; the sum is this side's, because the
// server keeps nothing between pages. A universe that moved under the pages
// is read again from the first page once; if it moves again the run is not
// a diagnosis and says so.
func (a *App) diagnose(p Profile) int {
	fmt.Fprintln(a.Err, "Reading the current operation contract...")
	described, status, err := a.channelRequest("describe", diagnoseOperation, nil, "", p)
	if err != nil {
		return a.fail("protocol_error", err.Error(), 1)
	}
	if status < 200 || status >= 300 || stringField(described, "status") != "ok" {
		return a.emitOrLapsed(p, described, []string{p.AccessToken, p.RefreshToken}, status)
	}
	revision := stringField(objectField(described, "meta"), "catalog_revision")
	var run diagnosisRun
	for attempt := 0; attempt < 2; attempt++ {
		run, p, err = a.diagnosisPages(p, revision)
		if err != nil {
			return a.fail("request_failed", err.Error(), 1)
		}
		if run.failed != nil || !run.changed {
			break
		}
		fmt.Fprintln(a.Err, "The strategy set changed between pages; reading it again from the first page...")
	}
	if run.failed != nil {
		return a.emitOrLapsed(p, run.failed, []string{p.AccessToken, p.RefreshToken}, run.failedStatus)
	}
	return a.emitResponse(run.result(), []string{p.AccessToken, p.RefreshToken}, http.StatusOK)
}

const diagnoseOperation = "diagnose.environment"

// A page the replica could not forward to the Leader is asked for again, a
// bounded number of times: the forward is a single hop with its own short
// deadline, and on a live deployment it fails now and then on one request
// and answers the next, so one refusal ending the whole run would make the
// run fail for a reason the next second does not have. Anything else that
// refuses a page ends the run as before.
const diagnosisLeaderRetries = 2

var diagnosisRetryWait = time.Second

// leaderDidNotAnswer says the channel refused the page because the replica's
// forward to the Leader failed.
func leaderDidNotAnswer(m map[string]any) bool {
	failure := objectField(m, "error")
	return stringField(m, "status") == "error" && stringField(failure, "code") == "diagnosis_refused" &&
		stringField(failure, "message") == "LEADER_UNAVAILABLE"
}

// diagnosisMaxPages bounds one run: at the server's smallest page a set far
// beyond any deployment's still fits.
const diagnosisMaxPages = 1000

type diagnosisRun struct {
	first        map[string]any
	pages        int
	rows         []any
	byVerdict    map[string]int
	expected     int
	written      int
	holds        bool
	changed      bool
	problems     []string
	failed       map[string]any
	failedStatus int
}

func (a *App) diagnosisPages(p Profile, revision string) (diagnosisRun, Profile, error) {
	run := diagnosisRun{byVerdict: map[string]int{}, holds: true}
	cursor := ""
	for run.pages < diagnosisMaxPages {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		fmt.Fprintf(a.Err, "Reading page %d...\n", run.pages+1)
		m, status, err := a.channelRequest("invoke", diagnoseOperation, params, revision, p)
		if err != nil {
			return run, p, err
		}
		if status == http.StatusUnauthorized && p.RefreshToken != "" {
			if renewed, renewErr := a.ensureSession(p.EnvironmentID, true); renewErr == nil {
				p = renewed
				m, status, err = a.channelRequest("invoke", diagnoseOperation, params, revision, p)
				if err != nil {
					return run, p, err
				}
			}
		}
		for retry := 1; retry <= diagnosisLeaderRetries && leaderDidNotAnswer(m); retry++ {
			fmt.Fprintf(a.Err, "The Leader did not answer the forwarded page; asking again (%d/%d)...\n", retry, diagnosisLeaderRetries)
			time.Sleep(diagnosisRetryWait)
			if m, status, err = a.channelRequest("invoke", diagnoseOperation, params, revision, p); err != nil {
				return run, p, err
			}
		}
		if status < 200 || status >= 300 || stringField(m, "status") == "error" {
			run.failed, run.failedStatus = m, status
			return run, p, nil
		}
		result := objectField(m, "result")
		run.pages++
		if run.first == nil {
			run.first = result
			universe := objectField(result, "universe")
			if stringField(universe, "status") != "ok" {
				run.holds = false
				run.problems = append(run.problems, "universe unreadable: "+stringField(universe, "reason"))
				return run, p, nil
			}
		}
		if result["universe_changed"] != nil {
			run.changed = true
			return run, p, nil
		}
		page := objectField(result, "page")
		expected, written := number(page["ids_expected"]), number(page["rows"])
		run.expected += expected
		run.written += written
		if holds, _ := page["holds"].(bool); !holds {
			run.holds = false
			run.problems = append(run.problems, fmt.Sprintf("page %d does not hold: %d expected, %d written", run.pages, expected, written))
		}
		for word, n := range objectField(page, "by_verdict") {
			run.byVerdict[word] += number(n)
		}
		rows, _ := result["strategies"].([]any)
		run.rows = append(run.rows, rows...)
		cursor = stringField(result, "next_cursor")
		if cursor == "" {
			return run, p, nil
		}
	}
	run.holds = false
	run.problems = append(run.problems, "stopped after the page bound")
	return run, p, nil
}

// result is the run's one record: the first page's universe and deployment,
// every row, and the coverage this side proved.
func (run diagnosisRun) result() map[string]any {
	universe := objectField(run.first, "universe")
	count := number(universe["count"])
	seen := map[string]int{}
	var ids []string
	last := ""
	ordered := true
	for _, raw := range run.rows {
		row, _ := raw.(map[string]any)
		id := stringField(row, "strategy_id")
		seen[id]++
		if seen[id] == 1 {
			ids = append(ids, id)
		}
		if last != "" && !idBefore(last, id) {
			ordered = false
		}
		last = id
	}
	var duplicates []string
	for id, n := range seen {
		if n > 1 {
			duplicates = append(duplicates, id)
		}
	}
	sort.Strings(duplicates)
	verdicts := 0
	for _, n := range run.byVerdict {
		verdicts += n
	}
	problems := append([]string(nil), run.problems...)
	if run.changed {
		problems = append(problems, "the strategy set changed again while it was read a second time")
	}
	if run.written != count {
		problems = append(problems, fmt.Sprintf("rows %d, universe %d", run.written, count))
	}
	if len(run.rows) != run.written {
		problems = append(problems, fmt.Sprintf("rows received %d, pages say %d", len(run.rows), run.written))
	}
	if verdicts != count {
		problems = append(problems, fmt.Sprintf("verdicts sum to %d, universe %d", verdicts, count))
	}
	if len(duplicates) > 0 {
		problems = append(problems, fmt.Sprintf("%d ids written more than once", len(duplicates)))
	}
	if !ordered {
		problems = append(problems, "ids are not in increasing order across pages")
	}
	// The set, not only its size: the ids received, in the server's order,
	// must hash to the universe's digest. A page that lost one id and wrote
	// one from outside the universe passes every count and fails this.
	if digest := stringField(universe, "digest"); digest == "" || universeDigest(ids) != digest {
		problems = append(problems, "the ids received do not hash to the universe's digest")
	}
	holds := run.holds && !run.changed && len(problems) == 0
	status := "ok"
	summary := fmt.Sprintf("诊断覆盖全集 %d 条，%d 页；各结论条数之和等于全集", count, run.pages)
	if !holds {
		status = "partial"
		summary = fmt.Sprintf("覆盖不成立：%v", problems)
	}
	if stringField(universe, "status") != "ok" {
		status = "error"
		summary = "全集读不到（" + stringField(universe, "reason") + "），本次诊断无效"
	}
	coverage := map[string]any{"holds": holds, "universe": count, "rows": run.written, "distinct": len(ids),
		"verdicts": verdicts, "missing": count - len(ids), "duplicates": duplicates, "problems": problems}
	out := map[string]any{
		"status": status, "summary": summary,
		"evidence": map[string]any{"complete": holds, "limitations": problems},
		"result": map[string]any{"universe": universe, "deployment": run.first["deployment"], "pages": run.pages,
			"by_verdict": run.byVerdict, "coverage": coverage, "strategies": run.rows},
		"next_call": []any{},
		"meta":      map[string]any{"operation": diagnoseOperation},
	}
	return out
}

func number(value any) int {
	switch v := value.(type) {
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

// universeDigest is the server's NormalizeUniverse digest: the ids sorted
// numerically without duplicates, joined by commas, the first 8 bytes of
// their SHA-256 in hex.
func universeDigest(ids []string) string {
	sorted := append([]string(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool { return idBefore(sorted[i], sorted[j]) })
	unique := sorted[:0]
	for i, id := range sorted {
		if i > 0 && id == sorted[i-1] {
			continue
		}
		unique = append(unique, id)
	}
	sum := sha256.Sum256([]byte(strings.Join(unique, ",")))
	return hex.EncodeToString(sum[:8])
}

func idBefore(left, right string) bool {
	if len(left) != len(right) {
		return len(left) < len(right)
	}
	return left < right
}
