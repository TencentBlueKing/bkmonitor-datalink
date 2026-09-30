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
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// acceptFixture is a deployment for the acceptance to read: every answer
// healthy unless a test changes it.
type acceptFixture struct {
	mu sync.Mutex
	// metrics answers metrics.get for the replica named, or "leader" for a
	// control_leader read, with the call's number.
	metrics func(target string, call int) (string, map[string]any)
	// override answers an operation in place of the healthy answer.
	override map[string]func() (string, map[string]any)
	objects  int
	// objectsBody answers api/objects in place of the refusal.
	objectsBody string
	calls       map[string]int
	leader      func(call int) map[string]any
	// diagnosis answers diagnose.environment in place of two DETECTING rows.
	diagnosis func() map[string]any
}

func series(labels map[string]string, value float64) map[string]any {
	return map[string]any{"labels": labels, "value": value}
}

func healthyMetrics(families ...map[string]any) map[string]any {
	byName := map[string]map[string]any{
		metricSourceRefresh:  {"name": metricSourceRefresh, "series": []any{series(map[string]string{"status": "PUBLISHED"}, 12)}},
		metricScopeClose:     {"name": metricScopeClose, "series": []any{series(map[string]string{"outcome": "counted"}, 3), series(map[string]string{"outcome": "closed"}, 0)}},
		metricRecoveryBeside: {"name": metricRecoveryBeside, "series": []any{series(map[string]string{"beside": "level_unavailable"}, 2), series(map[string]string{"beside": "level_recovering"}, 1)}},
		metricEventsByKind:   {"name": metricEventsByKind, "series": []any{series(map[string]string{"format": "standard_raw_event", "event_kind": "RECOVERY"}, 7)}},
		metricEventsRejected: {"name": metricEventsRejected, "series": []any{series(map[string]string{"rule": "standard_encode", "strategy": "_other"}, 0), series(map[string]string{"rule": "legacy_strategy_invalid", "strategy": "_other"}, 0)}},
		metricLoopTurn: {"name": metricLoopTurn, "series": []any{map[string]any{"labels": map[string]string{"loop": "control"}, "count": 9,
			"buckets": map[string]any{"16.384": 4, "65.536": 9, "262.144": 9, "1048.576": 9}}}},
	}
	for _, family := range families {
		byName[stringField(family, "name")] = family
	}
	out := map[string]any{"families": []any{}, "absent": []any{metricRecoveryHeld, metricRejectedOverrun}}
	for _, family := range byName {
		out["families"] = append(out["families"].([]any), family)
	}
	return out
}

func healthyAnswer(operation string) (string, map[string]any) {
	switch operation {
	case "fleet.get":
		return "ok", map[string]any{"degradations": []any{}, "no_data_horizon": map[string]any{"seconds": 3600, "source": "DYNAMIC", "replica": "pod-a"},
			"per_replica": []any{map[string]any{"replica": "pod-a"}, map[string]any{"replica": "pod-b"}}}
	case "k8s.pods":
		return "ok", map[string]any{"pods": []any{
			map[string]any{"name": "pod-a", "phase": "Running", "containers": []any{map[string]any{"name": "alarmd", "restarts": 0}}},
			map[string]any{"name": "pod-b", "phase": "Running", "containers": []any{map[string]any{"name": "alarmd", "restarts": 1,
				"last_termination": map[string]any{"state": "terminated", "reason": "OOMKilled", "exit_code": 137}}}}}}
	case "k8s.events":
		return "ok", map[string]any{"events": []any{map[string]any{"type": "Warning", "reason": "BackOff", "message": "restarting"}}}
	case "store.info":
		return "ok", map[string]any{"complete": true, "servers": []any{map[string]any{"roles": []any{"runtime"}, "status": "ok",
			"fields": map[string]any{"maxmemory": "1000", "used_memory": "500", "maxmemory_policy": "noeviction", "evicted_keys": "0"}}}}
	}
	return "ok", map[string]any{}
}

