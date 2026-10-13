// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consume

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/processingerror"
)

func TestRuntimeCumulativeSettlementWaitsForContinuousPrefix(t *testing.T) {
	t.Parallel()

	session := newFakeSession(SettlementCumulative)
	session.addBatch("lane-a", "100", "101", "102", "103")
	releases := map[string]chan struct{}{
		"100": make(chan struct{}),
		"101": make(chan struct{}),
		"102": make(chan struct{}),
		"103": make(chan struct{}),
	}
	started := make(chan string, 4)
	handler := HandlerFunc(func(ctx context.Context, message Message) Outcome {
		started <- message.ID
		select {
		case <-ctx.Done():
			return Block(ctx.Err())
		case <-releases[message.ID]:
			return Complete()
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := runRuntime(t, ctx, testRuntimeConfig(), session, handler)
	waitStrings(t, started, 4)
	close(releases["101"])
	close(releases["103"])
	assertNoConfirm(t, session.confirms)

	close(releases["100"])
	if got := waitConfirm(t, session.confirms); !slices.Equal(got, []string{"100", "101"}) {
		t.Fatalf("first confirm = %v, want [100 101]", got)
	}
	close(releases["102"])
	if got := waitConfirm(t, session.confirms); !slices.Equal(got, []string{"102", "103"}) {
		t.Fatalf("second confirm = %v, want [102 103]", got)
	}
	cancel()
	if err := waitRuntime(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRuntimeIndividualSettlementDoesNotWaitForEarlierDelivery(t *testing.T) {
	t.Parallel()

	session := newFakeSession(SettlementIndividual)
	session.addBatch("queue-a", "first", "second")
	firstRelease := make(chan struct{})
	secondComplete := make(chan struct{})
	handler := HandlerFunc(func(ctx context.Context, message Message) Outcome {
		if message.ID == "first" {
			select {
			case <-ctx.Done():
				return Block(ctx.Err())
			case <-firstRelease:
				return Complete()
			}
		}
		close(secondComplete)
		return Complete()
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := runRuntime(t, ctx, testRuntimeConfig(), session, handler)
	select {
	case <-secondComplete:
	case <-time.After(time.Second):
		t.Fatal("second handler did not complete")
	}
	if got := waitConfirm(t, session.confirms); !slices.Equal(got, []string{"second"}) {
		t.Fatalf("first confirm = %v, want [second]", got)
	}
	close(firstRelease)
	if got := waitConfirm(t, session.confirms); !slices.Equal(got, []string{"first"}) {
		t.Fatalf("second confirm = %v, want [first]", got)
	}
	cancel()
	if err := waitRuntime(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRuntimeRetriesInMemoryBeforeConfirm(t *testing.T) {
	t.Parallel()

	session := newFakeSession(SettlementIndividual)
	session.addBatch("queue-a", "message-a")
	var attempts atomic.Int32
	handler := HandlerFunc(func(_ context.Context, _ Message) Outcome {
		if attempts.Add(1) == 1 {
			return Retry(processingerror.RetryableError{Err: processingerror.HTTPStatus(502)}, time.Millisecond)
		}
		return Complete()
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := runRuntime(t, ctx, testRuntimeConfig(), session, handler)
	if got := waitConfirm(t, session.confirms); !slices.Equal(got, []string{"message-a"}) {
		t.Fatalf("confirm = %v", got)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("handler attempts = %d, want 2", got)
	}
	cancel()
	if err := waitRuntime(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRuntimeRetryExhaustionConfirmsAndContinues(t *testing.T) {
	session := newFakeSession(SettlementIndividual)
	session.addBatch("queue-a", "bad", "good")
	var attempts atomic.Int32
	handler := HandlerFunc(func(_ context.Context, m Message) Outcome {
		if m.ID == "bad" {
			attempts.Add(1)
			return Retry(processingerror.RetryableError{Err: processingerror.HTTPStatus(502)}, time.Millisecond)
		}
		return Complete()
	})
	cfg := testRuntimeConfig()
	cfg.RetryMaxAttempts = 99
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runRuntime(t, ctx, cfg, session, handler)
	confirmed := map[string]bool{}
	for len(confirmed) < 2 {
		for _, id := range waitConfirm(t, session.confirms) {
			confirmed[id] = true
		}
	}
	if attempts.Load() != 3 {
		t.Fatal("attempt limit", attempts.Load())
	}
	cancel()
	if err := waitRuntime(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchReadyRetriesKeepsItemForBlockedLane(t *testing.T) {
	t.Parallel()

	now := time.Now()
	entry := &trackedDelivery{delivery: Delivery{Meta: DeliveryMeta{Lane: "queue-a"}}}
	state := runtimeState{lanes: map[string]*laneState{"queue-a": {blocked: true}}}
	state.retries.add(&retryItem{entry: entry, next: now.Add(-time.Second), seq: 1})

	state.dispatchReadyRetries(now)
	if len(state.retries) != 1 || state.retries[0].entry != entry {
		t.Fatalf("blocked retry queue = %#v, want original item", state.retries)
	}
	if state.workCount != 0 {
		t.Fatalf("blocked retry work count = %d, want 0", state.workCount)
	}
}

func TestRuntimeReportsStructuredObserverEvents(t *testing.T) {
	t.Parallel()

	session := newFakeSession(SettlementIndividual)
	session.addBatch("queue-a", "message-a")
	observer := &recordingObserver{}
	var calls atomic.Int32
	handler := HandlerFunc(func(_ context.Context, _ Message) Outcome {
		if calls.Add(1) == 1 {
			return Retry(processingerror.RetryableError{Err: processingerror.HTTPStatus(502)}, time.Millisecond)
		}
		return Complete()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- New(
			testRuntimeConfig(),
			session,
			handler,
			WithObserver(RuntimeLabels{Stage: "clean", Transport: "kafka"}, observer),
		).Run(ctx)
	}()
	_ = waitConfirm(t, session.confirms)
	cancel()
	if err := waitRuntime(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if observer.started != 2 || observer.outcomes[OutcomeRetry] != 1 || observer.outcomes[OutcomeComplete] != 1 {
		t.Fatalf("observer handler events = started:%d outcomes:%v", observer.started, observer.outcomes)
	}
	if observer.retries != 1 || observer.settlements != 1 || observer.settledMessages != 1 || observer.shutdowns != 1 || observer.snapshots == 0 {
		t.Fatalf(
			"observer events = retries:%d settlements:%d shutdowns:%d snapshots:%d",
			observer.retries,
			observer.settlements,
			observer.shutdowns,
			observer.snapshots,
		)
	}
}

func TestRuntimeRetriesConfirmationWithoutReprocessing(t *testing.T) {
	t.Parallel()

	session := newFakeSession(SettlementIndividual)
	session.confirmFailures.Store(1)
	session.addBatch("queue-a", "message-a")
	var calls atomic.Int32
	handler := HandlerFunc(func(_ context.Context, _ Message) Outcome {
		calls.Add(1)
		return Complete()
	})
	config := testRuntimeConfig()
	config.ConfirmRetryBackoff = time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := runRuntime(t, ctx, config, session, handler)
	if got := waitConfirm(t, session.confirms); !slices.Equal(got, []string{"message-a"}) {
		t.Fatalf("confirm = %v", got)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
	cancel()
	if err := waitRuntime(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRuntimeOrdinaryFailureConfirmsWithoutRetry(t *testing.T) {
	for _, panicMessage := range []bool{false, true} {
		t.Run(fmt.Sprint(panicMessage), func(t *testing.T) {
			session := newFakeSession(SettlementIndividual)
			session.addBatch("queue-a", "bad")
			var calls atomic.Int32
			handler := HandlerFunc(func(context.Context, Message) Outcome {
				calls.Add(1)
				if panicMessage {
					panic("private payload")
				}
				return Block(errors.New("logic failed"))
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := runRuntime(t, ctx, testRuntimeConfig(), session, handler)
			waitConfirm(t, session.confirms)
			cancel()
			if err := waitRuntime(t, done); err != nil || calls.Load() != 1 {
				t.Fatal(err, calls.Load())
			}
		})
	}
}

func TestRuntimeDiscardIsAConfirmableTerminalOutcome(t *testing.T) {
	t.Parallel()

	session := newFakeSession(SettlementIndividual)
	session.addBatch("queue-a", "invalid-message")
	handler := HandlerFunc(func(_ context.Context, _ Message) Outcome {
		return Discard(errors.New("diagnostic persisted"))
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := runRuntime(t, ctx, testRuntimeConfig(), session, handler)
	if got := waitConfirm(t, session.confirms); !slices.Equal(got, []string{"invalid-message"}) {
		t.Fatalf("confirm = %v", got)
	}
	cancel()
	if err := waitRuntime(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRuntimeBadMessageDoesNotBlockLaterOrderedMessages(t *testing.T) {
	session := newFakeSession(SettlementCumulative)
	session.addBatchWithOrderKey("queue-a", "same", "bad", "good")
	handler := HandlerFunc(func(_ context.Context, m Message) Outcome {
		if m.ID == "bad" {
			return Block(errors.New("bad JSON"))
		}
		return Complete()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runRuntime(t, ctx, testRuntimeConfig(), session, handler)
	seen := map[string]bool{}
	for !seen["good"] {
		for _, id := range waitConfirm(t, session.confirms) {
			seen[id] = true
		}
	}
	cancel()
	if err := waitRuntime(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSerializesSameOrderKey(t *testing.T) {
	t.Parallel()

	session := newFakeSession(SettlementIndividual)
	session.addBatchWithOrderKey("queue-a", "same-key", "first", "second")
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})
	handler := HandlerFunc(func(ctx context.Context, message Message) Outcome {
		if message.ID == "first" {
			close(firstStarted)
			select {
			case <-ctx.Done():
				return Block(ctx.Err())
			case <-firstRelease:
				return Complete()
			}
		}
		close(secondStarted)
		return Complete()
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := runRuntime(t, ctx, testRuntimeConfig(), session, handler)
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first handler did not start")
	}
	select {
	case <-secondStarted:
		t.Fatal("second handler started before first completed")
	case <-time.After(30 * time.Millisecond):
	}
	close(firstRelease)
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second handler did not start after first completed")
	}
	waitConfirm(t, session.confirms)
	waitConfirm(t, session.confirms)
	cancel()
	if err := waitRuntime(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRuntimeRejectsDeliveryBeyondByteLimit(t *testing.T) {
	t.Parallel()

	session := newFakeSession(SettlementIndividual)
	delivery := session.delivery("queue-a", "large", "")
	delivery.Message.Body = []byte("too large")
	session.batches <- []Delivery{delivery}
	config := testRuntimeConfig()
	config.MaxBatchBytes = 4
	config.MaxInflightBytes = 4

	done := runRuntime(t, context.Background(), config, session, HandlerFunc(func(context.Context, Message) Outcome {
		return Complete()
	}))
	err := waitRuntime(t, done)
	if !errors.Is(err, ErrReceiveLimitExceeded) {
		t.Fatalf("Run() error = %v, want ErrReceiveLimitExceeded", err)
	}
	assertNoConfirm(t, session.confirms)
}

func TestRuntimeDiscardsUnscopedMessageWithoutInvokingBusiness(t *testing.T) {
	session := newFakeSession(SettlementIndividual)
	delivery := session.delivery("queue-a", "message-a", "")
	delivery.Message.TenantID = ""
	session.batches <- []Delivery{delivery}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runRuntime(t, ctx, testRuntimeConfig(), session, HandlerFunc(func(context.Context, Message) Outcome { t.Error("unscoped business execution"); return Complete() }))
	waitConfirm(t, session.confirms)
	cancel()
	if err := waitRuntime(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestDependencyCancellationIsOrdinaryFailureWhenTaskRemainsActive(t *testing.T) {
	session := newFakeSession(SettlementIndividual)
	session.addBatch("queue-a", "message-a")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runRuntime(t, ctx, testRuntimeConfig(), session, HandlerFunc(func(context.Context, Message) Outcome { return Retry(context.Canceled, 0) }))
	waitConfirm(t, session.confirms)
	cancel()
	if err := waitRuntime(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDrainsCompletedWorkOnCancellation(t *testing.T) {
	t.Parallel()

	session := newFakeSession(SettlementIndividual)
	session.addBatch("queue-a", "message-a")
	started := make(chan struct{})
	release := make(chan struct{})
	handler := HandlerFunc(func(ctx context.Context, _ Message) Outcome {
		close(started)
		select {
		case <-ctx.Done():
			return Block(ctx.Err())
		case <-release:
			return Complete()
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := runRuntime(t, ctx, testRuntimeConfig(), session, handler)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	close(release)
	if got := waitConfirm(t, session.confirms); !slices.Equal(got, []string{"message-a"}) {
		t.Fatalf("confirm = %v", got)
	}
	if err := waitRuntime(t, done); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRuntimeShutdownRemainsBoundedWhenHandlerIgnoresCancellation(t *testing.T) {
	t.Parallel()

	session := newFakeSession(SettlementIndividual)
	session.addBatch("queue-a", "message-a")
	started := make(chan struct{})
	release := make(chan struct{})
	handler := HandlerFunc(func(_ context.Context, _ Message) Outcome {
		close(started)
		<-release
		return Complete()
	})
	config := testRuntimeConfig()
	config.ShutdownDrainTimeout = 20 * time.Millisecond
	config.SessionCloseTimeout = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := runRuntime(t, ctx, config, session, handler)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	err := waitRuntime(t, done)
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want deadline exceeded", err)
	}
}

func testRuntimeConfig() Config {
	config := DefaultConfig()
	config.WorkerCount = 4
	config.MaxBatchMessages = 8
	config.MaxBatchBytes = 1024
	config.MaxInflightMessages = 8
	config.MaxInflightBytes = 1024
	config.MaxInflightPerLane = 8
	config.ProcessTimeout = time.Second
	config.RetryMaxElapsed = 100 * time.Millisecond
	config.RetryBackoffMin = time.Millisecond
	config.RetryBackoffMax = 5 * time.Millisecond
	config.MaxRetryMessages = 8
	config.ConfirmRetryBackoff = time.Millisecond
	config.EmptyReceiveBackoff = time.Millisecond
	config.ShutdownDrainTimeout = time.Second
	config.SessionCloseTimeout = time.Second
	return config
}

func runRuntime(t *testing.T, ctx context.Context, config Config, session Session, handler Handler) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- New(config, session, handler).Run(ctx)
	}()
	return done
}

func waitRuntime(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not stop")
		return nil
	}
}

func waitStrings(t *testing.T, values <-chan string, count int) []string {
	t.Helper()
	result := make([]string, 0, count)
	for len(result) < count {
		select {
		case value := <-values:
			result = append(result, value)
		case <-time.After(time.Second):
			t.Fatalf("received %d values, want %d", len(result), count)
		}
	}
	return result
}

func waitConfirm(t *testing.T, confirms <-chan []string) []string {
	t.Helper()
	select {
	case confirmed := <-confirms:
		return confirmed
	case <-time.After(time.Second):
		t.Fatal("confirmation did not arrive")
		return nil
	}
}

func assertNoConfirm(t *testing.T, confirms <-chan []string) {
	t.Helper()
	select {
	case confirmed := <-confirms:
		t.Fatalf("unexpected confirm: %v", confirmed)
	case <-time.After(30 * time.Millisecond):
	}
}

type fakeSession struct {
	mode            SettlementMode
	issuer          *ReceiptIssuer
	batches         chan []Delivery
	confirms        chan []string
	confirmFailures atomic.Int32

	mu        sync.Mutex
	positions map[uint64]string
	closed    bool
}

type recordingObserver struct {
	mu              sync.Mutex
	started         int
	outcomes        map[OutcomeKind]int
	retries         int
	settlements     int
	settledMessages int
	snapshots       int
	shutdowns       int
}

func (o *recordingObserver) DeliveryReceived(context.Context, DeliveryObservation) {}

func (o *recordingObserver) HandlerStarted(context.Context, Message) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started++
	if o.outcomes == nil {
		o.outcomes = make(map[OutcomeKind]int)
	}
}

func (o *recordingObserver) HandlerFinished(_ context.Context, outcome OutcomeKind, _ time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.outcomes[outcome]++
}

func (o *recordingObserver) RetryScheduled(context.Context) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.retries++
}

func (o *recordingObserver) StepFinished(context.Context, StepObservation) {}

func (o *recordingObserver) SettlementFinished(_ context.Context, observation SettlementObservation) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.settlements++
	o.settledMessages += observation.Messages
}

func (o *recordingObserver) FlowTransition(context.Context, string) {}

func (o *recordingObserver) OwnershipChanged(context.Context, OwnershipObservation) {}

func (o *recordingObserver) Snapshot(context.Context, RuntimeSnapshot) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.snapshots++
}

func (o *recordingObserver) ShutdownFinished(context.Context, bool, time.Duration, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.shutdowns++
}

var _ Observer = (*recordingObserver)(nil)

func newFakeSession(mode SettlementMode) *fakeSession {
	return &fakeSession{
		mode:      mode,
		issuer:    NewReceiptIssuer(),
		batches:   make(chan []Delivery, 8),
		confirms:  make(chan []string, 8),
		positions: make(map[uint64]string),
	}
}

func (s *fakeSession) Capabilities() Capabilities {
	return Capabilities{Settlement: s.mode}
}

func (s *fakeSession) Receive(ctx context.Context, _ ReceiveLimits) ([]Delivery, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case deliveries := <-s.batches:
		return deliveries, nil
	}
}

func (s *fakeSession) Confirm(_ context.Context, receipts []Receipt) error {
	for {
		failures := s.confirmFailures.Load()
		if failures == 0 {
			break
		}
		if s.confirmFailures.CompareAndSwap(failures, failures-1) {
			return errors.New("temporary confirm failure")
		}
	}
	positions := make([]string, 0, len(receipts))
	s.mu.Lock()
	for _, receipt := range receipts {
		token, ok := s.issuer.Resolve(receipt)
		if !ok {
			s.mu.Unlock()
			return errors.New("foreign receipt")
		}
		positions = append(positions, s.positions[token])
	}
	s.mu.Unlock()
	s.confirms <- positions
	return nil
}

func (s *fakeSession) Close(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeSession) addBatch(lane string, ids ...string) {
	s.addBatchWithOrderKey(lane, "", ids...)
}

func (s *fakeSession) addBatchWithOrderKey(lane, orderKey string, ids ...string) {
	deliveries := make([]Delivery, 0, len(ids))
	for _, id := range ids {
		deliveries = append(deliveries, s.delivery(lane, id, orderKey))
	}
	s.batches <- deliveries
}

func (s *fakeSession) delivery(lane, id, orderKey string) Delivery {
	receipt, token := s.issuer.Issue()
	s.mu.Lock()
	s.positions[token] = id
	s.mu.Unlock()
	return Delivery{
		Message: Message{ID: id, TenantID: "tenant-a", OrderKey: orderKey, Body: []byte(fmt.Sprintf("body-%s", id))},
		Receipt: receipt,
		Meta:    DeliveryMeta{Transport: "fake", Lane: lane, Position: id, Attempt: 1},
	}
}

var _ Session = (*fakeSession)(nil)

type fakePausableSession struct {
	*fakeSession
	paused chan string
}

func (s *fakePausableSession) Capabilities() Capabilities {
	return Capabilities{Settlement: s.mode, CanPauseLane: true}
}

func (s *fakePausableSession) Pause(_ context.Context, lane string) error {
	s.paused <- lane
	return nil
}

var _ LanePauser = (*fakePausableSession)(nil)

// 全部在途消息可以因正常背压而延后；解除背压后仍应逐条确认且保持原 Receipt。
func TestRuntimeAllInflightCanDeferAndResume(t *testing.T) {
	t.Parallel()
	session := newFakeSession(SettlementIndividual)
	for batch := 0; batch < 4; batch++ {
		deliveries := make([]Delivery, 64)
		for i := range deliveries {
			id := fmt.Sprintf("%d-%d", batch, i)
			deliveries[i] = session.delivery("redis", id, id)
		}
		session.batches <- deliveries
	}
	config := DefaultConfig()
	config.WorkerCount = 64
	config.MaxBatchMessages = 64
	config.MaxInflightMessages = 256
	config.MaxInflightPerLane = 256
	config.MaxRetryMessages = 64
	var resume atomic.Bool
	var mu sync.Mutex
	seen := map[string]bool{}
	allDeferred := make(chan struct{})
	var once sync.Once
	handler := HandlerFunc(func(_ context.Context, m Message) Outcome {
		if resume.Load() {
			return Complete()
		}
		mu.Lock()
		seen[m.ID] = true
		if len(seen) == 256 {
			once.Do(func() { close(allDeferred) })
		}
		mu.Unlock()
		return Defer(time.Millisecond)
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := runRuntime(t, ctx, config, session, handler)
	select {
	case <-allDeferred:
	case err := <-done:
		t.Fatalf("normal defer stopped source: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("inflight messages did not all reach defer")
	}
	resume.Store(true)
	confirmed := map[string]bool{}
	for len(confirmed) < 256 {
		select {
		case ids := <-session.confirms:
			for _, id := range ids {
				confirmed[id] = true
			}
		case err := <-done:
			t.Fatalf("source stopped: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("messages did not drain")
		}
	}
	cancel()
	if err := waitRuntime(t, done); err != nil {
		t.Fatal(err)
	}
}
