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
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/consumer"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/lifecycle"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/metric"
)

func TestRunApplicationPreCanceledDoesNotOpenRuntimeDependencies(t *testing.T) {
	t.Parallel()

	var opened bool
	dependencies := applicationDependencies{
		openSink: func(config.KafkaConfig) (decisionSinkRuntime, error) {
			opened = true
			return nil, nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runApplication(ctx, validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}), dependencies)
	if err != nil {
		t.Fatalf("runApplication() error = %v", err)
	}
	if opened {
		t.Fatal("runApplication() opened a dependency for a pre-canceled context")
	}
}

func TestRunApplicationCancellationDuringStartupStopsOpeningAndCleansUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cancelAt   string
		wantEvents string
	}{
		{name: "sink", cancelAt: "sink", wantEvents: "open-sink,shutdown-sink"},
		{name: "service", cancelAt: "service", wantEvents: "open-sink,open-service,close-service,shutdown-sink"},
		{name: "HTTP", cancelAt: "HTTP", wantEvents: "open-sink,open-service,new-http,close-service,shutdown-sink"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			events := make([]string, 0, 5)
			sink := &fakeDecisionSinkRuntime{shutdown: func(context.Context) error {
				events = append(events, "shutdown-sink")
				return nil
			}}
			service := newFakeServiceRuntime()
			service.run = func(context.Context) error {
				t.Fatal("Kafka service started after startup cancellation")
				return nil
			}
			service.close = func() error {
				events = append(events, "close-service")
				return nil
			}
			server := &fakeHTTPRuntime{run: func(context.Context, string, time.Duration) error {
				t.Fatal("HTTP service started after startup cancellation")
				return nil
			}}
			dependencies := applicationDependencies{
				openSink: func(config.KafkaConfig) (decisionSinkRuntime, error) {
					events = append(events, "open-sink")
					if test.cancelAt == "sink" {
						cancel()
					}
					return sink, nil
				},
				openService: func(config.KafkaConfig, consumer.ProcessorFactory, time.Duration) (serviceRuntime, error) {
					events = append(events, "open-service")
					if test.cancelAt == "service" {
						cancel()
					}
					return service, nil
				},
				newHTTP: func(*metric.Recorder, lifecycle.Source) (httpRuntime, error) {
					events = append(events, "new-http")
					if test.cancelAt == "HTTP" {
						cancel()
					}
					return server, nil
				},
			}
			if err := runApplication(ctx, validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}), dependencies); err != nil {
				t.Fatalf("runApplication() error = %v", err)
			}
			if got := strings.Join(events, ","); got != test.wantEvents {
				t.Fatalf("events = %q, want %q", got, test.wantEvents)
			}
		})
	}
}

func TestRunApplicationServiceOpenFailureClosesSink(t *testing.T) {
	t.Parallel()

	want := errors.New("service open failed")
	events := make([]string, 0, 3)
	sink := &fakeDecisionSinkRuntime{shutdown: func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("sink shutdown did not receive a deadline")
		}
		events = append(events, "shutdown-sink")
		return nil
	}}
	dependencies := applicationDependencies{
		openSink: func(config.KafkaConfig) (decisionSinkRuntime, error) {
			events = append(events, "open-sink")
			return sink, nil
		},
		openService: func(config.KafkaConfig, consumer.ProcessorFactory, time.Duration) (serviceRuntime, error) {
			events = append(events, "open-service")
			return nil, want
		},
		newHTTP: func(*metric.Recorder, lifecycle.Source) (httpRuntime, error) {
			t.Fatal("HTTP service must not be initialized after Kafka service open fails")
			return nil, nil
		},
	}
	err := runApplication(context.Background(), validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}), dependencies)
	if !errors.Is(err, want) {
		t.Fatalf("runApplication() error = %v, want %v", err, want)
	}
	if got := strings.Join(events, ","); got != "open-sink,open-service,shutdown-sink" {
		t.Fatalf("events = %q, want reverse sink cleanup", got)
	}
}

