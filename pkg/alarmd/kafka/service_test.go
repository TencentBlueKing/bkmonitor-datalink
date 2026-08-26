// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/consumer"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lifecycle"
)

func TestServiceRepeatsConsumeAfterRebalanceAndClosesNormally(t *testing.T) {
	t.Parallel()

	var consumeCalls atomic.Int32
	order := make([]string, 0, 2)
	var orderMu sync.Mutex
	group := newFakeConsumerGroup(func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
		if consumeCalls.Add(1) == 1 {
			return nil
		}
		<-ctx.Done()
		return ctx.Err()
	})
	group.closeFunc = func() error {
		orderMu.Lock()
		order = append(order, "group")
		orderMu.Unlock()
		return nil
	}
	client := &fakeServiceClient{closeFunc: func() error {
		orderMu.Lock()
		order = append(order, "client")
		orderMu.Unlock()
		return nil
	}}
	service := newTestService(t, group, client, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
	runContext, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(runContext) }()
	waitFor(t, func() bool { return consumeCalls.Load() >= 2 }, "second Consume call")
	cancelRun()
	if err := waitError(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	orderMu.Lock()
	defer orderMu.Unlock()
	if !reflect.DeepEqual(order, []string{"group", "client"}) {
		t.Fatalf("close order = %v, want group/client", order)
	}
	if snapshot := service.LifecycleSnapshot(); snapshot.Draining || snapshot.DrainTotal[lifecycle.DrainSuccess] != 1 {
		t.Fatalf("normal shutdown snapshot = %+v, want one successful drain", snapshot)
	}
}

func TestServiceRecoversTransientConsumeReturn(t *testing.T) {
	t.Parallel()

	for _, transient := range []error{sarama.ErrOutOfBrokers, sarama.ErrNotCoordinatorForConsumer} {
		transient := transient
		t.Run(transient.Error(), func(t *testing.T) {
			t.Parallel()

			var consumeCalls atomic.Int32
			group := newFakeConsumerGroup(func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
				if consumeCalls.Add(1) == 1 {
					return transient
				}
				<-ctx.Done()
				return ctx.Err()
			})
			observed := make(chan ConsumerRetry, 1)
			service := newTestService(t, group, &fakeServiceClient{}, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
			service.consumeRetryDelay = time.Nanosecond
			service.diagnostics.OnConsumeRetry = func(event ConsumerRetry) { observed <- event }
			runContext, cancelRun := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- service.Run(runContext) }()
			waitFor(t, func() bool { return consumeCalls.Load() >= 2 }, "Consume retry")
			select {
			case event := <-observed:
				if event.Source != ConsumerRetrySourceConsumeReturn || !errors.Is(event.Err, transient) {
					t.Fatalf("consumer retry = %#v", event)
				}
			default:
				t.Fatal("transient Consume return was not observed")
			}
			cancelRun()
			if err := waitError(t, done); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if service.FatalError() != nil {
				t.Fatalf("transient Consume return became fatal: %v", service.FatalError())
			}
		})
	}
}

