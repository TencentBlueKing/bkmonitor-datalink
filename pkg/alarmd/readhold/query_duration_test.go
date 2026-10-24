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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// nextOwner is a controller over the same records as one that owned the
// group before, as the process that takes it over has.
func nextOwner(t *testing.T, store *memoryControl, now *time.Time, spec GroupSpec) *Controller {
	t.Helper()
	next, err := NewController(Options{Control: store, Prefix: "schedule", MaxHold: 10 * time.Minute, Now: func() time.Time { return *now },
		Owner: func(qg execution.QueryGroupIdentity) (Owner, error) {
			return Owner{Fence: holdFence(qg), ContentScope: "view"}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	prepare(t, next, spec)
	return next
}

// A held group's measured query duration is kept in its record, rounded up
// to the second, as its next Slot is frozen; the process that takes the group
// over reads it there. A reading that moves by a second is written again a
// minute after the last write, not sooner; one that holds is written again
// half way to stale, and one older than a day is no reading.
func TestTheQueryDurationIsKeptForTheNextOwner(t *testing.T) {
	c, store, now := controllerFixture(t)
	spec := groupSpec("qg", 0)
	prepare(t, c, spec)
	observeEarly(t, c, "qg", 60*time.Second+spec.SettlingWait, 0)
	schedule := scheduleEvery(t, "qg", 60, 60)
	ctx := context.Background()
	freeze := func(c *Controller, slot execution.EvaluationTime) {
		t.Helper()
		if _, err := c.SlotReadHold(ctx, schedule, slot, holdFence("qg")); err != nil {
			t.Fatal(err)
		}
	}
	writes := func(c *Controller) map[string]uint64 { return c.Stats().QueryDurationWrites }

	c.MeasureQueryDuration("qg", 9300*time.Millisecond)
	freeze(c, 120)
	if got, ok := nextOwner(t, store, now, spec).InheritedQueryDuration("qg"); !ok || got != 10*time.Second {
		t.Fatalf("the next owner reads %v %v, want 10 s", got, ok)
	}
	if got := writes(c); got[QueryDurationWriteChanged] != 1 || got[QueryDurationWriteRefreshed] != 0 {
		t.Fatalf("writes %v, want one changed", got)
	}
	// Moved by a second, within the minute: not written.
	*now = now.Add(30 * time.Second)
	c.MeasureQueryDuration("qg", 11200*time.Millisecond)
	freeze(c, 180)
	if got, _ := nextOwner(t, store, now, spec).InheritedQueryDuration("qg"); got != 10*time.Second {
		t.Fatalf("written within the minute: the next owner reads %v", got)
	}
	// A minute on: written.
	*now = now.Add(31 * time.Second)
	freeze(c, 240)
	if got, _ := nextOwner(t, store, now, spec).InheritedQueryDuration("qg"); got != 12*time.Second {
		t.Fatalf("a minute on the next owner reads %v, want 12 s", got)
	}
	// Moved within its second: not written, however long after.
	*now = now.Add(time.Hour)
	c.MeasureQueryDuration("qg", 11900*time.Millisecond)
	freeze(c, 300)
	if got := writes(c); got[QueryDurationWriteChanged] != 2 || got[QueryDurationWriteRefreshed] != 0 {
		t.Fatalf("writes %v, want two changed and none refreshed", got)
	}
	// Half way to stale: refreshed, and a day after that write still a reading.
	*now = now.Add(QueryDurationStaleAfter / 2)
	freeze(c, 360)
	if got := writes(c); got[QueryDurationWriteRefreshed] != 1 {
		t.Fatalf("writes %v, want one refreshed", got)
	}
	*now = now.Add(QueryDurationStaleAfter - time.Minute)
	if _, ok := nextOwner(t, store, now, spec).InheritedQueryDuration("qg"); !ok {
		t.Fatal("a refreshed reading read as stale")
	}
	// A day after the last write, with nothing written since: no reading.
	*now = now.Add(time.Minute)
	if got, ok := nextOwner(t, store, now, spec).InheritedQueryDuration("qg"); ok {
		t.Fatalf("a reading a day old still read as %v", got)
	}
}

// A group that holds nothing and keeps no record gets none for its query
// duration: a missing record is what tells its successor it held nothing,
// and that it never measured an arrival.
func TestAQueryDurationAloneKeepsNoRecord(t *testing.T) {
	c, store, now := controllerFixture(t)
	spec := groupSpec("qg", 0)
	prepare(t, c, spec)
	c.MeasureQueryDuration("qg", 9*time.Second)
	if _, err := c.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), 120, holdFence("qg")); err != nil {
		t.Fatal(err)
	}
	if len(store.values) != 0 || len(store.requests) != 0 {
		t.Fatalf("a record was written for the query duration alone: %d values, %d writes", len(store.values), len(store.requests))
	}
	if _, ok := nextOwner(t, store, now, spec).InheritedQueryDuration("qg"); ok {
		t.Fatal("a group without a record read as having a query duration")
	}
	if got, ok := c.InheritedQueryDuration("qg"); ok {
		t.Fatalf("a reading kept only in memory read as the record's: %v", got)
	}
	if got := c.Stats().QueryDurationWrites[QueryDurationWriteChanged]; got != 0 {
		t.Fatalf("%d writes counted, want none", got)
	}
	// The hold rises and the record is first written, the reading with it:
	// that write is the reading's first, and counted.
	observeEarly(t, c, "qg", 60*time.Second+spec.SettlingWait, 0)
	if _, err := c.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), 180, holdFence("qg")); err != nil {
		t.Fatal(err)
	}
	if got, ok := nextOwner(t, store, now, spec).InheritedQueryDuration("qg"); !ok || got != 9*time.Second {
		t.Fatalf("once the record is written the next owner reads %v %v, want 9 s", got, ok)
	}
	if got := c.Stats().QueryDurationWrites[QueryDurationWriteChanged]; got != 1 {
		t.Fatalf("the reading's first write counted %d times, want once", got)
	}
}

// Asking a forgotten group for its reading, or giving it one, makes it no
// entry again.
func TestAForgottenGroupStaysForgotten(t *testing.T) {
	c, _, _ := controllerFixture(t)
	prepare(t, c, groupSpec("qg", 0))
	c.Forget("qg")
	c.MeasureQueryDuration("qg", 9*time.Second)
	if _, ok := c.InheritedQueryDuration("qg"); ok {
		t.Fatal("a forgotten group read as having a query duration")
	}
	if c.known("qg") != nil {
		t.Fatal("a forgotten group was made an entry again")
	}
}

// A record a build before the reading wrote has none; one this build wrote
// decodes under a build that knows nothing of it, as any later field does;
// and a negative reading does not decode.
func TestARecordWithoutAQueryDurationHasNone(t *testing.T) {
	old := []byte(`{"hold_ms":120000,"since_slot":60,"arrival_age_ms":130000}`)
	record, err := Decode(old)
	if err != nil || record.QueryDurationMillis != 0 || record.QueryDurationAtMillis != 0 {
		t.Fatalf("an old record: %+v %v", record, err)
	}
	if _, err := Decode([]byte(`{"hold_ms":120000,"since_slot":60,"query_duration_ms":-1}`)); err == nil {
		t.Fatal("a negative query duration decoded")
	}
	c, store, _ := controllerFixture(t)
	spec := groupSpec("qg", 0)
	store.values["qg"] = old
	prepare(t, c, spec)
	if got, ok := c.InheritedQueryDuration("qg"); ok {
		t.Fatalf("an old record read as a query duration of %v", got)
	}
}
