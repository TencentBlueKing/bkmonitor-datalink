// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

func TestSlotExecutionCoordinatorFinalizesSnapshotUnavailableWithoutQuery(t *testing.T) {
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activePlanResult("state-v2", 2)})
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{
		"finalization", "activation", "sequence", "query_free_admission_guard", "query_free_gap_load",
		"query_free_gap_apply", "activation", "query_free_admission_progress", "progress_commit",
	})
	if len(fixture.ports.mutations) != 1 {
		t.Fatalf("gap mutations=%d, want=1", len(fixture.ports.mutations))
	}
	mutation := fixture.ports.mutations[0]
	if mutation.Identity.Plan != planIdentity() || mutation.Identity.StateGeneration != "state-v2" ||
		mutation.ApplyVersion.StateApplyEpoch != 2 || mutation.ScheduleRevision != "plan-schedule-v2" ||
		len(mutation.Scopes) != 1 || mutation.Scopes[0].Scope != (execution.GapScope{}) ||
		mutation.Scopes[0].Kind != execution.GapOpen ||
		mutation.Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonSnapshotUnavailable) ||
		mutation.Scopes[0].RequiredFullSlots != 3 {
		t.Fatalf("unexpected query-free mutation: %+v", mutation)
	}
	completion := fixture.ports.lastProgress.Completion
	if completion.Kind != execution.CompletionSnapshotUnavailable || completion.Primary != nil ||
		completion.Result != observability.ResultDegraded ||
		completion.ReasonCode != execution.ReasonCode(contract.ReasonSnapshotUnavailable) {
		t.Fatalf("unexpected query-free completion: %+v", completion)
	}
	lastObservation := (*fixture.observations)[len(*fixture.observations)-1]
	if lastObservation.Stage != observability.StageProgressCommitted ||
		lastObservation.Result != observability.ResultDegraded ||
		lastObservation.ReasonCode != execution.ReasonCode(contract.ReasonSnapshotUnavailable) {
		t.Fatalf("query-free completion observation=%+v", lastObservation)
	}
}

func TestSlotExecutionCoordinatorReusesSufficientQueryFreeGapWithoutRewrite(t *testing.T) {
	activation := activePlanResult("state-v2", 2)
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activation})
	marker := queryFreeGapMarker(t, activation.Facts[0].Selected, currentQueryFreeApplyVersion(t), "plan-schedule-v2", []execution.GapScopeState{
		{
			Scope: execution.GapScope{}, Status: execution.GapStatusGapped,
			ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift), RequiredFullSlots: 3,
		},
		{
			Scope: execution.GapScope{LevelID: 5, HasLevel: true}, Status: execution.GapStatusWarming,
			ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 4, ObservedFullSlots: 1,
		},
	})
	before := cloneGapGuardSnapshot(marker)
	fixture.ports.markers[marker.Identity] = marker

	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{
		"finalization", "activation", "sequence", "query_free_admission_guard", "query_free_gap_load",
		"activation", "query_free_admission_progress", "progress_commit",
	})
	if fixture.ports.applyCalls != 0 || len(fixture.ports.mutations) != 0 || fixture.ports.progressCalls != 1 {
		t.Fatalf("query-free reuse apply=%d mutations=%d progress=%d",
			fixture.ports.applyCalls, len(fixture.ports.mutations), fixture.ports.progressCalls)
	}
	if fixture.ports.eventCount != 0 || fixture.ports.stateApplyCalls != 0 {
		t.Fatalf("query-free reuse leaked business effects: events=%d state=%d",
			fixture.ports.eventCount, fixture.ports.stateApplyCalls)
	}
	if got := fixture.ports.markers[marker.Identity]; !reflect.DeepEqual(got, before) {
		t.Fatalf("existing Guard changed: got=%+v want=%+v", got, before)
	}
	completion := fixture.ports.lastProgress.Completion
	if completion.Kind != execution.CompletionSnapshotUnavailable ||
		completion.ReasonCode != execution.ReasonCode(contract.ReasonSnapshotUnavailable) {
		t.Fatalf("query-free completion=%+v", completion)
	}
}

