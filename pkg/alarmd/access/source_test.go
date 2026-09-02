package access

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestSourceStreamsFullQueryAndConservesCompletion(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	provider := &fakeProvider{}
	permits := &recordingQueryPermits{}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, permits, Config{MinReadyDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.UnixMilli(2_000_000_000_000) }
	source.wait = func(context.Context, time.Duration) error { return nil }
	consumer := &recordingConsumer{}
	completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
	}, consumer)
	if err != nil {
		t.Fatal(err)
	}
	if consumer.begin != 1 || len(consumer.batches) != 1 || len(completion.PhysicalQueries) != 1 || !completion.AllRequiredCompleted {
		t.Fatalf("begin=%d batches=%d completion=%+v", consumer.begin, len(consumer.batches), completion)
	}
	if completion.PhysicalQueries[0].Delivery != consumer.batches[0].Delivery {
		t.Fatalf("completion delivery=%+v batch=%+v", completion.PhysicalQueries[0].Delivery, consumer.batches[0].Delivery)
	}
	if err := completion.Validate(consumer.header, []execution.SeriesDelivery{consumer.batches[0].Delivery}); err != nil {
		t.Fatalf("completion conservation: %v", err)
	}
	if len(permits.attempts) != 1 || permits.releases != 1 || permits.attempts[0].operation != execution.OperationNormal {
		t.Fatalf("query permits=%+v releases=%d", permits.attempts, permits.releases)
	}
	wantDeadline := frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli -
		frozen.Requirements[0].Consumers[0].DownstreamExecutionReserveMilliSec
	if permits.attempts[0].deadline.UnixMilli() != wantDeadline || provider.attempts[0].DeadlineUnixMilli != wantDeadline {
		t.Fatalf("LIVE permit/provider deadlines=%d/%d, want frozen query deadline %d",
			permits.attempts[0].deadline.UnixMilli(), provider.attempts[0].DeadlineUnixMilli, wantDeadline)
	}
}

func TestSourceProjectsPartialAndUnavailableCompletions(t *testing.T) {
	tests := []struct {
		name         string
		provider     *fakeProvider
		completeness execution.Completeness
		dataState    execution.DataState
		disposition  execution.AccessDisposition
		reason       execution.ReasonCode
		batches      int
		bindings     int
	}{
		{
			name: "partial data", provider: &fakeProvider{completeness: execution.CompletenessPartial},
			completeness: execution.CompletenessPartial, dataState: execution.DataStateData,
			batches: 1,
		},
		{
			name: "partial empty", provider: &fakeProvider{completeness: execution.CompletenessPartial, empty: true},
			completeness: execution.CompletenessPartial, dataState: execution.DataStateEmpty,
			disposition: execution.AccessDegraded, reason: execution.ReasonCode(contract.ReasonQueryPartial), bindings: 1,
		},
		{
			name: "unavailable", provider: &fakeProvider{completeness: execution.CompletenessUnavailable, empty: true, reason: execution.ReasonCode(contract.ReasonQueryTimeout)},
			completeness: execution.CompletenessUnavailable, dataState: execution.DataStateUnknown,
			disposition: execution.AccessUnavailable, reason: execution.ReasonCode(contract.ReasonQueryTimeout), bindings: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contractRef, frozen := frozenExecution(t)
			source, err := NewSource(staticFrozenPlan{plan: frozen}, test.provider, &recordingQueryPermits{}, Config{MinReadyDelay: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			source.now = func() time.Time { return time.UnixMilli(2_000_000_000_000) }
			source.wait = func(context.Context, time.Duration) error { return nil }
			consumer := &recordingConsumer{}
			completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
				Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
			}, consumer)
			if err != nil {
				t.Fatal(err)
			}
			if len(completion.PhysicalQueries) != 1 || completion.PhysicalQueries[0].Completeness != test.completeness ||
				completion.PhysicalQueries[0].DataState != test.dataState || len(consumer.batches) != test.batches ||
				len(completion.CompletionBindings) != test.bindings {
				t.Fatalf("completion=%+v batches=%d", completion, len(consumer.batches))
			}
			if test.bindings == 1 {
				binding := completion.CompletionBindings[0]
				if binding.Completeness != test.completeness || binding.DataState != test.dataState ||
					binding.Disposition != test.disposition || binding.ReasonCode != test.reason {
					t.Fatalf("binding=%+v", binding)
				}
			}
		})
	}
}

