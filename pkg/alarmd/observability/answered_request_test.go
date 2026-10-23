// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func answeredLine(t *testing.T, observation Observation) string {
	t.Helper()
	var output bytes.Buffer
	limiter, err := NewScopedLogLimiter(ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 100, MaxScopes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), observation)
	return output.String()
}

// A request's facts render within their grammar: a timeout source outside
// the closed list and a status outside 100 to 599 are not written, and the
// provider's excerpt never is. A degraded answer's request renders on the
// query line only, and once: a failure that names its own request keeps
// the line to that one.
func TestAnsweredRequestFactsRenderWithinTheirGrammar(t *testing.T) {
	const word = "provider-message-marker"
	request := func(source string, status int) *QueryRequestFacts {
		return &QueryRequestFacts{DeadlineUnixMilli: 1_700_124_050_000, DeadlineLeftAtDoMs: 45_000, DeadlineSource: "attempt",
			Expired: "none", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", AnswerStatus: status, AnsweredUnixMilli: 1_700_124_005_000,
			CompletedUnixMilli: 1_700_124_005_100, TimeoutSource: source, Excerpt: word}
	}
	line := answeredLine(t, Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultDegraded,
		ReasonCode: "QUERY_EMPTY", QueryRequest: request(QueryTimeoutSourceBodyReadClass, 200)})
	for _, key := range []string{`"provider_http_status":200`, `"timeout_source":"body_read_class_timeout"`,
		`"answered_at_ms":1700124005000`, `"completed_at_ms":1700124005100`, `"request_deadline_ms":1700124050000`,
		`"failure_provider_trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"`} {
		if !strings.Contains(line, key) {
			t.Fatalf("line misses %s: %s", key, line)
		}
	}
	if strings.Contains(line, word) {
		t.Fatalf("the excerpt reached the line: %s", line)
	}

	line = answeredLine(t, Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultDegraded,
		ReasonCode: "QUERY_EMPTY", QueryRequest: request("made_up_source", 42)})
	if strings.Contains(line, "timeout_source") || strings.Contains(line, "provider_http_status") {
		t.Fatalf("an unknown source or status was written: %s", line)
	}

	line = answeredLine(t, Observation{Component: ComponentAccess, Stage: StageQueryCompleted, Result: ResultFailed,
		ReasonCode: "QUERY_TIMEOUT", QueryRequest: request("", 200),
		QueryFailure: &QueryFailureFacts{Stage: "execute", Category: "provider_transport", Code: "QUERY_TIMEOUT", Detail: "body=timeout",
			Request: request(QueryTimeoutSourceBodyReadClass, 200)}})
	if strings.Count(line, `"request_deadline_ms"`) != 1 || strings.Count(line, `"provider_http_status"`) != 1 {
		t.Fatalf("the request was written more than once: %s", line)
	}

	line = answeredLine(t, Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultDegraded,
		ReasonCode: "QUERY_EMPTY", QueryRequest: request("", 200)})
	if strings.Contains(line, "provider_http_status") {
		t.Fatalf("a slot line rendered a query's answered request: %s", line)
	}
}
