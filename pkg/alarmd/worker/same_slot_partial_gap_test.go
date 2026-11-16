// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// The marker is preseeded as the prior attempt's committed fact. This tests
// the retry boundary, not the store's original commit: authoritative PRIMARY
// has become PARTIAL. Neither the series nor no-series completion path
// reopens a same-Slot tombstone. A found marker only grows by what the
// PARTIAL answer adds, from the revision the retry read: every scope and
// observation the first commit wrote stays, no reason falls.
func TestSameSlotCommittedGapSurvivesPartialPrimaryRetry(t *testing.T) {
	for _, status := range []execution.GapLoadStatus{execution.GapClearedTombstone, execution.GapFound} {
		for _, delivered := range []bool{false, true} {
			name := string(status) + "/no_series"
			if delivered {
				name = string(status) + "/delivered_series"
			}
			t.Run(name, func(t *testing.T) {
				header, batches := workerG4StreamFixture(t, strategy.DetectorKindProcPort)
				due := header.DuePlans[0]
				version, err := execution.BuildApplyVersion(header.Contract, due.StateApplyEpoch)
				if err != nil {
					t.Fatal(err)
				}
				marker := execution.GapGuardSnapshot{Identity: execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration}, Status: status, MarkerRevision: 3,
					PersistedApplyVersion: version, PersistedMutationDigest: execution.MutationDigest(strings.Repeat("a", 64)), LastScheduleRevision: due.ScheduleRevision}
				if status == execution.GapFound {
					marker.Scopes = []execution.GapScopeState{{Scope: execution.GapScope{HasLevel: true, LevelID: 5}, Status: execution.GapStatusWarming, ReasonCode: "HISTORY_GAPPED", RequiredFullSlots: 3, ObservedFullSlots: 1}}
				}
				before := marker
				before.Scopes = append([]execution.GapScopeState(nil), marker.Scopes...)
				b := batches[0]
				physical := execution.PhysicalQueryCompletion{Ref: b.CompletionRef, PhysicalQuery: b.PhysicalQuery, QueryRevision: b.QueryRevision,
					Completeness: execution.CompletenessPartial, DataState: execution.DataStateEmpty,
					PartialEvidence: &execution.PartialEvidence{Kind: execution.PartialEvidenceOmissionStable, Version: 1, EvidenceDigest: strings.Repeat("d", 64), OmissionOnly: true, ReturnedRecordsStable: true}}
				var send []execution.SeriesExecutionBatch
				if delivered {
					send = batches
					physical.DataState = execution.DataStateData
					physical.Delivery = b.Delivery
				}
				completion := execution.QueryExecutionCompletion{AllRequiredCompleted: true, PhysicalQueries: []execution.PhysicalQueryCompletion{physical}}
				completion.CompletionBindings = accessShapedCompletionBindings(t, header, completion.PhysicalQueries)
				ports, evaluator, coordinator := workerG4Coordinator(t)
				ports.activatedGapMarkers = map[execution.PlanGapIdentity]execution.GapGuardSnapshot{marker.Identity: marker}
				ports.executeOverride = streamExecution(header, send, completion)
				ports.failStage = "progress_commit"
				first, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
				if err == nil || first.Completed || !strings.Contains(err.Error(), "progress_commit") {
					t.Fatalf("first attempt must reach injected Progress failure: result=%+v err=%v", first, err)
				}
				ports.failStage = ""
				result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
				if err != nil || !result.Completed {
					t.Fatalf("partial retry did not finish Progress: result=%+v err=%v", result, err)
				}
				if status == execution.GapClearedTombstone && len(ports.gapMutations) != 0 {
					t.Errorf("same-Slot tombstone reopened: %+v", ports.gapMutations)
				}
				if status == execution.GapFound && len(ports.gapMutations) == 0 {
					t.Error("the PARTIAL answer did not grow the committed marker")
				}
				for _, mutation := range ports.gapMutations {
					grown, extends := execution.ExtendSameSlotGap(before.Scopes, mutation.Scopes)
					if mutation.ExpectedMarkerRevision != before.MarkerRevision || !extends {
						t.Fatalf("a retry wrote %+v over revision %d, want a growth of the marker it read", mutation, before.MarkerRevision)
					}
					kept := false
					for _, scope := range grown {
						kept = kept || (scope.Scope == before.Scopes[0].Scope && scope.ObservedFullSlots == before.Scopes[0].ObservedFullSlots &&
							execution.GapReasonRank(scope.ReasonCode) >= execution.GapReasonRank(before.Scopes[0].ReasonCode) &&
							scope.RequiredFullSlots >= before.Scopes[0].RequiredFullSlots)
					}
					if !kept {
						t.Fatalf("the grown marker %+v took back the committed level scope %+v", grown, before.Scopes[0])
					}
				}
				if len(evaluator.requests) != 0 || ports.stateApplyCalls != 0 || ports.eventCount != 0 {
					t.Errorf("PARTIAL PRIMARY caused business effects: evaluate=%d state=%d events=%d", len(evaluator.requests), ports.stateApplyCalls, ports.eventCount)
				}
				if !reflect.DeepEqual(ports.activatedGapMarkers[marker.Identity], before) {
					t.Error("persisted marker evidence changed")
				}
				if ports.lastProgress.Completion.Kind != execution.CompletionPartialGap {
					t.Errorf("PARTIAL PRIMARY lost from completion: %s", ports.lastProgress.Completion.Kind)
				}
			})
		}
	}
}
