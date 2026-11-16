// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build unix

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	accessuq "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access/uq"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// The window measurements of decision-020 batch 8 (plan 5.2): a Query Group
// of 500,000 series - and 100,000, the two peaks of which may differ by one
// window at most - at 1, 5, 15 and 30 points a series, with no-data off and
// on (max_groups 500,000), through the production bundle on a real Redis
// under the budgets a replica of the grid's memory (4 GiB unset) derives, the fake UQ in a process of its
// own. Each prints one line a shape and fails a shape that did not succeed. Run on demand with
// ALARMD_MEASURE_WINDOWS=1; ALARMD_MEASURE_WINDOWS_SHAPE narrows the run to
// the shapes it names, comma separated (500000x30/on), which is how the
// serial run gives each shape its own process: the peak resident set the
// kernel reports is the process's lifetime peak.

// windowMeasureLimitBytes is the replica the measurements size for, 4 GiB
// unless ALARMD_MEASURE_LIMIT_GIB names another: the memory of a cell of the
// (cores, memory) grid (decision-020 0.6.x throughput design §5). The share
// and the soft limit follow from it as Load derives them.
const windowMeasureLimitBytes = 4 << 30

// windowMeasureLimit is the replica's memory limit this measurement sizes for.
func windowMeasureLimit(t *testing.T) uint64 {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("ALARMD_MEASURE_LIMIT_GIB"))
	if raw == "" {
		return windowMeasureLimitBytes
	}
	gib, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || gib < 1 || gib > 64 {
		t.Fatalf("ALARMD_MEASURE_LIMIT_GIB=%q is no memory of 1 to 64 GiB", raw)
	}
	return gib << 30
}

// windowMeasureDeadline (ALARMD_MEASURE_DEADLINE_SECONDS, 1 to 300) is the
// real time the measured Slot has to its deadline when it starts: the
// Slots' times are the measurement's own clock, an hour ahead of the real
// one without it, so no query of theirs runs out of time; with it, they are
// a few minutes ahead, and the measured Slot starts that long before its
// deadline, as a Slot does that starts late in its period or reads a slow
// backend. The way 6 check reads where a slice running out of time is put.
const windowMeasureDeadline = "ALARMD_MEASURE_DEADLINE_SECONDS"

// windowMeasureDeadlineSeconds is windowMeasureDeadline's value, 0 unset.
func windowMeasureDeadlineSeconds(t *testing.T) int64 {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(windowMeasureDeadline))
	if raw == "" {
		return 0
	}
	seconds, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || seconds < 1 || seconds > 300 {
		t.Fatalf("%s=%q is no deadline of 1 to 300 seconds", windowMeasureDeadline, raw)
	}
	return seconds
}

// windowMeasureUQLatency, in the fake UQ's process, holds every answer that
// long before it is written (ALARMD_MEASURE_UQ_LATENCY_MS): a backend slow
// as a whole, whatever the query reads, for the way 6 check that a small
// Query Group so answered is not sliced.
const windowMeasureUQLatency = "ALARMD_MEASURE_UQ_LATENCY_MS"

// windowMeasureUQSeriesMicros (ALARMD_MEASURE_UQ_SERIES_MICROS) is what
// each series a query answers adds to its time before its answer is
// written, from its arrival, in microseconds: 172 unset,
// 0.172 ms a series, from the largest Query Group read in production (about
// 1.1 points a series, 96,000 series, P95 16.5 s), so a round's wall time
// holds the queries' time as slices read side by side overlap it. Shapes of
// more points a series are held at the same rate: how UQ's time grows with
// the points is not read yet.
const windowMeasureUQSeriesMicros = "ALARMD_MEASURE_UQ_SERIES_MICROS"

// windowMeasureSeriesMicros is windowMeasureUQSeriesMicros's value.
func windowMeasureSeriesMicros(t *testing.T) int64 {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(windowMeasureUQSeriesMicros))
	if raw == "" {
		return 172
	}
	micros, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || micros < 0 || micros > 10_000 {
		t.Fatalf("%s=%q is no time of 0 to 10,000 microseconds a series", windowMeasureUQSeriesMicros, raw)
	}
	return micros
}

// windowMeasureUQFormat (ALARMD_MEASURE_UQ_FORMAT) is what the fake UQ
// answers a query in: "shared" unset, the shared schema format to a query
// that asks for it (accessuq.SharedSchemaMediaType in its Accept) and JSON
// to one that does not, as UQ 3.9.4892 answers in production - a Slot's
// ordinary query asks for it; "json", JSON to every query, the comparison.
// The version and the format are read off production UQ: every query
// that asked for the shared format (opt_in) was answered in it, what the
// line's uq_production_version says.
const (
	windowMeasureUQFormat       = "ALARMD_MEASURE_UQ_FORMAT"
	windowMeasureUQFormatShared = "shared"
	windowMeasureUQFormatJSON   = "json"
)

// windowMeasureUQMode (ALARMD_MEASURE_UQ_MODE) is how the fake UQ holds its
// latency: before the answer's headers unset; "504", then a 504 and no
// answer, as UQ answers a query past its backend's limit; "slow_body", after
// the headers and before the body. The way 6 check reads both: the
// provider's own timeout, and alarmd's deadline ending a body being read.
const (
	windowMeasureUQMode         = "ALARMD_MEASURE_UQ_MODE"
	windowMeasureUQMode504      = "504"
	windowMeasureUQModeSlowBody = "slow_body"
)

// windowMeasureMaxGroups is the no-data bound of the shapes that detect it.
const windowMeasureMaxGroups = 500_000

type windowMeasureShape struct {
	series, points int
	noData         bool
	// cold is a no-data shape whose memory is gone when the measured Slot
	// begins: its first write lands in the measured Slot, not a warm-up.
	cold bool
}

func (shape windowMeasureShape) String() string {
	state := "off"
	if shape.noData {
		state = "on"
	}
	if shape.cold {
		state += "/cold"
	}
	return fmt.Sprintf("%dx%d/%s", shape.series, shape.points, state)
}

// windowMeasureShapes is every shape, or the ones
// ALARMD_MEASURE_WINDOWS_SHAPE names.
func windowMeasureShapes() []windowMeasureShape {
	var shapes []windowMeasureShape
	for _, series := range []int{500_000, 100_000} {
		for _, points := range []int{1, 5, 15, 30} {
			for _, noData := range []bool{false, true} {
				shapes = append(shapes, windowMeasureShape{series: series, points: points, noData: noData})
			}
		}
	}
	// The largest no-data first write, in the measured Slot.
	shapes = append(shapes, windowMeasureShape{series: 500_000, points: 30, noData: true, cold: true})
	named := strings.TrimSpace(os.Getenv("ALARMD_MEASURE_WINDOWS_SHAPE"))
	if named == "" {
		return shapes
	}
	wanted := map[string]bool{}
	for _, name := range strings.Split(named, ",") {
		wanted[strings.TrimSpace(name)] = true
	}
	var chosen []windowMeasureShape
	for _, shape := range shapes {
		if wanted[shape.String()] {
			chosen = append(chosen, shape)
			delete(wanted, shape.String())
		}
	}
	// Any other shape named as series x points / on|off, smaller ones for
	// reading a failure at a size that runs in seconds.
	for name := range wanted {
		var shape windowMeasureShape
		var state string
		base, cold := strings.CutSuffix(name, "/cold")
		if _, err := fmt.Sscanf(strings.ReplaceAll(base, "/", " "), "%dx%d %s", &shape.series, &shape.points, &state); err != nil ||
			shape.series <= 0 || shape.points <= 0 || (state != "on" && state != "off") || (cold && state != "on") {
			continue
		}
		shape.noData, shape.cold = state == "on", cold
		chosen = append(chosen, shape)
	}
	return chosen
}

