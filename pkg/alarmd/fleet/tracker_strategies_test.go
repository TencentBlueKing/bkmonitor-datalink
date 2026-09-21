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
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// observedObject is the only way this table learns a strategy: from a round
// that reported one. An object that never produced a round has none, which is
// the case the second test pins.
func observedObject(t *testing.T, queryGroup, strategy string) *Tracker {
	t.Helper()
	at := &clock{at: time.Date(2026, 9, 11, 16, 0, 0, 0, time.UTC)}
	tracker := newTracker(t, at)
	tracker.Observe(context.Background(), completion(queryGroup, "COMPLETED", strategy))
	return tracker
}

// An overdue object produced no round, so nothing about it reaches the list
// except what this table already learned from earlier rounds. Without that the
// row is a bare hash sitting beside rows that name a strategy, and the one
// question the reader arrived with -- which of my strategies is this -- has no
// answer on the page.
func TestTheTableNamesTheStrategiesItHasSeenBehindAnObject(t *testing.T) {
	tracker := observedObject(t, "qg-1", "77")

	strategies := tracker.StrategiesFor("qg-1")

	if len(strategies) != 1 || strategies[0].StrategyID != "77" {
		t.Fatalf("strategies = %+v, want the one this table observed", strategies)
	}
}

// One strategy is one entry however many shapes the round's traces name it in.
// The query budget observation on a live deployment named the strategy without
// its business while the completion named both, and the object's list read
// "11781 (business 7), 11781 (no business)" -- two rows for one strategy, one
// of them unanswerable. The entry with the business wins in either order of
// arrival, and the bare trace after it adds nothing.
func TestOneStrategyIsOneEntryWhetherOrNotEveryTraceNamesItsBusiness(t *testing.T) {
	budget := observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageQueryBudgetResolved,
		Trace: observability.TraceFields{QueryGroupKey: "qg-1", StrategyID: "11781"},
	}
	for name, order := range map[string][]observability.Observation{
		"bare trace first, then the completion with the business": {budget, completion("qg-1", "COMPLETED", "11781")},
		"completion with the business first, then the bare trace": {completion("qg-1", "COMPLETED", "11781"), budget},
	} {
		t.Run(name, func(t *testing.T) {
			tracker := newTracker(t, &clock{at: time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)})
			for _, observation := range order {
				tracker.Observe(context.Background(), observation)
			}
			strategies := tracker.StrategiesFor("qg-1")
			if len(strategies) != 1 || strategies[0] != (StrategyRef{StrategyID: "11781", BusinessID: "2"}) {
				t.Fatalf("strategies = %+v, want exactly one entry naming strategy 11781 with its business", strategies)
			}
		})
	}
	// A strategy only ever named bare is still listed -- without its business,
	// which is what is known -- rather than dropped.
	tracker := newTracker(t, &clock{at: time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)})
	tracker.Observe(context.Background(), budget)
	if strategies := tracker.StrategiesFor("qg-1"); len(strategies) != 1 || strategies[0] != (StrategyRef{StrategyID: "11781"}) {
		t.Fatalf("strategies = %+v, want the bare entry when nothing named the business", strategies)
	}
	// Two different strategies stay two entries.
	tracker.Observe(context.Background(), completion("qg-1", "COMPLETED", "11782"))
	if strategies := tracker.StrategiesFor("qg-1"); len(strategies) != 2 {
		t.Fatalf("strategies = %+v, want two entries for two strategies", strategies)
	}
}

// Empty for an object no round has ever spoken about, which is precisely the
// object most likely to be overdue: it was parked before this replica ever
// evaluated it. Inventing a strategy for it would be worse than saying nothing.
func TestAnObjectTheTableHasNeverSeenIsNotGivenStrategies(t *testing.T) {
	tracker := observedObject(t, "qg-1", "77")

	if strategies := tracker.StrategiesFor("qg-never-seen"); len(strategies) != 0 {
		t.Fatalf("strategies = %+v, want none for an object nothing reported", strategies)
	}
}
