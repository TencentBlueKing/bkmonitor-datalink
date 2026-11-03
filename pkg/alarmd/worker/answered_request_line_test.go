// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	uq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// lineReadTimeout is a body read failing with an error that reads as a
// timeout while the request's deadline is still ahead.
type lineReadTimeout struct{}

func (lineReadTimeout) Error() string   { return "read tcp: i/o timeout" }
func (lineReadTimeout) Timeout() bool   { return true }
func (lineReadTimeout) Temporary() bool { return true }

type lineFailingBody struct{ sent bool }

func (body *lineFailingBody) Read(buffer []byte) (int, error) {
	if !body.sent {
		body.sent = true
		return copy(buffer, `{"series":[`), nil
	}
	return 0, lineReadTimeout{}
}

func (*lineFailingBody) Close() error { return nil }

type lineRoundTripper func(*http.Request) (*http.Response, error)

func (round lineRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return round(request)
}

const lineExcerptWord = "bkbase-query-sync-timeout-marker"

// roundDeadlineError stands for access's timeout at the query's own
// deadline: it names the branch, and the error it wraps names the request.
type roundDeadlineError struct{ err error }

func (e roundDeadlineError) Error() string { return e.err.Error() }
func (e roundDeadlineError) Unwrap() error { return e.err }
func (roundDeadlineError) QueryFailure() (string, string) {
	return observability.QueryFailureCategoryProviderTransport, contract.ReasonQueryTimeout
}
func (roundDeadlineError) QueryFailureTimeoutSource() string {
	return observability.QueryTimeoutSourceRoundDeadline
}

type requestNamingError struct{ request *execution.RequestFacts }

func (requestNamingError) Error() string                                  { return "context deadline exceeded" }
func (e requestNamingError) QueryFailureRequest() *execution.RequestFacts { return e.request }