func TestSourceGivesReplayRetryAndProbeOneFreshFrozenRecoveryBudget(t *testing.T) {
	for _, operation := range []execution.Operation{
		execution.OperationReplay, execution.OperationRetry, execution.OperationProbe,
	} {
		t.Run(string(operation), func(t *testing.T) {
			contractRef, frozen := frozenExecution(t)
			provider := &fakeProvider{}
			permits := &recordingQueryPermits{}
			source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, permits, Config{MinReadyDelay: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			startedAt := time.UnixMilli(2_000_000_000_000)
			source.now = func() time.Time { return startedAt }
			source.wait = func(context.Context, time.Duration) error { return nil }
			request := execution.QueryExecutionRequest{Contract: contractRef, Operation: operation, AttemptNo: 3}
			if _, err := source.Execute(context.Background(), request, &recordingConsumer{}); err != nil {
				t.Fatal(err)
			}
			if len(provider.attempts) != 1 {
				t.Fatalf("provider attempts=%d, want 1", len(provider.attempts))
			}
			attempt := provider.attempts[0]
			if attempt.Operation != request.Operation || attempt.AttemptNo != request.AttemptNo ||
				attempt.RecoveryPermit == nil || attempt.RecoveryPermit.Operation != request.Operation ||
				attempt.RecoveryPermit.Slot != request.Contract.Slot {
				t.Fatalf("provider attempt=%+v", attempt)
			}
			consumer := frozen.Requirements[0].Consumers[0]
			intervalMillis := consumer.ConsumerDeadlineUnixMilli - int64(contractRef.Slot.EvaluationTime)*1000
			wantDeadline := startedAt.Add(time.Duration(intervalMillis-consumer.DownstreamExecutionReserveMilliSec) * time.Millisecond).UnixMilli()
			if len(permits.attempts) != 1 || permits.releases != 1 ||
				permits.attempts[0].deadline.UnixMilli() != wantDeadline || attempt.DeadlineUnixMilli != wantDeadline {
				t.Fatalf("permit/provider deadlines=%+v/%d releases=%d, want shared recovery deadline %d",
					permits.attempts, attempt.DeadlineUnixMilli, permits.releases, wantDeadline)
			}
		})
	}
}

func TestSourceRecoveryBudgetIsNotResetAcrossPermitWaitsOrPhysicalQueries(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	second := frozen.Requirements[0]
	second.RequirementID = "secondary"
	second.DatasetName = "secondary"
	second.RelativeWindow.StartOffsetSeconds = -120
	frozen.Requirements = append(frozen.Requirements, second)
	contractRef = bindFrozenDueDigest(t, contractRef, frozen)
	provider := &fakeProvider{}
	clock := time.UnixMilli(2_000_000_000_000)
	permits := &recordingQueryPermits{onAcquire: func() { clock = clock.Add(5 * time.Second) }}
	source, err := NewSource(staticFrozenPlan{plan: frozen}, provider, permits, Config{MinReadyDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	startedAt := clock
	source.now = func() time.Time { return clock }
	source.wait = func(context.Context, time.Duration) error { return nil }
	if _, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationReplay, AttemptNo: 2,
	}, &recordingConsumer{}); err != nil {
		t.Fatal(err)
	}
	if len(permits.attempts) != 2 || len(provider.attempts) != 2 || permits.releases != 2 {
		t.Fatalf("permit/provider attempts=%d/%d releases=%d, want two physical queries", len(permits.attempts), len(provider.attempts), permits.releases)
	}
	consumer := frozen.Requirements[0].Consumers[0]
	intervalMillis := consumer.ConsumerDeadlineUnixMilli - int64(contractRef.Slot.EvaluationTime)*1000
	wantDeadline := startedAt.Add(time.Duration(intervalMillis-consumer.DownstreamExecutionReserveMilliSec) * time.Millisecond).UnixMilli()
	for index := range permits.attempts {
		if permits.attempts[index].deadline.UnixMilli() != wantDeadline || provider.attempts[index].DeadlineUnixMilli != wantDeadline {
			t.Fatalf("physical query %d reset recovery deadline: permit/provider=%d/%d want=%d",
				index, permits.attempts[index].deadline.UnixMilli(), provider.attempts[index].DeadlineUnixMilli, wantDeadline)
		}
	}
}

func TestSourceRejectsInvalidOrPermitConsumedRecoveryBudget(t *testing.T) {
	t.Run("invalid frozen duration", func(t *testing.T) {
		contractRef, frozen := frozenExecution(t)
		consumer := &frozen.Requirements[0].Consumers[0]
		consumer.DownstreamExecutionReserveMilliSec = consumer.ConsumerDeadlineUnixMilli - int64(contractRef.Slot.EvaluationTime)*1000
		contractRef = bindFrozenDueDigest(t, contractRef, frozen)
		source, err := NewSource(staticFrozenPlan{plan: frozen}, &fakeProvider{}, &recordingQueryPermits{}, Config{MinReadyDelay: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		source.now = func() time.Time { return time.UnixMilli(2_000_000_000_000) }
		request := execution.QueryExecutionRequest{
			Contract: contractRef, Operation: execution.OperationReplay, AttemptNo: 2,
		}
		_, err = source.Execute(context.Background(), request, &recordingConsumer{})
		assertQueryExecutionBudgetExhausted(t, err, request, nil)
	})

	t.Run("permit wait consumed", func(t *testing.T) {
		contractRef, frozen := frozenExecution(t)
		clock := time.UnixMilli(2_000_000_000_000)
		permits := deadlineCheckingQueryPermits{now: func() time.Time { return clock }, wait: func() { clock = clock.Add(26 * time.Second) }}
		source, err := NewSource(staticFrozenPlan{plan: frozen}, &fakeProvider{}, permits, Config{MinReadyDelay: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		source.now = func() time.Time { return clock }
		source.wait = func(context.Context, time.Duration) error { return nil }
		request := execution.QueryExecutionRequest{
			Contract: contractRef, Operation: execution.OperationReplay, AttemptNo: 2,
		}
		_, err = source.Execute(context.Background(), request, &recordingConsumer{})
		assertQueryExecutionBudgetExhausted(t, err, request, context.DeadlineExceeded)
	})

	t.Run("normal permit deadline keeps live error semantics", func(t *testing.T) {
		contractRef, frozen := frozenExecution(t)
		clock := time.UnixMilli(2_000_000_000_000)
		permits := deadlineCheckingQueryPermits{now: func() time.Time { return clock }, wait: func() { clock = clock.Add(26 * time.Second) }}
		source, err := NewSource(staticFrozenPlan{plan: frozen}, &fakeProvider{}, permits, Config{MinReadyDelay: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		source.now = func() time.Time { return clock }
		source.wait = func(context.Context, time.Duration) error { return nil }
		_, err = source.Execute(context.Background(), execution.QueryExecutionRequest{
			Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
		}, &recordingConsumer{})
		var exhausted *execution.QueryExecutionBudgetExhaustedError
		if !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &exhausted) {
			t.Fatalf("normal permit deadline error=%v, want unchanged raw live deadline", err)
		}
	})
}

func assertQueryExecutionBudgetExhausted(
	t *testing.T,
	err error,
	request execution.QueryExecutionRequest,
	cause error,
) {
	t.Helper()
	var exhausted *execution.QueryExecutionBudgetExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("query execution budget error=%v, want typed exhaustion", err)
	}
	if exhausted.Slot != request.Contract.Slot || exhausted.Operation != request.Operation ||
		exhausted.AttemptNo != request.AttemptNo {
		t.Fatalf("query execution budget scope=%+v, want request=%+v", exhausted, request)
	}
	if cause != nil && !errors.Is(err, cause) {
		t.Fatalf("query execution budget cause=%v, want %v", err, cause)
	}
}

func TestPrepareUsesEarliestConsumerDeadlineAndDoesNotDelayEager(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	requirement := frozen.Requirements[0]
	second := requirement
	second.RequirementID = "secondary"
	second.DatasetName = "secondary"
	second.ReadinessClass = execution.ReadinessEager
	// Keep the EAGER requirement as the earliest consumer of the merged query.
	frozen.Requirements = []execution.DataRequirement{requirement, second}
	digest, err := execution.DeriveDuePlanSetDigest(frozen.DuePlans, frozen.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	contractRef.DuePlanSetDigest = digest
	prepared, err := Prepare(contractRef, frozen, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Queries) != 1 {
		t.Fatalf("queries=%d", len(prepared.Queries))
	}
	wantDeadline := requirement.Consumers[0].ConsumerDeadlineUnixMilli - requirement.Consumers[0].DownstreamExecutionReserveMilliSec
	if prepared.Queries[0].DeadlineUnixMilli != wantDeadline {
		t.Fatalf("deadline=%d want=%d", prepared.Queries[0].DeadlineUnixMilli, wantDeadline)
	}
}

func TestPrepareRejectsFinalizedRequiredUntilProbeGate(t *testing.T) {
	contractRef, frozen := frozenExecution(t)
	frozen.Requirements[0].ReadinessClass = execution.ReadinessFinalizedRequired
	digest, err := execution.DeriveDuePlanSetDigest(frozen.DuePlans, frozen.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	contractRef.DuePlanSetDigest = digest
	if _, err := Prepare(contractRef, frozen, time.Second); !errors.Is(err, ErrFinalizedRequiredUnsupported) {
		t.Fatalf("error=%v", err)
	}
}

func TestDataBindingsShareFullImmutableViewAcrossConsumers(t *testing.T) {
	_, frozen := frozenExecution(t)
	requirement := frozen.Requirements[0]
	requirement.Consumers = append(requirement.Consumers, requirement.Consumers[0])
	requirement.Consumers[1].Consumer.HasLevel = true
	requirement.Consumers[1].Consumer.LevelID = 1
	dataset := execution.NewDataset([]contract.CanonicalRecordV2{{RecordID: "record", SourceTime: 1, BusinessID: "2"}})
	query := PlannedQuery{Requirements: []execution.DataRequirement{requirement}}
	bindings, err := dataBindings(query, execution.ProviderSeriesBatch{Dataset: dataset, CompletionRef: "result"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 || bindings[0].View != bindings[1].View || !bindings[0].View.Uses(dataset) {
		t.Fatalf("bindings do not share one immutable full view: %+v", bindings)
	}
}

type staticFrozenPlan struct{ plan FrozenPlan }

func (source staticFrozenPlan) ResolveFrozenPlan(context.Context, execution.FrozenExecutionContractRef) (FrozenPlan, error) {
	return source.plan, nil
}

type fakeProvider struct {
	completeness execution.Completeness
	empty        bool
	reason       execution.ReasonCode
	attempts     []execution.QueryAttempt
}

func (provider *fakeProvider) Execute(ctx context.Context, attempt execution.QueryAttempt, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
	provider.attempts = append(provider.attempts, attempt)
	completeness := provider.completeness
	if completeness == "" {
		completeness = execution.CompletenessFull
	}
	if provider.empty {
		dataState := execution.DataStateEmpty
		if completeness == execution.CompletenessUnavailable {
			dataState = execution.DataStateUnknown
		}
		return execution.ProviderCompletion{Ref: "provider-result", PhysicalQuery: attempt.Spec.Digest,
			Completeness: completeness, DataState: dataState,
			RouteFacts: execution.ProviderRouteFacts{ProviderRouteRef: attempt.Spec.PlanFacts.ProviderRouteRef,
				Attempts: []execution.RouteAttemptFact{{AttemptNo: attempt.AttemptNo, Endpoint: "uq", Result: execution.RouteAttemptFailed, ReasonCode: provider.reason}}}}, nil
	}
	fields := []contract.DimensionFieldV2{{Name: "host", Value: json.RawMessage(`"127.0.0.1"`)}}
	dimension, _ := contract.DeriveDimensionIdentityDigestV2("tenant", "2", fields)
	recordID, _ := contract.DeriveRecordIDV2(dimension, int64(attempt.Slot.EvaluationTime)-60)
	records := []contract.CanonicalRecordV2{{RecordID: recordID, SourceTime: int64(attempt.Slot.EvaluationTime) - 60,
		BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Fields: fields, Digest: dimension},
		Values: map[string]json.RawMessage{"value": json.RawMessage(`60`)}, Dimensions: map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.1"`)}, ReceivedTime: int64(attempt.Slot.EvaluationTime)}}
	digest, _ := contract.DeriveCanonicalDigestV2("test-delivery", records)
	ref := execution.ProviderResultRef("provider-result")
	delivery := execution.SeriesDelivery{PhysicalQuery: attempt.Spec.Digest, QueryRevision: attempt.Spec.PlanFacts.QueryRevision, Series: 1, Records: 1, Digest: digest}
	if err := sink.ConsumeProviderSeries(ctx, execution.ProviderSeriesBatch{PhysicalQuery: attempt.Spec.Digest, CompletionRef: ref, Dataset: execution.NewDataset(records), Delivery: delivery}); err != nil {
		return execution.ProviderCompletion{}, err
	}
	return execution.ProviderCompletion{Ref: ref, PhysicalQuery: attempt.Spec.Digest, Completeness: completeness,
		DataState: execution.DataStateData, Delivery: delivery,
		RouteFacts: execution.ProviderRouteFacts{ProviderRouteRef: attempt.Spec.PlanFacts.ProviderRouteRef},
		Stats:      execution.ProviderStats{Series: 1, Records: 1}}, nil
}

type recordedPermitAttempt struct {
	slot      execution.SlotIdentity
	operation execution.Operation
	deadline  time.Time
}

type recordingQueryPermits struct {
	attempts  []recordedPermitAttempt
	releases  int
	onAcquire func()
}

func (permits *recordingQueryPermits) AcquireQueryPermit(
	_ context.Context,
	slot execution.SlotIdentity,
	operation execution.Operation,
	deadline time.Time,
) (QueryPermit, error) {
	permits.attempts = append(permits.attempts, recordedPermitAttempt{slot: slot, operation: operation, deadline: deadline})
	if permits.onAcquire != nil {
		permits.onAcquire()
	}
	permit := &recordingQueryPermit{release: func() { permits.releases++ }}
	if operation != execution.OperationNormal {
		permit.recovery = &execution.RecoveryPermit{
			PermitID: "recovery-permit", Slot: slot, Operation: operation, ExpiresAtUnixMilli: deadline.UnixMilli(),
		}
	}
	return permit, nil
}

type deadlineCheckingQueryPermits struct {
	now  func() time.Time
	wait func()
}

func (permits deadlineCheckingQueryPermits) AcquireQueryPermit(
	_ context.Context,
	_ execution.SlotIdentity,
	_ execution.Operation,
	deadline time.Time,
) (QueryPermit, error) {
	permits.wait()
	if !deadline.After(permits.now()) {
		return nil, context.DeadlineExceeded
	}
	return &recordingQueryPermit{}, nil
}

type recordingQueryPermit struct {
	recovery *execution.RecoveryPermit
	release  func()
}

func (permit *recordingQueryPermit) RecoveryPermit() *execution.RecoveryPermit {
	return permit.recovery
}
func (permit *recordingQueryPermit) Release() {
	if permit.release != nil {
		permit.release()
		permit.release = nil
	}
}

type recordingConsumer struct {
	begin   int
	header  execution.InternalExecutionHeader
	batches []execution.SeriesExecutionBatch
}

func (consumer *recordingConsumer) Begin(_ context.Context, header execution.InternalExecutionHeader) error {
	consumer.begin++
	consumer.header = header
	return nil
}
func (consumer *recordingConsumer) ConsumeSeries(_ context.Context, batch execution.SeriesExecutionBatch) error {
	consumer.batches = append(consumer.batches, batch)
	return batch.Validate(consumer.header)
}

func frozenExecution(t *testing.T) (execution.FrozenExecutionContractRef, FrozenPlan) {
	t.Helper()
	compiled := compilePlan(t)
	planID := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1001"}
	evaluationTime := execution.EvaluationTime(1_700_124_000)
	due := execution.DuePlan{Identity: planID, CompiledPlan: compiled, StateGeneration: "state-v1", StateApplyEpoch: 1,
		ScheduleRevision: "plan-schedule-v1", CompletionDeadlineUnixMilli: int64(evaluationTime)*1000 + 30_000}
	requirement := execution.DataRequirement{RequirementID: "primary", DatasetName: "primary", Role: execution.InputRolePrimary,
		LogicalQueryRef: "query", RelativeWindow: execution.RelativeQueryWindow{StartOffsetSeconds: -60, EndOffsetSeconds: 0, HalfOpen: true},
		StepMillis: 60_000, AlignmentMillis: 60_000, ResultWindowPolicy: execution.ResultWindowExactHalfOpen,
		ReadinessClass: execution.ReadinessEager, RequiredColumns: []string{"value"},
		Consumers: []execution.DataRequirementConsumer{{Consumer: execution.ConsumerRef{Plan: planID},
			ConsumerDeadlineUnixMilli: due.CompletionDeadlineUnixMilli, DownstreamExecutionReserveMilliSec: 5_000}}}
	facts := queryFacts(t)
	dueDigest, err := execution.DeriveDuePlanSetDigest([]execution.DuePlan{due}, []execution.DataRequirement{requirement})
	if err != nil {
		t.Fatal(err)
	}
	contractRef := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "group", EvaluationTime: evaluationTime},
		SnapshotRevision: "snapshot-v1", QueryRevision: facts.QueryRevision, ScheduleRevision: "schedule-v1",
		ScheduleSegmentStart: evaluationTime - 60, DuePlanSetDigest: dueDigest}
	return contractRef, FrozenPlan{DuePlans: []execution.DuePlan{due}, Requirements: []execution.DataRequirement{requirement},
		QueryFacts: map[execution.LogicalQueryRef]execution.QueryPlanFacts{"query": facts}}
}

func bindFrozenDueDigest(
	t *testing.T,
	contractRef execution.FrozenExecutionContractRef,
	frozen FrozenPlan,
) execution.FrozenExecutionContractRef {
	t.Helper()
	digest, err := execution.DeriveDuePlanSetDigest(frozen.DuePlans, frozen.Requirements)
	if err != nil {
		t.Fatal(err)
	}
	contractRef.DuePlanSetDigest = digest
	return contractRef
}

func queryFacts(t *testing.T) execution.QueryPlanFacts {
	t.Helper()
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{Provider: execution.ProviderUQ, ProviderRouteRef: "uq-main",
		TenantID: "tenant", BusinessID: "2", SpaceScope: "bkcc__2", QueryList: []execution.QueryClause{{DataSource: "bkmonitor",
			TableID: "system.cpu", FieldName: "usage", ReferenceName: "a", Driver: "influxdb", TimeField: "time",
			Functions: []execution.QueryFunction{{Method: "abs", Position: 0}}, TimeAggregation: execution.QueryFunction{Method: "avg", Position: 0, Window: "60s"}}},
		MetricMerge: "a", StepMillis: 60_000, AlignmentMillis: 60_000, Timezone: "UTC",
		Normalization: execution.DatasetNormalizationSpec{DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64), IdentityFields: []string{"host"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time"},
			SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
			SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
			ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value", ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "uq-threshold-normalization-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func compilePlan(t *testing.T) *strategy.CompiledPlan {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 4,
		MaxAlgorithmsPerLevel: 4, MaxGroupsPerAlgorithm: 4, MaxConditionsPerAlgorithm: 8, MaxASTNodesPerLevel: 32,
		MaxTriggerWindowSize: 64, MaxRecoveryConsecutiveWindows: 64, MaxRequiredHistoryPoints: 64, MaxTriggerComputeCost: 1 << 16,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 4, MaxCacheBytes: 1 << 20, NegativeCacheTTL: time.Minute, BudgetRevision: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "1001", Revision: "r1"}
	projection := contract.InputProjectionV2{ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id", MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired}
	level := contract.LevelIRV2{
		Definition: contract.LevelDefinitionV2{LevelID: 1, Priority: 1},
		Connector:  contract.LevelConnectorAND,
		DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{
			Type: "Threshold", Version: 1,
			Config: json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"50"}]}]}`),
		}}},
		TriggerPlan:  contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
		RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
	}
	plan := contract.EvaluationPlanV2{PlanID: "1001", StrategyRef: ref, InputProjection: projection,
		StrategyIR: contract.StrategyIRV2{Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2},
			StrategyRef: ref, InputProjection: projection,
			ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 60, AggregationInterval: 60, EvaluationInterval: 60},
			Levels:             []contract.LevelIRV2{level}}}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{Plan: plan, DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("a", 64), NormalizationDigest: strings.Repeat("b", 64), IdentityFields: []string{"host"}, SourceTimeField: "_time", ReceivedTimeField: "_received_time"},
		StateSemantics: strategy.StateSemantics{StateSchemaVersion: "v1", CodecSemanticsVersion: "v1", IdentitySchemaDigest: strings.Repeat("c", 64), SourceTimeSemanticsVersion: "seconds-v1", HistoryCellSemanticsVersion: "v1"}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("compile terminal=%+v", result.PlanTerminal())
	}
	return compiled
}
