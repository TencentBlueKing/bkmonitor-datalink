// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

var slotOperations = []execution.Operation{
	execution.OperationNormal, execution.OperationRetry, execution.OperationReplay, execution.OperationProbe,
}

// versionFrom is the Slot's own ApplyVersion moved by an epoch step and a
// time step; digest, when set, replaces the Slot digest.
func versionFrom(epochStep int, timeStep int64, digest execution.SlotIdentityDigest) execution.ApplyVersion {
	version := frozenApplyVersion()
	version.StateApplyEpoch = execution.StateApplyEpoch(int(version.StateApplyEpoch) + epochStep)
	version.EvaluationTime += execution.EvaluationTime(timeStep)
	if digest != "" {
		version.SlotDigest = digest
	}
	return version
}

// supersededAt sums what the observations counted as superseded at a site.
func supersededAt(observations []observability.Observation, site observability.StateSupersededSite, operation execution.Operation) int64 {
	total := int64(0)
	for _, observation := range observations {
		if facts := observation.StateSuperseded; facts != nil && facts.Site == site && observation.Operation == observability.Operation(operation) {
			total += facts.Count
		}
	}
	return total
}

func progressCoverage(observations []observability.Observation) *observability.HistoryCoverageFacts {
	for _, observation := range observations {
		if observation.Stage == observability.StageProgressCommitted && observation.Err == nil {
			return observation.HistoryCoverage
		}
	}
	return nil
}

func namedInternalUnknown(observations []observability.Observation) bool {
	for _, observation := range observations {
		if observation.ReasonCode == observability.ReasonInternalUnknown {
			return true
		}
	}
	return false
}

// A series whose State a later Slot wrote -- a later evaluation time, at
// this Slot's activation or a newer one -- is skipped under every operation:
// not evaluated, no event, no State write, and the Slot completes. The skip
// has its line under its word and site, the count and sample ride on the
// result, and the round's coverage counts it.
func TestASeriesALaterSlotWroteIsSkippedUnderEveryOperation(t *testing.T) {
	for _, operation := range slotOperations {
		for name, stored := range map[string]execution.ApplyVersion{
			"same activation":  versionFrom(0, 60, "later-slot"),
			"newer activation": versionFrom(1, 60, "later-slot"),
		} {
			t.Run(string(operation)+"/"+name, func(t *testing.T) {
				fixture := newFixture(t, true, "")
				fixture.ports.alreadyApplied = true
				fixture.ports.storedVersion = &stored
				result, err := fixture.coordinator.Execute(context.Background(), slotRequest(operation))
				if err != nil || !result.Completed {
					t.Fatalf("Execute = (%+v, %v), want completed", result, err)
				}
				// The trace of a Slot that evaluated nothing and wrote nothing,
				// as the already-applied series has it.
				assertTrace(t, fixture.trace, []string{
					"query", "gap_load", "state_load", "sequence", "admission_initial", "activation_progress", "progress_commit",
				})
				if fixture.ports.stateApplyCalls != 0 || fixture.ports.eventCount != 0 {
					t.Fatalf("a skipped series wrote: state=%d events=%d", fixture.ports.stateApplyCalls, fixture.ports.eventCount)
				}
				sample := result.Superseded.Sample
				if result.Superseded.Count != 1 || sample == nil || sample.Series != execution.SeriesIdentityDigest(strings.Repeat("c", 64)) ||
					sample.Slot != frozenApplyVersion() || sample.Stored != stored {
					t.Fatalf("result superseded = %+v sample %+v, want the series with both versions", result.Superseded, sample)
				}
				if got := supersededAt(*fixture.observations, observability.StateSupersededAtState, operation); got != 1 {
					t.Fatalf("state skips counted under %s = %d, want 1", operation, got)
				}
				skipLines := 0
				for _, observation := range *fixture.observations {
					if observation.Stage == observability.StageMutationCompared &&
						observation.ReasonCode == observability.ReasonStateSupersededBeforeEvaluation {
						skipLines++
					}
				}
				if skipLines != 1 {
					t.Fatalf("skip lines = %d, want one", skipLines)
				}
				if coverage := progressCoverage(*fixture.observations); coverage == nil || coverage.Superseded != 1 {
					t.Fatalf("progress coverage = %+v, want one superseded series", coverage)
				}
			})
		}
	}
}