func TestServiceRecoversTransientErrorsChannelWithoutDoubleConsumeLoop(t *testing.T) {
	t.Parallel()

	var consumeCalls atomic.Int32
	group := newFakeConsumerGroup(func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
		consumeCalls.Add(1)
		<-ctx.Done()
		return ctx.Err()
	})
	observed := make(chan ConsumerRetry, 1)
	service := newTestService(t, group, &fakeServiceClient{}, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
	service.consumeRetryDelay = time.Nanosecond
	service.diagnostics.OnConsumeRetry = func(event ConsumerRetry) { observed <- event }
	runContext, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(runContext) }()
	waitFor(t, func() bool { return consumeCalls.Load() == 1 }, "first Consume call")
	groupErrorChannel(group) <- &sarama.ConsumerError{Err: sarama.ErrOutOfBrokers}
	waitFor(t, func() bool { return consumeCalls.Load() == 2 }, "single recovered Consume call")
	time.Sleep(10 * time.Millisecond)
	if consumeCalls.Load() != 2 {
		t.Fatalf("Consume calls = %d, want exactly one recovered cycle", consumeCalls.Load())
	}
	select {
	case event := <-observed:
		if event.Source != ConsumerRetrySourceErrorsChannel || !errors.Is(event.Err, sarama.ErrOutOfBrokers) {
			t.Fatalf("consumer retry = %#v", event)
		}
	default:
		t.Fatal("transient group error was not observed")
	}
	cancelRun()
	if err := waitError(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestServiceCancellationStopsConsumeRetryAndPreservesObservedRoot(t *testing.T) {
	t.Parallel()

	root := sarama.ErrOutOfBrokers
	group := newFakeConsumerGroup(func(context.Context, []string, sarama.ConsumerGroupHandler) error { return root })
	observed := make(chan ConsumerRetry, 1)
	service := newTestService(t, group, &fakeServiceClient{}, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
	service.consumeRetryDelay = time.Hour
	service.diagnostics.OnConsumeRetry = func(event ConsumerRetry) { observed <- event }
	runContext, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(runContext) }()
	var event ConsumerRetry
	select {
	case event = <-observed:
	case <-time.After(time.Second):
		t.Fatal("transient Consume return was not observed")
	}
	cancelRun()
	if err := waitError(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if event.Source != ConsumerRetrySourceConsumeReturn || !errors.Is(event.Err, root) {
		t.Fatalf("consumer retry = %#v", event)
	}
}

func TestServiceDoesNotRetryAuthorizationOrLocalConsumeFailure(t *testing.T) {
	t.Parallel()

	for _, want := range []error{
		sarama.ErrGroupAuthorizationFailed,
		sarama.ConfigurationError("invalid config"),
		sarama.ErrClosedConsumerGroup,
		sarama.ErrClosedClient,
		errors.New("handler invariant"),
	} {
		want := want
		t.Run(want.Error(), func(t *testing.T) {
			t.Parallel()
			group := newFakeConsumerGroup(func(context.Context, []string, sarama.ConsumerGroupHandler) error { return want })
			service := newTestService(t, group, &fakeServiceClient{}, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
			if err := service.Run(context.Background()); !errors.Is(err, want) {
				t.Fatalf("Run() error = %v, want %v", err, want)
			}
			if group.consumeCalls.Load() != 1 || service.FatalError() == nil {
				t.Fatalf("Consume calls = %d, fatal = %v", group.consumeCalls.Load(), service.FatalError())
			}
		})
	}
}

func TestRetryableConsumerGroupErrorClassification(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "transport", err: sarama.ErrOutOfBrokers, want: true},
		{name: "coordinator", err: sarama.ErrNotCoordinatorForConsumer, want: true},
		{name: "metadata", err: sarama.ErrLeaderNotAvailable, want: true},
		{name: "wrapped consumer error", err: &sarama.ConsumerError{Err: sarama.ErrNetworkException}, want: true},
		{name: "authorization", err: sarama.ErrGroupAuthorizationFailed},
		{name: "configuration", err: sarama.ConfigurationError("invalid config")},
		{name: "closed group", err: sarama.ErrClosedConsumerGroup},
		{name: "closed client", err: sarama.ErrClosedClient},
		{name: "context cancellation", err: context.Canceled},
		{name: "local invariant", err: errors.New("handler invariant")},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := isRetryableConsumerGroupError(test.err); got != test.want {
				t.Fatalf("isRetryableConsumerGroupError(%v) = %t, want %t", test.err, got, test.want)
			}
		})
	}
}

func TestServiceDrainsGroupErrorsBeforeConsume(t *testing.T) {
	t.Parallel()

	want := errors.New("group transport failed")
	var group *fakeConsumerGroup
	group = newFakeConsumerGroup(func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
		select {
		case groupErrorChannel(group) <- want:
		case <-time.After(time.Second):
			return errors.New("group error was not drained")
		}
		<-ctx.Done()
		return ctx.Err()
	})
	client := &fakeServiceClient{}
	service := newTestService(t, group, client, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
	if err := service.Run(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want %v", err, want)
	}
	if snapshot := service.LifecycleSnapshot(); snapshot.FatalTotal != 1 || snapshot.DrainTotal[lifecycle.DrainSuccess] != 1 {
		t.Fatalf("fatal shutdown snapshot = %+v, want one fatal and successful drain", snapshot)
	}
}

