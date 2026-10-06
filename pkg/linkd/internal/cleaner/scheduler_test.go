// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/kafkaclient"
)

func TestSchedulerStartsOneFlowPerEnabledEventSource(t *testing.T) {
	t.Parallel()

	sources := []config.EventSource{
		schedulerSource("source-a", true),
		schedulerSource("source-b", false),
		schedulerSource("source-c", true),
	}
	started := make(chan string, len(sources))
	factory := FlowFactoryFunc(func(_ context.Context, source config.EventSource) (Flow, error) {
		return FlowFunc(func(ctx context.Context) error {
			started <- source.EventSourceID
			<-ctx.Done()
			return ctx.Err()
		}), nil
	})
	scheduler, err := NewScheduler(sources, config.SeverityConfig{}, factory)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := runScheduler(ctx, scheduler)
	got := map[string]int{}
	for range 2 {
		select {
		case eventSourceID := <-started:
			got[eventSourceID]++
		case <-time.After(time.Second):
			t.Fatal("enabled event source flow did not start")
		}
	}
	if got["source-a"] != 1 || got["source-c"] != 1 || got["source-b"] != 0 {
		t.Fatalf("started flows = %v", got)
	}
	select {
	case eventSourceID := <-started:
		t.Fatalf("unexpected extra flow for %q", eventSourceID)
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	if err := waitScheduler(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestSchedulerFailsFastAndWaitsForSibling(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("source failed")
	started := make(chan string, 2)
	fail := make(chan struct{})
	siblingStopped := make(chan struct{})
	factory := FlowFactoryFunc(func(_ context.Context, source config.EventSource) (Flow, error) {
		return FlowFunc(func(ctx context.Context) error {
			started <- source.EventSourceID
			if source.EventSourceID == "source-a" {
				<-fail
				return wantErr
			}
			<-ctx.Done()
			close(siblingStopped)
			return ctx.Err()
		}), nil
	})
	scheduler, err := NewScheduler([]config.EventSource{
		schedulerSource("source-a", true),
		schedulerSource("source-b", true),
	}, config.SeverityConfig{}, factory)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}

	done := runScheduler(context.Background(), scheduler)
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("event source flows did not both start")
		}
	}
	close(fail)
	runErr := waitScheduler(t, done)
	if !errors.Is(runErr, wantErr) || !strings.Contains(runErr.Error(), `event source "source-a"`) {
		t.Fatalf("Run() error = %v", runErr)
	}
	select {
	case <-siblingStopped:
	default:
		t.Fatal("Run() returned before the sibling flow stopped")
	}
}

func TestSchedulerTreatsUnexpectedFlowStopAsFailure(t *testing.T) {
	t.Parallel()

	scheduler, err := NewScheduler(
		[]config.EventSource{schedulerSource("source-a", true)},
		config.SeverityConfig{},
		FlowFactoryFunc(func(context.Context, config.EventSource) (Flow, error) {
			return FlowFunc(func(context.Context) error { return nil }), nil
		}),
	)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	runErr := scheduler.Run(context.Background())
	if runErr == nil || !strings.Contains(runErr.Error(), "flow stopped unexpectedly") {
		t.Fatalf("Run() error = %v", runErr)
	}
}

func TestSchedulerDoesNotStartDisabledEventSources(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	factoryCalls := 0
	scheduler, err := NewScheduler(
		[]config.EventSource{schedulerSource("source-a", false)},
		config.SeverityConfig{},
		FlowFactoryFunc(func(context.Context, config.EventSource) (Flow, error) {
			mu.Lock()
			factoryCalls++
			mu.Unlock()
			return nil, nil
		}),
	)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := runScheduler(ctx, scheduler)
	cancel()
	if err := waitScheduler(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if factoryCalls != 0 {
		t.Fatalf("factory calls = %d, want 0", factoryCalls)
	}
}

func TestSchedulerCopiesEventSourceConfiguration(t *testing.T) {
	t.Parallel()

	source := schedulerSource("source-a", true)
	source.Storage.Kafka.Security = kafkaclient.SecurityConfig{
		Protocol: kafkaclient.SecurityProtocolSASLPlaintext,
		SASL: &kafkaclient.SASLConfig{
			Mechanism: kafkaclient.SASLMechanismPlain,
			Username:  "linkd",
			Password:  "secret",
		},
	}
	observed := make(chan config.EventSource, 1)
	scheduler, err := NewScheduler(
		[]config.EventSource{source},
		config.SeverityConfig{},
		FlowFactoryFunc(func(_ context.Context, source config.EventSource) (Flow, error) {
			observed <- source
			return FlowFunc(func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}), nil
		}),
	)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	source.Storage.Kafka.Brokers[0] = "changed.example.com:9092"
	source.Storage.Kafka.Security.SASL.Password = "changed"

	ctx, cancel := context.WithCancel(context.Background())
	done := runScheduler(ctx, scheduler)
	configured := <-observed
	if configured.Storage.Kafka.Brokers[0] == source.Storage.Kafka.Brokers[0] ||
		configured.Storage.Kafka.Security.SASL.Password == source.Storage.Kafka.Security.SASL.Password {
		t.Fatalf("scheduler retained mutable caller configuration: %#v", configured)
	}
	cancel()
	if err := waitScheduler(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestSchedulerValidatesConstructionAndSingleRun(t *testing.T) {
	t.Parallel()

	if _, err := NewScheduler(
		[]config.EventSource{schedulerSource("source-a", true)},
		config.SeverityConfig{},
		nil,
	); err == nil || !strings.Contains(err.Error(), "flow factory is nil") {
		t.Fatalf("NewScheduler(enabled source, nil factory) error = %v", err)
	}
	if _, err := NewScheduler(
		[]config.EventSource{schedulerSource("", true)},
		config.SeverityConfig{},
		FlowFactoryFunc(func(context.Context, config.EventSource) (Flow, error) { return nil, nil }),
	); err == nil || !strings.Contains(err.Error(), "event_source_id is required") {
		t.Fatalf("NewScheduler(invalid source) error = %v", err)
	}

	scheduler, err := NewScheduler(
		[]config.EventSource{schedulerSource("source-a", false)},
		config.SeverityConfig{},
		nil,
	)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	//nolint:staticcheck // SA1012: 这里故意验证 Scheduler 拒绝 nil Context 的边界。
	if err := scheduler.Run(nil); err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("Run(nil) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scheduler.Run(ctx); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := scheduler.Run(ctx); err == nil || !strings.Contains(err.Error(), "only run once") {
		t.Fatalf("second Run() error = %v", err)
	}
}

func schedulerSource(id string, enabled bool) config.EventSource {
	source := validConfigEventSource(id, id+".example.com:9092")
	source.Enabled = enabled
	return source
}

func validConfigEventSource(id, broker string) config.EventSource {
	return config.EventSource{
		EventSourceID: id,
		Enabled:       true,
		Storage: config.EventSourceStorageConfig{
			Type: config.StorageTypeKafka,
			Kafka: config.KafkaStorageConfig{
				Brokers:       []string{broker},
				Topic:         "raw-events",
				ConsumerGroup: "linkd",
				Security: kafkaclient.SecurityConfig{
					Protocol: kafkaclient.SecurityProtocolPlaintext,
				},
			},
		},
	}
}

func runScheduler(ctx context.Context, scheduler *Scheduler) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- scheduler.Run(ctx)
	}()
	return done
}

func waitScheduler(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
		return nil
	}
}
