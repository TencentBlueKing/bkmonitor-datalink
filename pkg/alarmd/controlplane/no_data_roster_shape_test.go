// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

// The cases in this file compile a stored strategy document through
// BuildCatalog, the production compiler, and read what it published: the
// Plan, its no-data half, and the composition the fleet counts. The shapes
// and the expected readings come from the no-data capability decomposition
// (section 5.7, the layer-4 contract, and section 5.9, the user's ruling that
// no-data fails locally) and from decision-017 section 3.4 for target plans.
// None of the expected values is computed by the code under test.

// rosterShapeStrategy is the fixture strategy with a revision, the host
// scenario the backend's no-data scenario table dispatches on (nodata.py:68,
// constants/strategy.py:319) and the given edits to its first item.
func rosterShapeStrategy(t *testing.T, global bool, item func(map[string]any)) controlplane.SourceStrategy {
	t.Helper()
	strategy := twoThresholdStrategiesWithDelay(t, 0)[0]
	strategy.Document = editedDocument(t, strategy.Document, func(top map[string]any) {
		// A revision, so the strategy publishes the standard event: a global
		// Plan on the compatible event is refused for that reason alone, and
		// an ordinary one does not care.
		top["strategy_revision"] = 7
		top["scenario"] = "os"
		top["name"] = "no-data roster shape"
	}, item)
	strategy.Identity.GlobalBusiness = global
	return strategy
}

func compileRosterShape(t *testing.T, global bool, item func(map[string]any)) controlplane.Catalog {
	t.Helper()
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Planner: globalBusinessPlanner(t), Strategies: []controlplane.SourceStrategy{rosterShapeStrategy(t, global, item)}})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

// withNoData turns no-data detection on with the given dimensions, at the
// configuration the platform writes: enabled, a continuous count and a level.
func withNoData(item map[string]any, dimensions ...string) {
	agg := make([]any, 0, len(dimensions))
	for _, dimension := range dimensions {
		agg = append(agg, dimension)
	}
	item["no_data_config"] = map[string]any{"is_enabled": true, "continuous": 5, "level": 2, "agg_dimension": agg}
}

// legacyTarget is the stored target: one group holding one condition.
func legacyTarget(field, method string, values ...map[string]any) []any {
	list := make([]any, 0, len(values))
	for _, value := range values {
		list = append(list, value)
	}
	return []any{[]any{map[string]any{"field": field, "method": method, "value": list}}}
}

func staticHost(ip string) map[string]any {
	return map[string]any{"bk_target_ip": ip, "bk_target_cloud_id": 0}
}

// wantThresholdsCompiled says the Plan still carries the item's threshold
// level as the fixture states it: level 1, one Threshold algorithm.
func wantThresholdsCompiled(t *testing.T, plan controlplane.FrozenPlan) {
	t.Helper()
	levels := plan.Plan.StrategyIR.Levels
	if len(levels) != 1 || levels[0].Definition.LevelID != 1 || len(levels[0].DetectPlan.Algorithms) != 1 ||
		levels[0].DetectPlan.Algorithms[0].Type != "Threshold" {
		t.Fatalf("levels = %+v, want the fixture's level 1 Threshold: the thresholds went with the no-data half", levels)
	}
}

// wantAcceptedPlan says the strategy is published as an accepted Plan.
func wantAcceptedPlan(t *testing.T, catalog controlplane.Catalog) {
	t.Helper()
	wantNotWithheld(t, catalog)
	for _, disposition := range catalog.Dispositions {
		if disposition.SourceID == "1001" && disposition.Scope == "PLAN" && disposition.Disposition == controlplane.DispositionAccepted {
			return
		}
	}
	t.Fatalf("dispositions %+v hold no accepted Plan for the strategy", catalog.Dispositions)
}

