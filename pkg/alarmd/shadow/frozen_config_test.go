package shadow_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/shadow"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func frozenInput(t *testing.T, change func(*contract.EvaluationPlanV2)) (execution.DuePlan, []execution.DataRequirement, map[execution.LogicalQueryRef]execution.QueryPlanFacts) {
	t.Helper()
	b, err := os.ReadFile("../contract/testdata/shadow-final-v2/frozen_input.json")
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Plan     contract.EvaluationPlanV2 `json:"plan"`
		Query    json.RawMessage           `json:"query_config"`
		Schedule execution.ScheduleSpec    `json:"schedule"`
	}
	if err := json.Unmarshal(b, &input); err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(&input.Plan)
	}
	access := true
	queryCompiler, err := controlplane.NewLegacyPrimaryQueryCompiler("route-v1", "UTC", controlplane.LegacyQueryRuntimeFacts{AccessBKData: &access, BKDataCMDBLevelTables: []string{}, SystemDiskFilter: controlplane.LegacyRuntimeFilterFact{}, SystemNetworkFilter: controlplane.LegacyRuntimeFilterFact{}})
	if err != nil {
		t.Fatal(err)
	}
	facts, err := queryCompiler.CompilePrimaryQuery(context.Background(), controlplane.PrimaryQuerySource{Identity: controlplane.SourceIdentity{TenantID: input.Plan.StrategyRef.TenantID, BusinessID: "2", SpaceScope: "bkcc__2"}, StrategyID: input.Plan.PlanID, ItemID: "1", QueryMD5: "source-query", Expression: "a", QueryConfigs: []json.RawMessage{input.Query}})
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), config.Default().CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{Plan: input.Plan, DatasetContract: facts.Normalization.DatasetContract, StateSemantics: strategy.StateSemantics{StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1", IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1", HistoryCellSemanticsVersion: "detect-history-cell-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := result.Plan()
	if !ok || len(result.LevelTerminals()) != 0 {
		t.Fatalf("compile terminal %+v / %+v", result.PlanTerminal(), result.LevelTerminals())
	}
	revision, err := execution.DerivePlanScheduleRevision(input.Schedule)
	if err != nil {
		t.Fatal(err)
	}
	due := execution.DuePlan{Identity: execution.PlanIdentity{TenantID: input.Plan.StrategyRef.TenantID, BusinessID: "2", StrategyID: plan.PlanRef().StrategyID}, CompiledPlan: plan, ScheduleRevision: revision, ScheduleSpec: input.Schedule, CompletionDeadlineUnixMilli: 180000}
	template, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{DatasetName: "primary", Role: execution.InputRolePrimary, ConsumerLevelID: plan.Levels()[0].Definition().LevelID, LogicalQueryRef: execution.LogicalQueryRef(facts.QueryRevision), RelativeWindow: execution.RelativeQueryWindow{StartOffsetSeconds: -int64(plan.EvaluationSemantics().QueryWindow), HalfOpen: true}, StepMillis: facts.StepMillis, AlignmentMillis: facts.AlignmentMillis, ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessFinalizedRequired, InputProjection: execution.InputProjection{ValueFields: plan.Projection().ValueFields, DimensionFields: plan.Projection().DimensionFields, IdentityFields: facts.Normalization.DatasetContract.IdentityFields}})
	if err != nil {
		t.Fatal(err)
	}
	requirement := template.Bind(execution.DataRequirementConsumer{Consumer: execution.ConsumerRef{Plan: due.Identity}, ConsumerDeadlineUnixMilli: 180000, DownstreamExecutionReserveMilliSec: 5000})
	return due, []execution.DataRequirement{requirement}, map[execution.LogicalQueryRef]execution.QueryPlanFacts{template.LogicalQueryRef: facts}
}

