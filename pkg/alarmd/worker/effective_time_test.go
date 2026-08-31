// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestPrepareAlwaysEffectiveTimeFactsDeduplicatesRequirementDigestWithoutDroppingConsumers(t *testing.T) {
	header, plans := effectiveTimeHeaderForTest(t)
	provider := &recordingEffectiveTimeProvider{delegate: strategy.NewStaticScheduleProvider(nil)}

	prepared, err := prepareAlwaysEffectiveTimeFactsWithProvider(context.Background(), header, provider)
	if err != nil {
		t.Fatalf("prepareAlwaysEffectiveTimeFacts() error = %v", err)
	}
	if provider.calls != 1 || !reflect.DeepEqual(provider.requestCounts, []int{1}) {
		t.Fatalf("EffectiveTime Resolve calls/requests = %d/%v, want 1/[1] for one shared requirement digest", provider.calls, provider.requestCounts)
	}
	wantConsumers := 0
	for _, due := range plans {
		wantConsumers += len(due.CompiledPlan.Levels())
	}
	if len(prepared) != wantConsumers {
		t.Fatalf("prepared consumers = %d, want %d", len(prepared), wantConsumers)
	}

	requirementDigests := make(map[string]struct{})
	factDigests := make(map[string]struct{})
	for consumer, fact := range prepared {
		if !consumer.HasLevel || fact.Status() != strategy.EffectiveTimeActive {
			t.Fatalf("prepared consumer/fact = %+v / %+v", consumer, fact)
		}
		requirementDigests[fact.RequirementDigest()] = struct{}{}
		factDigests[fact.FactDigest()] = struct{}{}
	}
	if len(requirementDigests) != 1 || len(factDigests) != 1 {
		t.Fatalf("duplicate ALWAYS requirements resolved to requirement/fact digests = %d/%d, want 1/1", len(requirementDigests), len(factDigests))
	}
}

type recordingEffectiveTimeProvider struct {
	delegate      strategy.EffectiveTimeProvider
	calls         int
	requestCounts []int
}

func (provider *recordingEffectiveTimeProvider) Resolve(
	ctx context.Context,
	requests []strategy.EffectiveTimeRequest,
) ([]strategy.EffectiveTimeFact, error) {
	provider.calls++
	provider.requestCounts = append(provider.requestCounts, len(requests))
	return provider.delegate.Resolve(ctx, requests)
}

func TestBindAlwaysEffectiveTimeFactsUsesExactSelectedSeriesPlanAndLevelTargets(t *testing.T) {
	header, plans := effectiveTimeHeaderForTest(t)
	prepared := mustPrepareAlwaysEffectiveTimeFacts(t, header)
	excluded := execution.ConsumerRef{Plan: plans[2].Identity, LevelID: 1, HasLevel: true}
	header.EffectiveTimeFacts = []execution.BoundEffectiveTimeFact{
		{Consumer: excluded, SeriesIdentity: effectiveTimeSeries("f"), Fact: prepared[excluded]},
		{Consumer: excluded, SeriesIdentity: effectiveTimeSeries("f"), Fact: prepared[excluded]},
	}
	stateItems := []execution.StatePreflightItem{
		effectiveTimeStateItem(plans[0].Identity, "c"),
		effectiveTimeStateItem(plans[0].Identity, "c"),
		effectiveTimeStateItem(plans[0].Identity, "d"),
		effectiveTimeStateItem(plans[1].Identity, "e"),
		effectiveTimeStateItem(plans[1].Identity, "e"),
	}

	bound, err := bindAlwaysEffectiveTimeFacts(header, stateItems, prepared)
	if err != nil {
		t.Fatalf("bindAlwaysEffectiveTimeFacts() error = %v", err)
	}
	want := []alwaysEffectiveTimeTarget{
		{consumer: execution.ConsumerRef{Plan: plans[0].Identity, LevelID: 5, HasLevel: true}, series: effectiveTimeSeries("c")},
		{consumer: execution.ConsumerRef{Plan: plans[0].Identity, LevelID: 5, HasLevel: true}, series: effectiveTimeSeries("d")},
		{consumer: execution.ConsumerRef{Plan: plans[0].Identity, LevelID: 9, HasLevel: true}, series: effectiveTimeSeries("c")},
		{consumer: execution.ConsumerRef{Plan: plans[0].Identity, LevelID: 9, HasLevel: true}, series: effectiveTimeSeries("d")},
		{consumer: execution.ConsumerRef{Plan: plans[1].Identity, LevelID: 3, HasLevel: true}, series: effectiveTimeSeries("e")},
	}
	assertEffectiveTimeTargets(t, bound.EffectiveTimeFacts, want)
	if len(header.EffectiveTimeFacts) != 2 {
		t.Fatalf("static header facts changed to %d entries, want original 2", len(header.EffectiveTimeFacts))
	}
}

