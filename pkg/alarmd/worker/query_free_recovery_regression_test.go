// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A prior attempt already counted a full observation for this same Slot.
// Finalization must not erase that evidence or leave Progress permanently stuck.
func TestQueryFreeFinalizationPreservesObservedProtection(t *testing.T) {
	for _, required := range []uint32{9, 10} {
		activation := activePlanResult("state-v2", 2)
		activation.Facts[0].Selected.RequiredFullSlots = 9
		selected := activation.Facts[0].Selected
		fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activation})
		marker := queryFreeGapMarker(t, selected, currentQueryFreeApplyVersion(t), selected.ScheduleRevision,
			[]execution.GapScopeState{{Scope: execution.GapScope{}, Status: execution.GapStatusGapped,
				ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped), RequiredFullSlots: required, ObservedFullSlots: 1}})
		before := cloneGapGuardSnapshot(marker)
		fixture.ports.markers[marker.Identity] = marker
		result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
		if err != nil || !result.Completed || fixture.ports.progressCalls != 1 {
			t.Fatalf("required=%d: result=%+v err=%v progress=%d", required, result, err, fixture.ports.progressCalls)
		}
		if fixture.ports.applyCalls != 0 || !reflect.DeepEqual(before, fixture.ports.markers[marker.Identity]) {
			t.Fatal("reusing same-Slot protection changed persisted evidence")
		}
	}
}

// A committed level-only statement also wins over a retry proposal for this Slot.
func TestQueryFreeFinalizationPreservesCommittedLevelProtection(t *testing.T) {
	activation := activePlanResult("state-v2", 2)
	activation.Facts[0].Selected.RequiredFullSlots = 9
	selected := activation.Facts[0].Selected
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activation})
	marker := queryFreeGapMarker(t, selected, currentQueryFreeApplyVersion(t), selected.ScheduleRevision,
		[]execution.GapScopeState{
			{Scope: execution.GapScope{HasLevel: true, LevelID: 1}, Status: execution.GapStatusGapped, ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 3},
			{Scope: execution.GapScope{HasLevel: true, LevelID: 2}, Status: execution.GapStatusWarming, ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 4, ObservedFullSlots: 1},
		})
	fixture.ports.markers[marker.Identity] = marker
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || !result.Completed || fixture.ports.progressCalls != 1 {
		t.Fatalf("result=%+v err=%v progress=%d", result, err, fixture.ports.progressCalls)
	}
	if fixture.ports.applyCalls != 0 || !reflect.DeepEqual(marker, fixture.ports.markers[marker.Identity]) {
		t.Fatal("same-Slot level protection was rewritten")
	}
}

func TestQueryFreeFinalizationReusesWarmingProtection(t *testing.T) {
	activation := activePlanResult("state-v2", 2)
	activation.Facts[0].Selected.RequiredFullSlots = 9
	selected := activation.Facts[0].Selected
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activation})
	marker := queryFreeGapMarker(t, selected, currentQueryFreeApplyVersion(t), selected.ScheduleRevision,
		[]execution.GapScopeState{{Scope: execution.GapScope{}, Status: execution.GapStatusWarming,
			ReasonCode: execution.ReasonCode(contract.ReasonGapSkipped), RequiredFullSlots: 9, ObservedFullSlots: 1}})
	before := cloneGapGuardSnapshot(marker)
	fixture.ports.markers[marker.Identity] = marker
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || !result.Completed || fixture.ports.progressCalls != 1 {
		t.Fatalf("warming finalization: result=%+v err=%v progress=%d", result, err, fixture.ports.progressCalls)
	}
	if fixture.ports.applyCalls != 0 || !reflect.DeepEqual(before, fixture.ports.markers[marker.Identity]) {
		t.Fatal("reusing WARMING protection must leave persisted evidence unchanged")
	}
}
