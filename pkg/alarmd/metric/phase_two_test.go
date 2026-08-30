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

func TestPhaseTwoMetricsStayInsideSeriesBudget(t *testing.T) {
	if got := MaxCustomSeries(); got > CustomSeriesBudget {
		t.Fatalf("MaxCustomSeries() = %d, budget = %d", got, CustomSeriesBudget)
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
