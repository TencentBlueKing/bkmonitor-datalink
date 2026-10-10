// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// An ip_cloud target's roster is its resolved addresses, each the group a
// series from that address reports under by the shared sample's no-data
// dimensions; any other dimension set is refused by name.
func TestAnIPCloudTargetsRosterIsItsAddressesByTheSamplesDimensions(t *testing.T) {
	raw, err := os.ReadFile("../targetplan/testdata/ip_cloud_target_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var sample struct {
		Tenant           string          `json:"tenant"`
		Plan             json.RawMessage `json:"plan"`
		MemberKey        string          `json:"member_key"`
		NoDataDimensions []string        `json:"no_data_dimensions"`
	}
	if err := json.Unmarshal(raw, &sample); err != nil {
		t.Fatal(err)
	}
	plan, refusal := targetplan.Decode(sample.Plan, targetplan.Options{TenantID: sample.Tenant})
	if refusal != nil {
		t.Fatal(refusal)
	}
	roster, err := BuildRoster(RosterRequest{AggDimension: sample.NoDataDimensions, Plan: plan, TargetMembers: []string{sample.MemberKey}})
	if err != nil || roster.Source != RosterTargetPlan || len(roster.Groups) != 1 {
		t.Fatalf("roster = %+v, %v", roster, err)
	}
	series, projected := Project(map[string]string{contract.IPCloudIPDimension: "192.0.2.1", contract.IPCloudCloudDimension: "0", "device_name": "eth0"},
		sample.NoDataDimensions)
	if _, expected := roster.Groups[series.Key()]; !projected || !expected {
		t.Fatalf("the member's series projects to %q, not in the roster %v", series.Key(), roster.Groups)
	}
	if _, err := BuildRoster(RosterRequest{AggDimension: []string{contract.IPCloudIPDimension}, Plan: plan, TargetMembers: []string{sample.MemberKey}}); err == nil {
		t.Fatal("a roster by the address alone was built")
	}
}