func TestServiceRecoversOffsetOutOfRangeWithoutFatalExit(t *testing.T) {
	t.Parallel()

	var consumeCalls atomic.Int32
	group := newFakeConsumerGroup(func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
		consumeCalls.Add(1)
		<-ctx.Done()
		return ctx.Err()
	})
	recovered := make(chan OffsetReset, 1)
	service := newTestService(t, group, &fakeServiceClient{}, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
	var repairCalls atomic.Int32
	service.repairOffsets = func(context.Context) ([]OffsetReset, error) {
		if repairCalls.Add(1) == 1 {
			return nil, nil
		}
		return []OffsetReset{{Topic: "detect-input", Partition: 2, Offset: 99}}, nil
	}
	service.diagnostics = ConsumerDiagnostics{OnOffsetReset: func(event OffsetReset) { recovered <- event }}
	runContext, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(runContext) }()
	waitFor(t, func() bool { return consumeCalls.Load() == 1 }, "first Consume call")
	groupErrorChannel(group) <- &sarama.ConsumerError{Topic: "detect-input", Partition: 2, Err: sarama.ErrOffsetOutOfRange}
	waitFor(t, func() bool { return consumeCalls.Load() >= 2 }, "recovered Consume call")
	select {
	case event := <-recovered:
		if event.Topic != "detect-input" || event.Partition != 2 || event.Offset != 99 {
			t.Fatalf("offset reset = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("offset reset diagnostic was not recorded")
	}
	cancelRun()
	if err := waitError(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if snapshot := service.LifecycleSnapshot(); snapshot.FatalTotal != 0 {
		t.Fatalf("offset recovery snapshot = %+v, want no fatal", snapshot)
	}
}

func TestServiceRetriesRepairWhenClaimFallsBackToSymbolicOffset(t *testing.T) {
	t.Parallel()

	service := newTestService(
		t,
		newFakeConsumerGroup(nil),
		&fakeServiceClient{},
		noopProcessorFactory(),
		fakeSyncOffsetCommitter{},
		time.Second,
	)
	cycleCancelled := make(chan struct{})
	service.setCycleCancel(func() { close(cycleCancelled) })
	service.handleGroupError(&sarama.ConsumerError{
		Topic: "detect-input", Partition: 0,
		Err: fmt.Errorf("claim setup: %w", errComparatorSymbolicInitialOffset),
	})

	select {
	case <-cycleCancelled:
	case <-time.After(time.Second):
		t.Fatal("symbolic offset fallback did not restart the consume cycle")
	}
	if !service.offsetReset.Load() || service.firstFatal() != nil {
		t.Fatalf("offsetReset=%v fatal=%v, want recoverable retry", service.offsetReset.Load(), service.firstFatal())
	}
}

func TestServiceNormalCloseFailureRecordsFailedDrain(t *testing.T) {
	t.Parallel()

	want := errors.New("group close failed")
	group := newFakeConsumerGroup(func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
		<-ctx.Done()
		return ctx.Err()
	})
	group.closeFunc = func() error { return want }
	service := newTestService(t, group, &fakeServiceClient{}, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
	runContext, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(runContext) }()
	waitFor(t, func() bool { return group.consumeCalls.Load() == 1 }, "Consume call")
	cancelRun()
	if err := waitError(t, done); !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want close error %v", err, want)
	}
	if snapshot := service.LifecycleSnapshot(); snapshot.Draining || snapshot.DrainTotal[lifecycle.DrainFailed] != 1 {
		t.Fatalf("normal close failure snapshot = %+v, want one failed drain", snapshot)
	}
}

func TestServiceGracefulDrainWaitsForInflightCommit(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	events := make([]string, 0, 3)
	group := newHandlerConsumerGroup("trigger-input", 0, 1, &events)
	order := make([]string, 0, 2)
	group.closeFunc = func() error {
		order = append(order, "group")
		return nil
	}
	client := &fakeServiceClient{closeFunc: func() error {
		order = append(order, "client")
		return nil
	}}
	service := newTestService(t, group, client, func() consumer.Processor {
		return consumer.ProcessorFunc(func(context.Context, []byte, []byte) error {
			events = append(events, "process")
			close(started)
			<-release
			return nil
		})
	}, fakeSyncOffsetCommitter{events: &events}, time.Second)
	runContext, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(runContext) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("processor did not start")
	}
	cancelRun()
	select {
	case err := <-done:
		t.Fatalf("Run() returned before the in-flight record was released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := waitError(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"process", "commit", "mark"}) {
		t.Fatalf("events = %v, want process/commit/mark", events)
	}
	if !reflect.DeepEqual(order, []string{"group", "client"}) {
		t.Fatalf("close order = %v, want group/client", order)
	}
}

