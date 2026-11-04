// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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

func sourceHeldAt(queryGroup, strategy string, fact observability.SourceLivenessHeldFact) observability.Observation {
	return observability.Observation{Component: observability.ComponentState, Stage: observability.StageSourceLiveness,
		Result: observability.ResultSuccess, SourceLivenessHeld: &fact,
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: strategy, BusinessID: "2"}}
}

// A source_liveness account with a Level past its bound is a
// SOURCE_UNKNOWN_HELD row on the UNDETERMINED line, naming the source kind,
// the table, when it was first held and the last outcome; one within its
// bound is not; an account with none held clears it, and so does an hour
// without one.
func TestALevelHeldPastItsBoundIsOnTheUndeterminedLine(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	first := now.Add(-2 * time.Hour)
	held := observability.SourceLivenessHeldFact{Levels: 2, SourceKind: "log", TableID: "bklog_index_set_7",
		FirstHeldUnix: first.Unix(), LastResult: "unknown", LastReason: "empty_without_status", BoundSeconds: 3600}
	tracker.Observe(context.Background(), sourceHeldAt("qg-held", "4201", held))
	for _, row := range tracker.NoData() {
		if row.Kind == KindSourceUnknownHeld {
			t.Fatalf("within the bound: %+v, want no row", row)
		}
	}
	held.PastBound = 1
	tracker.Observe(context.Background(), sourceHeldAt("qg-held", "4201", held))
	rows := rowsOfKind(tracker.NoData(), KindSourceUnknownHeld)
	row, listed := rows["qg-held"]
	if !listed || len(rows) != 1 {
		t.Fatalf("past the bound: rows %+v, want qg-held's", rows)
	}
	list := []Anomaly{row}
	Attribute(list, now)
	got := list[0]
	if got.Finding.Check != CheckSourceUnknownHeld || got.Finding.Owner != OwnerUndetermined || got.SourceUnknownHeld == nil ||
		got.SourceUnknownHeld.TableID != "bklog_index_set_7" || got.SourceUnknownHeld.SourceKind != "log" ||
		!got.SourceUnknownHeld.FirstHeld.Equal(first) || got.SourceUnknownHeld.LastReason != "empty_without_status" ||
		!got.Since.Equal(first) || len(got.Strategies) != 1 || got.Strategies[0].StrategyID != "4201" {
		t.Fatalf("the row = %+v, want UNDETERMINED naming the log table, first held two hours ago, last empty", got)
	}
	tracker.Observe(context.Background(), sourceHeldAt("qg-held", "4201", observability.SourceLivenessHeldFact{}))
	if rows := rowsOfKind(tracker.NoData(), KindSourceUnknownHeld); len(rows) != 0 {
		t.Fatalf("after none held: %+v, want the row cleared", rows)
	}
	tracker.Observe(context.Background(), sourceHeldAt("qg-held", "4201", held))
	at.at = at.at.Add(sourceHeldStale + time.Minute)
	if rows := rowsOfKind(tracker.NoData(), KindSourceUnknownHeld); len(rows) != 0 {
		t.Fatalf("an hour without an account: %+v, want the row gone", rows)
	}
}
