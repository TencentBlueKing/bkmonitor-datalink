package access

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestPrepareUsesBuiltInReadinessProfileForThirtySecondPlan(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	evaluationMillis := int64(contractRef.Slot.EvaluationTime) * 1000
	frozen.DuePlans[0].ScheduleSpec = execution.ScheduleSpec{
		EvaluationIntervalSeconds: 30,
		Timezone:                  "UTC",
	}
	frozen.DuePlans[0].CompletionDeadlineUnixMilli = evaluationMillis + int64((30*time.Second)/time.Millisecond)
	frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = frozen.DuePlans[0].CompletionDeadlineUnixMilli
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)

	prepared, err := Prepare(contractRef, frozen, 30*time.Second)
	if err != nil {
		t.Fatalf("Prepare(30s built-in profile) error=%v", err)
	}
	if len(prepared.Queries) != 1 {
		t.Fatalf("queries=%d, want 1", len(prepared.Queries))
	}
	wantReadyAt := evaluationMillis + int64((10*time.Second)/time.Millisecond)
	if prepared.Queries[0].ReadyAtUnixMilli != wantReadyAt {
		t.Fatalf("ready_at=%d, want Python-compatible built-in boundary %d", prepared.Queries[0].ReadyAtUnixMilli, wantReadyAt)
	}
}

func TestPrepareReadinessProfilePreservesLongIntervalAndRecoveryBudget(t *testing.T) {
	for _, test := range []struct {
		name      string
		interval  time.Duration
		recovery  bool
		wantDelay time.Duration
	}{
		{name: "normal-long-interval", interval: time.Minute, wantDelay: 30 * time.Second},
		{name: "recovery-thirty-second-interval", interval: 30 * time.Second, recovery: true, wantDelay: 30 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			contractRef, frozen := frozenExecution(t)
			evaluationMillis := int64(contractRef.Slot.EvaluationTime) * 1000
			frozen.DuePlans[0].ScheduleSpec = execution.ScheduleSpec{
				EvaluationIntervalSeconds: int64(test.interval / time.Second),
				Timezone:                  "UTC",
			}
			frozen.DuePlans[0].CompletionDeadlineUnixMilli = evaluationMillis + test.interval.Milliseconds()
			frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = frozen.DuePlans[0].CompletionDeadlineUnixMilli
			contractRef = bindFrozenDueDigest(t, contractRef, frozen)

			prepared, err := prepare(contractRef, frozen, 30*time.Second, test.recovery)
			if err != nil {
				t.Fatalf("prepare() error=%v", err)
			}
			if len(prepared.Queries) != 1 {
				t.Fatalf("queries=%d, want 1", len(prepared.Queries))
			}
			wantReadyAt := evaluationMillis + test.wantDelay.Milliseconds()
			if prepared.Queries[0].ReadyAtUnixMilli != wantReadyAt {
				t.Fatalf("ready_at=%d, want unchanged delay boundary %d", prepared.Queries[0].ReadyAtUnixMilli, wantReadyAt)
			}
			if len(prepared.Queries[0].ReadinessInvalidRequirements) != 0 {
				t.Fatalf("readiness-invalid requirements=%+v", prepared.Queries[0].ReadinessInvalidRequirements)
			}
		})
	}
}

