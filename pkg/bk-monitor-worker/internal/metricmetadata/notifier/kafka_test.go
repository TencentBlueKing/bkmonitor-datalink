// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package notifier

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Shopify/sarama"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
)

type testSession struct {
	ctx    context.Context
	marked chan int64
}

func (s *testSession) Claims() map[string][]int32                      { return map[string][]int32{"metadata": {0}} }
func (s *testSession) MemberID() string                                { return "test" }
func (s *testSession) GenerationID() int32                             { return 1 }
func (s *testSession) MarkOffset(string, int32, int64, string)         {}
func (s *testSession) ResetOffset(string, int32, int64, string)        {}
func (s *testSession) Commit()                                         {}
func (s *testSession) MarkMessage(m *sarama.ConsumerMessage, _ string) { s.marked <- m.Offset }
func (s *testSession) Context() context.Context                        { return s.ctx }

type testClaim struct{ messages chan *sarama.ConsumerMessage }

func (c testClaim) Topic() string                            { return "metadata" }
func (c testClaim) Partition() int32                         { return 0 }
func (c testClaim) InitialOffset() int64                     { return 0 }
func (c testClaim) HighWaterMarkOffset() int64               { return 100 }
func (c testClaim) Messages() <-chan *sarama.ConsumerMessage { return c.messages }

type testPipeline struct {
	flushStarted chan struct{}
	allowCommit  chan struct{}
	failure      error
	once         sync.Once
	added        chan struct{}
}

