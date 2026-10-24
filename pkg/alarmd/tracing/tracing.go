// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package tracing exports alarmd's own spans: one alarmd.slot span for each
// Slot it samples, and an alarmd.query span under it for each query attempt,
// whose context is the traceparent the query service receives. Sampling is
// decided once per Slot, by ratio and then by a token bucket, so the export
// is bounded in traces a second whatever the Slot count, and every query and
// retry of a Slot follows its decision: no query span ever hangs under a Slot
// span that was not exported.
package tracing

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// The decisions a Slot's sampling ends in, and why exported spans are lost.
const (
	DecisionSampled         = "sampled"
	DecisionNotSampledRatio = "not_sampled_ratio"
	DecisionRateLimited     = "rate_limited"

	DropQueueFull    = "queue_full"
	DropExportFailed = "export_failed"
	DropPerSlotCap   = "per_slot_cap"
)

// Decisions and DropReasons are the closed label sets of the export's
// counters.
var (
	Decisions   = []string{DecisionSampled, DecisionNotSampledRatio, DecisionRateLimited}
	DropReasons = []string{DropQueueFull, DropExportFailed, DropPerSlotCap}
)

const (
	// MaxQuerySpansPerSlot bounds the query spans one Slot exports, so the
	// traces-a-second bound is also a spans-a-second bound however many
	// queries a Slot fans out to. A query past it sends the Slot span's
	// context, so the query service's span hangs under a span that exists.
	MaxQuerySpansPerSlot = 32
	// TokenAttribute is the resource attribute the collector reads the APM
	// application's token from.
	TokenAttribute = "bk.data.token"
	tracesPath     = "/v1/traces"
	exportTimeout  = 10 * time.Second
)

// Config is the deployment's span export. An empty Endpoint is off.
type Config struct {
	Endpoint           string
	Token              string
	SampleRatio        float64
	MaxTracesPerSecond int
	ServiceVersion     string
	// InstanceID is the span resource's service.instance.id when the
	// environment's resource does not name one (environmentInstance).
	InstanceID string
	// ExportFailed hears a batch the collector did not take, beside its
	// count; the process writes it as a bounded line. Nil hears nothing.
	ExportFailed func(err error)
}

// Counters hears the export's decisions and losses; the process wires its
// metric recorder here.
type Counters interface {
	TraceSlotDecided(decision string)
	TraceSpansDropped(reason string, spans int)
}

// Exporter samples Slots and exports their spans. A nil Exporter is the
// export turned off: no span is built, and every query is sent a fresh
// trace id with the sampled flag unset.
type Exporter struct {
	provider  *sdktrace.TracerProvider
	tracer    trace.Tracer
	processor *boundedProcessor
	ratio     float64
	bucket    *tokenBucket
	counters  Counters
	random    func() float64
}

// ValidateEndpoint is the check the deployment's endpoint is read under: an
// http or https URL naming a host, with no path, query or credentials - the
// exporter sends to /v1/traces on it.
func ValidateEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("must be an http or https URL naming a host and nothing else, such as http://collector:4318")
	}
	return nil
}

// New builds the export for cfg; nil when cfg has no endpoint.
func New(cfg Config, counters Counters) (*Exporter, error) {
	if cfg.Endpoint == "" {
		return nil, nil
	}
	if err := ValidateEndpoint(cfg.Endpoint); err != nil {
		return nil, fmt.Errorf("alarmd tracing: endpoint %w", err)
	}
	if cfg.SampleRatio < 0 || cfg.SampleRatio > 1 || math.IsNaN(cfg.SampleRatio) || cfg.MaxTracesPerSecond < 1 || counters == nil {
		return nil, errors.New("alarmd tracing: a sample ratio within 0..1, a positive trace rate and counters are required")
	}
	parsed, _ := url.Parse(cfg.Endpoint)
	client, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpointURL(parsed.Scheme+"://"+parsed.Host+tracesPath),
		otlptracehttp.WithTimeout(exportTimeout),
		// The processor bounds the work; a retry inside the exporter would
		// hold a batch past it.
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}))
	if err != nil {
		return nil, fmt.Errorf("alarmd tracing: build the exporter: %w", err)
	}
	return newExporter(cfg, counters, client), nil
}

