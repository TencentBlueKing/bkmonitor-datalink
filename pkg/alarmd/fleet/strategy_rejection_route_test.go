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

// The strategy routes are public: they answer without a session. A group
// this replica keeps a refusal for is answered there with the refusal's
// class and trace id on its live row, never with the words of the
// provider's answer, which can echo label values; those stay on the replica
// for the session-gated strategy read.
func TestTheStrategyRoutesNeverCarryAKeptRejectionsWords(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	const labelValue, token = "payments-7f9c", "Bearer abc123.def456"
	excerpt := `{"error":"cannot select more than 10000000 samples for pod=\"` + labelValue + `\"; authorization: ` + token + `"}`
	for round := 0; round < DefaultDegradedRounds; round++ {
		slot := int64(100 + 60*round)
		tracker.Observe(context.Background(), observability.Observation{
			QueryFailure: &observability.QueryFailureFacts{Stage: "provider", Category: "source_backend", Code: "QUERY_REJECTED",
				Detail: "http_status=400", HTTPStatus: 400,
				Rejection: &observability.QueryRejection{Class: "VM_SAMPLE_LIMIT", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", Excerpt: excerpt}},
			Trace: observability.TraceFields{QueryGroupKey: "qg-4101-a", EvaluationTime: slot},
		})
		at.at = at.at.Add(time.Millisecond)
		tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionReason: "QUERY_REJECTED",
			Trace: observability.TraceFields{QueryGroupKey: "qg-4101-a", EvaluationTime: slot},
		})
		at.at = at.at.Add(time.Minute)
	}
	if kept, ok := tracker.LocalRejection("qg-4101-a"); !ok || kept.Excerpt != excerpt {
		t.Fatalf("kept %+v (%v), want the excerpt kept on this replica", kept, ok)
	}

	snapshots := healthySnapshots()
	snapshots[0].Owned, snapshots[0].Determined = 1, 1
	snapshots[0].OwnedObjects = []string{"qg-4101-a"}
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 1, Known: true, IDs: []string{"qg-4101-a"}}},
		stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: snapshots})
	service.SetLocalRows(tracker.TrackedRow)
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(id string) StrategyLookupFacts {
		return StrategyLookupFacts{Available: true, Found: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
			Plans: plans(planA), Dispositions: []StrategyDisposition{{Scope: "PLAN", Disposition: "ACCEPTED"}}}
	}
	routes := WithStrategyStanding(handler, service, lookup, nil, nil, nil, "pod-a", func() time.Time { return now }, 0)

	for _, path := range []string{"/api/strategies/4101", "/api/strategies"} {
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		raw, _ := io.ReadAll(recorder.Result().Body)
		body := string(raw)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", path, recorder.Code, body)
		}
		for _, word := range []string{labelValue, "abc123.def456", `"excerpt"`} {
			if strings.Contains(body, word) {
				t.Fatalf("%s answers %q: %s", path, word, body)
			}
		}
		if path == "/api/strategies/4101" && !strings.Contains(body, `"rejection":{"class":"VM_SAMPLE_LIMIT"`) {
			t.Fatalf("%s does not name the refusal on the live row: %s", path, body)
		}
	}
	if kept, ok := tracker.LocalRejection("qg-4101-a"); !ok || kept.Excerpt != excerpt {
		t.Fatal("serving the routes gave up the kept refusal")
	}
}
