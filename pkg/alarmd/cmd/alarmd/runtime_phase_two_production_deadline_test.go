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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The production Query Group reports the Runner's deadline. This is the
// method the deadline order lives or dies by, and it was missing: NextDeadline
// was an optional interface the production Runtime never implemented, so every
// production Query Group was queued with no deadline, a short-period arrival
// lost to everything at a full queue as deadline_unknown, and two releases of
// deadline ordering never ordered anything. The interface now requires it, and
// this pins that the production value is the Runner's and not a zero of its own.
func TestProductionQueryGroupReportsTheRunnersDeadline(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(cfg.PhaseTwo.Scheduler.RecoveryLimits(), now)
	if err != nil {
		t.Fatal(err)
	}
	runner := newScopeTestSchedulerRunner(t, "query-group-deadline", flights, now, &countingExecutor{})
	group := &productionPhaseTwoQueryGroup{runner: runner, now: now}
	var runtime phaseTwoQueryGroupRuntime = group

	if got := runtime.NextDeadline(); !got.IsZero() {
		t.Fatalf("NextDeadline() before any round = %v, want zero: nothing is known yet", got)
	}
	// One round: the source says the Slot is due with a 60 s interval and the
	// executor completes it, so the Runner now carries a schedule-derived bound.
	if _, _, err := runtime.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne() error = %v", err)
	}
	want := runner.NextDeadline()
	if want.IsZero() {
		t.Fatal("scheduler.Runner reports no deadline after a completed round; the fixture no longer establishes a bound")
	}
	if got := runtime.NextDeadline(); !got.Equal(want) {
		t.Fatalf("production NextDeadline() = %v, want the Runner's %v", got, want)
	}
	if got := (*productionPhaseTwoQueryGroup)(nil).NextDeadline(); !got.IsZero() {
		t.Fatalf("nil receiver NextDeadline() = %v, want zero", got)
	}
}
