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

var logNow = time.Date(2026, 10, 16, 6, 0, 0, 0, time.UTC)

// logServer answers /query/ts with series, recording each body.
type logServer struct {
	mu     sync.Mutex
	bodies []map[string]any
	answer string
}

func (l *logServer) provider(t *testing.T) *Provider {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		l.mu.Lock()
		l.bodies = append(l.bodies, body)
		l.mu.Unlock()
		_, _ = w.Write([]byte(l.answer))
	}))
	t.Cleanup(s.Close)
	p, err := NewWithQuerySource(fixture(s.URL), nil, "alarmd-sre")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func logRequest() LogCountRequest {
	return LogCountRequest{Egress: "primary", Tenant: "tenant-fixture", Space: "scope-fixture", TableID: "2_bklog.fixture",
		By: []string{"__ext.io_kubernetes_pod"}, Window: "1h", Now: logNow}
}

// series is one group's answer to a one-hour count ending at logNow: a point
// per whole bucket from the window's start, as UQ answers - the first is the
// window's, any after it the next window's.
func series(keys []string, values []string, counts ...float64) map[string]any {
	points := []any{}
	for i, count := range counts {
		points = append(points, []any{logNow.Add(-time.Hour).Add(time.Duration(i) * time.Hour).UnixMilli(), count})
	}
	return map[string]any{"name": "_result0", "group_keys": keys, "group_values": values, "values": points}
}

func answer(partial bool, items ...map[string]any) string {
	encoded, _ := json.Marshal(map[string]any{"series": items, "is_partial": partial})
	return string(encoded)
}