func newExporter(cfg Config, counters Counters, exporter sdktrace.SpanExporter) *Exporter {
	attributes := []attribute.KeyValue{
		attribute.String("service.name", "alarmd"),
		attribute.String("service.version", cfg.ServiceVersion),
		// The SDK the spans come from, named as the SDK's own default
		// resource names it. A provider given a resource of its own keeps
		// that and the environment's (OTEL_RESOURCE_ATTRIBUTES), not the
		// default, so without these the spans would not say which SDK made
		// them.
		attribute.String("telemetry.sdk.name", "opentelemetry"),
		attribute.String("telemetry.sdk.language", "go"),
		attribute.String("telemetry.sdk.version", sdk.Version()),
	}
	if !environmentInstance() {
		attributes = append(attributes, attribute.String(instanceAttribute, cfg.InstanceID))
	}
	if cfg.Token != "" {
		attributes = append(attributes, attribute.String(TokenAttribute, cfg.Token))
	}
	processor := newBoundedProcessor(exporter, counters, cfg.ExportFailed)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.NewSchemaless(attributes...)),
		// Every span built here belongs to a Slot that was sampled already.
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(processor),
	)
	return &Exporter{provider: provider, tracer: provider.Tracer("alarmd"), processor: processor,
		ratio: cfg.SampleRatio, bucket: newTokenBucket(cfg.MaxTracesPerSecond, time.Now), counters: counters,
		random: randomFraction}
}

// instanceAttribute is the resource attribute naming the process.
const instanceAttribute = "service.instance.id"

// environmentInstance says whether the environment's resource
// (OTEL_RESOURCE_ATTRIBUTES, read as sdktrace.WithResource reads it to
// merge under the resource built here) names a service.instance.id. Then
// the instance is the deployment's: it names the Pod in terms a trace
// backend links to the Pod by, beside the k8s.* attributes it sets with it,
// which the worker id alone cannot. The worker id is the instance of a
// process the deployment does not name, and of one it names as empty.
func environmentInstance() bool {
	value, named := resource.Environment().Set().Value(instanceAttribute)
	return named && value.AsString() != ""
}

// Shutdown sends what is queued, within ctx.
func (exporter *Exporter) Shutdown(ctx context.Context) error {
	if exporter == nil {
		return nil
	}
	return exporter.provider.Shutdown(ctx)
}

// slotKey carries a Slot's decision to its queries.
type slotKey struct{}

// Slot is one Slot's span, or its decision not to have one.
type Slot struct {
	exporter *Exporter
	span     trace.Span
	queries  atomic.Int32
	dropped  atomic.Int32
}

// StartSlot decides whether this Slot is exported and returns the context its
// queries read the decision from.
func (exporter *Exporter) StartSlot(ctx context.Context, queryGroup string, evaluationTime int64) (context.Context, *Slot) {
	if exporter == nil {
		return ctx, nil
	}
	decision := DecisionSampled
	switch {
	case exporter.random() >= exporter.ratio:
		decision = DecisionNotSampledRatio
	case !exporter.bucket.take():
		decision = DecisionRateLimited
	}
	exporter.counters.TraceSlotDecided(decision)
	slot := &Slot{exporter: exporter}
	if decision == DecisionSampled {
		// A Slot's span is a root: whatever context the scheduler carries,
		// the trace begins here.
		ctx, slot.span = exporter.tracer.Start(ctx, "alarmd.slot", trace.WithNewRoot(),
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(attribute.String("alarmd.query_group", queryGroup),
				attribute.Int64("alarmd.evaluation_time", evaluationTime)))
	}
	return context.WithValue(ctx, slotKey{}, slot), slot
}

