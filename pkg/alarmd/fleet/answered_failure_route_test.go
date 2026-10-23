// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A failure the provider answered - a 200 whose status said the query
// failed - carries the request it answered and the provider's message. The
// message is kept on this replica by the refusal's rule, for the
// session-gated read; the public strategy routes answer exactly what they
// answered before the request facts existed: no message, no trace id, no
// status, no times, no timeout source.
func TestTheStrategyRoutesCarryNothingOfAnAnsweredFailuresRequest(t *testing.T) {
	// The Query Group planA's fixture names.
	const group, traceID = "qg-4101-a", "4bf92f3577b34da6a3ce929d0e0e4736"
	const excerpt = "bkbase query_sync timeout for pod=payments-7f9c at <address>"
	serve := func(request *observability.QueryRequestFacts) (map[string]string, *Tracker) {
		at := &clock{at: now}
		tracker := newTracker(t, at)
		for round := 0; round < DefaultDegradedRounds; round++ {
			slot := int64(100 + 60*round)
			var facts *observability.QueryRequestFacts
			if request != nil {
				copied := *request
				facts = &copied
			}
			tracker.Observe(context.Background(), observability.Observation{
				QueryFailure: &observability.QueryFailureFacts{Stage: "provider", Category: "source_backend", Code: "QUERY_UNAVAILABLE",
					Detail: "response=status_storage_timeout", Request: facts},
				Trace: observability.TraceFields{QueryGroupKey: group, EvaluationTime: slot},
			})
			at.at = at.at.Add(time.Millisecond)
			tracker.Observe(context.Background(), observability.Observation{
				ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionReason: "QUERY_UNAVAILABLE",
				Trace: observability.TraceFields{QueryGroupKey: group, EvaluationTime: slot},
			})
			at.at = at.at.Add(time.Minute)
		}
		snapshots := healthySnapshots()
		snapshots[0].Owned, snapshots[0].Determined = 1, 1
		snapshots[0].OwnedObjects = []string{group}
		service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 1, Known: true, IDs: []string{group}}},
			stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: snapshots})
		service.SetLocalRows(tracker.TrackedRow)
		handler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		lookup := func(string) StrategyLookupFacts {
			return StrategyLookupFacts{Available: true, Found: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
				Plans: plans(planA), Dispositions: []StrategyDisposition{{Scope: "PLAN", Disposition: "ACCEPTED"}}}
		}
		routes := WithStrategyStanding(handler, service, lookup, nil, nil, nil, "pod-a", func() time.Time { return now }, 0)
		bodies := map[string]string{}
		for _, path := range []string{"/api/strategies/410", "/api/strategies"} {
			recorder := httptest.NewRecorder()
			routes.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			raw, _ := io.ReadAll(recorder.Result().Body)
			if recorder.Code != http.StatusOK {
				t.Fatalf("%s: status %d: %s", path, recorder.Code, raw)
			}
			bodies[path] = string(raw)
		}
		return bodies, tracker
	}

	with, tracker := serve(&observability.QueryRequestFacts{DeadlineUnixMilli: 1_700_124_050_000, DeadlineLeftAtDoMs: 45_000,
		DeadlineSource: "attempt", Expired: "none", TraceID: traceID, AnswerStatus: 200, AnsweredUnixMilli: 1_700_124_005_000,
		CompletedUnixMilli: 1_700_124_005_100, TimeoutSource: observability.QueryTimeoutSourceBodyReadClass, Excerpt: excerpt})
	without, _ := serve(nil)
	// The live row names the failure, so the route is one that would show
	// anything kept with it.
	if !strings.Contains(with["/api/strategies/410"], `"code":"QUERY_UNAVAILABLE"`) {
		t.Fatalf("the live row does not name the failure: %s", with["/api/strategies/410"])
	}
	for path, body := range with {
		if body != without[path] {
			t.Fatalf("%s answers differently with the request facts:\nwith    %s\nwithout %s", path, body, without[path])
		}
		for _, word := range []string{"payments-7f9c", traceID, `"excerpt"`, "provider_http_status", "timeout_source", "answered_at", "completed_at"} {
			if strings.Contains(body, word) {
				t.Fatalf("%s answers %q: %s", path, word, body)
			}
		}
	}
	kept, ok := tracker.LocalRejection(group)
	if !ok || kept.Excerpt != excerpt || kept.TraceID != traceID || kept.HTTPStatus != 200 || kept.Class != "QUERY_UNAVAILABLE" {
		t.Fatalf("kept %+v (%v), want the provider's message kept on this replica", kept, ok)
	}
}