func (f *acceptFixture) serve(t *testing.T, p *Profile) *httptest.Server {
	t.Helper()
	f.calls = map[string]int{}
	fixtureDigest = universeDigest([]string{"1", "2"})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/ob/api/cli/auth/exchange":
			writeJSON(t, w, sessionResponse(*p))
		case "/ob/api/objects":
			w.WriteHeader(f.objectsStatus())
			// The refusal as datalink publicsurface.Refuse writes it.
			body := `{"status":"error","error":{"code":"public_surface_restricted","message":"This read is restricted to CLI sessions.","login_page":"cli","login_href":"../cli"}}`
			if f.objectsBody != "" {
				body = f.objectsBody
			}
			_, _ = w.Write([]byte(body))
		case "/ob/api/health":
			writeJSON(t, w, map[string]any{"restricted": true, "health": "ok"})
		case "/ob/metrics":
			w.WriteHeader(http.StatusForbidden)
		case "/ob/cli":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html></html>"))
		case "/ob/api/cli/channel":
			if r.Header.Get("Authorization") == "" {
				t.Errorf("a channel call without the session")
			}
			var body map[string]any
			decoder := json.NewDecoder(r.Body)
			decoder.UseNumber()
			_ = decoder.Decode(&body)
			if stringField(body, "mode") == "discover" {
				var rows []any
				for _, operation := range acceptOperations {
					rows = append(rows, map[string]any{"operation": operation, "availability": map[string]any{"available": true}})
				}
				out := envelope(*p, "ok", map[string]any{"operations": rows})
				objectField(out, "meta")["build"] = "0.3.1/abcdef"
				writeJSON(t, w, out)
				return
			}
			operation := stringField(body, "operation")
			params := objectField(body, "params")
			f.calls[operation]++
			if operation == "diagnose.environment" {
				page := diagnosisPage(2, []string{"1", "2"}, 2, true, "")
				if f.diagnosis != nil {
					page = f.diagnosis()
				}
				writeJSON(t, w, envelope(*p, "ok", page))
				return
			}
			var status string
			var result map[string]any
			switch {
			case f.override[operation] != nil:
				status, result = f.override[operation]()
			case operation == "metrics.get":
				target := stringField(params, "replica")
				if params["control_leader"] == true {
					target = "leader"
				}
				status, result = f.metrics(target, f.calls[operation])
				// Like the server: only the families asked for.
				if status != "error" {
					asked := map[string]bool{}
					for _, name := range asList(params["names"]) {
						asked[fmt.Sprint(name)] = true
					}
					var kept []any
					for _, family := range asList(result["families"]) {
						if asked[stringField(family.(map[string]any), "name")] {
							kept = append(kept, family)
						}
					}
					result = map[string]any{"families": kept}
				}
			default:
				status, result = healthyAnswer(operation)
			}
			out := envelope(*p, status, result)
			if status == "error" {
				out["result"] = nil
				out["error"] = result
			}
			if params["control_leader"] == true && status != "error" {
				leader := map[string]any{"owner_id": "pod-a", "owner_epoch": 7}
				if f.leader != nil {
					leader = f.leader(f.calls[operation])
				}
				objectField(out, "meta")["control_leader"] = leader
			}
			writeJSON(t, w, out)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	*p = fixtureProfile(server.URL)
	return server
}

func (f *acceptFixture) objectsStatus() int {
	if f.objects != 0 {
		return f.objects
	}
	return http.StatusForbidden
}

func acceptRunOf(t *testing.T, f *acceptFixture, args ...string) (int, map[string]string, map[string]any) {
	t.Helper()
	var p Profile
	server := f.serve(t, &p)
	t.Cleanup(server.Close)
	store := loggedIn(t, p, server)
	code, result, _, _ := run(t, store, server.Client(), "", append([]string{"accept", "--env", p.EnvironmentID}, args...)...)
	verdicts := map[string]string{}
	items, _ := objectField(result, "result")["items"].([]any)
	for _, raw := range items {
		item := raw.(map[string]any)
		verdicts[stringField(item, "item")] = stringField(item, "verdict")
	}
	return code, verdicts, result
}