func TestRunApplicationHTTPInitializationFailureClosesServiceThenSink(t *testing.T) {
	t.Parallel()

	want := errors.New("HTTP initialization failed")
	events := make([]string, 0, 5)
	sink := &fakeDecisionSinkRuntime{shutdown: func(context.Context) error {
		events = append(events, "shutdown-sink")
		return nil
	}}
	service := newFakeServiceRuntime()
	service.close = func() error {
		events = append(events, "close-service")
		return nil
	}
	dependencies := applicationDependencies{
		openSink: func(config.KafkaConfig) (decisionSinkRuntime, error) {
			events = append(events, "open-sink")
			return sink, nil
		},
		openService: func(config.KafkaConfig, consumer.ProcessorFactory, time.Duration) (serviceRuntime, error) {
			events = append(events, "open-service")
			return service, nil
		},
		newHTTP: func(*metric.Recorder, lifecycle.Source) (httpRuntime, error) {
			events = append(events, "new-http")
			return nil, want
		},
	}
	err := runApplication(context.Background(), validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}), dependencies)
	if !errors.Is(err, want) {
		t.Fatalf("runApplication() error = %v, want %v", err, want)
	}
	if got := strings.Join(events, ","); got != "open-sink,open-service,new-http,close-service,shutdown-sink" {
		t.Fatalf("events = %q, want reverse service/sink cleanup", got)
	}
}

func TestRunApplicationParentCancelDrainsServiceBeforeSinkWithinOneDeadline(t *testing.T) {
	t.Parallel()

	cfg := validApplicationConfig()
	cfg.ShutdownTimeout = config.Duration(200 * time.Millisecond)
	events := &eventLog{}
	serviceStarted := make(chan struct{})
	httpStarted := make(chan struct{})
	service := newFakeServiceRuntime()
	service.run = func(ctx context.Context) error {
		close(serviceStarted)
		<-ctx.Done()
		events.add("service-return")
		return nil
	}
	service.close = func() error {
		return errors.New("running service must own its shutdown")
	}
	server := &fakeHTTPRuntime{run: func(ctx context.Context, _ string, _ time.Duration) error {
		close(httpStarted)
		<-ctx.Done()
		events.add("http-return")
		return nil
	}}
	sink := &fakeDecisionSinkRuntime{shutdown: func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			return errors.New("sink shutdown did not receive the shared deadline")
		}
		events.add("sink-shutdown")
		return nil
	}}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	dependencies := applicationDependencies{
		openSink: func(config.KafkaConfig) (decisionSinkRuntime, error) { return sink, nil },
		openService: func(_ config.KafkaConfig, newProcessor consumer.ProcessorFactory, _ time.Duration) (serviceRuntime, error) {
			if newProcessor == nil || newProcessor() == nil {
				return nil, errors.New("Kafka service received an empty Processor factory")
			}
			return service, nil
		},
		newHTTP: func(gotRecorder *metric.Recorder, source lifecycle.Source) (httpRuntime, error) {
			if gotRecorder != recorder || source != service {
				return nil, errors.New("HTTP readiness and metrics did not share the runtime lifecycle source")
			}
			return server, nil
		},
	}
	runContext, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runApplication(runContext, cfg, recorder, dependencies) }()
	for index, started := range []<-chan struct{}{serviceStarted, httpStarted} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("runtime component %d did not start", index)
		}
	}
	cancelRun()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runApplication() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runApplication() did not stop")
	}
	got := events.snapshot()
	serviceIndex := strings.Index(got, "service-return")
	sinkIndex := strings.Index(got, "sink-shutdown")
	if serviceIndex < 0 || sinkIndex < 0 || serviceIndex > sinkIndex {
		t.Fatalf("events = %q, want Kafka service complete before sink shutdown", got)
	}
	if !strings.Contains(got, "http-return") {
		t.Fatalf("events = %q, want HTTP service to complete within the shared deadline", got)
	}
}

