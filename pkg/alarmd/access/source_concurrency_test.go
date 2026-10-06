package access

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func independentQueryExecution(t *testing.T) (execution.FrozenExecutionContractRef, FrozenPlan) {
	t.Helper()
	ref, frozen := frozenExecution(t)
	second := frozen.Requirements[0]
	facts, err := frozen.QueryFacts[second.LogicalQueryRef].WithMetricMerge("a + 1")
	if err != nil {
		t.Fatal(err)
	}
	second.LogicalQueryRef = execution.LogicalQueryRef(facts.QueryRevision)
	second.RequirementID = "independent-history"
	second.DatasetName = "independent-history"
	second.Role = execution.InputRoleAlgorithmDependency
	frozen.Requirements = append(frozen.Requirements, second)
	frozen.QueryFacts[second.LogicalQueryRef] = facts
	return bindFrozenDueDigest(t, ref, frozen), frozen
}

type concurrentProviderFunc func(context.Context, execution.QueryAttempt, execution.ProviderSeriesSink) (execution.ProviderCompletion, error)

func (f concurrentProviderFunc) Execute(ctx context.Context, attempt execution.QueryAttempt, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	return f(ctx, attempt, sink)
}

type synchronizedQueryPermits struct {
	mu sync.Mutex
	recordingQueryPermits
}

func (p *synchronizedQueryPermits) AcquireQueryPermit(ctx context.Context, slot execution.SlotIdentity, op execution.Operation, deadline time.Time) (QueryPermit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	permit, err := p.recordingQueryPermits.AcquireQueryPermit(ctx, slot, op, deadline)
	if err != nil {
		return nil, err
	}
	return &recordingQueryPermit{recovery: permit.RecoveryPermit(), release: func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		permit.Release()
	}}, nil
}

func TestSourceOverlapsIndependentReadyQueriesAndJoinsCancellation(t *testing.T) {
	ref, frozen := independentQueryExecution(t)
	started := make(chan execution.PhysicalQueryDigest, 2)
	finished := make(chan struct{}, 2)
	provider := concurrentProviderFunc(func(ctx context.Context, attempt execution.QueryAttempt, _ execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
		started <- attempt.Spec.Digest
		<-ctx.Done()
		finished <- struct{}{}
		return execution.ProviderCompletion{}, ctx.Err()
	})
	permits := &synchronizedQueryPermits{}
	source, err := NewSource(staticFrozenPlan{frozen}, provider, permits, Config{MinReadyDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.Unix(int64(ref.Slot.EvaluationTime)+2, 0) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := source.Execute(ctx, execution.QueryExecutionRequest{Contract: ref, Operation: execution.OperationNormal, AttemptNo: 1}, &recordingConsumer{})
		done <- err
	}()
	seen := make(map[execution.PhysicalQueryDigest]bool)
	for len(seen) < 2 {
		select {
		case digest := <-started:
			if seen[digest] {
				t.Fatal("one physical query started twice")
			}
			seen[digest] = true
		case <-time.After(time.Second):
			cancel()
			<-done
			t.Fatal("ready sibling did not start while independent query was blocked")
		}
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled execution succeeded")
	}
	if len(finished) != 2 || permits.releases != 2 {
		t.Fatalf("Execute returned before join/release: finished=%d releases=%d", len(finished), permits.releases)
	}
}

func TestSourceStartsReadySiblingBeforeEarlierDeadlineFutureQuery(t *testing.T) {
	ref, frozen := independentQueryExecution(t)
	evaluationTime := time.Unix(int64(ref.Slot.EvaluationTime), 0)
	frozen.DuePlans[0].ScheduleSpec.EvaluationIntervalSeconds = 120
	frozen.DuePlans[0].CompletionDeadlineUnixMilli = evaluationTime.Add(120 * time.Second).UnixMilli()
	for index := range frozen.Requirements {
		r := &frozen.Requirements[index]
		r.Consumers = append([]execution.DataRequirementConsumer(nil), r.Consumers...)
		r.Consumers[0].ConsumerDeadlineUnixMilli = frozen.DuePlans[0].CompletionDeadlineUnixMilli
	}
	frozen.Requirements[0].Consumers[0].DownstreamExecutionReserveMilliSec = 80_000
	frozen.Requirements[1].RelativeWindow = execution.RelativeQueryWindow{StartOffsetSeconds: -120, EndOffsetSeconds: -20, HalfOpen: true}
	ref = bindFrozenDueDigest(t, ref, frozen)
	prepared, err := Prepare(ref, frozen, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Queries[0].ReadyAtUnixMilli <= prepared.Queries[1].ReadyAtUnixMilli {
		t.Fatal("fixture must put the late-ready query first by deadline")
	}
	readyStarted := make(chan struct{})
	provider := concurrentProviderFunc(func(ctx context.Context, attempt execution.QueryAttempt, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
		if attempt.Spec.Digest == prepared.Queries[1].Spec.Digest {
			close(readyStarted)
		}
		return (&fakeProvider{}).Execute(ctx, attempt, sink)
	})
	source, err := NewSource(staticFrozenPlan{frozen}, provider, &synchronizedQueryPermits{}, Config{MinReadyDelay: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	clock := evaluationTime.Add(20 * time.Second)
	source.now = func() time.Time { return clock }
	source.wait = func(ctx context.Context, delay time.Duration) error {
		select {
		case <-readyStarted:
			clock = clock.Add(delay)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
			return errors.New("waited for late readiness before starting ready sibling")
		}
	}
	completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: ref, Operation: execution.OperationNormal, AttemptNo: 1,
	}, &recordingConsumer{})
	if err != nil || len(completion.PhysicalQueries) != 2 {
		t.Fatalf("ready-first execution: completion=%+v error=%v", completion, err)
	}
}
