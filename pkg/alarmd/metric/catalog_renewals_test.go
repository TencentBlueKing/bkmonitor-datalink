// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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

// The four cells read zero from start. A renewal's timelines count under
// their reasons, failed or not, since the batches before a failing one were
// renewed; its objects and contexts count as periodic. A write of the object
// catalog renews nothing.
func TestCatalogRenewalsAreCountedByKindAndReason(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	if cells := testutil.CollectAndCount(r.phaseTwo.catalogRenewals); cells != 4 {
		t.Fatalf("%d cells from start, want 4", cells)
	}
	observe := func(observation observability.Observation) {
		observation.Component = observability.ComponentControlPlane
		r.Observe(context.Background(), observation)
	}
	observe(observability.Observation{Stage: observability.StageActiveQGSet,
		ActiveQGSet: &observability.ActiveQGSetFacts{Operation: "renew", Result: "success", TimelinesAged: 3, TimelinesFirst: 7}})
	observe(observability.Observation{Stage: observability.StageActiveQGSet,
		ActiveQGSet: &observability.ActiveQGSetFacts{Operation: "renew", Result: "failure", TimelinesAged: 2}})
	observe(observability.Observation{Stage: observability.StageObjectCatalog,
		ObjectCatalog: &observability.ObjectCatalogFacts{Operation: "renew", Result: "success", RenewedObjects: 5, RenewedContexts: 11}})
	observe(observability.Observation{Stage: observability.StageObjectCatalog,
		ObjectCatalog: &observability.ObjectCatalogFacts{Operation: "write", Result: "success", RenewedObjects: 13, RenewedContexts: 17}})
	for cell, want := range map[[2]string]float64{
		{"timeline", "aged"}: 5, {"timeline", "first"}: 7, {"object", "periodic"}: 5, {"context", "periodic"}: 11,
	} {
		if got := testutil.ToFloat64(r.phaseTwo.catalogRenewals.WithLabelValues(cell[0], cell[1])); got != want {
			t.Fatalf("catalog_renewals_total%v = %v, want %v", cell, got, want)
		}
	}
}