func TestServiceDrainTimeoutClosesClientBeforeGroup(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	order := make([]string, 0, 2)
	var orderMu sync.Mutex
	events := make([]string, 0, 1)
	group := newHandlerConsumerGroup("trigger-input", 0, 1, &events)
	group.closeFunc = func() error {
		orderMu.Lock()
		order = append(order, "group")
		orderMu.Unlock()
		return nil
	}
	client := &fakeServiceClient{closeFunc: func() error {
		orderMu.Lock()
		order = append(order, "client")
		orderMu.Unlock()
		releaseOnce.Do(func() { close(release) })
		return nil
	}}
	service := newTestService(t, group, client, func() consumer.Processor {
		return consumer.ProcessorFunc(func(context.Context, []byte, []byte) error {
			close(started)
			<-release
			return nil
		})
	}, fakeSyncOffsetCommitter{}, 20*time.Millisecond)
	runContext, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(runContext) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("processor did not start")
	}
	cancelRun()
	err := waitError(t, done)
	if !errors.Is(err, ErrDrainTimeout) {
		t.Fatalf("Run() error = %v, want ErrDrainTimeout", err)
	}
	if snapshot := service.LifecycleSnapshot(); snapshot.Draining || snapshot.DrainTotal[lifecycle.DrainTimeout] != 1 {
		t.Fatalf("timeout shutdown snapshot = %+v, want one timeout drain", snapshot)
	}
	waitFor(t, func() bool {
		orderMu.Lock()
		defer orderMu.Unlock()
		return len(order) == 2
	}, "forced resource close")
	orderMu.Lock()
	defer orderMu.Unlock()
	if !reflect.DeepEqual(order, []string{"client", "group"}) {
		t.Fatalf("close order = %v, want client/group", order)
	}
}

func TestServiceRejectsSecondRunAndClosesResourcesOnce(t *testing.T) {
	t.Parallel()

	group := newFakeConsumerGroup(func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
		<-ctx.Done()
		return ctx.Err()
	})
	client := &fakeServiceClient{}
	service := newTestService(t, group, client, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
	runContext, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(runContext) }()
	waitFor(t, func() bool { return group.consumeCalls.Load() == 1 }, "first Consume call")
	if err := service.Run(context.Background()); !errors.Is(err, ErrServiceAlreadyRun) {
		t.Fatalf("second Run() error = %v, want ErrServiceAlreadyRun", err)
	}
	cancelRun()
	if err := waitError(t, done); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if snapshot := service.LifecycleSnapshot(); snapshot.Draining || snapshot.DrainTotal[lifecycle.DrainSuccess] != 1 {
		t.Fatalf("repeated Close changed terminal lifecycle snapshot: %+v", snapshot)
	}
	if group.closeCalls.Load() != 1 || client.closeCalls.Load() != 1 {
		t.Fatalf("close calls group=%d client=%d, want 1/1", group.closeCalls.Load(), client.closeCalls.Load())
	}
}

