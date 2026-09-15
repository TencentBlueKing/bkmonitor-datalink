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

func unavailableObservation(attributions ...string) observability.Observation {
	facts := make([]observability.QueryUnavailableFacts, 0, len(attributions))
	for _, attribution := range attributions {
		facts = append(facts, observability.QueryUnavailableFacts{Attribution: attribution})
	}
	return observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
		Operation: observability.OperationNormal, Result: observability.ResultFailed,
		QueryUnavailable: facts,
	}
}

// Three situations, one code: only counting them apart says whether a wave of
// QUERY_UNAVAILABLE objects is a provider fault or queries that were never
// sent. Every series is pre-created so a fallback that never fires is a zero,
// not an absent series, and one completion with several unavailable Plans
// counts once per Plan.
func TestQueryUnavailableCountsEachAttributionPerPhysicalQuery(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	for _, attribution := range observability.QueryUnavailableAttributions {
		if got := testutil.ToFloat64(recorder.phaseTwo.queryUnavailable.attributions.WithLabelValues(attribution)); got != 0 {
			t.Fatalf("%s starts at %v, want a pre-created zero", attribution, got)
		}
	}
	recorder.Observe(context.Background(), unavailableObservation(
		observability.QueryUnavailableNoAttempts, observability.QueryUnavailableNoAttempts, observability.QueryUnavailableFromAttempt))
	recorder.Observe(context.Background(), unavailableObservation("not_a_declared_attribution"))

	counts := map[string]float64{}
	for _, attribution := range observability.QueryUnavailableAttributions {
		counts[attribution] = testutil.ToFloat64(recorder.phaseTwo.queryUnavailable.attributions.WithLabelValues(attribution))
	}
	want := map[string]float64{
		observability.QueryUnavailableNoAttempts:      2,
		observability.QueryUnavailableFromAttempt:     1,
		observability.QueryUnavailableNoAttemptReason: 0,
		observability.QueryUnavailableOther:           1,
	}
	for attribution, expected := range want {
		if counts[attribution] != expected {
			t.Fatalf("%s = %v, want %v (all: %v)", attribution, counts[attribution], expected, counts)
		}
	}
	if invented := testutil.ToFloat64(recorder.phaseTwo.queryUnavailable.attributions.WithLabelValues("not_a_declared_attribution")); invented != 0 {
		t.Fatalf("an undeclared attribution became its own series: %v", invented)
	}
}