// windowMeasureStrategy is the largest Query Group's strategy
// (largeGroupStrategy), with no-data detected per device when the shape asks
// for it: device_name alone, so a group is a series and the round needs no
// host's business to judge it.
func windowMeasureStrategy(t *testing.T, shape windowMeasureShape) []byte {
	t.Helper()
	raw := largeGroupStrategy(t)
	if !shape.noData && shape.points == 1 {
		return raw
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	item := document["items"].([]any)[0].(map[string]any)
	if shape.points > 1 {
		// N points a series in one Slot, the period and the aggregation
		// staying 60 s: the advanced ring comparison over the N - 1 steps
		// before the Slot's own reads them as one history dataset beside
		// the Slot's point (strategy.TraditionalHistoryGroups), which is the
		// production path that puts N points of a series into one Slot.
		item["algorithms"] = []any{map[string]any{"level": 1, "type": "AdvancedRingRatio", "unit_prefix": "",
			"config": map[string]any{"floor": 50, "ceil": nil, "floor_interval": shape.points - 1,
				"ceil_interval": nil, "fetch_type": "avg"}}}
	}
	if !shape.noData {
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	item["no_data_config"] = map[string]any{"is_enabled": true, "continuous": 1, "level": 1,
		"agg_dimension": []any{"device_name"}, "max_groups": windowMeasureMaxGroups}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// windowMeasureUQSeries is the environment that makes this test binary the
// fake UQ of a measurement, in a process of its own (TestMeasureWindowUQServer):
// its value is the series count. Generating and serving half a million
// series' answers is not alarmd's memory or CPU, so neither is in the
// process measured.
const windowMeasureUQSeries = "ALARMD_MEASURE_UQ_SERIES"

// windowMeasureUQAddress prefixes the line the fake UQ prints its address on.
const windowMeasureUQAddress = "window-measure-uq "

// windowMeasureStep is the data step of the shapes: the strategy's 60 s.
const windowMeasureStep = int64(60)

// windowMeasureAnswers answers a query for [start_time, end_time) with every
// series' points at each step in it: one for the Slot's own minute, and the
// history a multi-point shape's ring comparison reads for the rest.
type windowMeasureAnswers struct {
	series  int
	latency time.Duration
	// seriesTime is what each series an answer holds adds to the time it is
	// held (windowMeasureUQSeriesMicros).
	seriesTime time.Duration
	// mode is how the latency is held (windowMeasureUQMode): before the
	// answer's headers, then a 504 for the answer, or after the headers
	// and before the body. format is what it answers in
	// (windowMeasureUQFormat).
	mode, format string
}

// waited holds the request until hold after it arrived - what reading it
// and counting its series took inside that, not on top of it - false when
// the client gave up first: the answer is not written, and the handler holds
// no connection or goroutine past it.
func waited(request *http.Request, arrived time.Time, hold time.Duration) bool {
	left := time.Until(arrived.Add(hold))
	if left <= 0 {
		return true
	}
	select {
	case <-request.Context().Done():
		return false
	case <-time.After(left): // fixed-wait: the latency is the backend being measured, not a wait for a condition; a cancelled request ends it
		return true
	}
}

func (answers windowMeasureAnswers) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	arrived := time.Now()
	if answers.mode == windowMeasureUQMode504 {
		// UQ's own timeout: its limit spent, a 504 and no answer.
		if waited(request, arrived, answers.latency) {
			writer.WriteHeader(http.StatusGatewayTimeout)
		}
		return
	}
	var query struct {
		StartTime string `json:"start_time"`
		EndTime   string `json:"end_time"`
		QueryList []struct {
			Conditions struct {
				Fields []struct {
					Field    string   `json:"field_name"`
					Operator string   `json:"op"`
					Values   []string `json:"value"`
				} `json:"field_list"`
				Connectors []string `json:"condition_list"`
			} `json:"conditions"`
		} `json:"query_list"`
	}
	if err := json.NewDecoder(request.Body).Decode(&query); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	start, err := strconv.ParseInt(strings.TrimSpace(query.StartTime), 10, 64)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	end, err := strconv.ParseInt(strings.TrimSpace(query.EndTime), 10, 64)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	// A sliced read's conditions, as UQ reads them for VictoriaMetrics: OR
	// groups of ANDed conditions; only the req and nreq ones on the answer's
	// own dimensions are judged, the strategy's own conditions holding for
	// every series this fake answers.
	var groups [][]windowMeasureCondition
	if len(query.QueryList) > 0 {
		conditions := query.QueryList[0].Conditions
		current := []windowMeasureCondition{}
		for index, field := range conditions.Fields {
			if index > 0 && index-1 < len(conditions.Connectors) && conditions.Connectors[index-1] == "or" {
				groups, current = append(groups, current), []windowMeasureCondition{}
			}
			if (field.Operator != "req" && field.Operator != "nreq") || len(field.Values) != 1 {
				continue
			}
			pattern, err := regexp.Compile(field.Values[0])
			if err != nil {
				http.Error(writer, err.Error(), http.StatusBadRequest)
				return
			}
			current = append(current, windowMeasureCondition{dimension: field.Field, negated: field.Operator == "nreq", pattern: pattern})
		}
		groups = append(groups, current)
	}
	// The time the answer is held: the latency, and what each series it
	// holds takes.
	hold := answers.latency
	if answers.seriesTime > 0 {
		counting := time.Now()
		held := windowMeasureSeriesCount(answers.series, groups)
		hold += time.Duration(held) * answers.seriesTime
		// What the request holds and what counting it took, for the
		// client's line: the fake's own work, inside the hold.
		writer.Header().Set(windowMeasureSeriesHeader, strconv.Itoa(held))
		writer.Header().Set(windowMeasureCountingHeader, strconv.FormatInt(time.Since(counting).Microseconds(), 10))
	}
	if dimensions := windowMeasureConditionDimensions(groups); dimensions != "" {
		writer.Header().Set(windowMeasureDimensionsHeader, dimensions)
	}
	shared := answers.format != windowMeasureUQFormatJSON &&
		strings.Contains(request.Header.Get("Accept"), accessuq.SharedSchemaMediaType)
	if shared {
		writer.Header().Set("Content-Type", accessuq.SharedSchemaMediaType)
	} else {
		writer.Header().Set("Content-Type", "application/json")
	}
	if answers.mode == windowMeasureUQModeSlowBody {
		// The answer begun, its headers out, and its body held: a read the
		// deadline ends here is one whose body was being read.
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
	}
	if !waited(request, arrived, hold) {
		return
	}
	out := bufio.NewWriterSize(writer, 1<<20)
	if shared {
		_ = writeWindowMeasureSharedAnswer(out, answers.series, start, end, groups)
	} else {
		_ = writeWindowMeasureAnswer(out, answers.series, start, end, groups)
	}
	_ = out.Flush()
}

// The headers the fake UQ answers each request with, beside its answer: the
// series it held, the microseconds counting them took (the fake's own work,
// inside the hold), and the dimensions the request's slice conditions name.
const (
	windowMeasureSeriesHeader     = "X-Window-Measure-Series"
	windowMeasureCountingHeader   = "X-Window-Measure-Counting-Micros"
	windowMeasureDimensionsHeader = "X-Window-Measure-Dimensions"
)

// windowMeasureConditionDimensions is the dimensions the request's slice
// conditions name, sorted and joined, empty for a request with none.
func windowMeasureConditionDimensions(groups [][]windowMeasureCondition) string {
	seen := map[string]bool{}
	for _, group := range groups {
		for _, condition := range group {
			seen[condition.dimension] = true
		}
	}
	dimensions := make([]string, 0, len(seen))
	for dimension := range seen {
		dimensions = append(dimensions, dimension)
	}
	sort.Strings(dimensions)
	return strings.Join(dimensions, "+")
}

// windowMeasureSharedFrameSeries is how many series one frame of a shared
// schema answer carries: under the client's 256 a frame and its 256 KiB a
// line, at the dimensions' lengths and 30 points a series. An assumption:
// the frames production UQ writes are not read.
const windowMeasureSharedFrameSeries = 128

// writeWindowMeasureSharedAnswer writes writeWindowMeasureAnswer's answer in
// the shared schema format (accessuq.SharedSchemaMediaType): a version
// frame, the one schema, frames of series, and the footer with the series
// and points it carried.
func writeWindowMeasureSharedAnswer(out *bufio.Writer, series int, start, end int64, groups [][]windowMeasureCondition) error {
	first := (start + windowMeasureStep - 1) / windowMeasureStep * windowMeasureStep
	_, _ = out.WriteString("{\"seq\":0,\"v\":1}\n")
	_, _ = out.WriteString(`{"seq":1,"schemas":[{"id":0,"columns":["_time","_result"],"types":["int64","float64"],` +
		`"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"]}]}` + "\n")
	seq, written, points, inFrame := 2, 0, 0, 0
	var line []byte
	flush := func() error {
		if inFrame == 0 {
			return nil
		}
		line = append(line, "]}\n"...)
		_, err := out.Write(line)
		line, inFrame = line[:0], 0
		seq++
		return err
	}
	for index := 0; index < series; index++ {
		values := map[string]string{"bk_target_cloud_id": "0",
			"bk_target_ip": fmt.Sprintf("198.51.100.%03d", index%256), "device_name": fmt.Sprintf("device-%025d", index)}
		if !windowMeasureSeriesIn(groups, values) {
			continue
		}
		if inFrame == 0 {
			line = fmt.Appendf(line, `{"seq":%d,"series":[`, seq)
		} else {
			line = append(line, ',')
		}
		inFrame++
		written++
		line = fmt.Appendf(line, `{"s":0,"g":["0","198.51.100.%03d","device-%025d"],"r":[`, index%256, index)
		value := "12.5"
		if index%10 == 0 {
			value = "90"
		}
		for at := first; at < end; at += windowMeasureStep {
			if at > first {
				line = append(line, ',')
			}
			line = append(line, '[')
			line = strconv.AppendInt(line, at*1000, 10)
			line = append(line, ',')
			line = append(line, value...)
			line = append(line, ']')
			points++
		}
		line = append(line, "]}"...)
		if inFrame == windowMeasureSharedFrameSeries {
			if err := flush(); err != nil {
				return err
			}
		}
	}
	if err := flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, `{"seq":%d,"end":{"series":%d,"points":%d,"is_partial":false,"status":null,"trace_id":"window-measure","result_table_id":["system.cpu"]}}`+"\n",
		seq, written, points)
	return err
}

// windowMeasureCondition is one slice condition of a read.
type windowMeasureCondition struct {
	dimension string
	negated   bool
	pattern   *regexp.Regexp
}

// windowMeasureSeriesIn is whether the series with these values is in the
// read: some OR group's every condition holds.
func windowMeasureSeriesIn(groups [][]windowMeasureCondition, values map[string]string) bool {
	if len(groups) == 0 {
		return true
	}
	for _, group := range groups {
		holds := true
		for _, condition := range group {
			if condition.pattern.MatchString(values[condition.dimension]) == condition.negated {
				holds = false
				break
			}
		}
		if holds {
			return true
		}
	}
	return false
}

// windowMeasureSeriesCount is how many of series series an answer to groups
// holds (windowMeasureSeriesIn).
func windowMeasureSeriesCount(series int, groups [][]windowMeasureCondition) int {
	count := 0
	for index := 0; index < series; index++ {
		values := map[string]string{"bk_target_cloud_id": "0",
			"bk_target_ip": fmt.Sprintf("198.51.100.%03d", index%256), "device_name": fmt.Sprintf("device-%025d", index)}
		if windowMeasureSeriesIn(groups, values) {
			count++
		}
	}
	return count
}

// writeWindowMeasureAnswer writes the answer for [start, end): series series,
// the three dimensions as the largest group's carry them - a cloud id of "0"
// throughout, the first group-by dimension, as a host's is; an address of
// fourteen bytes; a device name of 32 - and a point at each step of the range.
func writeWindowMeasureAnswer(out *bufio.Writer, series int, start, end int64, groups [][]windowMeasureCondition) error {
	first := (start + windowMeasureStep - 1) / windowMeasureStep * windowMeasureStep
	_, _ = out.WriteString(`{"series":[`)
	var line []byte
	written := 0
	for index := 0; index < series; index++ {
		values := map[string]string{"bk_target_cloud_id": "0",
			"bk_target_ip": fmt.Sprintf("198.51.100.%03d", index%256), "device_name": fmt.Sprintf("device-%025d", index)}
		if !windowMeasureSeriesIn(groups, values) {
			continue
		}
		line = line[:0]
		if written > 0 {
			line = append(line, ',')
		}
		written++
		line = fmt.Appendf(line, `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],`+
			`"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","198.51.100.%03d","device-%025d"],`+
			`"values":[`, index%256, index)
		value := "12.5"
		if index%10 == 0 {
			value = "90"
		}
		for at := first; at < end; at += windowMeasureStep {
			if at > first {
				line = append(line, ',')
			}
			line = append(line, '[')
			line = strconv.AppendInt(line, at*1000, 10)
			line = append(line, ',')
			line = append(line, value...)
			line = append(line, ']')
		}
		line = append(line, "]}"...)
		if _, err := out.Write(line); err != nil {
			return err
		}
	}
	_, err := out.WriteString(`],"status":null,"trace_id":"window-measure","is_partial":false,"result_table_id":["system.cpu"]}`)
	return err
}

// TestMeasureWindowUQServer is the fake UQ of a window measurement when the
// measurement runs this binary with windowMeasureUQSeries set: it prints its
// address and serves until its standard input closes.
func TestMeasureWindowUQServer(t *testing.T) {
	raw := os.Getenv(windowMeasureUQSeries)
	if raw == "" {
		t.Skip("the fake UQ of a window measurement, which runs it in a process of its own")
	}
	series, err := strconv.Atoi(raw)
	if err != nil || series <= 0 {
		t.Fatalf("%s=%q is no series count", windowMeasureUQSeries, raw)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var latency time.Duration
	if raw := strings.TrimSpace(os.Getenv(windowMeasureUQLatency)); raw != "" {
		millis, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			t.Fatalf("%s=%q is no latency in milliseconds", windowMeasureUQLatency, raw)
		}
		latency = time.Duration(millis) * time.Millisecond
	}
	mode := strings.TrimSpace(os.Getenv(windowMeasureUQMode))
	if mode != "" && mode != windowMeasureUQMode504 && mode != windowMeasureUQModeSlowBody {
		t.Fatalf("%s=%q is no mode: %s or %s", windowMeasureUQMode, mode, windowMeasureUQMode504, windowMeasureUQModeSlowBody)
	}
	// What the process read, in the measurement's output (its standard
	// error is the test's): the latency every answer is held for, and how.
	seriesTime := time.Duration(windowMeasureSeriesMicros(t)) * time.Microsecond
	format := strings.TrimSpace(os.Getenv(windowMeasureUQFormat))
	if format == "" {
		format = windowMeasureUQFormatShared
	}
	if format != windowMeasureUQFormatShared && format != windowMeasureUQFormatJSON {
		t.Fatalf("%s=%q is no format: %s or %s", windowMeasureUQFormat, format, windowMeasureUQFormatShared, windowMeasureUQFormatJSON)
	}
	fmt.Fprintf(os.Stderr, "fake UQ latency_ms=%d mode=%q series_micros=%d format=%s\n", latency.Milliseconds(), mode,
		seriesTime.Microseconds(), format)
	server := &http.Server{Handler: windowMeasureAnswers{series: series, latency: latency, seriesTime: seriesTime, mode: mode,
		format: format}, ReadHeaderTimeout: time.Minute}
	fmt.Printf("%s%s\n", windowMeasureUQAddress, listener.Addr())
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		_ = server.Close()
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
}

// startWindowMeasureUQ runs the fake UQ in a process of its own and returns
// its URL and process id; the process ends with the test. With
// ALARMD_MEASURE_UQ_CPUS (a taskset CPU list) it runs on those CPUs, off the
// cores alarmd is measured on: started from the test it would otherwise
// inherit their mask and its answering would be in alarmd's wall time.
func startWindowMeasureUQ(t *testing.T, series int) (string, int) {
	t.Helper()
	args := []string{os.Args[0], "-test.run=^TestMeasureWindowUQServer$", "-test.count=1"}
	if cpus := strings.TrimSpace(os.Getenv("ALARMD_MEASURE_UQ_CPUS")); cpus != "" {
		args = append([]string{"taskset", "-c", cpus}, args...)
	}
	command := exec.Command(args[0], args[1:]...)
	command.Env = append(os.Environ(), fmt.Sprintf("%s=%d", windowMeasureUQSeries, series))
	command.Stderr = os.Stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = command.Wait()
	})
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if address, ok := strings.CutPrefix(scanner.Text(), windowMeasureUQAddress); ok {
			go func() { _, _ = io.Copy(io.Discard, stdout) }()
			return "http://" + address, command.Process.Pid
		}
	}
	t.Fatalf("the fake UQ ended before it said its address: %v", scanner.Err())
	return "", 0
}

