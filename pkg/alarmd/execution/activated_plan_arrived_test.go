// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Plan new to its activation is told from one that is not by its Arrived
// mark, in comparison as in its bytes; a record without the mark serializes
// as it always did, and reads back unmarked.
func TestAnArrivedPlanIsToldFromOneThatIsNot(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	plan := execution.ActivatedPlan{Identity: identity, StateGeneration: "g", StateApplyEpoch: 1, ScheduleRevision: "r",
		RequiredFullSlots: 1, ForceWarming: true}
	arrived := plan
	arrived.Arrived = true
	if plan.Equal(arrived) || arrived.Equal(plan) {
		t.Fatal("an arrived Plan compares equal to the same Plan unmarked")
	}
	unmarked, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unmarked), "Arrived") {
		t.Fatalf("an unmarked Plan serializes the mark: %s", unmarked)
	}
	marked, err := json.Marshal(arrived)
	if err != nil {
		t.Fatal(err)
	}
	var decoded execution.ActivatedPlan
	if err := json.Unmarshal(marked, &decoded); err != nil || !decoded.Equal(arrived) {
		t.Fatalf("an arrived Plan read back as %+v (%v), want %+v", decoded, err, arrived)
	}
}
