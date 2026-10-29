// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

var traceParentFormat = regexp.MustCompile(`^00-([0-9a-f]{32})-[0-9a-f]{16}-00$`)

// headerServer answers every request with status and body, and keeps the
// headers each request carried.
func headerServer(t *testing.T, status int, body string) (*Client, *[]http.Header) {
	t.Helper()
	var seen []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		seen = append(seen, request.Header.Clone())
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, "alarmd", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client, &seen
}

// The provider decides feature flags - which storage a query is routed to
// among them - on the query source header, split at ':' into a source and a
// name, so it goes out exactly as configured. What correlates a request
// with the provider's side is a fresh W3C trace context per attempt and a
// header naming the strategies, which the provider only logs.
func TestARequestCarriesItsTraceAndStrategiesAndAnUnchangedSource(t *testing.T) {
	client, seen := headerServer(t, http.StatusOK, `{"series":[],"status":null,"trace_id":"t","is_partial":false}`)
	attempt := apmAttempt(t)
	attempt.Strategies = []string{"101", "202"}
	for range 2 {
		if _, err := client.Execute(context.Background(), attempt, &collectingSink{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(*seen) != 2 {
		t.Fatalf("%d requests", len(*seen))
	}
	traces := map[string]bool{}
	for _, header := range *seen {
		if got := header.Values(headerQuerySource); len(got) != 1 || got[0] != "alarmd" {
			t.Fatalf("query source %q, want exactly the configured source", got)
		}
		match := traceParentFormat.FindStringSubmatch(header.Get(headerTraceParent))
		if match == nil {
			t.Fatalf("traceparent %q is not an unsampled W3C trace context", header.Get(headerTraceParent))
		}
		traces[match[1]] = true
		if got := header.Get(headerStrategy); got != "101,202" {
			t.Fatalf("strategy header %q", got)
		}
	}
	if len(traces) != 2 {
		t.Fatal("two attempts shared one trace")
	}
}

// A query serving many strategies names as many as fit and how many more.
func TestTheStrategyHeaderIsBounded(t *testing.T) {
	ids := make([]string, 200)
	for index := range ids {
		ids[index] = strconv.Itoa(100000 + index)
	}
	header := strategyHeader(ids)
	if len(header) > maxStrategyHeaderBytes || !strings.HasPrefix(header, "100000,100001,") || !regexp.MustCompile(`,\+\d+$`).MatchString(header) {
		t.Fatalf("header %q (%d bytes)", header, len(header))
	}
	named := strings.Count(header, ",")
	more, _ := strconv.Atoi(header[strings.LastIndex(header, "+")+1:])
	if named+more != len(ids) {
		t.Fatalf("header names %d and counts %d more of %d", named, more, len(ids))
	}
	if strategyHeader(nil) != "" || strategyHeader([]string{"7"}) != "7" {
		t.Fatal("small lists are named whole")
	}
}

const rejectionTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

// A client error is the provider reading the query and refusing it as
// written: QUERY_REJECTED, with the class its body names, its trace id and
// the start of its body - every credential value in it replaced. 408 and
// 429 are the provider asking for time or less load, and 5xx is it failing:
// all three stay QUERY_UNAVAILABLE.
func TestAClientErrorIsTheQueryRefusedAsWritten(t *testing.T) {
	body := `{"error":"vm query failed: cannot select more than 10000000 samples; reduce the time range; ` +
		`condition password=Hunter9Secret header Authorization: Bearer abc123.def456","trace_id":"` + rejectionTraceID + `"}`
	client, _ := headerServer(t, http.StatusBadRequest, body)
	completion, err := client.Execute(context.Background(), apmAttempt(t), &collectingSink{})
	if err != nil {
		t.Fatal(err)
	}
	attempt := completion.RouteFacts.Attempts[0]
	if completion.Completeness != execution.CompletenessUnavailable || attempt.ReasonCode != execution.ReasonCode(contract.ReasonQueryRejected) ||
		attempt.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("completion %+v", attempt)
	}
	rejection := attempt.Rejection
	if rejection == nil || rejection.Class != "VM_SAMPLE_LIMIT" || rejection.TraceID != rejectionTraceID {
		t.Fatalf("rejection %+v", rejection)
	}
	for _, secret := range []string{"Hunter9Secret", "abc123.def456"} {
		if strings.Contains(rejection.Excerpt, secret) {
			t.Fatalf("excerpt %q keeps %q", rejection.Excerpt, secret)
		}
	}
	if !strings.Contains(rejection.Excerpt, "reduce the time range") {
		t.Fatalf("excerpt %q lost the provider's reason", rejection.Excerpt)
	}

	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusInternalServerError} {
		client, _ := headerServer(t, status, `{"error":"busy"}`)
		completion, err := client.Execute(context.Background(), apmAttempt(t), &collectingSink{})
		if err != nil {
			t.Fatal(err)
		}
		if got := completion.RouteFacts.Attempts[0].ReasonCode; got != execution.ReasonCode(contract.ReasonQueryUnavailable) {
			t.Fatalf("status %d filed under %s", status, got)
		}
	}
}

// A provider that says by its status it ran out of time files a timeout,
// named as the provider's, whatever its body says; a body that reads like a
// timeout under any other status does not make one.
func TestAProviderTimeoutStatusIsAQueryTimeout(t *testing.T) {
	for _, status := range []int{http.StatusGatewayTimeout, http.StatusRequestTimeout} {
		client, _ := headerServer(t, status, `{"error":"invalid query","trace_id":"`+rejectionTraceID+`"}`)
		completion, err := client.Execute(context.Background(), apmAttempt(t), &collectingSink{})
		if err != nil {
			t.Fatal(err)
		}
		attempt := completion.RouteFacts.Attempts[0]
		if attempt.ReasonCode != execution.ReasonCode(contract.ReasonQueryTimeout) || attempt.HTTPStatus != status {
			t.Fatalf("status %d: attempt %+v", status, attempt)
		}
		if attempt.Request == nil || attempt.Request.TimeoutSource != observability.QueryTimeoutSourceProviderStatus ||
			attempt.Request.AnswerStatus != status {
			t.Fatalf("status %d: request %+v", status, attempt.Request)
		}
		if attempt.Rejection == nil || attempt.Rejection.TraceID != rejectionTraceID {
			t.Fatalf("status %d: rejection %+v", status, attempt.Rejection)
		}
	}

	client, _ := headerServer(t, http.StatusBadRequest, `{"error":"Post http://backend.example.test/: context deadline exceeded"}`)
	completion, err := client.Execute(context.Background(), apmAttempt(t), &collectingSink{})
	if err != nil {
		t.Fatal(err)
	}
	attempt := completion.RouteFacts.Attempts[0]
	if attempt.ReasonCode != execution.ReasonCode(contract.ReasonQueryRejected) || attempt.Request == nil || attempt.Request.TimeoutSource != "" {
		t.Fatalf("a 400 whose body reads like a timeout: attempt %+v request %+v", attempt, attempt.Request)
	}
}

// A refusal whose body names no trace id is still found on the provider's
// side by the trace this attempt sent, which the provider takes as its own.
func TestARefusalWithoutATraceIDKeepsTheOneSent(t *testing.T) {
	client, seen := headerServer(t, http.StatusBadRequest, `{"error":"1:156: parse error: unterminated quoted string"}`)
	completion, err := client.Execute(context.Background(), apmAttempt(t), &collectingSink{})
	if err != nil {
		t.Fatal(err)
	}
	sent := traceParentFormat.FindStringSubmatch((*seen)[0].Get(headerTraceParent))
	rejection := completion.RouteFacts.Attempts[0].Rejection
	if sent == nil || rejection == nil || rejection.TraceID != sent[1] || rejection.Class != "PROMQL_PARSE" {
		t.Fatalf("rejection %+v, sent %v", rejection, sent)
	}
}

// The header is digits, commas and "+N" by construction: an id that is not
// a canonical decimal - one carrying a line break, a space or letters - is
// not named, only counted, and the request goes out all the same.
func TestTheStrategyHeaderNamesOnlyDecimalIDs(t *testing.T) {
	ids := []string{"101", "1\r\nX-Injected: yes", "2 3", "abc", "007", "202"}
	if got := strategyHeader(ids); got != "101,202,+4" {
		t.Fatalf("header %q", got)
	}
	if got := strategyHeader([]string{"x\n"}); got != "+1" {
		t.Fatalf("header %q for no decimal id", got)
	}
	client, seen := headerServer(t, http.StatusOK, `{"series":[],"status":null,"trace_id":"t","is_partial":false}`)
	attempt := apmAttempt(t)
	attempt.Strategies = ids
	if _, err := client.Execute(context.Background(), attempt, &collectingSink{}); err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 1 || (*seen)[0].Get(headerStrategy) != "101,202,+4" || (*seen)[0].Get("X-Injected") != "" {
		t.Fatalf("headers sent %v", *seen)
	}
}
