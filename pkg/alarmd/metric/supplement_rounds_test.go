// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// supplementComponents is the component that reports each stage of a
// supplement: an observation under any other is not the stage at all.
var supplementComponents = map[observability.Stage]observability.Component{
	observability.StageSlotStarted: observability.ComponentScheduler, observability.StageSlotCompleted: observability.ComponentScheduler,
	observability.StageRunnerCompleted: observability.ComponentScheduler, observability.StageSlotSourceCompleted: observability.ComponentScheduler,
	observability.StageQueryCompleted: observability.ComponentAccess, observability.StageEvaluationCompleted: observability.ComponentEvaluation,
	observability.StageGapLoaded: observability.ComponentState, observability.StageStatePreflight: observability.ComponentState,
	observability.StageStateAdmission: observability.ComponentState, observability.StageStateApplied: observability.ComponentState,
	observability.StageEventACKed: observability.ComponentOutput,
}

// fullSupplement is every observation a supplement of an earlier Slot
// reports, in order, under the given operation: its start, the gap and state
// it loaded, its evaluation, the state it admitted and applied, its events, a
// query that failed, its source and runner returning, and its completion,
// which completes no Slot and nears its retained share.
func fullSupplement(operation observability.Operation) []observability.Observation {
	run := []observability.Observation{}
	for _, stage := range []observability.Stage{observability.StageSlotStarted, observability.StageGapLoaded,
		observability.StageStatePreflight, observability.StageEvaluationCompleted, observability.StageStateAdmission,
		observability.StageStateApplied, observability.StageEventACKed, observability.StageQueryCompleted,
		observability.StageSlotSourceCompleted, observability.StageRunnerCompleted, observability.StageSlotCompleted} {
		observation := observability.Observation{Component: supplementComponents[stage], Stage: stage, Operation: operation,
			Result: observability.ResultSuccess, Duration: time.Second}
		switch stage {
		case observability.StageQueryCompleted:
			observation.Result = observability.ResultFailed
			observation.QueryFailure = &observability.QueryFailureFacts{Stage: "provider", Category: "provider_transport", Code: "QUERY_TIMEOUT"}
		case observability.StageSlotCompleted:
			observation.ExecuteOutcome = "incomplete"
			observation.SlotBudgetUsage = &observability.SlotBudgetUsageFacts{RetainedBytes: 99, RetainedShareBytes: 100}
		}
		run = append(run, observation)
	}
	return run
}

// The metrics that count rounds read the same with a whole supplement between
// the rounds as without, whichever of its stages each reads: an executor
// return, a Slot's own timings, a completed Slot near its retained share, and
// which progress timestamps are set -- a stall is read from those, and a
// supplement keeping them fresh would hide rounds that stopped. The same
// observations under a round's operation change every one of them, which
// shows each is reached.
func TestTheRoundMetricsLeaveAWholeSupplementOut(t *testing.T) {
	readings := func(operation observability.Operation) map[string]string {
		r := NewRecorder(BuildInfo{})
		ctx := context.Background()
		// A round that commits its Progress and nothing else: the one
		// progress timestamp the rounds set.
		r.Observe(ctx, observability.Observation{Component: observability.ComponentProgress, Stage: observability.StageProgressCommitted,
			Operation: observability.OperationNormal, Result: observability.ResultSuccess, ProgressCompletionKind: "FULL_COMPLETED"})
		if operation != "" {
			for _, observation := range fullSupplement(operation) {
				r.Observe(ctx, observation)
			}
		}
		read := map[string]string{}
		for _, family := range []string{"bkmonitor_alarmd_execute_return_total", "bkmonitor_alarmd_slot_operation_duration_seconds",
			"bkmonitor_alarmd_state_retained_share_approaching_slots_total"} {
			read[family] = seriesReading(gatherFamily(t, r, family))
		}
		kinds := []string{}
		for _, metric := range gatherFamily(t, r, "bkmonitor_alarmd_last_progress_timestamp_seconds") {
			for _, label := range metric.GetLabel() {
				kinds = append(kinds, label.GetValue())
			}
		}
		sort.Strings(kinds)
		read["last_progress kinds"] = strings.Join(kinds, ",")
		return read
	}
	rounds := readings("")
	supplemented, asRounds := readings(observability.OperationSupplement), readings(observability.OperationNormal)
	for name, want := range rounds {
		if supplemented[name] != want {
			t.Errorf("%s: a supplement changed it to %s, want %s", name, supplemented[name], want)
		}
		if asRounds[name] == want {
			t.Errorf("%s: the same observations as a round's left it at %s: the fixture does not reach it", name, want)
		}
	}
}

// seriesReading is a family's series as their labels and what they counted,
// without the moment each was created.
func seriesReading(metrics []*dto.Metric) string {
	lines := []string{}
	for _, metric := range metrics {
		line := ""
		for _, label := range metric.GetLabel() {
			line += label.GetName() + "=" + label.GetValue() + " "
		}
		switch {
		case metric.GetCounter() != nil:
			line += fmt.Sprint(metric.GetCounter().GetValue())
		case metric.GetHistogram() != nil:
			line += fmt.Sprint(metric.GetHistogram().GetSampleCount(), " ", metric.GetHistogram().GetSampleSum())
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	return strings.Join(lines, "; ")
}
