// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

func sharedTestData(count int) *PromData {
	data := NewPromData(nil)
	for i := 0; i < count; i++ {
		data.Tables = append(data.Tables, &TablesItem{
			Name: fmt.Sprintf("_result%d", i), MetricName: "example_metric",
			Columns: []string{"_time", "_value"}, Types: []string{"float", "float"},
			GroupKeys: []string{"device"}, GroupValues: []string{fmt.Sprintf("dev%d", i)},
			Values: [][]any{{int64(1700000000000), float64(i)}, {int64(1700000060000), nil}},
			Stat:   &StatItem{},
		})
	}
	return data
}

func TestSharedSchemaAccept(t *testing.T) {
	for _, test := range []struct {
		name, accept string
		want         sharedSchemaNegotiation
	}{
		{"absent", "", sharedSchemaNegotiation{}},
		{"old", "application/json", sharedSchemaNegotiation{}},
		{"wildcard", "*/*", sharedSchemaNegotiation{}},
		{"application wildcard", "application/*", sharedSchemaNegotiation{}},
		{"future", "application/vnd.bkmonitor.uq.shared-schema.v2+ndjson, application/json", sharedSchemaNegotiation{}},
		{"equal prefers v1", sharedSchemaV1MediaType + ", application/json", sharedSchemaNegotiation{explicit: true, selected: true}},
		{"opt in", sharedSchemaV1MediaType + ", application/json;q=0.9", sharedSchemaNegotiation{explicit: true, selected: true}},
		{"legacy preferred", sharedSchemaV1MediaType + ";q=0.5, application/json", sharedSchemaNegotiation{explicit: true}},
		{"v1 unacceptable", sharedSchemaV1MediaType + ";q=0, application/json", sharedSchemaNegotiation{explicit: true}},
		{"strict rejected", sharedSchemaV1MediaType, sharedSchemaNegotiation{explicit: true, reject: true}},
		{"json zero overrides wildcard", sharedSchemaV1MediaType + ", application/json;q=0, */*;q=1", sharedSchemaNegotiation{explicit: true, reject: true}},
		{"json wildcard permitted", sharedSchemaV1MediaType + ", */*;q=0.9", sharedSchemaNegotiation{explicit: true, selected: true}},
		{"params case quotes", strings.ToUpper(sharedSchemaV1MediaType) + ";ext=\"a,b\";q=1, application/json;q=0.8", sharedSchemaNegotiation{explicit: true, selected: true}},
		{"invalid q", sharedSchemaV1MediaType + ";q=NaN, application/json", sharedSchemaNegotiation{}},
		{"invalid range", sharedSchemaV1MediaType + ";q=1.1, application/json", sharedSchemaNegotiation{}},
		{"invalid media", sharedSchemaV1MediaType + ";q, application/json", sharedSchemaNegotiation{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, negotiateSharedSchema(test.accept))
		})
	}
	header := http.Header{"Vary": []string{"Origin"}}
	varyAccept(header)
	varyAccept(header)
	require.Equal(t, []string{"Origin", "Accept"}, header.Values("Vary"))
}

func TestSharedSchemaQueryTsNegotiationScopeAndMultipleHeaders(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/query/ts", nil)
	c.Request.Header.Add("Accept", sharedSchemaV1MediaType)
	c.Request.Header.Add("Accept", "application/json;q=0.9")
	resp := &response{c: c}
	require.Equal(t, sharedSchemaNegotiation{explicit: true, selected: true}, resp.queryTsSharedSchemaNegotiation(&structured.QueryTs{}))
	require.Equal(t, sharedSchemaNegotiation{}, resp.queryTsSharedSchemaNegotiation(&structured.QueryTs{ResponseContract: structured.NamedOutputsV1}))
	c.Set(proxy.ContextConfigUnifyResponseProcess, true)
	require.Equal(t, sharedSchemaNegotiation{}, resp.queryTsSharedSchemaNegotiation(&structured.QueryTs{}))
}

func sharedEncode(t *testing.T, data *PromData) ([]sharedFrame, []byte) {
	t.Helper()
	plan, reason, err := preflightSharedSchema(context.Background(), data)
	require.NoError(t, err)
	require.Empty(t, reason)
	var body bytes.Buffer
	flushes := 0
	stats, err := writeSharedSchema(context.Background(), &body, func() error { flushes++; return nil }, data, plan)
	require.NoError(t, err)
	require.Equal(t, int64(body.Len()), stats.Bytes)
	require.Equal(t, stats.Frames, flushes)
	lines := bytes.Split(body.Bytes(), []byte{'\n'})
	require.Empty(t, lines[len(lines)-1])
	frames := make([]sharedFrame, 0, len(lines)-1)
	for seq, line := range lines[:len(lines)-1] {
		require.LessOrEqual(t, len(line)+1, sharedSchemaMaxFrameBytes)
		var frame sharedFrame
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		require.NoError(t, decoder.Decode(&frame))
		require.Equal(t, seq, frame.Seq)
		require.LessOrEqual(t, len(frame.Series), sharedSchemaMaxSeries)
		frames = append(frames, frame)
	}
	require.Equal(t, stats.Frames, len(frames))
	return frames, body.Bytes()
}

