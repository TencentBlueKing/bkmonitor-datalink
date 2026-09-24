package controlplane_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"os"
	"testing"
)

func TestProductionCompilerSeparatesCadencesAndFreezesShortCompletion(t *testing.T) {
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if err = json.Unmarshal(payload, &documents); err != nil {
		t.Fatal(err)
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	var sources []controlplane.SourceStrategy
	for i, interval := range []int{10, 15, 60} {
		var document map[string]any
		if err = json.Unmarshal(documents[0], &document); err != nil {
			t.Fatal(err)
		}
		document["id"] = 1001 + i
		for _, item := range document["items"].([]any) {
			for _, query := range item.(map[string]any)["query_configs"].([]any) {
				query.(map[string]any)["agg_interval"] = interval
			}
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, controlplane.SourceStrategy{SourceID: fmt.Sprint(1001 + i), Document: encoded, Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}})
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{Strategies: sources, Planner: planner})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 3 {
		t.Fatalf("groups=%d dispositions=%+v", len(catalog.QueryGroups), catalog.Dispositions)
	}
	for _, group := range catalog.QueryGroups {
		for _, plan := range group.Plans {
			spec := plan.ScheduleSpec
			if spec.EvaluationIntervalSeconds*1000 != group.QueryPlan.StepMillis {
				t.Fatal("Schedule cadence differs from QG step")
			}
			expected := int64(0)
			if spec.EvaluationIntervalSeconds == 10 || spec.EvaluationIntervalSeconds == 15 {
				expected = 30
			}
			if spec.CompletionDeadlineOffsetSeconds != expected {
				t.Fatalf("spec=%+v", spec)
			}
		}
	}
}