// wantOnlyNoDataSuspended is the three-part reading section 5.9 item 1 asks
// for: the Plan is accepted, its threshold detection is compiled, and only its
// no-data half is off - named NO_DATA_ROSTER_UNSUPPORTED on the Plan, counted
// under SUSPENDED_ROSTER_UNSUPPORTED, and listed by strategy.
func wantOnlyNoDataSuspended(t *testing.T, catalog controlplane.Catalog) {
	t.Helper()
	wantAcceptedPlan(t, catalog)
	plan := onlyPlan(t, catalog)
	wantThresholdsCompiled(t, plan)
	if plan.Plan.NoData != nil || plan.NoDataSuspended != "NO_DATA_ROSTER_UNSUPPORTED" {
		t.Fatalf("no_data=%+v suspended=%q, want the section dropped and NO_DATA_ROSTER_UNSUPPORTED named",
			plan.Plan.NoData, plan.NoDataSuspended)
	}
	composition := controlplane.ComposeCatalog(catalog)
	want := map[nodata.RosterSource]int{"SUSPENDED_ROSTER_UNSUPPORTED": 1}
	if got := nonZeroSources(composition.NoDataPlans); !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog_no_data_plans = %v, want %v", got, want)
	}
	if len(composition.SuspendedNoDataObjects) != 1 || composition.SuspendedNoDataObjects[0].SourceID != "1001" ||
		composition.SuspendedNoDataObjects[0].Reason != "NO_DATA_ROSTER_UNSUPPORTED" ||
		composition.SuspendedNoDataObjects[0].Disposition != controlplane.DispositionAccepted {
		t.Fatalf("named suspensions = %+v, want the strategy named once, accepted, under NO_DATA_ROSTER_UNSUPPORTED",
			composition.SuspendedNoDataObjects)
	}
}

// wantNoDataAttached says the Plan is accepted with its no-data half attached
// and counted under source.
func wantNoDataAttached(t *testing.T, catalog controlplane.Catalog, source nodata.RosterSource) {
	t.Helper()
	wantAcceptedPlan(t, catalog)
	plan := onlyPlan(t, catalog)
	wantThresholdsCompiled(t, plan)
	if plan.Plan.NoData == nil || plan.NoDataSuspended != "" {
		t.Fatalf("no_data=%+v suspended=%q, want no-data attached", plan.Plan.NoData, plan.NoDataSuspended)
	}
	composition := controlplane.ComposeCatalog(catalog)
	want := map[nodata.RosterSource]int{source: 1}
	if got := nonZeroSources(composition.NoDataPlans); !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog_no_data_plans = %v, want %v", got, want)
	}
	if len(composition.SuspendedNoDataObjects) != 0 {
		t.Fatalf("a Plan with no-data attached is named as suspended: %+v", composition.SuspendedNoDataObjects)
	}
}

func nonZeroSources(counts map[nodata.RosterSource]int) map[nodata.RosterSource]int {
	kept := map[nodata.RosterSource]int{}
	for source, count := range counts {
		if count != 0 {
			kept[source] = count
		}
	}
	return kept
}

