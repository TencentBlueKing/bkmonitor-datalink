// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func resumeFixture(t *testing.T) (execution.InternalExecutionHeader, execution.DuePlan, execution.RuntimeStateView) {
	t.Helper()
	due := execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}, StateGeneration: "state-v1", StateApplyEpoch: 2, ScheduleRevision: "plan-schedule-v1", CompiledPlan: noDataPreflightPlan(t, "7", nil)}
	header := execution.InternalExecutionHeader{Contract: noDataPreflightContract(t, []execution.DuePlan{due}), DuePlans: []execution.DuePlan{due}}
	version, err := execution.BuildApplyVersion(header.Contract, due.StateApplyEpoch)
	if err != nil {
		t.Fatal(err)
	}
	view := execution.RuntimeStateView{Identity: execution.StateKeyIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration, SeriesIdentityDigest: "series"}, VersionComparison: execution.ApplyVersionEqual, PersistedApplyVersion: version,
		Levels: []execution.RuntimeLevelStateView{{LevelID: 5, HistoryCompleteness: execution.HistoryWarming, GapReasonCode: "HISTORY_WARMING"}}}
	return header, due, view
}

func TestResumedSeriesKeepsPersistedCompletionFacts(t *testing.T) {
	for _, name := range []string{"warming", "gapped", "full", "series guard"} {
		t.Run(name, func(t *testing.T) {
			header, due, view := resumeFixture(t)
			wantReason := execution.ReasonCode("HISTORY_WARMING")
			switch name {
			case "gapped":
				view.Levels[0].HistoryCompleteness = execution.HistoryGapped
				view.Levels[0].GapReasonCode = "SNAPSHOT_UNAVAILABLE"
				wantReason = "SNAPSHOT_UNAVAILABLE"
			case "full":
				view.Levels[0].HistoryCompleteness = execution.HistoryFull
				view.Levels[0].GapReasonCode = ""
				wantReason = ""
			case "series guard":
				view.Levels[0].HistoryCompleteness = execution.HistoryFull
				view.Levels[0].GapReasonCode = ""
				view.SeriesGuard = &execution.StateGuardFact{Status: execution.HistoryGapped, ReasonCode: "GAP_SKIPPED"}
				wantReason = "GAP_SKIPPED"
			}
			before := view
			before.Levels = append([]execution.RuntimeLevelStateView(nil), view.Levels...)
			result, err := resumedSeriesResult(header, due, view, execution.GapLoadResult{})
			if err != nil {
				t.Fatal(err)
			}
			plan := result.Plans[0]
			if len(plan.StateResults) != 0 || len(plan.GuardBeforeEvents) != 0 || !reflect.DeepEqual(view, before) {
				t.Fatal("resume modified state or emitted effects")
			}
			if wantReason == "" {
				if len(plan.LevelOutcomes) != 0 {
					t.Fatal("full persisted State fabricated business verdict")
				}
			} else {
				if len(plan.LevelOutcomes) != 1 || plan.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown || plan.LevelOutcomes[0].ReasonCode != wantReason {
					t.Fatalf("persisted protection lost: %+v", plan)
				}
			}
			// No window counts, and the one count a resumed series may carry:
			// that it was resumed. The assertion used to read "the whole
			// struct is zero", which was the mechanism -- a resumed series
			// reported nothing at all -- and not the rule, which is that
			// State does not retain the original window counts so none may be
			// invented from it. Saying "no window count" rather than "no
			// count" keeps the rule and admits the fact that explains a
			// round's low Levels.
			windowCounts := plan.HistoryCoverage
			windowCounts.Resumed = 0
			if !reflect.DeepEqual(windowCounts, execution.HistoryCoverage{}) {
				t.Fatal("resume manufactured window counts")
			}
			if plan.HistoryCoverage.Resumed != 1 {
				t.Fatalf("resumed series did not count itself: %+v", plan.HistoryCoverage)
			}
		})
	}
}

func TestResumedSeriesNeverInfersGapRecovery(t *testing.T) {
	for _, name := range []string{"same version", "same version different schedule", "newer", "older warm", "older clear", "older changed schedule", "same tombstone", "older tombstone"} {
		t.Run(name, func(t *testing.T) {
			header, due, view := resumeFixture(t)
			gap := execution.GapGuardSnapshot{Identity: execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration}, Status: execution.GapFound, MarkerRevision: 4, LastScheduleRevision: due.ScheduleRevision, PersistedApplyVersion: view.PersistedApplyVersion,
				Scopes: []execution.GapScopeState{{Status: execution.GapStatusWarming, RequiredFullSlots: 3, ObservedFullSlots: 1, ReasonCode: "GAP_SKIPPED"}}}
			wantErr := false
			switch name {
			case "same version different schedule":
				gap.LastScheduleRevision = "different"
				wantErr = true
			case "newer":
				gap.PersistedApplyVersion.StateApplyEpoch++
				wantErr = true
			case "older warm":
				gap.PersistedApplyVersion.StateApplyEpoch--
			case "older clear":
				gap.PersistedApplyVersion.StateApplyEpoch--
				gap.Scopes[0].ObservedFullSlots = 2
			case "older changed schedule":
				gap.PersistedApplyVersion.StateApplyEpoch--
				gap.LastScheduleRevision = "old"
				gap.Scopes[0].ObservedFullSlots = 2
			case "same tombstone":
				gap.Status = execution.GapClearedTombstone
				gap.Scopes = nil
			case "older tombstone":
				gap.Status = execution.GapClearedTombstone
				gap.Scopes = nil
				gap.PersistedApplyVersion.StateApplyEpoch--
			}
			before := gap
			before.Scopes = append([]execution.GapScopeState(nil), gap.Scopes...)
			result, err := resumedSeriesResult(header, due, view, execution.GapLoadResult{Items: []execution.GapGuardSnapshot{gap}})
			if !reflect.DeepEqual(gap, before) {
				t.Fatal("resume changed persisted gap evidence")
			}
			if wantErr {
				if err == nil {
					t.Fatal("version fence accepted conflicting marker")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			plan := result.Plans[0]
			if len(plan.GuardBeforeEvents) != 0 || len(plan.GuardAfterState) != 0 {
				t.Fatalf("resume inferred a recovery decision that was not persisted: before=%+v after=%+v", plan.GuardBeforeEvents, plan.GuardAfterState)
			}
		})
	}
}

func TestResumedSeriesRejectsMissingOrDifferentStateEvidence(t *testing.T) {
	for _, name := range []string{"generation", "version", "no levels", "no reason"} {
		t.Run(name, func(t *testing.T) {
			header, due, view := resumeFixture(t)
			switch name {
			case "generation":
				view.Identity.StateGeneration = "other"
			case "version":
				view.PersistedApplyVersion.StateApplyEpoch--
			case "no levels":
				view.Levels = nil
			case "no reason":
				view.Levels[0].GapReasonCode = ""
			}
			if _, err := resumedSeriesResult(header, due, view, execution.GapLoadResult{}); err == nil {
				t.Fatal("incomplete resume evidence accepted")
			}
		})
	}
}
