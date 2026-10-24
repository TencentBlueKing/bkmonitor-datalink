// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tracing

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

var unsampledParent = regexp.MustCompile(`^00-([0-9a-f]{32})-[0-9a-f]{16}-00$`)

type countersForTest struct {
	mu        sync.Mutex
	decisions map[string]int
	dropped   map[string]int
}

func (counters *countersForTest) TraceSlotDecided(decision string) {
	counters.mu.Lock()
	defer counters.mu.Unlock()
	if counters.decisions == nil {
		counters.decisions = map[string]int{}
	}
	counters.decisions[decision]++
}

func (counters *countersForTest) TraceSpansDropped(reason string, spans int) {
	counters.mu.Lock()
	defer counters.mu.Unlock()
	if counters.dropped == nil {
		counters.dropped = map[string]int{}
	}
	counters.dropped[reason] += spans
}

func (counters *countersForTest) read() (map[string]int, map[string]int) {
	counters.mu.Lock()
	defer counters.mu.Unlock()
	decisions, dropped := map[string]int{}, map[string]int{}
	for key, value := range counters.decisions {
		decisions[key] = value
	}
	for key, value := range counters.dropped {
		dropped[key] = value
	}
	return decisions, dropped
}

// exporterForTest keeps what it is sent, or refuses it.
type exporterForTest struct {
	mu     sync.Mutex
	spans  []sdktrace.ReadOnlySpan
	refuse bool
}

func (exporter *exporterForTest) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	if exporter.refuse {
		return errors.New("collector refused")
	}
	exporter.spans = append(exporter.spans, spans...)
	return nil
}

func (exporter *exporterForTest) Shutdown(context.Context) error { return nil }

func (exporter *exporterForTest) exported() []sdktrace.ReadOnlySpan {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	return append([]sdktrace.ReadOnlySpan(nil), exporter.spans...)
}

func testExporter(t *testing.T, ratio float64, rate int) (*Exporter, *exporterForTest, *countersForTest) {
	t.Helper()
	sink, counters := &exporterForTest{}, &countersForTest{}
	exporter := newExporter(Config{SampleRatio: ratio, MaxTracesPerSecond: rate, ServiceVersion: "test", InstanceID: "worker-1"}, counters, sink)
	t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })
	return exporter, sink, counters
}

func flush(t *testing.T, exporter *Exporter) {
	t.Helper()
	if err := exporter.provider.ForceFlush(testwait.Context(t)); err != nil {
		t.Fatal(err)
	}
}

// Off, there is nothing to build and nothing is sent anywhere: every query
// gets a fresh trace, not sampled, and its trace id for the failure line.
func TestOffEveryQueryIsAFreshUnsampledTrace(t *testing.T) {
	exporter, err := New(Config{}, &countersForTest{})
	if err != nil || exporter != nil {
		t.Fatalf("New(no endpoint) = (%v, %v), want no export", exporter, err)
	}
	ctx, slot := exporter.StartSlot(context.Background(), "qg", 60)
	query, parent, traceID := StartQuery(ctx, "uq", 1, 1)
	match := unsampledParent.FindStringSubmatch(parent)
	if slot != nil || query != nil || match == nil || match[1] != traceID {
		t.Fatalf("off: slot %v query %v traceparent %q id %q, want none, none and a fresh unsampled trace", slot, query, parent, traceID)
	}
	slot.End("success", "", false)
	query.End("COMPLETE", "", false)
}