func TestSharedSchemaWireAndOwnedProjection(t *testing.T) {
	data := sharedTestData(2)
	data.Tables[1].GroupKeys = []string{"other_device"}
	data.Tables[1].Values = [][]any{{int64(1700000000000), json.Number("9007199254740993")}, {int64(1700000060000), false}}
	data.Status = &metadata.Status{Code: "OK", Message: "example"}
	data.IsPartial, data.TraceID = true, "example-trace"
	data.SetResultTableID([]string{"example.result"})
	before, err := json.Marshal(data)
	require.NoError(t, err)
	frames, body := sharedEncode(t, data)
	fixture, err := os.ReadFile("testdata/shared_schema_v1.ndjson")
	require.NoError(t, err)
	require.Equal(t, string(fixture), string(body))
	require.Equal(t, 2, len(frames[3].Series))
	require.Equal(t, json.Number("9007199254740993"), frames[3].Series[1].Rows[0][1])
	require.Equal(t, false, frames[3].Series[1].Rows[1][1])
	end := frames[len(frames)-1].End
	require.Equal(t, int64(2), end.Series)
	require.Equal(t, int64(4), end.Points)
	require.Equal(t, data.Status, end.Status)
	require.Equal(t, data.TraceID, end.TraceID)
	require.True(t, end.IsPartial)
	after, err := json.Marshal(data)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NotContains(t, string(body), "metric_name")
	require.NotContains(t, string(body), `"stat":`)
}

func TestSharedSchemaEmptySeriesMetadataAndPacking(t *testing.T) {
	data := sharedTestData(513)
	data.Tables[0].Values = nil
	data.SetResultTableID(nil)
	frames, body := sharedEncode(t, data)
	require.Equal(t, 6, len(frames)) // header, one schema, three data frames, end.
	require.Len(t, frames[2].Series, 256)
	require.Len(t, frames[3].Series, 256)
	require.Len(t, frames[4].Series, 1)
	require.Empty(t, frames[2].Series[0].Rows)
	require.Contains(t, string(body), `"r":[]`)
	require.Contains(t, string(body), `"result_table_id":[]`)
	require.Equal(t, int64(1024), frames[5].End.Points)

	emptyFrames, emptyBody := sharedEncode(t, NewPromData(nil))
	require.Len(t, emptyFrames, 2)
	require.NotContains(t, string(emptyBody), "result_table_id")
	require.Equal(t, int64(0), emptyFrames[1].End.Series)
	require.Equal(t, int64(0), emptyFrames[1].End.Points)
}

func TestSharedSchemaCounterMatchesEncoder(t *testing.T) {
	ctx := context.Background()
	values := []any{nil, true, false, "", "汉字😀<&>\x00\b\n\r\t\f\\\"\u2028\u2029\xff", int64(math.MinInt64), uint64(math.MaxUint64), float64(0), math.Copysign(0, -1), float64(1e-7), float64(1e-6), float64(1e20), float64(1e21), float32(1e-7), float32(1e-6), float32(1e21), json.Number(""), json.Number("-0.15e+123")}
	random := rand.New(rand.NewSource(7))
	for i := 0; i < 1000; i++ {
		bits := random.Uint64()
		value := math.Float64frombits(bits)
		if !math.IsNaN(value) && !math.IsInf(value, 0) {
			values = append(values, value, float32(value))
		}
		text := make([]byte, i%128)
		_, _ = random.Read(text)
		values = append(values, string(text))
	}
	for _, value := range values {
		body, err := json.Marshal(value)
		counter := newSharedSizeCounter(ctx)
		counter.scalar(value)
		if err != nil {
			require.Equal(t, sharedFallbackScalar, counter.reason)
			continue
		}
		require.Empty(t, counter.reason, "%T %v", value, value)
		require.Equal(t, len(body), counter.size, "%T %v", value, value)
	}
	schema := sharedSchema{ID: 9, Columns: []string{"<_value>", "_time"}, Types: []string{"float", "float"}, GroupKeys: []string{"device"}}
	counter := newSharedSizeCounter(ctx)
	counter.schema(schema)
	body, err := json.Marshal(schema)
	require.NoError(t, err)
	require.Equal(t, len(body), counter.size)
	for _, seq := range []int{0, 9, 10, 99, 100, 9999} {
		frame, err := json.Marshal(sharedFrame{Seq: seq, Schemas: []sharedSchema{schema}})
		require.NoError(t, err)
		require.Equal(t, len(frame)+1, schemaFrameOverhead(seq)+counter.size)
	}
	series := sharedSeries{SchemaID: 10, Groups: []string{"<&汉字"}, Rows: [][]any{values[:18]}}
	counter = newSharedSizeCounter(ctx)
	counter.series(series, 18)
	body, err = json.Marshal(series)
	require.NoError(t, err)
	require.Equal(t, len(body), counter.size)
	ids := []string{"<&example.result", "second.result"}
	end := sharedEnd{Series: 123, Points: 123456, IsPartial: true, Status: &metadata.Status{Code: "EXAMPLE", Message: "<&message"}, ResultTableID: &ids, TraceID: "example-trace"}
	counter = newSharedSizeCounter(ctx)
	counter.end(end)
	body, err = json.Marshal(end)
	require.NoError(t, err)
	require.Equal(t, len(body), counter.size)
}

