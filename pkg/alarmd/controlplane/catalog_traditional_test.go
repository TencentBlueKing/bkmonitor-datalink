package controlplane_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestTraditionalHistoryCatalogSharesDiscreteQueriesAndBoundsContinuousWindow(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	document := g4LegacyStrategyDocument(t, 300, strategy.DetectorKindAdvancedYearRound, "latency", "custom.application", []string{"service"}, map[string]any{"ceil": 20, "ceil_interval": 2, "fetch_type": "avg"})
	var source map[string]any
	if err := json.Unmarshal(document, &source); err != nil {
		t.Fatal(err)
	}
	item := source["items"].([]any)[0].(map[string]any)
	item["algorithms"] = append(item["algorithms"].([]any), map[string]any{"level": 1, "type": strategy.DetectorKindYearRoundRange, "config": map[string]any{"days": 2, "method": "gte", "ratio": 2, "shock": 0}}, map[string]any{"level": 1, "type": strategy.DetectorKindAdvancedRingRatio, "config": map[string]any{"ceil": 20, "ceil_interval": 3, "fetch_type": "last"}})
	document, err = json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: []controlplane.SourceStrategy{{SourceID: "300", Document: document, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}}, Planner: planner})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 {
		t.Fatalf("catalog %+v", catalog)
	}
	plan := catalog.QueryGroups[0].Plans[0]
	if len(plan.RequirementTemplates) != 4 || len(plan.QueryPlans) != 1 {
		t.Fatalf("duplicate history requirements: %+v", plan.RequirementTemplates)
	}
	found := false
	for _, r := range plan.RequirementTemplates {
		if string(r.DatasetName) == "ring_history_3" {
			found = true
			if len(r.PointOffsetsSeconds) != 3 || r.RelativeWindow.StartOffsetSeconds != -240 || r.RelativeWindow.EndOffsetSeconds != -60 {
				t.Fatalf("continuous history %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("continuous history absent")
	}
}
