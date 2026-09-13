// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestPhaseTwoObservationRecordsOnlyBoundedWorkflowMetrics(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
		Result: observability.ResultSuccess, Duration: 250 * time.Millisecond,
		Counts: observability.Counts{Records: 3, Plans: 5, Levels: 7},
		Trace:  observability.TraceFields{ExecutionID: "execution-high-cardinality", TraceID: "trace-high-cardinality"},
	})

	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"busy", testutil.ToFloat64(recorder.phaseTwo.busy.WithLabelValues("evaluation")), 0.25},
		{"record", testutil.ToFloat64(recorder.phaseTwo.work.WithLabelValues("record")), 3},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Fatalf("%s = %v, want %v", check.name, check.got, check.want)
		}
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.lastProgress.WithLabelValues("evaluation_completed")); got <= 0 {
		t.Fatalf("last progress timestamp = %v", got)
	}
}

func TestPhaseTwoAlgorithmMetricsUseOnlyFixedLowCardinalityLabels(t *testing.T) {
	t.Parallel()

	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentEvaluation,
		Stage:     observability.StageEvaluationCompleted,
		Result:    observability.ResultSuccess,
		AlgorithmEvaluations: []observability.AlgorithmEvaluationFact{
			{
				SourceAlgorithmFamily: observability.AlgorithmFamilySimpleRingRatio,
				DetectorKind:          observability.AlgorithmDetectorKindSimpleRingRatio,
				Result:                observability.AlgorithmEvaluationResultAbnormal,
				ReasonCode:            observability.ReasonCode("high-cardinality-reason"),
				Provenance: observability.AlgorithmProvenance{
					LevelID: 7, RequirementID: "high-cardinality-requirement", QueryRef: "high-cardinality-query",
				},
			},
			{
				SourceAlgorithmFamily: observability.AlgorithmFamilyPingUnreachable,
				DetectorKind:          observability.AlgorithmDetectorKindThreshold,
				Result:                observability.AlgorithmEvaluationResultNormal,
			},
			{
				SourceAlgorithmFamily: observability.AlgorithmFamily("strategy-123"),
				DetectorKind:          observability.AlgorithmDetectorKindThreshold,
				Result:                observability.AlgorithmEvaluationResultNormal,
			},
		},
		AlgorithmInputs: []observability.AlgorithmInputFact{
			{
				SourceAlgorithmFamily: observability.AlgorithmFamilySimpleRingRatio,
				DetectorKind:          observability.AlgorithmDetectorKindSimpleRingRatio,
				InputName:             observability.AlgorithmInputNameHistory,
				DependencyPoint:       observability.AlgorithmDependencyPointPrevious,
				Result:                observability.AlgorithmInputResultAvailable,
			},
			{
				SourceAlgorithmFamily: observability.AlgorithmFamilyOsRestart,
				DetectorKind:          observability.AlgorithmDetectorKindOsRestart,
				InputName:             observability.AlgorithmInputNameHistory,
				DependencyPoint:       observability.AlgorithmDependencyPointTenMinute,
				Result:                observability.AlgorithmInputResultMissing,
			},
			{
				SourceAlgorithmFamily: observability.AlgorithmFamilyOsRestart,
				DetectorKind:          observability.AlgorithmDetectorKindOsRestart,
				InputName:             observability.AlgorithmInputName("strategy-input"),
				DependencyPoint:       observability.AlgorithmDependencyPointCurrent,
				Result:                observability.AlgorithmInputResultAvailable,
			},
		},
	})

	for _, check := range []struct {
		name string
		got  float64
	}{
		{"SimpleRingRatio abnormal", testutil.ToFloat64(recorder.phaseTwo.algorithmEvaluations.WithLabelValues("simple_ring_ratio", "abnormal"))},
		{"PingUnreachable through Threshold normal", testutil.ToFloat64(recorder.phaseTwo.algorithmEvaluations.WithLabelValues("ping_unreachable", "normal"))},
		{"SimpleRingRatio previous available", testutil.ToFloat64(recorder.phaseTwo.algorithmInputs.WithLabelValues("simple_ring_ratio", "history", "previous", "available"))},
		{"OsRestart ten-minute missing", testutil.ToFloat64(recorder.phaseTwo.algorithmInputs.WithLabelValues("os_restart", "history", "ten_minute", "missing"))},
	} {
		if check.got != 1 {
			t.Errorf("%s = %v, want 1", check.name, check.got)
		}
	}
	if got := testutil.CollectAndCount(recorder.phaseTwo.algorithmEvaluations); got != 2 {
		t.Fatalf("algorithm evaluation series = %d, want only the two valid fixed combinations", got)
	}
	if got := testutil.CollectAndCount(recorder.phaseTwo.algorithmInputs); got != 2 {
		t.Fatalf("algorithm input series = %d, want only the two valid fixed combinations", got)
	}
}