// A sampled Slot exports one root span and a span per query attempt under
// it; what the query service is sent is that query span's own context,
// sampled. A failed attempt says its reason code and nothing else.
func TestASampledSlotExportsItsQueriesUnderIt(t *testing.T) {
	exporter, sink, counters := testExporter(t, 1, 10)
	ctx, slot := exporter.StartSlot(context.Background(), "qg-7", 1_800_000_000)
	first, firstParent, firstID := StartQuery(ctx, "uq", 2, 1)
	second, secondParent, _ := StartQuery(ctx, "uq", 2, 2)
	first.End("COMPLETE", "", false)
	second.End("UNAVAILABLE", "QUERY_TIMEOUT", true)
	slot.End("failed", "QUERY_TIMEOUT", true)
	flush(t, exporter)
	spans := sink.exported()
	if len(spans) != 3 {
		t.Fatalf("exported %d spans, want the Slot's and two queries'", len(spans))
	}
	byName := map[string][]sdktrace.ReadOnlySpan{}
	for _, span := range spans {
		byName[span.Name()] = append(byName[span.Name()], span)
	}
	root := byName["alarmd.slot"][0]
	if root.Parent().IsValid() {
		t.Fatalf("the Slot span has parent %v, want a root", root.Parent())
	}
	for index, query := range byName["alarmd.query"] {
		if query.Parent().SpanID() != root.SpanContext().SpanID() || query.SpanContext().TraceID() != root.SpanContext().TraceID() {
			t.Fatalf("query span %d is not under the Slot span", index)
		}
	}
	for _, parent := range []string{firstParent, secondParent} {
		if !strings.HasPrefix(parent, "00-"+root.SpanContext().TraceID().String()+"-") || !strings.HasSuffix(parent, "-01") {
			t.Fatalf("traceparent %q, want the Slot's trace, sampled", parent)
		}
	}
	if firstID != root.SpanContext().TraceID().String() {
		t.Fatalf("trace id for the failure line %q, want the Slot's trace", firstID)
	}
	var failed sdktrace.ReadOnlySpan
	for _, query := range byName["alarmd.query"] {
		if query.Status().Code == codes.Error {
			failed = query
		}
	}
	if failed == nil || failed.Status().Description != "QUERY_TIMEOUT" || !strings.HasSuffix(secondParent, failed.SpanContext().SpanID().String()+"-01") {
		t.Fatalf("the failed attempt's span = %+v, want status QUERY_TIMEOUT and its own context sent", failed)
	}
	if decisions, _ := counters.read(); decisions[DecisionSampled] != 1 {
		t.Fatalf("decisions = %v, want one sampled", decisions)
	}
}

// A ratio of 0 samples nothing, and a query still carries a trace id it can
// be found by; a rate of 1 a second lets one Slot of three through.
func TestSamplingIsBoundedByTheRatioAndTheRate(t *testing.T) {
	none, sink, counters := testExporter(t, 0, 10)
	// The lowest draw there is: a ratio of 0 still samples nothing.
	none.random = func() float64 { return 0 }
	ctx, _ := none.StartSlot(context.Background(), "qg", 60)
	if query, parent, traceID := StartQuery(ctx, "uq", 1, 1); query != nil || unsampledParent.FindStringSubmatch(parent) == nil || traceID == "" {
		t.Fatalf("ratio 0: query %v traceparent %q id %q, want no span and an unsampled trace with an id", query, parent, traceID)
	}
	flush(t, none)
	if decisions, _ := counters.read(); len(sink.exported()) != 0 || decisions[DecisionNotSampledRatio] != 1 {
		t.Fatalf("ratio 0: %d spans, decisions %v", len(sink.exported()), decisions)
	}

	limited, _, counters := testExporter(t, 1, 1)
	at := time.Unix(1_800_000_000, 0)
	limited.bucket = newTokenBucket(1, func() time.Time { return at })
	for range 3 {
		_, slot := limited.StartSlot(context.Background(), "qg", 60)
		slot.End("success", "", false)
	}
	if decisions, _ := counters.read(); decisions[DecisionSampled] != 1 || decisions[DecisionRateLimited] != 2 {
		t.Fatalf("rate 1 with three Slots in one second: decisions %v, want 1 sampled and 2 rate_limited", decisions)
	}
	at = at.Add(time.Second)
	if _, slot := limited.StartSlot(context.Background(), "qg", 60); slot.span == nil {
		t.Fatal("a second later the bucket has a token again")
	}
}

// A Slot exports at most MaxQuerySpansPerSlot query spans; the rest send the
// Slot span's context, so nothing hangs under a span that was not exported,
// and the Slot span says how many it left out.
func TestASlotExportsAtMostItsCapOfQuerySpans(t *testing.T) {
	exporter, sink, counters := testExporter(t, 1, 10)
	ctx, slot := exporter.StartSlot(context.Background(), "qg", 60)
	var parents []string
	for attempt := range 40 {
		query, parent, _ := StartQuery(ctx, "uq", 1, uint32(attempt+1))
		parents = append(parents, parent)
		query.End("COMPLETE", "", false)
	}
	slot.End("success", "", false)
	flush(t, exporter)
	queries, exported := 0, map[string]bool{}
	var root sdktrace.ReadOnlySpan
	for _, span := range sink.exported() {
		exported[span.SpanContext().SpanID().String()] = true
		switch span.Name() {
		case "alarmd.query":
			queries++
		case "alarmd.slot":
			root = span
		}
	}
	if queries != MaxQuerySpansPerSlot || root == nil {
		t.Fatalf("exported %d query spans, want %d and the Slot's", queries, MaxQuerySpansPerSlot)
	}
	for _, attribute := range root.Attributes() {
		if attribute.Key == "alarmd.query.spans_dropped" && attribute.Value.AsInt64() != 8 {
			t.Fatalf("spans_dropped = %d, want 8", attribute.Value.AsInt64())
		}
	}
	for index, parent := range parents {
		spanID := strings.Split(parent, "-")[2]
		if !exported[spanID] {
			t.Fatalf("query %d sent parent %s, which was never exported", index, spanID)
		}
	}
	if _, dropped := counters.read(); dropped[DropPerSlotCap] != 8 {
		t.Fatalf("dropped = %v, want 8 per_slot_cap", dropped)
	}
}

