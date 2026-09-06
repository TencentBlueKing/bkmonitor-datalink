package access

import (
	"context"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"testing"
	"time"
)

func TestShortPeriodNormalAndAbsoluteRecoveryUseDifferentBudgets(t *testing.T) {
	for _, interval := range []int64{10, 15} {
		ref, frozen := frozenExecution(t)
		spec := execution.ScheduleSpec{EvaluationIntervalSeconds: interval, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 30}
		frozen.DuePlans[0].ScheduleSpec = spec
		eval := int64(ref.Slot.EvaluationTime) * 1000
		frozen.DuePlans[0].CompletionDeadlineUnixMilli = eval + 30000
		frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = eval + 30000
		ref = bindFrozenDueDigest(t, ref, frozen)
		prepared, err := Prepare(ref, frozen, 30*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if prepared.Queries[0].ReadyAtUnixMilli != eval+10000 || prepared.Queries[0].DeadlineUnixMilli != eval+25000 || prepared.Header.DeadlineUnixMilli != eval+30000 {
			t.Fatal("wrong frozen short-period times")
		}
		start := time.UnixMilli(eval + 50000)
		request := execution.QueryExecutionRequest{Contract: ref, Operation: execution.OperationReplay, AttemptNo: 1}
		deadline, err := deriveRecoveryQueryDeadline(request, frozen, start)
		if err != nil || deadline != start.Add(time.Duration(interval-5)*time.Second) {
			t.Fatalf("recovery took completion offset: %v %v", deadline, err)
		}
		var facts []observability.Observation
		provider := &fakeProvider{}
		permits := &recordingQueryPermits{}
		source, _ := NewSource(staticFrozenPlan{plan: frozen}, provider, permits, Config{MinReadyDelay: 30 * time.Second, Now: func() time.Time { return time.UnixMilli(eval) }, Observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) { facts = append(facts, o) })})
		_, err = source.Execute(context.Background(), execution.QueryExecutionRequest{Contract: ref, Operation: execution.OperationNormal, AttemptNo: 1}, &recordingConsumer{})
		ready, deferred := ReadinessDeferredAt(err)
		if !deferred || ready.UnixMilli() != eval+10000 || len(facts) != 1 || facts[0].QueryTiming.QueryDeadlineUnixMilli != eval+25000 {
			t.Fatalf("deferred facts=%+v err=%v", facts, err)
		}
	}
}

func TestShortPeriodRecoveryKeepsFiveOrTenSecondsAcrossQueriesAndPermits(t *testing.T) {
	for _, interval := range []int64{10, 15} {
		for _, operation := range []execution.Operation{execution.OperationReplay, execution.OperationRetry, execution.OperationProbe} {
			ref, frozen := frozenExecution(t)
			frozen.DuePlans[0].ScheduleSpec = execution.ScheduleSpec{EvaluationIntervalSeconds: interval, Timezone: "UTC", CompletionDeadlineOffsetSeconds: 30}
			frozen.DuePlans[0].CompletionDeadlineUnixMilli = int64(ref.Slot.EvaluationTime)*1000 + 30000
			frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = frozen.DuePlans[0].CompletionDeadlineUnixMilli
			second := frozen.Requirements[0]
			second.RequirementID, second.DatasetName = "secondary", "secondary"
			second.RelativeWindow.StartOffsetSeconds = -120
			frozen.Requirements = append(frozen.Requirements, second)
			ref = bindFrozenDueDigest(t, ref, frozen)
			started := time.UnixMilli(2_000_000_000_000)
			clock := started
			provider := &fakeProvider{}
			permits := &recordingQueryPermits{onAcquire: func() { clock = clock.Add(time.Second) }}
			source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, permits, Config{MinReadyDelay: time.Second, Now: func() time.Time { return clock }})
			if err != nil {
				t.Fatal(err)
			}
			source.wait = func(context.Context, time.Duration) error { return nil }
			_, err = source.Execute(context.Background(), execution.QueryExecutionRequest{Contract: ref, Operation: operation, AttemptNo: 2}, &recordingConsumer{})
			if err != nil {
				t.Fatal(err)
			}
			if len(provider.attempts) != 2 || len(permits.attempts) != 2 {
				t.Fatal("two-query recovery was not exercised")
			}
			want := started.Add(time.Duration(interval-5) * time.Second).UnixMilli()
			for i := range provider.attempts {
				if provider.attempts[i].DeadlineUnixMilli != want || permits.attempts[i].deadline.UnixMilli() != want {
					t.Fatal("short recovery deadline reset or inherited completion offset")
				}
			}
		}
	}
}
