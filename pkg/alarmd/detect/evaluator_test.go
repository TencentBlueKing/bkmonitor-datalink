// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestEvaluatorRunsGoldenDynamicLevels(t *testing.T) {
	payload, err := os.ReadFile("../contract/testdata/go-v2/execution_envelope_v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope contract.ExecutionEnvelopeV2
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	input, executions, digest := fixtureExecutions(t, &envelope)

	batch, err := newTestEvaluator(t).Evaluate(context.Background(), EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if batch.Completeness != contract.QueryCompletenessFull || batch.ExecutionMode != ExecutionModeStandard ||
		batch.DetectionCoverage != DetectionCoverageFull || len(batch.Series) != 1 || len(batch.Series[0].Records) != 1 {
		t.Fatalf("DetectionBatch = %#v", batch)
	}
	record := batch.Series[0].Records[0]
	if len(record.ProjectedValues) != 1 || record.ProjectedValues[0].CanonicalDecimal != "50.100000" {
		t.Fatalf("ProjectedValues = %#v", record.ProjectedValues)
	}
	if len(record.LevelFacts) != 2 || record.LevelFacts[0].Definition.LevelID != 1 || record.LevelFacts[1].Definition.LevelID != 5 ||
		record.LevelFacts[0].Definition.Priority != 20 || record.LevelFacts[1].Definition.Priority != 1 {
		t.Fatalf("LevelFacts order = %#v", record.LevelFacts)
	}
	for _, fact := range record.LevelFacts {
		if fact.Result != FactResultAnomalous || len(fact.DetectFingerprint) != 64 || len(fact.Evidence.PredicateDigest) != 64 {
			t.Fatalf("LevelFact = %#v", fact)
		}
	}
	if batch.Counts.Plans != 1 || batch.Counts.CompiledLevels != 2 || batch.Counts.LevelFacts != 2 ||
		batch.Counts.PredicateEvaluations != 2 || batch.Counts.AnomalousFacts != 2 {
		t.Fatalf("Counts = %#v", batch.Counts)
	}
}

func TestEvaluatorIsolatesLevelProjectionProblems(t *testing.T) {
	first := fixtureLevel(1, 20, contract.LevelConnectorAND, fixtureThresholdAlgorithm("GT", "50", "percent", ""))
	second := fixtureLevel(5, 1, contract.LevelConnectorAND, fixtureThresholdAlgorithmFor("other", "GT", "50", "percent", ""))
	plan := fixturePlan("1001", []contract.LevelIRV2{first, second})
	plan.InputProjection.ValueFields = []string{"other", "value"}
	plan.StrategyIR.InputProjection.ValueFields = []string{"other", "value"}
	envelope := fixtureEnvelope(t, []contract.EvaluationPlanV2{plan}, []fixtureRecord{
		{host: "host", sourceTime: 100, value: json.RawMessage(`60`)},
	}, contract.QueryCompletenessFull)
	input, executions, digest := fixtureExecutions(t, envelope)
	batch, err := newTestEvaluator(t).Evaluate(context.Background(), EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	facts := batch.Series[0].Records[0].LevelFacts
	if len(facts) != 2 || facts[0].Result != FactResultAnomalous || facts[1].Result != FactResultUnavailable ||
		facts[1].ReasonCode != contract.ReasonRequiredValueMissing {
		t.Fatalf("LevelFacts = %#v", facts)
	}
}

func TestEvaluatorThresholdOperatorsAndCompositions(t *testing.T) {
	operators := []struct {
		operator string
		value    string
		want     string
	}{
		{"GT", "51", FactResultAnomalous}, {"GTE", "50", FactResultAnomalous},
		{"EQ", "50", FactResultAnomalous}, {"NEQ", "50", FactResultNormal},
		{"LT", "49", FactResultAnomalous}, {"LTE", "50", FactResultAnomalous},
	}
	for _, test := range operators {
		t.Run(test.operator, func(t *testing.T) {
			fact := evaluateSingleFact(t, []contract.AlgorithmIRV2{fixtureThresholdAlgorithm(test.operator, "50", "percent", "")},
				contract.LevelConnectorAND, json.RawMessage(test.value), "percent")
			if fact.Result != test.want {
				t.Fatalf("result = %q, want %q", fact.Result, test.want)
			}
		})
	}

	t.Run("groups OR and conditions AND", func(t *testing.T) {
		config := thresholdConfig("percent", "", []thresholdTestGroup{
			{{"GT", "60"}, {"LT", "100"}},
			{{"GTE", "50"}, {"LTE", "50"}},
		})
		fact := evaluateSingleFact(t, []contract.AlgorithmIRV2{{Type: strategy.DetectorKindThreshold, Version: 1, Config: config}},
			contract.LevelConnectorAND, json.RawMessage(`50`), "percent")
		if fact.Result != FactResultAnomalous || !fact.Evidence.HasMatchedGroup || fact.Evidence.MatchedGroupOrdinal != 1 {
			t.Fatalf("fact = %#v", fact)
		}
	})

	t.Run("level AND", func(t *testing.T) {
		fact := evaluateSingleFact(t, []contract.AlgorithmIRV2{
			fixtureThresholdAlgorithm("GT", "40", "percent", ""), fixtureThresholdAlgorithm("LT", "60", "percent", ""),
		}, contract.LevelConnectorAND, json.RawMessage(`50`), "percent")
		if fact.Result != FactResultAnomalous {
			t.Fatalf("fact = %#v", fact)
		}
	})

	t.Run("level OR", func(t *testing.T) {
		fact := evaluateSingleFact(t, []contract.AlgorithmIRV2{
			fixtureThresholdAlgorithm("GT", "100", "percent", ""), fixtureThresholdAlgorithm("LT", "60", "percent", ""),
		}, contract.LevelConnectorOR, json.RawMessage(`50`), "percent")
		if fact.Result != FactResultAnomalous || !fact.Evidence.HasMatchedAlgorithm || fact.Evidence.MatchedAlgorithmOrdinal != 1 {
			t.Fatalf("fact = %#v", fact)
		}
	})
}

func TestEvaluatorUsesCompiledUnitsAndHalfEven(t *testing.T) {
	t.Run("percentunit", func(t *testing.T) {
		fact := evaluateSingleFact(t, []contract.AlgorithmIRV2{fixtureThresholdAlgorithm("GTE", "80", "percentunit", "%")},
			contract.LevelConnectorAND, json.RawMessage(`0.8`), "percentunit")
		if fact.Result != FactResultAnomalous {
			t.Fatalf("fact = %#v", fact)
		}
	})

	t.Run("half even up", func(t *testing.T) {
		fact := evaluateSingleFact(t, []contract.AlgorithmIRV2{fixtureThresholdAlgorithm("EQ", "1.234568", "percent", "")},
			contract.LevelConnectorAND, json.RawMessage(`1.2345675`), "percent")
		if fact.Result != FactResultAnomalous {
			t.Fatalf("fact = %#v", fact)
		}
	})

	t.Run("half even stays even", func(t *testing.T) {
		fact := evaluateSingleFact(t, []contract.AlgorithmIRV2{fixtureThresholdAlgorithm("EQ", "1.234566", "percent", "")},
			contract.LevelConnectorAND, json.RawMessage(`1.2345665`), "percent")
		if fact.Result != FactResultAnomalous {
			t.Fatalf("fact = %#v", fact)
		}
	})
}

func TestEvaluatorIsolatesRequiredValueProblems(t *testing.T) {
	records := []fixtureRecord{
		{host: "shared", sourceTime: 100, absent: true},
		{host: "shared", sourceTime: 160, value: json.RawMessage(`null`)},
		{host: "shared", sourceTime: 220, value: json.RawMessage(`"60"`)},
		{host: "shared", sourceTime: 280, value: json.RawMessage(`true`)},
		{host: "shared", sourceTime: 340, value: json.RawMessage(`1e100`)},
		{host: "shared", sourceTime: 400, value: json.RawMessage(`60`)},
	}
	envelope := fixtureEnvelope(t, []contract.EvaluationPlanV2{fixturePlan("1001", []contract.LevelIRV2{
		fixtureLevel(1, 1, contract.LevelConnectorAND, fixtureThresholdAlgorithm("GT", "50", "percent", "")),
	})}, records, contract.QueryCompletenessFull)
	input, executions, digest := fixtureExecutions(t, envelope)
	batch, err := newTestEvaluator(t).Evaluate(context.Background(), EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(batch.Series) != 1 || len(batch.Series[0].Records) != len(records) {
		t.Fatalf("Series = %#v", batch.Series)
	}
	wantReasons := []string{
		contract.ReasonRequiredValueMissing, contract.ReasonRequiredValueMissing,
		contract.ReasonRequiredValueTypeMismatch, contract.ReasonRequiredValueTypeMismatch,
		contract.ReasonRequiredValueNormalizationFailed, "",
	}
	for index, record := range batch.Series[0].Records {
		fact := record.LevelFacts[0]
		wantResult := FactResultUnavailable
		if index == len(records)-1 {
			wantResult = FactResultAnomalous
		}
		if fact.Result != wantResult || fact.ReasonCode != wantReasons[index] {
			t.Fatalf("record %d fact = %#v, want result=%s reason=%s", index, fact, wantResult, wantReasons[index])
		}
	}
	if batch.Counts.UnavailableFacts != 5 || batch.Counts.AnomalousFacts != 1 || batch.Counts.NormalFacts != 0 {
		t.Fatalf("Counts = %#v", batch.Counts)
	}
}

func TestEvaluatorCompletenessSemantics(t *testing.T) {
	base := fixtureEnvelope(t, []contract.EvaluationPlanV2{fixturePlan("1001", []contract.LevelIRV2{
		fixtureLevel(1, 1, contract.LevelConnectorAND, fixtureThresholdAlgorithm("GT", "50", "percent", "")),
	})}, []fixtureRecord{{host: "host", sourceTime: 100, value: json.RawMessage(`60`)}}, contract.QueryCompletenessFull)
	input, executions, digest := fixtureExecutions(t, base)
	evaluator := newTestEvaluator(t)

	for _, completeness := range []string{contract.QueryCompletenessFull, contract.QueryCompletenessPartial} {
		batch, err := evaluator.Evaluate(context.Background(), EvaluateRequest{
			Completeness: completeness, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
		})
		if err != nil || batch.Completeness != completeness || len(batch.Series) != 1 || batch.Counts.LevelFacts != 1 {
			t.Fatalf("Evaluate(%s) = (%#v, %v)", completeness, batch, err)
		}
	}

	batch, err := evaluator.Evaluate(context.Background(), EvaluateRequest{
		Completeness: contract.QueryCompletenessUnavailable, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	})
	if err != nil || len(batch.Series) != 0 || batch.Counts.SkippedPlans != 1 || batch.Counts.SkippedSelectedRecords != 1 {
		t.Fatalf("Evaluate(UNAVAILABLE) = (%#v, %v)", batch, err)
	}

	empty := fixtureEnvelope(t, base.PlanSet.EvaluationPlans, nil, contract.QueryCompletenessFull)
	emptyInput, emptyExecutions, emptyDigest := fixtureExecutions(t, empty)
	batch, err = evaluator.Evaluate(context.Background(), EvaluateRequest{
		Completeness: emptyInput.Execution().Completeness, DatasetContractDigest: emptyDigest, Plans: emptyExecutions, Limits: generousLimits(),
	})
	if err != nil || len(batch.Series) != 0 || batch.Counts.Plans != 0 || batch.Counts.EvaluatedRecords != 0 {
		t.Fatalf("Evaluate(FULL empty) = (%#v, %v)", batch, err)
	}
	_ = input
}

func TestEvaluatorSharesDatasetAcrossPlansAndSortsOutput(t *testing.T) {
	plans := []contract.EvaluationPlanV2{
		fixturePlan("1001", []contract.LevelIRV2{fixtureLevel(1, 1, contract.LevelConnectorAND, fixtureThresholdAlgorithm("GT", "50", "percent", ""))}),
		fixturePlan("1002", []contract.LevelIRV2{fixtureLevel(1, 1, contract.LevelConnectorAND, fixtureThresholdAlgorithm("GT", "70", "percent", ""))}),
	}
	records := []fixtureRecord{
		{host: "host-b", sourceTime: 220, value: json.RawMessage(`60`)},
		{host: "host-a", sourceTime: 160, value: json.RawMessage(`60`)},
		{host: "host-a", sourceTime: 100, value: json.RawMessage(`60`)},
	}
	envelope := fixtureEnvelope(t, plans, records, contract.QueryCompletenessFull)
	firstRanges := []contract.SelectorRangeV2{{Start: 0, End: 2}}
	secondRanges := []contract.SelectorRangeV2{{Start: 1, End: 3}}
	envelope.Selectors = []contract.PlanSelectorV2{
		{PlanOrdinal: 0, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &firstRanges}},
		{PlanOrdinal: 1, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &secondRanges}},
	}
	input, executions, digest := fixtureExecutions(t, envelope)
	batch, err := newTestEvaluator(t).Evaluate(context.Background(), EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if len(batch.Series) != 3 || batch.Series[0].PlanRef.StrategyID != "1001" || batch.Series[2].PlanRef.StrategyID != "1002" ||
		batch.Counts.EvaluatedRecords != 4 {
		t.Fatalf("series order = %#v", batch.Series)
	}
	for index := 1; index < len(batch.Series); index++ {
		previous := batch.Series[index-1]
		current := batch.Series[index]
		if previous.PlanRef.StrategyID == current.PlanRef.StrategyID && previous.DimensionIdentityDigest > current.DimensionIdentityDigest {
			t.Fatalf("dimension order = %#v", batch.Series)
		}
	}
	for _, series := range batch.Series {
		for index := 1; index < len(series.Records); index++ {
			if series.Records[index-1].SourceTime > series.Records[index].SourceTime {
				t.Fatalf("record order = %#v", series.Records)
			}
		}
		want := FactResultNormal
		if series.PlanRef.StrategyID == "1001" {
			want = FactResultAnomalous
		}
		for _, record := range series.Records {
			if record.LevelFacts[0].Result != want {
				t.Fatalf("plan %s fact = %#v", series.PlanRef.StrategyID, record.LevelFacts[0])
			}
		}
	}
}

func TestEvaluatorRejectsBudgetContextAndInternalFailuresWithoutPartialBatch(t *testing.T) {
	envelope := fixtureEnvelope(t, []contract.EvaluationPlanV2{fixturePlan("1001", []contract.LevelIRV2{
		fixtureLevel(1, 1, contract.LevelConnectorAND, fixtureThresholdAlgorithm("GT", "50", "percent", "")),
	})}, []fixtureRecord{
		{host: "host", sourceTime: 100, value: json.RawMessage(`60`)},
		{host: "host", sourceTime: 160, value: json.RawMessage(`60`)},
	}, contract.QueryCompletenessFull)
	input, executions, digest := fixtureExecutions(t, envelope)

	limits := generousLimits()
	limits.MaxSelectedRecordsPerPlan = 1
	batch, err := newTestEvaluator(t).Evaluate(context.Background(), EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: limits,
	})
	var budget *BudgetError
	if !errors.As(err, &budget) || !reflect.DeepEqual(batch, DetectionBatch{}) {
		t.Fatalf("budget Evaluate() = (%#v, %v)", batch, err)
	}

	batch, err = newTestEvaluator(t).Evaluate(context.Background(), EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: strings.Repeat("0", 64), Plans: executions, Limits: generousLimits(),
	})
	var internal *InternalError
	if !errors.As(err, &internal) || !reflect.DeepEqual(batch, DetectionBatch{}) {
		t.Fatalf("digest Evaluate() = (%#v, %v)", batch, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	batch, err = newTestEvaluator(t).Evaluate(ctx, EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	})
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(batch, DetectionBatch{}) {
		t.Fatalf("canceled Evaluate() = (%#v, %v)", batch, err)
	}

	registry, err := NewRegistry(panicDetector{})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewEvaluator(registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	batch, err = evaluator.Evaluate(context.Background(), EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	})
	if !errors.As(err, &internal) || !reflect.DeepEqual(batch, DetectionBatch{}) {
		t.Fatalf("panic Evaluate() = (%#v, %v)", batch, err)
	}

	registry, err = NewRegistry(invalidFactDetector{})
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err = NewEvaluator(registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	batch, err = evaluator.Evaluate(context.Background(), EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	})
	if !errors.As(err, &internal) || !reflect.DeepEqual(batch, DetectionBatch{}) {
		t.Fatalf("invalid fact Evaluate() = (%#v, %v)", batch, err)
	}
}

func TestEvaluatorAdmitsWholeMessageBeforeRunningAnyDetector(t *testing.T) {
	plans := []contract.EvaluationPlanV2{
		fixturePlan("1001", []contract.LevelIRV2{fixtureLevel(1, 1, contract.LevelConnectorAND,
			fixtureThresholdAlgorithm("GT", "50", "percent", ""))}),
		fixturePlan("1002", []contract.LevelIRV2{fixtureLevel(1, 1, contract.LevelConnectorAND,
			fixtureThresholdAlgorithm("GT", "50", "percent", ""))}),
	}
	envelope := fixtureEnvelope(t, plans, []fixtureRecord{
		{host: "hot", sourceTime: 100, value: json.RawMessage(`60`)},
		{host: "hot", sourceTime: 160, value: json.RawMessage(`60`)},
	}, contract.QueryCompletenessFull)
	firstOnly := []contract.SelectorRangeV2{{Start: 0, End: 1}}
	both := []contract.SelectorRangeV2{{Start: 0, End: 2}}
	envelope.Selectors = []contract.PlanSelectorV2{
		{PlanOrdinal: 0, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &firstOnly}},
		{PlanOrdinal: 1, Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &both}},
	}
	input, executions, digest := fixtureExecutions(t, envelope)
	detector := &countingThresholdDetector{}
	registry, err := NewRegistry(detector)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewEvaluator(registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	limits := generousLimits()
	limits.MaxRecordsPerSeries = 1

	batch, err := evaluator.Evaluate(context.Background(), EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: limits,
	})
	var budget *BudgetError
	if !errors.As(err, &budget) || !reflect.DeepEqual(batch, DetectionBatch{}) {
		t.Fatalf("Evaluate() = (%#v, %v), want empty budget failure", batch, err)
	}
	if calls := detector.calls.Load(); calls != 0 {
		t.Fatalf("detector calls = %d, want whole-message admission before the first call", calls)
	}
}

