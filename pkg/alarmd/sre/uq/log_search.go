// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package uq

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The bounds of one log search page: how many lines it answers, how long
// one line may be, how many bytes of text the page carries in all, and how
// long a cursor may be. A page at every maximum is a few hundred kilobytes of
// text and fields, inside the channel's response bound.
const (
	LogSearchDefaultLines      = 100
	LogSearchMaxLines          = 500
	LogSearchDefaultLineBytes  = 2048
	LogSearchMaxLineBytes      = 16 << 10
	LogSearchDefaultTotalBytes = 256 << 10
	LogSearchMaxTotalBytes     = 1 << 20
	LogSearchMaxCursorBytes    = 4096
	// LogSearchMaxOffset bounds the offset a cursor may carry where the
	// storage pages by offset: a cursor asks UQ for at most this many lines
	// past the window's first, so no call reads deep into a window.
	LogSearchMaxOffset = 10000
	// logSearchCursorValues bounds a table's search_after values and the
	// bytes of each, so a cursor carries sort keys, never a row.
	logSearchCursorValues     = 8
	logSearchCursorValueBytes = 128
)

// logSearchFields are the metadata fields a line carries beside its text,
// read either nested under __ext or flattened; any other column of the row
// stays out of the answer.
var logSearchFields = []string{"__ext.io_kubernetes_pod_namespace", "__ext.io_kubernetes_pod", "__ext.container_name", "serverIp", "path", "gseIndex", "iterationIndex"}

// LogSearchRequest reads one page of a log table's lines. Redact is applied
// to every line's text and field values before they are cut or counted; a
// request without it is refused, so no caller can read text unredacted.
type LogSearchRequest struct {
	Egress, Tenant, Space string
	TableID               string
	QueryString           string
	Window, End           string
	// Order is "desc" (newest first, the default) or "asc".
	Order      string
	Lines      int
	LineBytes  int
	TotalBytes int
	Cursor     string
	Redact     func(string) string
	Now        time.Time
}

// LogLine is one line: its time, the allowed metadata fields, its text after
// redaction and the cut, and the text's length before the cut.
type LogLine struct {
	Time      string            `json:"time"`
	Fields    map[string]string `json:"fields"`
	Text      string            `json:"text"`
	TextBytes int               `json:"text_bytes"`
	Truncated bool              `json:"truncated,omitempty"`
	Redacted  bool              `json:"redacted,omitempty"`
	// FieldsCut names the fields whose values were cut to 256 bytes.
	FieldsCut []string `json:"fields_cut,omitempty"`
}

// LogSearchLimits are the bounds this page was read under.
type LogSearchLimits struct {
	Lines      int `json:"lines"`
	LineBytes  int `json:"line_bytes"`
	TotalBytes int `json:"total_bytes"`
}

// LogPage answers one page. Every way the page is less than the lines asked
// is named: StoppedAt says which bound ended it, TruncatedLines how many
// lines were cut to line_bytes, RedactedLines how many lines redaction
// changed. NextCursor reads on from exactly the next line; it is empty when
// the window holds no more lines (Done) or UQ gave no continuation.
type LogPage struct {
	TableID           string          `json:"table_id"`
	Order             string          `json:"order"`
	Start             time.Time       `json:"start"`
	End               time.Time       `json:"end"`
	Lines             []LogLine       `json:"lines"`
	Total             int64           `json:"total"`
	Done              bool            `json:"done"`
	NextCursor        string          `json:"next_cursor,omitempty"`
	StoppedAt         string          `json:"stopped_at,omitempty"`
	TruncatedLines    int             `json:"truncated_lines"`
	FieldsCut         int             `json:"fields_cut"`
	RedactedLines     int             `json:"redacted_lines"`
	Partial           bool            `json:"partial"`
	QueryStringBytes  int             `json:"query_string_bytes"`
	Limits            LogSearchLimits `json:"limits"`
	RedactionContract string          `json:"redaction_contract"`
	Limitations       []string        `json:"limitations"`
}

