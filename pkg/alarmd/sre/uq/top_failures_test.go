// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package uq

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// testResponseBytes is the response bound the page-size tests set, so a
// page past it is cheap to send.
const testResponseBytes = 1 << 20

// failureServer answers raw Span pages from rows, recording each body.
type failureServer struct {
	mu     sync.Mutex
	bodies []map[string]any
	rows   func(from int) ([]map[string]any, int)
}

func (f *failureServer) handler(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.bodies = append(f.bodies, body)
	f.mu.Unlock()
	from, _ := body["from"].(float64)
	rows, total := f.rows(int(from))
	if rows == nil {
		rows = []map[string]any{}
	}
	encoded, _ := json.Marshal(map[string]any{"list": rows, "total": total})
	_, _ = w.Write(encoded)
}

func (f *failureServer) queryString(index int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[index]["query_list"].([]any)[0].(map[string]any)["query_string"].(string)
}

func failureProvider(t *testing.T, server *failureServer, shared bool) *Provider {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(server.handler))
	t.Cleanup(s.Close)
	p, err := NewWithQuerySource(traceFixture(s.URL+"/prefix", shared), nil, "fixture-runtime-source")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

var failureNow = time.Date(2026, 10, 15, 13, 10, 0, 0, time.UTC)

func span(trace, service, route string, httpStatus, code any, startMs, elapsedMs int64, extra map[string]any) map[string]any {
	row := map[string]any{"trace_id": trace, "span_name": route, "resource.service.name": service, "attributes.http.route": route,
		"start_time": startMs * 1000, "elapsed_time": elapsedMs * 1000}
	if httpStatus != nil {
		row["attributes.http.status_code"] = httpStatus
	}
	if code != nil {
		row["attributes.http.response.code"] = code
	}
	for key, value := range extra {
		row[key] = value
	}
	return row
}

// The failure predicate goes to UQ in the native query, so every row read is
// a failure and total counts failures; a business error (HTTP 200 with the
// span marked failed) is counted and grouped by its business code; each group
// carries its count, times, percentiles, at most three samples and one message
// (the exception event's, else the response's); groups are ordered by count,
// then last seen, then key.
func TestFailingServerSpansAreGroupedByRouteStatusAndBusinessCode(t *testing.T) {
	base := failureNow.Add(-time.Hour).UnixMilli()
	exception := []any{map[string]any{"name": "exception", "attributes": map[string]any{"exception.message": "metric point: field is empty"}}}
	rows := []map[string]any{
		span("t1", "web", "^kmc/metric_point/$", 200, 3600500, base+1000, 100, map[string]any{"events": exception}),
		span("t2", "web", "^kmc/metric_point/$", 200, 3600500, base+2000, 300, nil),
		span("t3", "web", "^kmc/metric_point/$", 200, 3600500, base+3000, 200, nil),
		span("t4", "web", "^kmc/metric_point/$", 200, 3600500, base+4000, 400, nil),
		span("t5", "query", "POST /query/ts", 503, nil, base+5000, 50, map[string]any{"attributes.http.response.message": "upstream unavailable"}),
		span("t6", "query", "POST /query/ts", "503", nil, base+6000, 70, nil),
		span("t7", "web", "^klc/collect/$", 200, "6806102", base+7000, 10, map[string]any{"attributes.http.response.message": "subscription not found"}),
	}
	server := &failureServer{rows: func(int) ([]map[string]any, int) { return rows, len(rows) }}
	p := failureProvider(t, server, false)
	answer, err := p.TopFailures(context.Background(), TopFailuresRequest{Egress: "primary", Target: "fixture", Window: "1h", Now: failureNow})
	if err != nil {
		t.Fatal(err)
	}
	if query := server.queryString(0); query != "kind:2 AND (status.code:2 OR attributes.http.status_code:>=500 OR attributes.http.response.status_code:>=500)" {
		t.Fatalf("query_string = %s, want the failure predicate", query)
	}
	if answer.Scanned != 7 || answer.Total != 7 || answer.Sampled || answer.TotalAtLeast || len(answer.Groups) != 3 {
		t.Fatalf("answer = %+v", answer)
	}
	first := answer.Groups[0]
	if first.Service != "web" || first.Route != "^kmc/metric_point/$" || first.HTTPStatus != "200" || first.BusinessCode != "3600500" || first.Count != 4 ||
		len(first.SampleTraceIDs) != 3 || first.Message != "metric point: field is empty" || first.P50Ms != 200 || first.P95Ms != 400 ||
		!first.FirstSeen.Equal(time.UnixMilli(base+1000).UTC()) || !first.LastSeen.Equal(time.UnixMilli(base+4000).UTC()) {
		t.Fatalf("first group = %+v", first)
	}
	if second := answer.Groups[1]; second.Route != "POST /query/ts" || second.HTTPStatus != "503" || second.Count != 2 || second.Message != "upstream unavailable" {
		t.Fatalf("second group = %+v, want the two 503s together whatever their number's spelling", second)
	}
	if third := answer.Groups[2]; third.BusinessCode != "6806102" || third.Message != "subscription not found" {
		t.Fatalf("third group = %+v", third)
	}
}

