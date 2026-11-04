package controlplane_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// strategyFactsDocument is the first test strategy with its query config's
// metric_id and agg_condition set, and its snapshot revision when one is
// given.
func strategyFactsDocument(t *testing.T, revision string, queryConfig map[string]json.RawMessage, item ...map[string]json.RawMessage) json.RawMessage {
	t.Helper()
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []map[string]any
	if err := json.Unmarshal(payload, &documents); err != nil {
		t.Fatal(err)
	}
	document := documents[0]
	if revision != "" {
		document["strategy_revision"] = json.RawMessage(revision)
	}
	for _, fields := range item {
		for key, value := range fields {
			document["items"].([]any)[0].(map[string]any)[key] = value
		}
	}
	query := document["items"].([]any)[0].(map[string]any)["query_configs"].([]any)[0].(map[string]any)
	for key, value := range queryConfig {
		if value == nil {
			delete(query, key)
			continue
		}
		query[key] = value
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func strategyFactsPlan(t *testing.T, document json.RawMessage) controlplane.FrozenPlan {
	t.Helper()
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "1001", Document: document,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		Planner: &recordingPlanner{facts: queryFacts(t)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 {
		t.Fatalf("catalog = %+v; dispositions = %+v", catalog.QueryGroups, catalog.Dispositions)
	}
	return catalog.QueryGroups[0].Plans[0]
}

// A Plan that publishes a snapshot revision freezes the strategy facts of
// its query configs, by the platform's rules, and publishes them in the
// output context; one that does not, or whose query configs name no metric,
// has none.
func TestAPlanFreezesItsStrategyFacts(t *testing.T) {
	queryConfig := map[string]json.RawMessage{"metric_id": json.RawMessage(`"bk_monitor.system.cpu.usage"`),
		"agg_condition": json.RawMessage(`[{"key":"ip","value":["192.0.2.1"],"method":"eq","condition":"and"}]`)}
	plan := strategyFactsPlan(t, strategyFactsDocument(t, "7", queryConfig))
	// The platform's md5s of these query configs (contract's golden vectors),
	// and the md5 of ["host"], the grouping the test dataset identifies
	// series by; the strategy detects no no-data.
	want := contract.StrategyFactsV2{Schema: "v2", MetricDigest: "3815df5a36678b1d738aa6f9a1522549", ConditionMD5: "92811130e44a4e5eb434d4dd5acbfdaa",
		DimensionDigest: "f8110ed86ce585651a6e4dd7acda8018", ConditionBasis: "query"}
	if plan.Plan.StrategyFacts == nil || *plan.Plan.StrategyFacts != want {
		t.Fatalf("strategy facts = %+v, want %+v", plan.Plan.StrategyFacts, want)
	}
	if got := controlplane.BuildOutputContext(plan).StrategyFacts; got == nil || *got != want {
		t.Fatalf("output context strategy facts = %+v, want %+v", got, want)
	}
	if legacy := strategyFactsPlan(t, strategyFactsDocument(t, "", queryConfig)); legacy.Plan.StrategyFacts != nil {
		t.Fatalf("a Plan without a snapshot revision froze facts %+v", legacy.Plan.StrategyFacts)
	}
	unnamed := strategyFactsPlan(t, strategyFactsDocument(t, "7", map[string]json.RawMessage{"metric_id": nil}))
	if unnamed.Plan.StrategyFacts != nil {
		t.Fatalf("a query config without a metric_id froze facts %+v", unnamed.Plan.StrategyFacts)
	}
}

// A Plan that detects no-data freezes the digest of its no-data grouping
// beside its own.
func TestAPlanFreezesItsNoDataGrouping(t *testing.T) {
	queryConfig := map[string]json.RawMessage{"metric_id": json.RawMessage(`"bk_monitor.system.cpu.usage"`)}
	plan := strategyFactsPlan(t, strategyFactsDocument(t, "7", queryConfig, map[string]json.RawMessage{
		"no_data_config": json.RawMessage(`{"is_enabled":true,"continuous":5,"agg_dimension":["tags.host"],"level":2}`)}))
	if plan.Plan.NoData == nil {
		t.Fatalf("the Plan detects no no-data: %+v", plan.Plan)
	}
	// md5 of ["host"] for both: the tags. prefix is read away.
	if facts := plan.Plan.StrategyFacts; facts == nil || facts.DimensionDigest != "f8110ed86ce585651a6e4dd7acda8018" ||
		facts.NoDataDimensionDigest != "f8110ed86ce585651a6e4dd7acda8018" {
		t.Fatalf("strategy facts = %+v, want the threshold and no-data groupings", facts)
	}
}
