// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/http/httptrace"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	stdout "go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.12.0"
)

type ExporterType string

const (
	ExporterStdout ExporterType = "stdout"
	ExporterHttp   ExporterType = "http"
	ExporterGrpc   ExporterType = "grpc"
)

var (
	Tracer = otel.Tracer("")
	Client = http.Client{}
)

func newStdoutExporter() (*stdout.Exporter, error) {
	return stdout.New(stdout.WithPrettyPrint())
}

func newHttpExporter(env string) (*otlptrace.Exporter, error) {
	return otlptracehttp.New(
		context.Background(),
		otlptracehttp.WithEndpoint(fmt.Sprintf("%s:4318", env)),
		otlptracehttp.WithInsecure(),
		otlptracehttp.WithHeaders(map[string]string{"X-BK-METADATA": "my.pod=pod1,my.namespace=ns1"}),
	)
}

func newGrpcExporter(env string) (*otlptrace.Exporter, error) {
	return otlptracegrpc.New(
		context.Background(),
		otlptracegrpc.WithEndpoint(fmt.Sprintf("%s:4317", env)),
		otlptracegrpc.WithInsecure(),
		// otlptracegrpc.WithCompressor("gzip"),	// 可支持 gzip 压缩
	)
}

func GetSpanExporter(et ExporterType) (sdktrace.SpanExporter, error) {
	switch et {
	case ExporterStdout:
		return newStdoutExporter()
	case ExporterHttp:
		return newHttpExporter(ConfEndpoint)
	case ExporterGrpc:
		return newGrpcExporter(ConfEndpoint)
	}
	return nil, errors.New("invalid exporter type")
}

func MustNewResource(token string) *resource.Resource {
	// 或者使用环境变量注入
	// export OTEL_RESOURCE_ATTRIBUTES="bk.data.token=Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="
	r, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("traces-demo"),
			semconv.ServiceInstanceIDKey.String("trace-demo-instance-ID"),
			semconv.ServiceVersionKey.String("v1.0.0"),
			attribute.String("environment", "test"),
			attribute.String("bk.data.token", token),
		),
	)
	// TODO(Note): 这里是刻意而为之，请确保 resource.Default() 里使用的 SchemaURL 和 resource.NewWithAttributes() 里的 SchemaURL 使用的是相同版本
	// 是的，这玩意确实有可能会不一样 ┓(-´∀`-)┏
	if err != nil {
		panic(err)
	}
	return r
}

func initTracer() (*sdktrace.TracerProvider, error) {
	exporter, err := GetSpanExporter(ExporterType(ConfExporter))
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(MustNewResource(ConfToken)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	return tp, nil
}

func SleepRandom() {
	time.Sleep(time.Duration(rand.Int31n(1000)) * time.Millisecond)
}

type HttpSrv struct {
	s    *http.Server
	mux  http.Handler
	addr string
}

func GetAgeFromLocalCache(w http.ResponseWriter, req *http.Request) {
	username := req.URL.Query().Get("username")
	b := getAge(req.Context(), username, "getAgeFromLocalCache", "local")
	n := rand.Int31n(2)
	switch n {
	case 1:
		b = queryAgeWithTraces(req.Context(), fmt.Sprintf("http://%s/age_cache?username=%s", ConfDownstreamAddr, username), "server")
	}

	w.Write(b)
}

func GetAgeFromCacheServer(w http.ResponseWriter, req *http.Request) {
	username := req.URL.Query().Get("username")
	b := getAge(req.Context(), username, "getAgeFromCacheServer", "remote")
	w.Write(b)
}

func getAge(ctx context.Context, username, spanName, from string) []byte {
	ctx, span := Tracer.Start(ctx, spanName)
	if span != nil {
		defer span.End()
	}
	// pretend to query from somewhere
	SleepRandom()

	buf := &bytes.Buffer{}

	b := []byte(fmt.Sprintf(`{"username":"%s", "age":%d, "from":"%s"}`, username, rand.Int31n(80), from))
	json.HTMLEscape(buf, b)
	return buf.Bytes()
}

func (srv *HttpSrv) Start() error {
	srv.s = &http.Server{
		Addr:    srv.addr,
		Handler: srv.mux,
	}
	return srv.s.ListenAndServe()
}

func (srv *HttpSrv) Close() error {
	return srv.s.Close()
}

func NewUpstreamServerWithTraces() *HttpSrv {
	mux := http.NewServeMux()
	mux.Handle("/age", otelhttp.NewHandler(http.HandlerFunc(GetAgeFromLocalCache), "UpstreamAge"))
	return &HttpSrv{
		mux:  mux,
		addr: ConfUpstreamAddr,
	}
}

func NewDownstreamServerWithTraces() *HttpSrv {
	mux := http.NewServeMux()
	mux.Handle("/age_cache", otelhttp.NewHandler(http.HandlerFunc(GetAgeFromCacheServer), "DownstreamAge"))
	return &HttpSrv{
		mux:  mux,
		addr: ConfDownstreamAddr,
	}
}

func queryAgeWithTraces(ctx context.Context, url, from string) []byte {
	ctx, span := Tracer.Start(ctx, "queryAgeWithTraces-"+from)
	if span != nil {
		defer span.End()
	}

	ctx = httptrace.WithClientTrace(ctx, otelhttptrace.NewClientTrace(ctx))
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)

	res, err := Client.Do(req)
	if err != nil {
		log.Fatal(err)
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Fatal(err)
	}
	_ = res.Body.Close()

	return body
}

func LoopQueryAgeWithTraces(stop chan struct{}) {
	ticker := time.NewTicker(3 * time.Second)
	count := 0
	for {
		select {
		case <-ticker.C:
			b := queryAgeWithTraces(context.Background(), fmt.Sprintf("http://%s/age?username=blueking-%d", ConfUpstreamAddr, count), "client")
			log.Println(string(b))
			count++
		case <-stop:
			return
		}
	}
}

var (
	ConfExporter       string
	ConfToken          string
	ConfUpstreamAddr   string
	ConfDownstreamAddr string
	ConfEndpoint       string
)

func init() {
	flag.StringVar(&ConfExporter, "exporter", "stdout", "exporter represents the standard exporter type, optional: stdout/http/grpc")
	flag.StringVar(&ConfToken, "token", "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==", "authentication token")
	flag.StringVar(&ConfEndpoint, "endpoint", "localhost", "report endpoint")
	flag.StringVar(&ConfUpstreamAddr, "upstream", "localhost:56089", "upstream server address for testing")
	flag.StringVar(&ConfDownstreamAddr, "downstream", "localhost:56099", "downstream server address for testing")
	flag.Parse()
}

func main() {
	tp, err := initTracer()
	if err != nil {
		log.Fatal(err)
	}

	Tracer = tp.Tracer("traces-demo/v1")
	Client = http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}

	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down tracer provider: %v\n", err)
		}
	}()

	upSrv := NewUpstreamServerWithTraces()
	go func() {
		if err := upSrv.Start(); err != nil {
			log.Fatal(err)
		}
	}()
	downSrv := NewDownstreamServerWithTraces()
	go func() {
		if err := downSrv.Start(); err != nil {
			log.Fatal(err)
		}
	}()

	stop := make(chan struct{}, 1)
	go LoopQueryAgeWithTraces(stop)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	<-sigCh
	stop <- struct{}{}
}