type sharedCustomScalar string

func (sharedCustomScalar) MarshalJSON() ([]byte, error) {
	panic("preflight must not call custom marshalers")
}

func TestSharedSchemaFallbacksBeforeEncoding(t *testing.T) {
	for _, test := range []struct {
		name, reason string
		change       func(*PromData)
	}{
		{"nil table", sharedFallbackShape, func(d *PromData) { d.Tables[0] = nil }},
		{"types mismatch", sharedFallbackShape, func(d *PromData) { d.Tables[0].Types = nil }},
		{"groups mismatch", sharedFallbackShape, func(d *PromData) { d.Tables[0].GroupValues = nil }},
		{"row mismatch", sharedFallbackShape, func(d *PromData) { d.Tables[0].Values[0] = []any{1} }},
		{"custom scalar", sharedFallbackScalar, func(d *PromData) { d.Tables[0].Values[0][1] = sharedCustomScalar("secret") }},
		{"object", sharedFallbackScalar, func(d *PromData) { d.Tables[0].Values[0][1] = map[string]any{"x": 1} }},
		{"NaN", sharedFallbackScalar, func(d *PromData) { d.Tables[0].Values[0][1] = math.NaN() }},
		{"Inf", sharedFallbackScalar, func(d *PromData) { d.Tables[0].Values[0][1] = math.Inf(1) }},
		{"invalid number", sharedFallbackScalar, func(d *PromData) { d.Tables[0].Values[0][1] = json.Number("01") }},
		{"giant scalar", sharedFallbackFrame, func(d *PromData) { d.Tables[0].Values[0][1] = strings.Repeat("x", sharedSchemaMaxFrameBytes*4) }},
		{"giant schema", sharedFallbackFrame, func(d *PromData) { d.Tables[0].Columns[0] = strings.Repeat("x", sharedSchemaMaxFrameBytes*4) }},
		{"giant footer", sharedFallbackFrame, func(d *PromData) { d.TraceID = strings.Repeat("x", sharedSchemaMaxFrameBytes*4) }},
		{"giant status", sharedFallbackFrame, func(d *PromData) {
			d.Status = &metadata.Status{Message: strings.Repeat("x", sharedSchemaMaxFrameBytes*4)}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := sharedTestData(1)
			test.change(data)
			plan, reason, err := preflightSharedSchema(context.Background(), data)
			require.NoError(t, err)
			require.Nil(t, plan)
			require.Equal(t, test.reason, reason)
		})
	}
	data := sharedTestData(sharedSchemaMaxSchemas + 1)
	for i, table := range data.Tables {
		table.GroupKeys[0] = fmt.Sprintf("dimension%d", i)
	}
	_, reason, err := preflightSharedSchema(context.Background(), data)
	require.NoError(t, err)
	require.Equal(t, sharedFallbackSchemas, reason)
	data = sharedTestData(40)
	for i, table := range data.Tables {
		table.GroupKeys[0] = fmt.Sprintf("%d", i) + strings.Repeat("x", 220*1024)
	}
	_, reason, err = preflightSharedSchema(context.Background(), data)
	require.NoError(t, err)
	require.Equal(t, sharedFallbackDictionary, reason)
}