func healthyFixture() *acceptFixture {
	return &acceptFixture{metrics: func(string, int) (string, map[string]any) { return "partial", healthyMetrics() }}
}

// A healthy deployment passes every item; a metrics answer that is partial
// because an asked-for family is absent is still read; the items and the
// answers are saved in one record.
func TestAcceptPassesAHealthyDeployment(t *testing.T) {
	code, verdicts, result := acceptRunOf(t, healthyFixture(), "--expect-build", "0.3.1", "--window", "0")
	for item, verdict := range verdicts {
		if verdict == verdictFail || verdict == verdictReadFailed {
			t.Errorf("%s: %s", item, verdict)
		}
	}
	if code != 0 || result["status"] != "ok" {
		t.Fatalf("code %d result %v", code, result)
	}
	for item, want := range map[string]string{
		"session and build": verdictPass, "operations listed and available": verdictPass, "public surface not degraded": verdictPass,
		"no-data horizon": verdictPass, "pods": verdictPass, "pod restarts": verdictInfo, "redis read": verdictPass,
		"redis noeviction and nothing evicted": verdictPass, "redis memory": verdictPass, "metrics read on every replica": verdictPass,
		"source refresh counted": verdictPass, "held recovery counter removed": verdictPass, "recovery beside another Level": verdictPass,
		"target out of scope closes nothing": verdictPass, "control loop slowest turn": verdictPass, "output events refused by alarmd": verdictPass,
		"diagnosis covers every strategy": verdictPass, "strategies detecting": verdictInfo, "public: restricted read refused": verdictPass,
		"public: health carries no coordinates": verdictPass, "public: metrics not served": verdictPass, "public: login page served": verdictPass,
		"control source publication not starved": verdictUndecided, "control source publication conflicts": verdictPass,
		"control source pending age": verdictNotBuilt,
	} {
		if verdicts[item] != want {
			t.Errorf("%s = %q, want %s", item, verdicts[item], want)
		}
	}
	saved := savedResult(t, result)
	answers := objectField(objectField(saved, "result"), "answers")
	if answers["store"] == nil || answers["metrics-1"] == nil || answers["diagnosis"] == nil {
		t.Fatalf("answers not saved: %v", answers)
	}
}

// Each fact the deployment gets wrong fails its own item and the run.
func TestAcceptFailsEachWrongFact(t *testing.T) {
	f := healthyFixture()
	f.objects = http.StatusOK
	f.metrics = func(string, int) (string, map[string]any) {
		return "ok", healthyMetrics(
			map[string]any{"name": metricRecoveryHeld, "series": []any{series(nil, 0)}},
			map[string]any{"name": metricScopeClose, "series": []any{series(map[string]string{"outcome": "closed"}, 2)}},
			map[string]any{"name": metricEventsRejected, "series": []any{series(map[string]string{"rule": "standard_encode", "strategy": "1001"}, 4)}},
			map[string]any{"name": metricLoopTurn, "series": []any{map[string]any{"labels": map[string]string{"loop": "control"}, "count": 9,
				"buckets": map[string]any{"262.144": 8, "1048.576": 9}}}},
		)
	}
	f.override = map[string]func() (string, map[string]any){"store.info": func() (string, map[string]any) {
		return "ok", map[string]any{"complete": true, "servers": []any{map[string]any{"roles": []any{"runtime"}, "status": "ok",
			"fields": map[string]any{"maxmemory": "1000", "used_memory": "900", "maxmemory_policy": "allkeys-lru", "evicted_keys": "5"}}}}
	}}
	code, verdicts, result := acceptRunOf(t, f, "--window", "0")
	for _, item := range []string{"held recovery counter removed", "target out of scope closes nothing", "output events refused by alarmd",
		"control loop slowest turn", "redis noeviction and nothing evicted", "redis memory", "public: restricted read refused"} {
		if verdicts[item] != verdictFail {
			t.Errorf("%s = %q, want FAIL", item, verdicts[item])
		}
	}
	if code != 1 || result["status"] != "failed" {
		t.Fatalf("code %d status %v", code, result["status"])
	}
}