func TestPhaseTwoSourceObservationMetricRecordsOnlyFixedEpisodeTransitions(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	for _, observation := range []observability.Observation{
		{
			Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotUnavailable,
			Result: observability.ResultDegraded, ReasonCode: observability.ReasonContractRetryable,
			SourceKind: observability.SourceKindLegacyStrategy,
		},
		{
			Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
			Result: observability.Result(observability.ResultRecovered), ReasonCode: observability.ReasonContractRetryable,
			SourceKind: observability.SourceKindLegacyStrategy,
		},
	} {
		recorder.Observe(context.Background(), observation)
	}
	for _, result := range []string{"degraded", "recovered"} {
		if got := testutil.ToFloat64(recorder.phaseTwo.sourceObservations.WithLabelValues(
			"legacy_strategy", result, "contract_retryable",
		)); got != 1 {
			t.Fatalf("legacy source %s transitions = %v, want 1", result, got)
		}
	}
	if got, err := testutil.GatherAndCount(recorder.Gatherer(), "bkmonitor_alarmd_observation_total"); err != nil || got != 0 {
		t.Fatalf("phase-two source transition leaked into generic observation metric: families=%d error=%v", got, err)
	}
}

func TestPhaseTwoActivationFailureMetricUsesOnlyFixedStageAndClass(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentControlPlane,
		Stage:     observability.StageActivationFailed,
		Result:    observability.ResultDegraded,
		ActivationFailure: &observability.ActivationFailureFacts{
			Stage:               observability.ActivationFailureStageReactivation,
			Class:               observability.ActivationFailureClassNotDrained,
			DrainingQueryGroups: 2, CandidateQueryGroups: 364, ReappearedQueryGroups: 1,
		},
		Trace: observability.TraceFields{QueryGroupKey: "must-not-be-a-label", StrategyID: "must-not-be-a-label"},
	})
	if got := testutil.ToFloat64(recorder.phaseTwo.activationFailures.WithLabelValues("reactivation", "not_drained")); got != 1 {
		t.Fatalf("reactivation/not_drained activation failures=%v, want 1", got)
	}
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentControlPlane,
		Stage:     observability.StageActivationFailed,
		Result:    observability.ResultDegraded,
		ActivationFailure: &observability.ActivationFailureFacts{
			Stage: "qg-high-cardinality", Class: "error-high-cardinality",
		},
	})
	if got := testutil.CollectAndCount(recorder.phaseTwo.activationFailures); got != 1 {
		t.Fatalf("activation failure series=%d, want only one fixed stage/class pair", got)
	}
}

func TestPhaseTwoSourceRefreshMetricUsesOnlyFixedStatus(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	for _, status := range observability.AllSourceRefreshStatuses() {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentControlPlane,
			Stage:     observability.StageSnapshotRefreshed,
			Result:    observability.ResultSuccess,
			SourceRefresh: &observability.SourceRefreshFacts{
				Status: status, SnapshotRevision: "must-not-be-a-label", PublicationEpoch: 9,
				CountsKnown: true, OldQueryGroups: 10, NewQueryGroups: 11,
				AddedQueryGroups: 2, RetiredQueryGroups: 1,
			},
		})
		if got := testutil.ToFloat64(recorder.phaseTwo.sourceRefreshes.WithLabelValues(string(status))); got != 1 {
			t.Fatalf("source refresh %s = %v, want 1", status, got)
		}
	}
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentControlPlane,
		Stage:     observability.StageSnapshotRefreshed,
		Result:    observability.ResultSuccess,
		SourceRefresh: &observability.SourceRefreshFacts{
			Status: "strategy-123", SnapshotRevision: "snapshot-invalid", PublicationEpoch: 10,
		},
	})
	if got := testutil.CollectAndCount(recorder.phaseTwo.sourceRefreshes); got != len(observability.AllSourceRefreshStatuses()) {
		t.Fatalf("source refresh series = %d, want %d fixed statuses", got, len(observability.AllSourceRefreshStatuses()))
	}
}