func TestSlotExecutionCoordinatorDoesNotReuseInsufficientSameSlotGap(t *testing.T) {
	activation := activePlanResult("state-v2", 2)
	selected := activation.Facts[0].Selected
	version := currentQueryFreeApplyVersion(t)
	planWide := func(status execution.GapStatus, required, observed uint32) []execution.GapScopeState {
		return []execution.GapScopeState{{
			Scope: execution.GapScope{}, Status: status,
			ReasonCode:        execution.ReasonCode(contract.ReasonConfigDrift),
			RequiredFullSlots: required, ObservedFullSlots: observed,
		}}
	}
	tests := []struct {
		name   string
		marker func(*testing.T) execution.GapGuardSnapshot
	}{
		{
			name: "different schedule revision",
			marker: func(t *testing.T) execution.GapGuardSnapshot {
				return queryFreeGapMarker(t, selected, version, "different-plan-schedule", planWide(execution.GapStatusGapped, 3, 0))
			},
		},
		{
			name: "plan scope warming",
			marker: func(t *testing.T) execution.GapGuardSnapshot {
				return queryFreeGapMarker(t, selected, version, selected.ScheduleRevision, planWide(execution.GapStatusWarming, 3, 1))
			},
		},
		{
			name: "observed full slot",
			marker: func(t *testing.T) execution.GapGuardSnapshot {
				return queryFreeGapMarker(t, selected, version, selected.ScheduleRevision, planWide(execution.GapStatusGapped, 3, 1))
			},
		},
		{
			name: "lower required full slots",
			marker: func(t *testing.T) execution.GapGuardSnapshot {
				return queryFreeGapMarker(t, selected, version, selected.ScheduleRevision, planWide(execution.GapStatusGapped, 2, 0))
			},
		},
		{
			name: "higher required full slots",
			marker: func(t *testing.T) execution.GapGuardSnapshot {
				return queryFreeGapMarker(t, selected, version, selected.ScheduleRevision, planWide(execution.GapStatusGapped, 4, 0))
			},
		},
		{
			name: "level scope only",
			marker: func(t *testing.T) execution.GapGuardSnapshot {
				return queryFreeGapMarker(t, selected, version, selected.ScheduleRevision, []execution.GapScopeState{{
					Scope: execution.GapScope{LevelID: 5, HasLevel: true}, Status: execution.GapStatusGapped,
					ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift), RequiredFullSlots: 3,
				}})
			},
		},
		{
			name: "same version tombstone",
			marker: func(t *testing.T) execution.GapGuardSnapshot {
				marker := queryFreeGapMarker(t, selected, version, selected.ScheduleRevision, planWide(execution.GapStatusGapped, 3, 0))
				marker.Status = execution.GapClearedTombstone
				marker.Scopes = nil
				return marker
			},
		},
		{
			name: "newer slot digest",
			marker: func(t *testing.T) execution.GapGuardSnapshot {
				newer := version
				newer.SlotDigest = execution.SlotIdentityDigest(strings.Repeat("f", 64))
				return queryFreeGapMarker(t, selected, newer, selected.ScheduleRevision, planWide(execution.GapStatusGapped, 3, 0))
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activation})
			marker := test.marker(t)
			fixture.ports.markers[marker.Identity] = marker
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
			if err == nil || result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			if fixture.ports.applyCalls != 0 || len(fixture.ports.mutations) != 0 || fixture.ports.progressCalls != 0 {
				t.Fatalf("insufficient Guard apply=%d mutations=%d progress=%d",
					fixture.ports.applyCalls, len(fixture.ports.mutations), fixture.ports.progressCalls)
			}
		})
	}
}

func TestSlotExecutionCoordinatorKeepsExistingGapWritePaths(t *testing.T) {
	activation := activePlanResult("state-v2", 2)
	selected := activation.Facts[0].Selected
	version := currentQueryFreeApplyVersion(t)
	oldVersion := version
	oldVersion.StateApplyEpoch--
	olderSlotDigest := version
	olderSlotDigest.SlotDigest = execution.SlotIdentityDigest(strings.Repeat("0", 64))
	tests := []struct {
		name       string
		seedMarker func(*testing.T, *queryFreePorts)
	}{
		{name: "missing"},
		{
			name: "older version",
			seedMarker: func(t *testing.T, ports *queryFreePorts) {
				marker := queryFreeGapMarker(t, selected, oldVersion, selected.ScheduleRevision, []execution.GapScopeState{{
					Scope: execution.GapScope{}, Status: execution.GapStatusGapped,
					ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift), RequiredFullSlots: 3,
				}})
				ports.markers[marker.Identity] = marker
			},
		},
		{
			name: "older slot digest",
			seedMarker: func(t *testing.T, ports *queryFreePorts) {
				marker := queryFreeGapMarker(t, selected, olderSlotDigest, selected.ScheduleRevision, []execution.GapScopeState{{
					Scope: execution.GapScope{}, Status: execution.GapStatusGapped,
					ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift), RequiredFullSlots: 3,
				}})
				ports.markers[marker.Identity] = marker
			},
		},
		{
			name: "different state generation is missing for selected Plan",
			seedMarker: func(t *testing.T, ports *queryFreePorts) {
				other := selected
				other.StateGeneration = "other-generation"
				marker := queryFreeGapMarker(t, other, version, other.ScheduleRevision, []execution.GapScopeState{{
					Scope: execution.GapScope{}, Status: execution.GapStatusGapped,
					ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift), RequiredFullSlots: 3,
				}})
				ports.markers[marker.Identity] = marker
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activation})
			if test.seedMarker != nil {
				test.seedMarker(t, fixture.ports)
			}
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
			if err != nil || !result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			if fixture.ports.applyCalls != 1 || len(fixture.ports.mutations) != 1 || fixture.ports.progressCalls != 1 {
				t.Fatalf("existing write path apply=%d mutations=%d progress=%d",
					fixture.ports.applyCalls, len(fixture.ports.mutations), fixture.ports.progressCalls)
			}
		})
	}
}

func TestSlotExecutionCoordinatorDoesNotReuseGapForGapSkipped(t *testing.T) {
	activation := activePlanResult("state-v2", 2)
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activation})
	selected := activation.Facts[0].Selected
	marker := queryFreeGapMarker(t, selected, currentQueryFreeApplyVersion(t), selected.ScheduleRevision, []execution.GapScopeState{{
		Scope: execution.GapScope{}, Status: execution.GapStatusGapped,
		ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift), RequiredFullSlots: 3,
	}})
	fixture.ports.markers[marker.Identity] = marker
	fixture.ports.finalization.Mode = execution.FinalizationGapSkipped
	fixture.ports.finalization.ReasonCode = execution.ReasonCode(contract.ReasonGapSkipped)

	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err == nil || result.Completed || fixture.ports.progressCalls != 0 {
		t.Fatalf("Execute() result=%+v error=%v progress=%d", result, err, fixture.ports.progressCalls)
	}
}