// A read that fails is not a pass: the item names it and the run fails.
func TestAcceptCountsAFailedReadAsNotPassing(t *testing.T) {
	f := healthyFixture()
	f.override = map[string]func() (string, map[string]any){"store.info": func() (string, map[string]any) {
		return "error", map[string]any{"code": "evidence_unavailable", "message": "the store did not answer"}
	}}
	code, verdicts, _ := acceptRunOf(t, f, "--window", "0")
	if verdicts["redis read"] != verdictReadFailed || verdicts["redis memory"] != verdictReadFailed || code != 1 {
		t.Fatalf("code %d verdicts %v", code, verdicts)
	}
}

// Beside-Level recoveries at zero are undecided, never failed: with no
// RECOVERY written there is nothing to count, and with some the zero counts
// occurrences, not errors.
func TestAcceptReadsTheBesideCountAgainstItsDenominator(t *testing.T) {
	for recoveries, want := range map[float64]string{0: "no RECOVERY written", 7: "counts occurrences, not errors"} {
		f := healthyFixture()
		f.metrics = func(string, int) (string, map[string]any) {
			return "ok", healthyMetrics(
				// One kind counted is not both: still undecided.
				map[string]any{"name": metricRecoveryBeside, "series": []any{series(map[string]string{"beside": "level_unavailable"}, 2)}},
				map[string]any{"name": metricEventsByKind, "series": []any{
					series(map[string]string{"format": "standard_raw_event", "event_kind": "RECOVERY"}, recoveries),
					series(map[string]string{"format": "standard_raw_event", "event_kind": "ABNORMAL"}, 5)}},
			)
		}
		code, verdicts, result := acceptRunOf(t, f, "--window", "0")
		if verdicts["recovery beside another Level"] != verdictUndecided || code != 0 {
			t.Fatalf("recoveries %g: code %d verdicts %v", recoveries, code, verdicts)
		}
		items, _ := objectField(result, "result")["items"].([]any)
		for _, raw := range items {
			item := raw.(map[string]any)
			if stringField(item, "item") != "recovery beside another Level" {
				continue
			}
			// Two replicas read, each with the same RECOVERY count.
			denominator := "RECOVERY events written since start " + strconv.FormatFloat(2*recoveries, 'g', -1, 64)
			if detail := stringField(item, "detail"); !strings.Contains(detail, denominator) || !strings.Contains(detail, want) {
				t.Fatalf("recoveries %g: detail %q, want %q and %q", recoveries, detail, denominator, want)
			}
		}
	}
}

// A refusal without the login page to go to is not the restricted surface.
func TestAcceptWantsTheRefusalToPointAtTheLoginPage(t *testing.T) {
	f := healthyFixture()
	f.objectsBody = `{"status":"error","error":{"code":"public_surface_restricted","login_page":"cli"}}`
	if code, verdicts, _ := acceptRunOf(t, f, "--window", "0"); verdicts["public: restricted read refused"] != verdictFail || code != 1 {
		t.Fatalf("code %d verdicts %v", code, verdicts)
	}
}

// The refresh counts are read from the Control Leader twice: confirmations
// pending with nothing published over the window is starvation; a Leader
// that changed between the reads leaves the increase undecided.
func TestAcceptReadsTheRefreshIncreaseFromTheLeader(t *testing.T) {
	refresh := func(pending, published float64) map[string]any {
		return healthyMetrics(map[string]any{"name": metricSourceRefresh, "series": []any{
			series(map[string]string{"status": "PENDING_CONFIRMATION"}, pending), series(map[string]string{"status": "PUBLISHED"}, published)}})
	}
	starved := healthyFixture()
	leaderReads := 0
	starved.metrics = func(target string, _ int) (string, map[string]any) {
		if target != "leader" {
			return "ok", refresh(1, 1)
		}
		leaderReads++
		return "ok", refresh(float64(leaderReads*5), 3)
	}
	code, verdicts, _ := acceptRunOf(t, starved, "--window", "10ms")
	if verdicts["control source publication not starved"] != verdictFail || code != 1 || leaderReads != 2 {
		t.Fatalf("starved: code %d reads %d verdicts %v", code, leaderReads, verdicts)
	}

	moved := healthyFixture()
	moved.leader = func(call int) map[string]any { return map[string]any{"owner_id": "pod-a", "owner_epoch": call} }
	if _, verdicts, _ := acceptRunOf(t, moved, "--window", "10ms"); verdicts["control source publication not starved"] != verdictUndecided {
		t.Fatalf("a Leader change: %v", verdicts)
	}
}

