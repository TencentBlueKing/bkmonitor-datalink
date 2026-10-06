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
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func arrivalObservation(facts observability.SlotReadinessFacts) observability.Observation {
	return observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageSlotReadinessArrival,
		Operation: observability.OperationNormal, Result: observability.ResultSuccess,
		SlotReadiness: &facts,
	}
}

// The histogram covers only the executions whose queries agree on one readiness
// moment, so on its own it is a sample presented as the whole. The boundary
// counter is what says how large a sample: a deployment where most Slots mix
// readiness boundaries can have a perfect-looking slack distribution over a
// small minority of its work.
func TestSlackIsCountedOnlyWhereThereIsOneBoundaryToMeasureAgainst(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), arrivalObservation(observability.SlotReadinessFacts{
		Boundary: observability.ReadinessBoundaryUnified, SlackSeconds: 12, Slack: true,
	}))
	recorder.Observe(context.Background(), arrivalObservation(observability.SlotReadinessFacts{
		Boundary: observability.ReadinessBoundaryMixed,
	}))
	recorder.Observe(context.Background(), arrivalObservation(observability.SlotReadinessFacts{
		Boundary: observability.ReadinessBoundaryNone,
	}))

	readiness := recorder.phaseTwo.slotReadiness
	for boundary, want := range map[string]float64{
		observability.ReadinessBoundaryUnified: 1,
		observability.ReadinessBoundaryMixed:   1,
		observability.ReadinessBoundaryNone:    1,
	} {
		if got := testutil.ToFloat64(readiness.boundary.WithLabelValues(boundary)); got != want {
			t.Fatalf("boundary %q = %v, want %v: every execution has to be counted somewhere", boundary, got, want)
		}
	}
	// Only the unified arrival reached the histogram. Counting the other two as
	// zero seconds would report a deployment arriving exactly on time for work
	// whose readiness moment does not exist.
	if got := testutil.CollectAndCount(readiness.slack); got == 0 {
		t.Fatal("the histogram recorded nothing at all")
	}
	if got := readinessSlackCount(t, recorder); got != 1 {
		t.Fatalf("slack observations = %v, want only the one with a single boundary", got)
	}
}

// An arrival that carries a boundary value from somewhere else must not mint a
// series. The label set is closed in 07 section 9 and the counter is one of the
// places a stray value would land.
func TestAnUnknownBoundaryCollapsesToOther(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	recorder.Observe(context.Background(), arrivalObservation(observability.SlotReadinessFacts{
		Boundary: "whatever-someone-passed", SlackSeconds: 3, Slack: true,
	}))

	readiness := recorder.phaseTwo.slotReadiness
	if got := testutil.ToFloat64(readiness.boundary.WithLabelValues(observability.ReadinessBoundaryOther)); got != 1 {
		t.Fatalf("OTHER = %v, want the unrecognised boundary counted there", got)
	}
	// Its slack went with it: a value measured against a boundary nobody can
	// name is not a measurement of lateness.
	if got := readinessSlackCount(t, recorder); got != 0 {
		t.Fatalf("slack observations = %v, want none from an unnameable boundary", got)
	}
}

// Other stages carry no arrival, and an observation that somehow does must not
// be counted twice by reaching this metric through a different stage.
func TestOnlyTheArrivalStageFeedsTheReadinessMetrics(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	observation := arrivalObservation(observability.SlotReadinessFacts{
		Boundary: observability.ReadinessBoundaryUnified, SlackSeconds: 4, Slack: true,
	})
	observation.Stage = observability.StageQueryCompleted
	recorder.Observe(context.Background(), observation)

	if got := readinessSlackCount(t, recorder); got != 0 {
		t.Fatalf("slack observations = %v, want none from another stage", got)
	}
}

// readinessSlackCount reads how many observations the histogram took.
func readinessSlackCount(t *testing.T, recorder *Recorder) uint64 {
	t.Helper()
	gathered, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range gathered {
		if family.GetName() != metricNamespace+"_"+metricSubsystem+"_slot_readiness_slack_seconds" {
			continue
		}
		for _, metric := range family.GetMetric() {
			return metric.GetHistogram().GetSampleCount()
		}
	}
	return 0
}
