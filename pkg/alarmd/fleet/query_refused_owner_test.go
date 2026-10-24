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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const refusedTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

// failedRounds tracks rounds of one Query Group failing with code at status
// and returns its attributed row.
func failedRounds(t *testing.T, queryGroup, code string, status int, detail string) Anomaly {
	t.Helper()
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		slot := int64(100 + 60*round)
		tracker.Observe(context.Background(), observability.Observation{
			QueryFailure: &observability.QueryFailureFacts{Stage: "provider", Category: "source_backend", Code: code,
				Detail: detail, HTTPStatus: status,
				Rejection: &observability.QueryRejection{Class: "HTTP_400", TraceID: refusedTraceID, Excerpt: `{"error":"refused"}`}},
			Trace: observability.TraceFields{QueryGroupKey: queryGroup, EvaluationTime: slot},
		})
		at.at = at.at.Add(time.Millisecond)
		tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionReason: code,
			Trace: observability.TraceFields{QueryGroupKey: queryGroup, EvaluationTime: slot},
		})
		at.at = at.at.Add(time.Minute)
	}
	row, tracked := tracker.TrackedRow(queryGroup)
	if !tracked {
		t.Fatalf("%s is not tracked after %d failed rounds", queryGroup, DefaultDegradedRounds)
	}
	list := []Anomaly{row}
	Attribute(list, at.at)
	return list[0]
}

// A bare refusal does not say whose it is -- the strategy's input, or a
// backend fault the provider reports the same way -- so it reads as the
// provider not answering does: undetermined, on this service's side, never
// handed to the strategy owner, with the provider's trace id kept on the row
// to read the refusal by.
func TestABareRefusalIsUndeterminedLikeTheProviderNotAnswering(t *testing.T) {
	row := failedRounds(t, "qg-4101-a", "QUERY_REJECTED", 400, "http_status=400")
	if row.Finding.Check != CheckQueryRefused {
		t.Fatalf("check %s, want QUERY_REFUSED", row.Finding.Check)
	}
	if words, provider := checkWords[CheckQueryRefused], checkWords[CheckBackendNotAnswering]; words != provider ||
		words.State != StateDependencyUnanswered || words.Action != ActionServiceFix {
		t.Fatalf("QUERY_REFUSED reads %+v, want BACKEND_NOT_ANSWERING's %+v (DEPENDENCY_UNANSWERED / SERVICE_FIX)", words, provider)
	}
	if owner := checkAnswers[CheckQueryRefused].Owner; owner != OwnerUndetermined || owner != checkAnswers[CheckBackendNotAnswering].Owner {
		t.Fatalf("QUERY_REFUSED is owned by %v, want UNDETERMINED like BACKEND_NOT_ANSWERING", owner)
	}
	if row.Failure == nil || row.Failure.Rejection == nil || row.Failure.Rejection.TraceID != refusedTraceID {
		t.Fatalf("failure %+v does not keep the provider's trace id", row.Failure)
	}
}

// A provider that ran out of time is the provider not answering, not a
// refusal.
func TestAProviderTimeoutIsTheProviderNotAnswering(t *testing.T) {
	row := failedRounds(t, "qg-4101-b", "QUERY_TIMEOUT", 504, "http_status=504")
	if row.Finding.Check != CheckBackendNotAnswering {
		t.Fatalf("check %s, want BACKEND_NOT_ANSWERING", row.Finding.Check)
	}
}