// windowMeasureProcessCPU is another process's user and system CPU seconds
// so far, from /proc/<pid>/stat (fields 14 and 15, in clock ticks of 1/100 s,
// USER_HZ on Linux), or zero where there is no /proc. taskset execs the
// command it wraps, so the fake UQ keeps the pid it was started with.
func windowMeasureProcessCPU(pid int) float64 {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	// The command name, field 2, is in parentheses and may hold spaces.
	_, rest, found := strings.Cut(string(stat), ") ")
	if !found {
		return 0
	}
	fields := strings.Fields(rest)
	if len(fields) < 13 {
		return 0
	}
	user, userErr := strconv.ParseUint(fields[11], 10, 64)
	system, systemErr := strconv.ParseUint(fields[12], 10, 64)
	if userErr != nil || systemErr != nil {
		return 0
	}
	return float64(user+system) / 100
}

// countingWindowEventSink encodes every event as the output does and keeps
// none: half a million kept events would be heap the Slot does not hold.
type countingWindowEventSink struct {
	batches, events atomic.Int64
}

func (sink *countingWindowEventSink) ConfigureStandardOutput(enginekafka.StandardEventConverter) error {
	return nil
}

func (sink *countingWindowEventSink) ConfigureLegacyOutput(enginekafka.LegacyEventConverter, string, int) error {
	return nil
}

func (sink *countingWindowEventSink) ProtocolNegotiation() *enginekafka.ProtocolNegotiation {
	return nil
}

func (sink *countingWindowEventSink) WriteBatch(_ context.Context, events []contract.TriggerEventV1) error {
	for index := range events {
		if _, err := contract.EncodeTriggerEventV1(&events[index]); err != nil {
			return err
		}
	}
	sink.batches.Add(1)
	sink.events.Add(int64(len(events)))
	return nil
}

func (sink *countingWindowEventSink) Shutdown(context.Context) error { return nil }

func (sink *countingWindowEventSink) Close() error { return nil }

// windowMeasureRuntimeSample reads, in one call, the runtime's heap objects,
// the live heap its last cycle marked, and the memory it holds from the
// system less what it has returned - the nearest the runtime comes to its
// resident set.
func windowMeasureRuntimeSample(samples []metrics.Sample) (objects, live, held uint64) {
	metrics.Read(samples)
	objects, live = samples[0].Value.Uint64(), samples[1].Value.Uint64()
	held = samples[2].Value.Uint64() - samples[3].Value.Uint64()
	return objects, live, held
}

func windowMeasureRuntimeSamples() []metrics.Sample {
	return []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}, {Name: "/gc/heap/live:bytes"},
		{Name: "/memory/classes/total:bytes"}, {Name: "/memory/classes/heap/released:bytes"}}
}

// windowMeasureCPU is this process's CPU time so far - alarmd's alone, the
// fake UQ answering from a process of its own: user and system from the
// kernel, apart, and the collector's share from the runtime.
func windowMeasureCPU() (user, system, collector float64, cycles uint64) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err == nil {
		user = float64(usage.Utime.Sec) + float64(usage.Utime.Usec)/1e6
		system = float64(usage.Stime.Sec) + float64(usage.Stime.Usec)/1e6
	}
	sample := []metrics.Sample{{Name: "/cpu/classes/gc/total:cpu-seconds"}, {Name: "/gc/cycles/total:gc-cycles"}}
	metrics.Read(sample)
	if sample[0].Value.Kind() == metrics.KindFloat64 {
		collector = sample[0].Value.Float64()
	}
	if sample[1].Value.Kind() == metrics.KindUint64 {
		cycles = sample[1].Value.Uint64()
	}
	return user, system, collector, cycles
}

// windowMeasureRedisMemory is the server's used_memory and its key count.
func windowMeasureRedisMemory(ctx context.Context, client *redis.Client) (used, keys int64) {
	if info, err := client.Info(ctx, "memory").Result(); err == nil {
		for _, line := range strings.Split(info, "\n") {
			if value, ok := strings.CutPrefix(strings.TrimSpace(line), "used_memory:"); ok {
				used, _ = strconv.ParseInt(value, 10, 64)
			}
		}
	}
	keys, _ = client.DBSize(ctx).Result()
	return used, keys
}

// windowMeasureRedisEvictions is the keys the server evicted since its stats
// were last reset (INFO stats evicted_keys), -1 unread, and its maxmemory and
// maxmemory-policy: a cell whose Redis evicted keys measured a State that was
// not all there, and is void.
func windowMeasureRedisEvictions(ctx context.Context, client *redis.Client) (evicted int64, maxmemory, policy string) {
	evicted = -1
	if info, err := client.Info(ctx, "stats").Result(); err == nil {
		for _, line := range strings.Split(info, "\n") {
			if value, ok := strings.CutPrefix(strings.TrimSpace(line), "evicted_keys:"); ok {
				evicted, _ = strconv.ParseInt(value, 10, 64)
			}
		}
	}
	config := func(name string) string {
		values, err := client.ConfigGet(ctx, name).Result()
		if err != nil || len(values) < 2 {
			return "unread"
		}
		return fmt.Sprint(values[1])
	}
	return evicted, config("maxmemory"), config("maxmemory-policy")
}

// windowMeasureMemInfo is the host's MemAvailable and the swap it uses, in
// KiB, from /proc/meminfo, -1 each where there is none: a cell whose host
// swapped measured its disk, and is void.
func windowMeasureMemInfo() (available, swapUsed int64) {
	available, swapUsed = -1, -1
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return available, swapUsed
	}
	fields := map[string]int64{}
	for _, line := range strings.Split(string(raw), "\n") {
		name, rest, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		parts := strings.Fields(rest)
		if len(parts) == 0 {
			continue
		}
		value, err := strconv.ParseInt(parts[0], 10, 64)
		if err == nil {
			fields[name] = value
		}
	}
	if value, ok := fields["MemAvailable"]; ok {
		available = value
	}
	total, hasTotal := fields["SwapTotal"]
	free, hasFree := fields["SwapFree"]
	if hasTotal && hasFree {
		swapUsed = total - free
	}
	return available, swapUsed
}

// windowMeasureHostCPU is the CPU seconds every core of the host has spent
// busy so far (the first line of /proc/stat: user, nice, system, irq,
// softirq, steal, in USER_HZ of 1/100 s), 0 where there is no /proc. Over a
// round, less alarmd's, the fake UQ's and Redis's, it is what other users of
// the host took.
func windowMeasureHostCPU() float64 {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	fields := strings.Fields(line)
	if len(fields) < 9 || fields[0] != "cpu" {
		return 0
	}
	var busy uint64
	for _, index := range []int{1, 2, 3, 6, 7, 8} {
		value, err := strconv.ParseUint(fields[index], 10, 64)
		if err != nil {
			return 0
		}
		busy += value
	}
	return float64(busy) / 100
}

// windowMeasureCPUsAllowed is the CPUs a process may run on
// (Cpus_allowed_list of /proc/<pid>/status), "unread" where there is none:
// what taskset left it.
// windowMeasureRedisCPUs is the taskset CPU list Redis is put on, off the
// cores alarmd is measured on.
const windowMeasureRedisCPUs = "ALARMD_MEASURE_REDIS_CPUS"

// windowMeasureCPUsApart reads back the cores alarmd and the fake UQ are
// allowed to run on, as the kernel has them, and fails a run by cores whose
// two overlap: what a reading says of alarmd's cores holds only for cores
// nothing else of the measurement ran on. It returns them for the measured
// line.
func windowMeasureCPUsApart(t *testing.T, uqPID int) string {
	t.Helper()
	alarmd, uq := windowMeasureCPUsAllowed("self"), windowMeasureCPUsAllowed(strconv.Itoa(uqPID))
	line := fmt.Sprintf("cpus_allowed_alarmd=%s cpus_allowed_uq=%s", alarmd, uq)
	if runtime.GOOS != "linux" || os.Getenv("ALARMD_MEASURE_PROCS") == "" {
		return line
	}
	seen := map[int]string{}
	for _, party := range []struct{ name, list string }{{"alarmd", alarmd}, {"uq", uq}} {
		cores, ok := windowMeasureCPUList(party.list)
		if !ok {
			t.Fatalf("%s: the %s cores %q read back are no CPU list", line, party.name, party.list)
		}
		for _, core := range cores {
			if other, taken := seen[core]; taken {
				t.Fatalf("%s: core %d is both %s's and %s's", line, core, other, party.name)
			}
			seen[core] = party.name
		}
	}
	return line
}

// windowMeasureCPUList is the cores of a Cpus_allowed_list ("0-11,15").
func windowMeasureCPUList(list string) ([]int, bool) {
	var cores []int
	for _, part := range strings.Split(list, ",") {
		low, high, ranged := strings.Cut(part, "-")
		first, err := strconv.Atoi(low)
		if err != nil {
			return nil, false
		}
		last := first
		if ranged {
			if last, err = strconv.Atoi(high); err != nil || last < first {
				return nil, false
			}
		}
		for core := first; core <= last; core++ {
			cores = append(cores, core)
		}
	}
	return cores, true
}

func windowMeasureCPUsAllowed(pid string) string {
	raw, err := os.ReadFile("/proc/" + pid + "/status")
	if err != nil {
		return "unread"
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if value, ok := strings.CutPrefix(line, "Cpus_allowed_list:"); ok {
			return strings.TrimSpace(value)
		}
	}
	return "unread"
}

// countingWindowTransport counts the bytes of every answer body the query
// client reads, the UQ answers' bytes the way access/uq counts a completion's
// (completion.Stats.Bytes), over all of them. It also keeps each request as
// it was answered (requests): the time to its answer's headers - the fake
// UQ's latency and its own answering, the latency held before any header is
// written - or that it failed, and what it asked for less its time range,
// the slice it read.
type countingWindowTransport struct {
	next  http.RoundTripper
	bytes atomic.Int64
	// mu guards requests, in the order they were answered.
	mu       sync.Mutex
	requests []windowMeasureRequest
}

// windowMeasureRequest is one query the fake UQ was sent: when it went
// out (sent, the fake UQ's arrival but for loopback), when its answer's
// headers came back (headersAt; headers is the time between), and when its
// body was read to its end or closed (bodyDone, zero for a request that
// failed), all on the query client's side.
type windowMeasureRequest struct {
	headers                   time.Duration
	sent, headersAt, bodyDone time.Time
	failed                    bool
	// canceled is a request alarmd took back (a read ahead yielded, a slice
	// let go), failed is any other failure, with its error (reason); held
	// and counting the series the fake UQ held and the time counting them
	// took, dimensions the dimensions its slice conditions name (the fake's
	// headers).
	canceled   bool
	reason     string
	held       int
	counting   time.Duration
	dimensions string
	// deadlineLeft is the time its query had left when it was sent, from
	// the deadline the query client put on it; -1 for none. shared is an
	// answer in the shared schema format (its Content-Type).
	deadlineLeft time.Duration
	shared       bool
	// slice is the digest of the request's body less start_time and
	// end_time: the same for the same slice of any Slot.
	slice string
}

