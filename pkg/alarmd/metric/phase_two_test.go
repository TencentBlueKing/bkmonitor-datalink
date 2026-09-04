// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"errors"
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
	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"normal queue", testutil.ToFloat64(recorder.phaseTwo.readyQueue.WithLabelValues("normal")), 2},
		{"recovery queue", testutil.ToFloat64(recorder.phaseTwo.readyQueue.WithLabelValues("recovery")), 3},
		{"normal inflight", testutil.ToFloat64(recorder.phaseTwo.queryInflight.WithLabelValues("normal")), 4},
		{"retry inflight", testutil.ToFloat64(recorder.phaseTwo.queryInflight.WithLabelValues("retry")), 1},
		{"replay inflight", testutil.ToFloat64(recorder.phaseTwo.queryInflight.WithLabelValues("replay")), 2},
		{"probe inflight", testutil.ToFloat64(recorder.phaseTwo.queryInflight.WithLabelValues("probe")), 1},
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