func TestEvaluatorBudgetErrorsHaveStableTypedScope(t *testing.T) {
	plans := []contract.EvaluationPlanV2{
		fixturePlan("1001", []contract.LevelIRV2{fixtureLevel(1, 1, contract.LevelConnectorAND,
			fixtureThresholdAlgorithm("GT", "50", "percent", ""))}),
		fixturePlan("1002", []contract.LevelIRV2{fixtureLevel(1, 1, contract.LevelConnectorAND,
			fixtureThresholdAlgorithm("GT", "50", "percent", ""))}),
	}
	tests := []struct {
		name           string
		records        []fixtureRecord
		setLimit       func(*ExecutionLimits)
		wantScope      BudgetScope
		wantPlanID     string
		wantReasonCode string
		wantBudget     string
		wantLimit      uint64
		wantActual     uint64
		wantError      string
	}{
		{
			name: "message plan count", records: twoSameSeriesRecords(),
			setLimit:  func(limits *ExecutionLimits) { limits.MaxPlans = 1 },
			wantScope: BudgetScopeMessage, wantReasonCode: contract.ReasonMessageBudgetExceeded,
			wantBudget: "plans", wantLimit: 1, wantActual: 2,
			wantError: "alarmd detect: budget exceeded: scope=MESSAGE budget=plans actual=2 limit=1 reason=MESSAGE_BUDGET_EXCEEDED",
		},
		{
			name: "message cumulative level facts", records: twoSameSeriesRecords(),
			setLimit:  func(limits *ExecutionLimits) { limits.MaxLevelFacts = 2 },
			wantScope: BudgetScopeMessage, wantReasonCode: contract.ReasonMessageBudgetExceeded,
			wantBudget: "level_facts", wantLimit: 2, wantActual: 4,
			wantError: "alarmd detect: budget exceeded: scope=MESSAGE budget=level_facts actual=4 limit=2 reason=MESSAGE_BUDGET_EXCEEDED",
		},
		{
			name: "message cumulative predicate evaluations", records: twoSameSeriesRecords(),
			setLimit:  func(limits *ExecutionLimits) { limits.MaxPredicateEvaluations = 2 },
			wantScope: BudgetScopeMessage, wantReasonCode: contract.ReasonMessageBudgetExceeded,
			wantBudget: "predicate_evaluations", wantLimit: 2, wantActual: 4,
			wantError: "alarmd detect: budget exceeded: scope=MESSAGE budget=predicate_evaluations actual=4 limit=2 reason=MESSAGE_BUDGET_EXCEEDED",
		},
		{
			name: "message cumulative result bytes", records: twoSameSeriesRecords(),
			setLimit: func(limits *ExecutionLimits) {
				onePlanBytes, ok := estimatePlanResultBytes(1, 2, 1, 1)
				if !ok {
					panic("test result byte estimate overflowed")
				}
				limits.MaxResultBytes = onePlanBytes
			},
			wantScope: BudgetScopeMessage, wantReasonCode: contract.ReasonMessageBudgetExceeded,
			wantBudget: "result_bytes", wantLimit: 1408, wantActual: 2816,
			wantError: "alarmd detect: budget exceeded: scope=MESSAGE budget=result_bytes actual=2816 limit=1408 reason=MESSAGE_BUDGET_EXCEEDED",
		},
		{
			name: "plan selected records", records: twoSameSeriesRecords(),
			setLimit:  func(limits *ExecutionLimits) { limits.MaxSelectedRecordsPerPlan = 1 },
			wantScope: BudgetScopePlan, wantPlanID: "1001", wantReasonCode: contract.ReasonPlanBudgetExceeded,
			wantBudget: "selected_records_per_plan", wantLimit: 1, wantActual: 2,
			wantError: "alarmd detect: budget exceeded: scope=PLAN plan_id=1001 budget=selected_records_per_plan actual=2 limit=1 reason=PLAN_BUDGET_EXCEEDED",
		},
		{
			name: "plan series", records: []fixtureRecord{
				{host: "host-a", sourceTime: 100, value: json.RawMessage(`60`)},
				{host: "host-b", sourceTime: 160, value: json.RawMessage(`60`)},
			},
			setLimit:  func(limits *ExecutionLimits) { limits.MaxSeriesPerPlan = 1 },
			wantScope: BudgetScopePlan, wantPlanID: "1001", wantReasonCode: contract.ReasonPlanBudgetExceeded,
			wantBudget: "series_per_plan", wantLimit: 1, wantActual: 2,
			wantError: "alarmd detect: budget exceeded: scope=PLAN plan_id=1001 budget=series_per_plan actual=2 limit=1 reason=PLAN_BUDGET_EXCEEDED",
		},
		{
			name: "plan records per series", records: twoSameSeriesRecords(),
			setLimit:  func(limits *ExecutionLimits) { limits.MaxRecordsPerSeries = 1 },
			wantScope: BudgetScopePlan, wantPlanID: "1001", wantReasonCode: contract.ReasonPlanBudgetExceeded,
			wantBudget: "records_per_series", wantLimit: 1, wantActual: 2,
			wantError: "alarmd detect: budget exceeded: scope=PLAN plan_id=1001 budget=records_per_series actual=2 limit=1 reason=PLAN_BUDGET_EXCEEDED",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := fixtureEnvelope(t, plans, test.records, contract.QueryCompletenessFull)
			input, executions, digest := fixtureExecutions(t, envelope)
			detector := &countingThresholdDetector{}
			registry, err := NewRegistry(detector)
			if err != nil {
				t.Fatal(err)
			}
			observations := make([]Observation, 0, 1)
			evaluator, err := NewEvaluator(registry, ObserverFunc(func(_ context.Context, observation Observation) {
				observations = append(observations, observation)
			}))
			if err != nil {
				t.Fatal(err)
			}
			limits := generousLimits()
			test.setLimit(&limits)
			batch, evaluateErr := evaluator.Evaluate(context.Background(), EvaluateRequest{
				Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: limits,
			})
			var budget *BudgetError
			if !errors.As(evaluateErr, &budget) {
				t.Fatalf("Evaluate() error = %v, want BudgetError", evaluateErr)
			}
			if !reflect.DeepEqual(batch, DetectionBatch{}) || detector.calls.Load() != 0 {
				t.Fatalf("Evaluate() batch = %#v, detector calls = %d, want empty and zero", batch, detector.calls.Load())
			}
			if budget.Scope != test.wantScope || budget.PlanID != test.wantPlanID || budget.ReasonCode != test.wantReasonCode ||
				budget.Budget != test.wantBudget || budget.Limit != test.wantLimit || budget.Actual != test.wantActual {
				t.Fatalf("BudgetError = %+v, want scope=%s plan=%s reason=%s budget=%s actual=%d limit=%d",
					budget, test.wantScope, test.wantPlanID, test.wantReasonCode, test.wantBudget, test.wantActual, test.wantLimit)
			}
			if got := budget.Error(); got != test.wantError {
				t.Fatalf("BudgetError.Error() = %q, want %q", got, test.wantError)
			}
			if len(observations) != 1 || observations[0].Result != ObservationTerminal ||
				observations[0].ReasonCode != test.wantReasonCode {
				t.Fatalf("observations = %+v, want one terminal with reason %s", observations, test.wantReasonCode)
			}

			exactLimits := generousLimits()
			setExecutionBudgetLimitForTest(t, &exactLimits, budget.Budget, budget.Actual)
			detector.calls.Store(0)
			exactBatch, exactErr := evaluator.Evaluate(context.Background(), EvaluateRequest{
				Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: exactLimits,
			})
			if exactErr != nil || exactBatch.Counts.Plans != 2 || detector.calls.Load() == 0 {
				t.Fatalf("exact boundary Evaluate() = (%#v, %v), detector calls=%d", exactBatch, exactErr, detector.calls.Load())
			}
		})
	}
}

