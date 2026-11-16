// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// A closed-segment attempt overtaken by newer state counts on the read hold's
// overtaken reading whichever way the attempt met it: refused at a State
// write that met a later one, as before, or having skipped series a later
// Slot had already written -- the shape the preflight refusal took before it
// became a skip. Read from the refusal alone, the reading stopped the day the
// refusal did. The gate on the group's record is unchanged: an attempt whose
// segment is still the group's is not counted, skip or not. And the skip's
// count and sample reach the completion line.
func TestAnAttemptThatSkippedSupersededSeriesCountsAsOvertaken(t *testing.T) {
	const qg = execution.QueryGroupIdentity("qg-overtaken")
	at := time.UnixMilli(1_788_000_000_000)
	sample := &execution.SupersededSample{Series: "series-a",
		Slot:   execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 1_788_000_060, SlotDigest: "slot-k"},
		Stored: execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 1_788_000_120, SlotDigest: "slot-k1"}}
	for _, test := range []struct {
		name   string
		closed bool
		result execution.SlotExecutionResult
		err    error
		want   uint64
	}{
		{name: "skipped on a closed segment", closed: true, want: 1,
			result: execution.SlotExecutionResult{Completed: true, Superseded: execution.SupersededSeries{Count: 2, Sample: sample}}},
		{name: "refused stale on a closed segment", closed: true, want: 1,
			err: &worker.StateConflictError{Stage: "state apply did not complete", Status: string(execution.StateApplyStale)}},
		{name: "nothing skipped on a closed segment", closed: true, want: 0,
			result: execution.SlotExecutionResult{Completed: true}},
		{name: "skipped on the group's own segment", closed: false, want: 0,
			result: execution.SlotExecutionResult{Completed: true, Superseded: execution.SupersededSeries{Count: 2, Sample: sample}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(readhold.Record{SinceSlot: 1, SegmentStart: 1_787_999_940, Closed: test.closed})
			if err != nil {
				t.Fatal(err)
			}
			store := &runtimeTestReadHoldControl{values: map[execution.QueryGroupIdentity][]byte{qg: raw}}
			holds := inheritedTestHolds(t, store, func() time.Time { return at })
			holds.load(context.Background(), []execution.QueryGroupIdentity{qg})
			if _, loaded := holds.controller.Reading(qg); !loaded {
				t.Fatal("fixture: the group's record is not loaded")
			}
			var completions []observability.Observation
			executor := observedProductionSlotExecutor{
				next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
					return test.result, test.err
				}),
				observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
					if observation.Stage == observability.StageSlotCompleted {
						completions = append(completions, observation)
					}
				}),
				readHolds: holds,
			}
			request := execution.SlotExecutionRequest{Contract: execution.FrozenExecutionContractRef{
				Slot: execution.SlotIdentity{QueryGroup: qg, EvaluationTime: 1_788_000_060}, ScheduleSegmentStart: 1_787_999_940}}
			_, _ = executor.Execute(context.Background(), request)
			if got := holds.overtaken.Load(); got != test.want {
				t.Fatalf("overtaken = %d, want %d", got, test.want)
			}
			if len(completions) != 1 {
				t.Fatalf("completion lines = %d, want 1", len(completions))
			}
			facts := completions[0].SlotSuperseded
			if test.result.Superseded.Count == 0 {
				if facts != nil {
					t.Fatalf("a completion that skipped nothing carries %+v", facts)
				}
				return
			}
			if facts == nil || facts.Series != 2 || facts.Sample == nil || facts.Sample.Series != "series-a" ||
				facts.Sample.Stored.EvaluationTime != 1_788_000_120 || facts.Sample.Slot.EvaluationTime != 1_788_000_060 {
				t.Fatalf("completion superseded facts = %+v", facts)
			}
		})
	}
}
