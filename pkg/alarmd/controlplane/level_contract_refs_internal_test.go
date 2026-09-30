// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Plan published without refs serializes without the field, so every
// object a deployment has stored keeps its bytes and its digest on the
// release that adds the field; a Plan published with them carries them
// under their own names and reads back as the same refs.
func TestPublishedRefsRideTheObjectAndAnObjectWithoutThemIsUnchanged(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	payload, err := json.Marshal(buildQueryGroupPlanObject(FrozenPlan{Identity: identity}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "level_contract_refs") {
		t.Fatalf("an object of a Plan published without refs carries the field: %s", payload)
	}
	refs := []execution.RuntimeLevelContractRef{{LevelID: 5, LevelStateCompatibility: strings.Repeat("a", 64), WarmupRequirementRef: strings.Repeat("b", 64), DetectFingerprint: strings.Repeat("c", 64)}}
	object := buildQueryGroupPlanObject(FrozenPlan{Identity: identity, LevelContractRefs: refs})
	payload, err = json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"level_contract_refs":[{"level_id":5,"level_state_compatibility":"`) {
		t.Fatalf("the refs are not under their own names: %s", payload)
	}
	var decoded QueryGroupPlanObject
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := levelContractRefsOf(decoded.LevelContractRefs); !reflect.DeepEqual(got, refs) {
		t.Fatalf("read back %+v, want %+v", got, refs)
	}
	if levelContractRefsOf(nil) != nil {
		t.Fatal("an object without refs reads back as refs")
	}
}