func TestSourceReadinessBudgetInvalidIsPlanLocalForSubThirtySecondPlans(t *testing.T) {
	for _, interval := range []time.Duration{10 * time.Second, 15 * time.Second} {
		t.Run(interval.String(), func(t *testing.T) {
			contractRef, frozen, shortPlan := mixedReadinessExecution(t, interval)
			provider := &fakeProvider{}
			permits := &recordingQueryPermits{}
			source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, permits, Config{MinReadyDelay: 30 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			source.now = func() time.Time {
				return time.UnixMilli(int64(contractRef.Slot.EvaluationTime)*1000 + int64((30*time.Second)/time.Millisecond))
			}
			source.wait = func(context.Context, time.Duration) error {
				t.Fatal("already-ready healthy sibling must not sleep")
				return nil
			}
			consumer := &recordingConsumer{}
			completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
				Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
			}, consumer)
			if err != nil {
				t.Fatalf("Execute(mixed readiness) error=%v; invalid short Plan must not fail the QG", err)
			}
			if consumer.begin != 1 || len(consumer.batches) != 1 || len(provider.attempts) != 1 || len(permits.attempts) != 1 {
				t.Fatalf("begin=%d batches=%d provider=%d permits=%d, want healthy sibling 1/1/1/1",
					consumer.begin, len(consumer.batches), len(provider.attempts), len(permits.attempts))
			}
			if got := consumer.batches[0].Inputs[0].Consumer.Plan; got == shortPlan {
				t.Fatalf("invalid short Plan received streamed input: %+v", got)
			}
			if len(completion.PhysicalQueries) != 2 || len(completion.CompletionBindings) != 1 || !completion.AllRequiredCompleted {
				t.Fatalf("completion=%+v, want one healthy query plus one Plan-local unavailable query", completion)
			}
			binding := completion.CompletionBindings[0]
			if binding.Consumer.Plan != shortPlan || binding.ReasonCode != execution.ReasonCode(contract.ReasonReadinessBudgetInvalid) ||
				binding.Disposition != execution.AccessUnavailable || binding.ImpactScope != execution.ImpactPlan ||
				binding.Completeness != execution.CompletenessUnavailable || binding.DataState != execution.DataStateUnknown {
				t.Fatalf("short Plan disposition=%+v", binding)
			}
		})
	}
}

func TestSourceReadinessBudgetInvalidDoesNotRejectSharedPhysicalQuery(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	evaluationMillis := int64(contractRef.Slot.EvaluationTime) * 1000
	healthy := &frozen.DuePlans[0]
	healthy.ScheduleSpec = execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Timezone: "UTC"}
	healthy.CompletionDeadlineUnixMilli = evaluationMillis + int64((60*time.Second)/time.Millisecond)
	frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = healthy.CompletionDeadlineUnixMilli

	shortPlan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1002"}
	short := *healthy
	short.Identity = shortPlan
	short.CompiledPlan = compilePlanForStrategy(t, shortPlan.StrategyID)
	short.ScheduleSpec = execution.ScheduleSpec{EvaluationIntervalSeconds: 15, Timezone: "UTC"}
	short.ScheduleRevision = "short-plan-schedule-v1"
	short.CompletionDeadlineUnixMilli = evaluationMillis + int64((15*time.Second)/time.Millisecond)
	frozen.DuePlans = append(frozen.DuePlans, short)
	shortConsumer := frozen.Requirements[0].Consumers[0]
	shortConsumer.Consumer = execution.ConsumerRef{Plan: shortPlan}
	shortConsumer.ConsumerDeadlineUnixMilli = short.CompletionDeadlineUnixMilli
	frozen.Requirements[0].Consumers = append(frozen.Requirements[0].Consumers, shortConsumer)
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)

	provider := &fakeProvider{}
	permits := &recordingQueryPermits{}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, permits, Config{MinReadyDelay: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.UnixMilli(evaluationMillis + int64((10*time.Second)/time.Millisecond)) }
	source.wait = func(context.Context, time.Duration) error {
		t.Fatal("shared physical query is already ready")
		return nil
	}
	consumer := &recordingConsumer{}
	completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
	}, consumer)
	if err != nil {
		t.Fatalf("Execute(shared readiness) error=%v; invalid consumer must not reject its healthy sibling", err)
	}
	if len(provider.attempts) != 1 || len(permits.attempts) != 1 || len(consumer.batches) != 1 ||
		len(consumer.batches[0].Inputs) != 1 || consumer.batches[0].Inputs[0].Consumer.Plan != healthy.Identity {
		t.Fatalf("provider=%d permits=%d batches=%+v, want one shared query delivered only to healthy consumer",
			len(provider.attempts), len(permits.attempts), consumer.batches)
	}
	if len(completion.PhysicalQueries) != 1 || len(completion.CompletionBindings) != 1 || !completion.AllRequiredCompleted {
		t.Fatalf("completion=%+v, want shared physical completion plus one Plan-local disposition", completion)
	}
	binding := completion.CompletionBindings[0]
	if binding.Consumer.Plan != shortPlan || binding.Provenance.PhysicalQuery != completion.PhysicalQueries[0].PhysicalQuery ||
		binding.Disposition != execution.AccessUnavailable || binding.ReasonCode != execution.ReasonCode(contract.ReasonReadinessBudgetInvalid) ||
		binding.ImpactScope != execution.ImpactPlan || binding.Completeness != execution.CompletenessUnavailable ||
		binding.DataState != execution.DataStateUnknown {
		t.Fatalf("short consumer disposition=%+v, physical=%+v", binding, completion.PhysicalQueries[0])
	}
}