// Ties are ordered by last seen, then by key, so the answer is the same each
// read.
func TestGroupsOfEqualCountAreOrderedByLastSeenThenKey(t *testing.T) {
	base := failureNow.Add(-time.Hour).UnixMilli()
	rows := []map[string]any{
		span("a", "web", "/b", 500, nil, base+1000, 1, nil),
		span("b", "web", "/a", 500, nil, base+1000, 1, nil),
		span("c", "web", "/c", 500, nil, base+9000, 1, nil),
	}
	p := failureProvider(t, &failureServer{rows: func(int) ([]map[string]any, int) { return rows, len(rows) }}, false)
	answer, err := p.TopFailures(context.Background(), TopFailuresRequest{Egress: "primary", Target: "fixture", Now: failureNow})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, group := range answer.Groups {
		order = append(order, group.Route)
	}
	if strings.Join(order, ",") != "/c,/a,/b" {
		t.Fatalf("order = %v, want last seen first, then key", order)
	}
}

// 4xx counts only when asked; a service narrows the native query.
func TestTheFailurePredicateFollowsTheRequest(t *testing.T) {
	server := &failureServer{rows: func(int) ([]map[string]any, int) { return nil, 0 }}
	p := failureProvider(t, server, false)
	if _, err := p.TopFailures(context.Background(), TopFailuresRequest{Egress: "primary", Target: "fixture", Window: "6h", Service: "web", Include4xx: true, Now: failureNow}); err != nil {
		t.Fatal(err)
	}
	want := `kind:2 AND (status.code:2 OR attributes.http.status_code:>=400 OR attributes.http.response.status_code:>=400) AND resource.service.name:"web"`
	if got := server.queryString(0); got != want {
		t.Fatalf("query_string = %s, want %s", got, want)
	}
	server.mu.Lock()
	start, end := server.bodies[0]["start_time"], server.bodies[0]["end_time"]
	server.mu.Unlock()
	if start != fmt.Sprint(failureNow.Add(-6*time.Hour).Unix()) || end != fmt.Sprint(failureNow.Unix()) {
		t.Fatalf("window %v..%v, want the six hours before now", start, end)
	}
}

// At most ten pages are read; when more failures may exist the answer says
// it is a sample, and a total at the backend's cap is a lower bound.
func TestALargeFailureSetIsSampledAndSaysSo(t *testing.T) {
	base := failureNow.Add(-time.Hour).UnixMilli()
	server := &failureServer{rows: func(from int) ([]map[string]any, int) {
		page := make([]map[string]any, 0, MaxTraceRows)
		for i := 0; i < MaxTraceRows; i++ {
			page = append(page, span(fmt.Sprintf("t%d", from+i), "web", "/busy", 500, nil, base+int64(from+i), 1, nil))
		}
		return page, 10000
	}}
	p := failureProvider(t, server, false)
	answer, err := p.TopFailures(context.Background(), TopFailuresRequest{Egress: "primary", Target: "fixture", Now: failureNow})
	if err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	pages := len(server.bodies)
	server.mu.Unlock()
	if pages != TopFailuresPages || answer.Scanned != TopFailuresPages*MaxTraceRows || !answer.Sampled || !answer.TotalAtLeast || answer.Total != 10000 {
		t.Fatalf("pages %d answer %+v, want ten pages, sampled, total at least 10000", pages, answer)
	}
}

