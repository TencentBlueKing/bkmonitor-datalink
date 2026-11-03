// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package access

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// deadlineReadingProvider reads the execution's deadline as each physical
// query runs: what a watchdog looking at the execution then would judge it by.
type deadlineReadingProvider struct {
	*fakeProvider
	marker *execution.StageMarker
	seen   []time.Time
	// read, when set, is sent on after each reading.
	read chan struct{}
}

func (provider *deadlineReadingProvider) Execute(ctx context.Context, attempt execution.QueryAttempt, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	provider.mu.Lock()
	provider.seen = append(provider.seen, provider.marker.Deadline())
	provider.mu.Unlock()
	if provider.read != nil {
		provider.read <- struct{}{}
	}
	return provider.fakeProvider.Execute(ctx, attempt, sink)
}

// A replay, retry or probe runs under the deadline access derives from its
// own arrival, and that is the deadline the execution carries while its query
// runs - not its frozen Slot's, which for a replay of a taken-over Slot is
// minutes gone. The clock here is decades past the frozen Slot.
func TestARecoveryCarriesTheDeadlineDerivedFromItsArrival(t *testing.T) {
	for _, operation := range []execution.Operation{execution.OperationReplay, execution.OperationRetry, execution.OperationProbe} {
		t.Run(string(operation), func(t *testing.T) {
			contractRef, frozen := frozenExecution(t)
			var marker execution.StageMarker
			marker.Begin(execution.SlotStageExecute)
			provider := &deadlineReadingProvider{fakeProvider: &fakeProvider{}, marker: &marker}
			source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, &recordingQueryPermits{}, Config{MinReadyDelay: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			arrival := time.UnixMilli(2_000_000_000_000)
			source.now = func() time.Time { return arrival }
			source.wait = func(context.Context, time.Duration) error { return nil }
			ctx := execution.WithStageMarker(context.Background(), &marker)
			if _, err := source.Execute(ctx, execution.QueryExecutionRequest{Contract: contractRef, Operation: operation, AttemptNo: 2},
				&recordingConsumer{}); err != nil {
				t.Fatal(err)
			}
			consumer := frozen.Requirements[0].Consumers[0]
			interval := consumer.ConsumerDeadlineUnixMilli - int64(contractRef.Slot.EvaluationTime)*1000
			want := arrival.Add(time.Duration(interval-consumer.DownstreamExecutionReserveMilliSec) * time.Millisecond)
			if len(provider.seen) != 1 || !provider.seen[0].Equal(want) || provider.attempts[0].DeadlineUnixMilli != want.UnixMilli() {
				t.Fatalf("deadline while the query ran = %v (attempt %d), want the recovery deadline %v",
					provider.seen, provider.attempts[0].DeadlineUnixMilli, want)
			}
		})
	}
}

// A normal execution's query raises the execution's deadline to that query's
// own. A second query that starts after the first one's deadline has gone is
// judged from its own start, not by the passed deadline: here the permit wait
// before the second query takes five minutes.
func TestEachPhysicalQueryCarriesItsOwnDeadline(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	second := frozen.Requirements[0]
	second.RequirementID = "secondary"
	second.DatasetName = "secondary"
	second.RelativeWindow.StartOffsetSeconds = -120
	frozen.Requirements = append(frozen.Requirements, second)
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)
	var marker execution.StageMarker
	marker.Begin(execution.SlotStageExecute)
	provider := &deadlineReadingProvider{fakeProvider: &fakeProvider{}, marker: &marker, read: make(chan struct{}, 2)}
	clock := time.UnixMilli(int64(contractRef.Slot.EvaluationTime)*1000 + 10_000)
	var startedSecond time.Time
	acquired := 0
	permits := &recordingQueryPermits{onAcquire: func() {
		if acquired++; acquired == 2 {
			// The queries run side by side; the first has read its deadline
			// before the second starts, as it would have long before a
			// five-minute wait was over.
			<-provider.read
			clock = clock.Add(5 * time.Minute)
			startedSecond = clock
		}
	}}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, permits, Config{MinReadyDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return clock }
	source.wait = func(context.Context, time.Duration) error { return nil }
	ctx := execution.WithStageMarker(context.Background(), &marker)
	if _, err := source.Execute(ctx, execution.QueryExecutionRequest{Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1},
		&recordingConsumer{}); err != nil {
		t.Fatal(err)
	}
	if len(provider.seen) != 2 || len(provider.attempts) != 2 {
		t.Fatalf("queries %d, want two", len(provider.seen))
	}
	first := time.UnixMilli(provider.attempts[0].DeadlineUnixMilli)
	if !provider.seen[0].Equal(first) {
		t.Fatalf("deadline during the first query = %v, want its own %v", provider.seen[0], first)
	}
	// The second query started once its permit came, five minutes after the
	// first one, with its own deadline already gone.
	if !time.UnixMilli(provider.attempts[1].DeadlineUnixMilli).Before(startedSecond) {
		t.Fatalf("setup: the second query's deadline %d is not past at its start %v", provider.attempts[1].DeadlineUnixMilli, startedSecond)
	}
	if !provider.seen[1].Equal(startedSecond) {
		t.Fatalf("deadline during the second query = %v, want counted from its start %v", provider.seen[1], startedSecond)
	}
}

// channelReadingPermits reads the execution's deadline while it waits for
// recovery channels, before any of its queries has started.
type channelReadingPermits struct {
	*recordingQueryPermits
	marker *execution.StageMarker
	seen   time.Time
}

func (permits *channelReadingPermits) AcquireRecoveryChannels(
	ctx context.Context, slot execution.SlotIdentity, operation execution.Operation, deadline time.Time, maximum int, beforeWait func(),
) (RecoveryChannels, error) {
	permits.seen = permits.marker.Deadline()
	return permits.recordingQueryPermits.AcquireRecoveryChannels(ctx, slot, operation, deadline, maximum, beforeWait)
}

// The wait for recovery channels is bounded by the recovery deadline, and
// the execution carries that deadline from the moment access derives it: a
// replay waiting its turn is judged by its own deadline, not left unjudged
// until its first query and not judged by its frozen Slot's.
func TestARecoveryWaitingForChannelsAlreadyCarriesItsDeadline(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	var marker execution.StageMarker
	marker.Begin(execution.SlotStageExecute)
	permits := &channelReadingPermits{recordingQueryPermits: &recordingQueryPermits{}, marker: &marker}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, &fakeProvider{}, permits, Config{MinReadyDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	arrival := time.UnixMilli(2_000_000_000_000)
	source.now = func() time.Time { return arrival }
	source.wait = func(context.Context, time.Duration) error { return nil }
	if _, err := source.Execute(execution.WithStageMarker(context.Background(), &marker),
		execution.QueryExecutionRequest{Contract: contractRef, Operation: execution.OperationReplay, AttemptNo: 2}, &recordingConsumer{}); err != nil {
		t.Fatal(err)
	}
	consumer := frozen.Requirements[0].Consumers[0]
	interval := consumer.ConsumerDeadlineUnixMilli - int64(contractRef.Slot.EvaluationTime)*1000
	want := arrival.Add(time.Duration(interval-consumer.DownstreamExecutionReserveMilliSec) * time.Millisecond)
	if !permits.seen.Equal(want) {
		t.Fatalf("deadline while waiting for recovery channels = %v, want the recovery deadline %v", permits.seen, want)
	}
}