func (transport *countingWindowTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	slice := ""
	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		// A copy goes on, the request itself left as it was sent.
		req = req.Clone(req.Context())
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
		slice = windowMeasureSliceDigest(body)
	}
	started := time.Now()
	deadlineLeft := time.Duration(-1)
	if deadline, bounded := req.Context().Deadline(); bounded {
		deadlineLeft = deadline.Sub(started)
	}
	resp, err := transport.next.RoundTrip(req)
	headers := time.Since(started)
	transport.mu.Lock()
	shared := err == nil && strings.HasPrefix(resp.Header.Get("Content-Type"), accessuq.SharedSchemaMediaType)
	index := len(transport.requests)
	recorded := windowMeasureRequest{headers: headers, sent: started, headersAt: started.Add(headers),
		failed: err != nil && !errors.Is(err, context.Canceled), canceled: err != nil && errors.Is(err, context.Canceled),
		deadlineLeft: deadlineLeft, shared: shared, slice: slice}
	if err != nil {
		recorded.reason = err.Error()
	} else {
		recorded.held, _ = strconv.Atoi(resp.Header.Get(windowMeasureSeriesHeader))
		counting, _ := strconv.ParseInt(resp.Header.Get(windowMeasureCountingHeader), 10, 64)
		recorded.counting = time.Duration(counting) * time.Microsecond
		recorded.dimensions = resp.Header.Get(windowMeasureDimensionsHeader)
	}
	transport.requests = append(transport.requests, recorded)
	transport.mu.Unlock()
	if err == nil && resp.Body != nil {
		resp.Body = &countingWindowBody{ReadCloser: resp.Body, bytes: &transport.bytes, done: func() {
			transport.mu.Lock()
			defer transport.mu.Unlock()
			if transport.requests[index].bodyDone.IsZero() {
				transport.requests[index].bodyDone = time.Now()
			}
		}}
	}
	return resp, err
}

// answered is how many requests were answered or failed so far.
func (transport *countingWindowTransport) answered() int {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return len(transport.requests)
}

// since is the requests answered or failed after the first from.
func (transport *countingWindowTransport) since(from int) []windowMeasureRequest {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	return append([]windowMeasureRequest(nil), transport.requests[from:]...)
}

// windowMeasureSliceDigest is the first 16 hex digits of the SHA-256 of a
// query's body with its time range taken out, "unparsed" for a body that is
// no JSON object.
func windowMeasureSliceDigest(body []byte) string {
	var query map[string]any
	if err := json.Unmarshal(body, &query); err != nil {
		return "unparsed"
	}
	delete(query, "start_time")
	delete(query, "end_time")
	canonical, err := json.Marshal(query)
	if err != nil {
		return "unparsed"
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:8])
}

// windowMeasureShapesLine is a Slot's shape outcomes, every one, as
// observability.InputSliceShapeOutcomes names them.
func windowMeasureShapesLine(usage observability.SlotBudgetUsageFacts) string {
	counts := []uint64{usage.InputSliceShapeStarted, usage.InputSliceShapeKept, usage.InputSliceShapeMerged,
		usage.InputSliceShapeVoidedRevision, usage.InputSliceShapeVoidedDimensions, usage.InputSliceShapeVoidedMaxLeaves,
		usage.InputSliceShapeRefusedNotALeaf, usage.InputSliceShapeRefusedNotAPartition, usage.InputSliceShapeRefusedMaxLeaves,
		usage.InputSliceShapeEvictedIdle}
	parts := make([]string, 0, len(counts))
	for index, outcome := range observability.InputSliceShapeOutcomes {
		parts = append(parts, fmt.Sprintf("%s:%d", outcome, counts[index]))
	}
	return strings.Join(parts, ",")
}

// windowMeasureRequestsLine is the measured Slot's requests: how many, how
// many failed, how many were answered in the shared schema format, the
// least and most time to their answers' headers in
// milliseconds over those answered, the most time any had left to its
// deadline when sent (the first's), and the slices they read, sorted.
//
// Beside them the query phase (windowMeasureQueryPath): uq_lead_ms from the
// Slot's start to its first request, the span, busy and idle, the longest
// request, the Slot's time with no request in flight (uq_non_query_ms, its
// wall clock less busy), and whether the query phase from the Slot's start -
// lead and span - fits a query window of 35 s and of 45 s.
func windowMeasureRequestsLine(requests []windowMeasureRequest, started time.Time, took time.Duration) string {
	failed, canceled, shared := 0, 0, 0
	least, most, deadlineLeft := int64(-1), int64(-1), int64(-1)
	slices := make([]string, 0, len(requests))
	for _, request := range requests {
		slices = append(slices, request.slice)
		deadlineLeft = max(deadlineLeft, request.deadlineLeft.Milliseconds())
		if request.shared {
			shared++
		}
		if request.canceled {
			canceled++
			continue
		}
		if request.failed {
			failed++
			continue
		}
		millis := request.headers.Milliseconds()
		if least < 0 || millis < least {
			least = millis
		}
		most = max(most, millis)
	}
	sort.Strings(slices)
	span, busy, idle := windowMeasureQueryPath(requests)
	lead, longest := time.Duration(0), time.Duration(0)
	for index, request := range requests {
		if index == 0 || request.sent.Before(started.Add(lead)) {
			lead = request.sent.Sub(started)
		}
		end := request.bodyDone
		if end.IsZero() {
			end = request.headersAt
		}
		longest = max(longest, end.Sub(request.sent))
	}
	phase := lead + span
	alone := windowMeasureFirstSliceAlone(requests, span)
	// The slices in flight on average over the busy time: every request's
	// time in flight over the time some request was.
	var inFlight time.Duration
	for _, request := range requests {
		end := request.bodyDone
		if end.IsZero() {
			end = request.headersAt
		}
		inFlight += end.Sub(request.sent)
	}
	inFlightAverage := 0.0
	if busy > 0 {
		inFlightAverage = float64(inFlight) / float64(busy)
	}
	return fmt.Sprintf("uq_requests=%d uq_requests_failed=%d uq_requests_canceled=%d uq_shared_answers=%d uq_request_ms_min=%d uq_request_ms_max=%d "+
		"uq_deadline_left_ms_first=%d uq_lead_ms=%d uq_query_span_ms=%d uq_query_busy_ms=%d uq_query_idle_ms=%d "+
		"uq_longest_request_ms=%d uq_non_query_ms=%d uq_in_flight_avg=%.2f first_slice_alone_ms=%d query_within_35s=%t query_within_45s=%t uq_slices=[%s]",
		len(requests), failed, canceled, shared, least, most, deadlineLeft, lead.Milliseconds(), span.Milliseconds(), busy.Milliseconds(),
		idle.Milliseconds(), longest.Milliseconds(), (took - busy).Milliseconds(), inFlightAverage, alone.Milliseconds(),
		phase <= 35*time.Second, phase <= 45*time.Second, strings.Join(slices, ","))
}

// countingWindowBody counts an answer's bytes and says when it was read to
// its end or closed (done, the first of the two).
type countingWindowBody struct {
	io.ReadCloser
	bytes *atomic.Int64
	done  func()
}

func (body *countingWindowBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	body.bytes.Add(int64(n))
	if err != nil {
		body.done()
	}
	return n, err
}

func (body *countingWindowBody) Close() error {
	body.done()
	return body.ReadCloser.Close()
}

// windowMeasureNeighbors samples, every 5 s of the measured Slot, the time
// alarmd's cores (0 to procs-1, as the host pins it with taskset) were busy
// less this process's own CPU - the fake UQ and Redis are pinned elsewhere -
// which is what other processes took on them (/proc/stat by core,
// /proc/self/stat; Linux only, none elsewhere).
type windowMeasureNeighbors struct {
	cores   int
	done    chan struct{}
	stopped chan struct{}
	// windows is each 5 s window's cores' worth of others' time.
	windows []float64
	total   time.Duration
}

// windowMeasureTicks is the kernel's clock ticks a second (CLK_TCK, 100 on
// the measurement host, read back there).
const windowMeasureTicks = 100

func startWindowMeasureNeighbors(cores int) *windowMeasureNeighbors {
	sampler := &windowMeasureNeighbors{cores: cores, done: make(chan struct{}), stopped: make(chan struct{})}
	go func() {
		defer close(sampler.stopped)
		busy, own, at := windowMeasureCoresBusy(cores), windowMeasureOwnTicks(), time.Now()
		ticker := time.NewTicker(5 * time.Second) // fixed-wait: the sampling period of a reading, not a wait for a condition
		defer ticker.Stop()
		sample := func() {
			nowBusy, nowOwn, now := windowMeasureCoresBusy(cores), windowMeasureOwnTicks(), time.Now()
			if busy >= 0 && nowBusy >= 0 && own >= 0 && nowOwn >= 0 && now.After(at) {
				others := float64((nowBusy-busy)-(nowOwn-own)) / windowMeasureTicks
				sampler.windows = append(sampler.windows, max(others, 0)/now.Sub(at).Seconds())
				sampler.total += now.Sub(at)
			}
			busy, own, at = nowBusy, nowOwn, now
		}
		for {
			select {
			case <-sampler.done:
				sample()
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	return sampler
}

// stop ends the sampling and says what it read: the cores' worth of others'
// time on alarmd's cores on average and in the busiest window, and whether
// that disturbs the cell - more than 0.4 cores on average, or 2 cores in
// any window (the measurement plan's thresholds for 8 cores).
func (sampler *windowMeasureNeighbors) stop() string {
	close(sampler.done)
	<-sampler.stopped
	if len(sampler.windows) == 0 {
		return "neighbors_on_alarmd_cores=unread"
	}
	sum, most := 0.0, 0.0
	for _, window := range sampler.windows {
		sum, most = sum+window, max(most, window)
	}
	average := sum / float64(len(sampler.windows))
	return fmt.Sprintf("neighbors_on_alarmd_cores_avg=%.2f neighbors_on_alarmd_cores_max_5s=%.2f neighbors_windows=%d disturbed=%t",
		average, most, len(sampler.windows), average > 0.4 || most > 2)
}

// windowMeasureCoresBusy is the ticks cores 0 to cores-1 were busy (user,
// nice, system, irq, softirq, steal), -1 unread.
func windowMeasureCoresBusy(cores int) int64 {
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return -1
	}
	var busy int64
	read := 0
	for _, line := range strings.Split(string(stat), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 || !strings.HasPrefix(fields[0], "cpu") || fields[0] == "cpu" {
			continue
		}
		core, err := strconv.Atoi(strings.TrimPrefix(fields[0], "cpu"))
		if err != nil || core >= cores {
			continue
		}
		for _, index := range []int{1, 2, 3, 6, 7, 8} {
			value, err := strconv.ParseInt(fields[index], 10, 64)
			if err != nil {
				return -1
			}
			busy += value
		}
		read++
	}
	if read != cores {
		return -1
	}
	return busy
}

// windowMeasureOwnTicks is this process's user and system ticks, -1 unread.
func windowMeasureOwnTicks() int64 {
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return -1
	}
	// The fields after the command, which is in parentheses and may hold
	// spaces: utime and stime are the 12th and 13th.
	text := string(stat)
	closing := strings.LastIndexByte(text, ')')
	if closing < 0 {
		return -1
	}
	fields := strings.Fields(text[closing+1:])
	if len(fields) < 13 {
		return -1
	}
	user, err1 := strconv.ParseInt(fields[11], 10, 64)
	system, err2 := strconv.ParseInt(fields[12], 10, 64)
	if err1 != nil || err2 != nil {
		return -1
	}
	return user + system
}

// windowMeasureRequestLines is the measured Slot's requests one a line, in
// the order sent: when it went out from the Slot's start, the dimensions its
// slice conditions name (none: no condition), the series the fake UQ held,
// the time the model holds that many (windowMeasureUQSeriesMicros each) and
// the fake's own time counting them, to its headers and to its body read,
// and how it ended: answered, canceled (alarmd took it back) or failed and
// why.
func windowMeasureRequestLines(requests []windowMeasureRequest, started time.Time, seriesMicros int64) []string {
	ordered := append([]windowMeasureRequest(nil), requests...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].sent.Before(ordered[j].sent) })
	lines := make([]string, 0, len(ordered))
	for index, request := range ordered {
		ended := "answered"
		switch {
		case request.canceled:
			ended = "canceled"
		case request.failed:
			ended = "failed:" + strings.ReplaceAll(request.reason, " ", "_")
		}
		body := int64(-1)
		if !request.bodyDone.IsZero() {
			body = request.bodyDone.Sub(request.sent).Milliseconds()
		}
		dimensions := request.dimensions
		if dimensions == "" {
			dimensions = "none"
		}
		lines = append(lines, fmt.Sprintf("uq_request=%d sent_ms=%d slice=%s dimensions=%s series=%d model_ms=%d counting_ms=%d "+
			"headers_ms=%d body_ms=%d ended=%s", index, request.sent.Sub(started).Milliseconds(), request.slice, dimensions,
			request.held, int64(request.held)*seriesMicros/1000, request.counting.Milliseconds(),
			request.headers.Milliseconds(), body, ended))
	}
	return lines
}

