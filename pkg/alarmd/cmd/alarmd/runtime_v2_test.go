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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lifecycle"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

func TestOpenApplicationBundleBuildsV2EvaluationService(t *testing.T) {
	t.Parallel()

	cfg := validApplicationConfig()
	redis := &fakeRedisRuntime{}
	events := &fakeTriggerEventRuntime{}
	receipts := &fakeReceiptRuntime{}
	service := newFakeServiceRuntime()
	service.close = func() error { return nil }
	factories := applicationComponentFactories{
		newEffectiveTime: phaseOneEffectiveTimeProvider,
		openRedis:        func(state.RedisBackendOptions) (redisRuntime, error) { return redis, nil },
		openTriggerEvents: func(enginekafka.DecisionSinkConfig) (triggerEventRuntime, error) {
			return events, nil
		},
		openReceipts: func(enginekafka.DecisionSinkConfig, enginekafka.ReceiptPublisherLimits) (receiptRuntime, error) {
			return receipts, nil
		},
		openEvaluationService: func(
			coordinates enginekafka.Config,
			router coordinator.MessageOutcomeRouter,
			critical coordinator.CriticalCompletion,
			gotReceipts coordinator.ReceiptPublisher,
			gate *coordinator.CriticalDependencyGate,
			diagnostics enginekafka.EvaluationDiagnostics,
			_ coordinator.DependencyRetryConfig,
			_ time.Duration,
		) (serviceRuntime, error) {
			if coordinates.Topic != cfg.Kafka.InputTopic {
				t.Fatalf("input topic = %q, want %q", coordinates.Topic, cfg.Kafka.InputTopic)
			}
			if router == nil || critical == nil || gotReceipts != receipts || gate == nil {
				t.Fatal("v2 evaluation service received an incomplete composition")
			}
			if diagnostics.OnRejected == nil || diagnostics.OnOffsetCommitted == nil {
				t.Fatal("v2 evaluation service did not receive rejection and offset diagnostics")
			}
			return service, nil
		},
	}

	recorder := metric.NewRecorder(metric.BuildInfo{})
	bundle, err := openApplicationBundleWithFactories(
		context.Background(), cfg, recorder,
		observability.Discard(observability.ComponentTrigger), factories,
	)
	if err != nil {
		t.Fatalf("openApplicationBundleWithFactories() error = %v", err)
	}
	if bundle.service != service || bundle.gate == nil {
		t.Fatal("application bundle did not retain the v2 service and dependency gate")
	}
	if bundle.resources == nil || !hasMetricFamily(t, recorder, "bkmonitor_alarmd_resource_state") {
		t.Fatal("application bundle did not bind the observation-only ResourceGovernor")
	}
	if redis.pings != 1 {
		t.Fatalf("Redis pings = %d, want one startup readiness check", redis.pings)
	}
	if err := bundle.Shutdown(context.Background()); err != nil {
		t.Fatalf("bundle.Shutdown() error = %v", err)
	}
}