func TestBindAlwaysEffectiveTimeFactsDoesNotAccumulateAcrossBatches(t *testing.T) {
	header, plans := effectiveTimeHeaderForTest(t)
	prepared := mustPrepareAlwaysEffectiveTimeFacts(t, header)

	first, err := bindAlwaysEffectiveTimeFacts(header, []execution.StatePreflightItem{
		effectiveTimeStateItem(plans[0].Identity, "c"),
	}, prepared)
	if err != nil {
		t.Fatalf("bind first batch: %v", err)
	}
	if len(first.EffectiveTimeFacts) != 2 {
		t.Fatalf("first batch facts = %d, want 2 Plan Levels", len(first.EffectiveTimeFacts))
	}

	second, err := bindAlwaysEffectiveTimeFacts(first, []execution.StatePreflightItem{
		effectiveTimeStateItem(plans[1].Identity, "e"),
	}, prepared)
	if err != nil {
		t.Fatalf("bind second batch: %v", err)
	}
	assertEffectiveTimeTargets(t, second.EffectiveTimeFacts, []alwaysEffectiveTimeTarget{{
		consumer: execution.ConsumerRef{Plan: plans[1].Identity, LevelID: 3, HasLevel: true},
		series:   effectiveTimeSeries("e"),
	}})
}

func TestBindAlwaysEffectiveTimeFactsReplacesEmptyAndRepeatedOldFacts(t *testing.T) {
	header, plans := effectiveTimeHeaderForTest(t)
	prepared := mustPrepareAlwaysEffectiveTimeFacts(t, header)
	want := []alwaysEffectiveTimeTarget{
		{consumer: execution.ConsumerRef{Plan: plans[0].Identity, LevelID: 5, HasLevel: true}, series: effectiveTimeSeries("c")},
		{consumer: execution.ConsumerRef{Plan: plans[0].Identity, LevelID: 9, HasLevel: true}, series: effectiveTimeSeries("c")},
	}
	oldConsumer := execution.ConsumerRef{Plan: plans[2].Identity, LevelID: 1, HasLevel: true}
	oldFact := execution.BoundEffectiveTimeFact{
		Consumer: oldConsumer, SeriesIdentity: effectiveTimeSeries("f"), Fact: prepared[oldConsumer],
	}
	for _, test := range []struct {
		name string
		old  []execution.BoundEffectiveTimeFact
	}{
		{name: "empty old facts"},
		{name: "repeated old facts", old: []execution.BoundEffectiveTimeFact{oldFact, oldFact}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := header
			candidate.EffectiveTimeFacts = test.old
			bound, err := bindAlwaysEffectiveTimeFacts(candidate, []execution.StatePreflightItem{
				effectiveTimeStateItem(plans[0].Identity, "c"),
			}, prepared)
			if err != nil {
				t.Fatalf("bindAlwaysEffectiveTimeFacts() error = %v", err)
			}
			assertEffectiveTimeTargets(t, bound.EffectiveTimeFacts, want)
		})
	}
}

func TestPrepareAlwaysEffectiveTimeFactsRejectsNonAlwaysRequirementsDeterministically(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "10"}
	header := execution.InternalExecutionHeader{
		Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{EvaluationTime: 1_788_000_000}},
		DuePlans: []execution.DuePlan{{
			Identity: identity, CompiledPlan: compileEffectiveTimePlanForTest(t, identity, []uint32{5}, true),
		}},
	}
	const want = "alarmd worker: non-ALWAYS EffectiveTime requires a resolved series fact"
	for attempt := 0; attempt < 2; attempt++ {
		_, err := prepareAlwaysEffectiveTimeFacts(context.Background(), header)
		if err == nil || err.Error() != want {
			t.Fatalf("attempt %d error = %v, want %q", attempt+1, err, want)
		}
	}
}