func TestRunApplicationFatalStartsSharedDeadlineBeforeServiceReturns(t *testing.T) {
	t.Parallel()

	cfg := validApplicationConfig()
	cfg.ShutdownTimeout = config.Duration(500 * time.Millisecond)
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
	service.close = func() error { return errors.New("unexpected service Close") }
	server := &fakeHTTPRuntime{run: func(ctx context.Context, _ string, _ time.Duration) error {
		close(httpStarted)
		<-ctx.Done()
		return nil
	}}
	var sinkDeadline time.Time
	sink := &fakeDecisionSinkRuntime{shutdown: func(ctx context.Context) error {
		sinkDeadline, _ = ctx.Deadline()
		return nil
	}}
	dependencies := applicationDependencies{
		openSink: func(config.KafkaConfig) (decisionSinkRuntime, error) { return sink, nil },
		openService: func(config.KafkaConfig, consumer.ProcessorFactory, time.Duration) (serviceRuntime, error) {
			return service, nil
		},
		newHTTP: func(*metric.Recorder, lifecycle.Source) (httpRuntime, error) { return server, nil },
	}
	done := make(chan error, 1)
	go func() {
		done <- runApplication(context.Background(), cfg, metric.NewRecorder(metric.BuildInfo{}), dependencies)
	}()
	for index, started := range []<-chan struct{}{serviceStarted, httpStarted} {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatalf("runtime component %d did not start", index)
		}
	}
	fatalAt := time.Now()
	close(service.fatalSignal)
	time.AfterFunc(100*time.Millisecond, func() { close(releaseService) })
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("runApplication() error = %v, want %v", err, want)
		}
	case <-time.After(time.Second):
		t.Fatal("runApplication() did not stop after fatal signal")
	}
	latestSharedDeadline := fatalAt.Add(cfg.ShutdownTimeout.Duration() + 20*time.Millisecond)
	if sinkDeadline.IsZero() || sinkDeadline.After(latestSharedDeadline) {
		t.Fatalf("sink deadline = %s, want one deadline established at fatal before service return", sinkDeadline)
	}
}

func TestRunApplicationHTTPFailureStopsKafkaBeforeSink(t *testing.T) {
	t.Parallel()

	want := errors.New("HTTP listen failed")
	events := &eventLog{}
	serviceStarted := make(chan struct{})
	service := newFakeServiceRuntime()
	service.run = func(ctx context.Context) error {
		close(serviceStarted)
		<-ctx.Done()
		events.add("service-return")
		return nil
	}
	service.close = func() error { return errors.New("unexpected service Close") }
	server := &fakeHTTPRuntime{run: func(context.Context, string, time.Duration) error {
		<-serviceStarted
		return want
	}}
	sink := &fakeDecisionSinkRuntime{shutdown: func(context.Context) error {
		events.add("sink-shutdown")
		return nil
	}}
	dependencies := applicationDependencies{
		openSink: func(config.KafkaConfig) (decisionSinkRuntime, error) { return sink, nil },
		openService: func(config.KafkaConfig, consumer.ProcessorFactory, time.Duration) (serviceRuntime, error) {
			return service, nil
		},
		newHTTP: func(*metric.Recorder, lifecycle.Source) (httpRuntime, error) { return server, nil },
	}
	err := runApplication(context.Background(), validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}), dependencies)
	if !errors.Is(err, want) {
		t.Fatalf("runApplication() error = %v, want %v", err, want)
	}
	if got := events.snapshot(); got != "service-return,sink-shutdown" {
		t.Fatalf("events = %q, want Kafka service complete before sink shutdown", got)
	}
}

func TestRunApplicationUnexpectedHTTPCancellationIsAnError(t *testing.T) {
	t.Parallel()

	serviceStarted := make(chan struct{})
	service := newFakeServiceRuntime()
	service.run = func(ctx context.Context) error {
		close(serviceStarted)
		<-ctx.Done()
		return nil
	}
	service.close = func() error { return errors.New("unexpected service Close") }
	server := &fakeHTTPRuntime{run: func(context.Context, string, time.Duration) error {
		<-serviceStarted
		return context.Canceled
	}}
	sink := &fakeDecisionSinkRuntime{shutdown: func(context.Context) error { return nil }}
	dependencies := applicationDependencies{
		openSink: func(config.KafkaConfig) (decisionSinkRuntime, error) { return sink, nil },
		openService: func(config.KafkaConfig, consumer.ProcessorFactory, time.Duration) (serviceRuntime, error) {
			return service, nil
		},
		newHTTP: func(*metric.Recorder, lifecycle.Source) (httpRuntime, error) { return server, nil },
	}
	err := runApplication(context.Background(), validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}), dependencies)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runApplication() error = %v, want unexpected HTTP cancellation", err)
	}
}

