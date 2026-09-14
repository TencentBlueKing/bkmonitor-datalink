// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The list the composition reports under and the list the compiler compiles
// from are the same list. If they were two, a source could compile and have
// no name in the composition -- reading as if it were not running -- or be
// named and never compile, reading as a source with nothing configured. Both
// answer the question "which data sources are onboarded" wrongly, in
// opposite directions.
func TestSupportedSourceSemanticsIsWhatTheCompilerAccepts(t *testing.T) {
	for _, semantics := range SupportedSourceSemantics {
		source, dataType, found := strings.Cut(semantics, "/")
		if !found {
			t.Fatalf("%q is not a data source label and a data type label", semantics)
		}
		if !pollingSourceSupported(legacyQueryConfig{DataSourceLabel: source, DataTypeLabel: dataType}) {
			t.Fatalf("the composition names %q but the compiler refuses it", semantics)
		}
	}
	if pollingSourceSupported(legacyQueryConfig{DataSourceLabel: "nobody", DataTypeLabel: "nothing"}) {
		t.Fatal("the compiler accepts a source the composition does not name")
	}
}

// A Query Group falls under exactly one label, so the family adds up. The
// empty list is the legacy compiler's way of saying plain time series, not
// its way of saying unknown, and a group with two sources is one entry, not
// two -- counting it under each would make the family look addable while it
// silently over-counted.
func TestSourceSemanticsLabelIsOnePartitionKey(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		semantics []string
		want      string
	}{
		{"empty is plain time series", nil, "bk_monitor/time_series"},
		{"one source", []string{"bk_monitor/log"}, "bk_monitor/log"},
		{"two sources are one mixed entry", []string{"custom/event", "bk_monitor/log"}, SourceSemanticsMixed},
		{"order does not change the key", []string{"bk_monitor/log", "custom/event"}, SourceSemanticsMixed},
		{"a repeat is still one source", []string{"bk_monitor/log", "bk_monitor/log"}, "bk_monitor/log"},
		{"an unnamed source is other, not its own label", []string{"nobody/nothing"}, SourceSemanticsOther},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := SourceSemanticsLabel(testCase.semantics); got != testCase.want {
				t.Fatalf("SourceSemanticsLabel(%v)=%q, want %q", testCase.semantics, got, testCase.want)
			}
		})
	}
}

func compositionTestCatalog() Catalog {
	group := func(semantics []string, plans int) QueryGroup {
		frozen := make([]FrozenPlan, plans)
		return QueryGroup{QueryPlan: execution.QueryPlanFacts{SourceSemantics: semantics}, Plans: frozen}
	}
	return Catalog{
		QueryGroups: []QueryGroup{
			group(nil, 3),
			group([]string{"bk_monitor/log"}, 2),
			group([]string{"bk_monitor/log"}, 1),
			group([]string{"custom/event", "bk_monitor/log"}, 1),
		},
		Dispositions: []ObjectDisposition{
			{SourceID: "1", Disposition: DispositionAccepted, Reason: "AUDIT_ONLY"},
			{SourceID: "2", Disposition: DispositionUnsupported, Reason: "QUERY_SOURCE_NOT_MIGRATED"},
			{SourceID: "3", Disposition: DispositionUnsupported, Reason: "QUERY_SOURCE_NOT_MIGRATED"},
			{SourceID: "4", Disposition: DispositionConfigRejected, Reason: "PLAN_INVALID"},
			{SourceID: "5", Disposition: Disposition("A_DISPOSITION_NOBODY_LISTED"), Reason: "WHATEVER"},
		},
	}
}

// Both families are partitions: the Query Groups add up to the Catalog's,
// the Plans to the Catalog's, and the objects to the dispositions recorded.
// Each supported source is present at zero, because "this source compiles
// nothing" and "nobody asked about this source" must not read the same --
// that equivalence is the whole reason this exists.
func TestComposeCatalogPartitionsAndPreCreatesEverySupportedSource(t *testing.T) {
	composition := ComposeCatalog(compositionTestCatalog())

	for _, semantics := range SupportedSourceSemantics {
		if _, present := composition.QueryGroups[semantics]; !present {
			t.Fatalf("%q has no Query Group series: a source with none reads as absent", semantics)
		}
		if _, present := composition.Plans[semantics]; !present {
			t.Fatalf("%q has no Plan series", semantics)
		}
	}
	if composition.QueryGroups["bk_fta/event"] != 0 {
		t.Fatalf("bk_fta/event=%d, want a pre-created zero", composition.QueryGroups["bk_fta/event"])
	}

	groups := 0
	for _, count := range composition.QueryGroups {
		groups += count
	}
	if groups != 4 {
		t.Fatalf("Query Groups sum to %d, want the Catalog's 4", groups)
	}
	plans := 0
	for _, count := range composition.Plans {
		plans += count
	}
	if plans != 7 {
		t.Fatalf("Plans sum to %d, want the Catalog's 7", plans)
	}
	if composition.QueryGroups["bk_monitor/time_series"] != 1 || composition.Plans["bk_monitor/time_series"] != 3 {
		t.Fatalf("the empty-semantics group is not counted as plain time series: %v", composition.QueryGroups)
	}
	if composition.QueryGroups["bk_monitor/log"] != 2 || composition.Plans["bk_monitor/log"] != 3 {
		t.Fatalf("bk_monitor/log groups=%d plans=%d", composition.QueryGroups["bk_monitor/log"], composition.Plans["bk_monitor/log"])
	}
	if composition.QueryGroups[SourceSemanticsMixed] != 1 {
		t.Fatalf("the two-source group is not counted once under mixed: %v", composition.QueryGroups)
	}

	objects := 0
	for _, count := range composition.Objects {
		objects += count
	}
	if objects != 5 {
		t.Fatalf("objects sum to %d, want the 5 dispositions recorded", objects)
	}
	if composition.Objects[DispositionUnsupported] != 2 || composition.Objects[DispositionAccepted] != 1 {
		t.Fatalf("objects by disposition=%v", composition.Objects)
	}
	if composition.Objects[DispositionOther] != 1 {
		t.Fatalf("a disposition the list does not name was dropped instead of counted under other: %v", composition.Objects)
	}
	if composition.Objects[DispositionRemoved] != 0 {
		t.Fatalf("REMOVED=%d, want a pre-created zero", composition.Objects[DispositionRemoved])
	}
}