func setExecutionBudgetLimitForTest(t testing.TB, limits *ExecutionLimits, budget string, value uint64) {
	t.Helper()
	switch budget {
	case "plans":
		limits.MaxPlans = value
	case "selected_records_per_plan":
		limits.MaxSelectedRecordsPerPlan = value
	case "series_per_plan":
		limits.MaxSeriesPerPlan = value
	case "records_per_series":
		limits.MaxRecordsPerSeries = value
	case "level_facts":
		limits.MaxLevelFacts = value
	case "predicate_evaluations":
		limits.MaxPredicateEvaluations = value
	case "result_bytes":
		limits.MaxResultBytes = value
	default:
		t.Fatalf("unknown budget %q", budget)
	}
}

func twoSameSeriesRecords() []fixtureRecord {
	return []fixtureRecord{
		{host: "hot", sourceTime: 100, value: json.RawMessage(`60`)},
		{host: "hot", sourceTime: 160, value: json.RawMessage(`60`)},
	}
}

func TestEvaluatorObservesOneBoundedAggregatePerCall(t *testing.T) {
	envelope := fixtureEnvelope(t, []contract.EvaluationPlanV2{fixturePlan("1001", []contract.LevelIRV2{
		fixtureLevel(1, 1, contract.LevelConnectorAND, fixtureThresholdAlgorithm("GT", "50", "percent", "")),
	})}, []fixtureRecord{{host: "host", sourceTime: 100, value: json.RawMessage(`60`)}}, contract.QueryCompletenessFull)
	_, executions, digest := fixtureExecutions(t, envelope)
	observations := make([]Observation, 0, 3)
	observer := ObserverFunc(func(_ context.Context, observation Observation) {
		observations = append(observations, observation)
	})
	evaluator, err := NewEvaluator(NewDefaultRegistry(), observer)
	if err != nil {
		t.Fatal(err)
	}

	batch, err := evaluator.Evaluate(context.Background(), EvaluateRequest{
		Completeness: contract.QueryCompletenessPartial, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	})
	if err != nil || len(batch.Series) != 1 {
		t.Fatalf("partial Evaluate() = (%#v, %v)", batch, err)
	}
	if len(observations) != 1 {
		t.Fatalf("partial observations = %d, want one", len(observations))
	}
	partial := observations[0]
	if partial.Stage != StageDetectCompleted || partial.Result != ObservationSuccess ||
		partial.ReasonCode != contract.ReasonQueryPartial || partial.Completeness != contract.QueryCompletenessPartial ||
		partial.Counts.Plans != 1 || partial.Counts.EvaluatedRecords != 1 || partial.Counts.AnomalousFacts != 1 || partial.Duration < 0 {
		t.Fatalf("partial observation = %+v", partial)
	}

	limits := generousLimits()
	limits.MaxResultBytes = 1
	batch, err = evaluator.Evaluate(context.Background(), EvaluateRequest{
		Completeness: contract.QueryCompletenessFull, DatasetContractDigest: digest, Plans: executions, Limits: limits,
	})
	var budget *BudgetError
	if !errors.As(err, &budget) || !reflect.DeepEqual(batch, DetectionBatch{}) {
		t.Fatalf("budget Evaluate() = (%#v, %v)", batch, err)
	}
	if len(observations) != 2 {
		t.Fatalf("budget observations = %d, want one additional aggregate", len(observations))
	}
	terminal := observations[1]
	if terminal.Stage != StageDetectCompleted || terminal.Result != ObservationTerminal ||
		terminal.ReasonCode != contract.ReasonMessageBudgetExceeded || terminal.Counts.EvaluatedRecords != 0 {
		t.Fatalf("terminal observation = %+v", terminal)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	batch, err = evaluator.Evaluate(canceled, EvaluateRequest{
		Completeness: contract.QueryCompletenessFull, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	})
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(batch, DetectionBatch{}) {
		t.Fatalf("canceled Evaluate() = (%#v, %v)", batch, err)
	}
	if len(observations) != 3 {
		t.Fatalf("failed observations = %d, want one additional aggregate", len(observations))
	}
	failed := observations[2]
	if failed.Stage != StageDetectCompleted || failed.Result != ObservationFailed ||
		failed.ReasonCode != "" || failed.Counts.EvaluatedRecords != 0 {
		t.Fatalf("failed observation = %+v", failed)
	}

	batch, err = evaluator.Evaluate(context.Background(), EvaluateRequest{
		Completeness: "user-controlled-invalid-value", DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	})
	if err == nil || !reflect.DeepEqual(batch, DetectionBatch{}) {
		t.Fatalf("invalid completeness Evaluate() = (%#v, %v)", batch, err)
	}
	if len(observations) != 4 || observations[3].Completeness != "" || observations[3].Result != ObservationFailed {
		t.Fatalf("invalid completeness observation = %+v", observations)
	}
}

func TestEvaluatorObserverPanicDoesNotChangeBusinessResult(t *testing.T) {
	envelope := fixtureEnvelope(t, []contract.EvaluationPlanV2{fixturePlan("1001", []contract.LevelIRV2{
		fixtureLevel(1, 1, contract.LevelConnectorAND, fixtureThresholdAlgorithm("GT", "50", "percent", "")),
	})}, []fixtureRecord{{host: "host", sourceTime: 100, value: json.RawMessage(`60`)}}, contract.QueryCompletenessFull)
	input, executions, digest := fixtureExecutions(t, envelope)
	baseRequest := EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	}
	budgetRequest := baseRequest
	budgetRequest.Limits.MaxResultBytes = 1
	internalRequest := baseRequest
	internalRequest.DatasetContractDigest = strings.Repeat("0", 64)
	tests := []struct {
		name      string
		request   EvaluateRequest
		errorKind string
	}{
		{name: "success", request: baseRequest},
		{name: "budget error", request: budgetRequest, errorKind: "budget"},
		{name: "internal error", request: internalRequest, errorKind: "internal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline, err := NewEvaluator(NewDefaultRegistry(), nil)
			if err != nil {
				t.Fatal(err)
			}
			withPanicObserver, err := NewEvaluator(NewDefaultRegistry(), ObserverFunc(func(context.Context, Observation) {
				panic("observer unavailable")
			}))
			if err != nil {
				t.Fatal(err)
			}
			wantBatch, wantErr := baseline.Evaluate(context.Background(), test.request)
			gotBatch, gotErr := withPanicObserver.Evaluate(context.Background(), test.request)
			if !reflect.DeepEqual(gotBatch, wantBatch) {
				t.Fatalf("batch = %#v, want %#v", gotBatch, wantBatch)
			}
			if (gotErr == nil) != (wantErr == nil) || gotErr != nil && gotErr.Error() != wantErr.Error() {
				t.Fatalf("error = %v, want %v", gotErr, wantErr)
			}
			switch test.errorKind {
			case "":
				if gotErr != nil {
					t.Fatalf("error = %v, want nil", gotErr)
				}
			case "budget":
				var budget *BudgetError
				if !errors.As(gotErr, &budget) {
					t.Fatalf("error = %v, want BudgetError", gotErr)
				}
			case "internal":
				var internal *InternalError
				if !errors.As(gotErr, &internal) {
					t.Fatalf("error = %v, want InternalError", gotErr)
				}
			}
		})
	}
}