func TestServicePreCanceledRunDoesNotConsume(t *testing.T) {
	t.Parallel()

	order := make([]string, 0, 2)
	group := newFakeConsumerGroup(func(context.Context, []string, sarama.ConsumerGroupHandler) error {
		return errors.New("Consume must not be called")
	})
	group.closeFunc = func() error {
		order = append(order, "group")
		return nil
	}
	client := &fakeServiceClient{closeFunc: func() error {
		order = append(order, "client")
		return nil
	}}
	service := newTestService(t, group, client, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if group.consumeCalls.Load() != 0 {
		t.Fatalf("Consume calls = %d, want 0", group.consumeCalls.Load())
	}
	if !reflect.DeepEqual(order, []string{"group", "client"}) {
		t.Fatalf("close order = %v, want group/client", order)
	}
}

func TestServiceStartupFatalDoesNotConsumeAndKeepsFirstError(t *testing.T) {
	t.Parallel()

	first := errors.New("startup failed")
	group := newFakeConsumerGroup(func(context.Context, []string, sarama.ConsumerGroupHandler) error {
		return errors.New("Consume must not be called")
	})
	client := &fakeServiceClient{}
	service := newTestService(t, group, client, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
	service.reportFatal(first)
	service.reportFatal(errors.New("later failure"))
	if err := service.Run(context.Background()); !errors.Is(err, first) {
		t.Fatalf("Run() error = %v, want first fatal %v", err, first)
	}
	if group.consumeCalls.Load() != 0 {
		t.Fatalf("Consume calls = %d, want 0", group.consumeCalls.Load())
	}
}

func TestServiceFatalSignalBroadcastsAfterReadinessDropsAndBeforeRunReturns(t *testing.T) {
	t.Parallel()

	setup := make(chan struct{})
	releaseClose := make(chan struct{})
	group := newFakeConsumerGroup(func(ctx context.Context, _ []string, handler sarama.ConsumerGroupHandler) error {
		session := newFakeSession(ctx, &[]string{})
		session.claims = map[string][]int32{}
		if err := handler.Setup(session); err != nil {
			return err
		}
		close(setup)
		<-ctx.Done()
		return handler.Cleanup(session)
	})
	group.closeFunc = func() error {
		<-releaseClose
		return nil
	}
	service := newTestService(t, group, &fakeServiceClient{}, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
	runDone := make(chan error, 1)
	go func() { runDone <- service.Run(context.Background()) }()
	select {
	case <-setup:
	case <-time.After(time.Second):
		t.Fatal("assignment was not set up")
	}
	waitFor(t, service.Ready, "service readiness")

	firstObserver := service.FatalSignal()
	secondObserver := service.FatalSignal()
	want := errors.New("consumer failed")
	service.reportFatal(want)

	for index, observer := range []<-chan struct{}{firstObserver, secondObserver} {
		select {
		case <-observer:
		case <-time.After(time.Second):
			t.Fatalf("fatal observer %d was not notified", index)
		}
	}
	if service.Ready() {
		t.Fatal("service remained ready after fatal notification")
	}
	if err := service.FatalError(); !errors.Is(err, want) {
		t.Fatalf("FatalError() = %v, want %v", err, want)
	}
	select {
	case err := <-runDone:
		t.Fatalf("Run() returned before resource close was released: %v", err)
	default:
	}

	close(releaseClose)
	if err := waitError(t, runDone); !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want %v", err, want)
	}
}

func TestServiceReadyTracksAssignmentAndShutdown(t *testing.T) {
	t.Parallel()

	setup := make(chan struct{})
	group := newFakeConsumerGroup(func(ctx context.Context, _ []string, handler sarama.ConsumerGroupHandler) error {
		session := newFakeSession(ctx, &[]string{})
		session.claims = map[string][]int32{}
		if err := handler.Setup(session); err != nil {
			return err
		}
		close(setup)
		<-ctx.Done()
		return handler.Cleanup(session)
	})
	service := newTestService(t, group, &fakeServiceClient{}, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
	if service.Ready() {
		t.Fatal("service was ready before Run")
	}
	runContext, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(runContext) }()
	select {
	case <-setup:
	case <-time.After(time.Second):
		t.Fatal("assignment was not set up")
	}
	waitFor(t, service.Ready, "service readiness")
	cancelRun()
	waitFor(t, func() bool { return !service.Ready() }, "service not-ready during shutdown")
	if err := waitError(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if service.Ready() {
		t.Fatal("service remained ready after Run returned")
	}
}

func TestServiceNormalCloseTimeoutUsesClientToInterruptGroup(t *testing.T) {
	t.Parallel()

	groupStarted := make(chan struct{})
	clientClosed := make(chan struct{})
	order := make([]string, 0, 3)
	var orderMu sync.Mutex
	group := newFakeConsumerGroup(func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
		<-ctx.Done()
		return ctx.Err()
	})
	group.closeFunc = func() error {
		orderMu.Lock()
		order = append(order, "group-start")
		orderMu.Unlock()
		close(groupStarted)
		<-clientClosed
		orderMu.Lock()
		order = append(order, "group-end")
		orderMu.Unlock()
		return sarama.ErrClosedClient
	}
	client := &fakeServiceClient{closeFunc: func() error {
		orderMu.Lock()
		order = append(order, "client")
		orderMu.Unlock()
		close(clientClosed)
		return nil
	}}
	service := newTestService(t, group, client, noopProcessorFactory(), fakeSyncOffsetCommitter{}, 20*time.Millisecond)
	runContext, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(runContext) }()
	waitFor(t, func() bool { return group.consumeCalls.Load() > 0 }, "Consume call")
	cancelRun()
	select {
	case <-groupStarted:
	case <-time.After(time.Second):
		t.Fatal("normal group close did not start")
	}
	err := waitError(t, done)
	if !errors.Is(err, ErrDrainTimeout) || errors.Is(err, sarama.ErrClosedClient) {
		t.Fatalf("Run() error = %v, want timeout without derived ErrClosedClient", err)
	}
	waitFor(t, func() bool {
		orderMu.Lock()
		defer orderMu.Unlock()
		return len(order) == 3
	}, "forced client interrupt and group close")
	orderMu.Lock()
	defer orderMu.Unlock()
	if !reflect.DeepEqual(order, []string{"group-start", "client", "group-end"}) {
		t.Fatalf("close order = %v, want group-start/client/group-end", order)
	}
}

func TestServiceShutdownDeadlineBoundsStuckCloseAndErrors(t *testing.T) {
	t.Parallel()

	releaseGroup := make(chan struct{})
	group := newFakeConsumerGroup(func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
		<-ctx.Done()
		return ctx.Err()
	})
	group.closeFunc = func() error {
		<-releaseGroup
		return nil
	}
	service := newTestService(t, group, &fakeServiceClient{}, noopProcessorFactory(), fakeSyncOffsetCommitter{}, 20*time.Millisecond)
	runContext, cancelRun := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(runContext) }()
	waitFor(t, func() bool { return group.consumeCalls.Load() > 0 }, "Consume call")
	started := time.Now()
	cancelRun()
	err := waitError(t, done)
	if !errors.Is(err, ErrDrainTimeout) {
		t.Fatalf("Run() error = %v, want ErrDrainTimeout", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Run() exceeded bounded shutdown: %s", elapsed)
	}
	close(releaseGroup)
}

