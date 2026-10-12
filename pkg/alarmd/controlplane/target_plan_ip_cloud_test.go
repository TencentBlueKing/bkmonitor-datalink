// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// A strategy carrying the shared ip_cloud sample's plan compiles under its
// own tenant, with the plan frozen as ip_cloud, its tenant and its host; a
// plan naming another tenant is refused by name at the plan's tenant, and
// the sibling runs either way.
func TestAnIPCloudPlanCompilesOnlyUnderTheStrategysTenant(t *testing.T) {
	payload, err := os.ReadFile("testdata/two_threshold_strategies.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents []json.RawMessage
	if err := json.Unmarshal(payload, &documents); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../targetplan/testdata/ip_cloud_target_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var sample struct {
		Tenant string          `json:"tenant"`
		Plan   json.RawMessage `json:"plan"`
	}
	if err := json.Unmarshal(raw, &sample); err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(documents[1], &document); err != nil {
		t.Fatal(err)
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(document["items"], &items); err != nil {
		t.Fatal(err)
	}
	withPlan := func(plan json.RawMessage) json.RawMessage {
		t.Helper()
		items[0]["target_plan"] = plan
		encoded, err := json.Marshal(items)
		if err != nil {
			t.Fatal(err)
		}
		document["items"] = encoded
		strategy, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		return strategy
	}
	build := func(strategy json.RawMessage) controlplane.Catalog {
		t.Helper()
		identity := controlplane.SourceIdentity{TenantID: sample.Tenant, BusinessID: "2", SpaceScope: "bkcc__2"}
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
			Strategies: []controlplane.SourceStrategy{
				{SourceID: "1001", Document: documents[0], Identity: identity},
				{SourceID: "1002", Document: strategy, Identity: identity},
			},
			Planner: &recordingPlanner{facts: queryFacts(t)},
		})
		if err != nil {
			t.Fatal(err)
		}
		return catalog
	}

	accepted := build(withPlan(sample.Plan))
	var frozen *contract.TargetPlanV1
	for index := range accepted.QueryGroups {
		for _, plan := range accepted.QueryGroups[index].Plans {
			if plan.Identity.StrategyID == "1002" {
				frozen = plan.Plan.TargetPlan
			}
		}
	}
	if frozen == nil || frozen.Rule != contract.TargetPlanRuleIPCloud || frozen.TenantID != sample.Tenant ||
		!reflect.DeepEqual(frozen.StaticHosts, []string{"501"}) || !frozen.Identity.Address {
		t.Fatalf("the ip_cloud plan froze as %+v (dispositions %+v)", frozen, accepted.Dispositions)
	}

	var other map[string]any
	if err := json.Unmarshal(sample.Plan, &other); err != nil {
		t.Fatal(err)
	}
	other["bk_tenant_id"] = "tenant-b"
	otherPlan, err := json.Marshal(other)
	if err != nil {
		t.Fatal(err)
	}
	refused := build(withPlan(otherPlan))
	if got := catalogStrategyIDs(refused); !reflect.DeepEqual(got, []string{"1001"}) {
		t.Fatalf("with a plan naming another tenant the catalog runs %v", got)
	}
	found := false
	for _, disposition := range refused.Dispositions {
		if disposition.SourceID == "1002" && disposition.Disposition == controlplane.DispositionUnsupported &&
			disposition.Reason == "TARGET_PLAN_TENANT_MISMATCH" && disposition.FieldPath == "items[0].target_plan.bk_tenant_id" {
			found = true
		}
	}
	if !found {
		t.Fatalf("another tenant's ip_cloud plan was not refused by name: %+v", refused.Dispositions)
	}
}