func TestEvaluatorAcceptsExactResultBudgetAndRejectsOneByteLessBeforeDetect(t *testing.T) {
	envelope := fixtureEnvelope(t, []contract.EvaluationPlanV2{fixturePlan("1001", []contract.LevelIRV2{
		fixtureLevel(1, 1, contract.LevelConnectorAND, fixtureThresholdAlgorithm("GT", "50", "percent", "")),
	})}, []fixtureRecord{
		{host: "hot", sourceTime: 100, value: json.RawMessage(`60`)},
		{host: "hot", sourceTime: 160, value: json.RawMessage(`60`)},
	}, contract.QueryCompletenessFull)
	input, executions, digest := fixtureExecutions(t, envelope)
	detector := &countingThresholdDetector{}
	registry, err := NewRegistry(detector)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewEvaluator(registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	exact, ok := estimatePlanResultBytes(1, 2, 1, 1)
	if !ok {
		t.Fatal("exact result budget overflowed")
	}
	limits := generousLimits()
	limits.MaxResultBytes = exact
	batch, err := evaluator.Evaluate(context.Background(), EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: limits,
	})
	if err != nil || len(batch.Series) != 1 || batch.Counts.EstimatedResultBytes != exact {
		t.Fatalf("exact budget Evaluate() = (%#v, %v)", batch, err)
	}
	detector.calls.Store(0)
	limits.MaxResultBytes = exact - 1
	batch, err = evaluator.Evaluate(context.Background(), EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: limits,
	})
	var budget *BudgetError
	if !errors.As(err, &budget) || !reflect.DeepEqual(batch, DetectionBatch{}) || detector.calls.Load() != 0 {
		t.Fatalf("under budget Evaluate() = (%#v, %v), detector calls=%d", batch, err, detector.calls.Load())
	}
}

