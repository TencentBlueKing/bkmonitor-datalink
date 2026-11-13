// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
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
		// The status still rises; the fewer FULL Slots and the reason of the
		// same rank are not taken.
		{"lower requirement", GapStrengthen, 8, base.ReasonCode, base.Scope, true},
		{"change reason", GapStrengthen, 9, "CONFIG_DRIFT", base.Scope, true},
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
						if s.ObservedFullSlots != 1 || s.ReasonCode != base.ReasonCode || s.RequiredFullSlots != max(9, tc.required) {
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

// A held scope's reason only rises within a Slot: PARTIAL answered
// UNAVAILABLE becomes UNAVAILABLE, its observations kept; UNAVAILABLE
// answered PARTIAL, or by another reason of its rank, is no growth.
func TestASameSlotScopesReasonOnlyRises(t *testing.T) {
	scope := GapScope{HasLevel: true, LevelID: 1}
	held := func(reason ReasonCode) []GapScopeState {
		return []GapScopeState{{Scope: scope, Status: GapStatusGapped, ReasonCode: reason, RequiredFullSlots: 3, ObservedFullSlots: 1}}
	}
	answer := func(reason ReasonCode) []GapScopeMutation {
		return []GapScopeMutation{{Scope: scope, Kind: GapOpen, ReasonCode: reason, RequiredFullSlots: 3}}
	}
	partial, unavailable := ReasonCode(contract.ReasonQueryPartial), ReasonCode(contract.ReasonQueryUnavailable)
	next, ok := ExtendSameSlotGap(held(partial), answer(unavailable))
	if !ok || len(next) != 1 || next[0].ReasonCode != unavailable || next[0].ObservedFullSlots != 1 || next[0].RequiredFullSlots != 3 {
		t.Fatalf("PARTIAL answered UNAVAILABLE = %+v, %t; want the scope UNAVAILABLE, its observations kept", next, ok)
	}
	if next, ok := ExtendSameSlotGap(held(unavailable), answer(partial)); ok {
		t.Fatalf("UNAVAILABLE answered PARTIAL grew the marker to %+v", next)
	}
	if next, ok := ExtendSameSlotGap(held(unavailable), answer(ReasonCode(contract.ReasonQueryTimeout))); ok {
		t.Fatalf("UNAVAILABLE answered by another reason of its rank grew the marker to %+v", next)
	}
	fewer := answer(unavailable)
	fewer[0].RequiredFullSlots = 2
	if next, ok := ExtendSameSlotGap(held(unavailable), fewer); ok {
		t.Fatalf("a held scope asked fewer FULL Slots grew the marker to %+v", next)
	}
}
