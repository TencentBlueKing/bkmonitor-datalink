// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"testing"
)

func gatherLocalView(t *testing.T, source func() LocalViewCounts) map[string]map[string]float64 {
	t.Helper()
	recorder := NewRecorder(BuildInfo{})
	recorder.SetLocalViewSource(source)
	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	gathered := map[string]map[string]float64{}
	for _, family := range families {
		for _, series := range family.Metric {
			key := ""
			for _, pair := range series.Label {
				key += pair.GetValue()
			}
			if gathered[family.GetName()] == nil {
				gathered[family.GetName()] = map[string]float64{}
			}
			gathered[family.GetName()][key] = series.GetGauge().GetValue()
		}
	}
	return gathered
}

// The Worker's view is published as it was measured: the two kinds of
// content apart, because the design sizes the delta by the largest object and
// the buffer by their sum, and the Query Group count beside them so the sum
// can be read per Query Group and against the owned count.
func TestTheLocalViewIsPublishedByKindWithItsQueryGroupCount(t *testing.T) {
	gathered := gatherLocalView(t, func() LocalViewCounts {
		return LocalViewCounts{QueryGroups: 601, ObjectBytes: 3_100_000, OutputContextBytes: 3_300_000}
	})
	bytes := gathered["bkmonitor_alarmd_local_view_object_bytes"]
	if bytes["query_group"] != 3_100_000 || bytes["output_context"] != 3_300_000 || len(bytes) != 2 {
		t.Fatalf("local_view_object_bytes = %v, want query_group 3100000 and output_context 3300000", bytes)
	}
	if got := gathered["bkmonitor_alarmd_local_view_query_groups"][""]; got != 601 {
		t.Fatalf("local_view_query_groups = %v, want 601", got)
	}
	// An idle Worker is a Worker with an empty view, and says so.
	idle := gatherLocalView(t, func() LocalViewCounts { return LocalViewCounts{} })
	if idle["bkmonitor_alarmd_local_view_object_bytes"]["query_group"] != 0 || idle["bkmonitor_alarmd_local_view_query_groups"][""] != 0 ||
		len(idle["bkmonitor_alarmd_local_view_object_bytes"]) != 2 {
		t.Fatalf("an idle Worker published %v / %v, want zeros on every series",
			idle["bkmonitor_alarmd_local_view_object_bytes"], idle["bkmonitor_alarmd_local_view_query_groups"])
	}
}

// A process without a Worker role has no view to report, and no series: a
// zero here would read as a Worker that owns nothing.
func TestAProcessWithoutAWorkerRolePublishesNoLocalView(t *testing.T) {
	gathered := gatherLocalView(t, nil)
	for _, name := range []string{"bkmonitor_alarmd_local_view_object_bytes", "bkmonitor_alarmd_local_view_query_groups"} {
		if series, ok := gathered[name]; ok {
			t.Errorf("%s published %v without a Worker role", name, series)
		}
	}
}
