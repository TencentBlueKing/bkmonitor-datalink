// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
)

// The retry count is the copy's own, and a revision that kept moving has a
// cell of its own beside read_error, at zero before it ever happens.
func TestPlatformSettingsRetriesAndRevisionMovedAreExported(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	r.SetPlatformSettingsSource(func() platformsettings.Stats {
		return platformsettings.Stats{
			Mode:        platformsettings.ModeStale,
			Retries:     7,
			Unavailable: map[platformsettings.UnavailableReason]uint64{platformsettings.UnavailableRevisionMoved: 2, platformsettings.UnavailableReadError: 1},
		}
	})
	retries := gatherFamily(t, r, "bkmonitor_alarmd_platform_settings_read_retry_total")
	if len(retries) != 1 || retries[0].GetCounter().GetValue() != 7 {
		t.Fatalf("read_retry_total = %v, want one cell at 7", retries)
	}
	reasons := map[string]float64{}
	for _, cell := range gatherFamily(t, r, "bkmonitor_alarmd_platform_settings_unavailable_total") {
		reasons[cell.GetLabel()[0].GetValue()] = cell.GetCounter().GetValue()
	}
	want := map[string]float64{"read_error": 1, "revision_moved": 2, "unpublished": 0, "decode_error": 0}
	if len(reasons) != len(want) {
		t.Fatalf("unavailable_total = %v, want %v", reasons, want)
	}
	for reason, value := range want {
		if got, ok := reasons[reason]; !ok || got != value {
			t.Fatalf("unavailable_total = %v, want %v", reasons, want)
		}
	}
}