func TestPhaseTwoActiveSetAndLegacyMigrationMetricsUseOnlyFixedLabels(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{Component: observability.ComponentControlPlane,
		Stage: observability.StageActiveQGSet, Result: observability.ResultSuccess,
		ActiveQGSet: &observability.ActiveQGSetFacts{Operation: "encode", Result: "success", QueryGroups: 7, ObjectBytes: 321, Duration: time.Second},
		Trace:       observability.TraceFields{QueryGroupKey: "must-not-be-a-label", SnapshotRevision: "must-not-be-a-label"}})
	if got := testutil.ToFloat64(recorder.phaseTwo.activeQGSetCount); got != 0 {
		t.Fatalf("unconfirmed encoded candidate changed current QG count=%v", got)
	}
	recorder.Observe(context.Background(), observability.Observation{Component: observability.ComponentControlPlane,
		Stage: observability.StageActiveQGSet, Result: observability.ResultSuccess,
		ActiveQGSet: &observability.ActiveQGSetFacts{Operation: "renew", Result: "success", QueryGroups: 7, ObjectBytes: 321, Duration: time.Second}})
	recorder.Observe(context.Background(), observability.Observation{Component: observability.ComponentControlPlane,
		Stage: observability.StageLegacyQGMigration, Result: observability.ResultFailed,
		LegacyMigration: &observability.LegacyQGMigrationFacts{Result: "fail_closed", ReasonClass: "contract", ScanKeys: 12, Duration: time.Second}})
	if got := testutil.ToFloat64(recorder.phaseTwo.activeQGSetCount); got != 7 {
		t.Fatalf("active QG count=%v", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.activeQGSetBytes); got != 321 {
		t.Fatalf("active set bytes=%v", got)
	}
	if got := testutil.CollectAndCount(recorder.phaseTwo.activeQGSetEncode); got != 1 {
		t.Fatalf("encode metric families=%d", got)
	}
	if got := testutil.CollectAndCount(recorder.phaseTwo.activeQGSetRedis); got != 1 {
		t.Fatalf("redis metric families=%d", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.legacyMigration.WithLabelValues("fail_closed", "contract")); got != 1 {
		t.Fatalf("migration total=%v", got)
	}
}

func TestPhaseTwoDrainingQueryGroupGaugeHasNoIdentityLabel(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageDrainingQGReconciled,
		Result: observability.ResultSuccess,
		DrainingQG: &observability.DrainingQGFacts{Total: 3, Undrained: 2, Isolated: 1,
			Samples: []observability.DrainingQGSample{{QueryGroupKey: "must-not-be-a-label"}}},
	})
	if got := testutil.ToFloat64(recorder.phaseTwo.undrainedDrainingQueryGroups); got != 2 {
		t.Fatalf("undrained draining query groups = %v, want 2", got)
	}
	if got := testutil.CollectAndCount(recorder.phaseTwo.undrainedDrainingQueryGroups); got != 1 {
		t.Fatalf("undrained gauge metric families=%d, want 1", got)
	}
}

func TestPhaseTwoOwnershipMetricsStayLowCardinality(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.SetOwnedQueryGroups(7)
	if got := testutil.ToFloat64(recorder.phaseTwo.ownedQueryGroups.WithLabelValues("complete")); got != 7 {
		t.Fatalf("owned query groups = %v, want 7", got)
	}

	for _, stage := range []observability.Stage{
		observability.StageAssignmentAcquired,
		observability.StageAssignmentLost,
		observability.StageTakeoverStarted,
		observability.StageTakeoverCompleted,
		observability.StageFenceChecked,
	} {
		recorder.Observe(context.Background(), observability.Observation{
			Component:  observability.ComponentOwnership,
			Stage:      stage,
			Result:     observability.ResultSuccess,
			ReasonCode: observability.ReasonNone,
			Trace: observability.TraceFields{
				QueryGroupKey: "high-cardinality-qg",
				OwnerID:       "high-cardinality-worker",
			},
		})
		if got := testutil.ToFloat64(recorder.phaseTwo.ownershipTransitions.WithLabelValues(string(stage), "success", "none")); got != 1 {
			t.Fatalf("%s ownership transitions = %v, want 1", stage, got)
		}
	}
	if isOwnershipTransitionStage(observability.StageLeaseRenewed) {
		t.Fatal("lease_renewed was admitted as a fixed ownership transition")
	}
}