func TestSlotExecutionCoordinatorHandlesMixedAndFullyReusedQueryFreePlans(t *testing.T) {
	first := planIdentity()
	second := planIdentity()
	second.StrategyID = "8"
	activation := execution.PlanActivationResult{
		Contract: frozenContract(),
		Facts: []execution.PlanActivationFact{
			{Plan: first, Selection: execution.ActivationCurrent, Selected: execution.ActivatedPlan{
				Identity: first, StateGeneration: "first-state", StateApplyEpoch: 2,
				ScheduleRevision: "first-schedule", RequiredFullSlots: 3,
			}},
			{Plan: second, Selection: execution.ActivationCurrent, Selected: execution.ActivatedPlan{
				Identity: second, StateGeneration: "second-state", StateApplyEpoch: 2,
				ScheduleRevision: "second-schedule", RequiredFullSlots: 4,
			}},
		},
	}
	tests := []struct {
		name          string
		reuseSecond   bool
		wantApply     int
		wantMutations []execution.PlanIdentity
	}{
		{name: "mixed", wantApply: 1, wantMutations: []execution.PlanIdentity{second}},
		{name: "all reused", reuseSecond: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activation})
			plans := []execution.PlanIdentity{first, second}
			fixture.ports.expectedTargets.Plans = append([]execution.PlanIdentity(nil), plans...)
			fixture.ports.finalization.Targets.Plans = append([]execution.PlanIdentity(nil), plans...)
			request := slotRequest(execution.OperationReplay)
			request.DuePlanTargets.Plans = append([]execution.PlanIdentity(nil), plans...)
			for index, fact := range activation.Facts {
				if index == 1 && !test.reuseSecond {
					continue
				}
				marker := queryFreeGapMarker(t, fact.Selected, applyVersionForActivatedPlan(t, request, fact.Selected), fact.Selected.ScheduleRevision, []execution.GapScopeState{{
					Scope: execution.GapScope{}, Status: execution.GapStatusGapped,
					ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift), RequiredFullSlots: fact.Selected.RequiredFullSlots,
				}})
				fixture.ports.markers[marker.Identity] = marker
			}

			result, err := fixture.coordinator.Execute(context.Background(), request)
			if err != nil || !result.Completed {
				t.Fatalf("Execute() result=%+v error=%v", result, err)
			}
			if fixture.ports.applyCalls != test.wantApply || len(fixture.ports.mutations) != len(test.wantMutations) || fixture.ports.progressCalls != 1 {
				t.Fatalf("apply=%d mutations=%+v progress=%d", fixture.ports.applyCalls, fixture.ports.mutations, fixture.ports.progressCalls)
			}
			for index, want := range test.wantMutations {
				if fixture.ports.mutations[index].Identity.Plan != want {
					t.Fatalf("mutation[%d]=%+v want Plan=%+v", index, fixture.ports.mutations[index], want)
				}
			}
		})
	}
}

func TestSlotExecutionCoordinatorRechecksActivationAndAdmissionAfterQueryFreeGapReuse(t *testing.T) {
	t.Run("activation changes", func(t *testing.T) {
		before := activePlanResult("state-v2", 2)
		after := activePlanResult("state-v3", 3)
		fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{before, after})
		selected := before.Facts[0].Selected
		marker := queryFreeGapMarker(t, selected, currentQueryFreeApplyVersion(t), selected.ScheduleRevision, []execution.GapScopeState{{
			Scope: execution.GapScope{}, Status: execution.GapStatusGapped,
			ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift), RequiredFullSlots: 3,
		}})
		fixture.ports.markers[marker.Identity] = marker
		newSelected := after.Facts[0].Selected
		newMarker := queryFreeGapMarker(t, newSelected, applyVersionForActivatedPlan(t, slotRequest(execution.OperationReplay), newSelected), newSelected.ScheduleRevision, []execution.GapScopeState{{
			Scope: execution.GapScope{}, Status: execution.GapStatusGapped,
			ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift), RequiredFullSlots: 3,
		}})
		fixture.ports.markers[newMarker.Identity] = newMarker

		result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
		if err != nil || result.Completed || result.Result != observability.ResultRetrying ||
			result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
			t.Fatalf("Execute() result=%+v error=%v", result, err)
		}
		if fixture.ports.progressCalls != 0 || len(fixture.ports.mutations) != 0 || fixture.ports.applyCalls != 0 {
			t.Fatalf("activation change mutations=%+v progress=%d", fixture.ports.mutations, fixture.ports.progressCalls)
		}
	})

	t.Run("progress admission lost", func(t *testing.T) {
		activation := activePlanResult("state-v2", 2)
		fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activation})
		selected := activation.Facts[0].Selected
		marker := queryFreeGapMarker(t, selected, currentQueryFreeApplyVersion(t), selected.ScheduleRevision, []execution.GapScopeState{{
			Scope: execution.GapScope{}, Status: execution.GapStatusGapped,
			ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift), RequiredFullSlots: 3,
		}})
		fixture.ports.markers[marker.Identity] = marker
		fixture.ports.admissionRejectAt = 2

		result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
		if err == nil || result.Completed || fixture.ports.progressCalls != 0 || fixture.ports.applyCalls != 0 {
			t.Fatalf("Execute() result=%+v error=%v apply=%d progress=%d",
				result, err, fixture.ports.applyCalls, fixture.ports.progressCalls)
		}
	})
}

