// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// awayAndBack takes strategy away out of the shared Query Group at the second
// given and puts it back, as it was, at the next, the others staying.
func (fixture *cutoverStallFixture) awayAndBack(ctx context.Context, left execution.EvaluationTime, away int64, staying ...int64) {
	fixture.t.Helper()
	installSharedGroup(fixture.t, ctx, fixture.redisClient, sharedGroupOf(fixture.t, staying...))
	fixture.cutOverAt(ctx, left)
	installSharedGroup(fixture.t, ctx, fixture.redisClient, sharedGroupOf(fixture.t, append(staying, away)...))
	fixture.cutOverAt(ctx, left+1)
}

// stateOf is the series state keys strategy id holds.
func (fixture *cutoverStallFixture) stateOf(ctx context.Context, id string) []string {
	fixture.t.Helper()
	keys, err := fixture.redisClient.Keys(ctx, fixture.cfg.Redis.StatePrefix+":runtime3:*").Result()
	if err != nil {
		fixture.t.Fatal(err)
	}
	var held []string
	for _, key := range keys {
		if strings.Contains(key, ":"+id+":") {
			held = append(held, key)
		}
	}
	return held
}

var reactivatedEnding = "COMPLETED_WITH_PARTIAL_GAP/" + contract.ReasonPlanReactivated

// A strategy that left its Query Group and came back, as it was, under the
// same state generation, is back: its first Slot back is spent warming it up
// and says so (PLAN_REACTIVATED), the strategies beside it writing their
// events. A strategy of the deployment's first activation was never forced
// to warm up and has no marker; the state it left is what says it ran. One
// added after the first activation has the marker of its first Slot too, and
// reads the same.
func TestAStrategyBackToTheStateItLeftIsReactivated(t *testing.T) {
	for _, test := range []struct {
		name    string
		away    string
		staying []int64
		setUp   func(fixture *cutoverStallFixture, ctx context.Context) execution.EvaluationTime
	}{
		{name: "of the first activation", away: "1003", staying: []int64{1001},
			setUp: func(fixture *cutoverStallFixture, ctx context.Context) execution.EvaluationTime {
				ran := execution.EvaluationTime(fixture.base + 60)
				if ended := fixture.runUntilEnded(ctx, ran); ended != fullEnding {
					t.Fatalf("Slot %d ended %q, want FULL", ran, ended)
				}
				fixture.awayAndBack(ctx, ran+1, 1003, 1001)
				return ran + 60
			}},
		{name: "added after it", away: "1004", staying: []int64{1001, 1003},
			setUp: func(fixture *cutoverStallFixture, ctx context.Context) execution.EvaluationTime {
				ran := execution.EvaluationTime(fixture.base + 60)
				fixture.joinedAndRan(ctx, ran)
				fixture.awayAndBack(ctx, ran+1, 1004, 1001, 1003)
				return ran + 60
			}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := startSharedGroupFixture(t)
			ctx := context.Background()
			first := test.setUp(fixture, ctx)
			if held := fixture.stateOf(ctx, test.away); len(held) == 0 {
				t.Fatalf("strategy %s holds no state: the case is not a return to it", test.away)
			}
			if ended := fixture.runUntilEnded(ctx, first); ended != reactivatedEnding {
				t.Fatalf("the first Slot back ended %q, want %q", ended, reactivatedEnding)
			}
			staying := make([]string, 0, len(test.staying))
			for _, id := range test.staying {
				staying = append(staying, strconv.FormatInt(id, 10))
			}
			fixture.wantEvents(first, staying, []string{test.away})
		})
	}
}