// A server that cannot target the Leader has its refresh counts summed over
// every replica instead.
func TestAcceptSumsTheReplicasWhereTheLeaderCannotBeTargeted(t *testing.T) {
	f := healthyFixture()
	replicaReads := 0
	f.metrics = func(target string, _ int) (string, map[string]any) {
		if target == "leader" {
			return "error", map[string]any{"code": "invalid_input", "message": "metrics.get does not accept control_leader"}
		}
		replicaReads++
		return "ok", healthyMetrics()
	}
	code, verdicts, _ := acceptRunOf(t, f, "--window", "10ms")
	if verdicts["control source publication not starved"] != verdictPass || code != 0 || replicaReads < 4 {
		t.Fatalf("code %d replica reads %d verdicts %v", code, replicaReads, verdicts)
	}
}

// The acceptance flags belong to accept alone.
func TestAcceptFlagsAreAcceptOnly(t *testing.T) {
	code, result, _, _ := run(t, Store{t.TempDir()}, http.DefaultClient, "", "discover", "--env", "e", "--window", "5m")
	if code != 2 || stringField(objectField(result, "error"), "code") != "invalid_input" {
		t.Fatalf("code %d %v", code, result)
	}
	code, _, _, _ = run(t, Store{t.TempDir()}, http.DefaultClient, "", "accept", "--env", "e", "--window", "2h")
	if code != 2 {
		t.Fatalf("a window past an hour: code %d", code)
	}
}

// Details too long for stdout leave every verdict there and the details in
// the saved record.
func TestAcceptKeepsEveryVerdictOnStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	a := New(strings.NewReader(""), &stdout, &stderr, "test")
	a.Store = Store{t.TempDir()}
	run := &acceptRun{app: a, answers: map[string]any{}}
	for index := 0; index < 30; index++ {
		run.add("item "+strconv.Itoa(index), verdictInfo, strings.Repeat("长", 1000))
	}
	run.add("the last", verdictFail, "short")
	code := a.acceptRecord(run)
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || stdout.Len() > stdoutBudget {
		t.Fatalf("stdout %d bytes: %v", stdout.Len(), err)
	}
	items, _ := objectField(result, "result")["items"].([]any)
	last := items[len(items)-1].(map[string]any)
	if code != 1 || result["result_omitted"] != true || len(items) != 31 || stringField(last, "verdict") != verdictFail {
		t.Fatalf("code %d omitted %v items %d last %v", code, result["result_omitted"], len(items), last)
	}
	saved, _ := objectField(savedResult(t, result), "result")["items"].([]any)
	if detail := stringField(saved[0].(map[string]any), "detail"); len([]rune(detail)) != acceptDetailRunes+1 {
		t.Fatalf("the saved record holds %d runes of the detail", len([]rune(detail)))
	}
}