func effectiveTimeHeaderForTest(t *testing.T) (execution.InternalExecutionHeader, []execution.DuePlan) {
	t.Helper()
	specs := []struct {
		strategyID string
		levels     []uint32
	}{
		{strategyID: "7", levels: []uint32{5, 9}},
		{strategyID: "8", levels: []uint32{3}},
		{strategyID: "9", levels: []uint32{1}},
	}
	plans := make([]execution.DuePlan, len(specs))
	for index, spec := range specs {
		identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: spec.strategyID}
		plans[index] = execution.DuePlan{
			Identity: identity, CompiledPlan: compileEffectiveTimePlanForTest(t, identity, spec.levels, false),
		}
	}
	return execution.InternalExecutionHeader{
		Contract: execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{EvaluationTime: 1_788_000_000}},
		DuePlans: plans,
	}, plans
}

func compileEffectiveTimePlanForTest(
	t *testing.T,
	identity execution.PlanIdentity,
	levelIDs []uint32,
	nonAlways bool,
) *strategy.CompiledPlan {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: 4096, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "effective-time-worker-test-v1",
	})
	if err != nil {
		t.Fatalf("NewCompiler() error = %v", err)
	}
	ref := contract.StrategyRefV2{TenantID: identity.TenantID, StrategyID: identity.StrategyID, Revision: "strategy-v1"}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	levels := make([]contract.LevelIRV2, len(levelIDs))
	for index, levelID := range levelIDs {
		triggerConfig := json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)
		if nonAlways {
			triggerConfig = json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60,"timezone_ref":"BUSINESS_LOCAL","uptime":{"time_ranges":[{"start":"09:00","end":"17:00"}],"active_calendars":[],"calendars":[]}}`)
		}
		levels[index] = contract.LevelIRV2{
			Definition: contract.LevelDefinitionV2{LevelID: levelID, Priority: uint32(index + 1)},
			Connector:  contract.LevelConnectorAND,
			DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{
				Type: "Threshold", Version: 1,
				Config: json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"50"}]}]}`),
			}}},
			TriggerPlan:  contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: triggerConfig},
			RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
		}
	}
	plan := contract.EvaluationPlanV2{
		PlanID: identity.StrategyID, StrategyRef: ref, InputProjection: projection,
		StrategyIR: contract.StrategyIRV2{
			Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2}, StrategyRef: ref,
			InputProjection: projection,
			ExecutionSemantics: contract.ExecutionSemanticsV2{
				EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300,
				AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120,
			},
			Levels: levels,
		},
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan: plan,
		DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64),
			IdentityFields: []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_time",
		},
		StateSemantics: strategy.StateSemantics{
			StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1",
			IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1",
			HistoryCellSemanticsVersion: "detect-history-cell-v1",
		},
	})
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("Compile() terminal = %+v", result.PlanTerminal())
	}
	return compiled
}

func mustPrepareAlwaysEffectiveTimeFacts(
	t *testing.T,
	header execution.InternalExecutionHeader,
) map[execution.ConsumerRef]strategy.EffectiveTimeFact {
	t.Helper()
	prepared, err := prepareAlwaysEffectiveTimeFacts(context.Background(), header)
	if err != nil {
		t.Fatalf("prepareAlwaysEffectiveTimeFacts() error = %v", err)
	}
	return prepared
}

func effectiveTimeStateItem(plan execution.PlanIdentity, digest string) execution.StatePreflightItem {
	return execution.StatePreflightItem{Identity: execution.StateKeyIdentity{
		Plan: plan, SeriesIdentityDigest: effectiveTimeSeries(digest),
	}}
}

func effectiveTimeSeries(character string) execution.SeriesIdentityDigest {
	return execution.SeriesIdentityDigest(strings.Repeat(character, 64))
}

func assertEffectiveTimeTargets(
	t *testing.T,
	facts []execution.BoundEffectiveTimeFact,
	want []alwaysEffectiveTimeTarget,
) {
	t.Helper()
	got := make([]alwaysEffectiveTimeTarget, len(facts))
	for index, fact := range facts {
		got[index] = alwaysEffectiveTimeTarget{consumer: fact.Consumer, series: fact.SeriesIdentity}
		if fact.Fact.Status() != strategy.EffectiveTimeActive || fact.Fact.RequirementDigest() == "" || fact.Fact.FactDigest() == "" {
			t.Fatalf("fact[%d] = %+v, want complete ACTIVE fact", index, fact)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bound targets = %+v, want %+v", got, want)
	}
}
