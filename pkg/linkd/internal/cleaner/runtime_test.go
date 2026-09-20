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
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/store"
)

type runtimeTestSession struct {
	mu          sync.Mutex
	deliveries  []consume.Delivery
	confirmed   []string
	issuer      *consume.ReceiptIssuer
	receiptIDs  map[uint64]string
	cancel      context.CancelFunc
	cancelAfter int
	closed      bool
	closeErr    error
	flowPaused  bool
	pauseFlow   int
	resumeFlow  int
	receives    int
}

func newRuntimeTestSession(cancel context.CancelFunc, values ...struct{ lane, id string }) *runtimeTestSession {
	issuer := consume.NewReceiptIssuer()
	session := &runtimeTestSession{issuer: issuer, receiptIDs: make(map[uint64]string), cancel: cancel, cancelAfter: 3}
	for _, value := range values {
		receipt, token := issuer.Issue()
		session.receiptIDs[token] = value.id
		session.deliveries = append(session.deliveries, consume.Delivery{
			Message: consume.Message{ID: value.id, Body: []byte(value.id), EnqueuedAt: time.Now()}, Receipt: receipt,
			Meta: consume.DeliveryMeta{Transport: "fake", Lane: value.lane, Position: value.id, Attempt: 1},
		})
	}
	return session
}

func (*runtimeTestSession) Capabilities() consume.Capabilities {
	return consume.Capabilities{Settlement: consume.SettlementCumulative}
}

func (s *runtimeTestSession) Receive(ctx context.Context, _ consume.ReceiveLimits) ([]consume.Delivery, error) {
	s.mu.Lock()
	s.receives++
	if s.flowPaused {
		s.mu.Unlock()
		return nil, nil
	}
	if len(s.deliveries) > 0 {
		items := s.deliveries
		s.deliveries = nil
		s.mu.Unlock()
		return items, nil
	}
	s.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}

func (s *runtimeTestSession) PauseFlow(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.flowPaused {
		s.flowPaused = true
		s.pauseFlow++
	}
	return nil
}

func (s *runtimeTestSession) ResumeFlow(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.flowPaused {
		s.flowPaused = false
		s.resumeFlow++
	}
	return nil
}

func (s *runtimeTestSession) Confirm(_ context.Context, receipts []consume.Receipt) error {
	s.mu.Lock()
	for _, receipt := range receipts {
		token, ok := s.issuer.Resolve(receipt)
		if !ok {
			s.mu.Unlock()
			return errors.New("receipt belongs to another session")
		}
		id, ok := s.receiptIDs[token]
		if !ok {
			s.mu.Unlock()
			return errors.New("receipt was already confirmed")
		}
		s.confirmed = append(s.confirmed, id)
		delete(s.receiptIDs, token)
	}
	shouldCancel := len(s.confirmed) >= s.cancelAfter && s.cancel != nil
	cancel := s.cancel
	s.mu.Unlock()
	if shouldCancel {
		cancel()
	}
	return nil
}

func (s *runtimeTestSession) Close(context.Context) error {
	s.closed = true
	return s.closeErr
}

type runtimeTestProcessor struct{}

func (runtimeTestProcessor) Process(_ context.Context, message consume.Message) (ProcessResult, error) {
	if message.ID == "a1" {
		time.Sleep(20 * time.Millisecond)
	}
	if message.ID == "bad" {
		return ProcessResult{DiscardErr: context.Canceled}, nil
	}
	return ProcessResult{Event: domain.Event{
		BKTenantID: "tenant", EventSourceID: "source", EventID: message.ID,
		Fingerprint: message.ID, Title: message.ID,
	}}, nil
}

type runtimeSequenceGate struct{ calls atomic.Int64 }

func (g *runtimeSequenceGate) Check(context.Context) ReceiveDecision {
	return ReceiveDecision{Allowed: g.calls.Add(1) > 1, RecheckAt: time.Now().Add(time.Millisecond)}
}

type runtimeRetryProcessor struct {
	mu       sync.Mutex
	attempts map[string]int
}

func (p *runtimeRetryProcessor) Process(_ context.Context, message consume.Message) (ProcessResult, error) {
	p.mu.Lock()
	p.attempts[message.ID]++
	attempt := p.attempts[message.ID]
	p.mu.Unlock()
	if message.ID == "a1" && attempt == 1 {
		return ProcessResult{}, errors.New("temporary processing failure")
	}
	return runtimeTestProcessor{}.Process(context.Background(), message)
}

