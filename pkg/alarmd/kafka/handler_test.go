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
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/consumer"
)

func TestConsumeClaimAdvancesLagCursorOnlyAfterCommit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		commit  error
		wantLag int64
	}{
		{name: "committed", wantLag: 5},
		{name: "commit failed", commit: errors.New("commit failed"), wantLag: 6},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			events := []string{}
			session := newFakeSession(context.Background(), &events)
			claim := newFakeClaim("trigger-input", 0, []*sarama.ConsumerMessage{{
				Topic: "trigger-input", Partition: 0, Offset: 4,
			}})
			claim.highWater = 10
			handler := NewHandler(noopProcessorFactory(), fakeSyncOffsetCommitter{events: &events, err: test.commit}, nil)
			setupHandler(t, handler, session, claim)

			err := handler.ConsumeClaim(session, claim)
			if test.commit == nil && err != nil {
				t.Fatalf("ConsumeClaim() error = %v", err)
			}
			if test.commit != nil && !errors.Is(err, test.commit) {
				t.Fatalf("ConsumeClaim() error = %v, want %v", err, test.commit)
			}
			snapshot := handler.assignment.Snapshot()
			if !snapshot.consumerLagKnown || snapshot.consumerLagRecords != test.wantLag || snapshot.inflightRecords != 0 {
				t.Fatalf("assignment snapshot = %+v, want known lag %d and no inflight", snapshot, test.wantLag)
			}
		})
	}
}

func TestConsumeClaimCommitsOffsetAfterProcessing(t *testing.T) {
	t.Parallel()

	events := make([]string, 0, 3)
	session := newFakeSession(context.Background(), &events)
	claim := newFakeClaim("trigger-input", 3, []*sarama.ConsumerMessage{{
		Topic: "trigger-input", Partition: 3, Offset: 41, Key: []byte("key"), Value: []byte("value"),
	}})
	handler := NewHandler(
		func() consumer.Processor {
			return consumer.ProcessorFunc(func(_ context.Context, key, value []byte) error {
				events = append(events, "process")
				if string(key) != "key" || string(value) != "value" {
					t.Fatalf("processor received key/value %q/%q", key, value)
				}
				return nil
			})
		},
		fakeSyncOffsetCommitter{events: &events},
		nil,
	)
	setupHandler(t, handler, session, claim)

	if err := handler.ConsumeClaim(session, claim); err != nil {
		t.Fatalf("ConsumeClaim() error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"process", "commit", "mark"}) {
		t.Fatalf("events = %v, want process/broker-commit/local-mark", events)
	}
	if session.markedOffset != 42 {
		t.Fatalf("marked offset = %d, want 42", session.markedOffset)
	}
}

func TestConsumeClaimDoesNotCommitProcessingFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("processing failed")
	events := make([]string, 0, 1)
	session := newFakeSession(context.Background(), &events)
	claim := newFakeClaim("trigger-input", 0, []*sarama.ConsumerMessage{{Topic: "trigger-input", Offset: 1}})
	fatal := make(chan error, 1)
	handler := NewHandler(
		func() consumer.Processor {
			return consumer.ProcessorFunc(func(context.Context, []byte, []byte) error {
				events = append(events, "process")
				return want
			})
		},
		fakeSyncOffsetCommitter{events: &events},
		func(err error) { fatal <- err },
	)
	setupHandler(t, handler, session, claim)

	if err := handler.ConsumeClaim(session, claim); !errors.Is(err, want) {
		t.Fatalf("ConsumeClaim() error = %v, want %v", err, want)
	}
	if !reflect.DeepEqual(events, []string{"process"}) {
		t.Fatalf("events = %v, want process only", events)
	}
	select {
	case err := <-fatal:
		if !errors.Is(err, want) {
			t.Fatalf("fatal error = %v, want %v", err, want)
		}
	default:
		t.Fatal("processing failure was not reported as fatal")
	}
}

