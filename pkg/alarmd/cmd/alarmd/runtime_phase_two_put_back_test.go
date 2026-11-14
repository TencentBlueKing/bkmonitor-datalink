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
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// A strategy deleted and put back unchanged while its Query Group's retired
// timeline is kept reopens that timeline, and every Plan of a reopened group
// warms up: the Slots of the hole wrote nothing, and its State, kept under
// the same identity and generation, is from before the hole. The reopened
// Segment's first Slot is spent on the warm-up marker and writes no event.
// It is not one the Worker judges by what it finds - the Plan is not new to
// the activation - so it warms up the same with its State gone. Its word is
// what the Slot finds of it: with its State kept, the Plan is back
// (PLAN_REACTIVATED); with its State gone and no marker - a Plan of the first
// activation never wrote one - nothing says it ran (CONFIG_DRIFT).
func TestAStrategyPutBackIntoItsRetiredGroupWarmsUp(t *testing.T) {
	for _, test := range []struct {
		name      string
		stateGone bool
		word      string
	}{
		{name: "its State kept", word: "COMPLETED_WITH_PARTIAL_GAP/PLAN_REACTIVATED"},
		{name: "its State gone", stateGone: true, word: "COMPLETED_WITH_PARTIAL_GAP/CONFIG_DRIFT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := startCutoverFixture(t, nil)
			ctx := context.Background()
			group := fixture.queryGroup
			document, err := fixture.redisClient.Get(ctx, "alarm-config.strategy_1001").Bytes()
			if err != nil {
				t.Fatal(err)
			}
			owns := func() bool {
				fixture.bundle.mu.RLock()
				defer fixture.bundle.mu.RUnlock()
				_, owned := fixture.bundle.runners[group]
				return owned
			}

			// Deleted: the group retires with nothing left to run, and is
			// released.
			fixture.clock.Store((fixture.base + 59) * 1000)
			installSharedGroup(t, ctx, fixture.redisClient, map[int64][]byte{})
			testwait.Until(t, "the deleted strategy's group is released", func() bool {
				if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
					t.Fatal(err)
				}
				return !owns()
			})
			if test.stateGone {
				keys, err := fixture.redisClient.Keys(ctx, fixture.cfg.Redis.StatePrefix+":runtime3:*").Result()
				if err != nil {
					t.Fatal(err)
				}
				var deleted []string
				for _, key := range keys {
					if strings.Contains(key, ":1001:") {
						deleted = append(deleted, key)
					}
				}
				if len(deleted) == 0 {
					t.Fatalf("fixture: no State of strategy 1001 among %v", keys)
				}
				if err := fixture.redisClient.Del(ctx, deleted...).Err(); err != nil {
					t.Fatal(err)
				}
			}

			// Put back unchanged a minute later: the same query, the same
			// group, whose retired timeline is reopened.
			boundary := fixture.base + 119
			fixture.clock.Store(boundary * 1000)
			installSharedGroup(t, ctx, fixture.redisClient, map[int64][]byte{1001: document})
			testwait.Until(t, "the put back strategy's group is opened again", func() bool {
				if err := fixture.bundle.refreshAndReconcile(ctx, true); err != nil {
					t.Fatal(err)
				}
				return owns()
			})
			activation, err := fixture.repository.LoadActivation(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range activation.Plans {
				if record.Fact.Plan.StrategyID == "1001" && (!record.Fact.Selected.ForceWarming || record.Fact.Selected.Arrived || record.Fact.Selected.Carry != nil) {
					t.Fatalf("the put back Plan's activation = %+v, want it forced to warm up, carrying nothing, not new", record.Fact.Selected)
				}
			}

			fixture.runner = settledRunner(fixture.bundle, group)
			first := execution.EvaluationTime(boundary + 1)
			fixture.clock.Store(firstRoundAtMillis(fixture.cfg, group, int64(first), 60))
			for attempt := 0; attempt < 20; attempt++ {
				if _, ended := fixture.slotEndings()[first]; ended {
					break
				}
				if nextAt := fixture.runner.NextReadyAt(); nextAt.After(fixture.now()) {
					fixture.clock.Store(nextAt.UnixMilli() + 1)
				}
				if _, attempted, err := fixture.runner.RunOne(ctx); err != nil {
					t.Fatal(err)
				} else if !attempted {
					fixture.clock.Store(fixture.now().UnixMilli() + 1000)
				}
			}
			if ended := fixture.slotEndings()[first]; ended != test.word {
				t.Fatalf("the reopened Segment's first Slot ended %q, want %q; endings %v", ended, test.word, fixture.slotEndings())
			}
			if events := fixture.eventsAt(first); len(events) != 0 {
				t.Fatalf("the warm-up Slot wrote events: %+v", events)
			}
		})
	}
}
