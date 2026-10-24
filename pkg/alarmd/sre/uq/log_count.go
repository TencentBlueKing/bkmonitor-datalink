// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package uq

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The bounds of one log count: how many groups it answers (a thousand
// groups of a few 256 byte values is a few MB, inside the channel's
// response bound) and how long a field value it carries.
const (
	LogCountDefaultTop = 100
	LogCountMaxTop     = 1000
	LogCountValueBytes = 256
)

// logTable is a log result table's name: <business>_bklog.<name>.
var logTable = regexp.MustCompile(`^[0-9]+_bklog\.[A-Za-z0-9_]+$`)

// LogCountRequest counts a log table's lines by fields over a window.
type LogCountRequest struct {
	Egress, Tenant, Space string
	TableID               string
	By                    []string
	QueryString           string
	Window, End           string
	Top                   int
	Now                   time.Time
}

// LogCountGroup is one group: its field values and how many lines it has.
type LogCountGroup struct {
	Values map[string]string `json:"values"`
	Count  int64             `json:"count"`
}

// LogCounts answers a log count: the groups by count, then value, cut to
// top, with every group's total. It carries no log text: field values only,
// and the filter's length, not the filter.
type LogCounts struct {
	TableID          string          `json:"table_id"`
	By               []string        `json:"by"`
	Start            time.Time       `json:"start"`
	End              time.Time       `json:"end"`
	Total            int64           `json:"total"`
	Groups           []LogCountGroup `json:"groups"`
	GroupsTotal      int             `json:"groups_total"`
	Truncated        bool            `json:"truncated"`
	Partial          bool            `json:"partial"`
	QueryStringBytes int             `json:"query_string_bytes"`
	Limitations      []string        `json:"limitations"`
}

// contentField is a field that holds a log's text: grouping by it would
// answer each line's text as a value.
func contentField(field string) bool {
	name := strings.ToLower(field)
	switch name {
	case "log", "message", "content", "msg", "body":
		return true
	}
	for _, suffix := range []string{".log", ".message", "_log", "_message", ".body"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// LogCount counts a log table's lines by fields in one aggregate query:
// count_over_time over the whole window, summed by the fields. The answer is
// read here and only the groups and counts leave it.
func (p *Provider) LogCount(ctx context.Context, request LogCountRequest) (LogCounts, error) {
	counts := LogCounts{TableID: request.TableID, By: request.By, Groups: []LogCountGroup{}, Limitations: []string{}, QueryStringBytes: len(request.QueryString)}
	if !logTable.MatchString(request.TableID) {
		return counts, fail("invalid_input", "table_id must be a log result table, <business>_bklog.<name>.")
	}
	if len(request.By) == 0 {
		return counts, fail("invalid_input", "by needs at least one field.")
	}
	for _, field := range request.By {
		if !text(field) {
			return counts, fail("invalid_input", "every field in by must be nonempty text.")
		}
		if contentField(field) {
			return counts, fail("log_count_content_field", fmt.Sprintf("%s holds log text; grouping by it would answer the text. Group by a metadata field and filter with query_string.", field))
		}
	}
	top := request.Top
	if top == 0 {
		top = LogCountDefaultTop
	}
	if top < 1 || top > LogCountMaxTop {
		return counts, fail("invalid_input", fmt.Sprintf("top must be 1 to %d.", LogCountMaxTop))
	}
	window := request.Window
	if window == "" {
		window = "1h"
	}
	length, ok := countWindow(window)
	if !ok {
		return counts, fail("invalid_input", "window must be a duration such as 90m, 6h or 3d, from 1m up to 30 days.")
	}
	now := request.Now
	if now.IsZero() {
		now = time.Now()
	}
	end := now.UTC()
	if request.End != "" {
		parsed, err := time.Parse(time.RFC3339, request.End)
		if err != nil || !strings.HasSuffix(request.End, "Z") {
			return counts, fail("invalid_input", "end must be an RFC 3339 time in UTC, ending in Z.")
		}
		if parsed.After(now) {
			return counts, fail("invalid_input", "end is in the future.")
		}
		end = parsed.UTC()
	}
	counts.Start, counts.End = end.Add(-length), end
	egress, ok := p.egresses[request.Egress]
	if !ok {
		return counts, fail("uq_egress_unknown", "Egress is not registered in this deployment.")
	}
	endpoint := ""
	for _, e := range egress.Endpoints {
		if strings.TrimSuffix(e.Path, "/") == "/query/ts" && e.TimeUnit == "s" {
			endpoint = e.Path
		}
	}
	if endpoint == "" {
		return counts, fail("uq_endpoint_denied", "This egress has no /query/ts endpoint with second timestamps.")
	}
	seconds := strconv.FormatInt(int64(length/time.Second), 10) + "s"
	dimensions := make([]any, len(request.By))
	for i, field := range request.By {
		dimensions[i] = field
	}
	query := map[string]any{"data_source": "bklog", "table_id": request.TableID, "field_name": "log", "reference_name": "a",
		"function":         []any{map[string]any{"method": "sum", "dimensions": dimensions}},
		"time_aggregation": map[string]any{"function": "count_over_time", "window": seconds}}
	if request.QueryString != "" {
		query["query_string"] = request.QueryString
	}
	// UQ's range is closed at its end and evaluates at every step from start
	// through end, each point counting the whole window that begins there: an
	// end_time of end would also answer the window from end on, and summing
	// both counted twice the window. end_time is a second before end, so the
	// window's own step is the only one asked, and the answer is still read
	// for the one bucket it begins in (below) whatever UQ returns past it.
	body := map[string]any{"query_list": []any{query}, "metric_merge": "a", "step": seconds,
		"start_time": strconv.FormatInt(counts.Start.Unix(), 10), "end_time": strconv.FormatInt(end.Add(-time.Second).Unix(), 10)}
	// The answer is read here and only its counts leave: the log masking that
	// keeps native log answers out of a receipt is not needed for it.
	receipt, err := p.query(ctx, Request{Egress: request.Egress, Endpoint: endpoint, Tenant: request.Tenant, Space: request.Space, Body: body}, true)
	if err != nil {
		return counts, err
	}
	var answer struct {
		Series []struct {
			GroupKeys   []string `json:"group_keys"`
			GroupValues []string `json:"group_values"`
			Values      [][]any  `json:"values"`
		} `json:"series"`
		IsPartial bool `json:"is_partial"`
	}
	if json.Unmarshal(receipt.Native, &answer) != nil {
		return counts, fail("uq_response_invalid", "The count answer could not be read.")
	}
	counts.Partial = answer.IsPartial
	if answer.IsPartial {
		counts.Limitations = append(counts.Limitations, "UQ marked the answer partial: some storage did not answer, so the counts are a lower bound")
	}
	// The count is the earliest bucket's: UQ aligns the window to whole
	// buckets, so a window that does not start on one is counted from the
	// bucket it starts in, and any later bucket is the next window's lines.
	bucket, found := int64(0), false
	for _, series := range answer.Series {
		for _, point := range series.Values {
			if at, ok := pointMillis(point); ok && (!found || at < bucket) {
				bucket, found = at, true
			}
		}
	}
	if found {
		if counted := time.UnixMilli(bucket).UTC(); !counted.Equal(counts.Start) {
			counts.Limitations = append(counts.Limitations, fmt.Sprintf("UQ counts whole %s buckets; the counted window is %s to %s, not the %s to %s asked",
				window, counted.Format(time.RFC3339), counted.Add(length).Format(time.RFC3339), counts.Start.Format(time.RFC3339), counts.End.Format(time.RFC3339)))
			counts.Start, counts.End = counted, counted.Add(length)
		}
	} else {
		// Zero groups then say only that nothing came back, not that the
		// window holds no lines: the reader is told which one it read.
		counts.Limitations = append(counts.Limitations, "UQ returned no points for this window")
	}
	for _, series := range answer.Series {
		group := LogCountGroup{Values: map[string]string{}}
		for i, key := range series.GroupKeys {
			if i < len(series.GroupValues) {
				group.Values[key] = cutValue(series.GroupValues[i])
			}
		}
		for _, point := range series.Values {
			if at, ok := pointMillis(point); ok && at == bucket {
				if value, ok := point[1].(float64); ok {
					group.Count += int64(value)
				}
			}
		}
		counts.Total += group.Count
		counts.Groups = append(counts.Groups, group)
	}
	sort.SliceStable(counts.Groups, func(i, j int) bool {
		if counts.Groups[i].Count != counts.Groups[j].Count {
			return counts.Groups[i].Count > counts.Groups[j].Count
		}
		return logGroupKey(counts.Groups[i], request.By) < logGroupKey(counts.Groups[j], request.By)
	})
	counts.GroupsTotal = len(counts.Groups)
	if len(counts.Groups) > top {
		counts.Groups, counts.Truncated = counts.Groups[:top], true
		counts.Limitations = append(counts.Limitations, fmt.Sprintf("the %d largest of %d groups; total counts every group", top, counts.GroupsTotal))
	}
	return counts, nil
}

// pointMillis is a [time, value] point's time, in milliseconds.
func pointMillis(point []any) (int64, bool) {
	if len(point) != 2 {
		return 0, false
	}
	at, ok := point[0].(float64)
	return int64(at), ok
}

func logGroupKey(group LogCountGroup, by []string) string {
	parts := make([]string, len(by))
	for i, field := range by {
		parts[i] = group.Values[field]
	}
	return strings.Join(parts, "\x00")
}

// cutValue keeps a field value to LogCountValueBytes, cut at a character.
func cutValue(value string) string {
	if len(value) <= LogCountValueBytes {
		return value
	}
	cut := LogCountValueBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// countWindow reads a window from a minute to MaxWindow: a duration or
// whole days.
func countWindow(window string) (time.Duration, bool) {
	var length time.Duration
	if days, found := strings.CutSuffix(window, "d"); found {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, false
		}
		length = time.Duration(n) * 24 * time.Hour
	} else {
		parsed, err := time.ParseDuration(window)
		if err != nil {
			return 0, false
		}
		length = parsed
	}
	return length, length >= time.Minute && length <= MaxWindow
}