func TestPhaseTwoQueryPermitMetricsUseOnlyFixedQueueOperationAndAdmissionLabels(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentScheduler, Stage: observability.StageQueryAdmission,
		Result: observability.ResultStarted, Operation: observability.OperationReplay,
		QueryPermit: &observability.QueryPermitFacts{
			QueueKind: observability.QueryQueueRecovery, Admission: true,
			NormalWaiting: 2, RecoveryWaiting: 3, NormalInflight: 4,
			RetryInflight: 1, ReplayInflight: 2, ProbeInflight: 1, RecoveryInflight: 4,
		},
	})
	// Only the admission counter is published from a permit event now. The
	// queue depth and inflight levels moved to the permit collector, which reads
	// them when the metric is scraped: a level published only at the moments it
	// changes reports the boundary rather than the interval.
	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"admission", testutil.ToFloat64(recorder.phaseTwo.queryAdmission.WithLabelValues("replay", "started")), 1},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Fatalf("%s = %v, want %v", check.name, check.got, check.want)
		}
	}
}

func TestCapacityMetricRequiresExplicitBoundedBudget(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentResource, Stage: observability.StageResourceHard,
		Result: observability.ResultPaused, ReasonCode: observability.ReasonContractDeterministic,
	})
	if got, err := testutil.GatherAndCount(recorder.Gatherer(), "bkmonitor_alarmd_capacity_transition_total"); err != nil || got != 0 {
		t.Fatalf("capacity metric families without budget = %v, error = %v", got, err)
	}
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentResource, Stage: observability.StageResourceHard,
		Result: observability.ResultPaused, ReasonCode: observability.ReasonContractDeterministic,
		CapacityBudget: observability.CapacityBudgetEvents,
	})
	if got := testutil.ToFloat64(recorder.phaseTwo.capacity.WithLabelValues("events", "rejected")); got != 1 {
		t.Fatalf("events rejection = %v, want 1", got)
	}
}

func TestWorkerWorkCountsOnlyCompletedEventACKAndProgress(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	eventACK := recorder.phaseTwo.work.WithLabelValues("event_ack")
	progressCommit := recorder.phaseTwo.work.WithLabelValues("progress_commit")
	for _, observation := range []observability.Observation{
		{Component: observability.ComponentOutput, Stage: observability.StageEventACKed,
			Result: observability.ResultFailed, Counts: observability.Counts{Events: 2}, Err: errors.New("ack failed")},
		{Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
			Result: observability.ResultFailed, Err: errors.New("commit failed")},
	} {
		recorder.Observe(context.Background(), observation)
	}
	if eventGot, progressGot := testutil.ToFloat64(eventACK), testutil.ToFloat64(progressCommit); eventGot != 0 || progressGot != 0 {
		t.Fatalf("failed attempts counted as completed work = event %v, progress %v", eventGot, progressGot)
	}

	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageEventACKed,
		Result: observability.ResultSuccess, Counts: observability.Counts{Events: 2},
	})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
		Result: observability.ResultDegraded, ReasonCode: observability.ReasonContractCoverage,
	})
	if got := testutil.ToFloat64(eventACK); got != 2 {
		t.Fatalf("successful event ACK work = %v, want 2", got)
	}
	if got := testutil.ToFloat64(progressCommit); got != 1 {
		t.Fatalf("committed degraded progress work = %v, want 1", got)
	}
}