func TestSlotExecutionCoordinatorDoesNotReuseUnreadableQueryFreeGap(t *testing.T) {
	activation := activePlanResult("state-v2", 2)
	selected := activation.Facts[0].Selected
	t.Run("corrupt", func(t *testing.T) {
		fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activation})
		marker := queryFreeGapMarker(t, selected, currentQueryFreeApplyVersion(t), selected.ScheduleRevision, []execution.GapScopeState{{
			Scope: execution.GapScope{}, Status: execution.GapStatusGapped,
			ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift), RequiredFullSlots: 3,
		}})
		marker.MarkerRevision = 0
		fixture.ports.markers[marker.Identity] = marker
		result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
		if err == nil || result.Completed || fixture.ports.applyCalls != 0 || fixture.ports.progressCalls != 0 {
			t.Fatalf("Execute() result=%+v error=%v apply=%d progress=%d",
				result, err, fixture.ports.applyCalls, fixture.ports.progressCalls)
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activation})
		fixture.ports.markers[execution.PlanGapIdentity{Plan: selected.Identity, StateGeneration: selected.StateGeneration}] = execution.GapGuardSnapshot{
			Identity: execution.PlanGapIdentity{Plan: selected.Identity, StateGeneration: selected.StateGeneration},
			Status:   execution.GapUnavailable, ReasonCode: execution.ReasonCode(contract.ReasonProviderUnavailable),
		}
		result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
		if err == nil || result.Completed || fixture.ports.applyCalls != 0 || fixture.ports.progressCalls != 0 {
			t.Fatalf("Execute() result=%+v error=%v apply=%d progress=%d",
				result, err, fixture.ports.applyCalls, fixture.ports.progressCalls)
		}
	})
}

func TestSlotExecutionCoordinatorSkipsGuardWhenActivationHasNoPlan(t *testing.T) {
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{noPlanResult()})
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{"finalization", "activation", "sequence", "activation", "progress_commit"})
	if len(fixture.ports.mutations) != 0 {
		t.Fatalf("no-Plan finalization wrote Guard: %+v", fixture.ports.mutations)
	}
}

func TestSlotExecutionCoordinatorFinalizesSnapshotUnavailableForPendingActivation(t *testing.T) {
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{pendingPlanResult("state-pending", 3)})
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	if len(fixture.ports.mutations) != 1 || fixture.ports.mutations[0].Identity != (execution.PlanGapIdentity{
		Plan: planIdentity(), StateGeneration: "state-pending",
	}) || fixture.ports.progressCalls != 1 {
		t.Fatalf("PENDING activation mutations=%+v progress=%d", fixture.ports.mutations, fixture.ports.progressCalls)
	}
}

func TestSlotExecutionCoordinatorProtectsQueryFreeActivationSelectionChangesBeforeProgress(t *testing.T) {
	tests := []struct {
		name            string
		before          execution.PlanActivationResult
		after           execution.PlanActivationResult
		wantGenerations []execution.StateGeneration
	}{
		{name: "pending_to_current", before: pendingPlanResult("pending-v1", 2), after: activePlanResult("current-v2", 3), wantGenerations: []execution.StateGeneration{"pending-v1", "current-v2"}},
		{name: "current_to_pending", before: activePlanResult("current-v1", 2), after: pendingPlanResult("pending-v2", 3), wantGenerations: []execution.StateGeneration{"current-v1", "pending-v2"}},
		{name: "pending_to_none", before: pendingPlanResult("pending-v1", 2), after: noPlanResult(), wantGenerations: []execution.StateGeneration{"pending-v1"}},
		{name: "none_to_pending", before: noPlanResult(), after: pendingPlanResult("pending-v2", 3), wantGenerations: []execution.StateGeneration{"pending-v2"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{test.before, test.after})
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
			if err != nil || result.Completed || result.Result != observability.ResultRetrying ||
				result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) || fixture.ports.progressCalls != 0 {
				t.Fatalf("Execute() result=%+v error=%v progress=%d", result, err, fixture.ports.progressCalls)
			}
			got := make([]execution.StateGeneration, len(fixture.ports.mutations))
			for index := range fixture.ports.mutations {
				got[index] = fixture.ports.mutations[index].Identity.StateGeneration
			}
			if !reflect.DeepEqual(got, test.wantGenerations) {
				t.Fatalf("protected generations=%v, want=%v; mutations=%+v", got, test.wantGenerations, fixture.ports.mutations)
			}
		})
	}
}

func TestSlotExecutionCoordinatorDoesNotAdvancePendingActivationWhenProgressControlIsUnreadable(t *testing.T) {
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{pendingPlanResult("state-pending", 3)})
	fixture.ports.activationErrorAt = 2
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || result.Completed || result.Result != observability.ResultRetrying ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonActivationReadFailed) || fixture.ports.progressCalls != 0 {
		t.Fatalf("Execute() result=%+v error=%v progress=%d", result, err, fixture.ports.progressCalls)
	}
	if len(fixture.ports.mutations) != 1 || fixture.ports.mutations[0].Identity.StateGeneration != "state-pending" {
		t.Fatalf("PENDING guard before unreadable control=%+v", fixture.ports.mutations)
	}
}