func TestRuntimeObserverAdaptersCoverExistingV2Callpoints(t *testing.T) {
	t.Parallel()

	recorder := metric.NewRecorder(metric.BuildInfo{})
	observer := observability.Multi(recorder)
	decoded, err := (observedMessageDecoder{
		next: inputv2.New(validApplicationConfig().ReaderLimits()), observer: observer,
	}).Decode(context.Background(), []byte(`{"schema":`))
	if err != nil || !decoded.Rejected {
		t.Fatalf("observed Decode() = %#v, %v; want deterministic rejection", decoded, err)
	}
	detectObserver(observer).ObserveDetect(context.Background(), detect.Observation{
		Stage: detect.StageDetectCompleted, Result: detect.ObservationSuccess,
		Counts: detect.DetectionCounts{Plans: 1, EvaluatedRecords: 2, CompiledLevels: 3},
	})
	stateObserver(observer).ObserveState(context.Background(), state.Observation{
		Stage: state.StageDependencyLoaded, Operation: state.OperationLoad, Result: state.OperationSucceeded,
		TouchedKeys: 2, StateBytes: 128,
	})
	offsetCommitDiagnostics(observer)(enginekafka.OffsetCommitEvidence{
		Topic: "execution-envelope", Partition: 1, NextOffset: 42,
	})

	for _, want := range []map[string]string{
		{"component": string(observability.ComponentAdapter), "stage": string(observability.StageMessageDecoded)},
		{"component": string(observability.ComponentDetect), "stage": string(observability.StageDetectCompleted)},
		{"component": string(observability.ComponentState), "stage": string(observability.StageDependencyLoaded)},
		{"component": string(observability.ComponentConsumer), "stage": string(observability.StageOffsetCommitted)},
	} {
		if !hasObservationMetric(t, recorder, want) {
			t.Fatalf("missing observation metric %v", want)
		}
	}
}

func TestObservedTriggerEventRuntimeRecordsBrokerACK(t *testing.T) {
	t.Parallel()

	recorder := metric.NewRecorder(metric.BuildInfo{})
	runtime := &observedTriggerEventRuntime{
		next: &fakeTriggerEventRuntime{}, observer: observability.Multi(recorder),
	}
	if err := runtime.WriteBatch(context.Background(), []contract.TriggerEventV1{{EventID: "event-1"}}); err != nil {
		t.Fatal(err)
	}
	if !hasObservationMetric(t, recorder, map[string]string{
		"component": string(observability.ComponentOutput), "stage": string(observability.StageOutputACKed),
		"result": string(observability.ResultSuccess),
	}) {
		t.Fatal("TriggerEvent ACK observation was not recorded")
	}
}

func TestRunApplicationRetriesStartupDependencyWhileHTTPStaysNotReady(t *testing.T) {
	t.Parallel()

	cfg := validApplicationConfig()
	cfg.DependencyRetry.MinDelay = config.Duration(time.Millisecond)
	cfg.DependencyRetry.MaxDelay = config.Duration(time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	service := newFakeServiceRuntime()
	service.snapshot = lifecycle.Snapshot{Ready: true, AssignedClaims: 1}
	service.run = func(ctx context.Context) error {
		cancel()
		<-ctx.Done()
		return nil
	}
	service.close = func() error { return nil }
	bundle := &applicationBundle{
		service: service,
		gate:    coordinator.NewCriticalDependencyGate(nil),
	}
	openAttempts := 0
	observedNotReady := false
	dependencies := applicationDependencies{
		openBundle: func(context.Context, config.Config, *metric.Recorder, *observability.Logger) (*applicationBundle, error) {
			openAttempts++
			if openAttempts == 1 {
				return nil, retryableStartupDependency(errors.New("broker unavailable"))
			}
			return bundle, nil
		},
		newHTTP: func(_ *metric.Recorder, source observability.HealthSource) (httpRuntime, error) {
			if snapshot := source.HealthSnapshot(); snapshot.Ready || snapshot.State != observability.HealthStarting {
				t.Fatalf("startup health = %+v, want starting and not ready", snapshot)
			}
			observedNotReady = true
			return &fakeHTTPRuntime{run: func(ctx context.Context, _ string, _ time.Duration) error {
				<-ctx.Done()
				return nil
			}}, nil
		},
	}

	if err := runApplication(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), dependencies); err != nil {
		t.Fatalf("runApplication() error = %v", err)
	}
	if !observedNotReady || openAttempts != 2 {
		t.Fatalf("observedNotReady=%v openAttempts=%d, want true and 2", observedNotReady, openAttempts)
	}
}

