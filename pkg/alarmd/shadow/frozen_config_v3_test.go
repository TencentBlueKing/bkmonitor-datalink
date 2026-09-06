package shadow_test

import (
	"context"
	"encoding/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/shadow"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"os"
	"reflect"
	"strings"
	"testing"
)

func queryV3FrozenInput(t *testing.T, units ...string) (execution.DuePlan, []execution.DataRequirement, map[execution.LogicalQueryRef]execution.QueryPlanFacts) {
	t.Helper()
	document, err := os.ReadFile("../contract/testdata/query-v3/strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 0 {
		var source map[string]any
		if err := json.Unmarshal(document, &source); err != nil {
			t.Fatal(err)
		}
		item := source["items"].([]any)[0].(map[string]any)
		item["unit"] = units[0]
		item["query_configs"].([]any)[0].(map[string]any)["unit"] = units[0]
		document, err = json.Marshal(source)
		if err != nil {
			t.Fatal(err)
		}
	}
	access := true
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("route-v1", "UTC", controlplane.LegacyQueryRuntimeFacts{AccessBKData: &access, BKDataCMDBLevelTables: []string{}, SystemDiskFilter: controlplane.LegacyRuntimeFilterFact{}, SystemNetworkFilter: controlplane.LegacyRuntimeFilterFact{}})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "103", Document: json.RawMessage(document), Identity: controlplane.SourceIdentity{TenantID: "default", BusinessID: "2", SpaceScope: "bkcc__2"}}}, Planner: planner})
	if err != nil || len(catalog.QueryGroups) != 1 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	frozen := catalog.QueryGroups[0].Plans[0]
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), config.Default().CompilerLimits())
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{Plan: frozen.Plan, DatasetContract: catalog.QueryGroups[0].QueryPlan.Normalization.DatasetContract, StateSemantics: strategy.StateSemantics{StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1", IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1", HistoryCellSemanticsVersion: "detect-history-cell-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := result.Plan()
	if !ok || len(result.LevelTerminals()) != 0 {
		t.Fatalf("compile %+v %+v", result.PlanTerminal(), result.LevelTerminals())
	}
	due := execution.DuePlan{Identity: frozen.Identity, CompiledPlan: plan, ScheduleSpec: frozen.ScheduleSpec, ScheduleRevision: frozen.ScheduleRevision, CompletionDeadlineUnixMilli: 180000}
	var requirements []execution.DataRequirement
	for _, template := range frozen.RequirementTemplates {
		requirements = append(requirements, template.Bind(execution.DataRequirementConsumer{Consumer: execution.ConsumerRef{Plan: due.Identity}, ConsumerDeadlineUnixMilli: 180000, DownstreamExecutionReserveMilliSec: 5000}))
	}
	// Plain Threshold uses runtime primary requirements rather than G4
	// templates. Bind the official template builder to these actual facts.
	if len(requirements) == 0 {
		facts := catalog.QueryGroups[0].QueryPlan
		template, err := execution.BuildDataRequirementTemplate(execution.DataRequirementTemplate{DatasetName: "primary", Role: execution.InputRolePrimary, ConsumerLevelID: plan.Levels()[0].Definition().LevelID, LogicalQueryRef: execution.LogicalQueryRef(facts.QueryRevision), RelativeWindow: execution.RelativeQueryWindow{StartOffsetSeconds: -int64(plan.EvaluationSemantics().QueryWindow), HalfOpen: true}, StepMillis: facts.StepMillis, AlignmentMillis: facts.AlignmentMillis, ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessFinalizedRequired, InputProjection: execution.InputProjection{ValueFields: plan.Projection().ValueFields, DimensionFields: plan.Projection().DimensionFields, IdentityFields: facts.Normalization.DatasetContract.IdentityFields}})
		if err != nil {
			t.Fatal(err)
		}
		requirements = append(requirements, template.Bind(execution.DataRequirementConsumer{Consumer: execution.ConsumerRef{Plan: due.Identity}, ConsumerDeadlineUnixMilli: 180000, DownstreamExecutionReserveMilliSec: 5000}))
		frozen.QueryPlans = map[execution.LogicalQueryRef]execution.QueryPlanFacts{template.LogicalQueryRef: facts}
	}
	return due, requirements, frozen.QueryPlans
}

func TestQueryV3ActualCatalogCompiler(t *testing.T) {
	due, req, queries := queryV3FrozenInput(t)
	before, _ := json.Marshal(queries)
	if _, err := shadow.BuildFrozenComparisonConfigV2(due, req, queries); err == nil {
		t.Fatal("V2 accepted multi-query")
	}
	c, err := shadow.BuildFrozenComparisonConfigV3(due, req, queries)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Query.Selectors) != 2 || c.Query.MetricMerge != "a / b * 100" {
		t.Fatalf("query=%+v", c.Query)
	}
	wire, _, err := contract.CanonicalComparisonConfigV3(c)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ALARMD_UPDATE_QUERY_V3_GOLDEN") == "1" {
		if err := os.WriteFile("../contract/testdata/query-v3/config.json", append(wire, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("../contract/testdata/query-v3/config.json")
	if err != nil {
		t.Fatal(err)
	}
	if string(wire)+"\n" != string(want) {
		t.Fatalf("golden drift: %s", wire)
	}
	after, _ := json.Marshal(queries)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("caller facts mutated")
	}
}