// A target shape whose expected set exists in the backend but cannot be
// derived by this build suspends no-data by name and nothing else.
//
// Each row is a shape the backend does enumerate - so the two agree that it
// has a roster - while this build's roster derivation does not reach it
// (section 5.7, rows d and e: "the first cut" names them and does not build
// them). Each is paired with the same target under a dimension set the first
// cut does derive, so a suspension cannot be the fixture's doing: only the
// shape differs between the two compilations.
//
//   - A static host list whose no-data dimensions name bk_target_ip without
//     being the host pair is row e: the backend filters its history by the
//     target and reports the missing instances (base.py:75-98).
//   - A topology target with the host pair is row d: the backend enumerates
//     the hosts under the nodes (base.py:188-199).
//   - A topology target whose dimensions name the host and one more is row e
//     over a topology (base.py:75-98 after :188-199).
//   - A static list naming hosts by identifier only cannot be read as hosts
//     by address: the backend subscripts inst["bk_target_ip"] (base.py:185)
//     and has no roster for it either.
//   - A dynamic group target with the host pair: the backend expands the
//     group to its hosts in the strategy's business (base.py:201-215) and
//     reports the missing ones; this build matches such a target on the
//     group's members but derives no roster from it.
func TestARosterShapeThisCutCannotDeriveSuspendsOnlyNoData(t *testing.T) {
	topology := legacyTarget("host_topo_node", "eq", map[string]any{"bk_obj_id": "set", "bk_inst_id": 12})
	for name, test := range map[string]struct {
		target     []any
		suspended  []string
		attached   []string
		attachedAs nodata.RosterSource
	}{
		"a static host list under the address alone": {
			target:    legacyTarget("bk_target_ip", "eq", staticHost("192.0.2.1")),
			suspended: []string{"bk_target_ip"},
			attached:  []string{"bk_target_ip", "bk_target_cloud_id"}, attachedAs: nodata.RosterTargetStatic,
		},
		"a static host list under the host pair and one dimension more": {
			target:    legacyTarget("bk_target_ip", "eq", staticHost("192.0.2.1")),
			suspended: []string{"bk_target_ip", "bk_target_cloud_id", "device_name"},
			attached:  []string{"bk_target_ip", "bk_target_cloud_id"}, attachedAs: nodata.RosterTargetStatic,
		},
		"a topology target under the host pair": {
			target:    topology,
			suspended: []string{"bk_target_ip", "bk_target_cloud_id"},
			// Without bk_target_ip the backend returns None before it reads
			// the target (base.py:173-174) and only the whole item is judged.
			attached: []string{"device_name"}, attachedAs: nodata.RosterWhole,
		},
		"a topology target under the host pair and one dimension more": {
			target:    topology,
			suspended: []string{"bk_target_ip", "bk_target_cloud_id", "appid"},
			attached:  []string{"appid"}, attachedAs: nodata.RosterWhole,
		},
		"a static list naming hosts by identifier only": {
			target:    legacyTarget("bk_target_ip", "eq", map[string]any{"bk_host_id": 101}),
			suspended: []string{"bk_target_ip", "bk_target_cloud_id"},
			attached:  []string{}, attachedAs: nodata.RosterWhole,
		},
		"a dynamic group target under the host pair": {
			target:    legacyTarget("dynamic_group", "eq", map[string]any{"dynamic_group_id": "dg-1"}),
			suspended: []string{"bk_target_ip", "bk_target_cloud_id"},
			// Without bk_target_ip the backend returns before it reads the
			// target (base.py:173-174), as for the topology rows.
			attached: []string{"device_name"}, attachedAs: nodata.RosterWhole,
		},
	} {
		t.Run(name, func(t *testing.T) {
			wantOnlyNoDataSuspended(t, compileRosterShape(t, false, func(item map[string]any) {
				item["target"] = test.target
				withNoData(item, test.suspended...)
			}))
			wantNoDataAttached(t, compileRosterShape(t, false, func(item map[string]any) {
				item["target"] = test.target
				withNoData(item, test.attached...)
			}), test.attachedAs)
		})
	}
}