// One aggregate query counts the lines by the fields asked: count_over_time
// over the whole window, summed by those fields, the filter pushed down as
// given. The groups come back by count, then by value, with their total.
func TestLogCountCountsLinesByTheFieldsAsked(t *testing.T) {
	server := &logServer{answer: answer(false,
		series([]string{"__ext.io_kubernetes_pod"}, []string{"web-1"}, 10, 5),
		series([]string{"__ext.io_kubernetes_pod"}, []string{"web-0"}, 30),
		series([]string{"__ext.io_kubernetes_pod"}, []string{"web-2"}, 15))}
	p := server.provider(t)
	request := logRequest()
	request.QueryString = "log:*timeout*"
	counts, err := p.LogCount(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Total != 55 || counts.GroupsTotal != 3 || counts.Truncated || counts.Partial || counts.QueryStringBytes != len("log:*timeout*") {
		t.Fatalf("counts %+v", counts)
	}
	var order []string
	for _, group := range counts.Groups {
		order = append(order, fmt.Sprintf("%s=%d", group.Values["__ext.io_kubernetes_pod"], group.Count))
	}
	if fmt.Sprint(order) != "[web-0=30 web-2=15 web-1=10]" {
		t.Fatalf("groups %v, want by count then by value", order)
	}
	body := server.bodies[0]
	query := body["query_list"].([]any)[0].(map[string]any)
	aggregation := query["time_aggregation"].(map[string]any)
	function := query["function"].([]any)[0].(map[string]any)
	if body["start_time"] != fmt.Sprint(logNow.Add(-time.Hour).Unix()) || body["end_time"] != fmt.Sprint(logNow.Add(-time.Second).Unix()) || body["step"] != "3600s" ||
		query["data_source"] != "bklog" || query["table_id"] != "2_bklog.fixture" || query["query_string"] != "log:*timeout*" ||
		aggregation["function"] != "count_over_time" || aggregation["window"] != "3600s" ||
		function["method"] != "sum" || fmt.Sprint(function["dimensions"]) != "[__ext.io_kubernetes_pod]" {
		t.Fatalf("body %v", body)
	}
}

// Past top the groups are cut and say so; the total stays every group's.
// A partial answer from UQ is said as such. Field values are cut at 256
// bytes on a character boundary.
func TestLogCountCutsAtTopAndSaysPartial(t *testing.T) {
	long := strings.Repeat("容", 100)
	server := &logServer{answer: answer(true,
		series([]string{"level"}, []string{"error"}, 3),
		series([]string{"level"}, []string{long}, 2),
		series([]string{"level"}, []string{"info"}, 1))}
	p := server.provider(t)
	request := logRequest()
	request.By, request.Top = []string{"level"}, 2
	counts, err := p.LogCount(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Total != 6 || counts.GroupsTotal != 3 || len(counts.Groups) != 2 || !counts.Truncated || !counts.Partial {
		t.Fatalf("counts %+v", counts)
	}
	if value := counts.Groups[1].Values["level"]; len(value) > LogCountValueBytes || strings.ContainsRune(value, '�') || !strings.HasPrefix(long, value) {
		t.Fatalf("long value cut to %d bytes: %q", len(value), value)
	}
}

// Requests that would return log text, or that are out of bounds, are
// refused before any query: a content field to group by, a table that is
// not a log table, no field, a window past 30 days, a top
// past 1000, an end in the future.
func TestLogCountRefusesBeforeAnyQuery(t *testing.T) {
	server := &logServer{answer: answer(false)}
	p := server.provider(t)
	for name, change := range map[string]func(*LogCountRequest){
		"content field":   func(r *LogCountRequest) { r.By = []string{"log"} },
		"message field":   func(r *LogCountRequest) { r.By = []string{"__ext.message"} },
		"not a log table": func(r *LogCountRequest) { r.TableID = "2_bkapm.trace_fixture" },
		"no field":        func(r *LogCountRequest) { r.By = nil },
		"container log":   func(r *LogCountRequest) { r.By = []string{"__ext.container_log"} },
		"otel body":       func(r *LogCountRequest) { r.By = []string{"body"} },
		"nested body":     func(r *LogCountRequest) { r.By = []string{"log.body"} },
		"window":          func(r *LogCountRequest) { r.Window = "31d" },
		"top":             func(r *LogCountRequest) { r.Top = 1001 },
		"future end":      func(r *LogCountRequest) { r.End = "2026-10-16T07:00:00Z" },
		"scope":           func(r *LogCountRequest) { r.Space = "other" },
	} {
		request := logRequest()
		change(&request)
		if _, err := p.LogCount(context.Background(), request); err == nil {
			t.Errorf("%s: answered, want refused", name)
		}
	}
	if len(server.bodies) != 0 {
		t.Fatalf("%d queries sent, want none", len(server.bodies))
	}
	if _, err := p.LogCount(context.Background(), logRequest()); err != nil {
		t.Fatalf("a valid request: %v", err)
	}
}

// Any number of fields can be grouped by.
func TestLogCountGroupsByAnyNumberOfFields(t *testing.T) {
	server := &logServer{answer: answer(false, series([]string{"a", "b", "c", "d"}, []string{"1", "2", "3", "4"}, 7))}
	p := server.provider(t)
	request := logRequest()
	request.By = []string{"a", "b", "c", "d"}
	counts, err := p.LogCount(context.Background(), request)
	if err != nil || counts.Total != 7 || counts.Groups[0].Values["d"] != "4" {
		t.Fatalf("counts %+v err %v", counts, err)
	}
}

// UQ answers a count_over_time range with a point at every step from start
// through its closed end, each the whole window from there: asked up to the
// window's end it also answers the next window. Only the window's own bucket
// is counted, so the count is the window's lines, not twice the window; the
// end asked stops a second short of the next step.
func TestLogCountCountsOnlyTheWindowsOwnBucket(t *testing.T) {
	server := &logServer{answer: answer(false,
		series([]string{"level"}, []string{"error"}, 40, 900),
		series([]string{"level"}, []string{"info"}, 2, 70))}
	p := server.provider(t)
	request := logRequest()
	request.By = []string{"level"}
	counts, err := p.LogCount(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Total != 42 || counts.Groups[0].Count != 40 || counts.Groups[1].Count != 2 {
		t.Fatalf("counts %+v, want only the window's bucket (40 + 2)", counts)
	}
	if !counts.Start.Equal(logNow.Add(-time.Hour)) || !counts.End.Equal(logNow) || len(counts.Limitations) != 0 {
		t.Fatalf("window %s to %s, limitations %v", counts.Start, counts.End, counts.Limitations)
	}
	body := server.bodies[0]
	if body["start_time"] != fmt.Sprint(logNow.Add(-time.Hour).Unix()) || body["end_time"] != fmt.Sprint(logNow.Add(-time.Second).Unix()) || body["step"] != "3600s" {
		t.Fatalf("body %v", body)
	}
}

// A window that does not begin on a bucket is counted from the bucket it
// begins in, as UQ aligns it, and the answer names the window it counted:
// asked 04:40 to 05:40, UQ answers the 04:00 and 05:00 buckets and the
// count is 04:00's.
func TestLogCountNamesTheAlignedWindowItCounted(t *testing.T) {
	bucket := logNow.Add(-2 * time.Hour)
	server := &logServer{answer: answer(false, map[string]any{"name": "_result0", "group_keys": []string{"level"}, "group_values": []string{"error"},
		"values": []any{[]any{bucket.UnixMilli(), 5}, []any{bucket.Add(time.Hour).UnixMilli(), 8}}})}
	p := server.provider(t)
	request := logRequest()
	request.By, request.End = []string{"level"}, logNow.Add(-20*time.Minute).Format(time.RFC3339)
	counts, err := p.LogCount(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Total != 5 || !counts.Start.Equal(bucket) || !counts.End.Equal(bucket.Add(time.Hour)) || len(counts.Limitations) != 1 || !strings.Contains(counts.Limitations[0], "whole 1h buckets") {
		t.Fatalf("counts %+v", counts)
	}
}

// An answer without a single point counts zero, and says it is UQ's empty
// answer rather than a window found to hold no lines.
func TestLogCountSaysWhenUQReturnedNoPoints(t *testing.T) {
	for name, native := range map[string]string{
		"no series":         answer(false),
		"series, no points": answer(false, series([]string{"level"}, []string{"error"})),
	} {
		server := &logServer{answer: native}
		p := server.provider(t)
		request := logRequest()
		request.By = []string{"level"}
		counts, err := p.LogCount(context.Background(), request)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if counts.Total != 0 || !counts.Start.Equal(logNow.Add(-time.Hour)) || !counts.End.Equal(logNow) ||
			len(counts.Limitations) != 1 || counts.Limitations[0] != "UQ returned no points for this window" {
			t.Fatalf("%s: counts %+v", name, counts)
		}
	}
}
