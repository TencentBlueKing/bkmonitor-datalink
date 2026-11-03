// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/tracing"
)

// slotExecutorForTest runs a Slot by sending its queries through the
// tracing context, as access does, and ends as told.
type slotExecutorForTest struct {
	queries int
	err     error
	sent    []string
}

func (executor *slotExecutorForTest) Execute(ctx context.Context, _ execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	for attempt := range executor.queries {
		query, parent, _ := tracing.StartQuery(ctx, "uq", 1, uint32(attempt+1))
		executor.sent = append(executor.sent, parent)
		query.End("COMPLETE", "", false)
	}
	return execution.SlotExecutionResult{}, executor.err
}

// otlpCollectorForTest keeps the spans an OTLP/HTTP client sends it.
func otlpCollectorForTest(t *testing.T) (string, func() []*tracepb.Span) {
	t.Helper()
	var mu sync.Mutex
	var spans []*tracepb.Span
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		decoded := &collector.ExportTraceServiceRequest{}
		if err := proto.Unmarshal(body, decoded); err == nil {
			mu.Lock()
			for _, resource := range decoded.ResourceSpans {
				for _, scope := range resource.ScopeSpans {
					spans = append(spans, scope.Spans...)
				}
			}
			mu.Unlock()
		}
		raw, _ := proto.Marshal(&collector.ExportTraceServiceResponse{})
		writer.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = writer.Write(raw)
	}))
	t.Cleanup(server.Close)
	return server.URL, func() []*tracepb.Span {
		mu.Lock()
		defer mu.Unlock()
		return append([]*tracepb.Span(nil), spans...)
	}
}

// A Slot the production executor runs is one root span carrying its Query
// Group, evaluation time, outcome and reason, and every query it sends is a
// span under it whose context is what the query service receives; with the
// export off, nothing is built and every query is an unsampled trace.
func TestASlotRunsUnderItsOwnSpanAndItsQueriesUnderIt(t *testing.T) {
	endpoint, received := otlpCollectorForTest(t)
	recorder := metric.NewRecorder(metric.BuildInfo{})
	traces, err := tracing.New(tracing.Config{Endpoint: endpoint, SampleRatio: 1, MaxTracesPerSecond: 5, InstanceID: "worker-1"}, recorder)
	if err != nil {
		t.Fatal(err)
	}
	next := &slotExecutorForTest{queries: 2, err: errors.New("the query failed")}
	executor := observedProductionSlotExecutor{next: next, observer: observability.ObserverFunc(func(context.Context, observability.Observation) {}), traces: traces}
	request := execution.SlotExecutionRequest{Contract: execution.FrozenExecutionContractRef{
		Slot: execution.SlotIdentity{QueryGroup: "qg-trace", EvaluationTime: 1_800_000_000}}}
	_, _ = executor.Execute(context.Background(), request)
	// A flush the case expects to finish, bounded by the test's deadline.
	shutdown := testwait.Context(t)
	if err := traces.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	spans := received()
	if len(spans) != 3 {
		t.Fatalf("collector got %d spans, want the Slot's and two queries'", len(spans))
	}
	var slot *tracepb.Span
	for _, span := range spans {
		if span.Name == "alarmd.slot" {
			slot = span
		}
	}
	if slot == nil || len(slot.ParentSpanId) != 0 || slot.Status.GetCode() != tracepb.Status_STATUS_CODE_ERROR {
		t.Fatalf("Slot span = %v, want a failed root", slot)
	}
	attributes := map[string]string{}
	for _, attribute := range slot.Attributes {
		attributes[attribute.Key] = attribute.Value.String()
	}
	if attributes["alarmd.query_group"] == "" || attributes["alarmd.evaluation_time"] == "" || attributes["alarmd.outcome"] == "" {
		t.Fatalf("Slot span attributes %v, want the Query Group, the evaluation time and the outcome", attributes)
	}
	for _, span := range spans {
		if span.Name == "alarmd.query" && (!bytes.Equal(span.ParentSpanId, slot.SpanId) || !bytes.Equal(span.TraceId, slot.TraceId)) {
			t.Fatalf("query span %v is not under the Slot span", span)
		}
	}
	for _, parent := range next.sent {
		if len(parent) != 55 || parent[53:] != "01" {
			t.Fatalf("query sent traceparent %q, want a sampled context", parent)
		}
	}
	if got := counterValue(t, recorder, "alarmd_trace_slots_total", map[string]string{"decision": tracing.DecisionSampled}); got != 1 {
		t.Fatalf("trace_slots_total{sampled} = %v, want 1", got)
	}

	off := &slotExecutorForTest{queries: 1}
	_, _ = observedProductionSlotExecutor{next: off, observer: observability.ObserverFunc(func(context.Context, observability.Observation) {})}.Execute(context.Background(), request)
	if len(off.sent) != 1 || off.sent[0][53:] != "00" {
		t.Fatalf("off: query sent %v, want one unsampled traceparent", off.sent)
	}
}