func TestSourceFutureReadinessReturnsWithoutWaitingInExecution(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	evaluationTime := time.UnixMilli(int64(contractRef.Slot.EvaluationTime) * 1000)
	frozen.DuePlans[0].CompletionDeadlineUnixMilli = evaluationTime.Add(time.Minute).UnixMilli()
	frozen.DuePlans[0].ScheduleSpec.EvaluationIntervalSeconds = 60
	frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = frozen.DuePlans[0].CompletionDeadlineUnixMilli
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)
	provider := &fakeProvider{}
	permits := &recordingQueryPermits{}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, permits, Config{MinReadyDelay: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return evaluationTime }
	waits := 0
	source.wait = func(context.Context, time.Duration) error {
		waits++
		return nil
	}
	consumer := &recordingConsumer{}
	_, err = source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
	}, consumer)
	var deferred interface{ ReadinessReadyAt() time.Time }
	if !errors.As(err, &deferred) {
		t.Fatalf("Execute(future readiness) error=%v, want typed readiness deferral", err)
	}
	wantReadyAt := evaluationTime.Add(30 * time.Second)
	if !deferred.ReadinessReadyAt().Equal(wantReadyAt) {
		t.Fatalf("readiness ready_at=%s, want %s", deferred.ReadinessReadyAt(), wantReadyAt)
	}
	if waits != 0 || consumer.begin != 0 || len(provider.attempts) != 0 || len(permits.attempts) != 0 {
		t.Fatalf("deferred execution crossed work boundary: waits=%d begin=%d provider=%d permits=%d",
			waits, consumer.begin, len(provider.attempts), len(permits.attempts))
	}
}

func TestSharedPendingReadinessDoesNotDelayEarlierQueryForLaterSibling(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	queries := []PlannedQuery{
		{Requirements: []execution.DataRequirement{{RequirementID: "first"}}, ReadyAtUnixMilli: now.Add(10 * time.Second).UnixMilli()},
		{Requirements: []execution.DataRequirement{{RequirementID: "second"}}, ReadyAtUnixMilli: now.Add(30 * time.Second).UnixMilli()},
	}
	if got := sharedPendingReadiness(queries, now); !got.IsZero() {
		t.Fatalf("sharedPendingReadiness()=%s, want no Slot-wide deferral across distinct consumer readiness", got)
	}
	queries[0].ReadyAtUnixMilli = queries[1].ReadyAtUnixMilli
	if got := sharedPendingReadiness(queries, now); !got.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("sharedPendingReadiness()=%s, want common boundary %s", got, now.Add(30*time.Second))
	}
	queries[0].ReadyAtUnixMilli = now.UnixMilli()
	if got := sharedPendingReadiness(queries, now); !got.IsZero() {
		t.Fatalf("sharedPendingReadiness()=%s, want ready sibling to preserve existing execution order", got)
	}
}

