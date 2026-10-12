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
	"reflect"
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
	if groups["prometheus/time_series"] != 0 {
		t.Fatalf("prometheus/time_series=%v, want a pre-created 0", groups["prometheus/time_series"])
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

// The global families as the first reading after global strategies reach a
// deployment reads them: the three outcomes, all present, adding up to the
// global strategies; the refused ones by word and query source, adding up to
// global_unsupported.
func TestCatalogCompositionReportsGlobalStrategiesByOutcomeAndRefusal(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	composition := controlplane.ComposeCatalog(controlplane.Catalog{
		Dispositions: []controlplane.ObjectDisposition{{SourceID: "1", Scope: "PLAN", Disposition: controlplane.DispositionAccepted}},
		GlobalStrategies: []controlplane.GlobalStrategy{
			{SourceID: "1", QuerySource: "bk_monitor/time_series"},
			{SourceID: "2", Refusal: controlplane.GlobalBusinessQueryKind, QuerySource: "bk_monitor/log"},
			{SourceID: "3", Refusal: controlplane.GlobalBusinessQueryKind, QuerySource: "bk_monitor/log"},
			{SourceID: "4", Refusal: controlplane.GlobalBusinessQueryKind, QuerySource: controlplane.GlobalQuerySourcePromQL},
			{SourceID: "5", Refusal: controlplane.GlobalBusinessQueryTable, QuerySource: "bk_monitor/time_series"},
			{SourceID: "6"},
		}})
	r.SetCatalogCompositionSource(func() *controlplane.CatalogComposition { return &composition })

	outcomes := compositionSeries(t, r, "bkmonitor_alarmd_catalog_global_strategies", "outcome")
	want := map[string]float64{"accepted": 1, "global_unsupported": 4, "withheld": 1}
	if !reflect.DeepEqual(outcomes, want) {
		t.Fatalf("outcomes = %v, want %v", outcomes, want)
	}
	pairs := map[string]float64{}
	sum := 0.0
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_catalog_global_strategies_unsupported") {
		labels := map[string]string{}
		for _, l := range m.Label {
			labels[l.GetName()] = l.GetValue()
		}
		pairs[labels["reason"]+"|"+labels["source_semantics"]] = m.GetGauge().GetValue()
		sum += m.GetGauge().GetValue()
	}
	wantPairs := map[string]float64{"query_kind|bk_monitor/log": 2, "query_kind|promql": 1, "query_table|bk_monitor/time_series": 1}
	if !reflect.DeepEqual(pairs, wantPairs) || sum != outcomes["global_unsupported"] {
		t.Fatalf("refused pairs = %v adding up to %v, want %v adding up to global_unsupported", pairs, sum, wantPairs)
	}

	// No global strategy: the outcomes read zero and are there to read.
	empty := controlplane.ComposeCatalog(controlplane.Catalog{})
	r.SetCatalogCompositionSource(func() *controlplane.CatalogComposition { return &empty })
	zero := map[string]float64{"accepted": 0, "global_unsupported": 0, "withheld": 0}
	if got := compositionSeries(t, r, "bkmonitor_alarmd_catalog_global_strategies", "outcome"); !reflect.DeepEqual(got, zero) {
		t.Fatalf("outcomes without global strategies = %v, want %v", got, zero)
	}
}
