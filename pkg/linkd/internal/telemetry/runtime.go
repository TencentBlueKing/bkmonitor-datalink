// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"regexp"
	goruntime "runtime"
	"time"

	promclient "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

const (
	metricsPath        = "/metrics"
	shutdownTimeout    = 5 * time.Second
	instrumentationKey = "linkd"
)

// Runtime 持有一个 Linkd 进程内共享的 OTel MeterProvider 和 Prometheus 服务。
// 指标关闭时 Runtime 使用 no-op provider，调用方无需增加条件分支。
type Runtime struct {
	provider                     metric.MeterProvider
	shutdown                     func(context.Context) error
	meter                        metric.Meter
	server                       *http.Server
	listener                     net.Listener
	profileServer                *http.Server
	profileListener              net.Listener
	previousMutexProfileFraction int
	metrics                      *instruments
}

// Start 创建指定职责的 telemetry runtime。Prometheus 端口会在返回前完成 bind，
// 因此地址冲突不会等到业务进程接管消息后才暴露。
func Start(ctx context.Context, cfg Config, role Role, version string) (*Runtime, error) {
	if ctx == nil {
		return nil, fmt.Errorf("start telemetry: context must not be nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !cfg.Enabled() {
		provider := metricnoop.NewMeterProvider()
		meter := provider.Meter(instrumentationKey)
		metrics, err := newInstruments(meter)
		if err != nil {
			return nil, err
		}
		runtime := &Runtime{provider: provider, meter: meter, metrics: metrics}
		if err := runtime.startProfiling(ctx, cfg.Profiling); err != nil {
			return nil, err
		}
		return runtime, nil
	}

	address := cfg.ListenAddress()
	registry := promclient.NewRegistry()
	exporter, err := prometheus.New(prometheus.WithRegisterer(registry))
	if err != nil {
		return nil, fmt.Errorf("create prometheus exporter: %w", err)
	}
	res, err := newResource(version, role)
	if err != nil {
		return nil, err
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(exporter),
		sdkmetric.WithView(metricViews()...),
	)
	meter := provider.Meter(instrumentationKey)
	metrics, err := newInstruments(meter)
	if err != nil {
		_ = provider.Shutdown(context.Background())
		return nil, err
	}
	// 调度延迟与 GC CPU 可区分外部请求等待和本机运行时竞争；不采集无关的全量运行时指标。
	goMetrics := collectors.WithGoCollectorRuntimeMetrics(collectors.GoRuntimeMetricsRule{
		Matcher: regexp.MustCompile(`^/(sched/latencies:seconds|cpu/classes/gc/.*|cpu/classes/total:cpu-seconds)$`),
	})
	if err := registry.Register(collectors.NewGoCollector(goMetrics)); err != nil {
		_ = provider.Shutdown(context.Background())
		return nil, fmt.Errorf("register go collector: %w", err)
	}
	if err := registry.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		_ = provider.Shutdown(context.Background())
		return nil, fmt.Errorf("register process collector: %w", err)
	}

	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(ctx, "tcp", address)
	if err != nil {
		_ = provider.Shutdown(context.Background())
		return nil, fmt.Errorf("listen prometheus metrics on %s: %w", address, err)
	}
	mux := http.NewServeMux()
	mux.Handle(metricsPath, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       time.Minute,
	}
	runtime := &Runtime{
		provider: provider,
		shutdown: provider.Shutdown,
		meter:    meter,
		server:   server,
		listener: listener,
		metrics:  metrics,
	}
	if err := runtime.startProfiling(ctx, cfg.Profiling); err != nil {
		// Serve 尚未启动，http.Server 还未跟踪 listener，Shutdown 无法
		// 代为关闭。必须在返回启动错误前显式释放已绑定的端口。
		closeErr := listener.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		shutdownErr := runtime.Shutdown(shutdownCtx)
		cancel()
		return nil, errors.Join(err, closeErr, shutdownErr)
	}
	go func() {
		// listener 已在 Start 中成功 bind；此后的 Serve 错误只能由底层 listener
		// 异常或 Shutdown 触发，不能安全地从此 goroutine 修改业务状态。
		_ = server.Serve(listener)
	}()
	return runtime, nil
}

func (r *Runtime) startProfiling(ctx context.Context, cfg ProfilingConfig) error {
	if !cfg.Enabled {
		return nil
	}
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(ctx, "tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen profiling on %s: %w", cfg.ListenAddress, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	for _, name := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		mux.Handle("/debug/pprof/"+name, pprof.Handler(name))
	}
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       time.Minute,
	}
	r.profileServer = server
	r.profileListener = listener
	// 采样率属于进程级运行时状态；只在显式启用诊断时修改，并在 Shutdown 时复位。
	goruntime.SetBlockProfileRate(cfg.BlockProfileRate)
	r.previousMutexProfileFraction = goruntime.SetMutexProfileFraction(cfg.MutexProfileFraction)
	go func() { _ = server.Serve(listener) }()
	return nil
}

// MeterProvider 返回进程级 provider，供需要 OTel API 的窄基础设施组件使用。
func (r *Runtime) MeterProvider() metric.MeterProvider {
	if r == nil || r.provider == nil {
		return nil
	}
	return r.provider
}

// PrometheusListenAddress 返回当前进程实际绑定的指标地址。
// 未启用 Prometheus exporter 或 Runtime 为空时返回空字符串。
func (r *Runtime) PrometheusListenAddress() string {
	if r == nil || r.listener == nil {
		return ""
	}
	return r.listener.Addr().String()
}

// ProfilingListenAddress 返回当前进程实际绑定的 pprof 地址；未启用时返回空字符串。
func (r *Runtime) ProfilingListenAddress() string {
	if r == nil || r.profileListener == nil {
		return ""
	}
	return r.profileListener.Addr().String()
}

// Shutdown 先停止 Prometheus HTTP server，再关闭 MeterProvider。
func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("shutdown telemetry: context must not be nil")
	}
	var shutdownErrors []error
	if r.profileServer != nil {
		serverCtx, cancel := context.WithTimeout(ctx, shutdownTimeout)
		if err := r.profileServer.Shutdown(serverCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("shutdown profiling server: %w", err))
		}
		cancel()
		goruntime.SetBlockProfileRate(0)
		goruntime.SetMutexProfileFraction(r.previousMutexProfileFraction)
	}
	if r.server != nil {
		serverCtx, cancel := context.WithTimeout(ctx, shutdownTimeout)
		if err := r.server.Shutdown(serverCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("shutdown prometheus server: %w", err))
		}
		cancel()
	}
	if r.shutdown != nil {
		if err := r.shutdown(ctx); err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("shutdown meter provider: %w", err))
		}
	}
	return errors.Join(shutdownErrors...)
}

func newResource(version string, role Role) (*resource.Resource, error) {
	if version == "" {
		version = "dev"
	}
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown-host"
	}
	instanceID := fmt.Sprintf("%s-%d", hostname, os.Getpid())
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("linkd"),
			semconv.ServiceVersion(version),
			semconv.ServiceInstanceID(instanceID),
			semconv.ServiceNamespace("kingeye"),
			attribute.String("linkd.role", string(role)),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create telemetry resource: %w", err)
	}
	return res, nil
}