type runtimeTestWriter struct{}

func (*runtimeTestWriter) CreateEvents(_ context.Context, events []domain.Event) ([]store.CreateEventItemResult, error) {
	results := make([]store.CreateEventItemResult, len(events))
	for index, event := range events {
		results[index].Result = store.CreateEventResult{StoredEvent: store.StoredEvent{Event: event}, Created: true}
	}
	return results, nil
}

type runtimePartialWriter struct{ failure error }

func (w runtimePartialWriter) CreateEvents(_ context.Context, events []domain.Event) ([]store.CreateEventItemResult, error) {
	results := make([]store.CreateEventItemResult, len(events))
	for index, event := range events {
		results[index].Result = store.CreateEventResult{StoredEvent: store.StoredEvent{Event: event}, Created: true}
	}
	if len(results) > 1 {
		results[1] = store.CreateEventItemResult{Err: w.failure}
	}
	return results, nil
}

type runtimeCountingWriter struct{ sizes chan int }

func (w runtimeCountingWriter) CreateEvents(_ context.Context, events []domain.Event) ([]store.CreateEventItemResult, error) {
	w.sizes <- len(events)
	return (&runtimeTestWriter{}).CreateEvents(context.Background(), events)
}

type runtimeTerminalReplayWriter struct{}

func (runtimeTerminalReplayWriter) CreateEvents(_ context.Context, events []domain.Event) ([]store.CreateEventItemResult, error) {
	results := make([]store.CreateEventItemResult, len(events))
	for index, event := range events {
		results[index].Result = store.CreateEventResult{
			StoredEvent: store.StoredEvent{
				Event: event, Processing: store.EventProcessing{State: domain.EventProcessStateAccepted},
			},
			Created: false,
		}
	}
	return results, nil
}

type runtimeUnprocessedReplayWriter struct{}

func (runtimeUnprocessedReplayWriter) CreateEvents(_ context.Context, events []domain.Event) ([]store.CreateEventItemResult, error) {
	results := make([]store.CreateEventItemResult, len(events))
	for index, event := range events {
		results[index].Result = store.CreateEventResult{
			StoredEvent: store.StoredEvent{Event: event, Processing: store.NewUnprocessedEventProcessing()},
			Created:     false,
		}
	}
	return results, nil
}

type runtimeTestMailbox struct {
	mu      sync.Mutex
	byLane  map[string][]string
	current string
}

func (m *runtimeTestMailbox) EnqueueBatch(_ context.Context, events []domain.Event) ([]MailboxEnqueueResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	results := make([]MailboxEnqueueResult, len(events))
	for index, event := range events {
		m.byLane[m.current] = append(m.byLane[m.current], event.EventID)
		results[index] = MailboxEnqueueResult{}
	}
	return results, nil
}

func TestRuntimeRestoresLaneOrderAfterConcurrentProcessing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	session := newRuntimeTestSession(cancel,
		struct{ lane, id string }{"lane-a", "a1"},
		struct{ lane, id string }{"lane-a", "a2"},
		struct{ lane, id string }{"lane-b", "b1"},
	)
	mailbox := &runtimeTestMailbox{byLane: map[string][]string{}}
	// Mailbox 端口不接收 lane；本测试用 Event ID 前缀验证同 lane 顺序。
	mailbox.current = "all"
	cfg := config.DefaultCleanerRuntimeConfig()
	cfg.BatchWaitMilliseconds = 1
	writer := &runtimeTestWriter{}
	runtime, err := NewRuntime(cfg, session, runtimeTestProcessor{}, writer, mailbox, allowReceiveGate{},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Run(ctx); err != nil {
		t.Fatal(err)
	}
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	positions := map[string]int{}
	for index, id := range mailbox.byLane["all"] {
		positions[id] = index
	}
	if positions["a1"] >= positions["a2"] || positions["b1"] >= positions["a1"] || len(session.confirmed) != 3 {
		t.Fatalf("mailbox=%v confirmed=%d", mailbox.byLane["all"], len(session.confirmed))
	}
}

