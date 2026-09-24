// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"context"
	"testing"
	"time"
	"unsafe"
)

// Two objects: one with few, large state keys and a slow query, one with
// many small keys and a fast query. state_keys ranks the second first;
// state_bytes and query_wall rank the first first -- the two questions the
// existing dimensions could not answer, on a live object of 249 keys and
// 86 MB whose preflight took twelve seconds of a sixty-second round.
func TestStateBytesAndQueryWallRankTheObjectTheKeysAndRunWallHide(t *testing.T) {
	now := time.Unix(600, 0)
	c := NewCostSummary(CostSummaryOptions{ProcessID: "process-a", Window: time.Minute, GroupCapacity: 4, PlanCapacity: 8, MetadataBytes: 4096, TopN: 2, Now: func() time.Time { return now }})
	big := CostGroup{QueryGroupKey: "big-state", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", Members: []CostPlanIdentity{{StrategyID: "4101", BusinessID: "2"}}}
	many := CostGroup{QueryGroupKey: "many-keys", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", Members: []CostPlanIdentity{{StrategyID: "4102", BusinessID: "2"}}}
	c.Reconcile([]CostGroup{big, many}, true)
	ctx := context.Background()
	observe := func(group string, stage Stage, duration time.Duration, keys, bytes int64) {
		c.Observe(ctx, Observation{Stage: stage, Result: ResultSuccess, Duration: duration,
			Trace:  TraceFields{QueryGroupKey: group, EvaluationTime: 590},
			Counts: Counts{Keys: keys, StateBytes: bytes}})
	}
	// The big one: 249 keys, 86 MB across preflight and apply, a 12 s query.
	observe("big-state", StageStatePreflight, 12*time.Second, 249, 85_855_443)
	observe("big-state", StageStateApplied, 2*time.Second, 249, 85_855_443)
	observe("big-state", StageQueryCompleted, 12*time.Second, 0, 0)
	// The many one: 2264 keys, 9 MB, a quick query; its preflight reports
	// keys and no bytes, which is unknown bytes and not zero bytes.
	observe("many-keys", StageStatePreflight, 300*time.Millisecond, 2264, 0)
	observe("many-keys", StageStateApplied, 7*time.Second, 2264, 9_364_560)
	observe("many-keys", StageQueryCompleted, 200*time.Millisecond, 0, 0)
	c.Publish(now)
	s := c.Snapshot()

	rows := map[string]CostContributor{}
	for _, row := range s.Contributors {
		if row.Scope == "query_group" {
			rows[row.Group.QueryGroupKey] = row
		}
	}
	if rows["big-state"].Current.StateBytes != 2*85_855_443 || rows["big-state"].Current.StateBytesUnknown != 0 {
		t.Fatalf("big-state bytes = %+v, want both calls' bytes summed", rows["big-state"].Current)
	}
	if rows["many-keys"].Current.StateBytes != 9_364_560 || rows["many-keys"].Current.StateBytesUnknown != 1 || rows["many-keys"].Current.StateKeys != 2*2264 {
		t.Fatalf("many-keys = %+v, want the apply's bytes, one call of unknown bytes, and the keys", rows["many-keys"].Current)
	}
	first := func(dimension string) string {
		t.Helper()
		for _, ranking := range s.Rankings {
			if ranking.Scope == "query_group" && ranking.Dimension == dimension {
				if len(ranking.Indexes) == 0 {
					t.Fatalf("%s ranking is empty", dimension)
				}
				return s.Contributors[ranking.Indexes[0]].Group.QueryGroupKey
			}
		}
		t.Fatalf("no %s ranking in %+v", dimension, s.Rankings)
		return ""
	}
	if got := first("state_keys"); got != "many-keys" {
		t.Fatalf("state_keys ranks %q first, want many-keys: keys alone read the big object as small", got)
	}
	if got := first("state_bytes"); got != "big-state" {
		t.Fatalf("state_bytes ranks %q first, want big-state", got)
	}
	if got := first("query_wall"); got != "big-state" {
		t.Fatalf("query_wall ranks %q first, want big-state", got)
	}
	// Every dimension the closed list names has a ranking in both scopes,
	// and nothing outside it does.
	seen := map[string]int{}
	for _, ranking := range s.Rankings {
		seen[ranking.Dimension]++
	}
	for _, dimension := range CostDimensions() {
		if seen[dimension] != 2 {
			t.Fatalf("dimension %s has %d rankings, want one per scope", dimension, seen[dimension])
		}
	}
	if len(seen) != len(CostDimensions()) {
		t.Fatalf("rankings name %d dimensions, the list %d: %v", len(seen), len(CostDimensions()), seen)
	}
	// The capacity estimate follows the dimension count: the ranking rows
	// are two scopes times the dimensions times TopN.
	sized := func(topN int) int64 {
		return CostSummaryCapacityBytes(CostSummaryOptions{ProcessID: "p", Window: time.Minute, GroupCapacity: 100, PlanCapacity: 100, MetadataBytes: 1, TopN: topN})
	}
	// Per extra TopN row: two scopes times the dimensions, each a contributor
	// row (three times over) and an index.
	perTopN := int64(2*len(CostDimensions())) * (3*int64(sizeOfCostContributor()) + 3*8)
	if got := sized(3) - sized(2); got != perTopN {
		t.Fatalf("capacity grows %d bytes per TopN row, want %d (two scopes x %d dimensions)", got, perTopN, len(CostDimensions()))
	}
}

func sizeOfCostContributor() uintptr { return unsafe.Sizeof(CostContributor{}) }