func TestServiceConcurrentForcedCloseNormalizesDerivedClosedClient(t *testing.T) {
	t.Parallel()

	groupStarted := make(chan struct{})
	clientClosed := make(chan struct{})
	group := newFakeConsumerGroup(func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
		<-ctx.Done()
		return ctx.Err()
	})
	group.closeFunc = func() error {
		close(groupStarted)
		<-clientClosed
		return sarama.ErrClosedClient
	}
	client := &fakeServiceClient{closeFunc: func() error {
		close(clientClosed)
		return nil
	}}
	service := newTestService(t, group, client, noopProcessorFactory(), fakeSyncOffsetCommitter{}, time.Second)
	runContext, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- service.Run(runContext) }()
	waitFor(t, func() bool { return group.consumeCalls.Load() > 0 }, "Consume call")
	cancelRun()
	select {
	case <-groupStarted:
	case <-time.After(time.Second):
		t.Fatal("normal group close did not start")
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := waitError(t, runDone); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if snapshot := service.LifecycleSnapshot(); snapshot.DrainTotal[lifecycle.DrainTimeout] != 1 {
		t.Fatalf("concurrent forced close snapshot = %+v, want one timeout drain", snapshot)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("repeated Close() error = %v", err)
	}
	if snapshot := service.LifecycleSnapshot(); snapshot.Draining {
		t.Fatalf("repeated Close after concurrent forced close restored draining: %+v", snapshot)
	}
	if group.closeCalls.Load() != 1 || client.closeCalls.Load() != 1 {
		t.Fatalf("close calls group=%d client=%d, want 1/1", group.closeCalls.Load(), client.closeCalls.Load())
	}
}