// A shared table is read with the application's own business and name.
func TestASharedTargetIsReadWithinItsApplication(t *testing.T) {
	server := &failureServer{rows: func(int) ([]map[string]any, int) { return nil, 0 }}
	p := failureProvider(t, server, true)
	if _, err := p.TopFailures(context.Background(), TopFailuresRequest{Egress: "primary", Target: "fixture", Now: failureNow}); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	conditions, _ := json.Marshal(server.bodies[0]["query_list"].([]any)[0].(map[string]any)["conditions"])
	server.mu.Unlock()
	if !strings.Contains(string(conditions), `"bk_biz_id"`) || !strings.Contains(string(conditions), `"app-fixture"`) {
		t.Fatalf("conditions = %s, want the application's scope", conditions)
	}
}

// The window and its end are checked before any call: a window outside the
// list, an end not in UTC or without a zone, an end in the future, or a
// window whose start is past the trace retention.
func TestAnUnreadableWindowIsRefusedBeforeAnyCall(t *testing.T) {
	server := &failureServer{rows: func(int) ([]map[string]any, int) { return nil, 0 }}
	p := failureProvider(t, server, false)
	for name, request := range map[string]TopFailuresRequest{
		"window past retention": {Window: "8d"},
		"window under a minute": {Window: "30s"},
		"window not a duration": {Window: "a week"},
		"local time":            {End: "2026-10-15T21:10:00+08:00"},
		"no zone":               {End: "2026-10-15T13:10:00"},
		"future":                {End: "2026-10-15T14:10:00Z"},
		"end past retention":    {End: "2026-10-08T13:00:00Z"},
		"start past retention":  {End: "2026-10-08T14:00:00Z", Window: "6h"},
		"not a time":            {End: "yesterday"},
	} {
		request.Egress, request.Target, request.Now = "primary", "fixture", failureNow
		if _, err := p.TopFailures(context.Background(), request); err == nil || !strings.Contains(err.Error(), "invalid_input") {
			t.Errorf("%s: err %v, want invalid_input", name, err)
		}
	}
	if _, err := p.TopFailures(context.Background(), TopFailuresRequest{Egress: "primary", Target: "fixture", End: "2026-10-08T19:30:00Z", Window: "6h", Now: failureNow}); err != nil {
		t.Errorf("a window just inside retention was refused: %v", err)
	}
	server.mu.Lock()
	calls := len(server.bodies)
	server.mu.Unlock()
	if calls != 1 {
		t.Fatalf("calls = %d, want only the valid window sent", calls)
	}
}

// Any window from a minute to the retention is read: minutes, hours and
// whole days, the start that far before the end. Before, only 1h and 6h
// were.
func TestAnyWindowUpToTheRetentionIsRead(t *testing.T) {
	server := &failureServer{rows: func(int) ([]map[string]any, int) { return nil, 0 }}
	p := failureProvider(t, server, false)
	for window, length := range map[string]time.Duration{"90m": 90 * time.Minute, "24h": 24 * time.Hour, "3d": 72 * time.Hour, "7d": TraceRetention} {
		answer, err := p.TopFailures(context.Background(), TopFailuresRequest{Egress: "primary", Target: "fixture", Window: window, Now: failureNow})
		if err != nil || !answer.End.Equal(failureNow) || !answer.Start.Equal(failureNow.Add(-length)) {
			t.Errorf("window %s: start %v end %v err %v, want %v before %v", window, answer.Start, answer.End, err, length, failureNow)
		}
	}
}