func TestApplicationHealthKeepsAssignmentSeparateFromDependencyGate(t *testing.T) {
	t.Parallel()

	health := newApplicationHealth()
	service := newFakeServiceRuntime()
	service.snapshot = lifecycle.Snapshot{Ready: false, AssignedClaims: 1}
	gate := coordinator.NewCriticalDependencyGate(nil)
	if _, err := gate.Pause(coordinator.DependencyBlocker{
		Dependency: coordinator.DependencyRedis,
		ReasonCode: contract.ReasonRedisUnavailable,
	}); err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	health.attach(&applicationBundle{service: service, gate: gate})
	snapshot := health.HealthSnapshot()
	if !snapshot.AssignmentReady || snapshot.Ready || snapshot.State != observability.HealthNotReady {
		t.Fatalf("health = %+v, want assignment ready but dependency-gated NotReady", snapshot)
	}
	if want := []observability.ReasonCode{observability.ReasonCode(contract.ReasonRedisUnavailable)}; !reflect.DeepEqual(snapshot.Reasons, want) {
		t.Fatalf("health reasons = %#v, want %#v", snapshot.Reasons, want)
	}
}

func TestApplicationBundleShutdownUsesReverseOrder(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	events := make([]string, 0, 4)
	add := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}
	service := newFakeServiceRuntime()
	service.close = func() error { add("service"); return nil }
	bundle := &applicationBundle{
		service:       service,
		receipts:      &fakeReceiptRuntime{shutdown: func(context.Context) { add("receipts") }},
		triggerEvents: &fakeTriggerEventRuntime{shutdown: func(context.Context) error { add("events"); return nil }},
		redis:         &fakeRedisRuntime{close: func() error { add("redis"); return nil }},
		gate:          coordinator.NewCriticalDependencyGate(nil),
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bundle.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	mu.Lock()
	got := strings.Join(events, ",")
	mu.Unlock()
	if got != "service,receipts,events,redis" {
		t.Fatalf("shutdown order = %q, want reverse dependency order", got)
	}
}

func TestApplicationBundleShutdownReturnsReceiptDrainError(t *testing.T) {
	t.Parallel()

	want := errors.New("receipt drain failed")
	bundle := &applicationBundle{
		receipts: &fakeReceiptRuntime{result: enginekafka.ReceiptDrainResult{
			Status: enginekafka.ReceiptDrainFailed,
			Err:    want,
		}},
	}
	if err := bundle.Shutdown(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Shutdown() error = %v, want receipt drain error", err)
	}
}

func TestOpenApplicationBundleDoesNotRetryUnclassifiedFactoryError(t *testing.T) {
	t.Parallel()

	want := errors.New("deterministic factory error")
	factories := applicationComponentFactories{
		newEffectiveTime: phaseOneEffectiveTimeProvider,
		openRedis:        func(state.RedisBackendOptions) (redisRuntime, error) { return &fakeRedisRuntime{}, nil },
		openTriggerEvents: func(enginekafka.DecisionSinkConfig) (triggerEventRuntime, error) {
			return nil, want
		},
		openReceipts: func(enginekafka.DecisionSinkConfig, enginekafka.ReceiptPublisherLimits) (receiptRuntime, error) {
			return &fakeReceiptRuntime{}, nil
		},
		openEvaluationService: func(
			enginekafka.Config,
			coordinator.MessageOutcomeRouter,
			coordinator.CriticalCompletion,
			coordinator.ReceiptPublisher,
			*coordinator.CriticalDependencyGate,
			enginekafka.EvaluationDiagnostics,
			coordinator.DependencyRetryConfig,
			time.Duration,
		) (serviceRuntime, error) {
			return newFakeServiceRuntime(), nil
		},
	}

	_, err := openApplicationBundleWithFactories(
		context.Background(), validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}),
		observability.Discard(observability.ComponentTrigger), factories,
	)
	if !errors.Is(err, want) {
		t.Fatalf("openApplicationBundleWithFactories() error = %v, want %v", err, want)
	}
	var dependency *startupDependencyError
	if errors.As(err, &dependency) {
		t.Fatalf("deterministic factory error was classified retryable: %v", err)
	}
}

