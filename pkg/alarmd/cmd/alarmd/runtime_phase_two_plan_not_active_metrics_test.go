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
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// How many Slots ended PLAN_NOT_ACTIVE is read from /metrics, whatever the
// log samples: slot_completed_reasons_total counts each Slot once, at its
// end, under the word its slot_completed line writes.

// servedSample is the value /metrics serves for the sample of family name
// with exactly labels, and whether it serves one at all: an absent sample is
// not a zero.
func servedSample(t *testing.T, recorder *metric.Recorder, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	served := httptest.NewRecorder()
	promhttp.HandlerFor(recorder.Gatherer(), promhttp.HandlerOpts{}).ServeHTTP(served, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range strings.Split(served.Body.String(), "\n") {
		open, end := strings.IndexByte(line, '{'), strings.LastIndexByte(line, '}')
		if open < 0 || end < open || line[:open] != name {
			continue
		}
		got := map[string]string{}
		for _, pair := range strings.Split(line[open+1:end], ",") {
			if key, value, ok := strings.Cut(pair, "="); ok {
				got[key] = strings.Trim(value, `"`)
			}
		}
		if len(got) != len(labels) {
			continue
		}
		matched := true
		for key, value := range labels {
			matched = matched && got[key] == value
		}
		if !matched {
			continue
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(line[end+1:]), 64)
		if err != nil {
			t.Fatalf("/metrics line %q: %v", line, err)
		}
		return value, true
	}
	return 0, false
}

// slotsEnded is how many Slots /metrics says ended under reason; a cell it
// does not serve fails, since every cell exists from the start.
func slotsEnded(t *testing.T, recorder *metric.Recorder, reason string) float64 {
	t.Helper()
	value, served := servedSample(t, recorder, "alarmd_slot_completed_reasons_total", map[string]string{"reason": reason})
	if !served {
		t.Fatalf("/metrics serves no slot_completed_reasons_total{reason=%q}", reason)
	}
	return value
}

// A process that has run no Slot serves every cell a PLAN_NOT_ACTIVE
// reading is made of, at zero: an absent series is not a reading.
func TestAFreshProcessServesThePlanNotActiveCellsAtZero(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	for _, reason := range []string{contract.ReasonPlanNotActive, contract.ReasonConfigDrift, contract.ReasonPlanReactivated, string(observability.ReasonNone), string(observability.ReasonOther)} {
		if got := slotsEnded(t, recorder, reason); got != 0 {
			t.Fatalf("a fresh process serves slot_completed_reasons_total{reason=%q} %v, want 0", reason, got)
		}
	}
	for _, reason := range execution.ForwardSkipReasons {
		for _, inFlight := range []string{"true", "false"} {
			labels := map[string]string{"result": observability.CursorAdvanceApplied, "refusal": "", "reason": string(reason), "in_flight": inFlight}
			if got, served := servedSample(t, recorder, "alarmd_schedule_cursor_advance_total", labels); !served || got != 0 {
				t.Fatalf("a fresh process serves schedule_cursor_advance_total%v = (%v, served %v), want 0", labels, got, served)
			}
		}
	}
}

// A Slot whose strategy was deleted while it ran ends PLAN_NOT_ACTIVE and
// is counted so, once; the Slot after it, which ends whole, adds nothing
// there.
func TestASlotThatEndsPlanNotActiveIsCountedOnceAndAWholeOneIsNot(t *testing.T) {
	fixture := startSharedGroupFixture(t)
	ctx := context.Background()
	inFlight := execution.EvaluationTime(fixture.base + 60)
	fixture.stallInFlight(ctx, inFlight)
	if got := slotsEnded(t, fixture.recorder, contract.ReasonPlanNotActive); got != 0 {
		t.Fatalf("before the edit slot_completed_reasons_total{PLAN_NOT_ACTIVE} = %v, want 0", got)
	}
	installSharedGroup(t, ctx, fixture.redisClient, map[int64][]byte{
		1003: sharedGroupStrategy(t, 1003, 1725000000, nil),
	})
	fixture.cutOverAt(ctx, inFlight+1)
	if ended := fixture.runUntilEnded(ctx, inFlight); ended != "COMPLETED_WITH_PARTIAL_GAP/"+contract.ReasonPlanNotActive {
		t.Fatalf("the in-flight Slot ended %q, want PLAN_NOT_ACTIVE", ended)
	}
	if got := slotsEnded(t, fixture.recorder, contract.ReasonPlanNotActive); got != 1 {
		t.Fatalf("after the Slot ended slot_completed_reasons_total{PLAN_NOT_ACTIVE} = %v, want 1", got)
	}
	whole := slotsEnded(t, fixture.recorder, string(observability.ReasonNone))
	if ended := fixture.runUntilEnded(ctx, inFlight+60); ended != fullEnding {
		t.Fatalf("the Slot after the edit ended %q, want it whole", ended)
	}
	if got := slotsEnded(t, fixture.recorder, contract.ReasonPlanNotActive); got != 1 {
		t.Fatalf("a whole Slot moved slot_completed_reasons_total{PLAN_NOT_ACTIVE} to %v, want it left at 1", got)
	}
	if got := slotsEnded(t, fixture.recorder, string(observability.ReasonNone)); got != whole+1 {
		t.Fatalf("slot_completed_reasons_total{none} = %v after the whole Slot, want %v", got, whole+1)
	}
}

// executedOnce runs one Slot through the production observed executor,
// answering as the worker answered, into recorder.
func executedOnce(t *testing.T, recorder *metric.Recorder, result execution.SlotExecutionResult) {
	t.Helper()
	executor := observedProductionSlotExecutor{
		next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
			return result, nil
		}),
		observer: recorder,
	}
	if _, err := executor.Execute(context.Background(), execution.SlotExecutionRequest{}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

// A Slot that ends PLAN_NOT_ACTIVE while its primary input was unavailable
// is filed by its cause as PRIMARY_INPUT_UNAVAILABLE, and keeps the change's
// word as its own (worker TestActivationChangeIsClassifiedFromTheSameComparisonAsDrift):
// it is counted under that word, PLAN_NOT_ACTIVE, once.
func TestASlotEndingPlanNotActiveWithItsPrimaryUnavailableIsCountedUnderItsWord(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	executedOnce(t, recorder, execution.SlotExecutionResult{
		Completed: true, CompletionKind: execution.CompletionUnavailable, Result: observability.ResultDegraded,
		ReasonCode:      execution.ReasonCode(contract.ReasonPlanNotActive),
		CompletionCause: execution.SlotCompletionCause{Cause: execution.CausePrimaryInputUnavailable, Reason: execution.ReasonCode(contract.ReasonQueryTimeout)},
	})
	if got := slotsEnded(t, recorder, contract.ReasonPlanNotActive); got != 1 {
		t.Fatalf("slot_completed_reasons_total{PLAN_NOT_ACTIVE} = %v, want 1", got)
	}
}

// A round that retries a Slot under PLAN_NOT_ACTIVE - a Guard written for
// another Plan of it this attempt - has not ended it: nothing is counted
// until the round that ends it, and then once.
func TestARetriedSlotIsCountedWhenItEndsNotWhenItRetries(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	executedOnce(t, recorder, execution.SlotExecutionResult{
		Result: observability.ResultRetrying, ReasonCode: execution.ReasonCode(contract.ReasonPlanNotActive),
	})
	if got := slotsEnded(t, recorder, contract.ReasonPlanNotActive); got != 0 {
		t.Fatalf("after the retry slot_completed_reasons_total{PLAN_NOT_ACTIVE} = %v, want 0", got)
	}
	executedOnce(t, recorder, execution.SlotExecutionResult{
		Completed: true, CompletionKind: execution.CompletionPartialGap, Result: observability.ResultDegraded,
		ReasonCode: execution.ReasonCode(contract.ReasonPlanNotActive),
	})
	if got := slotsEnded(t, recorder, contract.ReasonPlanNotActive); got != 1 {
		t.Fatalf("after the Slot ended slot_completed_reasons_total{PLAN_NOT_ACTIVE} = %v, want 1", got)
	}
}
