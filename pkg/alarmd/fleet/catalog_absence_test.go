// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Every state the control plane can be in when a point read finds no
// catalog has its own word, each fixture differs from the next in one fact,
// and the word is decided from that fact rather than from the nil the lookup
// returned. The table is checked against the closed list both ways.
//
// The deployment this is for answered "not the Leader, or the Leader before
// its first round" to every id asked for, while leading and eighty-five
// rounds in. Both halves of the answer were false and the true one -- the
// exit its rounds kept stopping at -- was on its own first screen.
func TestEveryReasonACatalogCanBeAbsentForHasItsOwnWord(t *testing.T) {
	t.Parallel()
	halfHour := 1800.0
	states := map[CatalogAbsenceReason]CatalogAbsenceFacts{
		CatalogAbsenceUnknown:           {},
		CatalogAbsenceNotLeader:         {Known: true, Role: "follower"},
		CatalogAbsenceFirstRoundPending: {Role: "leader"},
		CatalogAbsencePublishFailing: {Known: true, Role: "leader", Exit: "retain_executable",
			Text: "runtime compiler returned an unclassified terminal reason", FailingSeconds: &halfHour},
		CatalogAbsenceIndexMissing: {Known: true, Role: "leader"},
	}
	if len(states) != len(CatalogAbsenceReasons) {
		t.Fatalf("%d fixtures for %d reasons: every reason in the closed list needs one", len(states), len(CatalogAbsenceReasons))
	}
	for _, reason := range CatalogAbsenceReasons {
		facts, listed := states[reason]
		if !listed {
			t.Fatalf("reason %s has no fixture", reason)
		}
		// A reason with no facts at all is the one the nil source produces,
		// and it is reached through "not wired" rather than through a role.
		absence := catalogAbsenceOf(facts, "pod-a", reason != CatalogAbsenceUnknown)
		if absence.Reason != reason {
			t.Errorf("fixture for %s was refused under %s", reason, absence.Reason)
			continue
		}
		if absence.Error != "NOT_PUBLISHED" || absence.Replica != "pod-a" || absence.Detail == "" {
			t.Errorf("%s: %+v, want the error word, the replica and a sentence", reason, absence)
		}
		// The sentence is Chinese prose; the words are for machines. A
		// sentence that is only the word back again tells a person nothing.
		if strings.Contains(absence.Detail, string(reason)) {
			t.Errorf("%s: sentence %q repeats the code instead of saying what happened", reason, absence.Detail)
		}
	}
	// The failing state carries the facts the word was decided from, so a
	// reader who does not know the word still learns what happened -- and
	// the exit is the one fact that says which failure it is.
	failing := catalogAbsenceOf(states[CatalogAbsencePublishFailing], "pod-a", true)
	if failing.Exit != "retain_executable" || failing.Text == "" || failing.FailingSeconds == nil ||
		!strings.Contains(failing.Detail, "retain_executable") || !strings.Contains(failing.Detail, "30 分钟") {
		t.Errorf("failing = %+v, want the exit, its text and how long, in the sentence too", failing)
	}
	// Under a minute is not reported as zero minutes.
	brief := 20.0
	short := catalogAbsenceOf(CatalogAbsenceFacts{Known: true, Role: "leader", Exit: "publish", FailingSeconds: &brief}, "pod-a", true)
	if strings.Contains(short.Detail, "分钟") {
		t.Errorf("sentence %q, want no duration clause under a minute rather than a zero", short.Detail)
	}
	// The way out is offered only where the route is mounted.
	withRoute := states[CatalogAbsencePublishFailing]
	withRoute.DirectoryMounted = true
	offered := catalogAbsenceOf(withRoute, "pod-a", true)
	if offered.Next != strategyDirectoryRoute || !strings.Contains(offered.Detail, strategyDirectoryRoute) {
		t.Errorf("offered = %+v, want the directory route named on the body and in the sentence", offered)
	}
	if failing.Next != "" || strings.Contains(failing.Detail, strategyDirectoryRoute) {
		t.Errorf("unmounted = %+v, want no way out named: a route that is not mounted is not one", failing)
	}
	// Not knowing does not borrow a role from the facts it could not read.
	if unknown := catalogAbsenceOf(CatalogAbsenceFacts{Role: "leader"}, "pod-a", false); unknown.Reason != CatalogAbsenceUnknown || unknown.Role != "" {
		t.Errorf("unwired = %+v, want UNKNOWN with no role", unknown)
	}
}

// The refusal the route actually writes: the body carries the word and the
// sentence, not the two guesses it used to carry. A page that shows the
// body has something to show; one that shows only the status code does not.
func TestThePointReadRefusalSaysWhyThereIsNoCatalog(t *testing.T) {
	t.Parallel()
	halfHour := 1800.0
	handler := standingHandler(t, func(string) StrategyLookupFacts { return StrategyLookupFacts{} }, nil)
	handler = WithStrategyStanding(handler, nil, func(string) StrategyLookupFacts { return StrategyLookupFacts{} }, nil, nil,
		func() CatalogAbsenceFacts {
			return CatalogAbsenceFacts{Known: true, Role: "leader", Exit: "retain_executable",
				Text: "runtime compiler returned an unclassified terminal reason", FailingSeconds: &halfHour, DirectoryMounted: true}
		}, "pod-a", func() time.Time { return now }, 0)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/strategies/4101", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	var body CatalogAbsence
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", recorder.Body.String(), err)
	}
	if body.Reason != CatalogAbsencePublishFailing || body.Exit != "retain_executable" || body.Next != strategyDirectoryRoute ||
		body.Replica != "pod-a" || body.Detail == "" {
		t.Errorf("body = %+v, want the failing word with its exit, the replica and the way out", body)
	}
	if strings.Contains(recorder.Body.String(), "before its first round") {
		t.Error("the refusal still names a cause it did not check")
	}
}