func TestConsumeClaimStopsBeforeNextRecordWhenBrokerCommitFails(t *testing.T) {
	t.Parallel()

	want := errors.New("commit rejected")
	events := make([]string, 0, 1)
	session := newFakeSession(context.Background(), &events)
	claim := newFakeClaim("trigger-input", 0, []*sarama.ConsumerMessage{
		{Topic: "trigger-input", Offset: 1},
		{Topic: "trigger-input", Offset: 2},
	})
	processed := 0
	fatal := make(chan error, 1)
	handler := NewHandler(
		func() consumer.Processor {
			return consumer.ProcessorFunc(func(context.Context, []byte, []byte) error {
				processed++
				events = append(events, "process")
				return nil
			})
		},
		fakeSyncOffsetCommitter{events: &events, err: want},
		func(err error) { fatal <- err },
	)
	setupHandler(t, handler, session, claim)

	if err := handler.ConsumeClaim(session, claim); !errors.Is(err, want) {
		t.Fatalf("ConsumeClaim() error = %v, want %v", err, want)
	}
	if processed != 1 || !reflect.DeepEqual(events, []string{"process"}) {
		t.Fatalf("processed=%d events=%v, want one uncommitted record", processed, events)
	}
	select {
	case err := <-fatal:
		if !errors.Is(err, want) {
			t.Fatalf("fatal error = %v, want %v", err, want)
		}
	default:
		t.Fatal("broker commit failure was not reported as fatal")
	}
}

func TestRebalanceDuringOffsetCommitEndsClaimWithoutFatal(t *testing.T) {
	t.Parallel()

	sessionContext, cancelSession := context.WithCancel(context.Background())
	events := make([]string, 0, 1)
	var fatalCount atomic.Int32
	session := newFakeSession(sessionContext, &events)
	claim := newFakeClaim("trigger-input", 0, []*sarama.ConsumerMessage{{Topic: "trigger-input", Offset: 1}})
	handler := NewHandler(
		func() consumer.Processor {
			return consumer.ProcessorFunc(func(context.Context, []byte, []byte) error {
				events = append(events, "process")
				return nil
			})
		},
		cancelingOffsetCommitter{cancel: cancelSession},
		func(error) { fatalCount.Add(1) },
	)
	setupHandler(t, handler, session, claim)

	if err := handler.ConsumeClaim(session, claim); err != nil {
		t.Fatalf("ConsumeClaim() rebalance error = %v, want nil", err)
	}
	if !reflect.DeepEqual(events, []string{"process"}) || fatalCount.Load() != 0 {
		t.Fatalf("events=%v fatal=%d, want uncommitted normal revoke", events, fatalCount.Load())
	}
}

func TestRebalanceAfterBrokerAckKeepsLocalOffsetMark(t *testing.T) {
	t.Parallel()

	sessionContext, cancelSession := context.WithCancel(context.Background())
	events := make([]string, 0, 2)
	broker := &fakeOffsetBroker{onCommit: cancelSession}
	committer := &SaramaOffsetCommitter{coordinator: fakeCoordinator{broker: broker}, groupID: "alarmd-shadow"}
	session := newFakeSession(sessionContext, &events)
	claim := newFakeClaim("trigger-input", 3, []*sarama.ConsumerMessage{{Topic: "trigger-input", Partition: 3, Offset: 41}})
	handler := NewHandler(
		func() consumer.Processor {
			return consumer.ProcessorFunc(func(context.Context, []byte, []byte) error {
				events = append(events, "process")
				return nil
			})
		},
		committer,
		nil,
	)
	setupHandler(t, handler, session, claim)

	if err := handler.ConsumeClaim(session, claim); err != nil {
		t.Fatalf("ConsumeClaim() after successful ack = %v, want nil", err)
	}
	if !reflect.DeepEqual(events, []string{"process", "mark"}) || session.markedOffset != 42 {
		t.Fatalf("events=%v marked=%d, want process/mark at 42", events, session.markedOffset)
	}
}