// A collector that refuses never holds a Slot: its spans are counted lost,
// and the refusal is handed on for its line.
func TestARefusingCollectorCostsSpansNotSlots(t *testing.T) {
	sink, counters := &exporterForTest{refuse: true}, &countersForTest{}
	var mu sync.Mutex
	var refusals []error
	exporter := newExporter(Config{SampleRatio: 1, MaxTracesPerSecond: 10, ExportFailed: func(err error) {
		mu.Lock()
		defer mu.Unlock()
		refusals = append(refusals, err)
	}}, counters, sink)
	t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })
	started := time.Now()
	ctx, slot := exporter.StartSlot(context.Background(), "qg", 60)
	query, _, _ := StartQuery(ctx, "uq", 1, 1)
	query.End("COMPLETE", "", false)
	slot.End("success", "", false)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("the Slot took %s with a refusing collector", elapsed)
	}
	flush(t, exporter)
	if _, dropped := counters.read(); dropped[DropExportFailed] != 2 {
		t.Fatalf("dropped = %v, want both spans export_failed", dropped)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(refusals) != 1 {
		t.Fatalf("refusals handed on = %v, want the one batch", refusals)
	}
}

// A full queue drops the span that would not fit and counts it.
func TestAFullQueueDropsAndCounts(t *testing.T) {
	counters := &countersForTest{}
	blocked := make(chan struct{})
	processor := &boundedProcessor{exporter: &exporterForTest{}, counters: counters, queue: make(chan sdktrace.ReadOnlySpan, 1),
		flush: make(chan chan struct{}), stop: make(chan struct{}), done: blocked}
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanProcessor(processor))
	for range 3 {
		_, span := provider.Tracer("t").Start(context.Background(), "s")
		span.End()
	}
	if _, dropped := counters.read(); dropped[DropQueueFull] != 2 {
		t.Fatalf("dropped = %v, want two queue_full past a queue of one", dropped)
	}
}

// otlpExport sends one sampled Slot with one query through an exporter built
// for cfg to a collector on real OTLP/HTTP, and is what the collector
// received: the paths it was sent to, the resource's attributes and the
// spans.
func otlpExport(t *testing.T, cfg Config) ([]string, map[string]string, int) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	var requests []*collector.ExportTraceServiceRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		decoded := &collector.ExportTraceServiceRequest{}
		_ = proto.Unmarshal(body, decoded)
		mu.Lock()
		paths, requests = append(paths, request.URL.Path), append(requests, decoded)
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/x-protobuf")
		raw, _ := proto.Marshal(&collector.ExportTraceServiceResponse{})
		_, _ = writer.Write(raw)
	}))
	t.Cleanup(server.Close)
	cfg.Endpoint = server.URL
	exporter, err := New(cfg, &countersForTest{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, slot := exporter.StartSlot(context.Background(), "qg", 60)
	query, _, _ := StartQuery(ctx, "uq", 1, 1)
	query.End("COMPLETE", "", false)
	slot.End("success", "", false)
	// A flush the case expects to finish, bounded by the test's deadline.
	shutdown := testwait.Context(t)
	if err := exporter.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	attributes := map[string]string{}
	spans := 0
	for _, request := range requests {
		for _, resource := range request.ResourceSpans {
			for _, attribute := range resource.Resource.Attributes {
				attributes[attribute.Key] = attribute.Value.GetStringValue()
			}
			for _, scope := range resource.ScopeSpans {
				spans += len(scope.Spans)
			}
		}
	}
	return paths, attributes, spans
}

// Over real OTLP/HTTP: spans go to /v1/traces on the endpoint, the
// resource carries the service and the token, and the token is the only
// place it appears.
func TestTheExportSpeaksOTLPWithTheToken(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	paths, attributes, spans := otlpExport(t, Config{Token: "token-for-test", SampleRatio: 1, MaxTracesPerSecond: 5,
		ServiceVersion: "0.4.0", InstanceID: "worker-1"})
	if len(paths) == 0 || paths[0] != "/v1/traces" {
		t.Fatalf("collector saw %v, want a request to /v1/traces", paths)
	}
	if spans != 2 || attributes["service.name"] != "alarmd" || attributes["service.version"] != "0.4.0" ||
		attributes["service.instance.id"] != "worker-1" || attributes[TokenAttribute] != "token-for-test" {
		t.Fatalf("collector got %d spans with resource %v", spans, attributes)
	}
}

// The resource the collector gets names the SDK the spans come from, with
// the values the SDK's own default resource gives them - a provider given a
// resource of its own does not get the default's - beside everything it
// carried before, the token among them, and nothing else. The environment's
// resource attributes are merged under it as they were: one of their own is
// kept, and one alarmd sets itself is alarmd's.
func TestTheExportedResourceNamesItsSDKBesideWhatItCarried(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment=test,service.name=other")
	own, err := resource.New(context.Background(), resource.WithTelemetrySDK())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"service.name": "alarmd", "service.version": "0.4.0", "service.instance.id": "worker-1",
		TokenAttribute: "token-for-test", "deployment.environment": "test"}
	for _, attribute := range own.Attributes() {
		want[string(attribute.Key)] = attribute.Value.Emit()
	}
	if len(want) != 8 || want["telemetry.sdk.language"] != "go" || want["telemetry.sdk.version"] == "" {
		t.Fatalf("the SDK names itself %v", own.Attributes())
	}
	_, attributes, spans := otlpExport(t, Config{Token: "token-for-test", SampleRatio: 1, MaxTracesPerSecond: 5,
		ServiceVersion: "0.4.0", InstanceID: "worker-1"})
	if spans != 2 || !reflect.DeepEqual(attributes, want) {
		t.Fatalf("collector got %d spans with resource %v, want %v", spans, attributes, want)
	}
}

