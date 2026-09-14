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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The index reports how much longer its bound would have held the object back,
// and the number is the distance to the bound rather than a constant.
//
// A prediction of not-due carrying a zero here would put every violation in the
// smallest bucket, which reads as "the boundary was crossed while the round was
// in flight" -- the benign one of the two explanations this measurement exists
// to separate. An instrument whose failure mode is the reassuring answer is the
// shape this page has spent the day removing.
func TestThePredictionSaysHowMuchLongerItWouldHaveHeldTheObjectBack(t *testing.T) {
	index := newPhaseTwoDueIndex(metric.NewRecorder(metric.BuildInfo{}))
	lifecycle := &phaseTwoQueryGroupLifecycle{runner: walkRunner{}}
	at := time.Unix(1_000_000, 0)

	// A bound forty seconds out.
	index.Record("query-group-a", lifecycle, 0,
		scheduler.RunnerDueBound{NotDueUntilUnix: at.Add(40 * time.Second).Unix()}, at)

	due, _, _, heldFor := index.Predict("query-group-a", lifecycle, at)
	if due {
		t.Fatal("the index called an object due forty seconds before its bound, so the branch under " +
			"test did not run")
	}
	if heldFor != 40*time.Second {
		t.Fatalf("held for %v, want 40s -- the distance to the bound is the only thing that tells a "+
			"late bound from a boundary crossed in flight, and a constant tells neither", heldFor)
	}

	// And nothing is held back once the bound has passed.
	if due, _, _, heldFor := index.Predict("query-group-a", lifecycle, at.Add(time.Minute)); !due || heldFor != 0 {
		t.Fatalf("after the bound passed: due=%v heldFor=%v, want due with nothing held back", due, heldFor)
	}
}

// The overshoot is observed for the violation and for nothing else.
//
// Every round that was predicted due and found due is the index working. Those
// outnumber the violations by orders of magnitude, so observing them here would
// bury the distribution this exists to read under its own success -- and the
// buckets would then describe the healthy population, which is the reading a
// person would act on.
func TestTheOvershootIsObservedOnlyWhenTheIndexWasWrong(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{})
	dispatcher := walkDispatcher(8, 8, map[execution.QueryGroupIdentity]time.Time{"query-group-a": {}})
	dispatcher.bundle.dependencies.Recorder = recorder
	// The round has to reach a verdict on dueness. A round that lost a race or
	// failed its ownership check says nothing about the schedule, and the
	// comparison is deliberately skipped for those -- so a runner reporting the
	// zero bound would make this test pass by never reaching the branch.
	dispatcher.bundle.mu.Lock()
	dispatcher.bundle.setRunnerLocked("query-group-a", &phaseTwoQueryGroupLifecycle{
		runner: walkRunner{bound: scheduler.RunnerDueBound{Verdict: scheduler.DueVerdictDue}},
	})
	dispatcher.bundle.mu.Unlock()

	observed := func() uint64 {
		families, err := recorder.Gatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, family := range families {
			if family.GetName() != "bkmonitor_alarmd_due_index_audit_overshoot_seconds" {
				continue
			}
			for _, series := range family.Metric {
				return series.GetHistogram().GetSampleCount()
			}
		}
		return 0
	}

	runners, _ := dispatcher.bundle.snapshotScheduledRunners()
	if len(runners) != 1 {
		t.Fatalf("owned runners = %d, want the one this test drives", len(runners))
	}
	scheduled := runners[0]
	scheduled.predictedHeldFor = 7 * time.Second

	// Predicted due and found due: the index was right, and nothing is observed.
	scheduled.predictedDue = true
	dispatcher.recordDueBound(phaseTwoScheduledResult{scheduled: scheduled, ran: true})
	if got := observed(); got != 0 {
		t.Fatalf("observed %d samples for a round the index called correctly; the correct rounds "+
			"outnumber the violations and would bury the distribution", got)
	}

	// Predicted not due and found due: the violation.
	scheduled.predictedDue = false
	dispatcher.recordDueBound(phaseTwoScheduledResult{scheduled: scheduled, ran: true})
	if got := observed(); got != 1 {
		t.Fatalf("observed %d samples for the one violation, want 1", got)
	}
}
