// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func compiledPlanForContractTest(t *testing.T, mutate ...func(*contract.EvaluationPlanV2)) *strategy.CompiledPlan {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: 4096, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "execution-contract-refs-test-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "strategy-v1"}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	plan := contract.EvaluationPlanV2{
		PlanID: "7", StrategyRef: ref, InputProjection: projection,
		StrategyIR: contract.StrategyIRV2{
			Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2}, StrategyRef: ref, InputProjection: projection,
			ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120},
			Levels: []contract.LevelIRV2{{
				Definition: contract.LevelDefinitionV2{LevelID: 5, Priority: 1}, Connector: contract.LevelConnectorAND,
				DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: "Threshold", Version: 1,
					Config: json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"50"}]}]}`)}}},
				TriggerPlan:  contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
				RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
			}},
		},
	}
	for _, m := range mutate {
		m(&plan)
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{Plan: plan,
		DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64), IdentityFields: []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_time"},
		StateSemantics:  strategy.StateSemantics{StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1", IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1", HistoryCellSemanticsVersion: "detect-history-cell-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("plan did not compile: %+v", result.PlanTerminal())
	}
	return compiled
}

func loadedRecordWith(plan PlanIdentity, ref RuntimeLevelContractRef) StatePreflightResult {
	return StatePreflightResult{Items: []RuntimeStateView{{
		Identity: StateKeyIdentity{Plan: plan}, Status: StateFoundReady,
		Levels:  []RuntimeLevelStateView{{LevelID: ref.LevelID, LevelStateCompatibility: ref.LevelStateCompatibility, WarmupRequirementRef: ref.WarmupRequirementRef, HistoryCompleteness: HistoryFull}},
		History: []StateHistoryPoint{{RecordID: "r1", SourceTime: 60, Levels: []StateLevelFact{{LevelID: ref.LevelID, DetectFingerprint: ref.DetectFingerprint, Result: LevelFactNormal}}}},
	}}}
}

// A Plan published with its Level contract refs is validated and written by
// them, not by what this build derives. The two are the same string on a
// steady deployment; on a rollout that moves a formula they are not, and the
// record was written by the refs the Leader published, so those are the ones
// it is held to. Deriving them here refused every loaded record of every
// affected Plan for as long as the rollout lasted (#231).
func TestARecordIsHeldToThePublishedRefsNotThisBuildsDerivation(t *testing.T) {
	compiled := compiledPlanForContractTest(t)
	derived, err := DeriveRuntimeLevelContractRefs(compiled)
	if err != nil || len(derived) != 1 {
		t.Fatalf("derive: %v %+v", err, derived)
	}
	published := derived[0]
	published.LevelStateCompatibility = strings.Repeat("a", 64)
	published.WarmupRequirementRef = strings.Repeat("b", 64)
	identity := PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	due := DuePlan{Identity: identity, CompiledPlan: compiled, LevelContractRefs: []RuntimeLevelContractRef{published}}

	contracts := levelContractsOf(due)
	if err := validateLoadedStateContracts(due, loadedRecordWith(identity, published), &contracts); err != nil {
		t.Fatalf("a record written by the published refs was refused: %v", err)
	}
	contracts = levelContractsOf(due)
	if err := validateLoadedStateContracts(due, loadedRecordWith(identity, derived[0]), &contracts); err == nil {
		t.Fatal("a record written by this build's derivation was accepted although the Leader published other refs")
	}
	refs, err := LevelContractRefsFor(due, nil)
	if err != nil || len(refs) != 1 || refs[0] != published {
		t.Fatalf("a mutation would be written with %+v (%v), want the published refs", refs, err)
	}
	// Each half of the contract is held on its own. #231 was a record whose
	// compatibility matched and whose warmup reference did not; a check that
	// read only the compatibility would have let it through.
	for name, mutate := range map[string]func(*RuntimeLevelContractRef){
		"warmup reference":    func(ref *RuntimeLevelContractRef) { ref.WarmupRequirementRef = strings.Repeat("f", 64) },
		"state compatibility": func(ref *RuntimeLevelContractRef) { ref.LevelStateCompatibility = strings.Repeat("f", 64) },
		"detect fingerprint":  func(ref *RuntimeLevelContractRef) { ref.DetectFingerprint = strings.Repeat("f", 64) },
	} {
		other := published
		mutate(&other)
		contracts = levelContractsOf(due)
		if err := validateLoadedStateContracts(due, loadedRecordWith(identity, other), &contracts); err == nil {
			t.Fatalf("a record whose %s differs from the published one was accepted", name)
		}
	}
}

// A Plan published without refs by a Leader on another formula - an object
// from before the field, read by a Worker whose derivation moved - is the
// case the published refs exist for, and until the Leader republishes it has
// to work: the Slot runs, the loaded record is not held to refs this build
// cannot vouch for, and a mutation carries the record's own refs forward
// rather than rewriting the record under a contract the build that keyed it
// would refuse.
func TestWithoutPublishedRefsAFormulaSkewTrustsTheKey(t *testing.T) {
	compiled := compiledPlanForContractTest(t)
	derived, err := DeriveRuntimeLevelContractRefs(compiled)
	if err != nil {
		t.Fatal(err)
	}
	stored := derived[0]
	stored.LevelStateCompatibility = strings.Repeat("c", 64)
	stored.WarmupRequirementRef = strings.Repeat("d", 64)
	stored.DetectFingerprint = strings.Repeat("e", 64)
	identity := PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}

	// The same record under the same build without a skew is refused: the
	// tolerance is for the skew, not for any mismatch.
	steady := DuePlan{Identity: identity, CompiledPlan: compiled}
	contracts := levelContractsOf(steady)
	var mismatch *StateContractMismatchError
	if err := validateLoadedStateContracts(steady, loadedRecordWith(identity, stored), &contracts); !errors.As(err, &mismatch) {
		t.Fatalf("without a skew a record of other refs was not refused: %v", err)
	}

	skewed := DuePlan{Identity: identity, CompiledPlan: compiled, StateGenerationFormulaSkew: true}
	contracts = levelContractsOf(skewed)
	if err := validateLoadedStateContracts(skewed, loadedRecordWith(identity, stored), &contracts); err != nil {
		t.Fatalf("under a formula skew the loaded record was held to refs this build cannot vouch for: %v", err)
	}
	// A Level the Plan does not have is still refused: that is not a formula.
	foreign := stored
	foreign.LevelID = 9
	contracts = levelContractsOf(skewed)
	if err := validateLoadedStateContracts(skewed, loadedRecordWith(identity, foreign), &contracts); err == nil {
		t.Fatal("a record naming a Level the Plan does not have was accepted under the skew")
	}
	// The mutation carries the record's refs forward, not this build's.
	loaded := loadedRecordWith(identity, stored).Items[0].Levels
	refs, err := LevelContractRefsFor(skewed, loaded)
	if err != nil || len(refs) != 1 || refs[0].LevelStateCompatibility != stored.LevelStateCompatibility || refs[0].WarmupRequirementRef != stored.WarmupRequirementRef {
		t.Fatalf("a mutation under the skew would be written with %+v (%v), want the loaded record's refs carried forward", refs, err)
	}
	// And a record the Plan has no state for yet gets this build's derivation,
	// there being nothing to carry.
	refs, err = LevelContractRefsFor(skewed, nil)
	if err != nil || len(refs) != 1 || refs[0] != derived[0] {
		t.Fatalf("a first record under the skew would be written with %+v (%v), want this build's derivation", refs, err)
	}
	// With refs published the skew changes nothing: the published refs decide.
	both := DuePlan{Identity: identity, CompiledPlan: compiled, StateGenerationFormulaSkew: true, LevelContractRefs: derived}
	contracts = levelContractsOf(both)
	if err := validateLoadedStateContracts(both, loadedRecordWith(identity, stored), &contracts); err == nil {
		t.Fatal("with refs published, a record of other refs was accepted because of the skew")
	}
}

// The no-data view is held to the no-data Level's own published refs. The
// no-data Level shares its ID with the source Level it follows and has its
// own fingerprints, so the declared Levels' refs would name the wrong Level
// under the same ID - which is how the first cut of this refused every
// no-data record as "unknown or incomplete Level".
func TestTheNoDataViewIsHeldToItsOwnPublishedRefs(t *testing.T) {
	compiled := compiledPlanForContractTest(t, func(plan *contract.EvaluationPlanV2) {
		// The no-data Level follows source Level 1; the test plan's declared
		// Level moves to 1 so the two share an ID, which is the collision the
		// swap exists for.
		plan.StrategyIR.Levels[0].Definition.LevelID = 1
		plan.NoData = &contract.NoDataConfigV1{Continuous: 1, Level: 1}
	})
	declared := []RuntimeLevelContractRef{{LevelID: 1, LevelStateCompatibility: strings.Repeat("1", 64), WarmupRequirementRef: strings.Repeat("2", 64), DetectFingerprint: strings.Repeat("3", 64)}}
	noData := []RuntimeLevelContractRef{{LevelID: 1, LevelStateCompatibility: strings.Repeat("4", 64), WarmupRequirementRef: strings.Repeat("5", 64), DetectFingerprint: strings.Repeat("6", 64)}}
	due := DuePlan{Identity: PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}, CompiledPlan: compiled, LevelContractRefs: declared, NoDataLevelContractRefs: noData}
	real, err := PlanViewFor(due, SeriesKindReal)
	if err != nil || !reflect.DeepEqual(real.LevelContractRefs, declared) {
		t.Fatalf("the real view holds %+v (%v), want the declared Levels' refs", real.LevelContractRefs, err)
	}
	if compiled.NoDataView() == nil {
		t.Fatal("the test plan has no no-data Level, so the swap under test cannot be exercised")
	}
	view, err := PlanViewFor(due, SeriesKindNoData)
	if err != nil || !reflect.DeepEqual(view.LevelContractRefs, noData) || view.NoDataLevelContractRefs != nil {
		t.Fatalf("the no-data view holds %+v / %+v (%v), want the no-data Level's refs and nothing else", view.LevelContractRefs, view.NoDataLevelContractRefs, err)
	}
}