// Every page asks for the newest spans first, so pages neither overlap nor
// skip and a sampled answer is the most recent failing spans.
func TestEveryPageReadsTheNewestFirst(t *testing.T) {
	server := &failureServer{rows: func(int) ([]map[string]any, int) { return nil, 0 }}
	p := failureProvider(t, server, false)
	if _, err := p.TopFailures(context.Background(), TopFailuresRequest{Egress: "primary", Target: "fixture", Now: failureNow}); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	for i, body := range server.bodies {
		if order, _ := json.Marshal(body["order_by"]); string(order) != `["-start_time"]` {
			t.Fatalf("page %d order_by = %s, want newest first", i, order)
		}
	}
}

// A page too large for the response budget (spans carrying stack traces) is
// read again at half the rows, down to a floor, so a noisy failing service
// is answered rather than refused; the rows scanned and the requests made
// stay bounded, and the answer says the pages were shrunk.
func TestAPageTooLargeIsReadInSmallerPages(t *testing.T) {
	base := failureNow.Add(-time.Hour).UnixMilli()
	var requests int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests++
		limit, _ := body["limit"].(float64)
		from, _ := body["from"].(float64)
		if limit > 250 {
			_, _ = w.Write([]byte(strings.Repeat(" ", testResponseBytes+16)))
			return
		}
		rows := []map[string]any{}
		for i := 0; i < int(limit) && int(from)+i < 600; i++ {
			rows = append(rows, span(fmt.Sprintf("t%d", int(from)+i), "web", "/noisy", 500, nil, base+int64(from)+int64(i), 1, nil))
		}
		encoded, _ := json.Marshal(map[string]any{"list": rows, "total": 600})
		_, _ = w.Write(encoded)
	}))
	defer s.Close()
	p, err := NewWithQuerySource(traceFixture(s.URL+"/prefix", false), nil, "fixture-runtime-source")
	if err != nil {
		t.Fatal(err)
	}
	p.SetResponseBytes(testResponseBytes)
	defer p.Close()
	answer, err := p.TopFailures(context.Background(), TopFailuresRequest{Egress: "primary", Target: "fixture", Now: failureNow})
	if err != nil {
		t.Fatalf("a noisy service was refused: %v", err)
	}
	shrunk := false
	for _, limitation := range answer.Limitations {
		shrunk = shrunk || strings.Contains(limitation, "pages were shrunk")
	}
	if answer.Scanned != 600 || answer.Sampled || len(answer.Groups) != 1 || answer.Groups[0].Count != 600 || !shrunk || requests > TopFailuresRequests {
		t.Fatalf("answer %+v after %d requests", answer, requests)
	}
}

// When even the smallest page is too large the read stops, says so, and is
// a sample rather than a complete answer.
func TestAReadThatStopsShortIsASample(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", testResponseBytes+16)))
	}))
	defer s.Close()
	p, err := NewWithQuerySource(traceFixture(s.URL+"/prefix", false), nil, "fixture-runtime-source")
	if err != nil {
		t.Fatal(err)
	}
	p.SetResponseBytes(testResponseBytes)
	defer p.Close()
	answer, err := p.TopFailures(context.Background(), TopFailuresRequest{Egress: "primary", Target: "fixture", Now: failureNow})
	stopped := false
	for _, limitation := range answer.Limitations {
		stopped = stopped || strings.Contains(limitation, "the read stopped there")
	}
	if err != nil || !answer.Sampled || !stopped || answer.Pages > TopFailuresRequests {
		t.Fatalf("answer %+v err %v, want a stopped sample", answer, err)
	}
}

