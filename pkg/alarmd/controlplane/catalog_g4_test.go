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
		id           int64
		sourceKind   string
		detectorKind string
		metric       string
		table        string
		dimensions   []string
		config       any
		assert       func(*testing.T, controlplane.FrozenPlan)
	}{
		{101, strategy.DetectorKindSimpleRingRatio, strategy.DetectorKindSimpleRingRatio, "usage", "system.cpu", []string{"host"}, map[string]any{"floor": 50, "ceil": nil}, func(t *testing.T, plan controlplane.FrozenPlan) {
			if len(plan.RequirementTemplates) != 2 || plan.RequirementTemplates[1].DatasetName != "previous" ||
				!reflect.DeepEqual(plan.RequirementTemplates[1].PointOffsetsSeconds, []int64{60}) ||
				!reflect.DeepEqual(plan.RequirementTemplates[1].NamedPoints, []execution.NamedInputPoint{{Name: "previous", OffsetSeconds: 60}}) ||
				plan.RequirementTemplates[0].LogicalQueryRef != plan.RequirementTemplates[1].LogicalQueryRef {
				t.Fatalf("SimpleRingRatio requirements = %+v", plan.RequirementTemplates)
			}
		}},
		{102, strategy.DetectorKindOsRestart, strategy.DetectorKindOsRestart, "uptime", "system.env", []string{"bk_target_cloud_id", "bk_target_ip"}, map[string]any{}, func(t *testing.T, plan controlplane.FrozenPlan) {
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
		{103, strategy.DetectorKindProcPort, strategy.DetectorKindProcPort, "proc_exists", "system.proc_port", []string{
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
		{104, "PingUnreachable", strategy.DetectorKindThreshold, "loss_percent", "pingserver.base", []string{"bk_target_ip"}, []any{}, func(t *testing.T, plan controlplane.FrozenPlan) {
			if len(plan.RequirementTemplates) != 1 || len(plan.QueryPlans) != 1 ||
				!reflect.DeepEqual(plan.RequirementTemplates[0].InputProjection.ValueFields, []string{"value"}) {
				t.Fatalf("PingUnreachable frozen inputs = %+v / %+v", plan.RequirementTemplates, plan.QueryPlans)
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.sourceKind, func(t *testing.T) {
			document := g4LegacyStrategyDocument(t, test.id, test.sourceKind, test.metric, test.table, test.dimensions, test.config)
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
			if algorithm.Type != test.detectorKind || algorithm.Version != 1 || plan.RequirementTemplates[0].ConsumerLevelID != 1 {
				t.Fatalf("frozen plan = %+v", plan)
			}
			compiled := compileWithEvaluationCore(t, plan.Plan, catalog.QueryGroups[0].QueryPlan.Normalization.DatasetContract)
			compiledAlgorithm := compiled.Levels()[0].Algorithms()[0]
			if compiledAlgorithm.Kind() != test.detectorKind {
				t.Fatalf("compiled detector kind = %q, want %q", compiledAlgorithm.Kind(), test.detectorKind)
			}
			switch test.sourceKind {
			case strategy.DetectorKindProcPort:
				config, ok := compiledAlgorithm.ProcPortConfig()
				if !ok || config.ValueField != "value" || config.SourceMetric != "proc_exists" {
					t.Fatalf("ProcPort canonical/source value contract = %+v, ok=%v", config, ok)
				}
			case "PingUnreachable":
				var config map[string]any
				if err := json.Unmarshal(algorithm.Config, &config); err != nil {
					t.Fatal(err)
				}
				if config["source_algorithm_family"] != "ping_unreachable" || config["source_mapping_version"] != "ping-unreachable-to-threshold-v1" ||
					config["canonical_query_digest"] != string(plan.RequirementTemplates[0].LogicalQueryRef) {
					t.Fatalf("PingUnreachable source provenance = %+v", config)
				}
				provenance, ok := compiledAlgorithm.SourceProvenance()
				if !ok || provenance.SourceAlgorithmFamily != "ping_unreachable" || provenance.SourceMappingVersion != "ping-unreachable-to-threshold-v1" ||
					provenance.CanonicalQueryDigest != string(plan.RequirementTemplates[0].LogicalQueryRef) {
					t.Fatalf("PingUnreachable compiled provenance = %+v, ok=%v", provenance, ok)
				}
				detectors := compiled.Levels()[0].Detectors()
				if len(detectors) != 1 || detectors[0].Kind() != strategy.DetectorKindThreshold {
					t.Fatalf("PingUnreachable detectors = %+v", detectors)
				}
				normalizer, ok := compiled.Normalizer(detectors[0].NormalizerRef())
				if !ok {
					t.Fatal("PingUnreachable threshold normalizer is missing")
				}
				for value, want := range map[string]bool{"0": false, "1": true} {
					normalized := normalizer.Normalize(json.RawMessage(value))
					evaluated, err := detectors[0].Predicate().Evaluate(normalized.Value())
					if err != nil || !normalized.Available() || evaluated.Matched() != want {
						t.Fatalf("PingUnreachable Threshold(%s) = %+v, normalized=%+v, want %v", value, evaluated, normalized, want)
					}
				}
			}
			test.assert(t, plan)
		})
	}
}

func TestG4CatalogRejectsNonCanonicalFixedAlgorithmQueries(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		kind       string
		metric     string
		table      string
		dimensions []string
		field      string
		value      any
	}{
		{"OsRestart/table", strategy.DetectorKindOsRestart, "uptime", "system.env", []string{"bk_target_cloud_id", "bk_target_ip"}, "result_table_id", "system.cpu"},
		{"OsRestart/metric", strategy.DetectorKindOsRestart, "uptime", "system.env", []string{"bk_target_cloud_id", "bk_target_ip"}, "metric_field", "procs"},
		{"OsRestart/aggregation", strategy.DetectorKindOsRestart, "uptime", "system.env", []string{"bk_target_cloud_id", "bk_target_ip"}, "agg_method", "AVG"},
		{"OsRestart/interval", strategy.DetectorKindOsRestart, "uptime", "system.env", []string{"bk_target_cloud_id", "bk_target_ip"}, "agg_interval", 30},
		{"OsRestart/metric_id", strategy.DetectorKindOsRestart, "uptime", "system.env", []string{"bk_target_cloud_id", "bk_target_ip"}, "metric_id", "bk_monitor.proc_port"},
		{"ProcPort/table", strategy.DetectorKindProcPort, "proc_exists", "system.proc_port", []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}, "result_table_id", "system.env"},
		{"ProcPort/metric", strategy.DetectorKindProcPort, "proc_exists", "system.proc_port", []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}, "metric_field", "port_health"},
		{"ProcPort/aggregation", strategy.DetectorKindProcPort, "proc_exists", "system.proc_port", []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}, "agg_method", "AVG"},
		{"ProcPort/interval", strategy.DetectorKindProcPort, "proc_exists", "system.proc_port", []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}, "agg_interval", 30},
		{"ProcPort/metric_id", strategy.DetectorKindProcPort, "proc_exists", "system.proc_port", []string{"bk_target_cloud_id", "bk_target_ip", "display_name"}, "metric_id", "bk_monitor.os_restart"},
		{"PingUnreachable/table", controlplane.SourceAlgorithmTypePingUnreachable, "loss_percent", "pingserver.base", []string{"bk_target_cloud_id", "bk_target_ip"}, "result_table_id", "system.env"},
		{"PingUnreachable/metric", controlplane.SourceAlgorithmTypePingUnreachable, "loss_percent", "pingserver.base", []string{"bk_target_cloud_id", "bk_target_ip"}, "metric_field", "available"},
		{"PingUnreachable/aggregation", controlplane.SourceAlgorithmTypePingUnreachable, "loss_percent", "pingserver.base", []string{"bk_target_cloud_id", "bk_target_ip"}, "agg_method", "AVG"},
		{"PingUnreachable/interval", controlplane.SourceAlgorithmTypePingUnreachable, "loss_percent", "pingserver.base", []string{"bk_target_cloud_id", "bk_target_ip"}, "agg_interval", 30},
		{"PingUnreachable/source_type", controlplane.SourceAlgorithmTypePingUnreachable, "loss_percent", "pingserver.base", []string{"bk_target_cloud_id", "bk_target_ip"}, "metric_id", "bk_monitor.system.env.uptime"},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := g4LegacyStrategyDocument(t, int64(200+index), test.kind, test.metric, test.table, test.dimensions, map[string]any{})
			document = mutateG4QueryConfig(t, document, test.field, test.value)
			catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
				Strategies: []controlplane.SourceStrategy{{
					SourceID: strconv.Itoa(200 + index), Document: document,
					Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"},
				}},
				Planner: planner,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(catalog.QueryGroups) != 0 || len(catalog.Dispositions) != 1 {
				t.Fatalf("catalog = %+v", catalog)
			}
			disposition := catalog.Dispositions[0]
			if disposition.Scope != "LEVEL" || disposition.LevelID != 1 || disposition.Disposition != controlplane.DispositionConfigRejected || disposition.Reason != "ALGORITHM_QUERY_INVALID" {
				t.Fatalf("disposition = %+v", disposition)
			}
		})
	}
}

