// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

type fixedLease struct{ deadline time.Time }

func (lease fixedLease) Deadline() time.Time { return lease.deadline }

// decision-016 per-batch admission: an output batch is started only when
// the lease it runs under has at least one batch's bound plus the margin
// left. A Kafka write cannot be taken back once acknowledged, so the
// question is asked before the first byte; a batch held back is named as
// such -- neither an unknown acknowledgement nor a refusal of the content --
// and nothing reaches the producer.
func TestABatchIsNotStartedAgainstALeaseWithLessLifeThanItNeeds(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	needed := OutputAdmissionMargin + OutputBatchBound
	cases := []struct {
		name     string
		deadline time.Time
		admitted bool
	}{
		{name: "well inside the lease", deadline: now.Add(needed + 20*time.Second), admitted: true},
		{name: "exactly one batch and the margin left", deadline: now.Add(needed), admitted: true},
		{name: "one millisecond short", deadline: now.Add(needed - time.Millisecond), admitted: false},
		{name: "lease already gone", deadline: now.Add(-time.Second), admitted: false},
	}
	for _, test := range cases {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sent := 0
			producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
				sent++
				return 0, 0, nil
			}}
			sink, err := newTriggerEventSink("alarmd-trigger-event", producer, &fakeCloser{})
			if err != nil {
				t.Fatal(err)
			}
			sink.now = func() time.Time { return now }
			event := triggerEventGolden(t)
			event.WireFormat = contract.WireFormatStandardRawEvent
			ctx := execution.ContextWithLeaseAuthority(context.Background(), fixedLease{deadline: test.deadline})

			err = sink.WriteBatch(ctx, []contract.TriggerEventV1{event})
			if test.admitted {
				if err != nil || sent != 1 {
					t.Fatalf("WriteBatch() = %v, sent=%d, want the batch admitted and sent once", err, sent)
				}
				return
			}
			var deferred *OutputDeferredError
			if !errors.As(err, &deferred) || deferred.OutputDeferralReason() != contract.ReasonOutputLeaseExpiring {
				t.Fatalf("WriteBatch() = %v, want an OutputDeferredError named %s", err, contract.ReasonOutputLeaseExpiring)
			}
			if deferred.Needed != needed || deferred.Remaining != test.deadline.Sub(now) {
				t.Fatalf("deferral = %+v, want remaining %v against needed %v", deferred, test.deadline.Sub(now), needed)
			}
			var dependency interface{ RetryableOutputDependency() }
			if errors.As(err, &dependency) {
				t.Fatal("a deferral marked itself a retryable dependency; it would be read as an unknown acknowledgement")
			}
			if _, rejected := err.(interface{ OutputRejectionReason() string }); rejected {
				t.Fatal("a deferral marked itself a rejection; nothing about the content is wrong")
			}
			if sent != 0 {
				t.Fatalf("producer sent %d messages for a batch that was not admitted", sent)
			}
		})
	}
}

// A context that carries no lease authority admits as it always did: every
// path without a lease -- tools, tests, the sink opened alone -- keeps
// working, and the gate exists only where a Session does.
func TestABatchWithNoLeaseAuthorityIsAdmittedAsBefore(t *testing.T) {
	t.Parallel()

	sent := 0
	producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
		sent++
		return 0, 0, nil
	}}
	sink, err := newTriggerEventSink("alarmd-trigger-event", producer, &fakeCloser{})
	if err != nil {
		t.Fatal(err)
	}
	sink.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	event := triggerEventGolden(t)
	event.WireFormat = contract.WireFormatStandardRawEvent
	if err := sink.WriteBatch(context.Background(), []contract.TriggerEventV1{event}); err != nil || sent != 1 {
		t.Fatalf("WriteBatch() without a lease = %v, sent=%d, want admitted", err, sent)
	}
}

// The bound the admission uses is the bound the producer is built with.
func TestTheAdmissionBoundIsTheProducersOwnTimeout(t *testing.T) {
	t.Parallel()

	config, err := NewDecisionProducerOnlyConfig(validDecisionSinkConfig())
	if err != nil {
		t.Fatal(err)
	}
	if config.Producer.Timeout != OutputBatchBound {
		t.Fatalf("producer timeout = %v, admission bound = %v, want one number", config.Producer.Timeout, OutputBatchBound)
	}
}

// confirmingLease is a lease whose deadline a confirmation moves on, as one
// held under a worker session is (A2).
type confirmingLease struct {
	deadline  time.Time
	confirmed time.Time
	refuse    error
	calls     int
}

func (lease *confirmingLease) Deadline() time.Time { return lease.deadline }

func (lease *confirmingLease) Confirm(context.Context, time.Time) error {
	lease.calls++
	if lease.refuse != nil {
		return lease.refuse
	}
	lease.deadline = lease.confirmed
	return nil
}

// A batch that would outlast a lease held under a session confirms the lease
// once before it defers: confirmed, the batch goes out; refused, it defers
// as it would have, named the same way.
func TestABatchConfirmsALeaseHeldUnderASessionBeforeItDefers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	needed := OutputAdmissionMargin + OutputBatchBound
	for _, test := range []struct {
		name     string
		lease    *confirmingLease
		admitted bool
	}{
		{"the confirmation moves it on", &confirmingLease{deadline: now.Add(time.Second), confirmed: now.Add(needed + time.Second)}, true},
		{"the confirmation is refused", &confirmingLease{deadline: now.Add(time.Second), refuse: errors.New("stale")}, false},
		{"enough left asks for none", &confirmingLease{deadline: now.Add(needed + time.Second)}, true},
	} {
		sent := 0
		producer := &fakeSyncProducer{send: func(*sarama.ProducerMessage) (int32, int64, error) {
			sent++
			return 0, 0, nil
		}}
		sink, err := newTriggerEventSink("alarmd-trigger-event", producer, &fakeCloser{})
		if err != nil {
			t.Fatal(err)
		}
		sink.now = func() time.Time { return now }
		event := triggerEventGolden(t)
		event.WireFormat = contract.WireFormatStandardRawEvent
		err = sink.WriteBatch(execution.ContextWithLeaseAuthority(context.Background(), test.lease), []contract.TriggerEventV1{event})
		var deferred *OutputDeferredError
		switch {
		case test.admitted && (err != nil || sent != 1):
			t.Fatalf("%s: WriteBatch() = %v sent=%d, want the batch sent", test.name, err, sent)
		case !test.admitted && (!errors.As(err, &deferred) || sent != 0):
			t.Fatalf("%s: WriteBatch() = %v sent=%d, want it deferred", test.name, err, sent)
		}
		if wantCalls := map[bool]int{true: 0, false: 1}[test.name == "enough left asks for none"]; test.lease.calls != wantCalls {
			t.Fatalf("%s: %d confirmations, want %d", test.name, test.lease.calls, wantCalls)
		}
	}
}
