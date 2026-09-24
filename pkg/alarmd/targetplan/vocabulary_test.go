// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package targetplan_test

import (
	"reflect"
	"sort"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// The words themselves, written out. Every other test in this tree names
// them through the constants, so renaming a constant is caught everywhere
// and changing its value is caught nowhere - yet the value is what the
// object page shows, the target_resolved line prints, and an operator types
// into a Prometheus query. A word changed here is a page and a dashboard
// changed, so it is pinned by the string, not by the name. Compared as
// sets: no consumer reads these lists in order, so reordering one changes
// nothing and must not fail here.
func TestTheClosedWordsArePinnedByTheirSpelling(t *testing.T) {
	if got, want := sorted(targetplan.SelectorKindStatic, targetplan.SelectorKindGroup, targetplan.SelectorKindTopology),
		sorted("static", "dynamic_group", "dynamic_topology"); !reflect.DeepEqual(got, want) {
		t.Fatalf("selector kinds = %v, want %v", got, want)
	}
	states := make([]string, 0, len(targetplan.SelectorStates))
	for _, state := range targetplan.SelectorStates {
		states = append(states, string(state))
	}
	if got, want := sorted(states...), sorted("OK", "OKEmpty", "Incomplete", "Unavailable"); !reflect.DeepEqual(got, want) {
		t.Fatalf("selector states = %v, want %v", got, want)
	}
	resolutions := make([]string, 0, len(targetplan.ResolutionStates))
	for _, state := range targetplan.ResolutionStates {
		resolutions = append(resolutions, string(state))
	}
	if got, want := sorted(resolutions...), sorted("Complete", "Incomplete", "Unavailable"); !reflect.DeepEqual(got, want) {
		t.Fatalf("resolution states = %v, want %v", got, want)
	}
	if got, want := sorted(targetplan.SelectorReasons...), sorted(
		"none", "key_missing", "json_invalid", "structure_invalid", "model_mismatch", "read_failed",
		"stale", "index_unavailable", "node_missing", "node_in_other_business", "members_dropped", "source_unwired",
		"model_representation_unresolved",
	); !reflect.DeepEqual(got, want) {
		t.Fatalf("selector reasons = %v, want %v", got, want)
	}
}

func sorted(words ...string) []string {
	out := append([]string(nil), words...)
	sort.Strings(out)
	return out
}
