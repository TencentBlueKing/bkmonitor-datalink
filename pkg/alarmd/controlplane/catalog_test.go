package controlplane_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestBuildCatalogGroupsRealLegacyShapeDataOncePlansMany(t *testing.T) {
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(payload, &documents); err != nil {
		t.Fatal(err)
	}

	planner := &recordingPlanner{facts: queryFacts(t)}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{
			{SourceID: "1001", Document: documents[0], Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
			{SourceID: "1002", Document: documents[1], Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}},
		},
		Planner: planner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if planner.calls != 2 {
		t.Fatalf("planner calls=%d, want one per source Plan", planner.calls)
	}
	if len(catalog.QueryGroups) != 1 {
		t.Fatalf("groups=%d, want 1", len(catalog.QueryGroups))
	}
	group := catalog.QueryGroups[0]
	if len(group.Plans) != 2 {
		t.Fatalf("plans=%d, want 2", len(group.Plans))
	}
	if group.QueryPlan.QueryRevision == "" || group.Identity == "" || catalog.SnapshotRevision == "" {
		t.Fatalf("missing frozen identities: %#v", catalog)
	}
	for _, plan := range group.Plans {
		if plan.Plan.StrategyRef.TenantID != "tenant-a" || plan.Identity.BusinessID != "2" {
			t.Fatalf("identity guessed or lost: %#v", plan)
		}
		if plan.Plan.StrategyIR.ExecutionSemantics.EvaluationScope != contract.EvaluationScopeSeries {
			t.Fatalf("scope=%s", plan.Plan.StrategyIR.ExecutionSemantics.EvaluationScope)
		}
		if len(plan.Plan.StrategyIR.Levels) != 1 || len(plan.Plan.StrategyIR.Levels[0].DetectPlan.Algorithms) != 1 {
			t.Fatalf("threshold plan=%#v", plan.Plan)
		}
		assertCompilesWithEvaluationCore(t, plan.Plan, group.QueryPlan.Normalization.DatasetContract)
	}
}

func TestBuildCatalogAcceptsStableEmptyActiveSet(t *testing.T) {
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{},
		Planner:    &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ObservationID == "" || catalog.SnapshotRevision == "" {
		t.Fatalf("empty catalog identities = %#v", catalog)
	}
	if catalog.QueryGroups == nil || len(catalog.QueryGroups) != 0 || catalog.Dispositions == nil || len(catalog.Dispositions) != 0 {
		t.Fatalf("empty catalog collections = %#v", catalog)
	}
}

func assertCompilesWithEvaluationCore(t *testing.T, plan contract.EvaluationPlanV2, dataset contract.DatasetContractV2) {
	t.Helper()
	_ = compileWithEvaluationCore(t, plan, dataset)
}

func compileWithEvaluationCore(t *testing.T, plan contract.EvaluationPlanV2, dataset contract.DatasetContractV2) *strategy.CompiledPlan {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: 4096, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "test-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan: plan, DatasetContract: dataset,
		StateSemantics: strategy.StateSemantics{StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1", IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1", HistoryCellSemanticsVersion: "detect-history-cell-v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("Evaluation Core rejected plan: terminal=%#v levels=%#v", result.PlanTerminal(), result.LevelTerminals())
	}
	return compiled
}

func TestBuildCatalogRejectsMissingRealTenantFact(t *testing.T) {
	document := json.RawMessage(`{"id":1,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"q","expression":"a","query_configs":[{"agg_interval":60}],"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gt","threshold":1}]]}]}],"detects":[{"level":1,"trigger_config":{"count":1,"check_window":1}}]}`)
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "1", Document: document, Identity: controlplane.SourceIdentity{BusinessID: "2", SpaceScope: "bkcc__2"}}}, Planner: &recordingPlanner{facts: queryFacts(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 0 || len(catalog.Dispositions) != 1 {
		t.Fatalf("catalog=%#v", catalog)
	}
}

func TestCatalogRevisionIsIndependentFromSourceTraversalOrder(t *testing.T) {
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(payload, &documents); err != nil {
		t.Fatal(err)
	}
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	build := func(strategies []controlplane.SourceStrategy) controlplane.Catalog {
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: strategies, Planner: &recordingPlanner{facts: queryFacts(t)}})
		if err != nil {
			t.Fatal(err)
		}
		return catalog
	}
	forward := build([]controlplane.SourceStrategy{{SourceID: "1001", Document: documents[0], Identity: identity}, {SourceID: "1002", Document: documents[1], Identity: identity}})
	reverse := build([]controlplane.SourceStrategy{{SourceID: "1002", Document: documents[1], Identity: identity}, {SourceID: "1001", Document: documents[0], Identity: identity}})
	if forward.SnapshotRevision != reverse.SnapshotRevision {
		t.Fatalf("revision changed with traversal order: %s != %s", forward.SnapshotRevision, reverse.SnapshotRevision)
	}
}

