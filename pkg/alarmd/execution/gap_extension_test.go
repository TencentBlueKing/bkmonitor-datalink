// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"reflect"
	"testing"
)

func TestSameSlotGapExtensionIsMonotone(t *testing.T) {
	base := GapScopeState{Scope: GapScope{HasLevel: true, LevelID: 1}, Status: GapStatusWarming, ReasonCode: "HISTORY_GAPPED", RequiredFullSlots: 9, ObservedFullSlots: 1}
	for _, tc := range []struct {
		name     string
		kind     GapMutationKind
		required uint32
		reason   ReasonCode
		scope    GapScope
		ok       bool
	}{
		{"add Plan", GapOpen, 9, "SNAPSHOT_UNAVAILABLE", GapScope{}, true},
		{"strengthen status", GapStrengthen, 9, base.ReasonCode, base.Scope, true},
		{"raise requirement", GapStrengthen, 10, base.ReasonCode, base.Scope, true},
		{"lower requirement", GapStrengthen, 8, base.ReasonCode, base.Scope, false},
		{"change reason", GapStrengthen, 9, "CONFIG_DRIFT", base.Scope, false},
		{"clear", GapClear, 0, "", base.Scope, false},
		{"warmup", GapWarmup, 9, base.ReasonCode, base.Scope, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := []GapScopeState{base}
			next, ok := ExtendSameSlotGap(old, []GapScopeMutation{{Scope: tc.scope, Kind: tc.kind, RequiredFullSlots: tc.required, ReasonCode: tc.reason}})
			if ok != tc.ok {
				t.Fatalf("ok=%t want=%t", ok, tc.ok)
			}
			if old[0] != base {
				t.Fatal("input mutated")
			}
			if ok {
				for _, s := range next {
					if s.Scope == base.Scope {
						if s.ObservedFullSlots != 1 || s.ReasonCode != base.ReasonCode || s.RequiredFullSlots < 9 {
							t.Fatalf("lost evidence: %+v", s)
						}
						if !tc.scope.HasLevel && !reflect.DeepEqual(s, base) {
							t.Fatalf("adding Plan changed Level: %+v", s)
						}
					}
				}
			}
		})
	}
}
