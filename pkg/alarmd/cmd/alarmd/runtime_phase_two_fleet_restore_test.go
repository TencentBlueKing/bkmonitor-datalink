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
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestFleetRestoreRetriesWithinBudgetAndReacquires(t *testing.T) {
	at := time.Now()
	owned := []execution.QueryGroupIdentity{"a", "b"}
	reads := map[execution.QueryGroupIdentity]int{}
	publisher := fleetPublisher{
		tracker:       fleet.NewTracker(nil, "pod", func() time.Time { return at }),
		owned:         func() []execution.QueryGroupIdentity { return owned },
		now:           func() time.Time { return at },
		restoreBudget: 1, staleAfter: time.Minute,
		restore: func(_ context.Context, qg execution.QueryGroupIdentity) (fleet.RestoredState, error) {
			reads[qg]++
			if qg == "a" && reads[qg] == 1 {
				return fleet.RestoredState{}, errors.New("temporary read failure")
			}
			return fleet.RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at}, nil
		},
	}
	publisher.tracker.Observe(context.Background(), observability.Observation{
		RunOutcome: "source_not_due", Trace: observability.TraceFields{QueryGroupKey: "a"},
	})
	for i, want := range []int{0, 1, 2} {
		snapshot := publisher.snapshot(context.Background())
		if snapshot.Determined != want || reads["a"]+reads["b"] != i+1 {
			t.Fatalf("publish %d: determined=%d reads=%v", i, snapshot.Determined, reads)
		}
	}
	owned = []execution.QueryGroupIdentity{"b"}
	publisher.snapshot(context.Background())
	if _, kept := publisher.restoreAttempts["a"]; kept || publisher.tracker.HasConclusion("a") {
		t.Fatal("lost ownership retained restore state")
	}
	owned = []execution.QueryGroupIdentity{"a", "b"}
	if got := publisher.snapshot(context.Background()).Determined; got != 2 || reads["a"] != 3 {
		t.Fatalf("reacquisition did not restore: determined=%d reads=%v", got, reads)
	}
}

func TestFleetRestoreStopsFailedReadsAndAdvances(t *testing.T) {
	at := time.Now()
	reads := map[execution.QueryGroupIdentity]int{}
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "pod", func() time.Time { return at }),
		owned: func() []execution.QueryGroupIdentity {
			return []execution.QueryGroupIdentity{"broken", "missing", "stale", "healthy"}
		},
		now:           func() time.Time { return at },
		restoreBudget: 1, staleAfter: time.Minute,
		restore: func(_ context.Context, qg execution.QueryGroupIdentity) (fleet.RestoredState, error) {
			reads[qg]++
			switch qg {
			case "broken":
				return fleet.RestoredState{}, errors.New("read failure")
			case "missing":
				return fleet.RestoredState{}, nil
			case "stale":
				return fleet.RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at.Add(-time.Hour)}, nil
			default:
				return fleet.RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at}, nil
			}
		},
	}
	for i := 0; i < 10; i++ {
		publisher.snapshot(context.Background())
	}
	if reads["broken"] != fleetRestoreMaxAttempts || reads["missing"] != 1 || reads["stale"] != 1 || reads["healthy"] != 1 {
		t.Fatalf("unbounded retries or starved objects: %v", reads)
	}
	if publisher.tracker.Determined() != 1 {
		t.Fatal("missing, stale or unreadable history became determined")
	}
}

func TestFleetRestoreDoesNotOverwriteConclusionDuringRead(t *testing.T) {
	at := time.Now()
	tracker := fleet.NewTracker(nil, "pod", func() time.Time { return at })
	publisher := fleetPublisher{
		tracker: tracker, restoreBudget: 1, staleAfter: time.Minute,
		restore: func(context.Context, execution.QueryGroupIdentity) (fleet.RestoredState, error) {
			for i := 0; i < fleet.DefaultBlockedRounds; i++ {
				tracker.Observe(context.Background(), observability.Observation{
					RunOutcome: "source_error", Trace: observability.TraceFields{QueryGroupKey: "qg"},
				})
			}
			return fleet.RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at}, nil
		},
	}
	publisher.restoreOwned(context.Background(), []execution.QueryGroupIdentity{"qg"}, at)
	if got := tracker.Anomalies(); len(got) != 1 || got[0].Kind != fleet.KindBlockedRun {
		t.Fatalf("history overwrote a newer failure: %+v", got)
	}
}

func TestFleetRestoreFiltersRetiredRunnerDuringRead(t *testing.T) {
	at := time.Now()
	tracker := fleet.NewTracker(nil, "pod", func() time.Time { return at })
	publisher := fleetPublisher{
		tracker: tracker, restoreBudget: 1, staleAfter: time.Minute,
		owned: func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"current"} },
		now:   func() time.Time { return at },
		restore: func(context.Context, execution.QueryGroupIdentity) (fleet.RestoredState, error) {
			for i := 0; i < fleet.DefaultBlockedRounds; i++ {
				tracker.Observe(context.Background(), observability.Observation{
					RunOutcome: "source_error", Trace: observability.TraceFields{QueryGroupKey: "retired"},
				})
			}
			return fleet.RestoredState{LastCompletion: "FULL_COMPLETED", NextSlot: at}, nil
		},
	}
	snapshot := publisher.snapshot(context.Background())
	if snapshot.Determined != 1 || len(snapshot.Anomalies) != 0 || tracker.HasConclusion("retired") {
		t.Fatalf("retired runner contaminated snapshot: %+v", snapshot)
	}
}