func TestHandlerReportsOnlyFirstFatalAcrossClaims(t *testing.T) {
	t.Parallel()

	var entered atomic.Int32
	var fatalCount atomic.Int32
	release := make(chan struct{})
	handler := NewHandler(
		func() consumer.Processor {
			return consumer.ProcessorFunc(func(context.Context, []byte, []byte) error {
				entered.Add(1)
				<-release
				return errors.New("processing failed")
			})
		},
		fakeSyncOffsetCommitter{},
		func(error) { fatalCount.Add(1) },
	)
	session := newFakeSession(context.Background(), &[]string{})
	claims := []*fakeClaim{
		newFakeClaim("trigger-input", 0, []*sarama.ConsumerMessage{{Topic: "trigger-input", Partition: 0, Offset: 1}}),
		newFakeClaim("trigger-input", 1, []*sarama.ConsumerMessage{{Topic: "trigger-input", Partition: 1, Offset: 1}}),
	}
	setupHandler(t, handler, session, claims...)
	var wait sync.WaitGroup
	wait.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wait.Done()
			if err := handler.ConsumeClaim(session, claims[i]); err == nil {
				t.Error("ConsumeClaim() accepted processor failure")
			}
		}()
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for entered.Load() != 2 {
		select {
		case <-deadline.C:
			t.Fatal("both claims did not enter processor")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(release)
	wait.Wait()
	if fatalCount.Load() != 1 {
		t.Fatalf("fatal callback count = %d, want 1", fatalCount.Load())
	}
}

func TestInvalidClaimMessageIsFatalBeforeProcessing(t *testing.T) {
	t.Parallel()

	tests := map[string]*sarama.ConsumerMessage{
		"nil message":         nil,
		"coordinate mismatch": {Topic: "another-topic", Partition: 0, Offset: 1},
	}
	for name, message := range tests {
		name, message := name, message
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			events := make([]string, 0)
			fatal := make(chan error, 1)
			session := newFakeSession(context.Background(), &events)
			claim := newFakeClaim("trigger-input", 0, []*sarama.ConsumerMessage{message})
			handler := NewHandler(
				func() consumer.Processor {
					return consumer.ProcessorFunc(func(context.Context, []byte, []byte) error {
						events = append(events, "process")
						return nil
					})
				},
				fakeSyncOffsetCommitter{events: &events},
				func(err error) { fatal <- err },
			)
			setupHandler(t, handler, session, claim)

			if err := handler.ConsumeClaim(session, claim); err == nil {
				t.Fatal("ConsumeClaim() accepted invalid message")
			}
			if len(events) != 0 {
				t.Fatalf("events = %v, want no processing or commit", events)
			}
			select {
			case <-fatal:
			default:
				t.Fatal("invalid message was not reported as fatal")
			}
		})
	}
}

func TestConsumeClaimRejectsInvalidOffsetsBeforeMarking(t *testing.T) {
	t.Parallel()

	for _, offset := range []int64{-1, math.MaxInt64} {
		offset := offset
		t.Run("offset", func(t *testing.T) {
			t.Parallel()
			events := make([]string, 0, 1)
			session := newFakeSession(context.Background(), &events)
			claim := newFakeClaim("trigger-input", 0, []*sarama.ConsumerMessage{{Topic: "trigger-input", Offset: offset}})
			handler := NewHandler(
				func() consumer.Processor {
					return consumer.ProcessorFunc(func(context.Context, []byte, []byte) error {
						events = append(events, "process")
						return nil
					})
				},
				fakeSyncOffsetCommitter{events: &events},
				nil,
			)
			setupHandler(t, handler, session, claim)

			if err := handler.ConsumeClaim(session, claim); err == nil {
				t.Fatal("ConsumeClaim() accepted invalid offset")
			}
			if len(events) != 0 {
				t.Fatalf("events = %v, want no processing side effect", events)
			}
		})
	}
}

func TestStopDrainsCurrentRecordWithoutReadingNext(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	events := make([]string, 0, 3)
	session := newFakeSession(context.Background(), &events)
	claim := newFakeClaim("trigger-input", 0, []*sarama.ConsumerMessage{
		{Topic: "trigger-input", Offset: 1},
		{Topic: "trigger-input", Offset: 2},
	})
	processed := 0
	handler := NewHandler(
		func() consumer.Processor {
			return consumer.ProcessorFunc(func(context.Context, []byte, []byte) error {
				processed++
				events = append(events, "process")
				close(started)
				<-release
				return nil
			})
		},
		fakeSyncOffsetCommitter{events: &events},
		nil,
	)
	setupHandler(t, handler, session, claim)

	done := make(chan error, 1)
	go func() { done <- handler.ConsumeClaim(session, claim) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first record did not start")
	}
	handler.BeginDrain()
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ConsumeClaim() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ConsumeClaim() did not finish draining")
	}
	if processed != 1 || !reflect.DeepEqual(events, []string{"process", "commit", "mark"}) {
		t.Fatalf("processed=%d events=%v, want one committed record", processed, events)
	}
}

