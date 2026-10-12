// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// An empty answer is quiet only for an event count of groups - the primary
// asked from earlier than its window, requiring identity columns - answered
// whole and empty with nothing withheld. An ungrouped event count's empty
// answer is no data (the query service answers it with zeros when there are
// no events), and a group the target turned away is the target's empty.
func TestOnlyAnEventCountOfGroupsAnsweringWithNoGroupIsQuiet(t *testing.T) {
	grouped := []execution.DataRequirement{{Role: execution.InputRolePrimary, ProviderLeadSeconds: 240, RequiredColumns: []string{"host", "value"}}}
	whole := execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateEmpty}
	if !(queryAvailabilityEvidence{}).quietWhenEmpty(whole, grouped) {
		t.Fatal("an event count of groups answered whole with no group is not quiet")
	}
	for name, test := range map[string]struct {
		evidence     queryAvailabilityEvidence
		primary      execution.PrimaryInputFact
		requirements []execution.DataRequirement
	}{
		"ungrouped": {primary: whole, requirements: []execution.DataRequirement{{Role: execution.InputRolePrimary, ProviderLeadSeconds: 240, RequiredColumns: []string{"value"}}}},
		"no lead":   {primary: whole, requirements: []execution.DataRequirement{{Role: execution.InputRolePrimary, RequiredColumns: []string{"host", "value"}}}},
		"dependency led": {primary: whole, requirements: []execution.DataRequirement{{Role: execution.InputRoleAlgorithmDependency, ProviderLeadSeconds: 240,
			RequiredColumns: []string{"host", "value"}}}},
		"with data":         {primary: execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, requirements: grouped},
		"partial":           {primary: execution.PrimaryInputFact{Completeness: execution.CompletenessPartial, DataState: execution.DataStateEmpty}, requirements: grouped},
		"emptied by target": {evidence: queryAvailabilityEvidence{primaryEmptiedOutside: true}, primary: whole, requirements: grouped},
		"withheld":          {evidence: queryAvailabilityEvidence{primaryWithheldOtherwise: true}, primary: whole, requirements: grouped},
	} {
		if test.evidence.quietWhenEmpty(test.primary, test.requirements) {
			t.Errorf("%s: read as quiet", name)
		}
	}
}