// End closes the Slot's span with its outcome and reason code. The status
// description is the reason code alone: an error's text can carry addresses.
func (slot *Slot) End(outcome, reason string, failed bool) {
	if slot == nil || slot.span == nil {
		return
	}
	slot.span.SetAttributes(attribute.String("alarmd.outcome", outcome), attribute.String("alarmd.reason", reason),
		attribute.Int64("alarmd.query.spans_dropped", int64(slot.dropped.Load())))
	if failed {
		slot.span.SetStatus(codes.Error, reason)
	}
	slot.span.End()
}

// Query is one query attempt's span; nil when the attempt has none.
type Query struct{ span trace.Span }

// StartQuery is a query attempt under the Slot ctx carries: its span when the
// Slot was sampled and is under its cap, and the traceparent to send. A Slot
// over its cap sends its own context; an unsampled Slot, or a read that is no
// Slot's, sends a fresh trace id with the sampled flag unset. The trace id is
// returned either way, for the failure line.
func StartQuery(ctx context.Context, provider string, strategies int, attempt uint32) (*Query, string, string) {
	slot, _ := ctx.Value(slotKey{}).(*Slot)
	if slot == nil || slot.span == nil {
		parent, traceID := freshTraceParent()
		return nil, parent, traceID
	}
	if slot.queries.Add(1) > MaxQuerySpansPerSlot {
		slot.dropped.Add(1)
		slot.exporter.counters.TraceSpansDropped(DropPerSlotCap, 1)
		parent, traceID := traceParentOf(slot.span.SpanContext())
		return nil, parent, traceID
	}
	_, span := slot.exporter.tracer.Start(trace.ContextWithSpan(ctx, slot.span), "alarmd.query",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("alarmd.provider", provider), attribute.Int("alarmd.strategies", strategies),
			attribute.Int64("alarmd.attempt", int64(attempt))))
	parent, traceID := traceParentOf(span.SpanContext())
	return &Query{span: span}, parent, traceID
}

// End closes the attempt's span with its outcome and reason code.
func (query *Query) End(outcome, reason string, failed bool) {
	if query == nil {
		return
	}
	query.span.SetAttributes(attribute.String("alarmd.outcome", outcome), attribute.String("alarmd.reason", reason))
	if failed {
		query.span.SetStatus(codes.Error, reason)
	}
	query.span.End()
}

func traceParentOf(context trace.SpanContext) (string, string) {
	traceID := context.TraceID().String()
	return "00-" + traceID + "-" + context.SpanID().String() + "-01", traceID
}

// freshTraceParent is a W3C traceparent of a new random trace, not sampled:
// nobody records it on this side, which is what the flag says.
func freshTraceParent() (string, string) {
	var ids [24]byte
	_, _ = rand.Read(ids[:])
	traceID := hex.EncodeToString(ids[:16])
	return "00-" + traceID + "-" + hex.EncodeToString(ids[16:]) + "-00", traceID
}

func randomFraction() float64 {
	var bits [8]byte
	_, _ = rand.Read(bits[:])
	return float64(binary.BigEndian.Uint64(bits[:])>>11) / (1 << 53)
}

// tokenBucket admits rate events a second, holding at most rate tokens.
type tokenBucket struct {
	mu     sync.Mutex
	rate   float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

func newTokenBucket(rate int, now func() time.Time) *tokenBucket {
	return &tokenBucket{rate: float64(rate), tokens: float64(rate), last: now(), now: now}
}

func (bucket *tokenBucket) take() bool {
	bucket.mu.Lock()
	defer bucket.mu.Unlock()
	now := bucket.now()
	if elapsed := now.Sub(bucket.last).Seconds(); elapsed > 0 {
		bucket.tokens = math.Min(bucket.rate, bucket.tokens+elapsed*bucket.rate)
	}
	bucket.last = now
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}