func TestSharedSchemaFrameByteBoundaryAndPreflightScratch(t *testing.T) {
	data := sharedTestData(1)
	data.Tables[0].Values = [][]any{{int64(0), ""}}
	series := seriesProjection(data.Tables[0], 0)
	counter := newSharedSizeCounter(context.Background())
	counter.series(series, 2)
	remaining := sharedSchemaMaxFrameBytes - dataFrameOverhead(2) - counter.size
	data.Tables[0].Values[0][1] = strings.Repeat("x", remaining)
	frames, _ := sharedEncode(t, data)
	body, err := json.Marshal(frames[2])
	require.NoError(t, err)
	require.Equal(t, sharedSchemaMaxFrameBytes, len(body)+1)
	data.Tables[0].Values[0][1] = strings.Repeat("x", remaining+1)
	_, reason, err := preflightSharedSchema(context.Background(), data)
	require.NoError(t, err)
	require.Equal(t, sharedFallbackFrame, reason)

	// Input allocation is outside the measurement. A rejected huge candidate
	// must not allocate an encoded copy proportional to its size.
	for _, target := range []string{"series", "schema", "footer"} {
		t.Run(target, func(t *testing.T) {
			data := sharedTestData(1)
			text := strings.Repeat("<&", 16*1024*1024)
			switch target {
			case "series":
				data.Tables[0].Values[0][1] = text
			case "schema":
				data.Tables[0].Columns[0] = text
			case "footer":
				data.TraceID = text
			}
			measurement := testing.Benchmark(func(b *testing.B) {
				for b.Loop() {
					_, reason, err := preflightSharedSchema(context.Background(), data)
					if reason != sharedFallbackFrame || err != nil {
						b.Fatal(reason, err)
					}
				}
			})
			require.Less(t, measurement.AllocedBytesPerOp(), int64(32*1024))
		})
	}
}

type sharedFailWriter struct {
	bytes.Buffer
	failAt, calls int
	short         bool
}

func (w *sharedFailWriter) Write(body []byte) (int, error) {
	w.calls++
	if w.calls == w.failAt {
		if w.short {
			return w.Buffer.Write(body[:len(body)-1])
		}
		return 0, io.ErrClosedPipe
	}
	return w.Buffer.Write(body)
}

func TestSharedSchemaWriteFailureAndCancellation(t *testing.T) {
	data := sharedTestData(600)
	plan, reason, err := preflightSharedSchema(context.Background(), data)
	require.NoError(t, err)
	require.Empty(t, reason)
	for _, short := range []bool{false, true} {
		writer := &sharedFailWriter{failAt: 4, short: short}
		stats, err := writeSharedSchema(context.Background(), writer, nil, data, plan)
		require.Error(t, err)
		require.Equal(t, "write", stats.FailureStage)
		require.Equal(t, 4, writer.calls)
		require.NotContains(t, writer.String(), `"end":`)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var body bytes.Buffer
	flushes := 0
	stats, err := writeSharedSchema(ctx, &body, func() error {
		flushes++
		if flushes == 3 {
			cancel()
		}
		return nil
	}, data, plan)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "stream_cancel", stats.FailureStage)
	require.Equal(t, 3, flushes)
	require.NotContains(t, body.String(), `"end":`)
	_, _, err = preflightSharedSchema(ctx, data)
	require.ErrorIs(t, err, context.Canceled)
	stats, err = writeSharedSchema(context.Background(), io.Discard, func() error { return errors.New("flush failure") }, data, plan)
	require.Error(t, err)
	require.Equal(t, "write", stats.FailureStage)
	require.Equal(t, 0, stats.Frames)
}