// logCursor is a page's position: the native result-table options the page
// was read with (UQ's own search_after or offset, per table), how many of its
// rows earlier calls already answered, the native page size, and the query it
// belongs to.
type logCursor struct {
	Version  int                              `json:"v"`
	Binding  string                           `json:"b"`
	Start    int64                            `json:"s"`
	End      int64                            `json:"e"`
	Order    string                           `json:"o"`
	Skip     int                              `json:"k"`
	PageSize int                              `json:"n"`
	Options  map[string]logCursorTableOptions `json:"r,omitempty"`
}

type logCursorTableOptions struct {
	From        *int  `json:"from,omitempty"`
	SearchAfter []any `json:"search_after,omitempty"`
}

// logBinding names the query a cursor belongs to, so a cursor is not read
// against another table, filter, window or order.
func logBinding(table, queryString, order string, start, end time.Time) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{table, queryString, order, strconv.FormatInt(start.Unix(), 10), strconv.FormatInt(end.Unix(), 10)}, "\x00")))
	return hex.EncodeToString(digest[:12])
}

func (c logCursor) encode() (string, bool) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", false
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	return encoded, len(encoded) <= LogSearchMaxCursorBytes
}

func decodeLogCursor(value string) (logCursor, bool) {
	var c logCursor
	if len(value) > LogSearchMaxCursorBytes {
		return c, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(raw, &c) != nil || c.Version != 1 || c.Skip < 0 || c.PageSize < 1 || c.PageSize > LogSearchMaxLines {
		return c, false
	}
	for _, options := range c.Options {
		if !cursorValues(options.SearchAfter) || (options.From != nil && *options.From < 0) {
			return c, false
		}
	}
	return c, true
}

// cursorValues keeps a search_after a list of short scalars: sort keys, not
// row content.
func cursorValues(values []any) bool {
	if len(values) > logSearchCursorValues {
		return false
	}
	for _, value := range values {
		switch v := value.(type) {
		case float64, bool, nil, json.Number:
		case string:
			if len(v) > logSearchCursorValueBytes {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// nativeOptions keeps only the continuation from UQ's result_table_options:
// a table's offset and search_after. The SQL and schema UQ also returns stay
// out of the cursor.
func nativeOptions(raw json.RawMessage) (map[string]logCursorTableOptions, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, true
	}
	var tables map[string]struct {
		From        *int  `json:"from"`
		SearchAfter []any `json:"search_after"`
	}
	if json.Unmarshal(raw, &tables) != nil {
		return nil, false
	}
	out := map[string]logCursorTableOptions{}
	for table, options := range tables {
		if !cursorValues(options.SearchAfter) {
			return nil, false
		}
		if options.From != nil || len(options.SearchAfter) > 0 {
			out[table] = logCursorTableOptions{From: options.From, SearchAfter: options.SearchAfter}
		}
	}
	return out, true
}

// LogSearch reads one page of a log table's lines in time order through UQ's
// raw query, with UQ's own cursor (search_after, or an offset where the
// storage has no stable sort key). The answer is read here: only each line's
// time, its allowed metadata fields and its redacted text leave.
func (p *Provider) LogSearch(ctx context.Context, request LogSearchRequest) (LogPage, error) {
	page := LogPage{TableID: request.TableID, Lines: []LogLine{}, Limitations: []string{}, QueryStringBytes: len(request.QueryString)}
	if request.Redact == nil {
		return page, fail("log_search_redaction_unconfigured", "Log text is answered only through a redaction contract; none was given.")
	}
	if !logTable.MatchString(request.TableID) {
		return page, fail("invalid_input", "table_id must be a log result table, <business>_bklog.<name>.")
	}
	if len(request.QueryString) > 8192 {
		return page, fail("invalid_input", "query_string is at most 8192 bytes.")
	}
	order := request.Order
	if order == "" {
		order = "desc"
	}
	if order != "desc" && order != "asc" {
		return page, fail("invalid_input", "order is desc (newest first) or asc (oldest first).")
	}
	limits := LogSearchLimits{Lines: request.Lines, LineBytes: request.LineBytes, TotalBytes: request.TotalBytes}
	for _, bound := range []struct {
		value         *int
		fallback, max int
		name          string
	}{{&limits.Lines, LogSearchDefaultLines, LogSearchMaxLines, "lines"}, {&limits.LineBytes, LogSearchDefaultLineBytes, LogSearchMaxLineBytes, "line_bytes"},
		{&limits.TotalBytes, LogSearchDefaultTotalBytes, LogSearchMaxTotalBytes, "total_bytes"}} {
		if *bound.value == 0 {
			*bound.value = bound.fallback
		}
		if *bound.value < 1 || *bound.value > bound.max {
			return page, fail("invalid_input", fmt.Sprintf("%s must be 1 to %d.", bound.name, bound.max))
		}
	}
	page.Limits, page.Order = limits, order
	now := request.Now
	if now.IsZero() {
		now = time.Now()
	}
	var start, end time.Time
	var cursor logCursor
	if request.Cursor != "" {
		var ok bool
		if cursor, ok = decodeLogCursor(request.Cursor); !ok {
			return page, fail("log_search_cursor_invalid", fmt.Sprintf("cursor is not one this operation issued, or is longer than %d bytes.", LogSearchMaxCursorBytes))
		}
		start, end = time.Unix(cursor.Start, 0).UTC(), time.Unix(cursor.End, 0).UTC()
		if cursor.Order != order || cursor.Binding != logBinding(request.TableID, request.QueryString, order, start, end) {
			return page, fail("log_search_cursor_mismatch", "cursor belongs to another table, filter or order; pass the same ones the first page was read with.")
		}
		// A cursor is the caller's to send, and its binding is a digest the
		// caller can compute: its window, offset and tables are held to the
		// first page's rules again here, not trusted.
		if end.After(now) || end.Sub(start) < time.Minute || end.Sub(start) > MaxWindow {
			return page, fail("log_search_cursor_invalid", "the cursor's window must end no later than now and span 1m to 30 days, as a first page's does.")
		}
		for table, options := range cursor.Options {
			if table != request.TableID && !strings.HasPrefix(table, request.TableID+"|") {
				return page, fail("log_search_cursor_invalid", "the cursor names a table other than table_id.")
			}
			if options.From != nil && *options.From > LogSearchMaxOffset {
				return page, fail("log_search_cursor_offset_exceeded", fmt.Sprintf("the cursor's offset is past the maximum %d; narrow the window or the filter.", LogSearchMaxOffset))
			}
		}
		if request.Window != "" || request.End != "" {
			return page, fail("invalid_input", "a cursor carries its window; omit window and end when passing one.")
		}
	} else {
		window := request.Window
		if window == "" {
			window = "1h"
		}
		length, ok := countWindow(window)
		if !ok {
			return page, fail("invalid_input", "window must be a duration such as 90m, 6h or 3d, from 1m up to 30 days.")
		}
		end = now.UTC().Truncate(time.Second)
		if request.End != "" {
			parsed, err := time.Parse(time.RFC3339, request.End)
			if err != nil || !strings.HasSuffix(request.End, "Z") {
				return page, fail("invalid_input", "end must be an RFC 3339 time in UTC, ending in Z.")
			}
			if parsed.After(now) {
				return page, fail("invalid_input", "end is in the future.")
			}
			end = parsed.UTC().Truncate(time.Second)
		}
		start = end.Add(-length)
		cursor = logCursor{Version: 1, Binding: logBinding(request.TableID, request.QueryString, order, start, end), Start: start.Unix(), End: end.Unix(), Order: order, PageSize: limits.Lines}
	}
	page.Start, page.End = start, end
	egress, ok := p.egresses[request.Egress]
	if !ok {
		return page, fail("uq_egress_unknown", "Egress is not registered in this deployment.")
	}
	endpoint := ""
	for _, e := range egress.Endpoints {
		if strings.TrimSuffix(e.Path, "/") == "/query/ts/raw" && e.TimeUnit == "s" {
			endpoint = e.Path
		}
	}
	if endpoint == "" {
		return page, fail("uq_endpoint_denied", "This egress has no /query/ts/raw endpoint with second timestamps.")
	}
	query := map[string]any{"data_source": "bklog", "table_id": request.TableID, "field_name": "log", "reference_name": "a", "limit": cursor.PageSize, "from": 0}
	if request.QueryString != "" {
		query["query_string"] = request.QueryString
	}
	sort := "_time"
	if order == "desc" {
		sort = "-_time"
	}
	body := map[string]any{"query_list": []any{query}, "metric_merge": "a", "order_by": []any{sort}, "is_search_after": true, "timezone": "UTC",
		"start_time": strconv.FormatInt(start.Unix(), 10), "end_time": strconv.FormatInt(end.Unix(), 10)}
	if len(cursor.Options) > 0 {
		options := map[string]any{}
		for table, value := range cursor.Options {
			option := map[string]any{}
			if value.From != nil {
				option["from"] = *value.From
			}
			if len(value.SearchAfter) > 0 {
				option["search_after"] = value.SearchAfter
			}
			options[table] = option
		}
		body["result_table_options"] = options
	}
	// The answer is read here and only redacted lines leave it: the log
	// masking that keeps native log answers out of a receipt is replaced by
	// the redaction below, the same contract pod.logs answers container
	// output under.
	receipt, err := p.query(ctx, Request{Egress: request.Egress, Endpoint: endpoint, Tenant: request.Tenant, Space: request.Space, Body: body}, true)
	if err != nil {
		return page, err
	}
	var answer struct {
		Total              int64            `json:"total"`
		List               []map[string]any `json:"list"`
		ResultTableOptions json.RawMessage  `json:"result_table_options"`
		IsPartial          bool             `json:"is_partial"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(receipt.Native)))
	decoder.UseNumber()
	if decoder.Decode(&answer) != nil {
		return page, fail("uq_response_invalid", "The raw log answer could not be read.")
	}
	page.Total, page.Partial = answer.Total, answer.IsPartial
	if answer.IsPartial {
		page.Limitations = append(page.Limitations, "UQ marked the answer partial: some storage did not answer, so lines may be missing")
	}
	next, ok := nativeOptions(answer.ResultTableOptions)
	if !ok {
		return page, fail("uq_response_invalid", "UQ's continuation is not a sort key or offset this operation can carry.")
	}
	deep := false
	for table, options := range next {
		if table != request.TableID && !strings.HasPrefix(table, request.TableID+"|") {
			return page, fail("uq_response_invalid", "UQ's continuation names a table other than table_id.")
		}
		if options.From != nil && *options.From > LogSearchMaxOffset {
			deep = true
		}
	}
	rows := answer.List
	if cursor.Skip > len(rows) {
		return page, fail("log_search_cursor_stale", "the page this cursor points into has fewer lines than it had; read again without the cursor.")
	}
	used := 0
	answered := 0
	for _, row := range rows[cursor.Skip:] {
		if answered == limits.Lines {
			page.StoppedAt = "lines"
			break
		}
		line, redacted := logLine(row, request.Redact, limits.LineBytes)
		size := len(line.Text)
		for _, value := range line.Fields {
			size += len(value)
		}
		if answered > 0 && used+size > limits.TotalBytes {
			page.StoppedAt = "total_bytes"
			break
		}
		used += size
		answered++
		if line.Truncated {
			page.TruncatedLines++
		}
		page.FieldsCut += len(line.FieldsCut)
		if redacted {
			page.RedactedLines++
		}
		page.Lines = append(page.Lines, line)
	}
	consumed := cursor.Skip + answered
	switch {
	case consumed < len(rows):
		// The page stopped inside the native page: read it again from where
		// this one stopped.
		resume := cursor
		resume.Skip = consumed
		page.NextCursor, ok = resume.encode()
	case len(rows) < cursor.PageSize:
		page.Done = true
	case len(next) == 0:
		page.Limitations = append(page.Limitations, "UQ returned a full page without a continuation, so this operation cannot read past it; narrow the window or the filter")
	case deep:
		page.Limitations = append(page.Limitations, fmt.Sprintf("the next page's offset passes the maximum %d, so no cursor is given; narrow the window or the filter", LogSearchMaxOffset))
	default:
		resume := logCursor{Version: 1, Binding: cursor.Binding, Start: cursor.Start, End: cursor.End, Order: order, PageSize: limits.Lines, Options: next}
		page.NextCursor, ok = resume.encode()
	}
	if !ok {
		page.NextCursor = ""
		page.Limitations = append(page.Limitations, fmt.Sprintf("the next cursor would exceed %d bytes; narrow the window or the filter", LogSearchMaxCursorBytes))
	}
	if page.StoppedAt == "total_bytes" {
		page.Limitations = append(page.Limitations, fmt.Sprintf("the page stopped at %d lines to stay within total_bytes %d; next_cursor reads on from the next line", answered, limits.TotalBytes))
	}
	if page.FieldsCut > 0 {
		page.Limitations = append(page.Limitations, fmt.Sprintf("%d field values were cut to %d bytes; fields_cut names them per line", page.FieldsCut, LogCountValueBytes))
	}
	if page.TruncatedLines > 0 {
		page.Limitations = append(page.Limitations, fmt.Sprintf("%d lines were cut to line_bytes %d; text_bytes is each line's length before the cut", page.TruncatedLines, limits.LineBytes))
	}
	return page, nil
}

// logLine reads one raw row: its time, its allowed fields and its text, each
// redacted before the text is cut to lineBytes on a character boundary.
func logLine(row map[string]any, redact func(string) string, lineBytes int) (LogLine, bool) {
	line := LogLine{Fields: map[string]string{}}
	redacted := false
	text := scalarText(row["log"])
	if text == "" {
		text = scalarText(row["_value"])
	}
	clean := redact(text)
	redacted = clean != text
	line.TextBytes = len(clean)
	if len(clean) > lineBytes {
		cut := clean[:lineBytes]
		for !utf8.ValidString(cut) && len(cut) > 0 {
			cut = cut[:len(cut)-1]
		}
		clean, line.Truncated = cut, true
	}
	line.Text = clean
	line.Time = logTime(row)
	ext, _ := row["__ext"].(map[string]any)
	if ext == nil {
		if encoded, isText := row["__ext"].(string); isText {
			_ = json.Unmarshal([]byte(encoded), &ext)
		}
	}
	for _, field := range logSearchFields {
		value, found := row[field]
		if !found && strings.HasPrefix(field, "__ext.") && ext != nil {
			value, found = ext[strings.TrimPrefix(field, "__ext.")]
		}
		if !found || value == nil {
			continue
		}
		raw := scalarText(value)
		clean := redact(raw)
		if clean != raw {
			redacted = true
		}
		if len(clean) > LogCountValueBytes {
			line.FieldsCut = append(line.FieldsCut, field)
		}
		line.Fields[field] = cutValue(clean)
	}
	line.Redacted = redacted
	return line, redacted
}

// logTime is a row's time in RFC 3339 UTC, from the millisecond _time or
// dtEventTimeStamp column, or the row's time text as UQ gave it.
func logTime(row map[string]any) string {
	for _, field := range []string{"_time", "dtEventTimeStamp"} {
		switch v := row[field].(type) {
		case json.Number:
			if ms, err := v.Int64(); err == nil {
				return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
			}
			if f, err := v.Float64(); err == nil {
				return time.UnixMilli(int64(f)).UTC().Format(time.RFC3339Nano)
			}
		case string:
			if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
				return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
			}
			if v != "" {
				return v
			}
		}
	}
	return scalarText(row["time"])
}

func scalarText(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		return strconv.FormatBool(v)
	default:
		encoded, _ := json.Marshal(v)
		return string(encoded)
	}
}