// windowMeasureQueryPath is the measured Slot's query phase from its
// requests: span, from the first request sent to the last body read (its
// headers for a request that failed); busy, the time some request was in
// flight; idle, the span less busy - the time between slices no request was
// out, which is the Slot's own. With the span beside the Slot's wall clock
// and its core-seconds, a round over its minute reads as UQ's time or its
// own: the span is what the query window has to hold.
func windowMeasureQueryPath(requests []windowMeasureRequest) (span, busy, idle time.Duration) {
	type interval struct{ from, to time.Time }
	intervals := make([]interval, 0, len(requests))
	for _, request := range requests {
		end := request.bodyDone
		if end.IsZero() {
			end = request.headersAt
		}
		intervals = append(intervals, interval{request.sent, end})
	}
	if len(intervals) == 0 {
		return 0, 0, 0
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].from.Before(intervals[j].from) })
	first, last := intervals[0].from, intervals[0].to
	covered, at, until := time.Duration(0), intervals[0].from, intervals[0].to
	for _, next := range intervals[1:] {
		if next.to.After(last) {
			last = next.to
		}
		if next.from.After(until) {
			covered += until.Sub(at)
			at, until = next.from, next.to
			continue
		}
		if next.to.After(until) {
			until = next.to
		}
	}
	covered += until.Sub(at)
	return last.Sub(first), covered, last.Sub(first) - covered
}

// windowMeasureFirstSliceAlone is how long the first slice asked was the
// only one out: from its first request to the first request of another
// slice, the whole span when no other slice was asked.
func windowMeasureFirstSliceAlone(requests []windowMeasureRequest, span time.Duration) time.Duration {
	var first windowMeasureRequest
	for index, request := range requests {
		if index == 0 || request.sent.Before(first.sent) {
			first = request
		}
	}
	var other time.Time
	for _, request := range requests {
		if request.slice != first.slice && (other.IsZero() || request.sent.Before(other)) {
			other = request.sent
		}
	}
	if other.IsZero() {
		return span
	}
	return other.Sub(first.sent)
}

// windowMeasureProcs is the cores the measurement runs on: GOMAXPROCS, from
// ALARMD_MEASURE_PROCS, 4 (a 4c replica) without it. The host pins the
// process to as many CPUs (taskset); the line says both.
func windowMeasureProcs(t *testing.T) int {
	raw := strings.TrimSpace(os.Getenv("ALARMD_MEASURE_PROCS"))
	if raw == "" {
		return 4
	}
	procs, err := strconv.Atoi(raw)
	if err != nil || procs < 1 {
		t.Fatalf("ALARMD_MEASURE_PROCS=%q is not a count of cores", raw)
	}
	return procs
}

// windowMeasureResetPeakRSS clears the kernel's peak resident set for this
// process, so the next read is the measured round's own and not the warm-up
// rounds' (Linux: 5 written to /proc/self/clear_refs resets VmHWM). It reports
// whether the platform has the reset; without it the peak read after the
// round is the process's lifetime peak and is said so on the line.
func windowMeasureResetPeakRSS() bool {
	return os.WriteFile("/proc/self/clear_refs", []byte("5"), 0) == nil
}

// windowMeasureRoundPeakRSS is VmHWM in bytes, the peak resident set since
// the last reset, or zero where /proc/self/status has none.
func windowMeasureRoundPeakRSS() uint64 { return windowMeasureStatusBytes("VmHWM:") }

// windowMeasureIdleRSS is VmRSS in bytes: read with the bundle started and
// no Slot run, the idle baseline a series' memory is counted above.
func windowMeasureIdleRSS() uint64 { return windowMeasureStatusBytes("VmRSS:") }

// windowMeasureStatusBytes is the kB field of /proc/self/status named by
// field, in bytes, zero where it has none.
func windowMeasureStatusBytes(field string) uint64 {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(status), "\n") {
		if value, found := strings.CutPrefix(line, field); found {
			kib, err := strconv.ParseUint(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "kB")), 10, 64)
			if err != nil {
				return 0
			}
			return kib * 1024
		}
	}
	return 0
}

// windowMeasureMaxRSS is the process's peak resident set over its lifetime,
// in bytes: the kernel reports bytes on darwin and KiB elsewhere.
func windowMeasureMaxRSS() uint64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0
	}
	if runtime.GOOS == "darwin" {
		return uint64(usage.Maxrss)
	}
	return uint64(usage.Maxrss) * 1024
}

// windowMeasureMetricSum sums the samples of one family whose labels hold
// every pair in labels.
func windowMeasureMetricSum(recorder *metric.Recorder, name string, labels map[string]string) float64 {
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		return math.NaN()
	}
	var sum float64
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, series := range family.GetMetric() {
			matched := 0
			for _, label := range series.GetLabel() {
				if want, ok := labels[label.GetName()]; ok && want == label.GetValue() {
					matched++
				}
			}
			if matched != len(labels) {
				continue
			}
			switch {
			case series.GetCounter() != nil:
				sum += series.GetCounter().GetValue()
			case series.GetGauge() != nil:
				sum += series.GetGauge().GetValue()
			}
		}
	}
	return sum
}

// windowMeasureHistogramSums is one histogram family's sums, by the value of
// one of its labels, the others added together.
func windowMeasureHistogramSums(recorder *metric.Recorder, name, label string) map[string]float64 {
	sums := map[string]float64{}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		return sums
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, series := range family.GetMetric() {
			if series.GetHistogram() == nil {
				continue
			}
			value := ""
			for _, pair := range series.GetLabel() {
				if pair.GetName() == label {
					value = pair.GetValue()
				}
			}
			sums[value] += series.GetHistogram().GetSampleSum()
		}
	}
	return sums
}

// windowMeasureSecondsLine is a set of durations, largest first.
func windowMeasureSecondsLine(seconds map[string]float64) string {
	names := make([]string, 0, len(seconds))
	for name := range seconds {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return seconds[names[i]] > seconds[names[j]] })
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s:%.2fs", name, seconds[name]))
	}
	return strings.Join(parts, ",")
}

// windowMeasureMemoryClasses is what the runtime holds from the system, by
// class: the heap's objects, its unused and free spans and what it gave
// back, the goroutine stacks, the runtime's own metadata and the rest - the
// split the resident set beyond the memory limit is read by (HeapSys is
// objects, unused, free and released together).
func windowMeasureMemoryClasses() string {
	names := []string{"total", "heap/objects", "heap/unused", "heap/free", "heap/released", "heap/stacks",
		"os-stacks", "metadata/mspan/inuse", "metadata/mspan/free", "metadata/mcache/inuse", "metadata/mcache/free",
		"metadata/other", "profiling/buckets", "other"}
	samples := make([]metrics.Sample, len(names))
	for index, name := range names {
		samples[index].Name = "/memory/classes/" + name + ":bytes"
	}
	metrics.Read(samples)
	parts := make([]string, 0, len(names))
	for index, name := range names {
		if samples[index].Value.Kind() == metrics.KindUint64 {
			parts = append(parts, fmt.Sprintf("%s:%d", name, samples[index].Value.Uint64()))
		}
	}
	return strings.Join(parts, ",")
}

// windowMeasureRedisCommands is the server's calls by command since its
// counters were last reset, and its CPU seconds so far.
func windowMeasureRedisCommands(ctx context.Context, client *redis.Client) (map[string]int64, float64) {
	calls := map[string]int64{}
	if info, err := client.Info(ctx, "commandstats").Result(); err == nil {
		for _, line := range strings.Split(info, "\n") {
			line = strings.TrimSpace(line)
			name, rest, found := strings.Cut(strings.TrimPrefix(line, "cmdstat_"), ":")
			if !found || !strings.HasPrefix(line, "cmdstat_") {
				continue
			}
			for _, field := range strings.Split(rest, ",") {
				if value, ok := strings.CutPrefix(field, "calls="); ok {
					if count, err := strconv.ParseInt(value, 10, 64); err == nil {
						calls[name] = count
					}
				}
			}
		}
	}
	var cpu float64
	if info, err := client.Info(ctx, "cpu").Result(); err == nil {
		for _, line := range strings.Split(info, "\n") {
			line = strings.TrimSpace(line)
			for _, key := range []string{"used_cpu_sys:", "used_cpu_user:"} {
				if value, ok := strings.CutPrefix(line, key); ok {
					if seconds, err := strconv.ParseFloat(value, 64); err == nil {
						cpu += seconds
					}
				}
			}
		}
	}
	return calls, cpu
}

func windowMeasureCommandLine(calls map[string]int64) string {
	names := make([]string, 0, len(calls))
	for name, count := range calls {
		if count > 0 && name != "info" && name != "config|resetstat" && name != "config" {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(left, right int) bool { return calls[names[left]] > calls[names[right]] })
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s:%d", name, calls[name]))
	}
	return strings.Join(parts, ",")
}

// windowMeasureCapacity sizes the configuration for the measured replica the way
// Load does (config.Config.WithContainerCapacity reading the pod limit), and
// checks it came out as config.DeriveCoordinator says for that container: a
// process that read its capacity before the limit was set measures another
// replica, and is refused rather than measured.
func windowMeasureCapacity(t *testing.T, cfg config.Config) config.Config {
	t.Helper()
	inputs := config.DetectCapacityInputs()
	if limit := windowMeasureLimit(t); inputs.MemoryLimitBytes != limit {
		t.Fatalf("the process read a memory limit of %d (%s) before the measurement set %d; run it alone",
			inputs.MemoryLimitBytes, inputs.MemorySource, limit)
	}
	cfg = cfg.WithContainerCapacity()
	want := config.DeriveCoordinator(inputs, uint64(cfg.Limits.Store.MaxKeysPerBatch),
		uint64(cfg.Limits.Store.MaxKeysPerBatch)*execution.StateApplyMaxChunks)
	if cfg.PhaseTwo.Coordinator != want {
		t.Fatalf("coordinator %+v, want the derivation for the limit %+v", cfg.PhaseTwo.Coordinator, want)
	}
	// What the cell is sized at, for the preflight to read against its
	// limit: the share and the soft limit move with the memory.
	t.Logf("capacity limit_bytes=%d max_retained_bytes=%d go_memory_limit_bytes=%d",
		inputs.MemoryLimitBytes, cfg.PhaseTwo.Coordinator.MaxRetainedBytes, config.DeriveGoRuntime(inputs).MemoryLimitBytes)
	return cfg
}