func TestOwnedGroupServiceConsumesAllComparatorTopics(t *testing.T) {
	t.Parallel()

	wantTopics := []string{"trigger-input", "go-decision", "py-decision"}
	observed := make(chan []string, 1)
	group := newFakeConsumerGroup(func(ctx context.Context, topics []string, _ sarama.ConsumerGroupHandler) error {
		observed <- append([]string(nil), topics...)
		<-ctx.Done()
		return ctx.Err()
	})
	client := &fakeServiceClient{}
	service, err := newOwnedGroupService(
		wantTopics,
		group,
		client,
		func(reportFatal func(error)) (serviceHandler, error) {
			return NewHandler(noopProcessorFactory(), fakeSyncOffsetCommitter{}, reportFatal), nil
		},
		time.Second,
	)
	if err != nil {
		t.Fatalf("newOwnedGroupService() error = %v", err)
	}
	runContext, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- service.Run(runContext) }()
	select {
	case topics := <-observed:
		if !reflect.DeepEqual(topics, wantTopics) {
			t.Fatalf("Consume() topics = %v, want %v", topics, wantTopics)
		}
	case <-time.After(time.Second):
		t.Fatal("service did not start consuming")
	}
	cancelRun()
	if err := waitError(t, runDone); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestServiceForcedCloseReturnsKnownClientErrorAtDeadline(t *testing.T) {
	t.Parallel()

	want := errors.New("client close failed")
	releaseGroup := make(chan struct{})
	group := newFakeConsumerGroup(func(ctx context.Context, _ []string, _ sarama.ConsumerGroupHandler) error {
		<-ctx.Done()
		return ctx.Err()
	})
	group.closeFunc = func() error {
		<-releaseGroup
		return nil
	}
	client := &fakeServiceClient{closeFunc: func() error { return want }}
	service := newTestService(t, group, client, noopProcessorFactory(), fakeSyncOffsetCommitter{}, 20*time.Millisecond)
	err := service.Close()
	if !errors.Is(err, ErrDrainTimeout) || !errors.Is(err, want) {
		t.Fatalf("Close() error = %v, want timeout joined with client error", err)
	}
	if snapshot := service.LifecycleSnapshot(); snapshot.Draining || snapshot.DrainTotal[lifecycle.DrainFailed] != 1 {
		t.Fatalf("failed forced close snapshot = %+v, want one failed drain", snapshot)
	}
	close(releaseGroup)
}

func noopProcessorFactory() consumer.ProcessorFactory {
	return func() consumer.Processor {
		return consumer.ProcessorFunc(func(context.Context, []byte, []byte) error { return nil })
	}
}

func newTestService(
	t *testing.T,
	group consumerGroup,
	client serviceClient,
	newProcessor consumer.ProcessorFactory,
	offsets OffsetCommitter,
	drainTimeout time.Duration,
) *Service {
	t.Helper()
	service, err := newOwnedService("trigger-input", group, client, newProcessor, offsets, drainTimeout)
	if err != nil {
		t.Fatalf("newOwnedService() error = %v", err)
	}
	return service
}

type fakeConsumerGroup struct {
	errors       chan error
	consume      func(context.Context, []string, sarama.ConsumerGroupHandler) error
	closeFunc    func() error
	consumeCalls atomic.Int32
	closeCalls   atomic.Int32
	closeOnce    sync.Once
}

func newFakeConsumerGroup(consume func(context.Context, []string, sarama.ConsumerGroupHandler) error) *fakeConsumerGroup {
	return &fakeConsumerGroup{errors: make(chan error), consume: consume}
}

func (g *fakeConsumerGroup) Consume(ctx context.Context, topics []string, handler sarama.ConsumerGroupHandler) error {
	g.consumeCalls.Add(1)
	return g.consume(ctx, topics, handler)
}

func (g *fakeConsumerGroup) Errors() <-chan error { return g.errors }

func (g *fakeConsumerGroup) Close() error {
	g.closeCalls.Add(1)
	var err error
	g.closeOnce.Do(func() {
		if g.closeFunc != nil {
			err = g.closeFunc()
		}
		close(g.errors)
	})
	return err
}

func groupErrorChannel(group *fakeConsumerGroup) chan<- error { return group.errors }

func newHandlerConsumerGroup(topic string, partition int32, offset int64, events *[]string) *fakeConsumerGroup {
	return newFakeConsumerGroup(func(ctx context.Context, _ []string, handler sarama.ConsumerGroupHandler) error {
		session := newFakeSession(ctx, events)
		session.claims = map[string][]int32{topic: {partition}}
		messages := make(chan *sarama.ConsumerMessage, 1)
		messages <- &sarama.ConsumerMessage{Topic: topic, Partition: partition, Offset: offset}
		claim := &fakeClaim{topic: topic, partition: partition, messages: messages}
		if err := handler.Setup(session); err != nil {
			return err
		}
		err := handler.ConsumeClaim(session, claim)
		cleanupErr := handler.Cleanup(session)
		return errors.Join(err, cleanupErr)
	})
}

type fakeServiceClient struct {
	closeFunc  func() error
	closeCalls atomic.Int32
}

func (c *fakeServiceClient) Close() error {
	c.closeCalls.Add(1)
	if c.closeFunc != nil {
		return c.closeFunc()
	}
	return nil
}

func waitFor(t *testing.T, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !condition() {
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitError(t *testing.T, errors <-chan error) error {
	t.Helper()
	select {
	case err := <-errors:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for service")
		return nil
	}
}
