// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obchannel

import (
	"strings"
	"testing"
)

// The object summary line says a carried cause reason is carried, from which
// Slot when known and "origin unknown" when not; a physical one reads as it
// did.
func TestTheObjectLineSaysACarriedReasonIsCarried(t *testing.T) {
	line := func(origin map[string]any) string {
		fact := map[string]any{"kind": "DEGRADED_RUN", "cause_reason": "QUERY_TIMEOUT",
			"coverage": map[string]any{"levels": float64(4), "short": float64(1), "guarded": float64(1)}}
		if origin != nil {
			fact["cause_origin"] = origin
		}
		return objectExtentLine(map[string]any{"anomaly": fact})
	}
	if got := line(map[string]any{"origin": "carried", "slot": float64(1_700_000_040), "cause": "QUERY_TIMEOUT"}); !strings.Contains(got, "carried from slot 1700000040") {
		t.Fatalf("carried with origin: %q", got)
	}
	if got := line(map[string]any{"origin": "carried"}); !strings.Contains(got, "carried, origin unknown") {
		t.Fatalf("carried without origin: %q", got)
	}
	if got := line(map[string]any{"origin": "physical"}); strings.Contains(got, "carried") || !strings.Contains(got, "QUERY_TIMEOUT") {
		t.Fatalf("physical: %q", got)
	}
}