// windowMeasureResult is what one shape's measured Slot came to.
type windowMeasureResult struct {
	completed                       bool
	result, reason, cause           string
	usage                           observability.SlotBudgetUsageFacts
	timing                          observability.SlotTimingFacts
	took                            time.Duration
	cpu, gcCPU, redisCPU            float64
	gcCycles                        uint64
	fakeUQCPU                       float64
	uqBytes                         int64
	redisSent, redisReceived        float64
	redisUsedBefore, redisUsedAfter int64
	redisKeysBefore, redisKeysAfter int64
	cpuUser, cpuSystem              float64
	roundPeakRSS                    uint64
	roundPeakReset                  bool
	objectsPeak, livePeak, heldPeak uint64
	objectsBefore, liveBefore       uint64
	maxRSSBefore, maxRSSAfter       uint64
	reservedPeak                    uint64
	rejected                        float64
	refusals                        map[string]int
	// noDataMemoryRefused is the Plans whose no-data memory a write was
	// refused for, over every Slot of the shape, warm-ups included: the
	// first write is the first Slot's.
	noDataMemoryRefused int
	// stagesAhead, stageAheadDiffers and groupsStagedAhead are how the
	// shape's no-data first writes staged as their windows landed ended, over
	// every Slot of the shape: a cold shape's first write is staged.
	stagesAhead                [4]uint64
	stageAheadDiffers          [3]uint64
	groupsStagedAhead          uint64
	batches, events            int64
	keptRuns, outside          uint64
	witnessItems, witnessBytes float64
	commands                   map[string]int64
	// redisCallers is the Redis round trips' wall time by the job that
	// made them, summed over calls that may overlap.
	redisCallers map[string]float64
	// classesAtPeak is the runtime's memory classes at the moment it held
	// the most from the system, and classesAfter at the Slot's end.
	classesAtPeak, classesAfter string
	runErr                      error
	// warmups are the earlier Slots' completions, and the errors the first
	// few each part of the run raised.
	warmups, warmupErrors, errors []string
	// requests is the measured Slot's queries (countingWindowTransport),
	// and started when the measured Slot was run.
	requests []windowMeasureRequest
	started  time.Time
	// neighbors is what ran on alarmd's cores beside it in the measured Slot
	// (windowMeasureNeighbors.stop).
	neighbors string
	// idleRSS is the resident set with the bundle started and no Slot run
	// (windowMeasureIdleRSS).
	idleRSS uint64
	// host is what the round's host and Redis said beside the Slot's own
	// readings (windowMeasureHostLine).
	host string
}

// TestMeasureWindowedSlotsOnAGridReplica runs three Slots of each shape a
// minute apart and measures the third, the State warm from the first two:
// whether it succeeded and with what, the share's refusals, the round's wall
// time and the process's CPU time over it, the peaks of the heap objects, of
// the live heap the collector marked and of the memory the runtime holds,
// sampled every two milliseconds, the process's lifetime peak resident set
// before and after, the most the pool held, the windows, the event batches,
// the record-id counters, the witness cache and the Redis commands by kind.
// One line a shape and its plan 5.2 reading; a shape whose measured Slot
// did not complete, or did not succeed, fails. ALARMD_MEASURE_PROCS sets the
// cores (4 without it), and ALARMD_MEASURE_WINDOW_BOUND_PERCENT, in the
// alarmd_measure build only, bounds the windows at that percent of the share.
func TestMeasureWindowedSlotsOnAGridReplica(t *testing.T) {
	if os.Getenv("ALARMD_MEASURE_WINDOWS") == "" {
		t.Skip("a measurement: set ALARMD_MEASURE_WINDOWS=1")
	}
	t.Setenv(config.MemoryLimitEnvironment, strconv.FormatUint(windowMeasureLimit(t), 10))
	// The collector's budget and the cores of an Nc/MGi replica of the grid, as startup
	// installs them (applyPhaseTwoGoRuntime), given back after.
	if runtime.GOOS == "linux" && os.Getenv("ALARMD_MEASURE_PROCS") != "" &&
		(os.Getenv("ALARMD_MEASURE_UQ_CPUS") == "" || os.Getenv(windowMeasureRedisCPUs) == "") {
		t.Fatal("a run by cores needs ALARMD_MEASURE_UQ_CPUS and " + windowMeasureRedisCPUs + ": the fake UQ and Redis would answer on the measured cores")
	}
	previousProcs := runtime.GOMAXPROCS(windowMeasureProcs(t))
	inputs := config.DetectCapacityInputs()
	previousGC := debug.SetGCPercent(100)
	debug.SetGCPercent(previousGC)
	previousLimit := debug.SetMemoryLimit(-1)
	derived := config.DeriveGoRuntime(inputs)
	applyPhaseTwoGoRuntime(derived, runtimeGoBudgetSetters())
	windowBound := strings.TrimSpace(os.Getenv("ALARMD_MEASURE_WINDOW_BOUND_PERCENT"))
	if windowBound != "" {
		percent, err := strconv.ParseUint(windowBound, 10, 32)
		if err != nil || percent < 1 || percent > 99 {
			t.Fatalf("ALARMD_MEASURE_WINDOW_BOUND_PERCENT=%q is not a percent between 1 and 99", windowBound)
		}
		previous, ok := windowMeasureSetWindowBound(uint32(percent))
		if !ok {
			t.Fatal("ALARMD_MEASURE_WINDOW_BOUND_PERCENT needs the alarmd_measure build (-tags alarmd_measure)")
		}
		defer windowMeasureSetWindowBound(previous)
	} else {
		windowBound = "100"
	}
	if raw := strings.TrimSpace(os.Getenv("ALARMD_MEASURE_SLICE_READ_WIDTH")); raw != "" {
		width, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || width < 1 {
			t.Fatalf("ALARMD_MEASURE_SLICE_READ_WIDTH=%q is not a width of one or more", raw)
		}
		previous, ok := windowMeasureSetSliceReadWidth(int32(width))
		if !ok {
			t.Fatal("ALARMD_MEASURE_SLICE_READ_WIDTH needs the alarmd_measure build (-tags alarmd_measure)")
		}
		defer windowMeasureSetSliceReadWidth(previous)
	}
	readAhead := "on"
	if os.Getenv("ALARMD_MEASURE_SLICE_READ_AHEAD") == "off" {
		previous, ok := windowMeasureSetSliceReadAheadOff(true)
		if !ok {
			t.Fatal("ALARMD_MEASURE_SLICE_READ_AHEAD=off needs the alarmd_measure build (-tags alarmd_measure)")
		}
		defer windowMeasureSetSliceReadAheadOff(previous)
		readAhead = "off"
	}
	defer func() {
		runtime.GOMAXPROCS(previousProcs)
		debug.SetGCPercent(previousGC)
		debug.SetMemoryLimit(previousLimit)
	}()
	shapes := windowMeasureShapes()
	if len(shapes) == 0 {
		t.Fatalf("ALARMD_MEASURE_WINDOWS_SHAPE=%q names no shape", os.Getenv("ALARMD_MEASURE_WINDOWS_SHAPE"))
	}
	for _, shape := range shapes {
		t.Run(strings.ReplaceAll(shape.String(), "/", "_"), func(t *testing.T) {
			measured := measureWindowedShape(t, shape)
			t.Logf("shape=%s procs=%d window_bound_percent=%s slice_read_ahead=%s %s", shape, runtime.GOMAXPROCS(0), windowBound,
				readAhead, measured.line(shape))
			t.Logf("shape=%s warmups=%v warmup_errors=%q errors=%q", shape, measured.warmups, measured.warmupErrors, measured.errors)
			for _, line := range windowMeasureRequestLines(measured.requests, measured.started, windowMeasureSeriesMicros(t)) {
				t.Logf("shape=%s %s", shape, line)
			}
			if !measured.completed {
				t.Errorf("shape %s: the measured Slot did not complete (%v)", shape, measured.runErr)
			}
			// Plan 5.2, read off the line: success with no share refusal,
			// the round within the 60 s period and within the 40 s margin,
			// the most the runtime held within the soft limit and the
			// round's peak resident set within 3 GiB (3/4 of the limit).
			// Each is said; a Slot that did not succeed fails the shape.
			// The round's own peak where the kernel can reset it; elsewhere
			// the lifetime peak, which the warm-up rounds may have set.
			peak := measured.maxRSSAfter
			if measured.roundPeakReset && measured.roundPeakRSS > 0 {
				peak = measured.roundPeakRSS
			}
			// Three quarters of the limit (3 GiB of 4 GiB): the memory criterion
			// of every cell of the grid.
			rss := peak <= windowMeasureLimit(t)/4*3
			held := measured.heldPeak <= uint64(derived.MemoryLimitBytes)
			// The live heap the collector marked over the most the pool held:
			// what the Slot keeps that no share charges, beside the
			// process's fixed resident part.
			uncharged := int64(measured.livePeak) - int64(measured.reservedPeak)
			// A no-data memory not written for the share is a failure at this
			// scale too: the round succeeded, and the next one judges
			// absence without it.
			cores := 0.0
			if measured.took > 0 {
				cores = measured.cpu / measured.took.Seconds()
			}
			t.Logf("shape=%s criterion success=%t share_refusals=%d round_within_60s=%t round_within_40s=%t "+
				"runtime_held_within_soft_limit=%t (soft limit %d) round_peak_rss_within_three_quarters=%t "+
				"procs=%d effective_cores=%.2f live_minus_reserved=%d gc_cpu=%.2fs gc_cycles=%d",
				shape, measured.result == string(observability.ResultSuccess) && measured.noDataMemoryRefused == 0,
				len(measured.refusals)+measured.noDataMemoryRefused, measured.took <= 60*time.Second,
				measured.took <= 40*time.Second, held, derived.MemoryLimitBytes, rss,
				runtime.GOMAXPROCS(0), cores, uncharged, measured.gcCPU, measured.gcCycles)
			t.Logf("shape=%s no_data_stage_ahead carried=%d differs=%d discarded=%d stopped=%d differs_by=%v groups_staged=%d",
				shape, measured.stagesAhead[0], measured.stagesAhead[1], measured.stagesAhead[2], measured.stagesAhead[3],
				measured.stageAheadDiffers, measured.groupsStagedAhead)
			if shape.cold && (measured.stagesAhead[0] == 0 || measured.stagesAhead[1] != 0) {
				// The production bundle's first write of a cold shape is
				// staged as its windows land and carried by its statement.
				t.Errorf("shape %s: the first write staged ahead ended %v (differs %v), want it carried and none written whole",
					shape, measured.stagesAhead, measured.stageAheadDiffers)
			}
			if measured.completed && measured.result != string(observability.ResultSuccess) {
				t.Errorf("shape %s: the measured Slot ended %s/%s", shape, measured.result, measured.reason)
			}
		})
		runtime.GC()
		debug.FreeOSMemory()
	}
}