// The other ways stored State compares newer stay refusals under every
// operation: a newer activation at a time no later than the Slot is this Slot
// running behind its activation, and skipping would drop its decision; the
// tie of one moment broken by a larger digest says nothing about order.
// Skipping on the comparison alone would pass none of these.
func TestANewerStoredStateThatIsNotALaterSlotsIsStillRefused(t *testing.T) {
	for _, operation := range slotOperations {
		for name, stored := range map[string]execution.ApplyVersion{
			"newer activation at the Slot's time":  versionFrom(1, 0, ""),
			"newer activation at an earlier time":  versionFrom(1, -60, ""),
			"the same moment with a larger digest": versionFrom(0, 0, frozenApplyVersion().SlotDigest+"z"),
		} {
			t.Run(string(operation)+"/"+name, func(t *testing.T) {
				fixture := newFixture(t, true, "")
				fixture.ports.alreadyApplied = true
				fixture.ports.storedVersion = &stored
				result, err := fixture.coordinator.Execute(context.Background(), slotRequest(operation))
				if err == nil || result.Completed {
					t.Fatalf("Execute = (%+v, %v), want the preflight refusal", result, err)
				}
				if reason, named := worker.StateConflictReason(err); !named || reason != execution.ReasonCode(contract.ReasonStateStaleVersion) {
					t.Fatalf("refusal = %v (%s), want STATE_STALE_VERSION", err, reason)
				}
				if fixture.ports.stateApplyCalls != 0 || supersededAt(*fixture.observations, observability.StateSupersededAtState, operation) != 0 {
					t.Fatalf("a refused series wrote or was counted superseded: state=%d", fixture.ports.stateApplyCalls)
				}
			})
		}
	}
}

// The same moment with a smaller digest compares older and is written over,
// as it was before: the tie rule is not this change's.
func TestTheSameMomentWithASmallerDigestIsWrittenOverAsBefore(t *testing.T) {
	stored := versionFrom(0, 0, "")
	stored.SlotDigest = stored.SlotDigest[:len(stored.SlotDigest)-1]
	fixture := newFixture(t, true, "")
	fixture.ports.alreadyApplied = true
	fixture.ports.storedVersion = &stored
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
	if err != nil || !result.Completed || fixture.ports.stateApplyCalls != 1 || result.Superseded.Count != 0 {
		t.Fatalf("Execute = (%+v, %v) state=%d, want written and completed", result, err, fixture.ports.stateApplyCalls)
	}
}

// A Slot that skipped nothing says nothing about it: no skip line, no count
// on the result, and none in the coverage.
func TestASlotThatSkipsNothingNamesNoSkip(t *testing.T) {
	fixture := newFixture(t, true, "")
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed || result.Superseded.Count != 0 || result.Superseded.Sample != nil {
		t.Fatalf("Execute = (%+v, %v)", result, err)
	}
	for _, observation := range *fixture.observations {
		if observation.StateSuperseded != nil {
			t.Fatalf("an ordinary Slot carries a skip: %+v", observation)
		}
	}
	if coverage := progressCoverage(*fixture.observations); coverage != nil && coverage.Superseded != 0 {
		t.Fatalf("progress coverage = %+v", coverage)
	}
}

