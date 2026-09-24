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

// A catalog object read is counted by its own family and by nothing in the
// generic one. The (_other, _other) cell of observation_total is the one
// place an emitter nobody has catalogued shows up; a live replica had 822,000
// object-read hits in it, at three hundred a second, and a real stray
// emitter would have been invisible beside them.
func TestCatalogStagesStayOutOfTheGenericObservationFamily(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	other := r.observations.total.WithLabelValues(
		string(observability.ComponentOther), string(observability.StageOther),
		string(observability.ResultSuccess), string(observability.ReasonNone))
	ctx := context.Background()
	for _, stage := range []observability.Stage{
		observability.StageObjectRead, observability.StageObjectCatalog, observability.StageScheduleCutover,
	} {
		component, normalized := observability.NormalizeComponentStage(observability.ComponentControlPlane, stage)
		if component != observability.ComponentControlPlane || normalized != stage {
			t.Fatalf("%s normalized to (%s, %s): folded to the unclassified cell", stage, component, normalized)
		}
		if observability.IsGenericMetricComponentStage(component, normalized) {
			t.Fatalf("%s entered the generic metric cross product", stage)
		}
	}
	// One read that hit the cache, as loadObject reports every hit.
	r.Observe(ctx, observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageObjectRead,
		Result:     observability.ResultSuccess,
		ObjectRead: &observability.ObjectReadFacts{Kind: "query_group", Result: "hit"},
	})
	if got := testutil.ToFloat64(other); got != 0 {
		t.Fatalf("observation_total{_other,_other} = %v after an object read, want 0: the read has a family of its own", got)
	}
	if got := testutil.ToFloat64(r.phaseTwo.objectReads.WithLabelValues("query_group", "hit")); got != 1 {
		t.Fatalf("object_read_total{query_group,hit} = %v, want 1", got)
	}
	// And the cell still answers for an emitter that is unclassified.
	r.Observe(ctx, observability.Observation{
		Component: observability.Component("nobody"), Stage: observability.Stage("classified_this"),
		Result: observability.ResultSuccess,
	})
	if got := testutil.ToFloat64(other); got != 1 {
		t.Fatalf("observation_total{_other,_other} = %v after a stray emitter, want 1", got)
	}
}