func TestOpenApplicationBundleFailsOpenWhenReceiptKafkaIsUnavailable(t *testing.T) {
	t.Parallel()

	root := errors.New("receipt broker unavailable")
	service := newFakeServiceRuntime()
	service.close = func() error { return nil }
	var serviceReceipts coordinator.ReceiptPublisher
	factories := applicationComponentFactories{
		newEffectiveTime: phaseOneEffectiveTimeProvider,
		openRedis:        func(state.RedisBackendOptions) (redisRuntime, error) { return &fakeRedisRuntime{}, nil },
		openTriggerEvents: func(enginekafka.DecisionSinkConfig) (triggerEventRuntime, error) {
			return &fakeTriggerEventRuntime{}, nil
		},
		openReceipts: func(enginekafka.DecisionSinkConfig, enginekafka.ReceiptPublisherLimits) (receiptRuntime, error) {
			return nil, retryableStartupDependency(root)
		},
		openEvaluationService: func(
			_ enginekafka.Config,
			_ coordinator.MessageOutcomeRouter,
			_ coordinator.CriticalCompletion,
			receipts coordinator.ReceiptPublisher,
			_ *coordinator.CriticalDependencyGate,
			_ enginekafka.EvaluationDiagnostics,
			_ coordinator.DependencyRetryConfig,
			_ time.Duration,
		) (serviceRuntime, error) {
			serviceReceipts = receipts
			return service, nil
		},
	}
	recorder := metric.NewRecorder(metric.BuildInfo{})
	var output bytes.Buffer
	bundle, err := openApplicationBundleWithFactories(
		context.Background(), validApplicationConfig(), recorder,
		observability.New(observability.ComponentTrigger, &output), factories,
	)
	if err != nil {
		t.Fatalf("openApplicationBundleWithFactories() error = %v, want Receipt fail-open", err)
	}
	if bundle.service != service || bundle.receipts == nil || serviceReceipts != bundle.receipts {
		t.Fatal("evaluation service did not receive the fail-open Receipt publisher")
	}
	if bundle.receipts.TryEnqueue(&contract.MessageReceiptV1{}) {
		t.Fatal("unavailable Receipt publisher accepted an audit record")
	}
	drain := bundle.receipts.Shutdown(context.Background())
	if drain.Status != enginekafka.ReceiptDrainFailed || drain.Drops.Closed != 1 || !errors.Is(drain.Err, root) {
		t.Fatalf("Receipt drain = %+v, want one explicit drop with startup root cause", drain)
	}
	if err := bundle.Shutdown(context.Background()); !errors.Is(err, root) {
		t.Fatalf("bundle.Shutdown() error = %v, want Receipt startup root cause", err)
	}
	logOutput := output.String()
	for _, want := range []string{
		`"stage":"receipt_queued"`, `"result":"dropped"`,
		`"reason_code":"KAFKA_UNAVAILABLE"`, `"coverage_acceptable":false`,
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("log = %q, want %q", logOutput, want)
		}
	}
	if strings.Contains(logOutput, root.Error()) {
		t.Fatalf("Receipt fail-open log leaked broker error: %q", logOutput)
	}
	if !hasObservationMetric(t, recorder, map[string]string{
		"component":   string(observability.ComponentCoverage),
		"stage":       string(observability.StageReceiptQueued),
		"result":      string(observability.ResultDegraded),
		"reason_code": string(observability.ReasonContractRetryable),
	}) {
		t.Fatal("Receipt fail-open metric was not recorded")
	}
}