// A gap write the store answers SUPERSEDED is left out and counted at the gap
// site, at either of the Slot's two gap writes, and the Slot completes --
// State and events included where the Plan had them. STALE_VERSION from the
// same writes is still a refusal by its name.
func TestAGapWriteALaterSlotSupersededIsSkippedAndCounted(t *testing.T) {
	for _, site := range []struct {
		name, stage string
		degraded    bool
		writes      int
	}{
		{name: "before events", stage: "gap_before", degraded: true, writes: 0},
		{name: "after state", stage: "gap_after", writes: 1},
	} {
		t.Run(site.name, func(t *testing.T) {
			fixture := newFixture(t, true, "")
			fixture.ports.degraded = site.degraded
			fixture.ports.refuseGapStage, fixture.ports.refuseGapStatus = site.stage, execution.GapGuardSuperseded
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
			if err != nil || !result.Completed {
				t.Fatalf("Execute = (%+v, %v), want completed", result, err)
			}
			if len(fixture.ports.gapMutations) != 1 || fixture.ports.stateApplyCalls != site.writes || fixture.ports.eventCount != site.writes {
				t.Fatalf("gap writes %d state %d events %d", len(fixture.ports.gapMutations), fixture.ports.stateApplyCalls, fixture.ports.eventCount)
			}
			if got := supersededAt(*fixture.observations, observability.StateSupersededAtGap, execution.OperationReplay); got != 1 {
				t.Fatalf("gap skips counted = %d, want 1", got)
			}

			stale := newFixture(t, true, "")
			stale.ports.degraded = site.degraded
			stale.ports.refuseGapStage, stale.ports.refuseGapStatus = site.stage, execution.GapGuardStale
			result, err = stale.coordinator.Execute(context.Background(), slotRequest(execution.OperationReplay))
			if reason, named := worker.GapApplyReason(err); err == nil || result.Completed || !named ||
				reason != execution.ReasonCode(contract.ReasonGapApplyStaleVersion) {
				t.Fatalf("stale gap write = (%+v, %v), want GAP_APPLY_STALE_VERSION", result, err)
			}
			if got := supersededAt(*stale.observations, observability.StateSupersededAtGap, execution.OperationReplay); got != 0 {
				t.Fatalf("a refused gap write was counted superseded: %d", got)
			}
		})
	}
}

func laterGapMarker(version execution.ApplyVersion) map[execution.PlanGapIdentity]execution.GapGuardSnapshot {
	identity := execution.PlanGapIdentity{Plan: planIdentity(), StateGeneration: "state-v1"}
	return map[execution.PlanGapIdentity]execution.GapGuardSnapshot{identity: {
		Identity: identity, Status: execution.GapFound, MarkerRevision: 3, LastScheduleRevision: "plan-schedule-v1",
		PersistedMutationDigest: "another-slot-marker", PersistedApplyVersion: version,
		Scopes: []execution.GapScopeState{{Status: execution.GapStatusWarming,
			ReasonCode: execution.ReasonCode(contract.ReasonHistoryWarming), RequiredFullSlots: 3, ObservedFullSlots: 1}},
	}}
}

// A query-free finalization -- an expired replay given up as GAP_SKIPPED --
// that finds its Plan's marker already written by a later Slot completes on
// it: nothing is written, the marker counts at the gap site, nothing reads
// internal_unknown. A marker under a newer activation at a time no later than
// the Slot, or the same moment's larger digest, is still refused, by name.
func TestAQueryFreeFinalizationCompletesOnALaterSlotsMarker(t *testing.T) {
	for _, mode := range []execution.FinalizationMode{execution.FinalizationGapSkipped, execution.FinalizationSnapshotUnavailable} {
		t.Run(string(mode), func(t *testing.T) {
			fixture := newFixture(t, true, "")
			fixture.ports.finalizationMode = mode
			fixture.ports.activatedGapMarkers = laterGapMarker(versionFrom(0, 60, "later-slot"))
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if err != nil || !result.Completed {
				t.Fatalf("Execute = (%+v, %v), want completed on the later marker", result, err)
			}
			if len(fixture.ports.gapMutations) != 0 {
				t.Fatalf("the later marker was written over: %+v", fixture.ports.gapMutations)
			}
			if got := supersededAt(*fixture.observations, observability.StateSupersededAtGap, execution.OperationNormal); got != 1 {
				t.Fatalf("gap skips counted = %d, want 1", got)
			}
			if namedInternalUnknown(*fixture.observations) {
				t.Fatal("a line read internal_unknown")
			}
		})
	}
	for name, stored := range map[string]execution.ApplyVersion{
		"newer activation at the Slot's time":  versionFrom(1, 0, ""),
		"the same moment with a larger digest": versionFrom(0, 0, frozenApplyVersion().SlotDigest+"z"),
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newFixture(t, true, "")
			fixture.ports.finalizationMode = execution.FinalizationGapSkipped
			fixture.ports.activatedGapMarkers = laterGapMarker(stored)
			result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if err == nil || result.Completed || !strings.Contains(err.Error(), "marker is newer than the Slot") {
				t.Fatalf("Execute = (%+v, %v), want refused", result, err)
			}
			if reason, named := worker.GapApplyReason(err); !named || reason != execution.ReasonCode(contract.ReasonGapApplyStaleVersion) {
				t.Fatalf("refusal named %s (%v), want GAP_APPLY_STALE_VERSION", reason, named)
			}
			if len(fixture.ports.gapMutations) != 0 || supersededAt(*fixture.observations, observability.StateSupersededAtGap, execution.OperationNormal) != 0 {
				t.Fatal("a refused marker was written or counted superseded")
			}
		})
	}
}