// A target plan has a roster exactly when the item's no-data dimensions are
// the dimensions the plan's record key is read from (decision-017 section
// 3.4, second item: host_id {bk_host_id}; model_inst_id the model and
// instance dimensions; the Kubernetes rules their rule's dimensions). A
// subset, a superset and the empty set are each refused by name, the Plan
// accepted and its thresholds compiled; none is guessed as history or as the
// whole item - the empty set is spelled out there as one of the refusals.
//
// The plans are the writer's own documents (targetplan/testdata,
// writer-observed-plans.json), and the rule dimensions are the protocol's
// (decision-017 section 2.3). The order an item lists its dimensions in is
// not part of the set, so the exact set is also given reversed.
func TestATargetPlanHasARosterOnlyUnderExactlyItsKeyDimensions(t *testing.T) {
	k8sCluster := map[string]any{"schema_version": 1, "model_id": "cw-K8s_Cluster", "target_rule": "k8s_cluster",
		"failure_policy": "no_match", "dynamic_groups": []any{}, "dynamic_topologies": []any{},
		"static_targets": []any{map[string]any{"model_id": "cw-K8s_Cluster", "model_inst_id": "cluster-a",
			"match": map[string]any{"bcs_cluster_id": "cluster-a"}}}}
	k8sNode := map[string]any{"schema_version": 1, "model_id": "cw-K8s_Node", "target_rule": "k8s_node",
		"failure_policy": "no_match", "dynamic_groups": []any{}, "dynamic_topologies": []any{},
		"static_targets": []any{map[string]any{"model_id": "cw-K8s_Node", "model_inst_id": "cluster-a|node-01",
			"match": map[string]any{"bcs_cluster_id": "cluster-a", "node": "node-01"}}}}
	k8sWorkload := map[string]any{"schema_version": 1, "model_id": "cw-K8s_Workload", "target_rule": "k8s_workload",
		"failure_policy": "no_match", "dynamic_groups": []any{}, "dynamic_topologies": []any{},
		"static_targets": []any{map[string]any{"model_id": "cw-K8s_Workload", "model_inst_id": "cluster-a|prod|Deployment|web",
			"match": map[string]any{"bcs_cluster_id": "cluster-a", "namespace": "prod", "workload_kind": "Deployment", "workload_name": "web"}}}}
	// model_inst_id in its three readings (decision-017 section 2.3): behind
	// a model gate the writer names, by the model code a query
	// configuration's target identity points at, and by host identity.
	gated := map[string]any{"schema_version": 1, "model_id": "cw-MySQL", "target_rule": "model_inst_id",
		"failure_policy": "no_match", "dynamic_groups": []any{}, "dynamic_topologies": []any{},
		"model_match":    map[string]any{"cw_object_model_id": "17"},
		"static_targets": []any{map[string]any{"model_id": "cw-MySQL", "model_inst_id": "mysql-prod-01"}}}
	coded := map[string]any{"schema_version": 1, "model_id": "cw-MySQL", "target_rule": "model_inst_id",
		"failure_policy": "no_match", "dynamic_groups": []any{}, "dynamic_topologies": []any{},
		"static_targets": []any{map[string]any{"model_id": "cw-MySQL", "model_inst_id": "mysql-prod-01"}}}
	codedIdentity := func(item map[string]any) {
		query := item["query_configs"].([]any)[0].(map[string]any)
		query["target_identity"] = map[string]any{"type": "object_model_inst",
			"object_model_field": "object_model_code", "object_model_inst_field": "object_inst_id"}
	}
	byHost := map[string]any{"schema_version": 1, "model_id": "cw-Host", "target_rule": "model_inst_id",
		"failure_policy": "no_match", "dynamic_groups": []any{}, "dynamic_topologies": []any{},
		"static_targets": []any{map[string]any{"model_id": "cw-Host", "model_inst_id": "101"}}}
	hostID := map[string]any{"schema_version": 1, "model_id": "cw-Host", "target_rule": "host_id",
		"failure_policy": "no_match", "dynamic_groups": []any{}, "dynamic_topologies": []any{},
		"static_targets": []any{map[string]any{"bk_host_id": 101}}}

	for name, test := range map[string]struct {
		plan  map[string]any
		query func(map[string]any)
		exact []string
		// refused are the subset, the superset and the empty set, by name.
		refused map[string][]string
	}{
		"k8s_cluster": {plan: k8sCluster, exact: []string{"bcs_cluster_id"}, refused: map[string][]string{
			// The rule reads one dimension, so its only proper subset is the
			// empty set, which is the next row.
			"superset": {"bcs_cluster_id", "namespace"}, "empty": {},
			"another dimension": {"node"},
		}},
		"k8s_node": {plan: k8sNode, exact: []string{"bcs_cluster_id", "node"}, refused: map[string][]string{
			"subset": {"bcs_cluster_id"}, "superset": {"bcs_cluster_id", "node", "namespace"}, "empty": {},
		}},
		"k8s_workload": {plan: k8sWorkload, exact: []string{"bcs_cluster_id", "namespace", "workload_kind", "workload_name"},
			refused: map[string][]string{
				"subset":   {"bcs_cluster_id", "namespace", "workload_kind"},
				"superset": {"bcs_cluster_id", "namespace", "workload_kind", "workload_name", "pod_name"},
				"empty":    {},
			}},
		"model_inst_id behind a model gate": {plan: gated, exact: []string{"cw_object_model_inst_id", "cw_object_model_id"},
			refused: map[string][]string{
				"subset":   {"cw_object_model_inst_id"},
				"superset": {"cw_object_model_inst_id", "cw_object_model_id", "device_name"},
				"empty":    {},
			}},
		"model_inst_id by the model code": {plan: coded, query: codedIdentity, exact: []string{"object_model_code", "object_inst_id"},
			refused: map[string][]string{
				"subset":   {"object_inst_id"},
				"superset": {"object_model_code", "object_inst_id", "device_name"},
				"empty":    {},
			}},
		// Read by host identity the key is the host id, and so is the roster
		// group (decision-017 section 2.3 (c); global-business strategy
		// design section 4.4).
		"model_inst_id by host identity": {plan: byHost, exact: []string{"bk_host_id"}, refused: map[string][]string{
			"the model and instance dimensions": {"cw_object_model_id", "cw_object_model_inst_id"},
			"superset":                          {"bk_host_id", "device_name"}, "empty": {},
		}},
		"host_id": {plan: hostID, exact: []string{"bk_host_id"}, refused: map[string][]string{
			"superset": {"bk_host_id", "device_name"}, "empty": {},
			"the address pair": {"bk_target_ip", "bk_target_cloud_id"},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			compile := func(dimensions []string) controlplane.Catalog {
				return compileRosterShape(t, false, func(item map[string]any) {
					item["target_plan"] = test.plan
					if test.query != nil {
						test.query(item)
					}
					withNoData(item, dimensions...)
				})
			}
			t.Run("exact", func(t *testing.T) {
				wantNoDataAttached(t, compile(test.exact), nodata.RosterTargetPlan)
			})
			t.Run("exact, reversed", func(t *testing.T) {
				reversed := make([]string, 0, len(test.exact))
				for index := len(test.exact) - 1; index >= 0; index-- {
					reversed = append(reversed, test.exact[index])
				}
				wantNoDataAttached(t, compile(reversed), nodata.RosterTargetPlan)
			})
			for refusal, dimensions := range test.refused {
				t.Run(refusal, func(t *testing.T) {
					wantOnlyNoDataSuspended(t, compile(dimensions))
				})
			}
		})
	}
}