// The compile counter splits the strategies of a refresh round by whether the
// round compiled them or took them from an earlier round's compilation of the
// same document; the two series add up to the strategies of the round.
func TestPhaseTwoSourceCompileCounterSplitsCompiledFromReused(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentControlPlane,
		Stage:     observability.StageSnapshotRefreshed,
		Result:    observability.ResultSuccess,
		SourceRefresh: &observability.SourceRefreshFacts{
			Status: observability.AllSourceRefreshStatuses()[0], CompiledStrategies: 3, ReusedStrategies: 7,
		},
	})
	if got := testutil.ToFloat64(recorder.phaseTwo.sourceCompiles.WithLabelValues("compiled")); got != 3 {
		t.Fatalf("compiled = %v, want 3", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.sourceCompiles.WithLabelValues("reused")); got != 7 {
		t.Fatalf("reused = %v, want 7", got)
	}
	if got := testutil.CollectAndCount(recorder.phaseTwo.sourceCompiles); got != len(sourceCompileResults) {
		t.Fatalf("compile series = %d, want the %d fixed results and nothing else", got, len(sourceCompileResults))
	}
}

// The activation hold gauges follow the latest attempt: an attempt that held
// Query Groups sets them, and the next attempt that held none clears them.
func TestPhaseTwoActivationHoldGaugesFollowTheLatestAttempt(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	observe := func(held int, age int64) {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageActivationHold,
			Result:         observability.ResultSuccess,
			ActivationHold: &observability.ActivationHoldFacts{Reappeared: 3, Held: held, MaxAgeSeconds: age},
		})
	}
	observe(2, 540)
	if held, age := testutil.ToFloat64(recorder.phaseTwo.activationHeldQueryGroups), testutil.ToFloat64(recorder.phaseTwo.activationHeldAgeSecondsMax); held != 2 || age != 540 {
		t.Fatalf("held=%v age=%v, want 2 held for 540 s", held, age)
	}
	observe(0, 0)
	if held, age := testutil.ToFloat64(recorder.phaseTwo.activationHeldQueryGroups), testutil.ToFloat64(recorder.phaseTwo.activationHeldAgeSecondsMax); held != 0 || age != 0 {
		t.Fatalf("held=%v age=%v, want the gauges cleared by an attempt that held nothing", held, age)
	}
}

// A refresh round says how it read its source. The counter splits rounds by
// mode and reason, the strategies-read counter grows only on rounds that read,
// and the signal age follows the latest round: a value while the round found
// a signal, NaN when it found none, so a signal that disappears does not leave
// its last age standing as if it were still being measured.
func TestPhaseTwoSourceReadCounterAndSignalAgeFollowTheRound(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	observe := func(facts observability.SourceRefreshFacts) {
		facts.Status = observability.SourceRefreshUnchanged
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
			Result: observability.ResultSuccess, SourceRefresh: &facts,
		})
	}
	reads := func(mode observability.SourceReadMode, reason observability.SourceReadReason) float64 {
		return testutil.ToFloat64(recorder.phaseTwo.sourceReads.WithLabelValues(string(mode), string(reason)))
	}
	observe(observability.SourceRefreshFacts{ReadMode: observability.SourceReadFull, ReadReason: observability.SourceReadElected,
		StrategiesRead: 2, ChangeSignalPresent: true, ChangeSignalAgeSeconds: 30})
	if reads(observability.SourceReadFull, observability.SourceReadElected) != 1 ||
		testutil.ToFloat64(recorder.phaseTwo.sourceStrategiesRead) != 2 ||
		testutil.ToFloat64(recorder.phaseTwo.sourceChangeSignalAge) != 30 {
		t.Fatalf("after a full read: reads=%v strategies=%v age=%v", reads(observability.SourceReadFull, observability.SourceReadElected),
			testutil.ToFloat64(recorder.phaseTwo.sourceStrategiesRead), testutil.ToFloat64(recorder.phaseTwo.sourceChangeSignalAge))
	}
	observe(observability.SourceRefreshFacts{ReadMode: observability.SourceReadSkipped, ReadReason: observability.SourceReadUnchanged,
		ChangeSignalPresent: true, ChangeSignalAgeSeconds: 90})
	if reads(observability.SourceReadSkipped, observability.SourceReadUnchanged) != 1 ||
		testutil.ToFloat64(recorder.phaseTwo.sourceStrategiesRead) != 2 ||
		testutil.ToFloat64(recorder.phaseTwo.sourceChangeSignalAge) != 90 {
		t.Fatalf("after a skipped round: strategies=%v age=%v, want nothing read and the age moved on",
			testutil.ToFloat64(recorder.phaseTwo.sourceStrategiesRead), testutil.ToFloat64(recorder.phaseTwo.sourceChangeSignalAge))
	}
	observe(observability.SourceRefreshFacts{ReadMode: observability.SourceReadFull, ReadReason: observability.SourceReadMissing, StrategiesRead: 2})
	if reads(observability.SourceReadFull, observability.SourceReadMissing) != 1 ||
		testutil.ToFloat64(recorder.phaseTwo.sourceStrategiesRead) != 4 ||
		!math.IsNaN(testutil.ToFloat64(recorder.phaseTwo.sourceChangeSignalAge)) {
		t.Fatalf("after a round without a signal: strategies=%v age=%v, want the age unset",
			testutil.ToFloat64(recorder.phaseTwo.sourceStrategiesRead), testutil.ToFloat64(recorder.phaseTwo.sourceChangeSignalAge))
	}
	// A pair no round can report is not counted and does not add to what was read.
	observe(observability.SourceRefreshFacts{ReadMode: observability.SourceReadSkipped, ReadReason: observability.SourceReadChanged, StrategiesRead: 5})
	if testutil.ToFloat64(recorder.phaseTwo.sourceStrategiesRead) != 4 {
		t.Fatalf("an impossible read outcome added to strategies read: %v", testutil.ToFloat64(recorder.phaseTwo.sourceStrategiesRead))
	}
}

