// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package contract

import "testing"

// Every reason in the catalogue says whether a round met it or alarmd judged
// it, and nothing outside the catalogue is classified: a new reason without
// a line fails here instead of reading as met by default.
func TestEveryReasonSaysWhetherItIsMetOrJudged(t *testing.T) {
	for code := range reasonCatalogV2 {
		switch reasonSourcesV2[code] {
		case ReasonSourceMet, ReasonSourceJudged:
		default:
			t.Errorf("%s is in the reason catalogue and not classified met or judged (reasonSourcesV2)", code)
		}
	}
	for code := range reasonSourcesV2 {
		if _, known := reasonCatalogV2[code]; !known {
			t.Errorf("%s is classified and not in the reason catalogue", code)
		}
	}
	for code, judged := range map[string]bool{
		ReasonEffectiveTimeInactive: true, ReasonHistoryWarming: true, ReasonGapSkipped: true, ReasonQueryNotReady: true,
		ReasonQueryTimeout: false, ReasonRedisUnavailable: false, ReasonExecutionBudgetExhausted: false,
	} {
		if ReasonJudgedV2(code) != judged {
			t.Errorf("%s judged = %t, want %t", code, !judged, judged)
		}
	}
}