// A global strategy may detect no-data with no target or with a target plan,
// and one with an old-form target is refused whole and by name
// (global-business strategy design, sections 3.3, 4.4 and 4.6). The old form
// would drop every host outside the Plan's own business from the expected set
// silently (section 3.3), so the refusal is the strategy's and not just its
// no-data half's; the two accepted forms carry their no-data half as an
// ordinary strategy would, and a target plan whose dimensions do not express
// a roster suspends only no-data, as decision-017 has it for every Plan
// (section 4.4: everything decision-017 rules applies, and no new rule).
//
// The existing global cases compile with no-data off; every case here turns
// it on, which is the configuration this rule is about.
func TestAGlobalStrategyDetectsNoDataOnlyWithoutAnOldFormTarget(t *testing.T) {
	k8sCluster := map[string]any{"schema_version": 1, "model_id": "cw-K8s_Cluster", "target_rule": "k8s_cluster",
		"failure_policy": "no_match", "dynamic_groups": []any{}, "dynamic_topologies": []any{},
		"static_targets": []any{map[string]any{"model_id": "cw-K8s_Cluster", "model_inst_id": "cluster-a",
			"match": map[string]any{"bcs_cluster_id": "cluster-a"}, "bk_biz_id": 11}}}
	hostID := map[string]any{"schema_version": 1, "model_id": "cw-Host", "target_rule": "host_id",
		"failure_policy": "no_match", "dynamic_groups": []any{}, "dynamic_topologies": []any{},
		"static_targets": []any{map[string]any{"bk_host_id": 101}}}

	t.Run("no target", func(t *testing.T) {
		catalog := compileRosterShape(t, true, func(item map[string]any) { withNoData(item) })
		wantNoDataAttached(t, catalog, nodata.RosterHistory)
		wantGlobalAccepted(t, catalog)
	})
	t.Run("no target, grouped", func(t *testing.T) {
		catalog := compileRosterShape(t, true, func(item map[string]any) { withNoData(item, "bk_target_ip", "bk_target_cloud_id") })
		wantNoDataAttached(t, catalog, nodata.RosterHistory)
		wantGlobalAccepted(t, catalog)
	})
	t.Run("a Kubernetes target plan", func(t *testing.T) {
		catalog := compileRosterShape(t, true, func(item map[string]any) {
			item["target_plan"] = k8sCluster
			withNoData(item, "bcs_cluster_id")
		})
		wantNoDataAttached(t, catalog, nodata.RosterTargetPlan)
		wantGlobalAccepted(t, catalog)
	})
	t.Run("a host target plan", func(t *testing.T) {
		catalog := compileRosterShape(t, true, func(item map[string]any) {
			item["target_plan"] = hostID
			withNoData(item, "bk_host_id")
		})
		wantNoDataAttached(t, catalog, nodata.RosterTargetPlan)
		wantGlobalAccepted(t, catalog)
	})
	t.Run("a target plan whose dimensions express no roster", func(t *testing.T) {
		catalog := compileRosterShape(t, true, func(item map[string]any) {
			item["target_plan"] = hostID
			withNoData(item, "bk_target_ip", "bk_target_cloud_id")
		})
		wantOnlyNoDataSuspended(t, catalog)
		wantGlobalAccepted(t, catalog)
	})
	for name, target := range map[string][]any{
		// The one shape the first cut derives a roster for on an ordinary
		// strategy: refused here all the same, because it is the old form.
		"an old-form static host target": legacyTarget("bk_target_ip", "eq", staticHost("192.0.2.1")),
		"an old-form topology target":    legacyTarget("host_topo_node", "eq", map[string]any{"bk_obj_id": "set", "bk_inst_id": 12}),
	} {
		t.Run(name, func(t *testing.T) {
			item := func(item map[string]any) {
				item["target"] = target
				withNoData(item, "bk_target_ip", "bk_target_cloud_id")
			}
			catalog := compileRosterShape(t, true, item)
			if len(catalog.QueryGroups) != 0 {
				t.Fatalf("the global strategy compiled into %d groups; an old-form target refuses it whole", len(catalog.QueryGroups))
			}
			refused := false
			for _, disposition := range catalog.Dispositions {
				if disposition.Reason == "GLOBAL_STRATEGY_UNSUPPORTED" {
					refused = disposition.Disposition == controlplane.DispositionUnsupported &&
						disposition.Detail == "reason=legacy_target source=bk_monitor/time_series"
				}
			}
			if !refused {
				t.Fatalf("dispositions %+v, want GLOBAL_STRATEGY_UNSUPPORTED with reason=legacy_target", catalog.Dispositions)
			}
			composition := controlplane.ComposeCatalog(catalog)
			if got := nonZeroSources(composition.NoDataPlans); len(got) != 0 {
				t.Fatalf("catalog_no_data_plans = %v; a refused strategy has no Plan to count", got)
			}
			// The same document as an ordinary strategy compiles, so the
			// refusal is the global flag's and not the fixture's.
			if ordinary := compileRosterShape(t, false, item); len(ordinary.QueryGroups) != 1 {
				t.Fatalf("the same strategy as an ordinary one did not compile: %+v", ordinary.Dispositions)
			}
		})
	}
}

func wantGlobalAccepted(t *testing.T, catalog controlplane.Catalog) {
	t.Helper()
	plan := onlyPlan(t, catalog)
	if !plan.Plan.GlobalBusiness {
		t.Fatal("the Plan is not frozen as global")
	}
	want := map[string]int{"accepted": 1, "global_unsupported": 0, "withheld": 0}
	if got := controlplane.ComposeCatalog(catalog).GlobalStrategies; !reflect.DeepEqual(got, want) {
		t.Fatalf("global outcomes = %v, want %v", got, want)
	}
}