// The deployment names the instance: a service.instance.id in the
// environment's resource (OTEL_RESOURCE_ATTRIBUTES, which the chart sets to
// the Pod's namespace, name and container beside the k8s.* attributes) is
// the one the collector gets, with those attributes and alarmd's own, the
// token and the SDK among them, and nothing else. A process the deployment
// does not name, or names as empty, is its worker id.
func TestTheDeploymentNamesTheInstanceAndTheWorkerIDIsTheFallback(t *testing.T) {
	own, err := resource.New(context.Background(), resource.WithTelemetrySDK())
	if err != nil {
		t.Fatal(err)
	}
	podAttributes := "k8s.namespace.name=ns1,k8s.pod.name=alarmd-0,k8s.pod.uid=uid-1,k8s.container.name=alarmd"
	for _, tc := range []struct {
		name, environment string
		want              map[string]string
	}{
		{name: "named by the deployment", environment: "service.instance.id=ns1.alarmd-0.alarmd," + podAttributes,
			want: map[string]string{"service.instance.id": "ns1.alarmd-0.alarmd", "k8s.namespace.name": "ns1",
				"k8s.pod.name": "alarmd-0", "k8s.pod.uid": "uid-1", "k8s.container.name": "alarmd"}},
		{name: "not named", environment: podAttributes,
			want: map[string]string{"service.instance.id": "worker-1", "k8s.namespace.name": "ns1",
				"k8s.pod.name": "alarmd-0", "k8s.pod.uid": "uid-1", "k8s.container.name": "alarmd"}},
		{name: "named as empty", environment: "service.instance.id=", want: map[string]string{"service.instance.id": "worker-1"}},
		{name: "no environment", want: map[string]string{"service.instance.id": "worker-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_SERVICE_NAME", "")
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", tc.environment)
			want := map[string]string{"service.name": "alarmd", "service.version": "0.4.0", TokenAttribute: "token-for-test"}
			for _, attribute := range own.Attributes() {
				want[string(attribute.Key)] = attribute.Value.Emit()
			}
			for key, value := range tc.want {
				want[key] = value
			}
			_, attributes, spans := otlpExport(t, Config{Token: "token-for-test", SampleRatio: 1, MaxTracesPerSecond: 5,
				ServiceVersion: "0.4.0", InstanceID: "worker-1"})
			if spans != 2 || !reflect.DeepEqual(attributes, want) {
				t.Fatalf("collector got %d spans with resource %v, want %v", spans, attributes, want)
			}
		})
	}
}

func TestTheEndpointIsAHostAndNothingElse(t *testing.T) {
	for endpoint, valid := range map[string]bool{
		"http://collector:4318": true, "https://collector": true, "http://collector:4318/": true,
		"collector:4318": false, "http://collector:4318/v1/traces": false, "http://user:pass@collector": false,
		"ftp://collector": false, "http://collector?x=1": false, "http://": false,
	} {
		if err := ValidateEndpoint(endpoint); (err == nil) != valid {
			t.Errorf("ValidateEndpoint(%q) = %v, want valid %v", endpoint, err, valid)
		}
	}
}