func TestSharedSchemaResponseFallbackAndHTTPObservation(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		data := sharedTestData(2)
		if fallback {
			data.TraceID = strings.Repeat("x", sharedSchemaMaxFrameBytes)
		}
		recorder := &sharedErrorAwareRecorder{ResponseRecorder: httptest.NewRecorder()}
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/query/ts", nil)
		codec := "shared-schema-v1"
		if fallback {
			codec = "legacy-json"
		}
		beforeBytes := sharedMetricCounter(t, "unify_query_shared_schema_response_bytes_total", map[string]string{"codec": codec})
		beforeSuccess := sharedMetricCounter(t, "unify_query_api_request_total", map[string]string{"api": "/query/ts", "status": "success", "space_uid": "", "source_type": ""})
		err := (&response{c: c}).sharedSchemaSuccess(context.Background(), data)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, recorder.Body.Len(), c.Writer.Size())
		require.Equal(t, float64(recorder.Body.Len()), sharedMetricCounter(t, "unify_query_shared_schema_response_bytes_total", map[string]string{"codec": codec})-beforeBytes)
		require.Equal(t, 1.0, sharedMetricCounter(t, "unify_query_api_request_total", map[string]string{"api": "/query/ts", "status": "success", "space_uid": "", "source_type": ""})-beforeSuccess)
		if fallback {
			require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
			legacy, err := json.Marshal(data)
			require.NoError(t, err)
			require.JSONEq(t, string(legacy), recorder.Body.String())
		} else {
			require.Equal(t, sharedSchemaV1MediaType, recorder.Header().Get("Content-Type"))
			require.True(t, recorder.Flushed)
			require.Contains(t, recorder.Body.String(), `"end":`)
		}
	}
	log.InitTestLogger()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/query/ts", strings.NewReader(`{}`))
	c.Request.Header.Set("Accept", sharedSchemaV1MediaType)
	HandlerQueryTs(c) // Empty query would fail execution; negotiation rejects before it.
	require.Equal(t, http.StatusNotAcceptable, recorder.Code)
	require.Equal(t, "Accept", recorder.Header().Get("Vary"))
	require.Contains(t, recorder.Body.String(), `"error":`)
}

type sharedFailHTTPWriter struct {
	*sharedErrorAwareRecorder
	calls int
}

func (w *sharedFailHTTPWriter) Write(body []byte) (int, error) {
	w.calls++
	if w.calls == 3 {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseRecorder.Write(body)
}

func TestSharedSchemaResponseWriteFailureCountsOnce(t *testing.T) {
	writer := &sharedFailHTTPWriter{sharedErrorAwareRecorder: &sharedErrorAwareRecorder{ResponseRecorder: httptest.NewRecorder()}}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/query/ts", nil)
	labels := map[string]string{"api": "/query/ts", "space_uid": "", "source_type": "", "status": "failed"}
	beforeFailure := sharedMetricCounter(t, "unify_query_api_request_total", labels)
	labels["status"] = "success"
	beforeSuccess := sharedMetricCounter(t, "unify_query_api_request_total", labels)
	err := (&response{c: c}).sharedSchemaSuccess(context.Background(), sharedTestData(2))
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.Equal(t, 3, writer.calls)
	require.NotContains(t, writer.Body.String(), `"end":`)
	require.NotContains(t, writer.Body.String(), `"error":`)
	require.Equal(t, beforeSuccess, sharedMetricCounter(t, "unify_query_api_request_total", labels))
	labels["status"] = "failed"
	require.Equal(t, 1.0, sharedMetricCounter(t, "unify_query_api_request_total", labels)-beforeFailure)
}

func sharedMetricCounter(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, sample := range family.Metric {
			matches := 0
			for _, label := range sample.Label {
				if value, ok := labels[label.GetName()]; ok && value == label.GetValue() {
					matches++
				}
			}
			if matches == len(labels) {
				return sample.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func FuzzSharedSchemaScalarLength(f *testing.F) {
	f.Add("<&汉字\xff\u2028", uint64(0x8000000000000000))
	f.Add("\x00\n\t\\\"", uint64(0x7fefffffffffffff))
	f.Fuzz(func(t *testing.T, text string, bits uint64) {
		for _, value := range []any{text, math.Float64frombits(bits)} {
			counter := newSharedSizeCounter(context.Background())
			counter.scalar(value)
			if counter.reason == sharedFallbackFrame {
				continue
			}
			body, err := json.Marshal(value)
			if err != nil {
				require.Equal(t, sharedFallbackScalar, counter.reason)
			} else {
				require.Equal(t, len(body), counter.size, reflect.TypeOf(value).String())
			}
		}
	})
}

func BenchmarkSharedSchemaEncoding(b *testing.B) {
	data := sharedTestData(10000)
	for _, name := range []string{"legacy", "shared"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			writer := &sharedDiscardResponseWriter{header: make(http.Header)}
			for b.Loop() {
				if name == "legacy" {
					if err := render.WriteJSON(writer, data); err != nil {
						b.Fatal(err)
					}
				} else {
					plan, reason, err := preflightSharedSchema(context.Background(), data)
					if err != nil || reason != "" {
						b.Fatal(reason, err)
					}
					if _, err := writeSharedSchema(context.Background(), io.Discard, nil, data, plan); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

type sharedDiscardResponseWriter struct{ header http.Header }

func (w *sharedDiscardResponseWriter) Header() http.Header { return w.header }
func (*sharedDiscardResponseWriter) WriteHeader(int)       {}
func (*sharedDiscardResponseWriter) Write(body []byte) (int, error) {
	return len(body), nil
}
