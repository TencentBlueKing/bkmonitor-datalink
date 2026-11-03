// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package log

import (
	"context"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// 上游 sampled 为 00 的请求在 parent_based 采样下不记录 span，但它的日志行仍带上游的 trace id：
// 调用方写在失败行里的 trace id 要能在这里查到。
func TestALogLineOfAnUnsampledRequestCarriesItsTraceID(t *testing.T) {
	traceID, _ := oteltrace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := oteltrace.SpanIDFromHex("00f067aa0ba902b7")
	upstream := oteltrace.ContextWithRemoteSpanContext(context.Background(), oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, Remote: true}))
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())))
	ctx, span := provider.Tracer("t").Start(upstream, "request")
	defer span.End()
	if span.IsRecording() {
		t.Fatal("an unsampled upstream's request was recorded")
	}
	if line := withTraceID(ctx, "query failed"); !strings.HasPrefix(line, "["+traceID.String()+"] ") {
		t.Fatalf("log line %q, want the upstream trace id", line)
	}
}
