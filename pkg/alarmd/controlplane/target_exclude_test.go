// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

func TestTargetExclusionsChangeExecutionDigestAndRequireANewerObjectReader(t *testing.T) {
	for _, rule := range []string{"host_id", "ip_cloud"} {
		t.Run(rule, func(t *testing.T) {
			build := func(exclude any) objectBuild {
				return buildObjectCatalog(t, "", objectStrategyDocument(t, func(document map[string]any) {
					document["id"] = 101
					plan := map[string]any{"schema_version": 1, "model_id": "cw-Host", "target_rule": rule, "failure_policy": "no_match",
						"static_targets": []any{map[string]any{"bk_host_id": 101}, map[string]any{"bk_host_id": 102}}, "dynamic_groups": []any{}, "dynamic_topologies": []any{}}
					if rule == "ip_cloud" {
						plan["bk_tenant_id"] = "tenant-a"
					}
					if exclude != nil {
						plan["exclude"] = exclude
					}
					objectFirstItem(document)["target_plan"] = plan
				}))
			}
			old := build(nil)
			empty := build([]any{})
			if old.object != empty.object {
				t.Fatal("an empty exclusion changed existing execution bytes")
			}
			excluded := build([]any{map[string]any{"bk_host_id": 101}})
			duplicate := build([]any{map[string]any{"bk_host_id": "101"}, map[string]any{"bk_host_id": 101}})
			if excluded.object != duplicate.object {
				t.Fatal("equivalent exclusion identities changed the execution digest")
			}
			if excluded.object == old.object {
				t.Fatal("target exclusion did not change execution digest")
			}
			object := controlplane.BuildQueryGroupObject(excluded.group)
			if object.ContractVersion != "alarmd-query-group-object-v4" {
				t.Fatalf("unsafe object version %q", object.ContractVersion)
			}
			encoded, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			var read controlplane.QueryGroupObject
			if err := json.Unmarshal(encoded, &read); err != nil {
				t.Fatal(err)
			}
			keys := read.Plans[0].TargetPlan.ExcludeKeys
			if rule == "ip_cloud" {
				keys = read.Plans[0].TargetPlan.ExcludeHosts
			}
			if !reflect.DeepEqual(keys, []string{"101"}) {
				t.Fatalf("exclusion lost during propagation: %+v", read.Plans[0].TargetPlan)
			}
			if controlplane.BuildQueryGroupObject(old.group).ContractVersion != "alarmd-query-group-object-v2" {
				t.Fatal("old target plan version changed")
			}
		})
	}
}

func TestExcludedModelMembersSurviveRepositoryAssemblyAndCompilation(t *testing.T) {
	harness := newObjectCatalogHarness(t)
	catalog := objectCatalogTwoGroupsWithTargetPlan(t, json.RawMessage(`{"schema_version":1,"model_id":"cw-Host","target_rule":"model_inst_id","failure_policy":"no_match","static_targets":[{"model_id":"cw-Host","model_inst_id":"101"}],"dynamic_groups":[],"dynamic_topologies":[],"exclude":[{"model_id":"cw-Host","model_inst_id":"101"}]}`))
	harness.publish(t, catalog)
	compiler, semantics := runtimePlanCompiler(t)
	checked := false
	for _, group := range catalog.QueryGroups {
		original := group.Plans[0].Plan.TargetPlan
		if original == nil {
			continue
		}
		digest, err := controlplane.DeriveQueryGroupObjectDigest(group)
		if err != nil {
			t.Fatal(err)
		}
		loaded, err := harness.repository.LoadQueryGroupObject(harness.ctx, digest)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.ContractVersion != "alarmd-query-group-object-v4" {
			t.Fatalf("unsafe object version %s", loaded.ContractVersion)
		}
		assembled, err := controlplane.AssembleQueryGroup(loaded, outputContextsFor(t, harness, group))
		if err != nil {
			t.Fatal(err)
		}
		result, err := compiler.Compile(harness.ctx, strategy.CompileRequest{Plan: assembled.Plans[0].Plan, DatasetContract: assembled.QueryPlan.Normalization.DatasetContract, StateSemantics: semantics})
		if err != nil {
			t.Fatal(err)
		}
		compiled, ok := result.Plan()
		if !ok {
			t.Fatalf("compile terminal: %+v", result.PlanTerminal())
		}
		if !reflect.DeepEqual(compiled.TargetPlan(), original) || len(compiled.TargetPlan().ExcludeMembers) != 1 {
			t.Fatalf("compiled target lost exclusion: %+v", compiled.TargetPlan())
		}
		checked = true
	}
	if !checked {
		t.Fatal("no target-bearing plan checked")
	}
}
