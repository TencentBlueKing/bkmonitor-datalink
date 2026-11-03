// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/tracing"
)

type traceCountersForTest struct{}

func (traceCountersForTest) TraceSlotDecided(string)       {}
func (traceCountersForTest) TraceSpansDropped(string, int) {}

// An attempt under a sampled Slot is a span of its own, ended with the
// completeness it reached and its reason code, and what the provider is
// sent is that span's context, sampled.
func TestAnAttemptUnderASampledSlotIsItsOwnSpan(t *testing.T) {
	var mu sync.Mutex
	var spans []*tracepb.Span
	collectorServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		decoded := &collector.ExportTraceServiceRequest{}
		if proto.Unmarshal(body, decoded) == nil {
			mu.Lock()
			for _, resource := range decoded.ResourceSpans {
				for _, scope := range resource.ScopeSpans {
					spans = append(spans, scope.Spans...)
				}
			}
			mu.Unlock()
		}
		raw, _ := proto.Marshal(&collector.ExportTraceServiceResponse{})
		_, _ = writer.Write(raw)
	}))
	t.Cleanup(collectorServer.Close)
	traces, err := tracing.New(tracing.Config{Endpoint: collectorServer.URL, SampleRatio: 1, MaxTracesPerSecond: 5}, traceCountersForTest{})
	if err != nil {
		t.Fatal(err)
	}
	client, seen := headerServer(t, http.StatusOK, `{"series":[],"status":null,"trace_id":"t","is_partial":false}`)
	ctx, slot := traces.StartSlot(context.Background(), "qg", 60)
	completion, err := client.Execute(ctx, apmAttempt(t), &collectingSink{})
	if err != nil {
		t.Fatal(err)
	}
	slot.End("success", "", false)
	// A flush the case expects to finish, bounded by the test's deadline.
	shutdown := testwait.Context(t)
	if err := traces.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var query *tracepb.Span
	for _, span := range spans {
		if span.Name == "alarmd.query" {
			query = span
		}
	}
	if query == nil {
		t.Fatalf("exported %d spans and no query span", len(spans))
	}
	outcome := ""
	for _, attribute := range query.Attributes {
		if attribute.Key == "alarmd.outcome" {
			outcome = attribute.Value.GetStringValue()
		}
	}
	if outcome != string(completion.Completeness) {
		t.Fatalf("query span outcome %q, want the completeness %q", outcome, completion.Completeness)
	}
	sent := (*seen)[0].Get(headerTraceParent)
	if !strings.HasSuffix(sent, "-01") || !strings.Contains(sent, "-"+hex.EncodeToString(query.SpanId)+"-") {
		t.Fatalf("sent traceparent %q, want the query span's own context, sampled", sent)
	}
}