func TestG4CatalogSimpleRingRatioUsesArbitraryPrimaryQueryInterval(t *testing.T) {
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	document := g4LegacyStrategyDocument(
		t, 300, strategy.DetectorKindSimpleRingRatio, "request_latency", "custom.application", []string{"service"},
		map[string]any{"floor": 50, "ceil": nil},
	)
	document = mutateG4QueryConfig(t, document, "agg_interval", 120)
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{
			SourceID: "300", Document: document,
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
	requirements := catalog.QueryGroups[0].Plans[0].RequirementTemplates
	if len(requirements) != 2 {
		t.Fatalf("requirements = %+v", requirements)
	}
	primary, previous := requirements[0], requirements[1]
	if primary.DatasetName != "primary" || primary.RelativeWindow.StartOffsetSeconds != -120 || primary.RelativeWindow.EndOffsetSeconds != 0 ||
		previous.DatasetName != "previous" || previous.RelativeWindow.StartOffsetSeconds != -240 || previous.RelativeWindow.EndOffsetSeconds != -120 ||
		!reflect.DeepEqual(previous.PointOffsetsSeconds, []int64{120}) ||
		!reflect.DeepEqual(previous.NamedPoints, []execution.NamedInputPoint{{Name: "previous", OffsetSeconds: 120}}) ||
		primary.LogicalQueryRef != previous.LogicalQueryRef {
		t.Fatalf("SimpleRingRatio requirements = %+v", requirements)
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
	metricID := map[string]string{
		strategy.DetectorKindOsRestart:                  "bk_monitor.os_restart",
		strategy.DetectorKindProcPort:                   "bk_monitor.proc_port",
		controlplane.SourceAlgorithmTypePingUnreachable: "bk_monitor.ping-gse",
	}[kind]
	if metricID != "" {
		item["query_configs"].([]any)[0].(map[string]any)["metric_id"] = metricID
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

func mutateG4QueryConfig(t *testing.T, document json.RawMessage, field string, value any) json.RawMessage {
	t.Helper()
	var strategyDocument map[string]any
	if err := json.Unmarshal(document, &strategyDocument); err != nil {
		t.Fatal(err)
	}
	items := strategyDocument["items"].([]any)
	item := items[0].(map[string]any)
	queryConfigs := item["query_configs"].([]any)
	queryConfigs[0].(map[string]any)[field] = value
	mutated, err := json.Marshal(strategyDocument)
	if err != nil {
		t.Fatal(err)
	}
	return mutated
}