func (measured windowMeasureResult) line(shape windowMeasureShape) string {
	usage := measured.usage
	refusals := make([]string, 0, len(measured.refusals))
	for reason, count := range measured.refusals {
		refusals = append(refusals, fmt.Sprintf("%s:%d", reason, count))
	}
	sort.Strings(refusals)
	runErr := ""
	if measured.runErr != nil {
		runErr = measured.runErr.Error()
	}
	return fmt.Sprintf("goarch=%s goroot=%s series=%d points=%d no_data=%t result=%s reason=%s cause=%s run_err=%q "+
		"share_rejected=%.0f refusals=[%s] no_data_memory_refused=%d took=%s slot_ms=%d input_ms=%d preflight_ms=%d evaluate_ms=%d "+
		"cpu=%.2fs cpu_user=%.2fs cpu_sys=%.2fs gc_cpu=%.2fs redis_cpu=%.2fs round_peak_rss=%d (reset %t) "+
		"heap_objects_peak=%d (before %d) live_marked_peak=%d (before %d) runtime_held_peak=%d "+
		"maxrss_before=%d maxrss_after=%d reserved_peak=%d retained_peak=%d (input %d output %d state %d gap %d) share=%d "+
		"windows=%d ceiling_windows=%d raised_windows=%d usage_series=%d state_mutations=%d events=%d "+
		"write_batches=%d events_written=%d record_ids_kept_runs=%d record_ids_outside_window=%d "+
		"witness_items=%.0f witness_bytes=%.0f redis=[%s] "+
		"apply_ms=%d record_ids_ms=%d cut_ms=%d merge_ms=%d series_ms=%d no_data_read_ms=%d no_data_write_ms=%d "+
		"unattributed_ms=%d input_slices=%d input_slices_discarded=%d input_slice_cut_on=%s input_slices_deferred=%d first_seen_unseen_slices=%d first_seen_unseen_series=%d "+
		"redis_wall_by_caller=[%s] memory_at_held_peak=[%s] memory_after=[%s] gc_cycles=%d uq_answer_bytes=%d "+
		"redis_bytes_sent=%.0f redis_bytes_received=%.0f redis_used_memory_before=%d redis_used_memory_after=%d "+
		"redis_keys_before=%d redis_keys_after=%d fake_uq_cpu=%.2fs read_ahead_replayed=%d read_ahead_other_leaf=%d "+
		"read_ahead_yielded=%d read_ahead_failed=%d slice_timeouts=%d shapes=[%s] %s",
		runtime.GOARCH, runtime.GOROOT(), shape.series, shape.points, shape.noData, measured.result, measured.reason, measured.cause, runErr,
		measured.rejected, strings.Join(refusals, ","), measured.noDataMemoryRefused, measured.took.Round(time.Millisecond),
		measured.timing.Slot, measured.timing.Input, measured.timing.Preflight, measured.timing.Evaluate,
		measured.cpu, measured.cpuUser, measured.cpuSystem, measured.gcCPU, measured.redisCPU,
		measured.roundPeakRSS, measured.roundPeakReset,
		measured.objectsPeak, measured.objectsBefore, measured.livePeak, measured.liveBefore, measured.heldPeak,
		measured.maxRSSBefore, measured.maxRSSAfter, measured.reservedPeak, usage.RetainedBytes,
		usage.RetainedInputBytes, usage.RetainedOutputBytes, usage.RetainedStateBytes, usage.RetainedGapBytes,
		usage.RetainedShareBytes, usage.Windows, usage.WindowCeilingWindows, usage.WindowCeilingRaisedWindows,
		usage.Series, usage.StateMutations, usage.Events, measured.batches, measured.events,
		measured.keptRuns, measured.outside, measured.witnessItems, measured.witnessBytes,
		windowMeasureCommandLine(measured.commands),
		measured.timing.Apply, measured.timing.RecordIDs, measured.timing.Cut, measured.timing.Merge, measured.timing.Series,
		measured.timing.NoDataRead, measured.timing.NoDataWrite, measured.timing.Unattributed,
		usage.InputSlices, usage.InputSlicesDiscarded, usage.InputSliceCutOn, usage.InputSlicesDeferred, usage.FirstSeenUnseenSlices, usage.FirstSeenUnseenSeries,
		windowMeasureSecondsLine(measured.redisCallers),
		measured.classesAtPeak, measured.classesAfter, measured.gcCycles, measured.uqBytes,
		measured.redisSent, measured.redisReceived, measured.redisUsedBefore, measured.redisUsedAfter,
		measured.redisKeysBefore, measured.redisKeysAfter, measured.fakeUQCPU, usage.InputSlicesReadAhead,
		usage.InputSlicesReadAheadOtherLeaf, usage.InputSlicesReadAheadYielded, usage.InputSlicesReadAheadFailed,
		usage.InputSliceTimeouts, windowMeasureShapesLine(usage), windowMeasureRequestsLine(measured.requests, measured.started, measured.took)) + fmt.Sprintf(" rss_idle=%d %s ", measured.idleRSS, measured.neighbors) + measured.host
}

// measureWindowedShape runs one shape: its own Redis, bundle and answers.
func measureWindowedShape(t *testing.T, shape windowMeasureShape) windowMeasureResult {
	t.Helper()
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", windowMeasureStrategy(t, shape), 0).Err(); err != nil {
		t.Fatal(err)
	}
	// An hour ahead of the real clock, on a minute boundary: every deadline
	// derived from the injected clock is measured against the real one, and a
	// Slot of half a million series runs for minutes.
	// The Slots' clock: an hour ahead of the real one, or with a measured
	// deadline five minutes, the warm-ups' deadlines minutes away either way.
	deadlineSeconds := windowMeasureDeadlineSeconds(t)
	ahead := int64(3600)
	if deadlineSeconds > 0 {
		ahead = 300
	}
	base := time.Now().Unix() + ahead
	base = base - base%60
	var clock atomic.Int64
	clock.Store(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	uqURL, uqPID := startWindowMeasureUQ(t, shape.series)
	cpus := windowMeasureCPUsApart(t, uqPID)

	cfg := largeGroupConfig(t, address, uqURL)
	cfg.Redis.StatePrefix = "alarmd-window-measure"
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(2 * time.Hour)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(2 * time.Hour)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(2 * time.Hour)
	cfg = windowMeasureCapacity(t, cfg)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("configuration rejected: %v", err)
	}

	sink := &countingWindowEventSink{}
	answers := &countingWindowTransport{next: http.DefaultTransport}
	var measuring atomic.Bool
	var mu sync.Mutex
	measured := windowMeasureResult{refusals: map[string]int{}}
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		if observation.Stage == observability.StageNoDataMemoryRefused {
			mu.Lock()
			measured.noDataMemoryRefused++
			mu.Unlock()
		}
		if usage := observation.SlotBudgetUsage; usage != nil && observation.Stage == observability.StageSlotCompleted {
			mu.Lock()
			for index, count := range [...]uint64{usage.NoDataStagesAheadCarried, usage.NoDataStagesAheadDiffers,
				usage.NoDataStagesAheadDiscarded, usage.NoDataStagesAheadStopped} {
				measured.stagesAhead[index] += count
			}
			for index, count := range [...]uint64{usage.NoDataStageAheadDiffersKeptOtherwise,
				usage.NoDataStageAheadDiffersCountMismatch, usage.NoDataStageAheadDiffersPresentAsOf} {
				measured.stageAheadDiffers[index] += count
			}
			measured.groupsStagedAhead += usage.NoDataGroupsStagedAhead
			mu.Unlock()
		}
		if !measuring.Load() {
			// The warm-up Slots: each completion and the first error each
			// stage raised, so a shape whose earlier Slots failed says how.
			mu.Lock()
			defer mu.Unlock()
			if observation.Stage == observability.StageSlotCompleted {
				// With how its input was read: the first Slot of a shape has
				// no reading to count its slices from, and the slices it let
				// go and its time are what that costs.
				var slices, discarded, timeouts, kept uint64
				var cutOn string
				if usage := observation.SlotBudgetUsage; usage != nil {
					slices, discarded, cutOn = usage.InputSlices, usage.InputSlicesDiscarded, usage.InputSliceCutOn
					timeouts, kept = usage.InputSliceTimeouts, usage.InputSliceShapeKept
				}
				var slotMillis uint64
				if timing := observation.SlotTiming; timing != nil {
					slotMillis = timing.Slot
				}
				measured.warmups = append(measured.warmups, fmt.Sprintf("%s/%s/%s/slices=%d/discarded=%d/cut_on=%s/slice_timeouts=%d/shape_kept=%d/slot_ms=%d",
					observation.Result, observation.ReasonCode, observation.SlotCompletionCause, slices, discarded, cutOn, timeouts, kept, slotMillis))
			}
			if observation.Err != nil && len(measured.warmupErrors) < 4 {
				measured.warmupErrors = append(measured.warmupErrors, fmt.Sprintf("%s: %v", observation.Stage, observation.Err))
			}
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if observation.Err != nil && len(measured.errors) < 4 {
			measured.errors = append(measured.errors, fmt.Sprintf("%s: %v", observation.Stage, observation.Err))
		}
		if observation.CapacityRejection != nil || observation.ReasonCode == observability.CompletionCauseResourceShareExceeded {
			measured.refusals[string(observation.ReasonCode)]++
		}
		if observation.Stage == observability.StageSlotCompleted {
			measured.completed = true
			measured.result, measured.reason = string(observation.Result), string(observation.ReasonCode)
			measured.cause = observation.SlotCompletionCause
			if observation.SlotBudgetUsage != nil {
				measured.usage = *observation.SlotBudgetUsage
			}
			if observation.SlotTiming != nil {
				measured.timing = *observation.SlotTiming
			}
		}
	})
	recorder := metric.NewRecorder(metric.BuildInfo{})
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, recorder, observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, StreamClock: time.Now, HTTPClient: &http.Client{Transport: answers}, AdditionalObserver: observer,
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return sink, nil
			}),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bundle.Shutdown(ctx) }()
	settleExecutableView(ctx, bundle, 10*time.Second)
	idleRSS := windowMeasureIdleRSS()
	mu.Lock()
	measured.idleRSS = idleRSS
	mu.Unlock()

	// The Slot at base+60k reads the minute before it.
	const slots = 3
	for slot := int64(0); slot < slots-1; slot++ {
		clock.Store(base + slot*60 + 1)
		if err := bundle.runScheduledOnce(ctx); err != nil {
			t.Fatalf("Slot %d: %v", slot, err)
		}
	}

	if shape.cold {
		forgetNoDataMemory(ctx, t, redisClient)
	}
	runtime.GC()
	samples := windowMeasureRuntimeSamples()
	measured.objectsBefore, measured.liveBefore, _ = windowMeasureRuntimeSample(samples)
	measured.maxRSSBefore = windowMeasureMaxRSS()
	rejectedBefore := windowMeasureMetricSum(recorder, "alarmd_capacity_transition_total", map[string]string{"result": "rejected"})
	keptBefore, outsideBefore := execution.DatasetRecordIDCounts()
	batchesBefore, eventsBefore := sink.batches.Load(), sink.events.Load()
	// Keys evicted in the warm-ups, before the reset that zeroes the count.
	evictedWarmups, maxmemory, policy := windowMeasureRedisEvictions(ctx, redisClient)
	if err := redisClient.ConfigResetStat(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	_, redisCPUBefore := windowMeasureRedisCommands(ctx, redisClient)
	availableBefore, swapBefore := windowMeasureMemInfo()
	hostCPUBefore := windowMeasureHostCPU()
	unobservedBefore := windowMeasureMetricSum(recorder, "alarmd_lookback_supplement_unobserved_total",
		map[string]string{"reason": "first_read_incomplete"})
	supplementBefore := windowMeasureMetricSum(recorder, "alarmd_lookback_supplement_windows_total", nil)
	measured.redisUsedBefore, measured.redisKeysBefore = windowMeasureRedisMemory(ctx, redisClient)
	sentBefore := windowMeasureMetricSum(recorder, "alarmd_redis_bytes_total", map[string]string{"direction": "sent"})
	receivedBefore := windowMeasureMetricSum(recorder, "alarmd_redis_bytes_total", map[string]string{"direction": "received"})
	answersBefore := answers.bytes.Load()
	requestsBefore := answers.answered()
	fakeUQBefore := windowMeasureProcessCPU(uqPID)
	callersBefore := windowMeasureHistogramSums(recorder, "alarmd_redis_caller_duration_seconds", "caller")
	// The measured Slot's CPU by function, for decode, record-id derivation
	// and apply apart, when a directory to keep it in is named.
	if dir := os.Getenv("ALARMD_MEASURE_PROFILE_DIR"); dir != "" {
		profile, err := os.Create(filepath.Join(dir, "cpu-"+strings.ReplaceAll(shape.String(), "/", "_")+".pprof"))
		if err != nil {
			t.Fatal(err)
		}
		defer profile.Close()
		if err := pprof.StartCPUProfile(profile); err != nil {
			t.Fatal(err)
		}
		defer pprof.StopCPUProfile()
	}

	var objectsPeak, livePeak, heldPeak, reservedPeak atomic.Uint64
	var classesAtPeak atomic.Pointer[string]
	done := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		local := windowMeasureRuntimeSamples()
		ticker := time.NewTicker(2 * time.Millisecond) // fixed-wait: the sampling interval of a measurement; it waits on no condition
		defer ticker.Stop()
		for {
			objects, live, held := windowMeasureRuntimeSample(local)
			objectsPeak.Store(max(objectsPeak.Load(), objects))
			livePeak.Store(max(livePeak.Load(), live))
			if held > heldPeak.Load() {
				heldPeak.Store(held)
				classes := windowMeasureMemoryClasses()
				classesAtPeak.Store(&classes)
			}
			// The pool read off the coordinator that owns it, not gathered:
			// a gather of every family is the measurement's own CPU.
			reservedPeak.Store(max(reservedPeak.Load(), worker.RetainedReserved(bundle.coordinator)))
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	measured.roundPeakReset = windowMeasureResetPeakRSS()
	userBefore, systemBefore, gcBefore, cyclesBefore := windowMeasureCPU()
	if deadlineSeconds > 0 {
		// The measured Slot, at base+(slots-1)*60, is due a minute later
		// (its period): it starts deadlineSeconds before that in real time,
		// its read hold on top (the line's uq_deadline_left_ms_first says
		// what its first query had).
		due := time.Unix(base+(slots-1)*60+60, 0)
		start := due.Add(-time.Duration(deadlineSeconds) * time.Second)
		if time.Now().After(start) {
			t.Fatalf("the warm-ups ran past %s, %ds before the measured Slot is due: no %ds left to it", start, deadlineSeconds, deadlineSeconds)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Until(start)): // fixed-wait: the real clock brought to the measured Slot's deadline less the deadline measured, not a wait for a condition
		}
	}
	measuring.Store(true)
	clock.Store(base + (slots-1)*60 + 1)
	// Each window of the measured Slot, its bytes and series, as it is let
	// go (the alarmd_measure build's hook).
	var sizesMu sync.Mutex
	var sizes []string
	previousSizes, sizesRead := windowMeasureSetWindowSizes(func(bytes uint64, series int) {
		sizesMu.Lock()
		defer sizesMu.Unlock()
		sizes = append(sizes, fmt.Sprintf("%d/%d", bytes, series))
	})
	// Each wait of a read ahead for room, as it ends: how long reads ahead
	// held their queries, and the connections under them, unread.
	var waits []time.Duration
	previousWaits, waitsRead := windowMeasureSetReadAheadWaits(func(waited time.Duration) {
		sizesMu.Lock()
		defer sizesMu.Unlock()
		waits = append(waits, waited)
	})
	coreNeighbors := startWindowMeasureNeighbors(runtime.GOMAXPROCS(0))
	started := time.Now()
	runErr := bundle.runScheduledOnce(ctx)
	took := time.Since(started)
	if sizesRead {
		windowMeasureSetWindowSizes(previousSizes)
	}
	if waitsRead {
		windowMeasureSetReadAheadWaits(previousWaits)
	}
	measuring.Store(false)
	neighborsLine := coreNeighbors.stop()
	userAfter, systemAfter, gcAfter, cyclesAfter := windowMeasureCPU()
	answersAfter := answers.bytes.Load()
	measured.requests = answers.since(requestsBefore)
	measured.fakeUQCPU = windowMeasureProcessCPU(uqPID) - fakeUQBefore
	measured.roundPeakRSS = windowMeasureRoundPeakRSS()
	close(done)
	<-sampled

	mu.Lock()
	defer mu.Unlock()
	measured.runErr, measured.took, measured.started = runErr, took, started
	measured.neighbors = neighborsLine
	measured.cpuUser, measured.cpuSystem, measured.gcCPU = userAfter-userBefore, systemAfter-systemBefore, gcAfter-gcBefore
	measured.cpu = measured.cpuUser + measured.cpuSystem
	measured.gcCycles, measured.uqBytes = cyclesAfter-cyclesBefore, answersAfter-answersBefore
	measured.objectsPeak, measured.livePeak, measured.heldPeak = objectsPeak.Load(), livePeak.Load(), heldPeak.Load()
	measured.reservedPeak = reservedPeak.Load()
	if classes := classesAtPeak.Load(); classes != nil {
		measured.classesAtPeak = *classes
	}
	measured.classesAfter = windowMeasureMemoryClasses()
	measured.redisCallers = map[string]float64{}
	for caller, seconds := range windowMeasureHistogramSums(recorder, "alarmd_redis_caller_duration_seconds", "caller") {
		if spent := seconds - callersBefore[caller]; spent > 0 {
			measured.redisCallers[caller] = spent
		}
	}
	measured.maxRSSAfter = windowMeasureMaxRSS()
	measured.rejected = windowMeasureMetricSum(recorder, "alarmd_capacity_transition_total",
		map[string]string{"result": "rejected"}) - rejectedBefore
	keptAfter, outsideAfter := execution.DatasetRecordIDCounts()
	measured.keptRuns, measured.outside = keptAfter-keptBefore, outsideAfter-outsideBefore
	measured.batches, measured.events = sink.batches.Load()-batchesBefore, sink.events.Load()-eventsBefore
	measured.witnessItems = windowMeasureMetricSum(recorder, "alarmd_state_witness_items", nil)
	measured.witnessBytes = windowMeasureMetricSum(recorder, "alarmd_state_witness_bytes", nil)
	var redisCPUAfter float64
	measured.commands, redisCPUAfter = windowMeasureRedisCommands(ctx, redisClient)
	measured.redisCPU = redisCPUAfter - redisCPUBefore
	measured.redisUsedAfter, measured.redisKeysAfter = windowMeasureRedisMemory(ctx, redisClient)
	measured.redisSent = windowMeasureMetricSum(recorder, "alarmd_redis_bytes_total",
		map[string]string{"direction": "sent"}) - sentBefore
	measured.redisReceived = windowMeasureMetricSum(recorder, "alarmd_redis_bytes_total",
		map[string]string{"direction": "received"}) - receivedBefore
	evictedRound, _, _ := windowMeasureRedisEvictions(ctx, redisClient)
	availableAfter, swapAfter := windowMeasureMemInfo()
	// What other users of the host took over the round: every core's busy
	// time less alarmd's, the fake UQ's and Redis's.
	neighbors := windowMeasureHostCPU() - hostCPUBefore - measured.cpu - measured.fakeUQCPU - measured.redisCPU
	format := strings.TrimSpace(os.Getenv(windowMeasureUQFormat))
	if format == "" {
		format = windowMeasureUQFormatShared
	}
	measured.host = fmt.Sprintf("uq_series_micros=%d uq_format=%s uq_production_version=3.9.4892 redis_evicted_keys_warmups=%d redis_evicted_keys_round=%d "+
		"redis_maxmemory=%s redis_maxmemory_policy=%s mem_available_kib_before=%d mem_available_kib_after=%d "+
		"swap_used_kib_before=%d swap_used_kib_after=%d neighbor_cpu=%.2fs %s "+
		"first_read_incomplete=%.0f supplement_windows=%.0f",
		windowMeasureSeriesMicros(t), format, evictedWarmups, evictedRound, maxmemory, policy, availableBefore, availableAfter,
		swapBefore, swapAfter, neighbors, cpus,
		windowMeasureMetricSum(recorder, "alarmd_lookback_supplement_unobserved_total",
			map[string]string{"reason": "first_read_incomplete"})-unobservedBefore,
		windowMeasureMetricSum(recorder, "alarmd_lookback_supplement_windows_total", nil)-supplementBefore)
	sizesMu.Lock()
	defer sizesMu.Unlock()
	if !sizesRead {
		measured.host += " window_sizes=unread"
	} else {
		measured.host += " window_sizes_bytes_series=[" + strings.Join(sizes, ",") + "]"
	}
	if !waitsRead {
		measured.host += " read_ahead_waits=unread"
	} else {
		var total, longest time.Duration
		for _, waited := range waits {
			total, longest = total+waited, max(longest, waited)
		}
		measured.host += fmt.Sprintf(" read_ahead_waits=%d read_ahead_wait_ms_total=%d read_ahead_wait_ms_max=%d",
			len(waits), total.Milliseconds(), longest.Milliseconds())
	}
	return measured
}

