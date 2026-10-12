// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package readhold

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Asking which hold a later Slot would be frozen with is a question, not a
// freeze: an expired range asks it of its last Slot only to compare. The
// answer is the hold that Slot would get, and the record is left as it was,
// so the Slots before it are still written - the record says, Slot by
// Slot, the hold each was frozen with.
func TestAskingALaterSlotsHoldLeavesTheRecordAsItWas(t *testing.T) {
	c, store, _ := controllerFixture(t)
	prepare(t, c, groupSpec("qg", 0))
	ctx := context.Background()
	schedule := scheduleFor(t, "qg", 60, nil)
	if hold, err := c.SlotReadHold(ctx, schedule, 120, holdFence("qg")); err != nil || hold != 0 {
		t.Fatalf("first Slot's hold %v, %v", hold, err)
	}
	observeEarly(t, c, "qg", 180*time.Second, 0)
	before, _ := c.Reading("qg")
	writes := len(store.requests)

	asked, err := c.PeekSlotReadHold(schedule, 600)
	if err != nil || asked != 150*time.Second {
		t.Fatalf("the hold Slot 600 would be frozen with = %v, %v; want the raised 150 s", asked, err)
	}
	if after, _ := c.Reading("qg"); !reflect.DeepEqual(after, before) || len(store.requests) != writes {
		t.Fatalf("asking changed the record %+v -> %+v, or wrote it (%d -> %d writes)", before, after, writes, len(store.requests))
	}
	frozen, err := c.SlotReadHold(ctx, schedule, 180, holdFence("qg"))
	if err != nil || frozen != asked {
		t.Fatalf("Slot 180 after the question froze %v, %v; want %v and a record that takes it", frozen, err, asked)
	}
	if record, _ := c.Reading("qg"); record.SinceSlot != 180 {
		t.Fatalf("the record says the hold is in force since %d, want 180", record.SinceSlot)
	}
}

// The question keeps a transition the Slot it names would prune: a group
// whose Plan moved from one that held keeps the old group's deadline until a
// Slot past it is frozen. Asked about a Slot past the deadline - which would
// be frozen at zero - the next Slot before it still waits for the deadline:
// 55 s, as it does unasked.
func TestAskingALaterSlotsHoldKeepsATransition(t *testing.T) {
	c, _, _ := controllerFixture(t)
	prepare(t, c, groupSpec("old", 0))
	observeEarly(t, c, "old", 180*time.Second, 0)
	ctx := context.Background()
	boundary := execution.EvaluationTime(1200)
	newSpec := groupSpec("new", 120*time.Second)
	newSpec.Previous = []Previous{linked(newSpec, "old", boundary, 0)}
	prepare(t, c, newSpec)
	newSchedule := scheduleFor(t, "new", boundary, nil)
	if err := c.CloseSchedule(ctx, scheduleFor(t, "old", 60, &boundary), holdFence("old")); err != nil {
		t.Fatal(err)
	}
	if got, err := c.SlotReadHold(ctx, newSchedule, boundary, holdFence("new")); err != nil || got != 115*time.Second {
		t.Fatalf("the first Slot after the move froze %v, %v; want 115 s", got, err)
	}
	if asked, err := c.PeekSlotReadHold(newSchedule, 1500); err != nil || asked != 0 {
		t.Fatalf("Slot 1500 past the deadline would be frozen with %v, %v; want 0", asked, err)
	}
	if got, err := c.SlotReadHold(ctx, newSchedule, 1260, holdFence("new")); err != nil || got != 55*time.Second {
		t.Fatalf("Slot 1260 after the question froze %v, %v; want 55 s, the old group's deadline", got, err)
	}
}

// A group not seeded yet is not asked: seeding counts what it finds, and a
// question must count nothing. The asker falls back to freezing the Slot
// the ordinary way.
func TestAnUnseededGroupIsNotAsked(t *testing.T) {
	c, _, _ := controllerFixture(t)
	prepare(t, c, groupSpec("qg", 0))
	if _, err := c.PeekSlotReadHold(scheduleFor(t, "qg", 60, nil), 120); err == nil {
		t.Fatal("an unseeded group answered the question")
	}
}

