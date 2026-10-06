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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// fixtureDigest is the digest the pages claim; each test sets it to the
// universe it means, the way the server computes it.
var fixtureDigest = ""

func diagnosisPage(count int, ids []string, expected int, holds bool, next string) map[string]any {
	rows := []any{}
	byVerdict := map[string]any{"DETECTING": 0.0, "UNKNOWN": 0.0}
	for _, id := range ids {
		rows = append(rows, map[string]any{"strategy_id": id, "verdict": "DETECTING"})
		byVerdict["DETECTING"] = byVerdict["DETECTING"].(float64) + 1
	}
	page := map[string]any{"universe": map[string]any{"status": "ok", "count": float64(count), "digest": fixtureDigest},
		"page":       map[string]any{"ids_expected": float64(expected), "rows": float64(len(ids)), "holds": holds, "by_verdict": byVerdict},
		"strategies": rows, "deployment": map[string]any{"findings": []any{}}}
	if next != "" {
		page["next_cursor"] = next
	}
	return page
}

// diagnoseServer answers describe, then each invoke with the next page the
// script gives for the cursor it was asked with.
func diagnoseServer(t *testing.T, p *Profile, pages func(cursor string, call int) map[string]any) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ob/api/cli/auth/exchange":
			writeJSON(t, w, sessionResponse(*p))
		case "/ob/api/cli/channel":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if stringField(body, "operation") != diagnoseOperation {
				t.Errorf("operation %v", body["operation"])
			}
			if stringField(body, "mode") == "describe" {
				writeJSON(t, w, envelope(*p, "ok", map[string]any{}))
				return
			}
			calls++
			page := pages(stringField(objectField(body, "params"), "cursor"), calls)
			if refusal, refused := page["refused"].(string); refused {
				out := envelope(*p, "error", nil)
				out["error"] = map[string]any{"code": "diagnosis_refused", "message": refusal}
				writeJSON(t, w, out)
				return
			}
			status := "ok"
			if u := objectField(page, "universe"); stringField(u, "status") != "ok" {
				status = "partial"
			}
			writeJSON(t, w, envelope(*p, status, page))
		default:
			w.WriteHeader(404)
		}
	}))
	*p = fixtureProfile(server.URL)
	return server, &calls
}

func loggedIn(t *testing.T, p Profile, server *httptest.Server) Store {
	t.Helper()
	store := Store{t.TempDir()}
	if code, _, _, _ := run(t, store, server.Client(), bundle(p), "auth", "login"); code != 0 {
		t.Fatal("login failed")
	}
	return store
}

