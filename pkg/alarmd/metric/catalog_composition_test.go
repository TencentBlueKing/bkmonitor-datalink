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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

func compositionSeries(t *testing.T, r *Recorder, family, label string) map[string]float64 {
	t.Helper()
	got := map[string]float64{}
	for _, m := range gatherFamily(t, r, family) {
		key := ""
		for _, l := range m.Label {
			if l.GetName() == label {
				key = l.GetValue()
			}
		}
		got[key] = m.GetGauge().GetValue()
	}
	return got
}

// A replica that builds no Catalog reports no composition. Zeros there would
// say every data source has nothing, which on a follower -- every replica but
// one -- would be a deployment-wide alarm made of nothing.
func TestCatalogCompositionIsSilentWithoutABuiltCatalog(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	if got := compositionSeries(t, r, "bkmonitor_alarmd_catalog_query_groups", "source_semantics"); len(got) != 0 {
		t.Fatalf("unbound collector emitted %v", got)
	}
	r.SetCatalogCompositionSource(func() *controlplane.CatalogComposition { return nil })
	if got := compositionSeries(t, r, "bkmonitor_alarmd_catalog_query_groups", "source_semantics"); len(got) != 0 {
		t.Fatalf("a replica with no Catalog emitted %v", got)
	}
}

// The leader reports the partition, every supported source included, so a
// source with no Query Groups reads as zero. That distinction is the point:
// before this, "the polling sources compile nothing" and "nothing compiles at
// all" were the same reading, and the second one was true for days.
func TestCatalogCompositionReportsEverySupportedSource(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	composition := controlplane.ComposeCatalog(controlplane.Catalog{
		Dispositions: []controlplane.ObjectDisposition{
			{SourceID: "7", Disposition: controlplane.DispositionUnsupported, Reason: "QUERY_SOURCE_NOT_MIGRATED"},
		},
	})
	composition.QueryGroups["bk_monitor/log"] = 12
	composition.Plans["bk_monitor/log"] = 30
	r.SetCatalogCompositionSource(func() *controlplane.CatalogComposition { return &composition })

	groups := compositionSeries(t, r, "bkmonitor_alarmd_catalog_query_groups", "source_semantics")
	for _, semantics := range controlplane.SupportedSourceSemantics {
		if _, present := groups[semantics]; !present {
			t.Fatalf("%q has no series: a source that compiles nothing must read as zero", semantics)
		}
	}
	if groups["bk_monitor/log"] != 12 {
		t.Fatalf("bk_monitor/log=%v, want 12", groups["bk_monitor/log"])
	}
	if groups["bk_fta/event"] != 0 {
		t.Fatalf("bk_fta/event=%v, want a pre-created 0", groups["bk_fta/event"])
	}
	if plans := compositionSeries(t, r, "bkmonitor_alarmd_catalog_plans", "source_semantics"); plans["bk_monitor/log"] != 30 {
		t.Fatalf("bk_monitor/log plans=%v, want 30", plans["bk_monitor/log"])
	}

	objects := compositionSeries(t, r, "bkmonitor_alarmd_catalog_objects", "disposition")
	if objects[string(controlplane.DispositionUnsupported)] != 1 {
		t.Fatalf("objects=%v", objects)
	}
	if _, present := objects[string(controlplane.DispositionRemoved)]; !present {
		t.Fatal("REMOVED has no series: the disposition partition must be complete")
	}
}