// The pruned-cursor count is a gauge of the latest draining view, beside the
// undrained count it is a subset of.
func TestPhaseTwoDrainingCursorPrunedGaugeFollowsTheLatestView(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	observe := func(undrained, pruned int) {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageDrainingQGReconciled,
			Result: observability.ResultSuccess, Operation: observability.OperationLoad,
			DrainingQG: &observability.DrainingQGFacts{Total: undrained, Undrained: undrained, CursorPruned: pruned},
		})
	}
	observe(12, 12)
	if got := testutil.ToFloat64(recorder.phaseTwo.drainingCursorPrunedQueryGroups); got != 12 {
		t.Fatalf("draining_cursor_pruned_query_groups = %v, want 12", got)
	}
	observe(3, 0)
	if got := testutil.ToFloat64(recorder.phaseTwo.drainingCursorPrunedQueryGroups); got != 0 {
		t.Fatalf("draining_cursor_pruned_query_groups after a view with none = %v, want 0", got)
	}
}

// The planned-move count is a gauge of the latest successful rebalance
// planning round; a failed round leaves the last plan in place rather than
// reporting zero moves it never computed.
func TestPhaseTwoRebalancePlannedMovesGaugeFollowsTheLatestPlan(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	observe := func(result observability.Result, facts *observability.RebalanceFacts) {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentOwnership, Stage: observability.StageRebalancePlanned,
			Result: result, Operation: observability.OperationLoad, Rebalance: facts,
		})
	}
	observe(observability.ResultSuccess, &observability.RebalanceFacts{ReadyWorkers: 2, Assigned: 18, PlannedMoves: 3})
	if got := testutil.ToFloat64(recorder.phaseTwo.rebalancePlannedMoves); got != 3 {
		t.Fatalf("rebalance_planned_moves = %v, want 3", got)
	}
	observe(observability.ResultSuccess, &observability.RebalanceFacts{ReadyWorkers: 2, Assigned: 18})
	if got := testutil.ToFloat64(recorder.phaseTwo.rebalancePlannedMoves); got != 0 {
		t.Fatalf("rebalance_planned_moves after an even round = %v, want 0", got)
	}
}

