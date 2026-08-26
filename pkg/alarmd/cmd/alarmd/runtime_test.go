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
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lifecycle"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestRunApplicationPreCanceledDoesNotOpenDependencies(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opened := false
	dependencies := applicationDependencies{
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger) (*applicationBundle, error) {
			opened = true
			return nil, errors.New("must not open")
		},
	}
	if err := runApplication(ctx, validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}), dependencies); err != nil {
		t.Fatalf("runApplication() error = %v", err)
	}
	if opened {
		t.Fatal("pre-canceled runtime opened dependencies")
	}
}

func TestRunApplicationParentCancellationDrainsBundleAndHTTP(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	serviceStarted := make(chan struct{})
	httpStarted := make(chan struct{})
	serviceClosed := false
	service := newFakeServiceRuntime()
	service.run = func(ctx context.Context) error {
		close(serviceStarted)
		<-ctx.Done()
		return nil
	}
	service.close = func() error {
		serviceClosed = true
		return nil
	}
	bundle := &applicationBundle{service: service, gate: coordinator.NewCriticalDependencyGate(nil)}
	dependencies := applicationDependencies{
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger) (*applicationBundle, error) {
			return bundle, nil
		},
		newHTTP: func(*metric.Recorder, observability.HealthSource) (httpRuntime, error) {
			return &fakeHTTPRuntime{run: func(ctx context.Context, _ string, _ time.Duration) error {
				close(httpStarted)
				<-ctx.Done()
				return nil
			}}, nil
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- runApplication(ctx, validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}), dependencies)
	}()
	<-serviceStarted
	<-httpStarted
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("runApplication() error = %v", err)
	}
	if !serviceClosed {
		t.Fatal("application bundle was not closed")
	}
}

func TestRunApplicationUnexpectedHTTPStopIsFatal(t *testing.T) {
	t.Parallel()

	service := newFakeServiceRuntime()
	service.run = func(ctx context.Context) error { <-ctx.Done(); return nil }
	service.close = func() error { return nil }
	bundle := &applicationBundle{service: service, gate: coordinator.NewCriticalDependencyGate(nil)}
	dependencies := applicationDependencies{
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger) (*applicationBundle, error) {
			return bundle, nil
		},
		newHTTP: func(*metric.Recorder, observability.HealthSource) (httpRuntime, error) {
			return &fakeHTTPRuntime{run: func(context.Context, string, time.Duration) error { return nil }}, nil
		},
	}
	err := runApplication(context.Background(), validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}), dependencies)
	if !errors.Is(err, errHTTPServiceStopped) {
		t.Fatalf("runApplication() error = %v, want unexpected HTTP stop", err)
	}
}

func TestRunApplicationHTTPInitializationFailureDoesNotOpenBundle(t *testing.T) {
	t.Parallel()

	want := errors.New("HTTP initialization failed")
	opened := false
	dependencies := applicationDependencies{
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger) (*applicationBundle, error) {
			opened = true
			return nil, errors.New("must not open")
		},
		newHTTP: func(*metric.Recorder, observability.HealthSource) (httpRuntime, error) {
			return nil, want
		},
	}
	if err := runApplication(
		context.Background(), validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}), dependencies,
	); !errors.Is(err, want) {
		t.Fatalf("runApplication() error = %v, want %v", err, want)
	}
	if opened {
		t.Fatal("runtime opened public dependencies after local HTTP initialization failed")
	}
}

