// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package readhold

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A group with no record of its own reads first where its data has been
// seen to land: its peers' arrival age, or, on a source nobody has measured,
// the cold-start one. Its first Slot is held that far past its settling
// wait, the record says the arrival age was seeded and from where, and each
// origin is counted.
func TestAGroupWithoutARecordStartsFromItsSeed(t *testing.T) {
	for _, test := range []struct {
		name   string
		seed   time.Duration
		origin string
	}{
		{"from its peers", 22 * time.Second, SeedPeer},
		{"on a source nobody measured", 30 * time.Second, SeedColdStart},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, store, _ := controllerFixture(t)
			spec := groupSpec("qg", 0)
			spec.SettlingWait = 10 * time.Second
			spec.SeedArrivalAge, spec.SeedOrigin = test.seed, test.origin
			prepare(t, c, spec)
			hold, err := c.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), 120, holdFence("qg"))
			if err != nil || hold != test.seed-10*time.Second {
				t.Fatalf("first hold %v, %v; want the seed past the settling wait", hold, err)
			}
			record, _ := c.Reading("qg")
			if record.ArrivalAgeMillis != test.seed.Milliseconds() || record.ArrivalAgeSeed != test.origin || len(store.requests) != 1 {
				t.Fatalf("record %+v after %d writes", record, len(store.requests))
			}
			if got := c.Stats().Seeds[test.origin]; got != 1 {
				t.Fatalf("seeds counted %v", c.Stats().Seeds)
			}
		})
	}
}

// A group whose own record is there - a measured zero included, which is a
// record with nothing in it - is never seeded: its own reading stands.
func TestARecordedGroupIsNotSeeded(t *testing.T) {
	c, store, _ := controllerFixture(t)
	store.values["qg"] = []byte(`{"since_slot":1,"segment_start":60}`)
	spec := groupSpec("qg", 0)
	spec.SettlingWait = 10 * time.Second
	spec.SeedArrivalAge, spec.SeedOrigin = 30*time.Second, SeedColdStart
	prepare(t, c, spec)
	hold, err := c.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), 120, holdFence("qg"))
	if err != nil || hold != 0 {
		t.Fatalf("hold %v, %v; want the recorded zero", hold, err)
	}
	if record, _ := c.Reading("qg"); record.ArrivalAgeSeed != "" || len(c.Stats().Seeds) != 0 {
		t.Fatalf("a recorded group was seeded: %+v %v", record, c.Stats().Seeds)
	}
}

// A seed is where reading starts, not what was measured: the group's own
// early reads replace it, and its own arrival age drops the seed's mark.
func TestAMeasuredArrivalReplacesTheSeed(t *testing.T) {
	c, _, _ := controllerFixture(t)
	spec := groupSpec("qg", 0)
	spec.SettlingWait = 10 * time.Second
	spec.SeedArrivalAge, spec.SeedOrigin = 30*time.Second, SeedColdStart
	prepare(t, c, spec)
	if _, err := c.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), 120, holdFence("qg")); err != nil {
		t.Fatal(err)
	}
	observeEarly(t, c, "qg", 50*time.Second, 20_000)
	record, _ := c.Reading("qg")
	if record.ArrivalAgeSeed != "" || record.ArrivalAgeMillis != 50_000 {
		t.Fatalf("after a measured 50 s arrival the record is %+v", record)
	}
}

// A seed at or under the group's own read holds nothing, and a group at no
// hold keeps no record: a missing record is what tells its successor its hold
// was zero.
func TestASeedThatHoldsNothingWritesNoRecord(t *testing.T) {
	c, store, _ := controllerFixture(t)
	spec := groupSpec("qg", 0)
	spec.SettlingWait = 10 * time.Second
	spec.SeedArrivalAge, spec.SeedOrigin = 10*time.Second, SeedColdStart
	prepare(t, c, spec)
	hold, err := c.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), 120, holdFence("qg"))
	if err != nil || hold != 0 || len(store.requests) != 0 || len(c.Stats().Seeds) != 0 {
		t.Fatalf("hold %v err %v, %d writes, seeds %v; want nothing", hold, err, len(store.requests), c.Stats().Seeds)
	}
}

// Only a group whose own record was read, is there and decodes has a record
// of its own; the seed is worked out for every other.
func TestOnlyARecordReadAndDecodedIsTheGroupsOwn(t *testing.T) {
	c, store, _ := controllerFixture(t)
	store.values["recorded"] = []byte(`{"since_slot":1,"segment_start":60}`)
	store.values["corrupt"] = []byte(`not json`)
	for _, qg := range []string{"recorded", "missing"} {
		prepare(t, c, groupSpec(execution.QueryGroupIdentity(qg), 0))
	}
	// A corrupt record is restored with its error, the group still read.
	if err := c.Configure(groupSpec("corrupt", 0)); err != nil {
		t.Fatal(err)
	}
	_ = c.RestoreBatch(context.Background(), []execution.QueryGroupIdentity{"corrupt"})
	if !c.HasOwnRecord("recorded") || c.HasOwnRecord("corrupt") || c.HasOwnRecord("missing") || c.HasOwnRecord("never-configured") {
		t.Fatal("HasOwnRecord did not follow what was read")
	}
	if arrival, seed := c.Arrival("missing"); arrival != 0 || seed != "" {
		t.Fatalf("a group with nothing read reports arrival %d (%q)", arrival, seed)
	}
}