func TestRunApplicationUnexpectedKafkaCancellationIsAnError(t *testing.T) {
	t.Parallel()

	httpStarted := make(chan struct{})
	service := newFakeServiceRuntime()
	service.run = func(context.Context) error {
		<-httpStarted
		return context.Canceled
	}
	service.close = func() error { return errors.New("unexpected service Close") }
	server := &fakeHTTPRuntime{run: func(ctx context.Context, _ string, _ time.Duration) error {
		close(httpStarted)
		<-ctx.Done()
		return nil
	}}
	sink := &fakeDecisionSinkRuntime{shutdown: func(context.Context) error { return nil }}
	dependencies := applicationDependencies{
		openSink: func(config.KafkaConfig) (decisionSinkRuntime, error) { return sink, nil },
		openService: func(config.KafkaConfig, consumer.ProcessorFactory, time.Duration) (serviceRuntime, error) {
			return service, nil
		},
		newHTTP: func(*metric.Recorder, lifecycle.Source) (httpRuntime, error) { return server, nil },
	}
	err := runApplication(context.Background(), validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}), dependencies)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runApplication() error = %v, want unexpected Kafka cancellation", err)
	}
}

func TestRunApplicationShutdownDeadlineBoundsStuckServiceAndForcesSinkAttempt(t *testing.T) {
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
	service.close = func() error { return errors.New("unexpected service Close") }
	server := &fakeHTTPRuntime{run: func(ctx context.Context, _ string, _ time.Duration) error {
		<-ctx.Done()
		return nil
	}}
	sinkCalled := make(chan struct{})
	sink := &fakeDecisionSinkRuntime{shutdown: func(ctx context.Context) error {
		close(sinkCalled)
		return ctx.Err()
	}}
	dependencies := applicationDependencies{
		openSink: func(config.KafkaConfig) (decisionSinkRuntime, error) { return sink, nil },
		openService: func(config.KafkaConfig, consumer.ProcessorFactory, time.Duration) (serviceRuntime, error) {
			return service, nil
		},
		newHTTP: func(*metric.Recorder, lifecycle.Source) (httpRuntime, error) { return server, nil },
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
	started := time.Now()
	cancelRun()
	select {
	case err := <-done:
		if !errors.Is(err, ErrApplicationShutdownTimeout) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("runApplication() error = %v, want runtime timeout and expired sink context", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runApplication() exceeded its shutdown deadline")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("runApplication() shutdown elapsed = %s", elapsed)
	}
	select {
	case <-sinkCalled:
	default:
		t.Fatal("sink shutdown was not attempted after service deadline")
	}
	close(releaseService)
}

func TestRecordingDecisionSinkCountsOutputOnlyAfterAcknowledgement(t *testing.T) {
	t.Parallel()

	recorder := metric.NewRecorder(metric.BuildInfo{})
	started := make(chan struct{})
	release := make(chan struct{})
	next := &fakeDecisionSinkRuntime{write: func(context.Context, *contract.TriggerDecisionBatch) error {
		close(started)
		<-release
		return nil
	}, shutdown: func(context.Context) error { return nil }}
	sink := newRecordingDecisionSink(recorder, next)
	batch := &contract.TriggerDecisionBatch{Decisions: []contract.TriggerDecision{{}, {}}}
	done := make(chan error, 1)
	go func() { done <- sink.WriteBatch(context.Background(), batch) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("decision sink did not start")
	}
	if got := counterValue(t, recorder, "bkmonitor_alarm_engine_records_total", map[string]string{
		"stage": "trigger", "mode": "shadow", "direction": "output", "record_type": "trigger_decision",
	}); got != 0 {
		t.Fatalf("output records before ACK = %v, want 0", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("WriteBatch() error = %v", err)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarm_engine_records_total", map[string]string{
		"stage": "trigger", "mode": "shadow", "direction": "output", "record_type": "trigger_decision",
	}); got != 2 {
		t.Fatalf("output records after ACK = %v, want 2", got)
	}
}

func TestRecordingDecisionSinkDoesNotCountFailedOutput(t *testing.T) {
	t.Parallel()

	recorder := metric.NewRecorder(metric.BuildInfo{})
	want := errors.New("broker acknowledgement failed")
	next := &fakeDecisionSinkRuntime{
		write:    func(context.Context, *contract.TriggerDecisionBatch) error { return want },
		shutdown: func(context.Context) error { return nil },
	}
	sink := newRecordingDecisionSink(recorder, next)
	batch := &contract.TriggerDecisionBatch{Decisions: []contract.TriggerDecision{{}, {}}}
	if err := sink.WriteBatch(context.Background(), batch); !errors.Is(err, want) {
		t.Fatalf("WriteBatch() error = %v, want %v", err, want)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarm_engine_records_total", map[string]string{
		"stage": "trigger", "mode": "shadow", "direction": "output", "record_type": "trigger_decision",
	}); got != 0 {
		t.Fatalf("output records after failed ACK = %v, want 0", got)
	}
}

func validApplicationConfig() config.Config {
	return config.Config{
		Mode: config.ModeShadow,
		HTTP: config.HTTPConfig{Listen: "127.0.0.1:8080"},
		Kafka: config.KafkaConfig{
			Brokers:             []string{"127.0.0.1:9092"},
			InputTopic:          "alarm-engine-shadow-input",
			OutputTopic:         "alarm-engine-shadow-output",
			AllowedOutputTopics: []string{"alarm-engine-shadow-output"},
			GroupID:             "alarm-engine-shadow",
			ClientID:            "alarm-engine",
			BrokerVersion:       "2.6.0",
		},
		ShutdownTimeout: config.Duration(time.Second),
	}
}

func TestRecordingProcessorUsesBoundedSuccessAndFailureLabels(t *testing.T) {
	t.Parallel()

	recorder := metric.NewRecorder(metric.BuildInfo{})
	want := errors.New("processor failed")
	attempts := 0
	next := consumer.ProcessorFunc(func(context.Context, []byte, []byte) error {
		attempts++
		if attempts == 1 {
			return nil
		}
		return want
	})
	processor := newRecordingProcessor(recorder, next)
	if err := processor.Process(context.Background(), nil, nil); err != nil {
		t.Fatalf("first Process() error = %v", err)
	}
	if err := processor.Process(context.Background(), nil, nil); !errors.Is(err, want) {
		t.Fatalf("second Process() error = %v, want %v", err, want)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarm_engine_process_total", map[string]string{
		"stage": "trigger", "mode": "shadow", "status": "success", "error_code": "none",
	}); got != 1 {
		t.Fatalf("success process count = %v, want 1", got)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarm_engine_process_total", map[string]string{
		"stage": "trigger", "mode": "shadow", "status": "failed", "error_code": "internal",
	}); got != 1 {
		t.Fatalf("failed process count = %v, want 1", got)
	}
}

