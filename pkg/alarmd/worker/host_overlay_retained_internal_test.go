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
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A series without a host overlay - every series of a deployment whose
// records name their host's own address - is charged nothing for it; one
// with an overlay is charged for what it holds.
func TestAHostOverlayIsChargedOnlyWhereThereIsOne(t *testing.T) {
	if got := hostOverlaysRetainedBytes(nil); got != 0 {
		t.Fatalf("no overlay charged %d bytes", got)
	}
	overlay := map[execution.PlanIdentity]map[string]json.RawMessage{
		{TenantID: "tenant", BusinessID: "2", StrategyID: "101"}: {
			"bk_target_ip": json.RawMessage(`"192.0.2.172"`), "bk_target_cloud_id": json.RawMessage(`"0"`), "bk_host_id": json.RawMessage(`"720002"`),
		},
	}
	if got := hostOverlaysRetainedBytes(overlay); got == 0 {
		t.Fatal("an overlay was charged nothing")
	}
}
