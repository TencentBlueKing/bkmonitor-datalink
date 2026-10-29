// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package uq

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/redact"
)

// rawServer answers /query/ts/raw with the next answer in turn, recording
// each body.
type rawServer struct {
	mu      sync.Mutex
	bodies  []map[string]any
	answers []string
}

func (r *rawServer) provider(t *testing.T) *Provider {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/query/ts/raw" {
			t.Errorf("path %s, want /query/ts/raw", req.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		r.mu.Lock()
		r.bodies = append(r.bodies, body)
		answer := r.answers[0]
		if len(r.answers) > 1 {
			r.answers = r.answers[1:]
		}
		r.mu.Unlock()
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(s.Close)
	p, err := NewWithQuerySource([]Egress{{Name: "primary", URL: s.URL, Tenants: []string{"tenant-fixture"}, Spaces: []string{"scope-fixture"},
		Endpoints: []Endpoint{{Path: "/query/ts/raw", StartField: "start_time", EndField: "end_time", TimeUnit: "s"}}}}, nil, "alarmd-sre")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func (r *rawServer) body(i int) map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bodies[i]
}

func rawAnswer(total int, options map[string]any, rows ...map[string]any) string {
	encoded, _ := json.Marshal(map[string]any{"total": total, "list": rows, "result_table_options": options})
	return string(encoded)
}

func rawRow(at time.Time, text string) map[string]any {
	return map[string]any{"_time": at.UnixMilli(), "log": text, "dtEventTimeStamp": at.UnixMilli(), "gseIndex": 7, "__unique_key__": "u1",
		"__ext": map[string]any{"io_kubernetes_pod": "uq-0", "container_name": "unify-query", "io_kubernetes_pod_namespace": "fixture-ns", "io_kubernetes_pod_uid": "uid-1"}, "serverIp": "host-fixture"}
}

func searchRequest() LogSearchRequest {
	return LogSearchRequest{Egress: "primary", Tenant: "tenant-fixture", Space: "scope-fixture", TableID: "2_bklog.fixture", Window: "1h", Now: logNow,
		Redact: redact.New("runtime-credential").Text}
}

// One page comes back newest first through UQ's raw query with its own
// cursor asked for: each line's text, time and allowed fields, the text and
// the fields redacted under the contract before they leave. A line with
// nothing to redact comes back as written.
func TestLogSearchAnswersLinesRedactedNewestFirst(t *testing.T) {
	fieldOnly := rawRow(logNow.Add(-3*time.Minute), "a clean line under a credential-bearing path")
	fieldOnly["path"] = "/var/log/runtime-credential/0.log"
	server := &rawServer{answers: []string{rawAnswer(3, nil,
		rawRow(logNow.Add(-time.Minute), "dial runtime-credential failed token=abc123"),
		rawRow(logNow.Add(-2*time.Minute), "plain line kept as written"), fieldOnly)}}
	p := server.provider(t)
	request := searchRequest()
	request.QueryString = "log:*failed*"
	request.Lines = 5
	page, err := p.LogSearch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	body := server.body(0)
	query := body["query_list"].([]any)[0].(map[string]any)
	if query["data_source"] != "bklog" || query["table_id"] != "2_bklog.fixture" || query["query_string"] != "log:*failed*" || query["limit"] != float64(5) {
		t.Fatalf("query %v", query)
	}
	if order := body["order_by"].([]any); len(order) != 1 || order[0] != "-_time" || body["is_search_after"] != true {
		t.Fatalf("body %v: want newest first with UQ's search_after", body)
	}
	if body["start_time"] != strconv.FormatInt(logNow.Add(-time.Hour).Unix(), 10) || body["end_time"] != strconv.FormatInt(logNow.Unix(), 10) {
		t.Fatalf("window %v..%v, want the hour ending at now", body["start_time"], body["end_time"])
	}
	if len(page.Lines) != 3 || !page.Done || page.NextCursor != "" || page.Order != "desc" {
		t.Fatalf("page %+v", page)
	}
	first, second := page.Lines[0], page.Lines[1]
	if strings.Contains(first.Text, "runtime-credential") || strings.Contains(first.Text, "abc123") || !strings.Contains(first.Text, "dial") || !first.Redacted {
		t.Fatalf("first line %+v: want the credential and the token redacted, the rest kept", first)
	}
	if second.Text != "plain line kept as written" || second.Redacted || second.Truncated {
		t.Fatalf("second line %+v: want the text as written", second)
	}
	// A field value is redacted under the same contract as the text.
	third := page.Lines[2]
	if third.Text != "a clean line under a credential-bearing path" || !third.Redacted || strings.Contains(third.Fields["path"], "runtime-credential") || !strings.Contains(third.Fields["path"], "/var/log/") {
		t.Fatalf("third line %+v: want the path's credential redacted and the text kept", third)
	}
	if page.RedactedLines != 2 || page.TruncatedLines != 0 || page.StoppedAt != "" {
		t.Fatalf("page counts %+v", page)
	}
	if first.Time != logNow.Add(-time.Minute).Format(time.RFC3339Nano) || first.Fields["__ext.io_kubernetes_pod"] != "uq-0" || first.Fields["__ext.container_name"] != "unify-query" || first.Fields["serverIp"] != "host-fixture" {
		t.Fatalf("first line time and fields %+v", first)
	}
	if _, kept := first.Fields["__ext.io_kubernetes_pod_uid"]; kept {
		t.Fatalf("a field outside the allowed list came back: %v", first.Fields)
	}
	request.Order = "asc"
	server.answers = []string{rawAnswer(0, nil)}
	if _, err := p.LogSearch(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if order := server.body(1)["order_by"].([]any); order[0] != "_time" {
		t.Fatalf("asc order_by %v", order)
	}
}

// Every bound is enforced and every way a page is less than asked is named:
// a bound out of range is refused by name, a line longer than line_bytes is
// cut with its full length given, a page that would pass total_bytes or
// lines stops there with a cursor that reads on from the next line.
func TestLogSearchEnforcesAndNamesEveryLimit(t *testing.T) {
	server := &rawServer{answers: []string{rawAnswer(0, nil)}}
	p := server.provider(t)
	for name, mutate := range map[string]func(*LogSearchRequest){
		"lines":       func(r *LogSearchRequest) { r.Lines = LogSearchMaxLines + 1 },
		"line_bytes":  func(r *LogSearchRequest) { r.LineBytes = LogSearchMaxLineBytes + 1 },
		"total_bytes": func(r *LogSearchRequest) { r.TotalBytes = LogSearchMaxTotalBytes + 1 },
		"window":      func(r *LogSearchRequest) { r.Window = "31d" },
		"order":       func(r *LogSearchRequest) { r.Order = "sideways" },
		"table":       func(r *LogSearchRequest) { r.TableID = "2_bkmonitor.metric" },
	} {
		request := searchRequest()
		mutate(&request)
		if _, err := p.LogSearch(context.Background(), request); code(err) != "invalid_input" {
			t.Fatalf("%s out of range: %v", name, err)
		}
	}
	request := searchRequest()
	request.Redact = nil
	if _, err := p.LogSearch(context.Background(), request); code(err) != "log_search_redaction_unconfigured" {
		t.Fatalf("no redaction: %v", err)
	}
	request = searchRequest()
	request.Cursor = strings.Repeat("a", LogSearchMaxCursorBytes+1)
	if _, err := p.LogSearch(context.Background(), request); code(err) != "log_search_cursor_invalid" {
		t.Fatalf("oversized cursor: %v", err)
	}

	// line_bytes: cut on a character boundary, the full length given.
	server.answers = []string{rawAnswer(1, nil, rawRow(logNow.Add(-time.Minute), "超时 connecting to storage backend"))}
	request = searchRequest()
	request.LineBytes = 8
	page, err := p.LogSearch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	line := page.Lines[0]
	if !line.Truncated || line.TextBytes != len("超时 connecting to storage backend") || len(line.Text) > 8 || !strings.HasPrefix(line.Text, "超时") || page.TruncatedLines != 1 {
		t.Fatalf("cut line %+v page %+v", line, page)
	}
	if !strings.Contains(strings.Join(page.Limitations, ";"), "line_bytes 8") {
		t.Fatalf("limitations %v do not name line_bytes", page.Limitations)
	}

	// total_bytes: the page stops before the line that would pass it.
	rows := []map[string]any{}
	for i := 0; i < 4; i++ {
		rows = append(rows, rawRow(logNow.Add(-time.Duration(i+1)*time.Minute), strings.Repeat("x", 100)))
	}
	server.answers = []string{rawAnswer(4, nil, rows...), rawAnswer(4, nil, rows...)}
	request = searchRequest()
	request.Lines, request.TotalBytes = 4, 300
	page, err = p.LogSearch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Lines) != 2 || page.StoppedAt != "total_bytes" || page.NextCursor == "" || page.Done {
		t.Fatalf("total_bytes page %+v", page)
	}
	if !strings.Contains(strings.Join(page.Limitations, ";"), "total_bytes 300") {
		t.Fatalf("limitations %v do not name total_bytes", page.Limitations)
	}
	next := searchRequest()
	next.Window, next.Lines, next.TotalBytes, next.Cursor = "", 4, 300, page.NextCursor
	rest, err := p.LogSearch(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	// The native page was full and UQ gave no continuation: the page says it
	// cannot read past it instead of claiming the window is done.
	if len(rest.Lines) != 2 || rest.Lines[0].Time != logNow.Add(-3*time.Minute).Format(time.RFC3339Nano) || rest.Done || rest.NextCursor != "" ||
		!strings.Contains(strings.Join(rest.Limitations, ";"), "without a continuation") {
		t.Fatalf("the next page %+v does not start at the third line and end the window", rest)
	}

	// lines: the page stops at the lines asked even inside a larger native page.
	cursor := logCursor{Version: 1, Start: logNow.Add(-time.Hour).Unix(), End: logNow.Unix(), Order: "desc", PageSize: 4}
	cursor.Binding = logBinding("2_bklog.fixture", "", "desc", logNow.Add(-time.Hour), logNow)
	issued, _ := cursor.encode()
	server.answers = []string{rawAnswer(4, nil, rows...)}
	request = searchRequest()
	request.Window, request.Cursor, request.Lines = "", issued, 1
	page, err = p.LogSearch(context.Background(), request)
	if err != nil || len(page.Lines) != 1 || page.StoppedAt != "lines" || page.NextCursor == "" || page.Done {
		t.Fatalf("lines-bounded page %+v %v", page, err)
	}
}

// The next page is read with UQ's own continuation, passed back as given and
// carrying only the sort key or offset - never the SQL UQ also returns. A
// cursor answers only the query it came from, with its own window.
func TestLogSearchReadsOnWithUQsOwnCursor(t *testing.T) {
	options := map[string]any{"2_bklog.fixture|doris": map[string]any{"search_after": []any{1792735140000, "u1"}, "sql": "SELECT log FROM t WHERE secret-filter"}}
	server := &rawServer{answers: []string{
		rawAnswer(3, options, rawRow(logNow.Add(-time.Minute), "first"), rawRow(logNow.Add(-2*time.Minute), "second")),
		rawAnswer(3, nil, rawRow(logNow.Add(-3*time.Minute), "third")),
	}}
	p := server.provider(t)
	request := searchRequest()
	request.QueryString, request.Lines = "log:*x*", 2
	page, err := p.LogSearch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == "" || page.Done || strings.Contains(page.NextCursor, "SELECT") {
		t.Fatalf("first page %+v", page)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "secret-filter") || strings.Contains(string(raw), "SELECT") {
		t.Fatalf("the page carries UQ's SQL: %s", raw)
	}
	next := request
	next.Window, next.Cursor = "", page.NextCursor
	rest, err := p.LogSearch(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	sent, _ := json.Marshal(server.body(1)["result_table_options"])
	if string(sent) != `{"2_bklog.fixture|doris":{"search_after":[1792735140000,"u1"]}}` {
		t.Fatalf("continuation sent %s, want UQ's search_after alone", sent)
	}
	if server.body(1)["start_time"] != server.body(0)["start_time"] || server.body(1)["end_time"] != server.body(0)["end_time"] {
		t.Fatalf("the cursor's window moved: %v..%v then %v..%v", server.body(0)["start_time"], server.body(0)["end_time"], server.body(1)["start_time"], server.body(1)["end_time"])
	}
	if len(rest.Lines) != 1 || rest.Lines[0].Text != "third" || !rest.Done {
		t.Fatalf("second page %+v", rest)
	}
	other := next
	other.QueryString = "log:*y*"
	if _, err := p.LogSearch(context.Background(), other); code(err) != "log_search_cursor_mismatch" {
		t.Fatalf("cursor on another filter: %v", err)
	}
	withWindow := next
	withWindow.Window = "2h"
	if _, err := p.LogSearch(context.Background(), withWindow); code(err) != "invalid_input" {
		t.Fatalf("cursor with a window: %v", err)
	}
	server.answers = []string{rawAnswer(2, map[string]any{"t": map[string]any{"search_after": []any{strings.Repeat("row text ", 40)}}},
		rawRow(logNow.Add(-time.Minute), "a"), rawRow(logNow.Add(-2*time.Minute), "b"))}
	if _, err := p.LogSearch(context.Background(), request); code(err) != "uq_response_invalid" {
		t.Fatalf("a value-bearing continuation: %v", err)
	}
}

// Redaction comes before the cut: a credential that straddles line_bytes, or
// the 256 bytes a field value is cut to, is redacted whole, so no prefix of it
// leaves; text_bytes is the redacted length, and a cut field is named.
func TestLogSearchRedactsBeforeItCuts(t *testing.T) {
	text := strings.Repeat("x", 10) + "runtime-credential tail"
	row := rawRow(logNow.Add(-time.Minute), text)
	row["path"] = "/" + strings.Repeat("a", 250) + "runtime-credential/0.log"
	server := &rawServer{answers: []string{rawAnswer(1, nil, row)}}
	p := server.provider(t)
	request := searchRequest()
	request.LineBytes = 15
	page, err := p.LogSearch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	line := page.Lines[0]
	redactedText := redact.New("runtime-credential").Text(text)
	if strings.Contains(line.Text, "runti") || !line.Redacted || !line.Truncated || line.TextBytes != len(redactedText) || line.Text != redactedText[:15] {
		t.Fatalf("straddling text %+v: want it redacted whole, then cut; text_bytes %d", line, len(redactedText))
	}
	path := line.Fields["path"]
	if strings.Contains(path, "runti") || len(path) != LogCountValueBytes || len(line.FieldsCut) != 1 || line.FieldsCut[0] != "path" || page.FieldsCut != 1 {
		t.Fatalf("straddling field %q (%d bytes) cut %v page %d: want it redacted whole, then cut and named", path, len(path), line.FieldsCut, page.FieldsCut)
	}
	if !strings.Contains(strings.Join(page.Limitations, ";"), "field values were cut to 256 bytes") {
		t.Fatalf("limitations %v do not name the cut field", page.Limitations)
	}
}

// A cursor is the caller's to send and its binding a digest anyone can
// compute, so its window, offset and tables are held to the first page's
// rules again: an end in the future, a span outside 1m..30d, an offset past
// the maximum and another table are each refused by name; a continuation
// from UQ past the maximum offset is not handed out.
func TestLogSearchHoldsACursorToTheFirstPagesRules(t *testing.T) {
	server := &rawServer{answers: []string{rawAnswer(0, nil)}}
	p := server.provider(t)
	built := func(start, end time.Time, options map[string]logCursorTableOptions) string {
		c := logCursor{Version: 1, Start: start.Unix(), End: end.Unix(), Order: "desc", PageSize: 10, Options: options,
			Binding: logBinding("2_bklog.fixture", "", "desc", start.UTC(), end.UTC())}
		encoded, _ := c.encode()
		return encoded
	}
	deep, shallow := LogSearchMaxOffset+1, 100
	for name, tc := range map[string]struct {
		cursor string
		code   string
	}{
		"end in the future": {built(logNow.Add(23*time.Hour), logNow.Add(24*time.Hour), nil), "log_search_cursor_invalid"},
		"span over 30 days": {built(logNow.Add(-31*24*time.Hour), logNow, nil), "log_search_cursor_invalid"},
		"span under 1m":     {built(logNow.Add(-30*time.Second), logNow, nil), "log_search_cursor_invalid"},
		"offset past max":   {built(logNow.Add(-time.Hour), logNow, map[string]logCursorTableOptions{"2_bklog.fixture|doris": {From: &deep}}), "log_search_cursor_offset_exceeded"},
		"another table":     {built(logNow.Add(-time.Hour), logNow, map[string]logCursorTableOptions{"2_bklog.other|doris": {From: &shallow}}), "log_search_cursor_invalid"},
	} {
		request := searchRequest()
		request.Window, request.Cursor = "", tc.cursor
		if _, err := p.LogSearch(context.Background(), request); code(err) != tc.code {
			t.Fatalf("%s: %v, want %s", name, err, tc.code)
		}
	}
	request := searchRequest()
	request.Window, request.Cursor = "", built(logNow.Add(-time.Hour), logNow, map[string]logCursorTableOptions{"2_bklog.fixture|doris": {From: &shallow}})
	if _, err := p.LogSearch(context.Background(), request); err != nil {
		t.Fatalf("a cursor within every rule: %v", err)
	}
	server.answers = []string{rawAnswer(3, map[string]any{"2_bklog.fixture|doris": map[string]any{"from": LogSearchMaxOffset + 2}},
		rawRow(logNow.Add(-time.Minute), "a"), rawRow(logNow.Add(-2*time.Minute), "b"))}
	request = searchRequest()
	request.Lines = 2
	page, err := p.LogSearch(context.Background(), request)
	if err != nil || page.NextCursor != "" || page.Done || !strings.Contains(strings.Join(page.Limitations, ";"), "passes the maximum") {
		t.Fatalf("continuation past the maximum offset: %+v %v", page, err)
	}
}