// The question and the freeze are one computation: asked first and frozen
// next, every Slot gets the same hold - through a raise, across a moved
// Plan's transition and after it, and at the limit.
func TestTheQuestionAndTheFreezeGiveTheSameHold(t *testing.T) {
	c, _, _ := controllerFixture(t)
	prepare(t, c, groupSpec("old", 0))
	ctx := context.Background()
	oldSchedule := scheduleFor(t, "old", 60, nil)
	if _, err := c.SlotReadHold(ctx, oldSchedule, 120, holdFence("old")); err != nil {
		t.Fatal(err)
	}
	observeEarly(t, c, "old", 180*time.Second, 0)
	same := func(schedule execution.FrozenQueryGroupSchedule, qg execution.QueryGroupIdentity, slot execution.EvaluationTime) time.Duration {
		t.Helper()
		asked, askErr := c.PeekSlotReadHold(schedule, slot)
		frozen, freezeErr := c.SlotReadHold(ctx, schedule, slot, holdFence(qg))
		if askErr != nil || freezeErr != nil || asked != frozen {
			t.Fatalf("Slot %d: asked %v (%v), frozen %v (%v)", slot, asked, askErr, frozen, freezeErr)
		}
		return frozen
	}
	if got := same(oldSchedule, "old", 180); got != 150*time.Second {
		t.Fatalf("after the raise: %v, want 150 s", got)
	}
	boundary := execution.EvaluationTime(1200)
	newSpec := groupSpec("new", 120*time.Second)
	newSpec.Previous = []Previous{linked(newSpec, "old", boundary, 0)}
	prepare(t, c, newSpec)
	if err := c.CloseSchedule(ctx, scheduleFor(t, "old", 60, &boundary), holdFence("old")); err != nil {
		t.Fatal(err)
	}
	newSchedule := scheduleFor(t, "new", boundary, nil)
	if _, err := c.SlotReadHold(ctx, newSchedule, boundary, holdFence("new")); err != nil {
		t.Fatal(err)
	}
	if got := same(newSchedule, "new", 1260); got != 55*time.Second {
		t.Fatalf("in the transition: %v, want 55 s", got)
	}
	if got := same(newSchedule, "new", 1320); got != 0 {
		t.Fatalf("after the transition: %v, want 0", got)
	}
	// A moved Plan's transition held past the group's limit is cut to it on
	// every transition Slot, and the cut is counted - by the freeze, not the
	// question.
	narrow := groupSpec("narrow", 120*time.Second)
	narrow.Plans = []PlanRef{{Key: execution.PlanKey{PlanIdentity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "business", StrategyID: "narrow"}}}}
	narrow.HoldLimit = 50 * time.Second
	narrow.Previous = []Previous{linked(narrow, "old", boundary, 1140)}
	prepare(t, c, narrow)
	narrowSchedule := scheduleFor(t, "narrow", boundary, nil)
	if _, err := c.SlotReadHold(ctx, narrowSchedule, boundary, holdFence("narrow")); err != nil {
		t.Fatal(err)
	}
	clamps := func() uint64 {
		total := uint64(0)
		for _, count := range c.Stats().Clamped {
			total += count
		}
		return total
	}
	before := clamps()
	if asked, err := c.PeekSlotReadHold(narrowSchedule, 1260); err != nil || asked != 50*time.Second || clamps() != before {
		t.Fatalf("asked %v (%v) with %d cuts counted; want the 50 s limit and none counted", asked, err, clamps()-before)
	}
	if frozen, err := c.SlotReadHold(ctx, narrowSchedule, 1260, holdFence("narrow")); err != nil || frozen != 50*time.Second || clamps() != before+1 {
		t.Fatalf("froze %v (%v) with %d cuts counted; want the limit and one", frozen, err, clamps()-before)
	}
}