func TestSlotExecutionCoordinatorUsesSameQueryFreePathForGapSkipped(t *testing.T) {
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activePlanResult("state-v2", 2)})
	fixture.ports.finalization.Mode = execution.FinalizationGapSkipped
	fixture.ports.finalization.ReasonCode = execution.ReasonCode(contract.ReasonGapSkipped)
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	if len(fixture.ports.mutations) != 1 ||
		fixture.ports.mutations[0].Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) ||
		fixture.ports.lastProgress.Completion.Kind != execution.CompletionGapSkipped ||
		fixture.ports.lastProgress.Completion.ReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) {
		t.Fatalf("GAP_SKIPPED did not use query-free Guard/Progress: mutation=%+v progress=%+v",
			fixture.ports.mutations, fixture.ports.lastProgress)
	}
	lastObservation := (*fixture.observations)[len(*fixture.observations)-1]
	if lastObservation.Stage != observability.StageProgressCommitted ||
		lastObservation.Result != observability.ResultDegraded ||
		lastObservation.ReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) {
		t.Fatalf("GAP_SKIPPED completion observation=%+v", lastObservation)
	}
}

func TestSlotExecutionCoordinatorRejectsWrongFrozenPlanWithEchoedDigest(t *testing.T) {
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activePlanResult("state-v2", 2)})
	fixture.ports.finalization.Targets.Plans[0].StrategyID = "wrong-plan"
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err == nil || result.Completed {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	assertTrace(t, fixture.trace, []string{"finalization"})
}

func TestSlotExecutionCoordinatorReprotectsChangedActivationBeforeProgress(t *testing.T) {
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{
		activePlanResult("state-v2", 2),
		activePlanResult("state-v3", 3),
		activePlanResult("state-v3", 3),
	})
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || result.Completed || result.Result != observability.ResultRetrying ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("first Execute() result=%+v error=%v", result, err)
	}
	if len(fixture.ports.mutations) != 2 || fixture.ports.mutations[0].Identity.StateGeneration != "state-v2" ||
		fixture.ports.mutations[1].Identity.StateGeneration != "state-v3" ||
		fixture.ports.mutations[1].Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonSnapshotUnavailable) {
		t.Fatalf("changed activation was not protected before retry: %+v", fixture.ports.mutations)
	}
	result, err = fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || !result.Completed {
		t.Fatalf("redo Execute() result=%+v error=%v", result, err)
	}
	if len(fixture.ports.mutations) != 3 || fixture.ports.mutations[2].Identity.StateGeneration != "state-v3" ||
		fixture.ports.mutations[1].MutationDigest != fixture.ports.mutations[2].MutationDigest ||
		!reflect.DeepEqual(fixture.ports.applyStatuses, []execution.GapGuardApplyStatus{
			execution.GapGuardApplied, execution.GapGuardApplied, execution.GapGuardAlreadyApplied,
		}) {
		t.Fatalf("activation switch mutations=%+v", fixture.ports.mutations)
	}
	if fixture.ports.progressCalls != 1 || fixture.ports.activationCalls != 4 {
		t.Fatalf("activation calls=%d progress calls=%d", fixture.ports.activationCalls, fixture.ports.progressCalls)
	}
}

func TestSlotExecutionCoordinatorOnlyReprotectsChangedPlanBeforeProgress(t *testing.T) {
	stablePlan := planIdentity()
	changedPlan := planIdentity()
	changedPlan.StrategyID = "8"
	activation := func(stableGeneration, changedGeneration execution.StateGeneration, changedEpoch execution.StateApplyEpoch) execution.PlanActivationResult {
		return execution.PlanActivationResult{
			Contract: frozenContract(),
			Facts: []execution.PlanActivationFact{
				{
					Plan: stablePlan, Selection: execution.ActivationCurrent,
					Selected: execution.ActivatedPlan{
						Identity: stablePlan, StateGeneration: stableGeneration, StateApplyEpoch: 2,
						ScheduleRevision: "stable-schedule", RequiredFullSlots: 2,
					},
				},
				{
					Plan: changedPlan, Selection: execution.ActivationCurrent,
					Selected: execution.ActivatedPlan{
						Identity: changedPlan, StateGeneration: changedGeneration, StateApplyEpoch: changedEpoch,
						ScheduleRevision: "changed-schedule", RequiredFullSlots: 3,
					},
				},
			},
		}
	}
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{
		activation("stable-v1", "changed-v1", 2),
		activation("stable-v1", "changed-v2", 3),
	})
	fixture.ports.expectedTargets.Plans = []execution.PlanIdentity{stablePlan, changedPlan}
	fixture.ports.finalization.Targets.Plans = append([]execution.PlanIdentity(nil), fixture.ports.expectedTargets.Plans...)

	request := slotRequest(execution.OperationReplay)
	request.DuePlanTargets.Plans = []execution.PlanIdentity{stablePlan, changedPlan}
	result, err := fixture.coordinator.Execute(context.Background(), request)
	if err != nil || result.Completed || result.Result != observability.ResultRetrying ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	if fixture.ports.progressCalls != 0 {
		t.Fatalf("activation change committed Progress %d times", fixture.ports.progressCalls)
	}
	var stableGuards, oldChangedGuards, newChangedGuards int
	for _, mutation := range fixture.ports.mutations {
		switch mutation.Identity {
		case (execution.PlanGapIdentity{Plan: stablePlan, StateGeneration: "stable-v1"}):
			stableGuards++
		case (execution.PlanGapIdentity{Plan: changedPlan, StateGeneration: "changed-v1"}):
			oldChangedGuards++
		case (execution.PlanGapIdentity{Plan: changedPlan, StateGeneration: "changed-v2"}):
			newChangedGuards++
		}
	}
	if stableGuards != 1 || oldChangedGuards != 1 || newChangedGuards != 1 || len(fixture.ports.mutations) != 3 {
		t.Fatalf("unexpected activation protection scope: mutations=%+v", fixture.ports.mutations)
	}
}

