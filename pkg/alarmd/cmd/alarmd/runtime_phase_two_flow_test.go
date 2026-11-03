// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

// Every decided Slot's lag from readable data -- evaluation time plus read
// hold -- to its completion is heard once, whatever its period; a Slot
// booked without a decision (skipped, snapshot unavailable) is not.
func TestEveryDecidedSlotsLagIsHeardOnce(t *testing.T) {
	var lags []time.Duration
	evaluation := time.Now().Add(-90 * time.Second).Truncate(time.Second)
	request := execution.SlotExecutionRequest{AttemptNo: 1, Operation: execution.OperationNormal}
	request.Contract.Slot.EvaluationTime = execution.EvaluationTime(evaluation.Unix())
	request.Contract.ReadHoldMillis = 30_000
	for _, kind := range []execution.CompletionKind{execution.CompletionFull, execution.CompletionGapSkipped, execution.CompletionSnapshotUnavailable} {
		executor := observedProductionSlotExecutor{
			next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
				return execution.SlotExecutionResult{Completed: true, CompletionKind: kind, Result: observability.ResultSuccess}, nil
			}),
			observer:    observability.ObserverFunc(func(context.Context, observability.Observation) {}),
			decisionLag: func(lag time.Duration) { lags = append(lags, lag) },
		}
		if _, err := executor.Execute(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	if len(lags) != 1 || lags[0] < 55*time.Second || lags[0] > 70*time.Second {
		t.Fatalf("lags = %v, want one of about a minute (90 s since evaluation less the 30 s hold)", lags)
	}
}

// The lag is a normal round's: a normal Slot and its retry are heard; a
// replay, a probe and a supplement are late by what they are -- a missed
// Slot run again, a recovery's test read, a completed Slot read once more --
// and are not, so the histogram shows the rounds as they run.
func TestOnlyNormalRoundsLagIsHeard(t *testing.T) {
	evaluation := time.Now().Add(-90 * time.Second).Truncate(time.Second)
	heard := map[execution.Operation]int{}
	for _, operation := range []execution.Operation{execution.OperationNormal, execution.OperationRetry,
		execution.OperationReplay, execution.OperationProbe, execution.OperationSupplement} {
		request := execution.SlotExecutionRequest{AttemptNo: 1, Operation: operation}
		request.Contract.Slot.EvaluationTime = execution.EvaluationTime(evaluation.Unix())
		executor := observedProductionSlotExecutor{
			next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
				return execution.SlotExecutionResult{Completed: true, CompletionKind: execution.CompletionFull, Result: observability.ResultSuccess}, nil
			}),
			observer:    observability.ObserverFunc(func(context.Context, observability.Observation) {}),
			decisionLag: func(time.Duration) { heard[operation]++ },
		}
		if _, err := executor.Execute(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	want := map[execution.Operation]int{execution.OperationNormal: 1, execution.OperationRetry: 1}
	if !maps.Equal(heard, want) {
		t.Fatalf("heard = %v, want %v", heard, want)
	}
}

// The failure causes the metric holds are the ones the Source reports.
func TestTheQueryFailureCausesAreOneList(t *testing.T) {
	r := metric.NewRecorder(metric.BuildInfo{})
	for _, cause := range access.QueryFailures {
		r.RecordPhysicalQuery(time.Millisecond, cause)
	}
	flow := r.FlowCounters()
	for _, cause := range access.QueryFailures {
		if flow.QueryFailures[cause] != 1 {
			t.Fatalf("cause %s counted %d, want its own count: the metric's list is not the Source's", cause, flow.QueryFailures[cause])
		}
	}
	if len(flow.QueryFailures) != len(access.QueryFailures) || !slices.Contains(access.QueryFailures, "other") {
		t.Fatalf("failures = %v", flow.QueryFailures)
	}
}

// The block a replica publishes is the metrics' values, with the roles it
// runs and the heap only once sampled.
func TestTheFlowBlockIsTheMetricsValues(t *testing.T) {
	r := metric.NewRecorder(metric.BuildInfo{})
	r.RecordPhysicalQuery(time.Second, access.QueryFailureTimeout)
	r.RecordDecisionLag(2 * time.Second)
	facts := flowFactsSource(r, roles.Set{roles.Worker, roles.Channel})()
	if facts.Queries != 1 || facts.QueryFailures[access.QueryFailureTimeout] != 1 || facts.DecisionLag.Count != 1 ||
		len(facts.QueryDuration.Buckets) != len(fleet.FlowQueryBounds) || !slices.Equal(facts.Roles, []string{"worker", "channel"}) ||
		facts.LiveHeapBytes != nil {
		t.Fatalf("flow facts = %+v", facts)
	}
}