func TestSourceMixedReadinessPreservesPerQueryWaitInsteadOfSlotWideDeferral(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	evaluationTime := time.UnixMilli(int64(contractRef.Slot.EvaluationTime) * 1000)
	frozen.DuePlans[0].CompletionDeadlineUnixMilli = evaluationTime.Add(2 * time.Minute).UnixMilli()
	frozen.DuePlans[0].ScheduleSpec.EvaluationIntervalSeconds = 120
	frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = frozen.DuePlans[0].CompletionDeadlineUnixMilli

	primaryFacts := frozen.QueryFacts[frozen.Requirements[0].LogicalQueryRef]
	auxiliaryFacts, err := primaryFacts.WithMetricMerge("a + 1")
	if err != nil {
		t.Fatal(err)
	}
	auxiliaryRef := execution.LogicalQueryRef(auxiliaryFacts.QueryRevision)
	auxiliary := frozen.Requirements[0]
	auxiliary.RequirementID = "earlier-ready-history"
	auxiliary.DatasetName = "earlier_ready_history"
	auxiliary.Role = execution.InputRoleAlgorithmDependency
	auxiliary.LogicalQueryRef = auxiliaryRef
	auxiliary.RelativeWindow = execution.RelativeQueryWindow{StartOffsetSeconds: -120, EndOffsetSeconds: -20, HalfOpen: true}
	frozen.Requirements = append(frozen.Requirements, auxiliary)
	frozen.QueryFacts[auxiliaryRef] = auxiliaryFacts
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)

	provider := &fakeProvider{}
	permits := &recordingQueryPermits{}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, permits, Config{MinReadyDelay: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	clock := evaluationTime
	source.now = func() time.Time { return clock }
	var waited time.Duration
	source.wait = func(_ context.Context, delay time.Duration) error {
		waited += delay
		clock = clock.Add(delay)
		return nil
	}
	consumer := &recordingConsumer{}
	completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
	}, consumer)
	if err != nil {
		t.Fatalf("Execute(mixed readiness) error=%v, want existing per-query wait path", err)
	}
	if waited != 30*time.Second || consumer.begin != 1 || len(provider.attempts) != 2 ||
		len(permits.attempts) != 2 || len(completion.PhysicalQueries) != 2 {
		t.Fatalf("wait=%s begin=%d provider=%d permits=%d completion=%+v",
			waited, consumer.begin, len(provider.attempts), len(permits.attempts), completion)
	}
}

func mixedReadinessExecution(
	t *testing.T,
	shortInterval time.Duration,
) (execution.FrozenExecutionContractRef, FrozenPlan, execution.PlanIdentity) {
	t.Helper()
	contractRef, frozen := frozenExecution(t)
	evaluationMillis := int64(contractRef.Slot.EvaluationTime) * 1000

	healthy := &frozen.DuePlans[0]
	healthy.ScheduleSpec = execution.ScheduleSpec{EvaluationIntervalSeconds: 60, Timezone: "UTC"}
	healthy.CompletionDeadlineUnixMilli = evaluationMillis + int64((60*time.Second)/time.Millisecond)
	frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = healthy.CompletionDeadlineUnixMilli

	shortPlan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1002"}
	short := *healthy
	short.Identity = shortPlan
	short.CompiledPlan = compilePlanForStrategy(t, shortPlan.StrategyID)
	short.ScheduleSpec = execution.ScheduleSpec{EvaluationIntervalSeconds: int64(shortInterval / time.Second), Timezone: "UTC"}
	short.ScheduleRevision = "short-plan-schedule-v1"
	short.CompletionDeadlineUnixMilli = evaluationMillis + int64(shortInterval/time.Millisecond)
	frozen.DuePlans = append(frozen.DuePlans, short)

	shortRequirement := frozen.Requirements[0]
	shortRequirement.RequirementID = "short-primary"
	shortRequirement.DatasetName = "short-primary"
	shortRequirement.RelativeWindow = execution.RelativeQueryWindow{StartOffsetSeconds: -30, EndOffsetSeconds: 0, HalfOpen: true}
	shortRequirement.Consumers = []execution.DataRequirementConsumer{{
		Consumer:                           execution.ConsumerRef{Plan: shortPlan},
		ConsumerDeadlineUnixMilli:          short.CompletionDeadlineUnixMilli,
		DownstreamExecutionReserveMilliSec: 5_000,
	}}
	frozen.Requirements = append(frozen.Requirements, shortRequirement)
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)
	return contractRef, frozen, shortPlan
}