// A strategy of the first activation back after the state it left is gone -
// past its TTL, here deleted - has nothing of it from before and no marker:
// it is judged new at its first Slot back, which is evaluated and writes its
// events (FULL), where with its state kept that Slot warms it up.
func TestAStrategyBackAfterItsStateIsGoneIsJudgedNew(t *testing.T) {
	for _, test := range []struct {
		name   string
		forget bool
		ended  string
		events bool
	}{
		{name: "state gone", forget: true, ended: fullEnding, events: true},
		{name: "state kept", ended: reactivatedEnding},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := startSharedGroupFixture(t)
			ctx := context.Background()
			ran := execution.EvaluationTime(fixture.base + 60)
			if ended := fixture.runUntilEnded(ctx, ran); ended != fullEnding {
				t.Fatalf("Slot %d ended %q, want FULL", ran, ended)
			}
			installSharedGroup(fixture.t, ctx, fixture.redisClient, sharedGroupOf(fixture.t, 1001))
			fixture.cutOverAt(ctx, ran+1)
			if test.forget {
				held := fixture.stateOf(ctx, "1003")
				if len(held) == 0 {
					t.Fatal("strategy 1003 held no state to lose")
				}
				if err := fixture.redisClient.Del(ctx, held...).Err(); err != nil {
					t.Fatal(err)
				}
			}
			installSharedGroup(fixture.t, ctx, fixture.redisClient, sharedGroupOf(fixture.t, 1001, 1003))
			fixture.cutOverAt(ctx, ran+2)
			first := ran + 60
			if ended := fixture.runUntilEnded(ctx, first); ended != test.ended {
				t.Fatalf("the first Slot back ended %q, want %q", ended, test.ended)
			}
			if events := fixture.eventsFor(first, "1003"); (len(events) > 0) != test.events {
				t.Fatalf("strategy 1003 wrote %d events on its first Slot back, want events %t", len(events), test.events)
			}
		})
	}
}

// A strategy back whose first Slot back ends without its query - its
// snapshot gone - is not worded by the return at all: the Slot reads no
// state, so nothing of the strategy from before is read, and the Slot ends on
// the query's word (SNAPSHOT_UNAVAILABLE), with or without a marker from
// before, as it did before the state was evidence. No event is written for
// it.
func TestAStrategyBackWhoseFirstSlotEndsQueryFreeIsWordedByTheQuery(t *testing.T) {
	for _, test := range []struct {
		name  string
		away  string
		setUp func(fixture *cutoverStallFixture, ctx context.Context) execution.EvaluationTime
	}{
		{name: "of the first activation, no marker", away: "1003",
			setUp: func(fixture *cutoverStallFixture, ctx context.Context) execution.EvaluationTime {
				ran := execution.EvaluationTime(fixture.base + 60)
				if ended := fixture.runUntilEnded(ctx, ran); ended != fullEnding {
					t.Fatalf("Slot %d ended %q, want FULL", ran, ended)
				}
				fixture.awayAndBack(ctx, ran+1, 1003, 1001)
				return ran + 60
			}},
		{name: "added after it, a marker", away: "1004",
			setUp: func(fixture *cutoverStallFixture, ctx context.Context) execution.EvaluationTime {
				ran := execution.EvaluationTime(fixture.base + 60)
				fixture.joinedAndRan(ctx, ran)
				fixture.awayAndBack(ctx, ran+1, 1004, 1001, 1003)
				return ran + 60
			}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := startSharedGroupFixture(t)
			ctx := context.Background()
			first := test.setUp(fixture, ctx)
			schedule, err := fixture.production.dependencies.Catalog.ReadFrozenSchedule(ctx, fixture.queryGroup, first)
			if err != nil {
				t.Fatal(err)
			}
			expireOldContent(t, fixture, schedule)
			if err := fixture.repository.ConfigureObjectCache(1, 1); err != nil {
				t.Fatal(err)
			}
			ended := fixture.runUntilEnded(ctx, first)
			if !strings.HasSuffix(ended, "/"+contract.ReasonSnapshotUnavailable) && ended != "GAP_SKIPPED/GAP_SKIPPED" {
				t.Fatalf("the first Slot back, past its snapshot, ended %q, want it on the query's word", ended)
			}
			fixture.wantEvents(first, nil, []string{test.away})
		})
	}
}