func (p *testPipeline) Reset() {}
func (p *testPipeline) Add(context.Context, []byte) (int, error) {
	if p.added != nil {
		p.added <- struct{}{}
	}
	return 1, nil
}
func (p *testPipeline) Flush(ctx context.Context) error {
	if p.flushStarted != nil {
		p.once.Do(func() { close(p.flushStarted) })
	}
	if p.allowCommit != nil {
		select {
		case <-p.allowCommit:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return p.failure
}
func newTestHandler(t *testing.T, p Pipeline, o core.Options) (*handler, *testSession, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &testSession{ctx: ctx, marked: make(chan int64, 100)}
	h := &handler{pipeline: p, options: o, dataID: "42"}
	if err := h.Setup(s); err != nil {
		t.Fatal(err)
	}
	return h, s, cancel
}

func TestMessagesMarkedOnlyAfterCommit(t *testing.T) {
	o := core.DefaultOptions()
	o.BatchRecords = 2
	p := &testPipeline{flushStarted: make(chan struct{}), allowCommit: make(chan struct{})}
	h, s, cancel := newTestHandler(t, p, o)
	claim := testClaim{messages: make(chan *sarama.ConsumerMessage, 2)}
	claim.messages <- &sarama.ConsumerMessage{Topic: "metadata", Offset: 1, Value: []byte("one")}
	claim.messages <- &sarama.ConsumerMessage{Topic: "metadata", Offset: 2, Value: []byte("two")}
	close(claim.messages)
	claimDone := make(chan struct{})
	go func() { defer close(claimDone); _ = h.ConsumeClaim(s, claim) }()
	select {
	case <-p.flushStarted:
	case <-time.After(time.Second):
		t.Fatal("batch did not flush")
	}
	select {
	case offset := <-s.marked:
		t.Fatalf("offset %d marked before commit", offset)
	default:
	}
	close(p.allowCommit)
	for _, want := range []int64{1, 2} {
		select {
		case got := <-s.marked:
			if got != want {
				t.Fatalf("offset=%d want=%d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("committed message not marked")
		}
	}
	<-claimDone
	cancel()
	if err := h.Cleanup(s); err != nil {
		t.Fatal(err)
	}
}

func TestFailedBatchAndSuccessorsRemainUnmarked(t *testing.T) {
	o := core.DefaultOptions()
	o.BatchRecords = 1
	expected := errors.New("database commit failed")
	h, s, cancel := newTestHandler(t, &testPipeline{failure: expected}, o)
	claim := testClaim{messages: make(chan *sarama.ConsumerMessage, 3)}
	for i := int64(1); i <= 3; i++ {
		claim.messages <- &sarama.ConsumerMessage{Offset: i, Value: []byte("data")}
	}
	close(claim.messages)
	done := make(chan struct{})
	go func() { _ = h.ConsumeClaim(s, claim); close(done) }()
	select {
	case <-h.done:
	case <-time.After(time.Second):
		t.Fatal("failed processor did not exit")
	}
	<-done
	cancel()
	if err := h.Cleanup(s); !errors.Is(err, expected) {
		t.Fatalf("error=%v", err)
	}
	select {
	case offset := <-s.marked:
		t.Fatalf("failed message marked: %d", offset)
	default:
	}
}

func TestRebalanceDrainsPendingBatch(t *testing.T) {
	o := core.DefaultOptions()
	o.BatchWait = time.Hour
	p := &testPipeline{added: make(chan struct{}, 1)}
	h, s, cancel := newTestHandler(t, p, o)
	claim := testClaim{messages: make(chan *sarama.ConsumerMessage, 1)}
	claim.messages <- &sarama.ConsumerMessage{Offset: 7, Value: []byte("data")}
	done := make(chan struct{})
	go func() { _ = h.ConsumeClaim(s, claim); close(done) }()
	select {
	case <-p.added:
	case <-time.After(time.Second):
		t.Fatal("record not processed")
	}
	cancel()
	<-done
	cleanup := make(chan error, 1)
	go func() { cleanup <- h.Cleanup(s) }()
	select {
	case err := <-cleanup:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("rebalance drain blocked")
	}
	select {
	case offset := <-s.marked:
		if offset != 7 {
			t.Fatal(offset)
		}
	default:
		t.Fatal("drained batch not marked")
	}
}

func TestMessageQueueRejectsOversizedMessage(t *testing.T) {
	queue := newMessageQueue(1, 8)
	err := queue.Push(context.Background(), queuedMessage{queueSize: 9})
	if err == nil {
		t.Fatal("oversized message was accepted")
	}
}

func TestMessageQueueCanceledPushAndDrainAfterClose(t *testing.T) {
	queue := newMessageQueue(1, 16)
	first := queuedMessage{queueSize: 8}
	if err := queue.Push(context.Background(), first); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := queue.Push(ctx, queuedMessage{queueSize: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled push error = %v", err)
	}

	queue.Close()
	got, ok, err := queue.Receive(context.Background())
	if err != nil || !ok || got.queueSize != first.queueSize {
		t.Fatalf("receive queued message = (%+v, %t, %v)", got, ok, err)
	}
	if _, ok, err = queue.Receive(context.Background()); err != nil || ok {
		t.Fatalf("receive after drain = (ok=%t, err=%v), want closed and empty", ok, err)
	}
}

func TestQueueBudgetIncludesPendingUntilCommit(t *testing.T) {
	o := core.DefaultOptions()
	o.QueueMessages = 1
	o.BatchWait = time.Hour
	p := &testPipeline{flushStarted: make(chan struct{}), allowCommit: make(chan struct{})}
	h, s, cancel := newTestHandler(t, p, o)
	claim := testClaim{messages: make(chan *sarama.ConsumerMessage, 2)}
	claim.messages <- &sarama.ConsumerMessage{Offset: 1, Value: []byte("one")}
	claim.messages <- &sarama.ConsumerMessage{Offset: 2, Value: []byte("two")}
	close(claim.messages)
	done := make(chan struct{})
	go func() { defer close(done); _ = h.ConsumeClaim(s, claim) }()
	select {
	case <-p.flushStarted:
	case <-time.After(time.Second):
		t.Fatal("queue pressure did not trigger flush")
	}
	h.queue.mu.Lock()
	count, bytes := h.queue.count, h.queue.bytes
	h.queue.mu.Unlock()
	if count != 1 || bytes != queuedMessageSize(&sarama.ConsumerMessage{Value: []byte("one")}) {
		t.Fatalf("budget before commit: count=%d bytes=%d", count, bytes)
	}
	close(p.allowCommit)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second message remained blocked after commit")
	}
	cancel()
	if err := h.Cleanup(s); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{1, 2} {
		select {
		case got := <-s.marked:
			if got != want {
				t.Fatalf("offset=%d want=%d", got, want)
			}
		default:
			t.Fatalf("offset %d not marked", want)
		}
	}
}

func TestQueueByteBudgetRetainedAfterReceive(t *testing.T) {
	q := newMessageQueue(2, 10)
	first := queuedMessage{queueSize: 8}
	if err := q.Push(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := q.Receive(context.Background()); err != nil || !ok {
		t.Fatalf("receive: ok=%t err=%v", ok, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := q.Push(ctx, queuedMessage{queueSize: 3}); !errors.Is(err, context.Canceled) {
		t.Fatalf("push while budget retained: %v", err)
	}
	q.Release(first)
	if err := q.Push(context.Background(), queuedMessage{queueSize: 3}); err != nil {
		t.Fatal(err)
	}
}