func TestOpenApplicationBundleRejectsDeterministicReceiptFactoryError(t *testing.T) {
	t.Parallel()

	want := errors.New("invalid Receipt configuration")
	factories := applicationComponentFactories{
		newEffectiveTime: phaseOneEffectiveTimeProvider,
		openRedis:        func(state.RedisBackendOptions) (redisRuntime, error) { return &fakeRedisRuntime{}, nil },
		openTriggerEvents: func(enginekafka.DecisionSinkConfig) (triggerEventRuntime, error) {
			return &fakeTriggerEventRuntime{}, nil
		},
		openReceipts: func(enginekafka.DecisionSinkConfig, enginekafka.ReceiptPublisherLimits) (receiptRuntime, error) {
			return nil, want
		},
		openEvaluationService: func(
			enginekafka.Config,
			coordinator.MessageOutcomeRouter,
			coordinator.CriticalCompletion,
			coordinator.ReceiptPublisher,
			*coordinator.CriticalDependencyGate,
			enginekafka.EvaluationDiagnostics,
			coordinator.DependencyRetryConfig,
			time.Duration,
		) (serviceRuntime, error) {
			t.Fatal("evaluation service opened after deterministic Receipt failure")
			return nil, nil
		},
	}

	_, err := openApplicationBundleWithFactories(
		context.Background(), validApplicationConfig(), metric.NewRecorder(metric.BuildInfo{}),
		observability.Discard(observability.ComponentTrigger), factories,
	)
	if !errors.Is(err, want) {
		t.Fatalf("openApplicationBundleWithFactories() error = %v, want deterministic error", err)
	}
	var dependency *startupDependencyError
	if errors.As(err, &dependency) {
		t.Fatalf("deterministic Receipt error was classified external: %v", err)
	}
}

func TestDependencyGateObserverRecordsPauseAndResume(t *testing.T) {
	t.Parallel()

	recorder := metric.NewRecorder(metric.BuildInfo{})
	var output bytes.Buffer
	gate := coordinator.NewCriticalDependencyGate(dependencyGateObserver(
		recorder, observability.New(observability.ComponentTrigger, &output),
	))
	if _, err := gate.Pause(coordinator.DependencyBlocker{
		Dependency: coordinator.DependencyRedis,
		ReasonCode: contract.ReasonRedisUnavailable,
	}); err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	if _, err := gate.Resume(coordinator.DependencyRedis); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}

	logOutput := output.String()
	for _, want := range []string{
		`"stage":"dependency_gate","result":"paused"`,
		`"stage":"dependency_gate","result":"resumed"`,
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("log = %q, want %q", logOutput, want)
		}
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	results := map[string]bool{}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_observation_total" {
			continue
		}
		for _, sample := range family.Metric {
			for _, label := range sample.Label {
				if label.GetName() == "result" {
					results[label.GetValue()] = true
				}
			}
		}
	}
	if !results[string(observability.ResultPaused)] || !results[string(observability.ResultResumed)] {
		t.Fatalf("dependency gate metrics results = %v, want paused and resumed", results)
	}
}

func TestDependencyGateObserverReportsRemovedBlockerAsResumedWhileOthersRemain(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	gate := coordinator.NewCriticalDependencyGate(dependencyGateObserver(
		metric.NewRecorder(metric.BuildInfo{}), observability.New(observability.ComponentTrigger, &output),
	))
	for _, blocker := range []coordinator.DependencyBlocker{
		{Dependency: coordinator.DependencyRedis, ReasonCode: contract.ReasonRedisUnavailable},
		{Dependency: coordinator.DependencyProvider, ReasonCode: contract.ReasonProviderUnavailable},
	} {
		if _, err := gate.Pause(blocker); err != nil {
			t.Fatalf("Pause(%+v) error = %v", blocker, err)
		}
	}
	if _, err := gate.Resume(coordinator.DependencyRedis); err != nil {
		t.Fatalf("Resume(redis) error = %v", err)
	}
	if gate.Ready() {
		t.Fatal("gate became ready while provider blocker remains")
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[2], `"result":"resumed"`) ||
		!strings.Contains(lines[2], `"dependency":"redis"`) ||
		!strings.Contains(lines[2], `"reason_code":"REDIS_UNAVAILABLE"`) {
		t.Fatalf("gate transition logs = %q, want removed Redis blocker reported as resumed", output.String())
	}
}