func TestRunApplicationFatalStartsOneShutdownDeadline(t *testing.T) {
	t.Parallel()

	cfg := validApplicationConfig()
	cfg.ShutdownTimeout = config.Duration(400 * time.Millisecond)
	want := errors.New("consumer fatal")
	serviceStarted := make(chan struct{})
	httpStarted := make(chan struct{})
	releaseService := make(chan struct{})
	service := newFakeServiceRuntime()
	service.fatalErr = want
	service.run = func(ctx context.Context) error {
		close(serviceStarted)
		<-ctx.Done()
		<-releaseService
		return want
	}
	var eventDeadline time.Time
	bundle := &applicationBundle{
		service: service,
		gate:    coordinator.NewCriticalDependencyGate(nil),
		triggerEvents: &fakeTriggerEventRuntime{shutdown: func(ctx context.Context) error {
			eventDeadline, _ = ctx.Deadline()
			return nil
		}},
	}
	dependencies := applicationDependencies{
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger) (*applicationBundle, error) {
			return bundle, nil
		},
		newHTTP: func(*metric.Recorder, observability.HealthSource) (httpRuntime, error) {
			return &fakeHTTPRuntime{run: func(ctx context.Context, _ string, _ time.Duration) error {
				close(httpStarted)
				<-ctx.Done()
				return nil
			}}, nil
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- runApplication(context.Background(), cfg, metric.NewRecorder(metric.BuildInfo{}), dependencies)
	}()
	for _, started := range []<-chan struct{}{serviceStarted, httpStarted} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("runtime component did not start")
		}
	}
	fatalAt := time.Now()
	close(service.fatalSignal)
	time.AfterFunc(50*time.Millisecond, func() { close(releaseService) })
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("runApplication() error = %v, want %v", err, want)
	}
	latest := fatalAt.Add(cfg.ShutdownTimeout.Duration() + 20*time.Millisecond)
	if eventDeadline.IsZero() || eventDeadline.After(latest) {
		t.Fatalf("event shutdown deadline = %s, want deadline fixed when fatal was observed", eventDeadline)
	}
}

func TestRunApplicationLogsFatalWithoutErrorBody(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	want := errors.New("consumer fatal broker=secret-broker payload=secret-payload")
	serviceStarted := make(chan struct{})
	httpStarted := make(chan struct{})
	service := newFakeServiceRuntime()
	service.fatalErr = want
	service.run = func(ctx context.Context) error {
		close(serviceStarted)
		<-ctx.Done()
		return want
	}
	bundle := &applicationBundle{service: service, gate: coordinator.NewCriticalDependencyGate(nil)}
	dependencies := applicationDependencies{
		logger: observability.New(observability.ComponentTrigger, &output),
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger) (*applicationBundle, error) {
			return bundle, nil
		},
		newHTTP: func(*metric.Recorder, observability.HealthSource) (httpRuntime, error) {
			return &fakeHTTPRuntime{run: func(ctx context.Context, _ string, _ time.Duration) error {
				close(httpStarted)
				<-ctx.Done()
				return nil
			}}, nil
		},
	}
	done := make(chan error, 1)
	go func() {
		done <- runApplication(context.Background(), validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}), dependencies)
	}()
	for _, started := range []<-chan struct{}{serviceStarted, httpStarted} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("runtime component did not start")
		}
	}
	close(service.fatalSignal)
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("runApplication() error = %v, want %v", err, want)
	}
	logOutput := output.String()
	for _, stage := range []string{`"stage":"startup"`, `"stage":"fatal"`, `"stage":"shutdown"`} {
		if !strings.Contains(logOutput, stage) {
			t.Fatalf("log = %q, want %q", logOutput, stage)
		}
	}
	for _, secret := range []string{"secret-broker", "secret-payload"} {
		if strings.Contains(logOutput, secret) {
			t.Fatalf("structured log leaked %q: %q", secret, logOutput)
		}
	}
}