func TestSlotExecutionCoordinatorReturnsRetryWhenActivationKeepsChanging(t *testing.T) {
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{
		activePlanResult("state-v2", 2),
		activePlanResult("state-v3", 3),
	})
	fixture.ports.cycleActivations = true
	// No deadline. An earlier version ran this under a hundred millisecond
	// context, which read as a safety net and acted as a second, competing
	// verdict: the ports answer from memory, so on an unloaded machine the run
	// finished in under a millisecond, and on a loaded one the deadline landed
	// first and the call returned "context deadline exceeded" from the gap
	// preflight instead of the drift retry being asserted. Nothing needs the
	// deadline - the coordinator reads activations exactly twice and returns on
	// the difference, which is what the call count below pins - so a budget
	// could only ever decide the outcome for reasons outside the subject.
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || result.Completed || result.Result != observability.ResultRetrying ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("Execute() result=%+v error=%v", result, err)
	}
	// Two reads and no Progress commit is what makes the retry a decision
	// rather than a timeout: the coordinator saw the activation change between
	// the guard read and the progress read, and stopped there.
	if fixture.ports.activationCalls != 2 || fixture.ports.progressCalls != 0 {
		t.Fatalf("activation calls=%d progress calls=%d", fixture.ports.activationCalls, fixture.ports.progressCalls)
	}
}

func TestSlotExecutionCoordinatorSnapshotUnavailableRedoUsesCanonicalEnsureGapped(t *testing.T) {
	fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activePlanResult("state-v2", 2)})
	fixture.ports.activationErrorAt = 2
	if result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay)); err != nil || result.Completed || result.Result != observability.ResultRetrying ||
		result.ReasonCode != execution.ReasonCode(contract.ReasonActivationReadFailed) {
		t.Fatalf("first Execute() result=%+v error=%v", result, err)
	}
	if fixture.ports.progressCalls != 0 {
		t.Fatalf("control-plane failure advanced Progress %d times", fixture.ports.progressCalls)
	}
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || !result.Completed {
		t.Fatalf("redo Execute() result=%+v error=%v", result, err)
	}
	if len(fixture.ports.mutations) != 2 || len(fixture.ports.applyStatuses) != 2 {
		t.Fatalf("redo mutations=%+v statuses=%v", fixture.ports.mutations, fixture.ports.applyStatuses)
	}
	first, redo := fixture.ports.mutations[0], fixture.ports.mutations[1]
	if first.Scopes[0].Kind != execution.GapOpen || redo.Scopes[0].Kind != execution.GapStrengthen ||
		first.MutationDigest != redo.MutationDigest {
		t.Fatalf("redo did not preserve ENSURE_GAPPED digest: first=%+v redo=%+v", first, redo)
	}
	if !reflect.DeepEqual(fixture.ports.applyStatuses, []execution.GapGuardApplyStatus{
		execution.GapGuardApplied, execution.GapGuardAlreadyApplied,
	}) {
		t.Fatalf("apply statuses=%v", fixture.ports.applyStatuses)
	}
}

func TestSlotExecutionCoordinatorDoesNotAdvanceWhenActivationIsUnreadable(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activePlanResult("state-v2", 2)})
			fixture.ports.activationErrorAt = failAt
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
			if err != nil || result.Completed || result.Result != observability.ResultRetrying ||
				result.ReasonCode != execution.ReasonCode(contract.ReasonActivationReadFailed) || fixture.ports.progressCalls != 0 {
				t.Fatalf("Execute() result=%+v error=%v progress=%d", result, err, fixture.ports.progressCalls)
			}
			if failAt == 1 && len(fixture.ports.mutations) != 0 {
				t.Fatalf("Guard was written without a readable activation fact: %+v", fixture.ports.mutations)
			}
		})
	}
}

func TestSlotExecutionCoordinatorRequiresAdmissionBeforeQueryFreeGuardAndProgress(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			fixture := newQueryFreeFixture(t, []execution.PlanActivationResult{activePlanResult("state-v2", 2)})
			fixture.ports.admissionRejectAt = failAt
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
			if err == nil || result.Completed || fixture.ports.progressCalls != 0 {
				t.Fatalf("Execute() result=%+v error=%v progress=%d", result, err, fixture.ports.progressCalls)
			}
			if failAt == 1 && len(fixture.ports.mutations) != 0 {
				t.Fatalf("Guard admission failure still wrote mutations: %+v", fixture.ports.mutations)
			}
			if failAt == 2 && len(fixture.ports.mutations) != 1 {
				t.Fatalf("Progress admission was not checked after Guard: %+v", fixture.ports.mutations)
			}
		})
	}
}

type queryFreeFixture struct {
	trace        *[]string
	observations *[]observability.Observation
	ports        *queryFreePorts
	coordinator  *worker.SlotExecutionCoordinator
}

