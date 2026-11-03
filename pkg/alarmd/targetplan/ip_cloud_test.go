// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package targetplan_test

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// ipCloudContract is the ip_cloud sample the writer and this reader share,
// byte for byte (testdata/ip_cloud_target_contract.json, the writer's
// tests/integrations/strategy/fixtures/ip_cloud_target_contract.json).
type ipCloudContract struct {
	Tenant           string          `json:"tenant"`
	Plan             json.RawMessage `json:"plan"`
	Host             json.RawMessage `json:"host"`
	MemberKey        string          `json:"member_key"`
	NoDataDimensions []string        `json:"no_data_dimensions"`
	Records          []struct {
		Name       string                     `json:"name"`
		Dimensions map[string]json.RawMessage `json:"dimensions"`
		Key        string                     `json:"key"`
		Matches    bool                       `json:"matches"`
	} `json:"records"`
}

func loadIPCloudContract(t *testing.T) ipCloudContract {
	t.Helper()
	raw, err := os.ReadFile("testdata/ip_cloud_target_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var sample ipCloudContract
	if err := json.Unmarshal(raw, &sample); err != nil {
		t.Fatal(err)
	}
	if len(sample.Records) == 0 || sample.MemberKey == "" {
		t.Fatal("the shared sample holds no records")
	}
	return sample
}

// The shared sample's plan decodes under its tenant into an ip_cloud plan
// whose static target is the host by id and whose no-data dimensions are
// the sample's; each of the sample's records reads the key the sample
// gives, or none, and matches exactly when that key is the member's.
func TestTheSharedIPCloudSampleDecodesAndReadsEveryRecordAsWritten(t *testing.T) {
	sample := loadIPCloudContract(t)
	plan, refusal := targetplan.Decode(sample.Plan, targetplan.Options{TenantID: sample.Tenant})
	if refusal != nil {
		t.Fatalf("the shared plan was refused: %v", refusal)
	}
	if plan.Rule != contract.TargetPlanRuleIPCloud || plan.TenantID != sample.Tenant || !plan.Identity.Address ||
		!reflect.DeepEqual(plan.StaticHosts, []string{"501"}) || len(plan.StaticKeys) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	want := append([]string(nil), sample.NoDataDimensions...)
	sort.Strings(want)
	if got := plan.Identity.RosterDimensions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("no-data dimensions = %v, want the sample's %v", got, want)
	}
	for _, record := range sample.Records {
		key, placed := contract.ReadIPCloudKey(func(name string) (json.RawMessage, bool) {
			raw, present := record.Dimensions[name]
			return raw, present
		})
		if placed != (record.Key != "") || key != record.Key {
			t.Fatalf("%s: read %q, %v; want %q", record.Name, key, placed, record.Key)
		}
		if matches := placed && key == sample.MemberKey; matches != record.Matches {
			t.Fatalf("%s: matches %v, want %v", record.Name, matches, record.Matches)
		}
	}
}

// An ip_cloud plan is read only as the strategy's own tenant's: another
// tenant is refused by its own name, and a plan naming none is refused as
// unreadable. The tenant is ip_cloud's alone, the model is the host model,
// a static target is a host by id, and every other unknown field is still
// refused. Dynamic references are read as every host rule reads them.
func TestAnIPCloudPlanIsReadOnlyAsTheStrategysTenants(t *testing.T) {
	sample := loadIPCloudContract(t)
	edited := func(edit func(map[string]any)) json.RawMessage {
		t.Helper()
		var document map[string]any
		if err := json.Unmarshal(sample.Plan, &document); err != nil {
			t.Fatal(err)
		}
		edit(document)
		raw, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for _, test := range []struct {
		name   string
		plan   json.RawMessage
		tenant string
		reason string
		path   string
	}{
		{name: "another tenant", plan: sample.Plan, tenant: "tenant-b", reason: targetplan.ReasonTenantMismatch, path: "bk_tenant_id"},
		{name: "no tenant", plan: edited(func(d map[string]any) { delete(d, "bk_tenant_id") }), tenant: sample.Tenant,
			reason: targetplan.ReasonUnsupported, path: "bk_tenant_id"},
		{name: "tenant on host_id", plan: edited(func(d map[string]any) { d["target_rule"] = "host_id" }), tenant: sample.Tenant,
			reason: targetplan.ReasonUnsupported, path: "bk_tenant_id"},
		{name: "not the host model", plan: edited(func(d map[string]any) { d["model_id"] = "cw-MySQL" }), tenant: sample.Tenant,
			reason: targetplan.ReasonUnsupported, path: "model_id"},
		{name: "unknown field", plan: edited(func(d map[string]any) { d["bk_biz_id"] = 2 }), tenant: sample.Tenant,
			reason: targetplan.ReasonUnsupported, path: "bk_biz_id"},
		{name: "static member, not host", plan: edited(func(d map[string]any) {
			d["static_targets"] = []any{map[string]any{"model_id": "cw-Host", "model_inst_id": "501"}}
		}), tenant: sample.Tenant, reason: targetplan.ReasonUnsupported, path: "static_targets[0].model_id"},
	} {
		plan, refusal := targetplan.Decode(test.plan, targetplan.Options{TenantID: test.tenant})
		if refusal == nil || refusal.Reason != test.reason || refusal.Path != test.path {
			t.Fatalf("%s: decoded %+v, refusal %+v; want %s at %s", test.name, plan, refusal, test.reason, test.path)
		}
	}

	dynamic := edited(func(d map[string]any) {
		d["static_targets"] = []any{}
		d["dynamic_groups"] = []any{map[string]any{"dynamic_group_id": "1"}}
		d["dynamic_topologies"] = []any{map[string]any{"bk_biz_id": 2, "bk_obj_id": "module", "bk_inst_id": 31}}
	})
	plan, refusal := targetplan.Decode(dynamic, targetplan.Options{TenantID: sample.Tenant})
	if refusal != nil || !reflect.DeepEqual(plan.DynamicGroups, []string{"1"}) || len(plan.DynamicTopologies) != 1 || len(plan.StaticHosts) != 0 {
		t.Fatalf("a dynamic ip_cloud plan = %+v, %v", plan, refusal)
	}
}
