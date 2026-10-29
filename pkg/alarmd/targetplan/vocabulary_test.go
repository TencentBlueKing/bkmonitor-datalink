// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package targetplan_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

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
	if got, want := sorted(targetplan.SelectorKindStatic, targetplan.SelectorKindGroup, targetplan.SelectorKindTopology, targetplan.SelectorKindExclude),
		sorted("static", "dynamic_group", "dynamic_topology", "exclude"); !reflect.DeepEqual(got, want) {
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
		"model_representation_unresolved", "tenant_mismatch", "address_unresolved", "address_ambiguous",
		"index_incomplete", "excluded_absent",
	); !reflect.DeepEqual(got, want) {
		t.Fatalf("selector reasons = %v, want %v", got, want)
	}
	if got, want := sorted(targetplan.DroppedMemberReasons...), sorted(
		"member_invalid", "member_unlisted", "member_lost", "model_mismatch", "no_host_id",
		"host_unindexed", "host_other_tenant", "address_shared",
	); !reflect.DeepEqual(got, want) {
		t.Fatalf("dropped member reasons = %v, want %v", got, want)
	}
}

// A named id is the source's text written into "<id>:<reason>" names joined
// by commas on one line: whatever would split a name or end the field
// becomes "_", a long id keeps its head, and an empty one is "-". The cut
// can split a multi-byte rune; its bytes become "_", so the line stays
// valid UTF-8.
func TestADroppedMembersIDCannotBreakItsName(t *testing.T) {
	long := strings.Repeat("9", targetplan.MaxDroppedMemberIDBytes+10)
	for id, want := range map[string]string{
		"701":                         "701",
		"a,b:c d=e\"f":                "a_b_c_d_e_f",
		"":                            "-",
		long:                          long[:targetplan.MaxDroppedMemberIDBytes],
		"主机1":                         "______1",
		strings.Repeat("a", 63) + "主": strings.Repeat("a", 63) + "_",
	} {
		named := targetplan.NameDropped(nil, id, targetplan.DroppedMemberLost)
		if len(named) != 1 || named[0].ID != want || named[0].Reason != targetplan.DroppedMemberLost || !utf8.ValidString(named[0].ID) {
			t.Fatalf("NameDropped(%q) = %+v, want id %q", id, named, want)
		}
	}
	full := make([]targetplan.DroppedMember, targetplan.MaxNamedDroppedMembers)
	if named := targetplan.NameDropped(full, "1", targetplan.DroppedMemberLost); len(named) != targetplan.MaxNamedDroppedMembers {
		t.Fatalf("a full list grew to %d", len(named))
	}
}

func sorted(words ...string) []string {
	out := append([]string(nil), words...)
	sort.Strings(out)
	return out
}