func TestRegistryRejectsInvalidAndDuplicateDetectors(t *testing.T) {
	if _, err := NewRegistry(nil); err == nil {
		t.Fatal("NewRegistry() accepted nil detector")
	}
	if _, err := NewRegistry(thresholdDetector{}, thresholdDetector{}); err == nil {
		t.Fatal("NewRegistry() accepted duplicate detector")
	}
	if _, err := NewEvaluator(nil, nil); err == nil {
		t.Fatal("NewEvaluator() accepted nil registry")
	}
}

func TestEvaluatorIsDeterministicAndConcurrentSafe(t *testing.T) {
	envelope := fixtureEnvelope(t, []contract.EvaluationPlanV2{fixturePlan("1001", []contract.LevelIRV2{
		fixtureLevel(1, 20, contract.LevelConnectorAND, fixtureThresholdAlgorithm("LT", "70", "percent", "")),
		fixtureLevel(5, 1, contract.LevelConnectorAND, fixtureThresholdAlgorithm("GTE", "50", "percent", "")),
	})}, []fixtureRecord{
		{host: "host", sourceTime: 160, value: json.RawMessage(`60`)},
		{host: "host", sourceTime: 100, value: json.RawMessage(`50`)},
	}, contract.QueryCompletenessFull)
	input, executions, digest := fixtureExecutions(t, envelope)
	request := EvaluateRequest{Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits()}
	evaluator := newTestEvaluator(t)
	want, err := evaluator.Evaluate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errorsChannel := make(chan error, 16)
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			got, evaluateErr := evaluator.Evaluate(context.Background(), request)
			if evaluateErr != nil {
				errorsChannel <- evaluateErr
				return
			}
			if !reflect.DeepEqual(got, want) {
				errorsChannel <- errors.New("non-deterministic result")
			}
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Error(err)
	}
}

