// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "testing"

// The health response - what the page opens on and what alarmd-cli's
// fleet.get returns - says the platform no-data horizon in force and which
// layer set it, from the replicas that resolved it. Read as JSON through the
// route, because a field on the struct is not the route filling it.
func TestTheHealthResponseSaysThePlatformHorizonAndItsLayer(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[1].PlatformSettings = &PlatformSettingsFacts{Mode: "authoritative",
		NoDataTrackingHorizonSeconds: 3600, NoDataTrackingHorizonSource: "DYNAMIC"}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, []string{"pod-a", "pod-b"})

	horizon, ok := requestJSON(t, handler, "/api/health")["no_data_horizon"].(map[string]any)
	if !ok {
		t.Fatal("health response carries no no_data_horizon")
	}
	if horizon["seconds"] != float64(3600) || horizon["source"] != "DYNAMIC" || horizon["replica"] != "pod-b" {
		t.Fatalf("no_data_horizon = %v, want 3600 from DYNAMIC said by pod-b", horizon)
	}
}