func TestConsumerDiagnosticsRecordsConsumeRetryWithoutErrorBody(t *testing.T) {
	t.Parallel()

	recorder := metric.NewRecorder(metric.BuildInfo{})
	var output bytes.Buffer
	diagnostics := consumerDiagnostics(recorder, observability.New(observability.ComponentTrigger, &output))
	if diagnostics.OnOffsetReset == nil || diagnostics.OnConsumeRetry == nil {
		t.Fatal("consumer diagnostics did not wire offset reset and consume retry")
	}
	diagnostics.OnConsumeRetry(enginekafka.ConsumerRetry{
		Source: enginekafka.ConsumerRetrySourceErrorsChannel,
		Err:    errors.New("secret broker address"),
	})
	logOutput := output.String()
	for _, want := range []string{
		`"stage":"consume_retry"`, `"result":"retrying"`,
		`"reason_code":"KAFKA_UNAVAILABLE"`, `"source":"errors_channel"`,
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("log = %q, want %q", logOutput, want)
		}
	}
	if strings.Contains(logOutput, "secret broker address") {
		t.Fatalf("consume retry log leaked broker error: %q", logOutput)
	}
	if !hasObservationMetric(t, recorder, map[string]string{
		"component":   string(observability.ComponentConsumer),
		"result":      string(observability.ResultRetrying),
		"reason_code": string(observability.ReasonContractRetryable),
	}) {
		t.Fatal("consume retry metric was not recorded")
	}
}

func hasObservationMetric(t *testing.T, recorder *metric.Recorder, want map[string]string) bool {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_observation_total" {
			continue
		}
		for _, sample := range family.Metric {
			labels := map[string]string{}
			for _, label := range sample.Label {
				labels[label.GetName()] = label.GetValue()
			}
			matched := true
			for name, value := range want {
				if labels[name] != value {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
		}
	}
	return false
}

func hasMetricFamily(t *testing.T, recorder *metric.Recorder, name string) bool {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	for _, family := range families {
		if family.GetName() == name {
			return true
		}
	}
	return false
}

type fakeRedisRuntime struct {
	pings int
	close func() error
}

func (runtime *fakeRedisRuntime) Ping(context.Context) error {
	runtime.pings++
	return nil
}

func (*fakeRedisRuntime) MGet(context.Context, []string) ([][]byte, error)    { return [][]byte{}, nil }
func (*fakeRedisRuntime) SetMany(context.Context, []state.BackendWrite) error { return nil }
func (runtime *fakeRedisRuntime) Close() error {
	if runtime.close != nil {
		return runtime.close()
	}
	return nil
}

type fakeTriggerEventRuntime struct {
	shutdown func(context.Context) error
}

func (*fakeTriggerEventRuntime) WriteBatch(context.Context, []contract.TriggerEventV1) error {
	return nil
}
func (runtime *fakeTriggerEventRuntime) Shutdown(ctx context.Context) error {
	if runtime.shutdown != nil {
		return runtime.shutdown(ctx)
	}
	return nil
}

type fakeReceiptRuntime struct {
	shutdown func(context.Context)
	result   enginekafka.ReceiptDrainResult
}

func (*fakeReceiptRuntime) TryEnqueue(*contract.MessageReceiptV1) bool { return true }
func (runtime *fakeReceiptRuntime) Shutdown(ctx context.Context) enginekafka.ReceiptDrainResult {
	if runtime.shutdown != nil {
		runtime.shutdown(ctx)
	}
	if runtime.result.Status != "" || runtime.result.Err != nil {
		return runtime.result
	}
	return enginekafka.ReceiptDrainResult{Status: enginekafka.ReceiptDrainSuccess}
}