type panicDetector struct{}

func (panicDetector) Key() DetectorKey {
	return DetectorKey{Kind: strategy.DetectorKindThreshold, Version: 1}
}

func (panicDetector) Evaluate(context.Context, strategy.DetectorSpec, strategy.NormalizedNumber) (AlgorithmFact, error) {
	panic("test panic")
}

type invalidFactDetector struct{}

func (invalidFactDetector) Key() DetectorKey {
	return DetectorKey{Kind: strategy.DetectorKindThreshold, Version: 1}
}

func (invalidFactDetector) Evaluate(context.Context, strategy.DetectorSpec, strategy.NormalizedNumber) (AlgorithmFact, error) {
	return AlgorithmFact{Matched: true, MatchedGroup: -1}, nil
}

type countingThresholdDetector struct {
	calls atomic.Uint64
}

func (*countingThresholdDetector) Key() DetectorKey {
	return DetectorKey{Kind: strategy.DetectorKindThreshold, Version: 1}
}

func (detector *countingThresholdDetector) Evaluate(
	ctx context.Context,
	spec strategy.DetectorSpec,
	value strategy.NormalizedNumber,
) (AlgorithmFact, error) {
	detector.calls.Add(1)
	return (thresholdDetector{}).Evaluate(ctx, spec, value)
}

type fixtureRecord struct {
	host       string
	sourceTime int64
	value      json.RawMessage
	absent     bool
}

