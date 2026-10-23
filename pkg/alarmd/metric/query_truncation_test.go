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
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Every source is there at zero before anything is cut; each query whose
// answer may have been cut counts once, by its source; a source outside the
// closed list counts nowhere.
func TestATruncatedAnswerIsCountedOncePerQueryBySource(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	truncated := recorder.phaseTwo.queryTruncation.truncated
	if got := testutil.CollectAndCount(truncated); got != len(observability.TruncationSources) {
		t.Fatalf("%d series before any cut, want one per source at zero (%d)", got, len(observability.TruncationSources))
	}
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
		Operation: observability.OperationNormal, Result: observability.ResultSuccess,
		QueryTruncation: []observability.QueryTruncationFacts{{Source: "custom/event", Dimension: "host"},
			{Source: "custom/event", Dimension: "pod"}, {Source: "bk_monitor/time_series", Dimension: "host"}},
	})
	if got := testutil.ToFloat64(truncated.WithLabelValues("custom/event")); got != 2 {
		t.Fatalf("custom/event counted %g, want one per query (2)", got)
	}
	if got := testutil.CollectAndCount(truncated); got != len(observability.TruncationSources) {
		t.Fatalf("%d series after a source outside the list, want it counted nowhere", got)
	}
}