func TestRunApplicationShutdownDeadlineStillAttemptsOutputsAndRedis(t *testing.T) {
	t.Parallel()

	cfg := validApplicationConfig()
	cfg.ShutdownTimeout = config.Duration(20 * time.Millisecond)
	serviceStarted := make(chan struct{})
	releaseService := make(chan struct{})
	service := newFakeServiceRuntime()
	service.run = func(context.Context) error {
		close(serviceStarted)
		<-releaseService
		return nil
	}
	eventsCalled := make(chan struct{})
	redisCalled := make(chan struct{})
	bundle := &applicationBundle{
		service: service,
		gate:    coordinator.NewCriticalDependencyGate(nil),
		triggerEvents: &fakeTriggerEventRuntime{shutdown: func(ctx context.Context) error {
			close(eventsCalled)
			return ctx.Err()
		}},
		redis: &fakeRedisRuntime{close: func() error {
			close(redisCalled)
			return nil
		}},
	}
	dependencies := applicationDependencies{
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger) (*applicationBundle, error) {
			return bundle, nil
		},
		newHTTP: func(*metric.Recorder, observability.HealthSource) (httpRuntime, error) {
			return &fakeHTTPRuntime{run: func(ctx context.Context, _ string, _ time.Duration) error {
				<-ctx.Done()
				return nil
			}}, nil
		},
	}
	runContext, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runApplication(runContext, cfg, metric.NewRecorder(metric.BuildInfo{}), dependencies)
	}()
	select {
	case <-serviceStarted:
	case <-time.After(time.Second):
		t.Fatal("Kafka service did not start")
	}
	cancelRun()
	if err := <-done; !errors.Is(err, ErrApplicationShutdownTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runApplication() error = %v, want timeout and expired output context", err)
	}
	for name, called := range map[string]<-chan struct{}{"events": eventsCalled, "redis": redisCalled} {
		select {
		case <-called:
		case <-time.After(time.Second):
			t.Fatalf("%s shutdown was not attempted after service timeout", name)
		}
	}
	close(releaseService)
}

func validApplicationConfig() config.Config {
	cfg := config.Default()
	cfg.Kafka.Brokers = []string{"127.0.0.1:9092"}
	cfg.Kafka.InputTopic = "alarmd-shadow-input-v2"
	cfg.Kafka.TriggerEvent.Topic = "alarmd-shadow-trigger-event-v1"
	cfg.Kafka.MessageReceipt.Topic = "alarmd-shadow-message-receipt-v1"
	cfg.Kafka.AllowedOutputTopics = []string{
		cfg.Kafka.TriggerEvent.Topic,
		cfg.Kafka.MessageReceipt.Topic,
	}
	cfg.Kafka.GroupID = "alarmd-shadow-v2"
	cfg.Kafka.ClientID = "alarmd"
	cfg.Kafka.BrokerVersion = "2.6.0"
	cfg.Kafka.InitialOffset = "oldest"
	cfg.Redis.Address = "127.0.0.1:6379"
	cfg.Redis.StatePrefix = "alarmd-shadow"
	return cfg
}

type fakeServiceRuntime struct {
	run         func(context.Context) error
	close       func() error
	fatalSignal chan struct{}
	fatalErr    error
	snapshot    lifecycle.Snapshot
}

func newFakeServiceRuntime() *fakeServiceRuntime {
	return &fakeServiceRuntime{fatalSignal: make(chan struct{})}
}

func (service *fakeServiceRuntime) Run(ctx context.Context) error {
	if service.run != nil {
		return service.run(ctx)
	}
	<-ctx.Done()
	return nil
}

func (service *fakeServiceRuntime) Close() error {
	if service.close != nil {
		return service.close()
	}
	return nil
}

func (service *fakeServiceRuntime) FatalSignal() <-chan struct{} { return service.fatalSignal }
func (service *fakeServiceRuntime) FatalError() error            { return service.fatalErr }
func (service *fakeServiceRuntime) LifecycleSnapshot() lifecycle.Snapshot {
	return service.snapshot
}

type fakeHTTPRuntime struct {
	run func(context.Context, string, time.Duration) error
}

func (runtime *fakeHTTPRuntime) Run(ctx context.Context, address string, timeout time.Duration) error {
	return runtime.run(ctx, address, timeout)
}
