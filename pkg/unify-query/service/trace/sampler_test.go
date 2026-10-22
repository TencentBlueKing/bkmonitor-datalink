// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package trace

import (
	"context"
	"testing"

	"github.com/spf13/viper"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// always 记录每个请求，与之前一致；parent_based 按上游的 sampled 标志：00 的请求不记录，但 span
// 仍带上游的 trace id；01 和没有上游的请求照旧记录。未知取值拒绝。
func TestTheSamplerFollowsTheConfiguredMode(t *testing.T) {
	traceID, _ := oteltrace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := oteltrace.SpanIDFromHex("00f067aa0ba902b7")
	parent := func(flags oteltrace.TraceFlags) context.Context {
		return oteltrace.ContextWithRemoteSpanContext(context.Background(), oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
			TraceID: traceID, SpanID: spanID, TraceFlags: flags, Remote: true}))
	}
	for _, testCase := range []struct {
		sampler   string
		ctx       context.Context
		recording bool
	}{
		{sampler: SamplerAlways, ctx: parent(0), recording: true},
		{sampler: "", ctx: parent(0), recording: true},
		{sampler: SamplerParentBased, ctx: parent(0), recording: false},
		{sampler: SamplerParentBased, ctx: parent(oteltrace.FlagsSampled), recording: true},
		{sampler: SamplerParentBased, ctx: context.Background(), recording: true},
	} {
		sampler, err := newSampler(testCase.sampler)
		if err != nil {
			t.Fatal(err)
		}
		provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sampler))
		_, span := provider.Tracer("t").Start(testCase.ctx, "request")
		if span.IsRecording() != testCase.recording {
			t.Errorf("sampler %q: recording %v, want %v", testCase.sampler, span.IsRecording(), testCase.recording)
		}
		if testCase.ctx != context.Background() && span.SpanContext().TraceID() != traceID {
			t.Errorf("sampler %q: trace id %s, want the upstream's", testCase.sampler, span.SpanContext().TraceID())
		}
		span.End()
	}
	if _, err := newSampler("sometimes"); err == nil {
		t.Fatal("an unknown sampler was accepted")
	}
}

// trace.sampler 从配置读入后，Start 构造的 provider 照它采样：写 parent_based 时上游 00 的请求不记录；
// 不写时与之前一样全部记录。
func TestTheConfiguredSamplerReachesTheProvider(t *testing.T) {
	previous := viper.Get(SamplerConfigPath)
	t.Cleanup(func() {
		viper.Set(SamplerConfigPath, previous)
		InitConfig()
	})
	traceID, _ := oteltrace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := oteltrace.SpanIDFromHex("00f067aa0ba902b7")
	unsampled := oteltrace.ContextWithRemoteSpanContext(context.Background(), oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, Remote: true}))
	for _, testCase := range []struct {
		configured any
		recording  bool
	}{
		{configured: SamplerParentBased, recording: false},
		{configured: nil, recording: true},
	} {
		viper.Set(SamplerConfigPath, testCase.configured)
		InitConfig()
		provider, err := newTracerProvider(tracetest.NewInMemoryExporter(), resource.Empty())
		if err != nil {
			t.Fatal(err)
		}
		_, span := provider.Tracer("t").Start(unsampled, "request")
		if span.IsRecording() != testCase.recording {
			t.Errorf("trace.sampler %v: an unsampled upstream's request recording %v, want %v", testCase.configured, span.IsRecording(), testCase.recording)
		}
		span.End()
		_ = provider.Shutdown(context.Background())
	}
}
