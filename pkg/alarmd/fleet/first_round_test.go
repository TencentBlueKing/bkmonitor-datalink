// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"strings"
	"testing"
	"time"
)

// Waiting for a first round is a narrow thing, and each bound is tested on
// both sides: the period against the restart grace, the wake against now and
// against one period ahead, and a backoff or cooldown bound.
func TestAwaitingFirstRoundOnlyForALongObjectWhoseTurnHasNotCome(t *testing.T) {
	grace := int64(RestartCatchUpGrace / time.Second)
	wake := func(interval int64, dueIn time.Duration) WakeFacts {
		return WakeFacts{Known: true, IntervalSeconds: interval, DueAt: now.Add(dueIn)}
	}
	for _, tc := range []struct {
		name string
		wake WakeFacts
		want bool
	}{
		{"a two-hour object due within its period", wake(7200, time.Hour), true},
		{"period one second past the grace", wake(grace+1, time.Minute), true},
		{"period equal to the grace", wake(grace, time.Minute), false},
		{"a minute object after a restart", wake(60, 30*time.Second), false},
		{"due exactly one period ahead", wake(7200, 2*time.Hour), true},
		{"due one second past one period", wake(7200, 2*time.Hour+time.Second), false},
		{"due now", wake(7200, 0), false},
		{"past due", wake(7200, -time.Minute), false},
		{"a backoff bound", WakeFacts{Known: true, IntervalSeconds: 7200, DueAt: now.Add(time.Hour), Deferred: true}, false},
		{"a query cooldown", WakeFacts{Known: true, IntervalSeconds: 7200, DueAt: now.Add(time.Hour), Cooling: true}, false},
		{"no entry", WakeFacts{}, false},
	} {
		if got := AwaitingFirstRound(tc.wake, now); got != tc.want {
			t.Errorf("%s: awaiting = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Undetermined objects that are only waiting for their first round are said
// as such and do not hold the verdict at UNKNOWN; any other undetermined
// object still does, and only it is counted unknown.
func TestObjectsAwaitingTheirFirstRoundDoNotMakeTheVerdictUnknown(t *testing.T) {
	waits := []FirstRoundWait{
		{QueryGroup: "a", IntervalSeconds: 7200, DueAt: now.Add(time.Hour)},
		{QueryGroup: "b", IntervalSeconds: 14400, DueAt: now.Add(2 * time.Hour)},
		{QueryGroup: "c", IntervalSeconds: 14400, DueAt: now.Add(3 * time.Hour)},
		{QueryGroup: "d", IntervalSeconds: 216000, DueAt: now.Add(50 * time.Hour)},
	}
	snapshots := healthySnapshots()
	snapshots[0].Owned, snapshots[0].Determined = 504, 500
	snapshots[0].AwaitingFirstRound, snapshots[0].AwaitingFirstRoundTotal = waits, 4
	view := Aggregate(Expectation{QueryGroups: 953, Known: true}, snapshots, replicas(), now, freshness)
	if view.Health != HealthHealthy || view.Unknown != 0 || view.AwaitingFirstRound == nil || view.AwaitingFirstRound.Objects != 4 {
		t.Fatalf("health %s unknown %d awaiting %+v gaps %+v, want HEALTHY with four awaiting", view.Health, view.Unknown, view.AwaitingFirstRound, view.Gaps)
	}
	for _, want := range []string{"4 个对象在等第一轮", "周期 2 h 的 1 个", "周期 4 h 的 2 个", "周期 60 h 的 1 个，最迟 09-11 14:00Z 到期"} {
		if !strings.Contains(view.AwaitingFirstRound.Line, want) {
			t.Errorf("line %q lacks %q", view.AwaitingFirstRound.Line, want)
		}
	}
	// One more undetermined object that is not waiting: UNKNOWN, and it alone
	// is unknown.
	snapshots[0].Owned = 505
	view = Aggregate(Expectation{QueryGroups: 954, Known: true}, snapshots, replicas(), now, freshness)
	if view.Health != HealthUnknown || view.Unknown != 1 {
		t.Fatalf("health %s unknown %d, want UNKNOWN with the one object not waiting", view.Health, view.Unknown)
	}
}