type fakeDecisionSinkRuntime struct {
	write    func(context.Context, *contract.TriggerDecisionBatch) error
	shutdown func(context.Context) error
}

func (s *fakeDecisionSinkRuntime) WriteBatch(ctx context.Context, batch *contract.TriggerDecisionBatch) error {
	if s.write == nil {
		return nil
	}
	return s.write(ctx, batch)
}

func (s *fakeDecisionSinkRuntime) Shutdown(ctx context.Context) error {
	return s.shutdown(ctx)
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

func (s *fakeServiceRuntime) Run(ctx context.Context) error { return s.run(ctx) }
func (s *fakeServiceRuntime) Close() error                  { return s.close() }
func (s *fakeServiceRuntime) FatalSignal() <-chan struct{}  { return s.fatalSignal }
func (s *fakeServiceRuntime) FatalError() error             { return s.fatalErr }
func (s *fakeServiceRuntime) LifecycleSnapshot() lifecycle.Snapshot {
	return s.snapshot
}

type fakeHTTPRuntime struct {
	run func(context.Context, string, time.Duration) error
}

func (s *fakeHTTPRuntime) Run(ctx context.Context, address string, timeout time.Duration) error {
	return s.run(ctx, address, timeout)
}

type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(event string) {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *eventLog) snapshot() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.events, ",")
}

func counterValue(t *testing.T, recorder *metric.Recorder, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, sample := range family.Metric {
			matched := len(sample.Label) == len(labels)
			for _, label := range sample.Label {
				if labels[label.GetName()] != label.GetValue() {
					matched = false
					break
				}
			}
			if matched {
				return sample.GetCounter().GetValue()
			}
		}
	}
	return 0
}