type thresholdTestCondition struct {
	operator  string
	threshold string
}

type thresholdTestGroup []thresholdTestCondition

func fixturePlan(strategyID string, levels []contract.LevelIRV2) contract.EvaluationPlanV2 {
	strategyRef := contract.StrategyRefV2{TenantID: "default", StrategyID: strategyID, Revision: "strategy-r1"}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	strategyIR := contract.StrategyIRV2{
		Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2, Minor: 0}, RequiredFeatures: []string{}, StrategyRef: strategyRef,
		ExecutionSemantics: contract.ExecutionSemanticsV2{
			EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60,
			EvaluationInterval: 60, LatenessTolerance: 120,
		},
		InputProjection: projection, Levels: levels,
	}
	return contract.EvaluationPlanV2{PlanID: strategyID, StrategyRef: strategyRef, InputProjection: projection, StrategyIR: strategyIR}
}

func fixtureLevel(id, priority uint32, connector string, algorithms ...contract.AlgorithmIRV2) contract.LevelIRV2 {
	return contract.LevelIRV2{
		Definition: contract.LevelDefinitionV2{LevelID: id, Priority: priority}, Connector: connector,
		DetectPlan:   contract.DetectPlanV2{Algorithms: algorithms},
		TriggerPlan:  contract.TypedPlanV1{Type: strategy.TriggerPlanTypeNOfM, Version: 1, Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
		RecoveryPlan: contract.TypedPlanV1{Type: strategy.RecoveryPlanTypeContinuousTriggerMiss, Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
	}
}

func fixtureThresholdAlgorithm(operator, threshold, dataUnit, prefix string) contract.AlgorithmIRV2 {
	return fixtureThresholdAlgorithmFor("value", operator, threshold, dataUnit, prefix)
}

func fixtureThresholdAlgorithmFor(valueField, operator, threshold, dataUnit, prefix string) contract.AlgorithmIRV2 {
	return contract.AlgorithmIRV2{
		Type: strategy.DetectorKindThreshold, Version: 1,
		Config: thresholdConfigFor(valueField, dataUnit, prefix, []thresholdTestGroup{{{operator: operator, threshold: threshold}}}),
	}
}

func thresholdConfig(dataUnit, prefix string, groups []thresholdTestGroup) json.RawMessage {
	return thresholdConfigFor("value", dataUnit, prefix, groups)
}

func thresholdConfigFor(valueField, dataUnit, prefix string, groups []thresholdTestGroup) json.RawMessage {
	type condition struct {
		Operator         string `json:"operator"`
		ThresholdDecimal string `json:"threshold_decimal"`
	}
	type group struct {
		Conditions []condition `json:"conditions"`
	}
	payloadGroups := make([]group, len(groups))
	for groupIndex, sourceGroup := range groups {
		payloadGroups[groupIndex].Conditions = make([]condition, len(sourceGroup))
		for conditionIndex, source := range sourceGroup {
			payloadGroups[groupIndex].Conditions[conditionIndex] = condition{Operator: source.operator, ThresholdDecimal: source.threshold}
		}
	}
	payload, err := json.Marshal(struct {
		ValueField          string `json:"value_field"`
		DataUnit            string `json:"data_unit"`
		ThresholdUnitPrefix string `json:"threshold_unit_prefix"`
		Precision           struct {
			DecimalPlaces uint32 `json:"decimal_places"`
			Rounding      string `json:"rounding"`
		} `json:"precision"`
		Groups []group `json:"groups"`
	}{
		ValueField: valueField, DataUnit: dataUnit, ThresholdUnitPrefix: prefix,
		Precision: struct {
			DecimalPlaces uint32 `json:"decimal_places"`
			Rounding      string `json:"rounding"`
		}{DecimalPlaces: 6, Rounding: "HALF_EVEN"}, Groups: payloadGroups,
	})
	if err != nil {
		panic(err)
	}
	return payload
}

func fixtureEnvelope(
	t testing.TB,
	plans []contract.EvaluationPlanV2,
	records []fixtureRecord,
	completeness string,
) *contract.ExecutionEnvelopeV2 {
	t.Helper()
	canonicalRecords := make([]contract.CanonicalRecordV2, len(records))
	minSourceTime := int64(1)
	maxSourceTime := int64(1)
	for index, source := range records {
		fields := []contract.DimensionFieldV2{{Name: "host", Value: mustJSONRaw(source.host)}}
		dimensionDigest, err := contract.DeriveDimensionIdentityDigestV2("default", "2", fields)
		if err != nil {
			t.Fatalf("DeriveDimensionIdentityDigestV2() error = %v", err)
		}
		recordID, err := contract.DeriveRecordIDV2(dimensionDigest, source.sourceTime)
		if err != nil {
			t.Fatalf("DeriveRecordIDV2() error = %v", err)
		}
		values := map[string]json.RawMessage{}
		if !source.absent {
			values["value"] = source.value
		}
		canonicalRecords[index] = contract.CanonicalRecordV2{
			RecordID: recordID, SourceTime: source.sourceTime, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Fields: fields, Digest: dimensionDigest},
			Values:            values, Dimensions: map[string]json.RawMessage{"host": mustJSONRaw(source.host)},
			ReceivedTime: source.sourceTime + 1,
		}
		if index == 0 || source.sourceTime < minSourceTime {
			minSourceTime = source.sourceTime
		}
		if index == 0 || source.sourceTime > maxSourceTime {
			maxSourceTime = source.sourceTime
		}
	}
	ranges := []contract.SelectorRangeV2{{Start: 0, End: uint32(len(records))}}
	selectors := make([]contract.PlanSelectorV2, len(plans))
	for index := range plans {
		selectorRanges := append([]contract.SelectorRangeV2(nil), ranges...)
		selectors[index] = contract.PlanSelectorV2{
			PlanOrdinal: uint32(index), Selector: contract.SelectorV2{Kind: contract.SelectorKindRanges, Ranges: &selectorRanges},
		}
	}
	reason := ""
	if completeness == contract.QueryCompletenessPartial {
		reason = contract.ReasonQueryPartial
	} else if completeness == contract.QueryCompletenessUnavailable {
		reason = contract.ReasonQueryUnavailable
	}
	windowFrom := minSourceTime - 300
	if windowFrom < 1 {
		windowFrom = 1
	}
	return &contract.ExecutionEnvelopeV2{
		Schema: contract.Schema{Name: contract.ExecutionEnvelopeSchemaV2, Major: 2, Minor: 0}, RequiredFeatures: []string{},
		ExecutionID: "execution-1", MessageID: "message-1", TenantID: "default",
		QueryGroup:   contract.QueryGroupV2{Key: "query-group-1", QueryMD5: "query-md5-1", QueryRevision: "query-r1", EvaluationTime: maxSourceTime + 60},
		SourceWindow: contract.SourceWindowV2{FromTime: windowFrom, UntilTime: maxSourceTime + 60},
		QueryResult:  contract.QueryResultV2{Completeness: completeness, ReasonCode: reason},
		DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64), IdentityFields: []string{"host"},
			SourceTimeField: "time", ReceivedTimeField: "received_time",
		},
		PlanSet: contract.PlanSetV2{PlanCount: uint32(len(plans)), EvaluationPlans: plans}, Selectors: selectors, Records: canonicalRecords,
	}
}