func savedResult(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	data, err := os.ReadFile(stringField(objectField(result, "meta"), "result_file"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	_ = json.Unmarshal(data, &saved)
	return saved
}

// Two pages, each proving its slice: the CLI sums them, finds every id once
// and the verdicts summing to the universe, and exits 0 with one record.
func TestDiagnoseReadsEveryPageAndProvesTheCoverage(t *testing.T) {
	var p Profile
	fixtureDigest = universeDigest([]string{"1", "2", "3", "4", "10"})
	server, calls := diagnoseServer(t, &p, func(cursor string, _ int) map[string]any {
		if cursor == "" {
			return diagnosisPage(5, []string{"1", "2", "3"}, 3, true, "c1")
		}
		return diagnosisPage(5, []string{"4", "10"}, 2, true, "")
	})
	defer server.Close()
	store := loggedIn(t, p, server)
	code, result, _, _ := run(t, store, server.Client(), "", "diagnose", "--env", p.EnvironmentID)
	saved := savedResult(t, result)
	coverage := objectField(objectField(saved, "result"), "coverage")
	if code != 0 || result["status"] != "ok" || coverage["holds"] != true || *calls != 2 || coverage["missing"] != 0.0 {
		t.Fatalf("code %d status %v coverage %v calls %d", code, result["status"], coverage, *calls)
	}
}

// An id written twice, or rows not summing to the universe, is not a
// diagnosis: the record names the problem and the exit is 3.
func TestDiagnoseExitsNonZeroWhenThePagesDoNotAddUp(t *testing.T) {
	fixtureDigest = universeDigest([]string{"1", "2", "3", "4", "5"})
	var p Profile
	server, _ := diagnoseServer(t, &p, func(cursor string, _ int) map[string]any {
		if cursor == "" {
			return diagnosisPage(5, []string{"1", "2", "3"}, 3, true, "c1")
		}
		return diagnosisPage(5, []string{"3", "4"}, 2, true, "")
	})
	defer server.Close()
	store := loggedIn(t, p, server)
	code, result, _, _ := run(t, store, server.Client(), "", "diagnose", "--env", p.EnvironmentID)
	coverage := objectField(objectField(savedResult(t, result), "result"), "coverage")
	problems, _ := json.Marshal(coverage["problems"])
	if code != 3 || result["status"] != "partial" || coverage["holds"] != false || !strings.Contains(string(problems), "more than once") {
		t.Fatalf("code %d status %v coverage %v", code, result["status"], coverage)
	}
}

// A set that moved between pages is read again from the first page once;
// the second reading is the record.
func TestDiagnoseReadsAgainOnceWhenTheSetChanged(t *testing.T) {
	fixtureDigest = universeDigest([]string{"1", "2", "5"})
	var p Profile
	server, calls := diagnoseServer(t, &p, func(cursor string, call int) map[string]any {
		switch {
		case call == 2:
			page := diagnosisPage(3, nil, 0, true, "")
			page["universe_changed"] = map[string]any{"from_digest": "d1", "to_digest": "d2"}
			return page
		case cursor == "":
			return diagnosisPage(3, []string{"1", "2"}, 2, true, "c1")
		default:
			return diagnosisPage(3, []string{"5"}, 1, true, "")
		}
	})
	defer server.Close()
	store := loggedIn(t, p, server)
	code, result, _, stderr := run(t, store, server.Client(), "", "diagnose", "--env", p.EnvironmentID)
	if code != 0 || *calls != 4 || !strings.Contains(stderr, "changed between pages") {
		t.Fatalf("code %d calls %d stderr %q result %v", code, *calls, stderr, result)
	}
}

// With no universe there is no diagnosis: an error, exit 1.
func TestDiagnoseFailsWhenTheUniverseCannotBeRead(t *testing.T) {
	var p Profile
	server, _ := diagnoseServer(t, &p, func(string, int) map[string]any {
		return map[string]any{"universe": map[string]any{"status": "unreadable", "reason": "SOURCE_INCOMPLETE"},
			"page": map[string]any{"holds": false}, "strategies": []any{}}
	})
	defer server.Close()
	store := loggedIn(t, p, server)
	code, result, _, _ := run(t, store, server.Client(), "", "diagnose", "--env", p.EnvironmentID)
	if code != 1 || result["status"] != "error" || !strings.Contains(stringField(result, "summary"), "SOURCE_INCOMPLETE") {
		t.Fatalf("code %d result %v", code, result)
	}
}

// Every count right and the set wrong: a page lost id 4 and wrote id 99,
// outside the universe. Rows, distinct ids, order and verdicts all add up;
// the digest of the ids received does not, and the exit is 3.
func TestDiagnoseProvesTheSetNotOnlyItsSize(t *testing.T) {
	fixtureDigest = universeDigest([]string{"1", "2", "3", "4", "10"})
	var p Profile
	server, _ := diagnoseServer(t, &p, func(cursor string, _ int) map[string]any {
		if cursor == "" {
			return diagnosisPage(5, []string{"1", "2", "3"}, 3, true, "c1")
		}
		return diagnosisPage(5, []string{"10", "99"}, 2, true, "")
	})
	defer server.Close()
	store := loggedIn(t, p, server)
	code, result, _, _ := run(t, store, server.Client(), "", "diagnose", "--env", p.EnvironmentID)
	coverage := objectField(objectField(savedResult(t, result), "result"), "coverage")
	problems, _ := json.Marshal(coverage["problems"])
	if code != 3 || !strings.Contains(string(problems), "digest") || strings.Contains(string(problems), "more than once") {
		t.Fatalf("code %d problems %s", code, problems)
	}
}

// The digest is the server's: the golden value is fleet.NormalizeUniverse's
// digest of the same ids, pinned on both sides (fleet's
// TestNormalizeUniverseDigestIsPinned) so neither can drift alone.
func TestUniverseDigestMatchesTheServersAlgorithm(t *testing.T) {
	if got := universeDigest([]string{"10", "2", "2", "1", "379"}); got != "5fa412068ab113ae" {
		t.Fatalf("digest %q, want the server's 5fa412068ab113ae", got)
	}
}

// A page the replica could not forward to the Leader is asked for again, at
// most twice; the run goes on from where it was and proves its coverage as
// before. A third refusal, or any other refusal, ends the run with it.
func TestDiagnoseAsksAgainWhenTheLeaderDidNotAnswerAForward(t *testing.T) {
	diagnosisRetryWait = 0
	defer func() { diagnosisRetryWait = time.Second }()
	fixtureDigest = universeDigest([]string{"1", "2", "3"})
	for _, tc := range []struct {
		name     string
		refusals int
		refusal  string
		code     int
		calls    int
	}{
		{"answered on the second ask", 1, "LEADER_UNAVAILABLE", 0, 3},
		{"answered on the third ask", 2, "LEADER_UNAVAILABLE", 0, 4},
		{"never answered", 3, "LEADER_UNAVAILABLE", 1, 3},
		{"another refusal is not asked again", 1, "UNIVERSE_UNREADABLE", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var p Profile
			server, calls := diagnoseServer(t, &p, func(cursor string, call int) map[string]any {
				if call <= tc.refusals {
					return map[string]any{"refused": tc.refusal}
				}
				if cursor == "" {
					return diagnosisPage(3, []string{"1", "2"}, 2, true, "c1")
				}
				return diagnosisPage(3, []string{"3"}, 1, true, "")
			})
			defer server.Close()
			store := loggedIn(t, p, server)
			code, result, _, stderr := run(t, store, server.Client(), "", "diagnose", "--env", p.EnvironmentID)
			if (code == 0) != (tc.code == 0) || *calls != tc.calls {
				t.Fatalf("code %d calls %d result %v stderr %q", code, *calls, result, stderr)
			}
			asked := strings.Count(stderr, "asking again")
			if want := min(tc.refusals, diagnosisLeaderRetries); tc.refusal == "LEADER_UNAVAILABLE" && asked != want {
				t.Errorf("asked again %d times, want %d: %q", asked, want, stderr)
			}
			if tc.code == 0 {
				coverage := objectField(objectField(savedResult(t, result), "result"), "coverage")
				if coverage["holds"] != true {
					t.Errorf("coverage %v", coverage)
				}
			}
		})
	}
}