func TestRuntimeIgnoresSessionCloseDeadlineAfterRequestedShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	session := newRuntimeTestSession(nil)
	session.closeErr = context.DeadlineExceeded
	runtime, err := NewRuntime(
		config.DefaultCleanerRuntimeConfig(),
		session,
		runtimeTestProcessor{},
		&runtimeTestWriter{},
		&runtimeTestMailbox{byLane: map[string][]string{}},
		allowReceiveGate{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !session.closed {
		t.Fatal("session was not closed")
	}
}

func TestRuntimePollsPausedFlowAndResumesAfterBackpressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	session := newRuntimeTestSession(cancel, struct{ lane, id string }{"lane-a", "event-1"})
	session.cancelAfter = 1
	gate := &runtimeSequenceGate{}
	runtime, err := NewRuntime(
		config.DefaultCleanerRuntimeConfig(), session, runtimeTestProcessor{}, &runtimeTestWriter{},
		&runtimeTestMailbox{byLane: map[string][]string{}}, gate, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Run(ctx); err != nil {
		t.Fatal(err)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.pauseFlow != 1 || session.resumeFlow != 1 || session.receives < 2 {
		t.Fatalf("pause=%d resume=%d receives=%d", session.pauseFlow, session.resumeFlow, session.receives)
	}
}

func TestRuntimeProcessingRetryKeepsLaneGap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	session := newRuntimeTestSession(cancel,
		struct{ lane, id string }{"lane-a", "a1"},
		struct{ lane, id string }{"lane-a", "a2"},
	)
	session.cancelAfter = 2
	processor := &runtimeRetryProcessor{attempts: make(map[string]int)}
	writer := &runtimeTestWriter{}
	mailbox := &runtimeTestMailbox{byLane: map[string][]string{}, current: "all"}
	cfg := config.DefaultCleanerRuntimeConfig()
	cfg.BatchWaitMilliseconds = 1
	runtime, err := NewRuntime(cfg, session, processor, writer, mailbox, allowReceiveGate{},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Run(ctx); err != nil {
		t.Fatal(err)
	}
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	if got := mailbox.byLane["all"]; len(got) != 2 || got[0] != "a1" || got[1] != "a2" {
		t.Fatalf("mailbox order after retry=%v", got)
	}
}

func TestRuntimePartialBulkResultAdvancesOnlyContinuousPrefix(t *testing.T) {
	temporary := errors.New("temporary store failure")
	session := newRuntimeTestSession(nil,
		struct{ lane, id string }{"lane-a", "a1"},
		struct{ lane, id string }{"lane-a", "a2"},
		struct{ lane, id string }{"lane-a", "a3"},
	)
	entries := make([]*cleanerEntry, len(session.deliveries))
	for index, delivery := range session.deliveries {
		entries[index] = &cleanerEntry{
			delivery: delivery,
			readyAt:  time.Now(),
			event: domain.Event{
				BKTenantID: "tenant", EventSourceID: "source", EventID: delivery.Message.ID,
				Fingerprint: delivery.Message.ID, Title: delivery.Message.ID,
			},
		}
	}
	mailbox := &runtimeTestMailbox{byLane: map[string][]string{}, current: "all"}
	runtime, err := NewRuntime(config.DefaultCleanerRuntimeConfig(), session, runtimeTestProcessor{},
		runtimePartialWriter{failure: temporary}, mailbox, allowReceiveGate{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	result := runtime.runLaneBatch(context.Background(), "lane-a", entries)
	if result.settled != 1 || !errors.Is(result.err, temporary) {
		t.Fatalf("batch result=%+v", result)
	}
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	if got := mailbox.byLane["all"]; len(got) != 1 || got[0] != "a1" {
		t.Fatalf("mailbox advanced past gap: %v", got)
	}
	if len(session.confirmed) != 1 || session.confirmed[0] != "a1" {
		t.Fatalf("confirmed=%v", session.confirmed)
	}
}

func TestRuntimeTerminalReplaySkipsMailboxAndConfirmsSource(t *testing.T) {
	session := newRuntimeTestSession(nil, struct{ lane, id string }{"lane-a", "event-1"})
	entry := &cleanerEntry{
		delivery: session.deliveries[0], readyAt: time.Now(),
		event: domain.Event{
			BKTenantID: "tenant", EventSourceID: "source", EventID: "event-1", Fingerprint: "fp-1", Title: "event-1",
		},
	}
	mailbox := &runtimeTestMailbox{byLane: map[string][]string{}, current: "all"}
	runtime, err := NewRuntime(
		config.DefaultCleanerRuntimeConfig(), session, runtimeTestProcessor{}, runtimeTerminalReplayWriter{},
		mailbox, allowReceiveGate{}, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	result := runtime.runLaneBatch(context.Background(), "lane-a", []*cleanerEntry{entry})
	if result.err != nil || result.blocked != nil || result.settled != 1 {
		t.Fatalf("result=%+v", result)
	}
	if got := mailbox.byLane["all"]; len(got) != 0 {
		t.Fatalf("terminal replay was enqueued: %v", got)
	}
	if len(session.confirmed) != 1 || session.confirmed[0] != "event-1" {
		t.Fatalf("confirmed=%v", session.confirmed)
	}
}

func TestRuntimeUnprocessedReplayStillEnqueuesMailbox(t *testing.T) {
	session := newRuntimeTestSession(nil, struct{ lane, id string }{"lane-a", "event-1"})
	entry := &cleanerEntry{
		delivery: session.deliveries[0], readyAt: time.Now(),
		event: domain.Event{
			BKTenantID: "tenant", EventSourceID: "source", EventID: "event-1", Fingerprint: "fp-1", Title: "event-1",
		},
	}
	mailbox := &runtimeTestMailbox{byLane: map[string][]string{}, current: "all"}
	runtime, err := NewRuntime(
		config.DefaultCleanerRuntimeConfig(), session, runtimeTestProcessor{}, runtimeUnprocessedReplayWriter{},
		mailbox, allowReceiveGate{}, slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatal(err)
	}
	result := runtime.runLaneBatch(context.Background(), "lane-a", []*cleanerEntry{entry})
	if result.err != nil || result.blocked != nil || result.settled != 1 {
		t.Fatalf("result=%+v", result)
	}
	if got := mailbox.byLane["all"]; len(got) != 1 || got[0] != "event-1" {
		t.Fatalf("unprocessed replay mailbox=%v", got)
	}
}

func TestRuntimeFlushesLaneBatchByCountBytesOrWait(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*config.CleanerRuntimeConfig)
		bodies    []string
		readyAgo  time.Duration
		wantSize  int
	}{
		{name: "count", configure: func(cfg *config.CleanerRuntimeConfig) { cfg.MaxBatchMessages = 2 }, bodies: []string{"a", "b", "c"}, wantSize: 2},
		{name: "bytes", configure: func(cfg *config.CleanerRuntimeConfig) { cfg.MaxBatchBytes = 5 }, bodies: []string{"12345"}, wantSize: 1},
		{name: "wait", configure: func(cfg *config.CleanerRuntimeConfig) { cfg.BatchWaitMilliseconds = 5 }, bodies: []string{"a"}, readyAgo: 10 * time.Millisecond, wantSize: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := make([]struct{ lane, id string }, len(test.bodies))
			for index := range values {
				values[index] = struct{ lane, id string }{"lane-a", string(rune('a' + index))}
			}
			session := newRuntimeTestSession(nil, values...)
			now := time.Now()
			entries := make([]*cleanerEntry, len(values))
			for index, delivery := range session.deliveries {
				delivery.Message.Body = []byte(test.bodies[index])
				entries[index] = &cleanerEntry{
					delivery: delivery, readyAt: now.Add(-test.readyAgo),
					event: domain.Event{BKTenantID: "tenant", EventSourceID: "source", EventID: delivery.Message.ID,
						Fingerprint: delivery.Message.ID, Title: delivery.Message.ID},
				}
			}
			cfg := config.DefaultCleanerRuntimeConfig()
			test.configure(&cfg)
			sizes := make(chan int, 1)
			mailbox := &runtimeTestMailbox{byLane: map[string][]string{}, current: "all"}
			runtime, err := NewRuntime(cfg, session, runtimeTestProcessor{}, runtimeCountingWriter{sizes: sizes}, mailbox,
				allowReceiveGate{},
				slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			results := make(chan laneBatchResult, 1)
			slots := make(chan struct{}, 1)
			var batches sync.WaitGroup
			runtime.maybeStartBatch(context.Background(), &cleanerLane{name: "lane-a", entries: entries}, now, false, slots, results, &batches)
			select {
			case size := <-sizes:
				if size != test.wantSize {
					t.Fatalf("batch size=%d want=%d", size, test.wantSize)
				}
			case <-time.After(time.Second):
				t.Fatal("batch was not flushed")
			}
			result := <-results
			batches.Wait()
			if result.err != nil || result.settled != test.wantSize {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

var _ consume.Session = (*runtimeTestSession)(nil)

var _ consume.FlowController = (*runtimeTestSession)(nil)