func fixtureExecutions(t testing.TB, envelope *contract.ExecutionEnvelopeV2) (*inputv2.EvaluationInput, []PlanExecution, string) {
	t.Helper()
	payload := encodeFixtureEnvelope(t, envelope)
	decoded, err := inputv2.New(detectReaderLimits()).Decode(context.Background(), payload)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.Input == nil {
		t.Fatalf("Decode() terminal = %#v", decoded.Terminals.Items())
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), compilerLimits())
	if err != nil {
		t.Fatalf("NewCompiler() error = %v", err)
	}
	views := decoded.Input.PlanViews()
	executions := make([]PlanExecution, 0, len(views))
	for _, view := range views {
		snapshot := view.Strategy().Snapshot()
		plan := contract.EvaluationPlanV2{
			PlanID: view.PlanID(), StrategyRef: snapshot.StrategyRef, InputProjection: snapshot.InputProjection, StrategyIR: snapshot,
		}
		result, compileErr := compiler.Compile(context.Background(), strategy.CompileRequest{
			Plan: plan, DatasetContract: envelope.DatasetContract,
			StateSemantics: strategy.StateSemantics{
				StateSchemaVersion: "state-v1", CodecSemanticsVersion: "codec-v1", IdentitySchemaDigest: strings.Repeat("3", 64),
				SourceTimeSemanticsVersion: "source-time-v1", HistoryCellSemanticsVersion: "history-cell-v1",
			},
		})
		if compileErr != nil {
			t.Fatalf("Compile(%s) error = %v", view.PlanID(), compileErr)
		}
		compiled, ok := result.Plan()
		if !ok || result.PlanTerminal() != nil || len(result.LevelTerminals()) != 0 {
			t.Fatalf("Compile(%s) = plan:%v planTerminal:%#v levelTerminals:%#v", view.PlanID(), ok, result.PlanTerminal(), result.LevelTerminals())
		}
		executions = append(executions, PlanExecution{View: view, Plan: compiled})
	}
	sort.Slice(executions, func(left, right int) bool { return executions[left].View.PlanID() > executions[right].View.PlanID() })
	if len(executions) == 0 {
		return decoded.Input, nil, deriveDatasetDigest(t, envelope.DatasetContract)
	}
	return decoded.Input, executions, executions[0].Plan.DatasetContractDigest()
}

func encodeFixtureEnvelope(t testing.TB, envelope *contract.ExecutionEnvelopeV2) []byte {
	t.Helper()
	planDigest, err := contract.DerivePlanSetDigestV2(envelope.PlanSet)
	if err != nil {
		t.Fatalf("DerivePlanSetDigestV2() error = %v", err)
	}
	envelope.PlanSet.PlanSetDigest = planDigest
	payloadDigest, err := contract.DeriveExecutionEnvelopePayloadDigestV2(*envelope)
	if err != nil {
		t.Fatalf("DeriveExecutionEnvelopePayloadDigestV2() error = %v", err)
	}
	envelope.PayloadDigest = payloadDigest
	payload, err := contract.CanonicalJSONV2(envelope)
	if err != nil {
		t.Fatalf("CanonicalJSONV2() error = %v", err)
	}
	return payload
}

func deriveDatasetDigest(t testing.TB, dataset contract.DatasetContractV2) string {
	t.Helper()
	digest, err := contract.DeriveCanonicalDigestV2("strategy-dataset-contract-v1", dataset)
	if err != nil {
		t.Fatalf("derive dataset digest: %v", err)
	}
	return digest
}

func compilerLimits() strategy.Limits {
	return strategy.Limits{
		MaxPlanBytes: 1 << 20, MaxLevelsPerPlan: 32, MaxAlgorithmsPerLevel: 32, MaxGroupsPerAlgorithm: 64,
		MaxConditionsPerAlgorithm: 256, MaxASTNodesPerLevel: 4096, MaxRequiredHistoryPoints: 4096,
		MaxCompiledPlanBytes: 1 << 20, MaxCacheEntries: 128, MaxCacheBytes: 16 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "test-v1",
	}
}

func generousLimits() ExecutionLimits {
	return ExecutionLimits{
		MaxPlans: 100, MaxSelectedRecordsPerPlan: 10_000, MaxSeriesPerPlan: 10_000, MaxRecordsPerSeries: 10_000,
		MaxLevelFacts: 1_000_000, MaxPredicateEvaluations: 10_000_000, MaxResultBytes: 1 << 30,
	}
}

func newTestEvaluator(t testing.TB) *Evaluator {
	t.Helper()
	evaluator, err := NewEvaluator(NewDefaultRegistry(), nil)
	if err != nil {
		t.Fatalf("NewEvaluator() error = %v", err)
	}
	return evaluator
}

func evaluateSingleFact(
	t testing.TB,
	algorithms []contract.AlgorithmIRV2,
	connector string,
	value json.RawMessage,
	dataUnit string,
) LevelFact {
	t.Helper()
	plan := fixturePlan("1001", []contract.LevelIRV2{fixtureLevel(5, 1, connector, algorithms...)})
	plan.InputProjection.DataUnit = dataUnit
	plan.StrategyIR.InputProjection.DataUnit = dataUnit
	envelope := fixtureEnvelope(t, []contract.EvaluationPlanV2{plan}, []fixtureRecord{{host: "host", sourceTime: 100, value: value}}, contract.QueryCompletenessFull)
	input, executions, digest := fixtureExecutions(t, envelope)
	batch, err := newTestEvaluator(t).Evaluate(context.Background(), EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest, Plans: executions, Limits: generousLimits(),
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	return batch.Series[0].Records[0].LevelFacts[0]
}

func mustJSONRaw(value string) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("marshal JSON string: %v", err))
	}
	return payload
}
