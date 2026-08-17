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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarm-engine/consumer"
)

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
		context.Background(),
		nil,
	)

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
		context.Background(),
		func(err error) { fatal <- err },
	)

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
		context.Background(),
		func(err error) { fatal <- err },
	)

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
		context.Background(),
		func(error) { fatalCount.Add(1) },
	)

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
	committer := &SaramaOffsetCommitter{coordinator: fakeCoordinator{broker: broker}, groupID: "alarm-engine-shadow"}
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
		context.Background(),
		nil,
	)

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
		context.Background(),
		func(error) { fatalCount.Add(1) },
	)
	var wait sync.WaitGroup
	wait.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wait.Done()
			session := newFakeSession(context.Background(), &[]string{})
			claim := newFakeClaim("trigger-input", int32(i), []*sarama.ConsumerMessage{{Topic: "trigger-input", Partition: int32(i), Offset: 1}})
			if err := handler.ConsumeClaim(session, claim); err == nil {
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
				context.Background(),
				func(err error) { fatal <- err },
			)

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
				context.Background(),
				nil,
			)

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

	stop, cancelStop := context.WithCancel(context.Background())
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
		stop,
		nil,
	)

	done := make(chan error, 1)
	go func() { done <- handler.ConsumeClaim(session, claim) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first record did not start")
	}
	cancelStop()
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

type fakeSession struct {
	ctx          context.Context
	events       *[]string
	markedOffset int64
	mu           sync.Mutex
}

func newFakeSession(ctx context.Context, events *[]string) *fakeSession {
	return &fakeSession{ctx: ctx, events: events}
}

func (s *fakeSession) Claims() map[string][]int32 { return nil }
func (s *fakeSession) MemberID() string           { return "member" }
func (s *fakeSession) GenerationID() int32        { return 1 }
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
func (c *fakeClaim) InitialOffset() int64                     { return 0 }
func (c *fakeClaim) HighWaterMarkOffset() int64               { return 0 }
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