func TestStopDoesNotLetIdleClaimCancelInflightCommit(t *testing.T) {
	t.Parallel()

	sessionContext, cancelSession := context.WithCancel(context.Background())
	defer cancelSession()
	events := make([]string, 0, 3)
	session := newFakeSession(sessionContext, &events)
	session.claims = map[string][]int32{"trigger-input": {0, 1}}
	busyMessages := make(chan *sarama.ConsumerMessage, 1)
	idleMessages := make(chan *sarama.ConsumerMessage)
	busyClaim := &fakeClaim{topic: "trigger-input", partition: 0, messages: busyMessages}
	idleClaim := &fakeClaim{topic: "trigger-input", partition: 1, messages: idleMessages}
	started := make(chan struct{})
	release := make(chan struct{})
	handler := NewHandler(
		func() consumer.Processor {
			return consumer.ProcessorFunc(func(context.Context, []byte, []byte) error {
				events = append(events, "process")
				close(started)
				<-release
				return nil
			})
		},
		fakeSyncOffsetCommitter{events: &events},
		nil,
	)
	if err := handler.Setup(session); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	errorsByClaim := make(chan error, 2)
	runClaim := func(claim *fakeClaim) {
		err := handler.ConsumeClaim(session, claim)
		cancelSession() // Sarama cancels the whole session when any claim returns.
		errorsByClaim <- err
	}
	go runClaim(busyClaim)
	go runClaim(idleClaim)
	deadline := time.Now().Add(time.Second)
	for !handler.Ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !handler.Ready() {
		t.Fatal("handler did not become ready after both claims initialized")
	}
	busyMessages <- &sarama.ConsumerMessage{Topic: "trigger-input", Partition: 0, Offset: 1}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("busy claim did not start processing")
	}

	handler.BeginDrain()
	select {
	case err := <-errorsByClaim:
		t.Fatalf("a claim returned before the in-flight record completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	for range 2 {
		select {
		case err := <-errorsByClaim:
			if err != nil {
				t.Fatalf("ConsumeClaim() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("claims did not finish after drain")
		}
	}
	if !reflect.DeepEqual(events, []string{"process", "commit", "mark"}) {
		t.Fatalf("events=%v, want in-flight process/commit/mark before session cancellation", events)
	}
}

func setupHandler(t *testing.T, handler *Handler, session *fakeSession, claims ...*fakeClaim) {
	t.Helper()
	session.claims = make(map[string][]int32)
	for _, claim := range claims {
		session.claims[claim.topic] = append(session.claims[claim.topic], claim.partition)
	}
	if err := handler.Setup(session); err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
}

type fakeSession struct {
	ctx          context.Context
	events       *[]string
	markedOffset int64
	claims       map[string][]int32
	member       string
	generation   int32
	mu           sync.Mutex
}

func newFakeSession(ctx context.Context, events *[]string) *fakeSession {
	return &fakeSession{ctx: ctx, events: events}
}

func (s *fakeSession) Claims() map[string][]int32 { return s.claims }
func (s *fakeSession) MemberID() string {
	if s.member == "" {
		return "member"
	}
	return s.member
}
func (s *fakeSession) GenerationID() int32 {
	if s.generation == 0 {
		return 1
	}
	return s.generation
}
func (s *fakeSession) MarkOffset(_ string, _ int32, offset int64, _ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.markedOffset = offset
	*s.events = append(*s.events, "mark")
}
func (s *fakeSession) Commit() {
	panic("session Commit must not be used because it cannot return broker errors")
}
func (s *fakeSession) ResetOffset(string, int32, int64, string)    {}
func (s *fakeSession) MarkMessage(*sarama.ConsumerMessage, string) {}
func (s *fakeSession) Context() context.Context                    { return s.ctx }

type fakeClaim struct {
	topic     string
	partition int32
	messages  chan *sarama.ConsumerMessage
	initial   int64
	highWater int64
}

func newFakeClaim(topic string, partition int32, records []*sarama.ConsumerMessage) *fakeClaim {
	messages := make(chan *sarama.ConsumerMessage, len(records))
	for _, record := range records {
		messages <- record
	}
	close(messages)
	return &fakeClaim{topic: topic, partition: partition, messages: messages}
}

func (c *fakeClaim) Topic() string                            { return c.topic }
func (c *fakeClaim) Partition() int32                         { return c.partition }
func (c *fakeClaim) InitialOffset() int64                     { return c.initial }
func (c *fakeClaim) HighWaterMarkOffset() int64               { return c.highWater }
func (c *fakeClaim) Messages() <-chan *sarama.ConsumerMessage { return c.messages }

type fakeSyncOffsetCommitter struct {
	events *[]string
	err    error
}

func (c fakeSyncOffsetCommitter) CommitOffset(context.Context, sarama.ConsumerGroupSession, consumer.Record) error {
	if c.err != nil {
		return c.err
	}
	*c.events = append(*c.events, "commit")
	return nil
}

type cancelingOffsetCommitter struct {
	cancel context.CancelFunc
}

func (c cancelingOffsetCommitter) CommitOffset(ctx context.Context, _ sarama.ConsumerGroupSession, _ consumer.Record) error {
	c.cancel()
	return ctx.Err()
}
