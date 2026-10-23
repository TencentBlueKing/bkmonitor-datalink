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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

// The readers show the hold the latest frozen Slot actually got - the
// record's HoldMillis at SinceSlot, the clamp already applied - beside the
// base the next Slot outside a transition uses. A Slot a Plan's transition
// (or a predecessor's bound standing in for an unread hold) froze above the
// base names that part, which falls back by itself and gets no time_delay
// suggestion; a base chosen and not yet frozen reads as the last Slot's
// hold and the next one's. Read as the pending base, a 115 s transition
// read as no hold at all.
func TestTheReadersShowTheHoldTheLatestSlotWasFrozenWith(t *testing.T) {
	h, c, at := runtimeTestHolds(t)
	zero, sixty, ninety := int64(0), int64(60000), int64(90000)
	plan := execution.PlanKey{PlanIdentity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "business", StrategyID: "strategy"}}
	records := map[execution.QueryGroupIdentity]readhold.Record{
		// A transition froze the Slot at 115 s over a base of none.
		"transition": {SinceSlot: 1000, HoldMillis: 115000, PendingHoldMillis: &zero,
			Transitions: []readhold.Transition{{Key: plan, DeadlineMillis: 1_200_000}}},
		// A predecessor nobody could read: its bound, clamped to the limit.
		"fallback": {SinceSlot: 1060, HoldMillis: 600000, PendingHoldMillis: &zero, AtLimit: true, LimitMillis: 600000,
			Transitions: []readhold.Transition{{Key: plan, DeadlineMillis: 9_000_000, Fallback: true}}},
		// A lowering chosen, not yet frozen: no transition about it.
		"lowering": {SinceSlot: 1120, HoldMillis: 120000, PendingHoldMillis: &sixty},
		// A raise chosen, not yet frozen.
		"raised": {SinceSlot: 1, HoldMillis: 0, PendingHoldMillis: &ninety},
		"plain":  {SinceSlot: 1180, HoldMillis: 120000},
	}
	groups := make([]execution.QueryGroupIdentity, 0, len(records))
	for qg, record := range records {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		c.values[qg] = raw
		session, err := ownership.OpenSession(context.Background(), &fakePhaseTwoOwnershipStore{}, qg, "worker", *at, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		h.bind(qg, session)
		groups = append(groups, qg)
	}
	h.restore(context.Background(), groups)
	want := map[string]struct {
		millis, base, transition, since int64
		says                            string
	}{
		"transition": {115000, 0, 115000, 1000, "过渡"},
		"fallback":   {600000, 0, 600000, 1060, "过渡"},
		"lowering":   {120000, 60000, 0, 1120, "下一个 Slot"},
		"raised":     {0, 90000, 0, 0, "下一个 Slot"},
		"plain":      {120000, 120000, 0, 1180, "推后 120 秒"},
	}
	facts := h.fleetFacts()
	rows := map[string]lookbackGroupReading{}
	for _, row := range h.groupPage(nil, "", 200).Groups {
		rows[string(row.QueryGroup)] = row
	}
	for qg, w := range want {
		got, listed := facts[qg]
		if !listed {
			t.Fatalf("%s: not published", qg)
		}
		if got.Millis != w.millis || got.BaseMillis != w.base || got.TransitionMillis != w.transition || got.HeldSince != w.since {
			t.Errorf("%s: published hold %d base %d transition %d since %d, want %d %d %d %d",
				qg, got.Millis, got.BaseMillis, got.TransitionMillis, got.HeldSince, w.millis, w.base, w.transition, w.since)
		}
		if !strings.Contains(got.Annotation, w.says) {
			t.Errorf("%s: annotation %q does not say %q", qg, got.Annotation, w.says)
		}
		if w.transition > 0 && got.SuggestedDelaySeconds != 0 {
			t.Errorf("%s: a transition's hold suggests a time_delay of %d s", qg, got.SuggestedDelaySeconds)
		}
		row := rows[qg]
		if row.ReadHoldMillis != w.millis || row.BaseMillis != w.base || row.TransitionMillis != w.transition || row.Annotation != got.Annotation {
			t.Errorf("%s: group page %d/%d/%d %q, want the published reading %d/%d/%d %q",
				qg, row.ReadHoldMillis, row.BaseMillis, row.TransitionMillis, row.Annotation, w.millis, w.base, w.transition, got.Annotation)
		}
	}
}