func TestFrozenComparisonConfigRealCompiler(t *testing.T) {
	due, requirements, queries := frozenInput(t, nil)
	before, _ := json.Marshal(struct {
		Requirements []execution.DataRequirement
		Queries      map[execution.LogicalQueryRef]execution.QueryPlanFacts
	}{requirements, queries})
	got, err := shadow.BuildFrozenComparisonConfigV2(due, requirements, queries)
	if err != nil {
		t.Fatalf("%v queries=%+v", err, queries)
	}
	if got.Selector.Table != "" || got.Selector.StepMillis != 60000 || got.Selector.QueryAlignmentMillis != 60000 || got.Schedule.AlignmentSeconds != 0 || !reflect.DeepEqual(got.SelectionOrder, []uint32{5, 1}) {
		t.Fatalf("real facts lost %+v", got)
	}
	b, digest, err := contract.CanonicalComparisonConfigV2(got)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../contract/testdata/shadow-final-v2/frozen_expected.json")
	if os.Getenv("ALARMD_UPDATE_FROZEN_GOLDEN") == "1" {
		if err := os.WriteFile("../contract/testdata/shadow-final-v2/frozen_expected.json", append(b, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		want = append(b, '\n')
		err = nil
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(b)+"\n" {
		t.Fatalf("golden differs: %s digest=%s", b, digest)
	}
	for i := 0; i < 3; i++ {
		again, err := shadow.BuildFrozenComparisonConfigV2(due, requirements, queries)
		if err != nil || !reflect.DeepEqual(got, again) {
			t.Fatal("not deterministic", err)
		}
	}
	after, _ := json.Marshal(struct {
		Requirements []execution.DataRequirement
		Queries      map[execution.LogicalQueryRef]execution.QueryPlanFacts
	}{requirements, queries})
	if string(before) != string(after) {
		t.Fatal("mutated caller frozen facts")
	}
}

func TestFrozenComparisonConfigRejectsUnrepresentedFacts(t *testing.T) {
	for _, name := range []string{"missing_query", "wrong_revision", "wrong_consumer", "missing_requirement", "wrong_schedule", "offset", "function", "expression", "query_time_field", "numeric_filter", "normalization"} {
		t.Run(name, func(t *testing.T) {
			due, requirements, queries := frozenInput(t, nil)
			key := requirements[0].LogicalQueryRef
			q := queries[key]
			switch name {
			case "missing_query":
				delete(queries, key)
			case "wrong_revision":
				q.QueryRevision = "wrong"
				queries[key] = q
			case "wrong_consumer":
				requirements[0].Consumers[0].Consumer.Plan.StrategyID = "other"
			case "missing_requirement":
				requirements = nil
			case "wrong_schedule":
				due.ScheduleRevision = "wrong"
			default:
				switch name {
				case "offset":
					q.QueryList[0].Offset = "1m"
				case "function":
					q.QueryList[0].Functions[0].Without = true
				case "expression":
					q.MetricMerge = "abs(a)"
				case "query_time_field":
					q.QueryList[0].TimeField = "other"
				case "numeric_filter":
					q.QueryList[0].Conditions = execution.QueryConditions{Fields: []execution.QueryConditionField{{Field: "host", Operator: "eq", Values: []execution.QueryScalar{{Kind: execution.QueryScalarNumber, NumberValue: "1"}}}}}
				case "normalization":
					q.Normalization.Version = "other"
				}
				q.QueryRevision = ""
				var err error
				q, err = execution.BuildQueryPlanFacts(q)
				if err != nil {
					t.Fatal(err)
				}
				delete(queries, key)
				requirements[0].LogicalQueryRef = execution.LogicalQueryRef(q.QueryRevision)
				queries[requirements[0].LogicalQueryRef] = q
			}
			if _, err := shadow.BuildFrozenComparisonConfigV2(due, requirements, queries); !errors.Is(err, shadow.ErrFrozenConfigUnsupported) {
				t.Fatalf("accepted %s: %v", name, err)
			}
		})
	}
}

func TestFrozenComparisonConfigSemanticClosure(t *testing.T) {
	digestOf := func(change func(*contract.EvaluationPlanV2)) string {
		due, req, queries := frozenInput(t, change)
		c, err := shadow.BuildFrozenComparisonConfigV2(due, req, queries)
		if err != nil {
			t.Fatal(err)
		}
		_, digest, err := contract.CanonicalComparisonConfigV2(c)
		if err != nil {
			t.Fatal(err)
		}
		return digest
	}
	base := digestOf(nil)
	for name, change := range map[string]func(*contract.EvaluationPlanV2){
		"sibling_threshold": func(p *contract.EvaluationPlanV2) {
			a := &p.StrategyIR.Levels[1].DetectPlan.Algorithms[0]
			a.Config = []byte(strings.Replace(string(a.Config), `"50"`, `"51"`, 1))
		},
		"priority": func(p *contract.EvaluationPlanV2) { p.StrategyIR.Levels[1].Definition.Priority = 30 },
		"recovery": func(p *contract.EvaluationPlanV2) {
			p.StrategyIR.Levels[1].RecoveryPlan.Config = []byte(`{"enabled":true,"consecutive_windows":2}`)
		},
		"trigger": func(p *contract.EvaluationPlanV2) {
			p.StrategyIR.Levels[1].TriggerPlan.Config = []byte(`{"window_size":2,"required_anomalies":1,"step_seconds":60}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if digestOf(change) == base {
				t.Fatal("semantic change omitted")
			}
		})
	}
	if digestOf(func(p *contract.EvaluationPlanV2) {
		p.StrategyRef.Revision = "different-native-revision"
		p.StrategyIR.StrategyRef.Revision = p.StrategyRef.Revision
	}) != base {
		t.Fatal("native revision polluted comparison identity")
	}
}

func TestFrozenComparisonConfigEmptyUnitsNEQAndComplexBoundary(t *testing.T) {
	due, req, queries := frozenInput(t, func(p *contract.EvaluationPlanV2) {
		p.InputProjection.DataUnit = ""
		p.StrategyIR.InputProjection.DataUnit = ""
		for i := range p.StrategyIR.Levels {
			a := &p.StrategyIR.Levels[i].DetectPlan.Algorithms[0]
			a.Config = []byte(strings.ReplaceAll(strings.ReplaceAll(string(a.Config), `"percent"`, `""`), `"GT"`, `"NEQ"`))
		}
	})
	c, err := shadow.BuildFrozenComparisonConfigV2(due, req, queries)
	if err != nil {
		t.Fatal(err)
	}
	if c.Numeric.SourceUnit != "" || c.Numeric.TargetUnit != "" || c.Levels[0].Detectors[0].Operator != "NE" {
		t.Fatal("empty units or NEQ mapping lost", c)
	}
	due, req, queries = frozenInput(t, func(p *contract.EvaluationPlanV2) {
		a := &p.StrategyIR.Levels[0].DetectPlan.Algorithms[0]
		var config map[string]any
		if err := json.Unmarshal(a.Config, &config); err != nil {
			t.Fatal(err)
		}
		groups := config["groups"].([]any)
		config["groups"] = append(groups, groups[0])
		a.Config, _ = json.Marshal(config)
	})
	if c, err := shadow.BuildFrozenComparisonConfigV2(due, req, queries); err != nil || c.Levels[0].Detectors[0].MappingVersion != "canonical-threshold-dnf-v2" || len(c.Levels[0].Detectors[0].SemanticConfig) == 0 {
		t.Fatal("complex predicate not preserved", err)
	}
}

func TestFrozenComparisonConfigConcurrentImmutable(t *testing.T) {
	due, req, queries := frozenInput(t, nil)
	want, err := shadow.BuildFrozenComparisonConfigV2(due, req, queries)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := shadow.BuildFrozenComparisonConfigV2(due, req, queries)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Error("concurrent projection differs", err)
			}
			got.Levels[0].Detectors[0].Threshold = "0"
			got.Projection.IdentityFields[0] = "changed"
		}()
	}
	wg.Wait()
}