// A timeout the round filed at the query's own deadline names that branch
// on the request its wrapped error carries.
func TestARoundDeadlineTimeoutNamesItsBranch(t *testing.T) {
	var output bytes.Buffer
	limiter, err := observability.NewScopedLogLimiter(observability.ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 100, MaxScopes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := observability.NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	c := &SlotExecutionCoordinator{ports: Ports{Observer: observability.NewLoggingObserver(observability.New("alarmd", &output), policy)}}
	request := &execution.RequestFacts{DeadlineUnixMilli: 1_700_124_050_000, DeadlineLeftAtDoMs: 100, DeadlineSource: "attempt",
		Expired: "attempt", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736"}
	c.observeQueryFailure(context.Background(), execution.OperationNormal, time.Now(), "execute",
		roundDeadlineError{err: requestNamingError{request: request}})
	if line := renderedQueryLine(t, output.Bytes()); line["timeout_source"] != observability.QueryTimeoutSourceRoundDeadline {
		t.Fatalf("timeout_source = %v, want %s: %s", line["timeout_source"], observability.QueryTimeoutSourceRoundDeadline, output.String())
	}
	if request.TimeoutSource != "" {
		t.Fatal("naming the branch changed the request facts the error carries")
	}
}

// Every degraded or failed query line whose query the provider answered
// names the request it answered, as a transport failure always has: the
// trace id it was sent with, the deadline on it and what was left at the
// send, which deadline that was and which had expired; the status the
// provider answered with, when it answered and when the attempt ended. A
// timeout names the branch that filed it, so a body read that timed out
// with a minute left reads body_read_class_timeout on its own line. The
// provider's message is never on the line.
func TestEveryAnsweredQueryLineNamesTheRequestItAnswered(t *testing.T) {
	for _, tc := range []struct {
		name, reason, timeoutSource string
		status                      int
		body                        func() io.ReadCloser
	}{
		{name: "body read timeout before the deadline", reason: contract.ReasonQueryTimeout, status: http.StatusOK,
			timeoutSource: observability.QueryTimeoutSourceBodyReadClass,
			body:          func() io.ReadCloser { return &lineFailingBody{} }},
		{name: "body status", reason: contract.ReasonQueryUnavailable, status: http.StatusOK,
			body: func() io.ReadCloser {
				return io.NopCloser(strings.NewReader(`{"series":[],"status":{"code":"STORAGE_TIMEOUT","message":"` +
					lineExcerptWord + ` at 192.0.2.30:8080"},"is_partial":false}`))
			}},
		{name: "partial", reason: contract.ReasonQueryPartial, status: http.StatusOK,
			body: func() io.ReadCloser {
				return io.NopCloser(strings.NewReader(`{"series":[],"status":{"code":"QUERY_TS_PARTIAL","message":"` +
					lineExcerptWord + `"},"is_partial":true}`))
			}},
		{name: "empty", reason: contract.ReasonQueryEmpty, status: http.StatusOK,
			body: func() io.ReadCloser { return io.NopCloser(strings.NewReader(`{"series":[],"is_partial":false}`)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent string
			client, err := uq.NewClient("http://uq.example.test", "alarmd", &http.Client{Transport: lineRoundTripper(func(request *http.Request) (*http.Response, error) {
				sent = request.Header.Get("traceparent")
				return &http.Response{StatusCode: tc.status, Body: tc.body(), Request: request,
					Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			limiter, err := observability.NewScopedLogLimiter(observability.ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 100, MaxScopes: 1024})
			if err != nil {
				t.Fatal(err)
			}
			policy, err := observability.NewScopedBoundedLogPolicy(limiter)
			if err != nil {
				t.Fatal(err)
			}
			c := &SlotExecutionCoordinator{ports: Ports{Observer: observability.NewLoggingObserver(observability.New("alarmd", &output), policy)}}

			attempt := traceLineAttempt(t, time.Minute)
			done, err := client.Execute(context.Background(), attempt, slowSink{})
			if err != nil {
				c.observeQueryFailure(context.Background(), execution.OperationNormal, time.Now(), "execute", err)
			} else {
				c.observeQueryCompleted(context.Background(), execution.OperationNormal, time.Now(),
					execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{
						{Completeness: done.Completeness, DataState: done.DataState, RouteFacts: done.RouteFacts}}},
					execution.EvaluationResult{Result: observability.ResultDegraded, ReasonCode: execution.ReasonCode(tc.reason)})
			}
			parts := strings.Split(sent, "-")
			if len(parts) != 4 {
				t.Fatalf("sent traceparent %q", sent)
			}
			line := renderedQueryLine(t, output.Bytes())
			// An empty answer is a whole one: the query succeeded and the
			// evaluation is what it degraded.
			if tc.reason == contract.ReasonQueryEmpty {
				if line["result"] != "success" || line["reason_code"] != string(observability.ReasonNone) || line["evaluation_reason"] != tc.reason {
					t.Fatalf("empty answer: result %v reason %v evaluation_reason %v: %s", line["result"], line["reason_code"],
						line["evaluation_reason"], output.String())
				}
			} else if line["reason_code"] != tc.reason {
				t.Fatalf("reason %v, want %s: %s", line["reason_code"], tc.reason, output.String())
			}
			want := map[string]any{
				"failure_provider_trace_id": parts[1],
				"request_deadline_ms":       float64(attempt.DeadlineUnixMilli),
				"deadline_source":           execution.RequestDeadlineAttempt,
				"expired":                   execution.RequestExpiredNone,
				"provider_http_status":      float64(tc.status),
			}
			for key, value := range want {
				if line[key] != value {
					t.Fatalf("%s = %v, want %v; line %s", key, line[key], value, output.String())
				}
			}
			for _, key := range []string{"deadline_left_at_do_ms", "answered_at_ms", "completed_at_ms"} {
				if value, ok := line[key].(float64); !ok || value <= 0 {
					t.Fatalf("%s = %v, want a positive value; line %s", key, line[key], output.String())
				}
			}
			if got, _ := line["timeout_source"].(string); got != tc.timeoutSource {
				t.Fatalf("timeout_source = %q, want %q; line %s", got, tc.timeoutSource, output.String())
			}
			if strings.Contains(output.String(), lineExcerptWord) || strings.Contains(output.String(), "192.0.2.30") {
				t.Fatalf("the provider's message reached the line: %s", output.String())
			}
		})
	}
}
