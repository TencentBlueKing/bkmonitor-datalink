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

func unavailableObservation(facts ...observability.QueryUnavailableFacts) observability.Observation {
	return observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
		Operation: observability.OperationNormal, Result: observability.ResultFailed,
		QueryUnavailable: facts,
	}
}

// Three situations, one code: only counting them apart says whether a wave of
// QUERY_UNAVAILABLE objects is a provider fault or queries that were never
// sent; and of those that got an answer, whether the backend refused them
// (4xx) or failed (5xx). Every series is pre-created so a fallback that never
// fires is a zero, not an absent series, and one completion with several
// unavailable Plans counts once per Plan.
func TestQueryUnavailableCountsEachAttributionPerPhysicalQuery(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	cell := func(attribution, class string) float64 {
		return testutil.ToFloat64(recorder.phaseTwo.queryUnavailable.attributions.WithLabelValues(attribution, class))
	}
	for _, attribution := range observability.QueryUnavailableAttributions {
		for _, class := range observability.QueryHTTPClasses {
			if got := cell(attribution, class); got != 0 {
				t.Fatalf("%s/%s starts at %v, want a pre-created zero", attribution, class, got)
			}
		}
	}
	none, refused, failed := observability.QueryHTTPClassNone, observability.QueryHTTPClass4xx, observability.QueryHTTPClass5xx
	recorder.Observe(context.Background(), unavailableObservation(
		observability.QueryUnavailableFacts{Attribution: observability.QueryUnavailableNoAttempts, HTTPClass: none},
		observability.QueryUnavailableFacts{Attribution: observability.QueryUnavailableNoAttempts, HTTPClass: none},
		observability.QueryUnavailableFacts{Attribution: observability.QueryUnavailableFromAttempt, HTTPClass: refused},
		observability.QueryUnavailableFacts{Attribution: observability.QueryUnavailableFromAttempt, HTTPClass: failed}))
	recorder.Observe(context.Background(), unavailableObservation(observability.QueryUnavailableFacts{Attribution: "not_a_declared_attribution", HTTPClass: "3xx"}))

	want := map[[2]string]float64{
		{observability.QueryUnavailableNoAttempts, none}:                         2,
		{observability.QueryUnavailableFromAttempt, refused}:                     1,
		{observability.QueryUnavailableFromAttempt, failed}:                      1,
		{observability.QueryUnavailableOther, observability.QueryHTTPClassOther}: 1,
	}
	for _, attribution := range observability.QueryUnavailableAttributions {
		for _, class := range observability.QueryHTTPClasses {
			if got := cell(attribution, class); got != want[[2]string{attribution, class}] {
				t.Fatalf("%s/%s = %v, want %v", attribution, class, got, want[[2]string{attribution, class}])
			}
		}
	}
	if invented := cell("not_a_declared_attribution", "3xx"); invented != 0 {
		t.Fatalf("an undeclared label became its own series: %v", invented)
	}
}
