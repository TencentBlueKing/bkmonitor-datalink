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
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

const answeredSeries = `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip"],"group_values":["192.0.2.10"],"values":[[1700123456789,12.5]]}`

// readTimeout is a read of a body failing with an error that reads as a
// timeout (net.Error.Timeout) while the request's own deadline is ahead.
type readTimeout struct{}

func (readTimeout) Error() string   { return "read tcp 192.0.2.20:51234->192.0.2.10:10205: i/o timeout" }
func (readTimeout) Timeout() bool   { return true }
func (readTimeout) Temporary() bool { return true }

// headThenFail is a body that gives its first bytes and then fails.
type headThenFail struct {
	head string
	err  error
	sent bool
}

func (body *headThenFail) Read(buffer []byte) (int, error) {
	if !body.sent {
		body.sent = true
		return copy(buffer, body.head), nil
	}
	return 0, body.err
}

func (*headThenFail) Close() error { return nil }

// answeringClient answers every request with status and body, and reports
// the trace id each request was sent with.
func answeringClient(t *testing.T, status int, body func() io.ReadCloser, sent *string) *Client {
	t.Helper()
	httpClient := &http.Client{Transport: diagnosticRoundTripper(func(request *http.Request) (*http.Response, error) {
		parts := strings.Split(request.Header.Get(headerTraceParent), "-")
		if len(parts) == 4 {
			*sent = parts[1]
		}
		return &http.Response{StatusCode: status, Body: body(), Request: request,
			Header: http.Header{"Content-Type": []string{"application/json"}}}, nil
	})}
	client, err := NewClient("http://uq.example.test", "alarmd", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// wantAnsweredRequest checks the facts of a request the provider answered:
// the trace id it was sent with, the deadline on it and what was left of it,
// which deadline that was and that neither had run out, the status the
// provider answered with, and when it answered and ended.
func wantAnsweredRequest(t *testing.T, name string, request *execution.RequestFacts, sent string, attempt execution.QueryAttempt) {
	t.Helper()
	if request == nil {
		t.Fatalf("%s: no request facts on an answered attempt", name)
	}
	if sent == "" || request.TraceID != sent {
		t.Fatalf("%s: trace id %q, want the sent %q", name, request.TraceID, sent)
	}
	if request.DeadlineUnixMilli != attempt.DeadlineUnixMilli || request.DeadlineLeftAtDoMs <= 0 ||
		request.DeadlineSource != execution.RequestDeadlineAttempt || request.Expired != execution.RequestExpiredNone {
		t.Fatalf("%s: request %+v, want the attempt's deadline, time left, nothing expired", name, *request)
	}
	if request.AnswerStatus != http.StatusOK {
		t.Fatalf("%s: answer status %d, want 200", name, request.AnswerStatus)
	}
	if request.AnsweredUnixMilli <= 0 || request.CompletedUnixMilli < request.AnsweredUnixMilli {
		t.Fatalf("%s: answered %d completed %d, want the answer's time and the end after it", name,
			request.AnsweredUnixMilli, request.CompletedUnixMilli)
	}
}

// A body whose read fails with a timeout-class error while the deadline is
// still a minute ahead is QUERY_TIMEOUT by ClassifyTransportFailure alone.
// The failure says so: the request it answered, its 200, and the branch
// that filed it - body_read_class_timeout, not the deadline.
func TestABodyReadTimeoutBeforeTheDeadlineNamesItsRequestAndBranch(t *testing.T) {
	var sent string
	client := answeringClient(t, http.StatusOK, func() io.ReadCloser {
		return &headThenFail{head: `{"series":[` + answeredSeries + `,`, err: readTimeout{}}
	}, &sent)
	attempt := validAttempt(t)
	_, err := client.Execute(context.Background(), attempt, &collectingSink{})
	failure := bodyFailureOf(t, err)
	if _, code := failure.QueryFailure(); code != "QUERY_TIMEOUT" || failure.QueryFailureDetail() != "body=timeout" {
		t.Fatalf("failure %s %q, want QUERY_TIMEOUT body=timeout", code, failure.QueryFailureDetail())
	}
	request := failure.QueryFailureRequest()
	wantAnsweredRequest(t, "body read timeout", request, sent, attempt)
	if request.TimeoutSource != observability.QueryTimeoutSourceBodyReadClass {
		t.Fatalf("timeout source %q, want %q", request.TimeoutSource, observability.QueryTimeoutSourceBodyReadClass)
	}
	// The error chain is sanitized as an excerpt is: no address survives.
	if strings.Contains(request.ErrorChain, "192.0.2.") {
		t.Fatalf("error chain %q keeps an address", request.ErrorChain)
	}
}

// A body cut by a connection reset is QUERY_UNAVAILABLE and names no timeout
// source; one whose deadline passed mid-read names deadline_passed.
func TestABodyFailureNamesATimeoutSourceOnlyForATimeout(t *testing.T) {
	var sent string
	client := answeringClient(t, http.StatusOK, func() io.ReadCloser {
		return &headThenFail{head: `{"series":[`, err: errors.New("connection reset by peer")}
	}, &sent)
	_, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
	failure := bodyFailureOf(t, err)
	if request := failure.QueryFailureRequest(); request == nil || request.TimeoutSource != "" || request.AnswerStatus != http.StatusOK {
		t.Fatalf("a cut body's request %+v, want its 200 and no timeout source", request)
	}

	stalled := partialBodyServer(t, false, 0)
	client, err = NewClient(stalled.URL, "alarmd", stalled.Client())
	if err != nil {
		t.Fatal(err)
	}
	attempt := validAttempt(t)
	attempt.DeadlineUnixMilli = time.Now().Add(time.Second).UnixMilli()
	_, err = client.Execute(context.Background(), attempt, &collectingSink{})
	failure = bodyFailureOf(t, err)
	if request := failure.QueryFailureRequest(); request == nil || request.TimeoutSource != observability.QueryTimeoutSourceDeadlinePassed {
		t.Fatalf("a body stalled past its deadline: request %+v, want %q", request, observability.QueryTimeoutSourceDeadlinePassed)
	}
}

// A 200 whose body status says the query failed completes UNAVAILABLE, and
// its attempt names the request it answered and the provider's message,
// sanitized and bounded: no address, no credential, no host, and no longer
// than an excerpt.
func TestABodyStatusFailureNamesItsRequestAndAnExcerpt(t *testing.T) {
	message := "bkbase query_sync timeout at 192.0.2.30:8080 via http://query.example.test/v3/sync?token=abc123 " +
		"authorization: Bearer abc123.def456 " + strings.Repeat("detail ", 200)
	body := `{"series":[],"status":{"code":"STORAGE_TIMEOUT","message":` + quoteJSON(message) + `},"is_partial":false}`
	var sent string
	client := answeringClient(t, http.StatusOK, func() io.ReadCloser { return io.NopCloser(strings.NewReader(body)) }, &sent)
	attempt := validAttempt(t)
	completion, err := client.Execute(context.Background(), attempt, &collectingSink{})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Completeness != execution.CompletenessUnavailable {
		t.Fatalf("completeness %s, want UNAVAILABLE", completion.Completeness)
	}
	request := completion.RouteFacts.Attempts[0].Request
	wantAnsweredRequest(t, "body status", request, sent, attempt)
	excerpt := request.Excerpt
	if !strings.HasPrefix(excerpt, "bkbase query_sync timeout") {
		t.Fatalf("excerpt %q, want the provider's message", excerpt)
	}
	for _, leaked := range []string{"192.0.2.30", "query.example.test", "abc123", "def456"} {
		if strings.Contains(excerpt, leaked) {
			t.Fatalf("excerpt %q keeps %q", excerpt, leaked)
		}
	}
	if len(excerpt) > errorExcerptBytes+len("...") || !strings.HasSuffix(excerpt, "...") {
		t.Fatalf("excerpt of %d bytes, want it cut at %d and marked", len(excerpt), errorExcerptBytes)
	}
}

// A partial answer and an empty one are answers, not failures, but a round
// reads both as degraded: each names the request it answered. A status that
// carried no message leaves no excerpt.
func TestPartialAndEmptyAnswersNameTheirRequest(t *testing.T) {
	for name, body := range map[string]string{
		"partial": `{"series":[` + answeredSeries + `],"is_partial":true}`,
		"empty":   `{"series":[],"is_partial":false}`,
	} {
		var sent string
		client := answeringClient(t, http.StatusOK, func() io.ReadCloser { return io.NopCloser(strings.NewReader(body)) }, &sent)
		attempt := validAttempt(t)
		completion, err := client.Execute(context.Background(), attempt, &collectingSink{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		request := completion.RouteFacts.Attempts[0].Request
		wantAnsweredRequest(t, name, request, sent, attempt)
		if request.Excerpt != "" || request.TimeoutSource != "" {
			t.Fatalf("%s: request %+v, want no excerpt and no timeout source", name, *request)
		}
	}
}

// A full answer with data, no failure and no message carries no request
// facts: a success names nothing new.
func TestAFullSuccessfulAnswerCarriesNoRequestFacts(t *testing.T) {
	var sent string
	body := `{"series":[` + answeredSeries + `],"is_partial":false}`
	client := answeringClient(t, http.StatusOK, func() io.ReadCloser { return io.NopCloser(strings.NewReader(body)) }, &sent)
	completion, err := client.Execute(context.Background(), validAttempt(t), &collectingSink{})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Completeness != execution.CompletenessFull || completion.DataState != execution.DataStateData {
		t.Fatalf("completion %s/%s, want FULL with data", completion.Completeness, completion.DataState)
	}
	for _, attempt := range completion.RouteFacts.Attempts {
		if attempt.Request != nil {
			t.Fatalf("a full success carries request facts %+v", *attempt.Request)
		}
	}
}

// steppingClock reads whole milliseconds, a step later at every read, so
// two reads of it never agree.
type steppingClock struct{ at time.Time }

func (clock *steppingClock) now() time.Time {
	clock.at = clock.at.Add(7 * time.Millisecond)
	return clock.at
}

// A body failure's request ends at the instant bodyFailure timed it: its
// completed time is its send plus its timing's elapsed, to the millisecond.
func TestABodyFailureEndsWhenItsTimingEnds(t *testing.T) {
	var sent string
	client := answeringClient(t, http.StatusOK, func() io.ReadCloser {
		return &headThenFail{head: `{"series":[`, err: readTimeout{}}
	}, &sent)
	clock := &steppingClock{at: time.Now().Truncate(time.Second)}
	client.WithNow(clock.now)
	attempt := validAttempt(t)
	attempt.BudgetStartUnixMilli = clock.at.Add(-time.Second).UnixMilli()
	attempt.ReadyAtUnixMilli = attempt.BudgetStartUnixMilli
	_, err := client.Execute(context.Background(), attempt, &collectingSink{})
	failure := bodyFailureOf(t, err)
	timing, request := failure.QueryFailureTiming(), failure.QueryFailureRequest()
	if timing == nil || request == nil {
		t.Fatalf("timing %+v request %+v, want both", timing, request)
	}
	sentAt := attempt.DeadlineUnixMilli - timing.BudgetMillis
	if request.CompletedUnixMilli != sentAt+timing.ElapsedMillis {
		t.Fatalf("completed %d, want the timing's end %d", request.CompletedUnixMilli, sentAt+timing.ElapsedMillis)
	}
}

func quoteJSON(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"', '\\':
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	out.WriteByte('"')
	return out.String()
}
