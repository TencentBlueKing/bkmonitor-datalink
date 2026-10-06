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
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func controlSourceRounds(t *testing.T, r *Recorder) map[[2]string]float64 {
	t.Helper()
	got := map[[2]string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_control_source_refresh_total") {
		labels := map[string]string{}
		for _, label := range m.Label {
			labels[label.GetName()] = label.GetValue()
		}
		got[[2]string{labels["outcome"], labels["exit"]}] = m.GetCounter().GetValue()
	}
	return got
}

// Every round counts, whichever way it ended, and a failed round counts
// under the exit it named. The series exist at zero from the start: the
// deployment this was written for read a flat nothing for three releases,
// and nothing is what an absent series and a zero one both look like.
func TestControlSourceRoundsCountEveryRoundUnderItsExit(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	initial := controlSourceRounds(t, r)
	if len(initial) != len(controlplane.SourceRefreshExits) {
		t.Fatalf("series before any round = %d, want one per exit (%d)", len(initial), len(controlplane.SourceRefreshExits))
	}
	for pair, value := range initial {
		if value != 0 {
			t.Fatalf("%v before any round = %v, want 0", pair, value)
		}
	}
	if initial[[2]string{"succeeded", "none"}] != 0 || initial[[2]string{"failed", "active_set_invalid_id"}] != 0 {
		t.Fatalf("the succeeded/none and failed/active_set_invalid_id pairs are not pre-created: %v", initial)
	}
	round := func(outcome, exit string) {
		r.Observe(context.Background(), observability.Observation{
			Component: observability.ComponentControlPlane, Stage: observability.StageSnapshotRefreshed,
			Result:             observability.ResultFailed,
			Err:                errors.New("boom"),
			ControlSourceRound: &observability.ControlSourceRoundFacts{Outcome: outcome, Exit: exit},
		})
	}
	round(observability.ControlSourceRoundFailed, string(controlplane.SourceRefreshExitActiveSetInvalidID))
	round(observability.ControlSourceRoundFailed, string(controlplane.SourceRefreshExitActiveSetInvalidID))
	round(observability.ControlSourceRoundFailed, "an_exit_nobody_declared")
	round(observability.ControlSourceRoundSucceeded, "")
	got := controlSourceRounds(t, r)
	if got[[2]string{"failed", "active_set_invalid_id"}] != 2 || got[[2]string{"failed", "other"}] != 1 ||
		got[[2]string{"succeeded", "none"}] != 1 || len(got) != len(controlplane.SourceRefreshExits) {
		t.Fatalf("rounds = %v", got)
	}
	// A round fact outside the control plane's refresh stage is not a round.
	r.Observe(context.Background(), observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
		ControlSourceRound: &observability.ControlSourceRoundFacts{Outcome: observability.ControlSourceRoundFailed, Exit: "documents"},
	})
	if after := controlSourceRounds(t, r); after[[2]string{"failed", "documents"}] != 0 {
		t.Fatalf("a round fact on another stage was counted: %v", after)
	}
}

// The mode is read at scrape time from the process's state, 1 on exactly
// one role and mode pair. The age of the last success is a series only once
// a success is known, and it is measured from the persisted time, not from
// anything the process remembers about itself.
func TestControlSourceCollectorReportsStateAtScrapeTime(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	stats := ControlSourceStats{}
	r.SetControlSourceSource(func() ControlSourceStats { return stats })
	now := time.Unix(1_700_000_600, 0)
	r.phaseTwo.controlSource.now = func() time.Time { return now }

	if modes := gatherFamily(t, r, "bkmonitor_alarmd_control_source_mode"); len(modes) != 0 {
		t.Fatalf("mode series before the first control round = %v, want none", modes)
	}
	stats = ControlSourceStats{Known: true, Role: observability.ControlSourceRoleLeader, Mode: observability.ControlSourceModeNeverSucceeded}
	modes := map[[2]string]float64{}
	for _, m := range gatherFamily(t, r, "bkmonitor_alarmd_control_source_mode") {
		labels := map[string]string{}
		for _, label := range m.Label {
			labels[label.GetName()] = label.GetValue()
		}
		modes[[2]string{labels["role"], labels["mode"]}] = m.GetGauge().GetValue()
	}
	want := len(observability.ControlSourceRoles) * len(observability.ControlSourceModes)
	ones := 0
	for _, value := range modes {
		if value == 1 {
			ones++
		}
	}
	if len(modes) != want || ones != 1 || modes[[2]string{"leader", "never_succeeded"}] != 1 {
		t.Fatalf("modes = %v, want %d series with 1 on leader/never_succeeded only", modes, want)
	}
	if age := gatherFamily(t, r, "bkmonitor_alarmd_control_source_last_success_age_seconds"); len(age) != 0 {
		t.Fatalf("age series with no known success = %v, want none", age)
	}
	stats = ControlSourceStats{Known: true, Role: observability.ControlSourceRoleFollower,
		Mode: observability.ControlSourceModeDegradedLastGood, LastSuccessAt: now.Add(-7 * time.Minute)}
	age := gatherFamily(t, r, "bkmonitor_alarmd_control_source_last_success_age_seconds")
	if len(age) != 1 || age[0].GetGauge().GetValue() != 420 {
		t.Fatalf("age = %v, want one series at 420", age)
	}
	// The same scrape a minute later reads a minute more without anything
	// having happened: the age is a difference at read time.
	now = now.Add(time.Minute)
	if age := gatherFamily(t, r, "bkmonitor_alarmd_control_source_last_success_age_seconds"); age[0].GetGauge().GetValue() != 480 {
		t.Fatalf("age a minute later = %v, want 480", age[0].GetGauge().GetValue())
	}
}

// The pending age is the leader's: a follower refreshes nothing and emits no
// series, rather than a zero that reads as "nothing waiting"; the leader
// emits it, zero included.
func TestThePendingAgeIsEmittedByTheLeaderOnly(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	stats := ControlSourceStats{Known: true, Role: observability.ControlSourceRoleFollower, Mode: observability.ControlSourceModeHealthy}
	r.SetControlSourceSource(func() ControlSourceStats { return stats })
	if series := gatherFamily(t, r, "bkmonitor_alarmd_source_pending_confirmation_age_seconds"); len(series) != 0 {
		t.Fatalf("a follower emitted %v", series)
	}
	stats.Role, stats.Leading = observability.ControlSourceRoleLeader, true
	if series := gatherFamily(t, r, "bkmonitor_alarmd_source_pending_confirmation_age_seconds"); len(series) != 1 || series[0].GetGauge().GetValue() != 0 {
		t.Fatalf("the leader emitted %v, want one series at 0", series)
	}
}