// One more pending answer than before is a change the next round confirms;
// two with nothing published is starvation. The Leader's pending age past the
// window fails on its own, and a build without the gauge says it has none.
func TestAcceptJudgesPendingConfirmationOnBothSidesOfItsBound(t *testing.T) {
	leaderRefresh := func(step float64, age *float64) func(string, int) (string, map[string]any) {
		reads := 0.0
		return func(target string, _ int) (string, map[string]any) {
			families := []map[string]any{{"name": metricSourceRefresh, "series": []any{
				series(map[string]string{"status": "PENDING_CONFIRMATION"}, reads*step), series(map[string]string{"status": "PUBLISHED"}, 3)}}}
			if target == "leader" {
				reads++
				if age != nil {
					families = append(families, map[string]any{"name": metricPendingAge, "series": []any{series(nil, *age)}})
				}
			}
			return "ok", healthyMetrics(families...)
		}
	}
	for step, want := range map[float64]string{1: verdictPass, 2: verdictFail} {
		f := healthyFixture()
		f.metrics = leaderRefresh(step, nil)
		if _, verdicts, _ := acceptRunOf(t, f, "--window", "10ms"); verdicts["control source publication not starved"] != want ||
			verdicts["control source pending age"] != verdictNotBuilt {
			t.Fatalf("pending up by %g: %v", step, verdicts)
		}
	}
	for age, want := range map[float64]string{0: verdictPass, 300: verdictPass, 301: verdictFail} {
		f := healthyFixture()
		f.metrics = leaderRefresh(0, &age)
		if _, verdicts, _ := acceptRunOf(t, f, "--window", "0"); verdicts["control source pending age"] != want {
			t.Fatalf("pending for %g s against the default five minutes: %v", age, verdicts["control source pending age"])
		}
	}
}

// A refusal is judged by who owns its rule: the strategy's configuration or
// data is listed for governance and passes; alarmd's own fails; a rule the
// table does not name is taken as alarmd's and says so; a build that still
// counts the strategy's compatible refusals under alarmd's rule is told apart.
func TestAcceptJudgesARefusalByWhoOwnsItsRule(t *testing.T) {
	refused := func(rows ...map[string]any) *acceptFixture {
		f := healthyFixture()
		f.metrics = func(string, int) (string, map[string]any) {
			return "ok", healthyMetrics(map[string]any{"name": metricEventsRejected, "series": toAny2(rows)})
		}
		return f
	}
	cell := func(rule, strategy string, value float64) map[string]any {
		return series(map[string]string{"rule": rule, "strategy": strategy}, value)
	}
	detailOf := func(result map[string]any, item string) (string, string) {
		items, _ := objectField(result, "result")["items"].([]any)
		for _, raw := range items {
			if row := raw.(map[string]any); stringField(row, "item") == item {
				return stringField(row, "detail"), stringField(row, "column")
			}
		}
		return "", ""
	}
	const own, governed = "output events refused by alarmd", "output events refused by strategy configuration or data"

	code, verdicts, result := acceptRunOf(t, refused(cell("legacy_strategy_invalid", "1001", 3), cell("standard_business_identity", "1002", 1),
		cell("legacy_payload_too_large", "1003", 2), cell("standard_encode", "_other", 0)), "--window", "0")
	detail, column := detailOf(result, governed)
	if code != 0 || verdicts[own] != verdictPass || verdicts[governed] != verdictInfo || column != columnGovernance ||
		// Summed over the two replicas read.
		!strings.Contains(detail, "legacy_strategy_invalid/1001=6") || !strings.Contains(detail, "standard_business_identity/1002=2") {
		t.Fatalf("the strategy's refusals: code %d verdicts %v governance %q in %q", code, verdicts, detail, column)
	}

	for _, rule := range []string{"standard_identity_missing", "standard_action_unknown", "standard_levels_invalid", "standard_too_many_levels",
		"standard_encode", "event_invalid", "format_unsupported", "legacy_context_missing", "legacy_conversion_rejected", "legacy_output_invalid", "_other"} {
		code, verdicts, _ := acceptRunOf(t, refused(cell(rule, "1001", 1), cell("legacy_strategy_invalid", "_other", 0)), "--window", "0")
		if code != 1 || verdicts[own] != verdictFail {
			t.Fatalf("alarmd's rule %s: code %d verdict %s", rule, code, verdicts[own])
		}
	}

	_, verdicts, result = acceptRunOf(t, refused(cell("some_future_rule", "1001", 1), cell("legacy_strategy_invalid", "_other", 0)), "--window", "0")
	if detail, _ := detailOf(result, own); verdicts[own] != verdictFail || !strings.Contains(detail, "not in the ownership table") {
		t.Fatalf("an unnamed rule: %s %q", verdicts[own], detail)
	}

	_, verdicts, result = acceptRunOf(t, refused(cell("legacy_conversion_rejected", "1001", 1)), "--window", "0")
	if detail, _ := detailOf(result, own); verdicts[own] != verdictFail || !strings.Contains(detail, "has no legacy_strategy_invalid") {
		t.Fatalf("a build before the split: %s %q", verdicts[own], detail)
	}
	_, _, result = acceptRunOf(t, refused(cell("legacy_conversion_rejected", "1001", 1), cell("legacy_strategy_invalid", "_other", 0)), "--window", "0")
	if detail, _ := detailOf(result, own); strings.Contains(detail, "has no legacy_strategy_invalid") {
		t.Fatalf("a build with the split told it has none: %q", detail)
	}
}