func TestG1DoesNotSilentlyDropUnsupportedLegacySemantics(t *testing.T) {
	base := `{"id":1,"bk_biz_id":2,"update_time":1,%s"items":[{"id":1,"query_md5":"q","expression":"a","query_configs":[{"agg_interval":60}],"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gt","threshold":1}]]}]}],"detects":[{"level":1,"trigger_config":{"count":1,"check_window":1%s}}]}`
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	for _, test := range []struct{ name, prefix, trigger string }{
		{name: "priority semantics", prefix: `"priority":1,"priority_group_key":"group",`, trigger: ""},
		{name: "non-default uptime", prefix: "", trigger: `,"uptime":{"time_ranges":[{"start":"09:00","end":"18:00"}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := json.RawMessage(fmt.Sprintf(base, test.prefix, test.trigger))
			catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "1", Document: document, Identity: identity}}, Planner: &recordingPlanner{facts: queryFacts(t)}})
			if err != nil {
				t.Fatal(err)
			}
			if len(catalog.QueryGroups) != 0 || len(catalog.Dispositions) != 1 {
				t.Fatalf("unsupported semantics were silently accepted: %#v", catalog)
			}
			if test.name == "non-default uptime" && catalog.Dispositions[0].Scope != "PLAN" {
				t.Fatalf("uptime scope=%s", catalog.Dispositions[0].Scope)
			}
		})
	}
}

func TestRealShapeNegativeSpaceMultiQueryAndDynamicLevelAlgorithms(t *testing.T) {
	document := json.RawMessage(`{"id":9,"bk_biz_id":-2,"update_time":9,"items":[{"id":3,"query_md5":"q","expression":"a+b","unit":"percent","query_configs":[{"agg_interval":60},{"agg_interval":30}],"algorithms":[{"level":5,"type":"Threshold","config":[{"method":"gt","threshold":80}]},{"level":5,"type":"Threshold","config":[[{"method":"lt","threshold":10}],[{"method":"gte","threshold":90}]]}]}],"detects":[{"level":5,"priority":7,"connector":"or","trigger_config":{"count":1,"check_window":2},"recovery_config":{"check_window":1}}]}`)
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "-2", SpaceScope: "bkcc__-2"}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "9", Document: document, Identity: identity}}, Planner: &recordingPlanner{facts: queryFactsFor(t, "-2", "bkcc__-2")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 {
		t.Fatalf("catalog=%#v", catalog)
	}
	plan := catalog.QueryGroups[0].Plans[0]
	level := plan.Plan.StrategyIR.Levels[0]
	if plan.Plan.StrategyIR.ExecutionSemantics.AggregationInterval != 30 || level.Definition.LevelID != 5 || level.Definition.Priority != 7 || level.Connector != contract.LevelConnectorOR || len(level.DetectPlan.Algorithms) != 2 {
		t.Fatalf("plan=%#v", plan.Plan)
	}
	if plan.PlanRevision == "" || catalog.QueryGroups[0].MembershipDigest == "" || catalog.QueryGroups[0].ScheduleRevision == "" {
		t.Fatalf("revisions=%#v", catalog.QueryGroups[0])
	}
	assertCompilesWithEvaluationCore(t, plan.Plan, catalog.QueryGroups[0].QueryPlan.Normalization.DatasetContract)
}

func TestRejectedPlanDoesNotBlockValidSibling(t *testing.T) {
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if json.Unmarshal(payload, &documents) != nil {
		t.Fatal("fixture")
	}
	bad := json.RawMessage(`{"id":99,"bk_biz_id":2,"update_time":1,"items":[{},{}]}`)
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "99", Document: bad, Identity: identity}, {SourceID: "1001", Document: documents[0], Identity: identity}}, Planner: &recordingPlanner{facts: queryFacts(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 || len(catalog.Dispositions) != 2 {
		t.Fatalf("catalog=%#v", catalog)
	}
}

func TestUnsupportedLevelDoesNotBlockSiblingLevel(t *testing.T) {
	document := json.RawMessage(`{"id":10,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"q","expression":"a","unit":"percent","query_configs":[{"agg_interval":60}],"algorithms":[{"level":1,"type":"Threshold","config":[{"method":"gt","threshold":80}]},{"level":5,"type":"TimeSeriesForecasting","config":{}}]}],"detects":[{"level":1,"priority":1,"trigger_config":{"count":1,"check_window":1}},{"level":5,"priority":5,"trigger_config":{"count":1,"check_window":1}}]}`)
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "10", Document: document, Identity: identity}}, Planner: &recordingPlanner{facts: queryFacts(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans[0].Plan.StrategyIR.Levels) != 1 {
		t.Fatalf("catalog=%#v", catalog)
	}
	found := false
	for _, item := range catalog.Dispositions {
		if item.Scope == "LEVEL" && item.LevelID == 5 && item.Disposition == controlplane.DispositionUnsupported {
			found = true
		}
	}
	if !found {
		t.Fatalf("dispositions=%#v", catalog.Dispositions)
	}
}

func TestSameLevelMultipleAlgorithmsANDAndUnusedDirtyDetect(t *testing.T) {
	document := json.RawMessage(`{"id":11,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"q","expression":"a","unit":"percent","query_configs":[{"agg_interval":60}],"algorithms":[{"level":5,"type":"Threshold","config":[{"method":"gt","threshold":80}]},{"level":5,"type":"Threshold","config":[{"method":"lt","threshold":90}]}]}],"detects":[{"level":5,"priority":9,"connector":"and","trigger_config":{"count":1,"check_window":1}},{"level":99,"trigger_config":{"count":0,"check_window":0}}]}`)
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "11", Document: document, Identity: identity}}, Planner: &recordingPlanner{facts: queryFacts(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 {
		t.Fatalf("catalog=%#v", catalog)
	}
	level := catalog.QueryGroups[0].Plans[0].Plan.StrategyIR.Levels[0]
	if level.Connector != contract.LevelConnectorAND || len(level.DetectPlan.Algorithms) != 2 {
		t.Fatalf("level=%#v", level)
	}
}

func TestLegacyLevelPriorityDistinguishesMissingFromExplicitZero(t *testing.T) {
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	build := func(t *testing.T, detect string) controlplane.Catalog {
		t.Helper()
		document := json.RawMessage(fmt.Sprintf(`{"id":11,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"q","expression":"a","unit":"percent","query_configs":[{"agg_interval":60}],"algorithms":[{"level":1,"type":"Threshold","config":[{"method":"gt","threshold":80}]}]}],"detects":[%s]}`, detect))
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "11", Document: document, Identity: identity}}, Planner: &recordingPlanner{facts: queryFacts(t)}})
		if err != nil {
			t.Fatal(err)
		}
		return catalog
	}

	missing := build(t, `{"level":1,"trigger_config":{"count":1,"check_window":1}}`)
	if got := missing.QueryGroups[0].Plans[0].Plan.StrategyIR.Levels[0].Definition.Priority; got != 1 {
		t.Fatalf("missing priority compatibility mapping=%d, want 1", got)
	}

	explicitZero := build(t, `{"level":1,"priority":0,"trigger_config":{"count":1,"check_window":1}}`)
	if len(explicitZero.QueryGroups) != 0 || len(explicitZero.Dispositions) != 1 || explicitZero.Dispositions[0].Reason != "LEVEL_PRIORITY_INVALID" {
		t.Fatalf("explicit zero priority was silently rewritten: %#v", explicitZero)
	}
}

func TestLegacyRecoveryPresenceMatchesPythonContract(t *testing.T) {
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	build := func(t *testing.T, recovery string) controlplane.Catalog {
		t.Helper()
		document := json.RawMessage(fmt.Sprintf(`{"id":11,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"q","expression":"a","unit":"percent","query_configs":[{"agg_interval":60}],"algorithms":[{"level":1,"type":"Threshold","config":[{"method":"gt","threshold":80}]}]}],"detects":[{"level":1,"trigger_config":{"count":1,"check_window":1}%s}]}`, recovery))
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "11", Document: document, Identity: identity}}, Planner: &recordingPlanner{facts: queryFacts(t)}})
		if err != nil {
			t.Fatal(err)
		}
		return catalog
	}

	disabled := build(t, `,"recovery_config":{}`)
	if len(disabled.QueryGroups) != 1 {
		t.Fatalf("empty recovery config should disable recovery: %#v", disabled)
	}
	var disabledConfig struct {
		Enabled            bool   `json:"enabled"`
		ConsecutiveWindows uint32 `json:"consecutive_windows"`
	}
	if err := json.Unmarshal(disabled.QueryGroups[0].Plans[0].Plan.StrategyIR.Levels[0].RecoveryPlan.Config, &disabledConfig); err != nil {
		t.Fatal(err)
	}
	if disabledConfig.Enabled || disabledConfig.ConsecutiveWindows != 0 {
		t.Fatalf("disabled recovery=%+v", disabledConfig)
	}

	invalid := build(t, `,"recovery_config":{"check_window":0}`)
	if len(invalid.QueryGroups) != 0 || len(invalid.Dispositions) != 1 || invalid.Dispositions[0].Reason != "RECOVERY_CONFIG_INVALID" {
		t.Fatalf("non-empty zero recovery was silently disabled: %#v", invalid)
	}
}

func TestMembershipPlanAndScheduleRevisionsAreIndependent(t *testing.T) {
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if json.Unmarshal(payload, &documents) != nil {
		t.Fatal("fixture")
	}
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	build := func(document json.RawMessage) controlplane.QueryGroup {
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "1001", Document: document, Identity: identity}}, Planner: &recordingPlanner{facts: queryFacts(t)}})
		if err != nil {
			t.Fatal(err)
		}
		return catalog.QueryGroups[0]
	}
	base := build(documents[0])
	threshold := build(json.RawMessage(strings.Replace(string(documents[0]), `"threshold":80`, `"threshold":81`, 1)))
	schedule := build(json.RawMessage(strings.ReplaceAll(string(documents[0]), `"agg_interval":60`, `"agg_interval":30`)))
	if base.MembershipDigest != threshold.MembershipDigest || base.MembershipDigest != schedule.MembershipDigest {
		t.Fatal("membership changed without member change")
	}
	if base.Plans[0].PlanRevision == threshold.Plans[0].PlanRevision {
		t.Fatal("plan revision ignored threshold change")
	}
	if base.ScheduleRevision == schedule.ScheduleRevision {
		t.Fatal("QG schedule revision ignored cadence change")
	}
}

func TestLegacyZeroUpdateTimeUsesContentRevision(t *testing.T) {
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	build := func(t *testing.T, threshold int) contract.StrategyRefV2 {
		t.Helper()
		document := json.RawMessage(fmt.Sprintf(`{"id":11,"bk_biz_id":2,"update_time":0,"items":[{"id":1,"query_md5":"q","expression":"a","unit":"percent","query_configs":[{"agg_interval":60}],"algorithms":[{"level":1,"type":"Threshold","config":[{"method":"gt","threshold":%d}]}]}],"detects":[{"level":1,"trigger_config":{"count":1,"check_window":1}}]}`, threshold))
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "11", Document: document, Identity: identity}}, Planner: &recordingPlanner{facts: queryFacts(t)}})
		if err != nil {
			t.Fatal(err)
		}
		return catalog.QueryGroups[0].Plans[0].Plan.StrategyRef
	}
	first := build(t, 80)
	second := build(t, 81)
	if first.Revision == "" || first.Revision == "0" || first.Revision == second.Revision {
		t.Fatalf("content revisions were not derived independently: first=%q second=%q", first.Revision, second.Revision)
	}
}

func TestCatalogPassesItemExpressionFunctionsToPrimaryQueryCompiler(t *testing.T) {
	document := json.RawMessage(`{"id":12,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"q","expression":"a","functions":[{"id":"abs","params":[]}],"query_configs":[{"agg_interval":60}],"algorithms":[{"level":1,"type":"Threshold","config":[{"method":"gt","threshold":80}]}]}],"detects":[{"level":1,"trigger_config":{"count":1,"check_window":1}}]}`)
	planner := &recordingPlanner{facts: queryFacts(t)}
	_, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "12", Document: document,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		Planner: planner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(planner.lastSource.Functions) != 1 || string(planner.lastSource.Functions[0]) != `{"id":"abs","params":[]}` {
		t.Fatalf("primary query source=%#v", planner.lastSource)
	}
}

func TestBuildCatalogRejectsMultipleItemsWithoutSilentlySelectingFirst(t *testing.T) {
	document := json.RawMessage(`{"id":12,"bk_biz_id":2,"update_time":1,"items":[{"id":1,"query_md5":"q","expression":"a","query_configs":[{"agg_interval":60}],"algorithms":[{"level":1,"type":"Threshold","config":[{"method":"gt","threshold":80}]}]},{"id":2,"query_md5":"q2","expression":"b","query_configs":[{"agg_interval":60}],"algorithms":[{"level":1,"type":"Threshold","config":[{"method":"gt","threshold":90}]}]}],"detects":[{"level":1,"trigger_config":{"count":1,"check_window":1}}]}`)
	planner := &recordingPlanner{facts: queryFacts(t)}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "12", Document: document,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		Planner: planner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if planner.calls != 0 || len(catalog.QueryGroups) != 0 || len(catalog.Dispositions) != 1 {
		t.Fatalf("multiple Items were partially compiled: catalog=%#v planner calls=%d", catalog, planner.calls)
	}
	disposition := catalog.Dispositions[0]
	if disposition.Scope != "PLAN" || disposition.Disposition != controlplane.DispositionUnsupported || disposition.Reason != "UNSUPPORTED_MULTI_ITEM_STRATEGY" {
		t.Fatalf("multiple Item disposition=%#v", disposition)
	}
}

func TestBuildCatalogAbsentSourceGetsOneGraceCycleBeforeRemoval(t *testing.T) {
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(payload, &documents); err != nil {
		t.Fatal(err)
	}
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	both := []controlplane.SourceStrategy{
		{SourceID: "1001", Document: documents[0], Identity: identity},
		{SourceID: "1002", Document: documents[1], Identity: identity},
	}
	onlyFirst := both[:1]
	previous, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: both, Planner: &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	lastGood := &controlplane.PublishedSnapshot{
		Publication: controlplane.SnapshotPublicationRef{SnapshotRevision: previous.SnapshotRevision, PublicationEpoch: 1},
		QueryGroups: previous.QueryGroups,
	}
	pendingRemoval := controlplane.ObjectDisposition{SourceID: "1002", Scope: "STRATEGY",
		Disposition: controlplane.DispositionPendingRemoval, Reason: "REMOVED_FROM_ACTIVE_SET"}
	removed := controlplane.ObjectDisposition{SourceID: "1002", Scope: "STRATEGY",
		Disposition: controlplane.DispositionRemoved, Reason: "ABSENT_FROM_ACTIVE_SET"}
	sourceIncomplete := controlplane.ObjectDisposition{SourceID: "1002", Scope: "STRATEGY",
		Disposition: controlplane.DispositionSourceIncomplete, Reason: "SOURCE_READ_INCOMPLETE"}
	for _, test := range []struct {
		name         string
		strategies   []controlplane.SourceStrategy
		previous     []controlplane.ObjectDisposition
		wantPlans    []string
		wantStrategy *controlplane.ObjectDisposition
	}{
		{
			name: "absent once is retained with PENDING_REMOVAL", strategies: onlyFirst,
			previous: previous.Dispositions, wantPlans: []string{"1001", "1002"}, wantStrategy: &pendingRemoval,
		},
		{
			name: "absent without any audit history is retained with PENDING_REMOVAL", strategies: onlyFirst,
			previous: nil, wantPlans: []string{"1001", "1002"}, wantStrategy: &pendingRemoval,
		},
		{
			name: "absent twice leaves the Catalog with REMOVED", strategies: onlyFirst,
			previous:  append(append([]controlplane.ObjectDisposition(nil), previous.Dispositions...), pendingRemoval),
			wantPlans: []string{"1001"}, wantStrategy: &removed,
		},
		{
			name: "present again after PENDING_REMOVAL is accepted without a removal fact", strategies: both,
			previous:  append(append([]controlplane.ObjectDisposition(nil), previous.Dispositions...), pendingRemoval),
			wantPlans: []string{"1001", "1002"},
		},
		{
			name:       "SOURCE_INCOMPLETE still retains even after PENDING_REMOVAL",
			strategies: []controlplane.SourceStrategy{both[0], {SourceID: "1002", Identity: identity, SourceDisposition: &sourceIncomplete}},
			previous:   append(append([]controlplane.ObjectDisposition(nil), previous.Dispositions...), pendingRemoval),
			wantPlans:  []string{"1001", "1002"}, wantStrategy: &sourceIncomplete,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
				Strategies: test.strategies, Planner: &recordingPlanner{facts: queryFacts(t)},
				LastGood: lastGood, PreviousDispositions: test.previous,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := catalogStrategyIDs(catalog); !reflect.DeepEqual(got, test.wantPlans) {
				t.Fatalf("catalog plans=%v, want %v", got, test.wantPlans)
			}
			var strategyDispositions []controlplane.ObjectDisposition
			for _, disposition := range catalog.Dispositions {
				if disposition.Scope == "STRATEGY" {
					strategyDispositions = append(strategyDispositions, disposition)
				}
			}
			if test.wantStrategy == nil {
				if len(strategyDispositions) != 0 {
					t.Fatalf("strategy dispositions=%#v, want none", strategyDispositions)
				}
				return
			}
			if len(strategyDispositions) != 1 || strategyDispositions[0] != *test.wantStrategy {
				t.Fatalf("strategy dispositions=%#v, want %#v", strategyDispositions, *test.wantStrategy)
			}
		})
	}
}

func catalogStrategyIDs(catalog controlplane.Catalog) []string {
	ids := make([]string, 0)
	for _, group := range catalog.QueryGroups {
		for _, plan := range group.Plans {
			ids = append(ids, plan.Identity.StrategyID)
		}
	}
	sort.Strings(ids)
	return ids
}

type recordingPlanner struct {
	facts      execution.QueryPlanFacts
	calls      int
	lastSource controlplane.PrimaryQuerySource
}

func (p *recordingPlanner) CompilePrimaryQuery(_ context.Context, source controlplane.PrimaryQuerySource) (execution.QueryPlanFacts, error) {
	p.calls++
	p.lastSource = source
	return p.facts, nil
}

func queryFacts(t *testing.T) execution.QueryPlanFacts {
	return queryFactsFor(t, "2", "bkcc__2")
}
func queryFactsFor(t *testing.T, business, space string) execution.QueryPlanFacts {
	t.Helper()
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-primary", TenantID: "tenant-a", BusinessID: business, SpaceScope: space,
		QueryList:   []execution.QueryClause{{DataSource: "bk_monitor", Driver: "influxdb", TableID: "system.cpu", FieldName: "usage", TimeField: "time", ReferenceName: "a", Functions: []execution.QueryFunction{{Method: "default", Position: 0}}, TimeAggregation: execution.QueryFunction{Method: "avg", Position: 0}}},
		MetricMerge: "a", StepMillis: 60000, AlignmentMillis: 60000,
		Normalization: execution.DatasetNormalizationSpec{DatasetContract: contract.DatasetContractV2{SchemaDigest: "schema", NormalizationDigest: "normalization", IdentityFields: []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_time"}, SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond, SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1, ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value", ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return facts
}