func newQueryFreeFixture(t *testing.T, activations []execution.PlanActivationResult) queryFreeFixture {
	t.Helper()
	trace := make([]string, 0, 16)
	observations := make([]observability.Observation, 0, 8)
	base := &recordingPorts{trace: &trace}
	expectedTargets := execution.FrozenDuePlanTargets{
		DuePlanSetDigest: frozenContract().DuePlanSetDigest,
		Plans:            []execution.PlanIdentity{planIdentity()},
	}
	ports := &queryFreePorts{
		recordingPorts: base,
		finalization: execution.QueryFreeFinalization{
			Contract: frozenContract(), Mode: execution.FinalizationSnapshotUnavailable,
			ReasonCode: execution.ReasonCode(contract.ReasonSnapshotUnavailable),
			Targets: execution.FrozenDuePlanTargets{
				DuePlanSetDigest: expectedTargets.DuePlanSetDigest,
				Plans:            append([]execution.PlanIdentity(nil), expectedTargets.Plans...),
			},
		},
		expectedTargets: expectedTargets,
		activations:     activations,
		markers:         make(map[execution.PlanGapIdentity]execution.GapGuardSnapshot),
	}
	coordinator, err := worker.NewSlotExecutionCoordinator(worker.Ports{
		Finalization: ports, Activation: ports,
		Query: ports, Sequencer: ports, Evaluator: ports, Admission: ports, GapGuard: ports,
		Events: ports, State: ports, Progress: ports,
		Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observability.NormalizeObservation(observation))
		}),
	}, worker.ProvisionalBudget{
		MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10,
	})
	if err != nil {
		t.Fatalf("NewSlotExecutionCoordinator() error: %v", err)
	}
	return queryFreeFixture{trace: &trace, observations: &observations, ports: ports, coordinator: coordinator}
}

type queryFreePorts struct {
	*recordingPorts
	finalization      execution.QueryFreeFinalization
	expectedTargets   execution.FrozenDuePlanTargets
	activations       []execution.PlanActivationResult
	cycleActivations  bool
	activationCalls   int
	activationErrorAt int
	lastActivation    execution.PlanActivationResult
	admissionCalls    int
	admissionRejectAt int
	markers           map[execution.PlanGapIdentity]execution.GapGuardSnapshot
	mutations         []execution.PlanGapMutation
	applyStatuses     []execution.GapGuardApplyStatus
	applyCalls        int
	progressCalls     int
}

func (ports *queryFreePorts) ResolveFinalization(
	_ context.Context,
	_ execution.SlotExecutionRequest,
) (execution.QueryFreeFinalization, error) {
	ports.record("finalization")
	return ports.finalization, nil
}

func (ports *queryFreePorts) LoadActivations(
	_ context.Context,
	_ execution.PlanActivationRequest,
) (execution.PlanActivationResult, error) {
	ports.record("activation")
	ports.activationCalls++
	if ports.activationErrorAt == ports.activationCalls {
		ports.activationErrorAt = 0
		return execution.PlanActivationResult{}, errors.New("injected activation read")
	}
	index := ports.activationCalls - 1
	if ports.cycleActivations {
		index %= len(ports.activations)
	}
	if index >= len(ports.activations) {
		index = len(ports.activations) - 1
	}
	ports.lastActivation = ports.activations[index]
	return ports.lastActivation, nil
}

func (ports *queryFreePorts) Sequence(
	ctx context.Context,
	_ execution.SequencingScope,
	run func(context.Context) error,
) error {
	ports.record("sequence")
	return run(ctx)
}

func (ports *queryFreePorts) Check(
	_ context.Context,
	request execution.SideEffectAdmissionRequest,
) (execution.SideEffectAdmissionResult, error) {
	ports.admissionCalls++
	stage := "query_free_admission_guard"
	if ports.activationCalls >= 2 {
		stage = "query_free_admission_progress"
	}
	ports.record(stage)
	fact, found := ports.lastActivation.Find(request.Plan)
	if !found || fact.Selection == execution.ActivationNone || request.StateApplyEpoch != fact.Selected.StateApplyEpoch ||
		request.OwnerFence != slotRequest(execution.OperationReplay).OwnerFence {
		return execution.SideEffectAdmissionResult{}, errors.New("query-free admission facts drifted")
	}
	if ports.admissionRejectAt == ports.admissionCalls {
		return execution.SideEffectAdmissionResult{
			Admitted: false, ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift),
		}, nil
	}
	return execution.SideEffectAdmissionResult{Admitted: true}, nil
}

func (ports *queryFreePorts) LoadGapsInto(ctx context.Context, request execution.GapLoadRequest, accept func(execution.GapGuardSnapshot) error) error {
	result, err := ports.LoadGaps(ctx, request)
	if err != nil {
		return err
	}
	for _, item := range result.Items {
		if err := accept(item); err != nil {
			return err
		}
	}
	return nil
}

func (ports *queryFreePorts) LoadGaps(
	_ context.Context,
	request execution.GapLoadRequest,
) (execution.GapLoadResult, error) {
	ports.record("query_free_gap_load")
	items := make([]execution.GapGuardSnapshot, len(request.Items))
	for index, item := range request.Items {
		marker, ok := ports.markers[item.Identity]
		if !ok {
			marker = execution.GapGuardSnapshot{Identity: item.Identity, Status: execution.GapMissing}
		}
		items[index] = marker
	}
	return execution.GapLoadResult{Items: items}, nil
}