// The marker read older than the Slot and written by a later Slot before
// this one's write lands is answered SUPERSEDED by the store: enough for the
// finalization, which completes and counts it, where any other refusal from
// the store stays one.
func TestAFinalizationWhoseMarkerALaterSlotWroteFirstCompletes(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.finalizationMode = execution.FinalizationGapSkipped
	fixture.ports.refuseGapStage, fixture.ports.refuseGapStatus = "gap_before", execution.GapGuardSuperseded
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed || len(fixture.ports.gapMutations) != 1 {
		t.Fatalf("Execute = (%+v, %v) writes %d, want completed after one write", result, err, len(fixture.ports.gapMutations))
	}
	if got := supersededAt(*fixture.observations, observability.StateSupersededAtGap, execution.OperationNormal); got != 1 {
		t.Fatalf("gap skips counted = %d, want 1", got)
	}
	conflict := newFixture(t, true, "")
	conflict.ports.finalizationMode = execution.FinalizationGapSkipped
	conflict.ports.refuseGapStage = "gap_before"
	if result, err := conflict.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal)); err == nil || result.Completed {
		t.Fatalf("a conflicting write = (%+v, %v), want refused", result, err)
	}
}

// Resumed series whose Plan marker a later Slot wrote leave it as it is and
// complete; the marker is counted once for the Plan, not once per series that
// read it. A marker under a newer activation at the Slot's time still fails
// the Slot.
func TestResumedSeriesUnderALaterSlotsMarkerCompleteAndCountItOnce(t *testing.T) {
	fixture := newFixture(t, true, "")
	fixture.ports.alreadyApplied, fixture.ports.reverseStateReceipts = true, true
	fixture.ports.activatedGapMarkers = laterGapMarker(versionFrom(0, 60, "later-slot"))
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationRetry))
	if err != nil || !result.Completed {
		t.Fatalf("Execute = (%+v, %v), want completed", result, err)
	}
	if coverage := progressCoverage(*fixture.observations); coverage == nil || coverage.Resumed != 2 {
		t.Fatalf("progress coverage = %+v, want both series resumed", coverage)
	}
	if got := supersededAt(*fixture.observations, observability.StateSupersededAtGap, execution.OperationRetry); got != 1 {
		t.Fatalf("gap skips counted = %d, want 1 for the Plan's one marker", got)
	}

	refused := newFixture(t, true, "")
	refused.ports.alreadyApplied = true
	refused.ports.activatedGapMarkers = laterGapMarker(versionFrom(1, 0, ""))
	result, err = refused.coordinator.Execute(context.Background(), slotRequest(execution.OperationRetry))
	if err == nil || result.Completed || !strings.Contains(err.Error(), "resumed Plan gap is newer than Slot") {
		t.Fatalf("Execute = (%+v, %v), want refused", result, err)
	}
}

// An attempt whose activation changed under it after evaluation converges and
// retries; the series it skipped on the way are on that result too, so the
// completion line and the read hold see them whichever exit the attempt took.
func TestAConvergedAttemptStillCarriesWhatItSkipped(t *testing.T) {
	fixture := newFixture(t, true, "")
	stored := versionFrom(0, 60, "later-slot")
	fixture.ports.alreadyApplied, fixture.ports.storedVersion = true, &stored
	fixture.ports.activationChangeAt, fixture.ports.persistActivatedGaps = 2, true
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || result.Completed || result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("Execute = (%+v, %v), want the activation change's retry", result, err)
	}
	if result.Superseded.Count != 1 || result.Superseded.Sample == nil || result.Superseded.Sample.Stored != stored {
		t.Fatalf("converged result superseded = %+v, want the skipped series", result.Superseded)
	}
}
