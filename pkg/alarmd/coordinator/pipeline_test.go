// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

func TestEvaluationPipelineRunsG1ThresholdThroughRuntimeState(t *testing.T) {
	t.Parallel()

	payload := encodeG1Envelope(t)
	adapter := inputv2.New(g1ReaderLimits())
	decoded, err := adapter.Decode(context.Background(), payload)
	if err != nil || decoded.Rejected || decoded.Input == nil {
		t.Fatalf("Decode() = %#v, %v", decoded, err)
	}

	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), g1CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	detector, err := detect.NewEvaluator(detect.NewDefaultRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	backend := &memoryStateBackend{values: make(map[string][]byte)}
	codec, store := newG1StateStore(t, backend)
	semantics, err := state.RuntimeStateSemantics()
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := NewEvaluationPipeline(PipelineOptions{
		Compiler: compiler, Detector: detector, EffectiveTime: strategy.NewStaticScheduleProvider(nil),
		State: store, StateCodec: codec,
		StateSemantics: strategy.StateSemantics{
			StateSchemaVersion: semantics.StateSchemaVersion, CodecSemanticsVersion: semantics.CodecSemanticsVersion,
			IdentitySchemaDigest: semantics.IdentitySchemaDigest, SourceTimeSemanticsVersion: semantics.SourceTimeSemanticsVersion,
			HistoryCellSemanticsVersion: semantics.HistoryCellSemanticsVersion,
		},
		DetectLimits: detect.ExecutionLimits{
			MaxPlans: 4, MaxSelectedRecordsPerPlan: 100, MaxSeriesPerPlan: 100, MaxRecordsPerSeries: 100,
			MaxLevelFacts: 1_000, MaxPredicateEvaluations: 1_000, MaxResultBytes: 1 << 20,
		},
		TriggerLimits: trigger.EvaluationLimitsV2{
			MaxLevels: 8, MaxTriggerWindowSize: 100, MaxRecoveryConsecutiveWindows: 100,
			MaxRequiredHistoryPoints: 1_000, MaxLevelResultsPerEvent: 8, MaxEvidenceBytesPerEvent: 64 << 10,
			MaxComputeCost: 10_000,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	message, err := pipeline.EvaluateMessage(context.Background(), decoded.Input)
	if err != nil {
		t.Fatalf("EvaluateMessage() error = %v", err)
	}
	result := message.CriticalResult
	if len(result.Events) != 1 || result.Events[0].EventKind != contract.LevelResultAbnormal || result.Events[0].PrimaryLevelID != 5 {
		t.Fatalf("events = %#v, want one Level 5 ABNORMAL", result.Events)
	}
	if len(result.StateWrite.Items) != 1 || !result.StateWrite.Items[0].Window.Changed() {
		t.Fatalf("state write = %#v, want one changed window", result.StateWrite)
	}
	if message.Receipt == nil || message.Receipt.Status != contract.ReceiptStatusCompleted ||
		message.Receipt.Counts != (contract.ReceiptCountsV1{Received: 1, Selected: 1, Processed: 1, Events: 1}) ||
		len(message.Receipt.PerPlan) != 1 || message.Receipt.PerPlan[0].Abnormal != 1 {
		t.Fatalf("receipt = %#v, want one processed ABNORMAL event", message.Receipt)
	}

	ack := 0
	completer, err := NewCriticalCompleter(triggerEventSinkFunc(func(_ context.Context, events []contract.TriggerEventV1) error {
		ack += len(events)
		return nil
	}), store)
	if err != nil {
		t.Fatal(err)
	}
	if err := completer.Complete(context.Background(), result); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if ack != 1 || len(backend.values) != 1 {
		t.Fatalf("completion = ack:%d redis keys:%d, want 1/1", ack, len(backend.values))
	}

	loaded, err := store.LoadWindows(context.Background(), state.LoadWindowsRequest{Items: []state.LoadWindowSpec{{
		Identity: result.StateWrite.Items[0].Identity, Requirements: result.StateWrite.Items[0].Requirements,
	}}})
	if err != nil || len(loaded.Items) != 1 || loaded.Items[0].Status != state.LoadFound {
		t.Fatalf("state round-trip = %#v, %v", loaded, err)
	}
	history, ok := loaded.Items[0].Window.History(5)
	if !ok || history.Summarize(1_725_000_000, 1).Completeness != state.HistoryFull {
		t.Fatalf("persisted history is not FULL: %#v, %v", history, ok)
	}
}

func TestEvaluationPipelineResolvesEffectiveTimeOncePerMessage(t *testing.T) {
	t.Parallel()

	adapter := inputv2.New(g1ReaderLimits())
	decoded, err := adapter.Decode(context.Background(), encodeSharedG1Envelope(t))
	if err != nil || decoded.Rejected || decoded.Input == nil {
		t.Fatalf("Decode() = %#v, %v", decoded, err)
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), g1CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	detector, err := detect.NewEvaluator(detect.NewDefaultRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	backend := &memoryStateBackend{values: make(map[string][]byte)}
	codec, store := newG1StateStore(t, backend)
	semantics, err := state.RuntimeStateSemantics()
	if err != nil {
		t.Fatal(err)
	}
	resolveCalls := 0
	provider := effectiveTimeProviderFunc(func(ctx context.Context, requests []strategy.EffectiveTimeRequest) ([]strategy.EffectiveTimeFact, error) {
		resolveCalls++
		if len(requests) != 2 {
			t.Fatalf("EffectiveTime requests = %d, want one request for each compiled Plan Level", len(requests))
		}
		return strategy.NewStaticScheduleProvider(nil).Resolve(ctx, requests)
	})
	pipeline, err := NewEvaluationPipeline(PipelineOptions{
		Compiler: compiler, Detector: detector, EffectiveTime: provider, State: store, StateCodec: codec,
		StateSemantics: strategy.StateSemantics{
			StateSchemaVersion: semantics.StateSchemaVersion, CodecSemanticsVersion: semantics.CodecSemanticsVersion,
			IdentitySchemaDigest: semantics.IdentitySchemaDigest, SourceTimeSemanticsVersion: semantics.SourceTimeSemanticsVersion,
			HistoryCellSemanticsVersion: semantics.HistoryCellSemanticsVersion,
		},
		DetectLimits: detect.ExecutionLimits{
			MaxPlans: 4, MaxSelectedRecordsPerPlan: 100, MaxSeriesPerPlan: 100, MaxRecordsPerSeries: 100,
			MaxLevelFacts: 1_000, MaxPredicateEvaluations: 1_000, MaxResultBytes: 1 << 20,
		},
		TriggerLimits: trigger.EvaluationLimitsV2{
			MaxLevels: 8, MaxTriggerWindowSize: 100, MaxRecoveryConsecutiveWindows: 100,
			MaxRequiredHistoryPoints: 1_000, MaxLevelResultsPerEvent: 8, MaxEvidenceBytesPerEvent: 64 << 10,
			MaxComputeCost: 10_000,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := pipeline.EvaluateMessage(context.Background(), decoded.Input); err != nil {
		t.Fatal(err)
	}
	if resolveCalls != 1 {
		t.Fatalf("EffectiveTime Resolve calls = %d, want 1 per message", resolveCalls)
	}
}

type memoryStateBackend struct {
	mu     sync.Mutex
	values map[string][]byte
}

func (backend *memoryStateBackend) MGet(_ context.Context, keys []string) ([][]byte, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	result := make([][]byte, len(keys))
	for index, key := range keys {
		result[index] = append([]byte(nil), backend.values[key]...)
		if _, ok := backend.values[key]; !ok {
			result[index] = nil
		}
	}
	return result, nil
}

func (backend *memoryStateBackend) SetMany(_ context.Context, writes []state.BackendWrite) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	for _, write := range writes {
		backend.values[write.Key] = append([]byte(nil), write.Value...)
	}
	return nil
}

func newG1StateStore(t testing.TB, backend state.Backend) (*state.Codec, *state.Store) {
	t.Helper()
	codec, err := state.NewCodec(state.CodecLimits{MaxLevels: 8, MaxPoints: 1_000, MaxEncodedBytes: 128 << 10})
	if err != nil {
		t.Fatal(err)
	}
	router, err := state.NewFixedRouter("memory", backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.NewStore(state.StoreOptions{
		Prefix: "alarmd-test", Codec: codec, Router: router,
		Limits: state.StoreLimits{MaxKeysPerBatch: 100, MaxKeyBytesPerBatch: 1 << 20, MaxLoadedBytes: 16 << 20, MaxWrittenBytes: 16 << 20},
		MinTTL: time.Minute, MaxTTL: 24 * time.Hour, RestartMargin: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	return codec, store
}

func encodeG1Envelope(t testing.TB) []byte {
	t.Helper()
	fields := []contract.DimensionFieldV2{{Name: "host", Value: json.RawMessage(`"127.0.0.1"`)}}
	dimensionDigest, err := contract.DeriveDimensionIdentityDigestV2("default", "2", fields)
	if err != nil {
		t.Fatal(err)
	}
	recordID, err := contract.DeriveRecordIDV2(dimensionDigest, 1_725_000_000)
	if err != nil {
		t.Fatal(err)
	}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	ref := contract.StrategyRefV2{TenantID: "default", StrategyID: "1001", Revision: "strategy-r1"}
	strategyIR := contract.StrategyIRV2{
		Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2, Minor: 0}, RequiredFeatures: []string{}, StrategyRef: ref,
		ExecutionSemantics: contract.ExecutionSemanticsV2{
			EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60,
			EvaluationInterval: 60, LatenessTolerance: 120,
		},
		InputProjection: projection,
		Levels: []contract.LevelIRV2{{
			Definition: contract.LevelDefinitionV2{LevelID: 5, LevelCode: "critical", Priority: 1}, Connector: contract.LevelConnectorAND,
			DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{
				Type: "Threshold", Version: 1,
				Config: json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GT","threshold_decimal":"50"}]}]}`),
			}}},
			TriggerPlan:  contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
			RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
		}},
	}
	ranges := []contract.SelectorRangeV2{{Start: 0, End: 1}}
	envelope := contract.ExecutionEnvelopeV2{
		Schema: contract.Schema{Name: contract.ExecutionEnvelopeSchemaV2, Major: 2, Minor: 0}, RequiredFeatures: []string{},
		ExecutionID: "execution-1", MessageID: "message-1", TenantID: "default",
		QueryGroup:   contract.QueryGroupV2{Key: "query-group-1", QueryMD5: "query-md5-1", QueryRevision: "query-r1", EvaluationTime: 1_725_000_060},
		SourceWindow: contract.SourceWindowV2{FromTime: 1_724_999_700, UntilTime: 1_725_000_060},
		QueryResult:  contract.QueryResultV2{Completeness: contract.QueryCompletenessFull},
		DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64), IdentityFields: []string{"host"},
			SourceTimeField: "time", ReceivedTimeField: "received_time",
		},
		PlanSet: contract.PlanSetV2{PlanCount: 1, EvaluationPlans: []contract.EvaluationPlanV2{{
			PlanID: "1001", StrategyRef: ref, InputProjection: projection, StrategyIR: strategyIR,
		}}},
		Selectors: []contract.PlanSelectorV2{{PlanOrdinal: 0, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &ranges}}},
		Records: []contract.CanonicalRecordV2{{
			RecordID: recordID, SourceTime: 1_725_000_000, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Fields: fields, Digest: dimensionDigest},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(`50.1`)}, Dimensions: map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.1"`)},
			ReceivedTime: 1_725_000_001,
		}},
	}
	envelope.PlanSet.PlanSetDigest, err = contract.DerivePlanSetDigestV2(envelope.PlanSet)
	if err != nil {
		t.Fatal(err)
	}
	envelope.PayloadDigest, err = contract.DeriveExecutionEnvelopePayloadDigestV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := contract.CanonicalJSONV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func encodeSharedG1Envelope(t testing.TB) []byte {
	t.Helper()
	var envelope contract.ExecutionEnvelopeV2
	if err := json.Unmarshal(encodeG1Envelope(t), &envelope); err != nil {
		t.Fatal(err)
	}
	secondFields := []contract.DimensionFieldV2{{Name: "host", Value: json.RawMessage(`"127.0.0.2"`)}}
	secondDimension, err := contract.DeriveDimensionIdentityDigestV2(envelope.TenantID, "2", secondFields)
	if err != nil {
		t.Fatal(err)
	}
	secondRecordID, err := contract.DeriveRecordIDV2(secondDimension, envelope.Records[0].SourceTime)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Records = append(envelope.Records, contract.CanonicalRecordV2{
		RecordID: secondRecordID, SourceTime: envelope.Records[0].SourceTime, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Fields: secondFields, Digest: secondDimension},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`50.2`)},
		Dimensions:        map[string]json.RawMessage{"host": json.RawMessage(`"127.0.0.2"`)},
		ReceivedTime:      envelope.Records[0].ReceivedTime,
	})
	secondPlan := envelope.PlanSet.EvaluationPlans[0]
	secondPlan.PlanID = "1002"
	secondPlan.StrategyRef.StrategyID = "1002"
	secondPlan.StrategyIR.StrategyRef.StrategyID = "1002"
	envelope.PlanSet.EvaluationPlans = append(envelope.PlanSet.EvaluationPlans, secondPlan)
	envelope.PlanSet.PlanCount = 2
	ranges := []contract.SelectorRangeV2{{Start: 0, End: 2}}
	envelope.Selectors[0].Selector.Ranges = &ranges
	secondSelector := envelope.Selectors[0]
	secondSelector.PlanOrdinal = 1
	envelope.Selectors = append(envelope.Selectors, secondSelector)
	envelope.PlanSet.PlanSetDigest, err = contract.DerivePlanSetDigestV2(envelope.PlanSet)
	if err != nil {
		t.Fatal(err)
	}
	envelope.PayloadDigest, err = contract.DeriveExecutionEnvelopePayloadDigestV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := contract.CanonicalJSONV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

type effectiveTimeProviderFunc func(context.Context, []strategy.EffectiveTimeRequest) ([]strategy.EffectiveTimeFact, error)

func (function effectiveTimeProviderFunc) Resolve(
	ctx context.Context,
	requests []strategy.EffectiveTimeRequest,
) ([]strategy.EffectiveTimeFact, error) {
	return function(ctx, requests)
}

func g1ReaderLimits() contract.ReaderLimitsV2 {
	return contract.ReaderLimitsV2{
		MaxEnvelopeBytes: 1 << 20, MaxRecordsPerMessage: 100, MaxPlansPerMessage: 10, MaxLevelsPerPlan: 10,
		MaxSelectorBytes: 1 << 16, MaxRecordBytes: 1 << 16, MaxPlanSetBytes: 1 << 18,
		MaxContractDepth: 32, MaxStringBytes: 1 << 16, MaxValidationIssues: 100,
	}
}

func g1CompilerLimits() strategy.Limits {
	return strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxRequiredHistoryPoints: 4_096,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "coordinator-test-v1",
	}
}