func toAny2(rows []map[string]any) []any {
	out := make([]any, len(rows))
	for index, row := range rows {
		out[index] = row
	}
	return out
}

// The login page named at the top level, where the server never puts it, is
// not the refusal: only the server's own shape passes.
func TestAcceptReadsTheRefusalWhereTheServerWritesIt(t *testing.T) {
	f := healthyFixture()
	f.objectsBody = `{"error":"public_surface_restricted","login_href":"../cli"}`
	if code, verdicts, _ := acceptRunOf(t, f, "--window", "0"); verdicts["public: restricted read refused"] != verdictFail || code != 1 {
		t.Fatalf("code %d verdicts %v", code, verdicts)
	}
}

// The login link is judged by where it leads, not by its text: a server that
// sees the entry's prefix writes a longer relative path to the same page and
// passes; a link to anywhere else fails.
func TestAcceptFollowsTheLoginLinkToWhereItLeads(t *testing.T) {
	for href, want := range map[string]string{"../cli": verdictPass, "/ob/cli": verdictPass, "../../ob/cli": verdictPass,
		"../../cli": verdictFail, "cli": verdictFail, "https://elsewhere.example/ob/cli": verdictFail} {
		f := healthyFixture()
		f.objectsBody = `{"status":"error","error":{"code":"public_surface_restricted","login_href":"` + href + `"}}`
		if _, verdicts, _ := acceptRunOf(t, f, "--window", "0"); verdicts["public: restricted read refused"] != want {
			t.Fatalf("login_href %q: %s, want %s", href, verdicts["public: restricted read refused"], want)
		}
	}
}

// notDetectingPage is a covered page whose rows carry the verdicts given,
// each strategy not DETECTING with the refusal the compiler made of it.
func notDetectingPage(verdicts map[string]string) map[string]any {
	ids := make([]string, 0, len(verdicts))
	for id := range verdicts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	fixtureDigest = universeDigest(ids)
	page := diagnosisPage(len(ids), ids, len(ids), true, "")
	byVerdict := map[string]any{}
	rows := []any{}
	for _, id := range ids {
		verdict := verdicts[id]
		byVerdict[verdict] = number(byVerdict[verdict]) + 1
		row := map[string]any{"strategy_id": id, "verdict": verdict}
		if verdict != "DETECTING" {
			row["reason"] = "LEVEL_INVALID"
			row["dispositions"] = []any{
				map[string]any{"scope": "PLAN", "disposition": "ACCEPTED"},
				map[string]any{"scope": "LEVEL", "level_id": 1, "disposition": "CONFIG_REJECTED", "reason": "LEVEL_INVALID",
					"field_path": "level.trigger_plan", "detail": `json: unknown field "cw_calendars"`},
			}
		}
		rows = append(rows, row)
	}
	objectField(page, "page")["by_verdict"] = byVerdict
	page["strategies"] = rows
	return page
}