// windowMeasureField is one dimension of a shape: its name and the value a
// series index gives it.
type windowMeasureField struct {
	name  string
	value func(index int) string
}

// TestMeasureDimensionRepetitionForInterning reads, for the synthetic shape
// the window measurements use (the largest Query Group's three dimensions at
// 500,000 series) and for the largest Query Group itself (95,760 series of
// the same three), and beside them the disk shape and the wide shape the
// input measurements read (access/uq columnar_decode_test.go), how often each
// dimension's values repeat: distinct values over all values per field, and
// the bytes a Query Group interning each field's values would no longer keep
// - every repetition's bytes, the string headers staying as they are. It is
// arithmetic over the generators, with no bundle; the interning decision of
// decision-020 batch 8 (trade-off two) reads it beside the window
// measurements. Runs with ALARMD_MEASURE_WINDOWS=1.
func TestMeasureDimensionRepetitionForInterning(t *testing.T) {
	if os.Getenv("ALARMD_MEASURE_WINDOWS") == "" {
		t.Skip("a measurement: set ALARMD_MEASURE_WINDOWS=1")
	}
	largest := []windowMeasureField{
		{"bk_target_cloud_id", func(int) string { return "0" }},
		{"bk_target_ip", func(index int) string { return fmt.Sprintf("198.51.100.%03d", index%256) }},
		{"device_name", func(index int) string { return fmt.Sprintf("device-%025d", index) }},
	}
	disk := []windowMeasureField{
		{"bk_target_ip", func(index int) string { return fmt.Sprintf("192.0.2.%d", index%256) }},
		{"bk_target_cloud_id", func(int) string { return "0" }},
		{"device_name", func(index int) string { return fmt.Sprintf("/dev/vd%c%d", 'a'+index%26, index%7) }},
		{"mountpoint", func(index int) string { return fmt.Sprintf("/data%d", index) }},
	}
	wide := append([]windowMeasureField(nil), disk...)
	for tag := 0; tag < 21; tag++ {
		wide = append(wide, windowMeasureField{fmt.Sprintf("tag%02d", tag),
			func(index int) string { return fmt.Sprintf("%0320d", index*21+tag) }})
	}
	for _, shape := range []struct {
		name   string
		series int
		fields []windowMeasureField
	}{
		{"synthetic", 500_000, largest},
		{"largest_query_group", 95_760, largest},
		{"disk", 500_000, disk},
		{"wide", 50_000, wide},
	} {
		var total, saved, names uint64
		for _, field := range shape.fields {
			seen := make(map[string]struct{}, 1024)
			var fieldBytes, fieldSaved uint64
			for index := 0; index < shape.series; index++ {
				value := field.value(index)
				fieldBytes += uint64(len(value))
				if _, repeated := seen[value]; repeated {
					fieldSaved += uint64(len(value))
					continue
				}
				seen[value] = struct{}{}
			}
			// The name repeats on every series as well; interning it saves
			// all but one copy.
			nameSaved := uint64(len(field.name)) * uint64(shape.series-1)
			total += fieldBytes
			saved += fieldSaved
			names += nameSaved
			t.Logf("shape=%s field=%s distinct=%d total=%d ratio=%.6f value_bytes=%d interned_saves=%d (%d a series) name_saves=%d",
				shape.name, field.name, len(seen), shape.series, float64(len(seen))/float64(shape.series), fieldBytes,
				fieldSaved, fieldSaved/uint64(shape.series), nameSaved)
		}
		t.Logf("shape=%s series=%d value_bytes=%d interned_saves=%d (%d a series) name_saves=%d (%d a series)",
			shape.name, shape.series, total, saved, saved/uint64(shape.series), names, names/uint64(shape.series))
	}
}

// forgetNoDataMemory deletes every no-data memory the warm-up Slots wrote,
// its staged pieces with it, so the next Slot writes it first.
func forgetNoDataMemory(ctx context.Context, t *testing.T, client *redis.Client) {
	t.Helper()
	deleted := 0
	for _, pattern := range []string{"*:nodata-hash:*", "*:nodata-hash-shard:*", "*:nodata-stage:*", "*:nodata-stage-shard:*"} {
		iter := client.Scan(ctx, 0, pattern, 1000).Iterator()
		for iter.Next(ctx) {
			if err := client.Del(ctx, iter.Val()).Err(); err != nil {
				t.Fatal(err)
			}
			deleted++
		}
		if err := iter.Err(); err != nil {
			t.Fatal(err)
		}
	}
	if deleted == 0 {
		t.Fatal("a cold shape found no no-data memory to forget: the warm-up Slots wrote none")
	}
}
