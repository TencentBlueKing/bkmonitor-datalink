// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// addressBusinesses answers the business at an address of a tenant.
type addressBusinesses map[string]string

func (businesses addressBusinesses) LookupAddressBusiness(tenant, address string) (string, bool) {
	business, found := businesses[tenant+"|"+address]
	return business, found
}

// The filter admits each record of the shared ip_cloud sample exactly when
// the sample says it matches the member: a record whose address reads as
// the member's is admitted, one at another address is outside the target,
// and one whose address does not read - a half missing, null, a boolean, a
// fraction, a negative cloud area, conflicting aliases - has no key.
func TestAnIPCloudTargetAdmitsTheSharedSamplesRecordsAsTheSampleSays(t *testing.T) {
	raw, err := os.ReadFile("../targetplan/testdata/ip_cloud_target_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var sample struct {
		MemberKey string `json:"member_key"`
		Records   []struct {
			Name       string                     `json:"name"`
			Dimensions map[string]json.RawMessage `json:"dimensions"`
			Key        string                     `json:"key"`
			Matches    bool                       `json:"matches"`
		} `json:"records"`
	}
	if err := json.Unmarshal(raw, &sample); err != nil {
		t.Fatal(err)
	}
	identity := contract.TargetPlanIdentityV1{Dimensions: []string{contract.IPCloudIPDimension, contract.IPCloudCloudDimension}, Address: true}
	plan := PlanContext{TargetPlan: &TargetPlanContext{Identity: identity, Members: memberSet{sample.MemberKey: {}}}}
	for _, record := range sample.Records {
		decision := (TargetPlanFilter{}).Admit(plan, &Facts{Dimensions: record.Dimensions})
		want := TargetPlanReasonOutOfTarget
		switch {
		case record.Matches:
			want = ""
		case record.Key == "":
			want = TargetPlanReasonKeyMissing
		}
		if decision.Admit != record.Matches || decision.Reason != want {
			t.Fatalf("%s: %+v, want admit=%v reason=%q", record.Name, decision, record.Matches, want)
		}
	}
}

// A global business Plan's event on an ip_cloud target is filed under the
// business of the one host at the record's address inside the plan's
// tenant; an address the reader holds no one host at falls through to the
// next source.
func TestAnIPCloudTargetsEventIsFiledUnderItsHostsBusiness(t *testing.T) {
	target := &contract.TargetPlanV1{Rule: contract.TargetPlanRuleIPCloud, TenantID: "tenant-a",
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{contract.IPCloudIPDimension, contract.IPCloudCloudDimension}, Address: true}}
	lookups := BusinessLookups{Addresses: addressBusinesses{"tenant-a|192.0.2.1|0": "2", "tenant-b|192.0.2.9|0": "3"}}
	dimensions := func(ip string) map[string]json.RawMessage {
		return map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"` + ip + `"`), "bk_target_cloud_id": json.RawMessage(`0`)}
	}
	if got := AttributeBusiness(target, nil, "9", dimensions("192.0.2.1"), lookups); got.BusinessID != "2" || got.Source != contract.BusinessAttributionTarget {
		t.Fatalf("the tenant's host: %+v", got)
	}
	if got := AttributeBusiness(target, nil, "9", dimensions("192.0.2.9"), lookups); got.BusinessID != "9" || got.Source != contract.BusinessAttributionGlobal {
		t.Fatalf("another tenant's host: %+v", got)
	}
}