// Covering every strategy is not detecting with them. A set that lists
// strategies and detects none fails, and names each one with the layer it
// stopped at and the compiler's words, so one read shows what a release
// would otherwise uncover one at a time.
func TestAcceptFailsASetThatDetectsNothingAndNamesWhereEachStopped(t *testing.T) {
	f := healthyFixture()
	f.diagnosis = func() map[string]any {
		return notDetectingPage(map[string]string{"54": "NOT_DETECTING", "55": "NOT_DETECTING"})
	}
	code, verdicts, result := acceptRunOf(t, f, "--window", "0")
	if verdicts["diagnosis covers every strategy"] != verdictPass || verdicts["strategies detecting"] != verdictFail || code != 1 {
		t.Fatalf("code %d verdicts %v", code, verdicts)
	}
	var detail string
	items, _ := objectField(result, "result")["items"].([]any)
	for _, raw := range items {
		if item := raw.(map[string]any); stringField(item, "item") == "strategies detecting" {
			detail = stringField(item, "detail")
		}
	}
	for _, want := range []string{"0 of 2 detecting", "NOT_DETECTING=2", "54 NOT_DETECTING LEVEL_INVALID [LEVEL LEVEL_INVALID level.trigger_plan]", `unknown field "cw_calendars"`} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail lacks %q: %s", want, detail)
		}
	}
	saved := savedResult(t, result)
	listed, _ := objectField(objectField(saved, "result"), "answers")["not_detecting"].([]any)
	if len(listed) != 2 {
		t.Fatalf("record lists %v, want both strategies", listed)
	}
	refusals, _ := listed[0].(map[string]any)["refusals"].([]any)
	if len(refusals) != 1 || stringField(refusals[0].(map[string]any), "field_path") != "level.trigger_plan" {
		t.Fatalf("refusals = %v, want the one refusal, not the accepted plan", refusals)
	}
}

// A set that detects some of its strategies is information, with the rest
// named; an empty set has nothing to detect.
func TestAcceptListsWhatDoesNotDetectBesideWhatDoes(t *testing.T) {
	f := healthyFixture()
	f.diagnosis = func() map[string]any {
		return notDetectingPage(map[string]string{"7": "DETECTING", "8": "DATA_ABSENT"})
	}
	_, verdicts, result := acceptRunOf(t, f, "--window", "0")
	if verdicts["strategies detecting"] != verdictInfo {
		t.Fatalf("verdicts %v", verdicts)
	}
	saved := savedResult(t, result)
	listed, _ := objectField(objectField(saved, "result"), "answers")["not_detecting"].([]any)
	if len(listed) != 1 || stringField(listed[0].(map[string]any), "strategy_id") != "8" {
		t.Fatalf("record lists %v, want strategy 8 alone", listed)
	}
	f.diagnosis = func() map[string]any { return notDetectingPage(map[string]string{}) }
	if _, verdicts, _ := acceptRunOf(t, f, "--window", "0"); verdicts["strategies detecting"] != verdictInfo {
		t.Fatalf("an empty set: %v", verdicts)
	}
}

// A diagnosis that did not cover the set says nothing about all of it: the
// table is information even when the part read detects nothing.
func TestAcceptDoesNotFailDetectingOnAPartialDiagnosis(t *testing.T) {
	f := healthyFixture()
	f.diagnosis = func() map[string]any {
		page := notDetectingPage(map[string]string{"54": "NOT_DETECTING", "55": "NOT_DETECTING"})
		objectField(page, "page")["holds"] = false
		return page
	}
	_, verdicts, result := acceptRunOf(t, f, "--window", "0")
	if verdicts["diagnosis covers every strategy"] != verdictFail || verdicts["strategies detecting"] != verdictInfo {
		t.Fatalf("verdicts %v", verdicts)
	}
	items, _ := objectField(result, "result")["items"].([]any)
	for _, raw := range items {
		if item := raw.(map[string]any); stringField(item, "item") == "strategies detecting" &&
			!strings.HasPrefix(stringField(item, "detail"), "coverage does not hold") {
			t.Fatalf("detail = %q, want it to say the read was partial", stringField(item, "detail"))
		}
	}
}
