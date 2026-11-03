// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The strategy document a Python-compatible Plan carries for its output is
// part of the snapshot revision. The platform's strategy cache rewrites two
// things in it round after round without the strategy changing -- an invalid
// strategy's invalid_type, and the order of a dynamic target's hosts -- and
// each such round published a whole new catalog. Neither reaches the Plan now;
// every other change does, and no other list is reordered.
func TestTheOutputSnapshotDoesNotMoveWithTheCachesRewrites(t *testing.T) {
	const document = `{"id":1,"bk_biz_id":2,"update_time":1,"name":"a & b",%s` +
		`"items":[{"id":1,"query_md5":"q","expression":"a","target":[[{"field":"bk_target_ip","method":"eq","value":[%s]}]],` +
		`"query_configs":[{"agg_interval":60,"agg_condition":[%s]}],` +
		`"algorithms":[{"level":1,"type":"Threshold","config":[[{"method":"gt","threshold":1}]]}]}],` +
		`"detects":[{"level":1,"trigger_config":{"count":1,"check_window":1}}]}`
	const (
		hostA      = `{"bk_target_ip":"192.0.2.1","bk_target_cloud_id":0}`
		hostB      = `{"bk_target_ip":"192.0.2.2","bk_target_cloud_id":0}`
		hostC      = `{"bk_target_ip":"192.0.2.3","bk_target_cloud_id":0}`
		conditionA = `{"key":"device","method":"eq","value":["a"]}`
		conditionB = `{"condition":"and","key":"mode","method":"eq","value":["b"]}`
	)
	identity := controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}
	type built struct {
		revision execution.SnapshotRevision
		group    execution.QueryGroupIdentity
		context  execution.OutputContextDigest
		strategy json.RawMessage
	}
	build := func(t *testing.T, invalid, hosts, conditions string) built {
		t.Helper()
		source := json.RawMessage(fmt.Sprintf(document, invalid, hosts, conditions))
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
			Strategies: []controlplane.SourceStrategy{{SourceID: "1", Document: source, Identity: identity}},
			Planner:    &recordingPlanner{facts: queryFacts(t)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 {
			t.Fatalf("strategy did not become a Plan: %#v", catalog.Dispositions)
		}
		plan := catalog.QueryGroups[0].Plans[0]
		if plan.Plan.LegacyOutput == nil {
			t.Fatal("the Plan carries no output snapshot")
		}
		digest, err := controlplane.DeriveOutputContextDigest(plan)
		if err != nil {
			t.Fatal(err)
		}
		return built{revision: catalog.SnapshotRevision, group: catalog.QueryGroups[0].Identity, context: digest, strategy: plan.Plan.LegacyOutput.Strategy}
	}
	base := build(t, `"is_invalid":true,"invalid_type":"",`, hostA+","+hostB+","+hostC, conditionA+","+conditionB)

	// The two rewrites the cache makes between rounds.
	for name, variant := range map[string]built{
		"invalid_type written as the reason": build(t, `"is_invalid":true,"invalid_type":"invalid_metric",`, hostA+","+hostB+","+hostC, conditionA+","+conditionB),
		"is_invalid absent":                  build(t, ``, hostA+","+hostB+","+hostC, conditionA+","+conditionB),
		"target hosts reordered":             build(t, `"is_invalid":true,"invalid_type":"",`, hostC+","+hostA+","+hostB, conditionA+","+conditionB),
	} {
		if variant.revision != base.revision || variant.context != base.context || variant.group != base.group {
			t.Errorf("%s moved the catalog: revision %s -> %s, context %s -> %s", name, base.revision, variant.revision, base.context, variant.context)
		}
	}

	// A change to the strategy still reaches the snapshot: another host, and
	// the conditions in another order, which the platform compares in order.
	for name, variant := range map[string]built{
		"a host added":         build(t, `"is_invalid":true,"invalid_type":"",`, hostA+","+hostB, conditionA+","+conditionB),
		"conditions reordered": build(t, `"is_invalid":true,"invalid_type":"",`, hostA+","+hostB+","+hostC, conditionB+","+conditionA),
	} {
		if variant.context == base.context || variant.revision == base.revision {
			t.Errorf("%s did not reach the output snapshot", name)
		}
	}

	var snapshot map[string]json.RawMessage
	if err := json.Unmarshal(base.strategy, &snapshot); err != nil {
		t.Fatal(err)
	}
	if _, kept := snapshot["is_invalid"]; kept {
		t.Error("is_invalid is still in the snapshot")
	}
	if _, kept := snapshot["invalid_type"]; kept {
		t.Error("invalid_type is still in the snapshot")
	}
	if string(snapshot["name"]) != `"a & b"` || bytes.Contains(base.strategy, []byte("\\u0026")) {
		t.Errorf("the rest of the document was not carried as written: name %s", snapshot["name"])
	}
	if !bytes.Contains(base.strategy, []byte(`"agg_condition":[`+conditionA+","+conditionB+`]`)) {
		t.Errorf("the condition list was reordered or rewritten: %s", base.strategy)
	}
}