// Gauges of the observe-only family emit no series until their first
// observation: a zero before the first computation would read exactly like
// a healthy steady state.
func TestPhaseTwoObserveOnlyGaugesEmitNoSeriesUntilFirstObservation(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	family := map[string]*loadedGauge{
		"undrained_draining_query_groups":     recorder.phaseTwo.undrainedDrainingQueryGroups,
		"draining_cursor_pruned_query_groups": recorder.phaseTwo.drainingCursorPrunedQueryGroups,
		"rebalance_planned_moves":             recorder.phaseTwo.rebalancePlannedMoves,
		"activation_held_query_groups":        recorder.phaseTwo.activationHeldQueryGroups,
		"activation_held_age_seconds_max":     recorder.phaseTwo.activationHeldAgeSecondsMax,
		"assignment_index_stale_rounds":       recorder.phaseTwo.assignmentIndexStaleRounds,
	}
	for name, gauge := range family {
		if got := testutil.CollectAndCount(gauge); got != 0 {
			t.Fatalf("%s emitted %d series before any observation", name, got)
		}
	}
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentControlPlane, Stage: observability.StageDrainingQGReconciled,
		Result: observability.ResultSuccess, Operation: observability.OperationLoad,
		DrainingQG: &observability.DrainingQGFacts{},
	})
	for _, name := range []string{"undrained_draining_query_groups", "draining_cursor_pruned_query_groups"} {
		if got := testutil.CollectAndCount(family[name]); got != 1 || testutil.ToFloat64(family[name]) != 0 {
			t.Fatalf("%s after its first observation emitted %d series", name, got)
		}
	}
	if got := testutil.CollectAndCount(family["rebalance_planned_moves"]); got != 0 {
		t.Fatalf("an unrelated observation loaded rebalance_planned_moves (%d series)", got)
	}
}

// Index reads count by result, confirmations count by outcome, record reads
// count by path, the stale-round gauge follows the latest read, and writes
// count by success or failure.
func TestPhaseTwoAssignmentIndexMetricsFollowObservations(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageAssignmentIndexRead,
		Result: observability.ResultSuccess, Operation: observability.OperationLoad,
		AssignmentIndex: &observability.AssignmentIndexFacts{Result: observability.AssignmentIndexStale, StaleRounds: 4, Opened: 2, Released: 1, Reads: 3},
	})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageAssignmentIndexRead,
		Result: observability.ResultSuccess, Operation: observability.OperationLoad,
		AssignmentIndex: &observability.AssignmentIndexFacts{Result: observability.AssignmentIndexMissing, Reads: 900, FullRead: true},
	})
	recorder.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentOwnership, Stage: observability.StageAssignmentIndexWritten,
		Result: observability.ResultFailed, Operation: observability.OperationWrite,
		AssignmentIndex: &observability.AssignmentIndexFacts{Workers: 2},
	})
	if got := testutil.ToFloat64(recorder.phaseTwo.assignmentIndexStaleRounds); got != 0 {
		t.Fatalf("assignment_index_stale_rounds = %v, want the latest read's 0", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.assignmentIndexReads.WithLabelValues(observability.AssignmentIndexStale)); got != 1 {
		t.Fatalf("assignment_index_read_total{stale} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.assignmentIndexConfirm.WithLabelValues(observability.AssignmentIndexOpened)); got != 2 {
		t.Fatalf("assignment_index_confirm_total{opened} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.assignmentIndexConfirm.WithLabelValues(observability.AssignmentIndexReleased)); got != 1 {
		t.Fatalf("assignment_index_confirm_total{released} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.assignmentRecordReads.WithLabelValues("index")); got != 3 {
		t.Fatalf("assignment_record_read_total{index} = %v, want 3", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.assignmentRecordReads.WithLabelValues("full")); got != 900 {
		t.Fatalf("assignment_record_read_total{full} = %v, want 900", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.assignmentIndexWrites.WithLabelValues("failed")); got != 1 {
		t.Fatalf("assignment_index_write_total{failed} = %v, want 1", got)
	}
}

func TestPhaseTwoScheduleCursorAdvanceCountsByOutcome(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	for _, status := range []string{observability.CursorAdvanceApplied, observability.CursorAdvanceApplied, observability.CursorAdvanceConflict} {
		recorder.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageScheduleCursorAdvanced,
			Result: observability.ResultSuccess, Operation: observability.OperationWrite,
			CursorAdvance: &observability.CursorAdvanceFacts{From: 120, To: 600, Status: status},
		})
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.scheduleCursorAdvances.WithLabelValues(observability.CursorAdvanceApplied)); got != 2 {
		t.Fatalf("schedule_cursor_advance_total{applied} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(recorder.phaseTwo.scheduleCursorAdvances.WithLabelValues(observability.CursorAdvanceConflict)); got != 1 {
		t.Fatalf("schedule_cursor_advance_total{conflict} = %v, want 1", got)
	}
}
