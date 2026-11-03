// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package readhold

import (
	"context"
	"testing"
	"time"
)

// A record says held at its limit only while its hold is at the limit: the
// flag says the last arrival wanted more than the limit, and a hold under
// the limit - one an earlier build lowered by trials, before trials at the
// limit were refused - is not held at it.
func TestAHoldUnderItsLimitIsNotHeldAtIt(t *testing.T) {
	pending := int64(600_000)
	for _, test := range []struct {
		name   string
		record Record
		want   bool
	}{
		{"at the limit", Record{AtLimit: true, LimitMillis: 600_000, HoldMillis: 600_000}, true},
		{"pending at the limit", Record{AtLimit: true, LimitMillis: 600_000, HoldMillis: 300_000, PendingHoldMillis: &pending}, true},
		{"lowered under the limit", Record{AtLimit: true, LimitMillis: 600_000, HoldMillis: 300_000}, false},
		{"no limit recorded", Record{AtLimit: true, HoldMillis: 600_000}, false},
		{"not flagged", Record{LimitMillis: 600_000, HoldMillis: 600_000}, false},
	} {
		if got := test.record.HeldAtLimit(); got != test.want {
			t.Errorf("%s: held at limit %t, want %t (record %+v)", test.name, got, test.want, test.record)
		}
	}
}

// A group seeded past its limit starts held at the limit, and one seeded
// under it does not.
func TestASeedPastTheLimitStartsHeldAtIt(t *testing.T) {
	for _, test := range []struct {
		name  string
		seed  time.Duration
		limit time.Duration
		want  bool
	}{
		{"past the limit", 90 * time.Second, 30 * time.Second, true},
		{"under the limit", 30 * time.Second, 60 * time.Second, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _, _ := controllerFixture(t)
			spec := groupSpec("qg", 0)
			spec.SettlingWait = 10 * time.Second
			spec.HoldLimit = test.limit
			spec.SeedArrivalAge, spec.SeedOrigin = test.seed, SeedColdStart
			prepare(t, c, spec)
			if _, err := c.SlotReadHold(context.Background(), scheduleFor(t, "qg", 60, nil), 120, holdFence("qg")); err != nil {
				t.Fatal(err)
			}
			record, _ := c.Reading("qg")
			if record.HeldAtLimit() != test.want {
				t.Fatalf("seeded %v under limit %v: held at limit %t, want %t (record %+v)", test.seed, test.limit,
					record.HeldAtLimit(), test.want, record)
			}
		})
	}
}
