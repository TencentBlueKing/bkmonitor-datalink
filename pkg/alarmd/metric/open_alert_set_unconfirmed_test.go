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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// Why the copy could not answer from the consumer's sets is one family, each
// reason present at zero before anything happened, so "never" is a reading
// and not a missing series; the Console's two unconfirmed states are counted
// as the copy counts them, once per entry.
func TestTheReasonsTheSetsCouldNotAnswerAreScrapedEachAtZero(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	stats := openalerts.Stats{}
	r.SetOpenAlertSetSource(func() openalerts.Stats { return stats })
	read := func() map[string]float64 {
		reasons := map[string]float64{}
		for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_open_alert_set_unavailable_total") {
			reasons[m.Label[0].GetValue()] = m.GetCounter().GetValue()
		}
		return reasons
	}
	reasons := read()
	if len(reasons) != 4 || reasons["read_error"] != 0 || reasons["location_unconfirmed"] != 0 || reasons["keying_unconfirmed"] != 0 ||
		reasons["too_large"] != 0 {
		t.Fatalf("before anything: %v, want the four reasons at zero", reasons)
	}
	stats.Unavailable = map[openalerts.UnavailableReason]uint64{openalerts.UnavailableLocationUnconfirmed: 1, openalerts.UnavailableKeyingUnconfirmed: 2}
	if reasons = read(); reasons["location_unconfirmed"] != 1 || reasons["keying_unconfirmed"] != 2 {
		t.Fatalf("reasons = %v, want the copy's counts", reasons)
	}
}
