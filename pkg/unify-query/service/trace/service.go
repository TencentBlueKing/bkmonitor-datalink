// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trace

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.10.0"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// Service
type Service struct {
	tracerProvider *sdktrace.TracerProvider

	wg         sync.WaitGroup
	ctx        context.Context
	cancelFunc context.CancelFunc
}

// Type
func (s *Service) Type() string {
	return "trace"
}

// newHTTPClient
func (s *Service) newHTTPClient() otlptrace.Client {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(strings.Join([]string{otlpHost, otlpPort}, ":")),
		otlptracehttp.WithInsecure(),
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{
			Enabled:         true,
			InitialInterval: time.Nanosecond,
			MaxInterval:     time.Nanosecond,
			MaxElapsedTime:  5,
		}),
	}
	if viper.GetBool("kms.enabled") {
		opts = append(opts, otlptracehttp.WithHeaders(viper.GetStringMapString("trace.otlp.headers")))
	}
	client := otlptracehttp.NewClient(opts...)
	return client
}

// newGrpcClient
func (s *Service) newGrpcClient() otlptrace.Client {
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(strings.Join([]string{otlpHost, otlpPort}, ":")),
		otlptracegrpc.WithInsecure(),
	}
	if viper.GetBool("kms.enabled") {
		opts = append(opts, otlptracegrpc.WithHeaders(viper.GetStringMapString("trace.otlp.headers")))
	}
	client := otlptracegrpc.NewClient(opts...)
	return client
}

// newResource
func (s *Service) newResource() *resource.Resource {
	attributes := []attribute.KeyValue{
		semconv.ServiceNameKey.String(ServiceName),
		attribute.Key("bk.data.token").String(otlpToken),
	}
	hostname, ip := metadata.GetLocalHost()
	instanceHost := ip
	if instanceHost == "" {
		instanceHost = hostname
	}
	if instanceHost != "" {
		attributes = append(attributes, attribute.String("service.instance.id", ServiceName+"@"+instanceHost))
	}
	return resource.NewWithAttributes(semconv.SchemaURL, attributes...)
}

// Start
func (s *Service) Start(ctx context.Context) {
	var (
		client   otlptrace.Client
		exporter *otlptrace.Exporter
		err      error
	)

	if !Enable {
		return
	}

	switch OtlpType {
	case "http":
		client = s.newHTTPClient()
	case "grpc":
		client = s.newGrpcClient()
	default:
		panic("unknown trace otlp type")
	}

	exporter, err = otlptrace.New(ctx, client)
	if err != nil {
		// Exporter errors may contain request headers; report the failed stage only.
		log.Errorf(ctx, "trace exporter initialization failed (protocol=%s)", OtlpType)
		return
	}

	// 这里的wg不是用于goroutine运行判断，需要注意
	s.wg.Add(1)

	s.tracerProvider, err = newTracerProvider(exporter, s.newResource())
	if err != nil {
		panic(err)
	}
	otel.SetTracerProvider(s.tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	s.ctx, s.cancelFunc = context.WithCancel(ctx)
}

// Reload
func (s *Service) Reload(ctx context.Context) {
	s.Close()
	s.Start(ctx)
}

// Close
func (s *Service) Close() {
	if s.tracerProvider == nil {
		return
	}

	go func(tracerProvider *sdktrace.TracerProvider) {
		defer s.wg.Done()
		if err := tracerProvider.Shutdown(s.ctx); err != nil {
			log.Errorf(context.TODO(), "failed to shutdown TracerProvider: %v", err)
		}
	}(s.tracerProvider)
}

// Wait
func (s *Service) Wait() {
	s.wg.Wait()
}

// newSampler 按配置构造采样器。always 记录所有请求；parent_based 时带上游 traceparent 的请求按其
// sampled 标志决定，标志为 00 的请求只建不记录的 span，span context 仍带上游的 trace id，
// 日志行照样能用它关联；没有上游的请求照旧全记录。
func newSampler(name string) (sdktrace.Sampler, error) {
	switch name {
	case "", SamplerAlways:
		return sdktrace.AlwaysSample(), nil
	case SamplerParentBased:
		return sdktrace.ParentBased(sdktrace.AlwaysSample()), nil
	default:
		return nil, fmt.Errorf("unknown trace sampler %q: want %s or %s", name, SamplerAlways, SamplerParentBased)
	}
}

// newTracerProvider 按当前配置（trace.sampler）构造 provider，Start 与用例走同一处。
func newTracerProvider(exporter sdktrace.SpanExporter, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	sampler, err := newSampler(Sampler)
	if err != nil {
		return nil, err
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sampler),
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	), nil
}