func (ports *queryFreePorts) ApplyGap(
	_ context.Context,
	request execution.GapGuardApplyRequest,
) (execution.GapGuardApplyResult, error) {
	ports.record("query_free_gap_apply")
	ports.applyCalls++
	items := make([]execution.GapGuardApplyItemResult, len(request.Items))
	for index, mutation := range request.Items {
		status := execution.GapGuardApplied
		if marker, ok := ports.markers[mutation.Identity]; ok &&
			marker.PersistedApplyVersion == mutation.ApplyVersion &&
			marker.PersistedMutationDigest == mutation.MutationDigest {
			status = execution.GapGuardAlreadyApplied
		} else {
			scopes := make([]execution.GapScopeState, len(mutation.Scopes))
			for scopeIndex, scope := range mutation.Scopes {
				scopes[scopeIndex] = execution.GapScopeState{
					Scope: scope.Scope, Status: execution.GapStatusGapped, ReasonCode: scope.ReasonCode,
					RequiredFullSlots: scope.RequiredFullSlots,
				}
			}
			ports.markers[mutation.Identity] = execution.GapGuardSnapshot{
				Identity: mutation.Identity, MarkerRevision: 1,
				PersistedApplyVersion: mutation.ApplyVersion, PersistedMutationDigest: mutation.MutationDigest,
				Status: execution.GapFound, LastScheduleRevision: mutation.ScheduleRevision, Scopes: scopes,
			}
		}
		ports.mutations = append(ports.mutations, mutation)
		ports.applyStatuses = append(ports.applyStatuses, status)
		items[index] = execution.GapGuardApplyItemResult{Identity: mutation.Identity, Status: status}
	}
	return execution.GapGuardApplyResult{Items: items}, nil
}

func (ports *queryFreePorts) CommitProgress(
	_ context.Context,
	request execution.ProgressCommitRequest,
) (execution.ProgressCommitResult, error) {
	ports.record("progress_commit")
	ports.progressCalls++
	ports.lastProgress = request
	return execution.ProgressCommitResult{Status: execution.ProgressCommitted}, nil
}

func (ports *queryFreePorts) BeginSlot(_ context.Context, _ execution.ProgressBeginRequest) (execution.ProgressBeginResult, error) {
	return execution.ProgressBeginResult{Status: execution.ProgressCommitted}, nil
}

func activePlanResult(generation execution.StateGeneration, epoch execution.StateApplyEpoch) execution.PlanActivationResult {
	return execution.PlanActivationResult{
		Contract: frozenContract(),
		Facts: []execution.PlanActivationFact{{
			Plan: planIdentity(), Selection: execution.ActivationCurrent,
			Selected: execution.ActivatedPlan{
				Identity: planIdentity(), StateGeneration: generation, StateApplyEpoch: epoch,
				ScheduleRevision: "plan-schedule-v2", RequiredFullSlots: 3,
			},
		}},
	}
}

func currentQueryFreeApplyVersion(t *testing.T) execution.ApplyVersion {
	t.Helper()
	return applyVersionForActivatedPlan(t, slotRequest(execution.OperationReplay), activePlanResult("state-v2", 2).Facts[0].Selected)
}

func applyVersionForActivatedPlan(
	t *testing.T,
	request execution.SlotExecutionRequest,
	plan execution.ActivatedPlan,
) execution.ApplyVersion {
	t.Helper()
	version, err := execution.BuildApplyVersion(request.Contract, plan.StateApplyEpoch)
	if err != nil {
		t.Fatalf("BuildApplyVersion() error: %v", err)
	}
	return version
}

func queryFreeGapMarker(
	t *testing.T,
	plan execution.ActivatedPlan,
	version execution.ApplyVersion,
	scheduleRevision execution.PlanScheduleRevision,
	scopes []execution.GapScopeState,
) execution.GapGuardSnapshot {
	t.Helper()
	mutations := make([]execution.GapScopeMutation, len(scopes))
	for index, scope := range scopes {
		kind := execution.GapOpen
		if scope.Status == execution.GapStatusWarming {
			kind = execution.GapWarmup
		}
		mutations[index] = execution.GapScopeMutation{
			Scope: scope.Scope, Kind: kind, ReasonCode: scope.ReasonCode,
			RequiredFullSlots: scope.RequiredFullSlots,
		}
	}
	mutation, err := execution.BuildPlanGapMutation(execution.PlanGapMutation{
		Identity:     execution.PlanGapIdentity{Plan: plan.Identity, StateGeneration: plan.StateGeneration},
		ApplyVersion: version, ScheduleRevision: scheduleRevision, Scopes: mutations,
	})
	if err != nil {
		t.Fatalf("BuildPlanGapMutation() error: %v", err)
	}
	return execution.GapGuardSnapshot{
		Identity: mutation.Identity, MarkerRevision: 7,
		PersistedApplyVersion: mutation.ApplyVersion, PersistedMutationDigest: mutation.MutationDigest,
		Status: execution.GapFound, LastScheduleRevision: scheduleRevision,
		Scopes: append([]execution.GapScopeState(nil), scopes...),
	}
}

func cloneGapGuardSnapshot(marker execution.GapGuardSnapshot) execution.GapGuardSnapshot {
	marker.Scopes = append([]execution.GapScopeState(nil), marker.Scopes...)
	return marker
}

func pendingPlanResult(generation execution.StateGeneration, epoch execution.StateApplyEpoch) execution.PlanActivationResult {
	result := activePlanResult(generation, epoch)
	result.Facts[0].Selection = execution.ActivationPending
	return result
}

func noPlanResult() execution.PlanActivationResult {
	return execution.PlanActivationResult{
		Contract: frozenContract(),
		Facts:    []execution.PlanActivationFact{{Plan: planIdentity(), Selection: execution.ActivationNone}},
	}
}
