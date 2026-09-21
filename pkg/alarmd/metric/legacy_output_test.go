// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"strings"
	"testing"
)

func TestLegacyPodCacheObservationKeepsBoundedOutcomes(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	for _, result := range []string{"hit", "miss", "error", "unbounded-pod"} {
		recorder.RecordLegacyPodCache(result)
	}
	wire := scrape(t, recorder)
	for _, result := range []string{"hit", "miss", "error"} {
		if !strings.Contains(wire, `bkmonitor_alarmd_legacy_pod_cache_total{result="`+result+`"} 1`) {
			t.Fatalf("missing %s counter", result)
		}
	}
	if strings.Contains(wire, "unbounded-pod") {
		t.Fatal("high cardinality label accepted")
	}
	normalized := observability.NormalizeObservation(observability.Observation{Component: observability.ComponentRuntime, Stage: observability.StageLegacyPodCache, Result: observability.ResultDegraded})
	if normalized.Component != observability.ComponentRuntime || normalized.Stage != observability.StageLegacyPodCache || normalized.Result != observability.ResultDegraded {
		t.Fatal("fallback diagnostic was normalized away")
	}
}