// Pages shrunk to the floor still stop at the request cap: the read is
// bounded in calls as well as rows, and what it scanned is a sample.
func TestShrunkPagesStopAtTheRequestCap(t *testing.T) {
	base := failureNow.Add(-time.Hour).UnixMilli()
	var requests int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests++
		limit, _ := body["limit"].(float64)
		from, _ := body["from"].(float64)
		if limit > 100 {
			_, _ = w.Write([]byte(strings.Repeat(" ", testResponseBytes+16)))
			return
		}
		rows := []map[string]any{}
		for i := 0; i < int(limit); i++ {
			rows = append(rows, span(fmt.Sprintf("t%d", int(from)+i), "web", "/noisy", 500, nil, base+int64(from)+int64(i), 1, nil))
		}
		encoded, _ := json.Marshal(map[string]any{"list": rows, "total": 5000})
		_, _ = w.Write(encoded)
	}))
	defer s.Close()
	p, err := NewWithQuerySource(traceFixture(s.URL+"/prefix", false), nil, "fixture-runtime-source")
	if err != nil {
		t.Fatal(err)
	}
	p.SetResponseBytes(testResponseBytes)
	defer p.Close()
	answer, err := p.TopFailures(context.Background(), TopFailuresRequest{Egress: "primary", Target: "fixture", Now: failureNow})
	if err != nil || requests != TopFailuresRequests || answer.Pages != TopFailuresRequests || !answer.Sampled || answer.Scanned >= 5000 {
		t.Fatalf("answer %+v after %d requests err %v, want the cap", answer, requests, err)
	}
}

// A read whose time runs out answers what it scanned: it stops between
// pages when the next would not fit (twice the slowest page so far), the
// groups of the scanned pages stand, and the answer is a sample saying why.
func TestATopFailuresReadOutOfTimeAnswersWhatItScanned(t *testing.T) {
	base := failureNow.Add(-time.Hour).UnixMilli()
	server := &failureServer{rows: func(from int) ([]map[string]any, int) {
		time.Sleep(150 * time.Millisecond) // fixed-wait: the server's per-page answer time, which the case sets
		page := make([]map[string]any, 0, MaxTraceRows)
		for i := 0; i < MaxTraceRows; i++ {
			page = append(page, span(fmt.Sprintf("t%d", from+i), "web", "/busy", 500, nil, base+int64(from+i), 1, nil))
		}
		return page, 10000
	}}
	p := failureProvider(t, server, false)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond) // fixed-wait: the read's deadline the case runs out
	defer cancel()
	answer, err := p.TopFailures(ctx, TopFailuresRequest{Egress: "primary", Target: "fixture", Now: failureNow})
	if err != nil {
		t.Fatalf("a read out of time failed instead of answering what it scanned: %v", err)
	}
	if answer.Pages == 0 || answer.Pages >= TopFailuresPages || answer.Scanned == 0 || len(answer.Groups) == 0 || !answer.Sampled {
		t.Fatalf("answer pages %d scanned %d groups %d sampled %v", answer.Pages, answer.Scanned, len(answer.Groups), answer.Sampled)
	}
	found := false
	for _, limitation := range answer.Limitations {
		found = found || strings.Contains(limitation, "time ran out")
	}
	if !found {
		t.Fatalf("limitations %v, want the reason named", answer.Limitations)
	}
}

// A page the operation's deadline cuts -- one far slower than the pages
// before it, which the pre-check could not foresee -- ends the read with
// what was scanned, rather than failing it.
func TestATopFailuresPageCutByTheDeadlineKeepsThePagesBefore(t *testing.T) {
	base := failureNow.Add(-time.Hour).UnixMilli()
	server := &failureServer{rows: func(from int) ([]map[string]any, int) {
		if from > 0 {
			time.Sleep(2 * time.Second) // fixed-wait: the slow page's answer time past the read's 500 ms, which the case sets
		}
		page := make([]map[string]any, 0, MaxTraceRows)
		for i := 0; i < MaxTraceRows; i++ {
			page = append(page, span(fmt.Sprintf("t%d", from+i), "web", "/busy", 500, nil, base+int64(from+i), 1, nil))
		}
		return page, 10000
	}}
	p := failureProvider(t, server, false)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond) // fixed-wait: the read's deadline the case runs out
	defer cancel()
	answer, err := p.TopFailures(ctx, TopFailuresRequest{Egress: "primary", Target: "fixture", Now: failureNow})
	if err != nil || answer.Scanned != MaxTraceRows || len(answer.Groups) == 0 || !answer.Sampled {
		t.Fatalf("err %v scanned %d groups %d sampled %v, want the first page answered", err, answer.Scanned, len(answer.Groups), answer.Sampled)
	}
}
