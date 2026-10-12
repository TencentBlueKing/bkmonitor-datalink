// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/absentalerts"
)

// A cursor is the key it was made from, whatever the tenant holds.
func TestAnAbsentCursorReadsBackItsKey(t *testing.T) {
	for _, key := range []absentalerts.Key{{TenantID: "system", StrategyID: "10"}, {StrategyID: "10"}, {TenantID: "a/b c", StrategyID: "7"}} {
		parsed, ok := ParseAbsentCursor(EncodeAbsentCursor(key))
		if !ok || parsed != key {
			t.Fatalf("%+v read back as %+v %v", key, parsed, ok)
		}
	}
	for _, raw := range []string{"%%", EncodeAbsentCursor(absentalerts.Key{TenantID: "system"})} {
		if _, ok := ParseAbsentCursor(raw); ok {
			t.Fatalf("%q was read as a cursor", raw)
		}
	}
}

// The page refuses what it cannot answer as asked, and passes on what it
// can.
func TestTheAbsentPageRefusesAWordOutsideItsLists(t *testing.T) {
	var asked AbsentCandidateQuery
	source := func() AbsentCandidateSource {
		return func(_ context.Context, query AbsentCandidateQuery) (AbsentCandidatesResponse, bool) {
			asked = query
			return AbsentCandidatesResponse{State: AbsentStateReady}, true
		}
	}
	handler := WithAbsentCandidates(http.NotFoundHandler(), source, nil, "replica-a")
	for query, refusal := range map[string]string{
		"limit=0": "INVALID_LIMIT", "limit=201": "INVALID_LIMIT", "limit=x": "INVALID_LIMIT",
		"cursor=%25%25": "INVALID_CURSOR", "outcome=gone": "OUTCOME_UNKNOWN", "execution=sent": "EXECUTION_UNKNOWN",
		// The word the decision count used before it said decided.
		"outcome=closed": "OUTCOME_UNKNOWN",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/absent?"+query, nil))
		if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), refusal) {
			t.Fatalf("%s: %d %s", query, recorder.Code, recorder.Body.String())
		}
	}
	cursor := EncodeAbsentCursor(absentalerts.Key{TenantID: "system", StrategyID: "10"})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet,
		"/api/absent?limit=200&cursor="+cursor+"&outcome=close_decided&execution=not_run&strategy_id=10", nil))
	if recorder.Code != http.StatusOK || asked.Limit != 200 || asked.After == nil || asked.After.StrategyID != "10" ||
		asked.Outcome != absentalerts.OutcomeCloseDecided || asked.Execution != AbsentExecutionNotRun || asked.StrategyID != "10" {
		t.Fatalf("the page was not asked what the request asked: %d %+v", recorder.Code, asked)
	}
	if !strings.Contains(recorder.Body.String(), `"answered_by":"replica-a"`) || !strings.Contains(recorder.Body.String(), `"rows":[]`) {
		t.Fatalf("an empty page does not say who answered or that it has no rows: %s", recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/absent", nil))
	if asked.Limit != AbsentPageDefaultRows {
		t.Fatalf("a page asked without a limit got %d rows, want %d", asked.Limit, AbsentPageDefaultRows)
	}
}
