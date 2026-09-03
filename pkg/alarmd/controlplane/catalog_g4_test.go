package controlplane_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestG4CatalogFreezesFourAlgorithmQueryAndRequirementContracts(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		id         int64
		kind       string
		metric     string
		table      string
		dimensions []string
		config     any
		assert     func(*testing.T, controlplane.FrozenPlan)
	}{
		{101, strategy.DetectorKindSimpleRingRatio, "usage", "system.cpu", []string{"host"}, map[string]any{"floor": 50, "ceil": nil}, func(t *testing.T, plan controlplane.FrozenPlan) {
			if len(plan.RequirementTemplates) != 2 || plan.RequirementTemplates[1].DatasetName != "previous" ||
				!reflect.DeepEqual(plan.RequirementTemplates[1].PointOffsetsSeconds, []int64{60}) ||
				!reflect.DeepEqual(plan.RequirementTemplates[1].NamedPoints, []execution.NamedInputPoint{{Name: "previous", OffsetSeconds: 60}}) ||
				plan.RequirementTemplates[0].LogicalQueryRef != plan.RequirementTemplates[1].LogicalQueryRef {
				t.Fatalf("SimpleRingRatio requirements = %+v", plan.RequirementTemplates)
			}
		}},
		{102, strategy.DetectorKindOsRestart, "uptime", "system.env", []string{"bk_target_cloud_id", "bk_target_ip"}, map[string]any{}, func(t *testing.T, plan controlplane.FrozenPlan) {
			if len(plan.RequirementTemplates) != 2 || len(plan.QueryPlans) != 2 {
				t.Fatalf("OsRestart frozen inputs = %+v / %+v", plan.RequirementTemplates, plan.QueryPlans)
			}
			primary := plan.QueryPlans[plan.RequirementTemplates[0].LogicalQueryRef]
			history := plan.QueryPlans[plan.RequirementTemplates[1].LogicalQueryRef]
			if primary.MetricMerge != "a <= 3600" || history.MetricMerge != "a" || primary.QueryRevision == history.QueryRevision ||
				!reflect.DeepEqual(plan.RequirementTemplates[1].NamedPoints, []execution.NamedInputPoint{
					{Name: "previous", OffsetSeconds: 60}, {Name: "previous_10m", OffsetSeconds: 600},
					{Name: "previous_25m", OffsetSeconds: 1500},
				}) {
				t.Fatalf("OsRestart primary=%+v history=%+v requirements=%+v", primary, history, plan.RequirementTemplates)
			}
		}},
		{103, strategy.DetectorKindProcPort, "proc_exists", "system.proc_port", []string{
			"protocol", "listen", "nonlisten", "not_accurate_listen", "bind_ip", "bk_target_ip", "bk_target_cloud_id", "display_name",
		}, map[string]any{}, func(t *testing.T, plan controlplane.FrozenPlan) {
			projection := plan.RequirementTemplates[0].InputProjection
			if !reflect.DeepEqual(projection.ValueFields, []string{"value"}) ||
				!reflect.DeepEqual(projection.DimensionFields, []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"}) ||
				!reflect.DeepEqual(projection.IdentityFields, []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}) {
				t.Fatalf("ProcPort projection = %+v", projection)
			}
			for _, dynamic := range projection.DimensionFields {
				if containsG4String(projection.IdentityFields, dynamic) {
					t.Fatalf("dynamic field %q entered identity %v", dynamic, projection.IdentityFields)
				}
			}
		}},
		{104, strategy.DetectorKindPingUnreachable, "loss_percent", "pingserver.base", []string{"bk_target_ip"}, map[string]any{}, func(t *testing.T, plan controlplane.FrozenPlan) {
			if len(plan.RequirementTemplates) != 1 || len(plan.QueryPlans) != 1 ||
				!reflect.DeepEqual(plan.RequirementTemplates[0].InputProjection.ValueFields, []string{"value"}) {
				t.Fatalf("PingUnreachable frozen inputs = %+v / %+v", plan.RequirementTemplates, plan.QueryPlans)
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			document := g4LegacyStrategyDocument(t, test.id, test.kind, test.metric, test.table, test.dimensions, test.config)
			catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
				Strategies: []controlplane.SourceStrategy{{
					SourceID: strconv.FormatInt(test.id, 10), Document: document,
					Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
				}},
				Planner: planner,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 {
				t.Fatalf("catalog = %+v", catalog)
			}
			plan := catalog.QueryGroups[0].Plans[0]
			algorithm := plan.Plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0]
			if algorithm.Type != test.kind || algorithm.Version != 1 || plan.RequirementTemplates[0].ConsumerLevelID != 1 {
				t.Fatalf("frozen plan = %+v", plan)
			}
			compiled := compileWithEvaluationCore(t, plan.Plan, catalog.QueryGroups[0].QueryPlan.Normalization.DatasetContract)
			compiledAlgorithm := compiled.Levels()[0].Algorithms()[0]
			switch test.kind {
			case strategy.DetectorKindProcPort:
				config, ok := compiledAlgorithm.ProcPortConfig()
				if !ok || config.ValueField != "value" || config.SourceMetric != "proc_exists" {
					t.Fatalf("ProcPort canonical/source value contract = %+v, ok=%v", config, ok)
				}
			case strategy.DetectorKindPingUnreachable:
				config, ok := compiledAlgorithm.PingUnreachableConfig()
				if !ok || config.ValueField != "value" || config.SourceMetric != "loss_percent" || config.ThresholdDecimal != "1.000000" {
					t.Fatalf("PingUnreachable canonical/source value contract = %+v, ok=%v", config, ok)
				}
			}
			test.assert(t, plan)
		})
	}
}

func g4LegacyStrategyDocument(t *testing.T, id int64, kind, metric, table string, dimensions []string, config any) json.RawMessage {
	t.Helper()
	item := map[string]any{
		"id": 1, "query_md5": "query-md5", "expression": "a", "unit": "",
		"query_configs": []any{map[string]any{
			"data_source_label": "bk_monitor", "data_type_label": "time_series", "metric_field": metric,
			"alias": "a", "agg_dimension": dimensions, "agg_method": "MAX", "agg_interval": 60,
			"result_table_id": table,
		}},
		"algorithms": []any{map[string]any{"level": 1, "type": kind, "config": config}},
	}
	if kind == strategy.DetectorKindOsRestart {
		item["functions"] = []any{map[string]any{"id": "abs", "params": []any{}}}
	}
	document, err := json.Marshal(map[string]any{
		"id": id, "bk_biz_id": 2, "update_time": 1,
		"items": []any{item},
		"detects": []any{map[string]any{
			"level": 1, "priority": 1, "connector": "and",
			"trigger_config": map[string]any{"count": 1, "check_window": 1},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func containsG4String(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
