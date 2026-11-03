// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"context"
	"testing"
	"time"
)

// writerCase is a store refreshed once a minute against a writer whose
// publish times the case sets.
type writerCase struct {
	t           *testing.T
	now         time.Time
	published   time.Time
	store       *Store
	transitions []Transition
}

func newWriterCase(t *testing.T) *writerCase {
	t.Helper()
	c := &writerCase{t: t, now: time.Unix(1_800_000_000, 0)}
	c.published = c.now
	store, err := NewStore(writerLoads{c: c}, StoreOptions{RefreshInterval: time.Minute, MaxAge: 10 * time.Minute,
		PublishedMaxAge: 15 * time.Minute, PublishedMaxAgeCap: 2 * time.Hour, Now: func() time.Time { return c.now },
		OnTransition: func(transition Transition) { c.transitions = append(c.transitions, transition) }})
	if err != nil {
		t.Fatal(err)
	}
	c.store = store
	return c
}

// writerLoads loads the writer's last publish.
type writerLoads struct{ c *writerCase }

func (loads writerLoads) Load(context.Context, time.Time) (*Index, error) {
	return identityLoad([]string{"720002", identityLive}, map[string]string{}, loads.c.published, 0)()
}

// minute refreshes once and moves the clock a minute on.
func (c *writerCase) minute() string {
	c.t.Helper()
	if err := c.store.Refresh(context.Background()); err != nil {
		c.t.Fatal(err)
	}
	_, reason := c.store.Usable()
	c.now = c.now.Add(time.Minute)
	return reason
}

// run lets the writer publish after each of gaps, refreshing every minute,
// and returns how many refreshes found the index stale.
func (c *writerCase) run(gaps ...time.Duration) int {
	stale := 0
	for _, gap := range gaps {
		next := c.published.Add(gap)
		for c.now.Before(next) {
			if c.minute() == IndexStale {
				stale++
			}
		}
		c.published = next
	}
	return stale
}

func repeat(gap time.Duration, n int) []time.Duration {
	gaps := make([]time.Duration, n)
	for i := range gaps {
		gaps[i] = gap
	}
	return gaps
}

// A writer measured at one deployment publishes every five minutes (gaps 2.4
// to 6.4 minutes) but skips a cycle every half hour to hour - its long gaps
// over six hours were 18.3, 16.3, 15.4, 19.8, 15.0, 32.9, 19.3 and 16.2
// minutes, and with the fixed
// 15-minute bound the index was stale about a tenth of the time. With the
// bound derived from the longest recent gap, none of it is stale once the
// first long gap has been seen.
func TestAWriterThatSkipsCyclesIsNotStale(t *testing.T) {
	c := newWriterCase(t)
	c.run(repeat(5*time.Minute, 4)...)
	minutes := func(m float64) time.Duration { return time.Duration(m * float64(time.Minute)) }
	var gaps []time.Duration
	for _, long := range []float64{18.3, 16.3, 15.4, 19.8, 15.0, 32.9, 19.3, 16.2} {
		gaps = append(gaps, repeat(5*time.Minute, 8)...)
		gaps = append(gaps, minutes(long))
	}
	// The first long gap is the one no earlier gap covers: 18.3 minutes
	// against the 15-minute floor - 2 x 5 minutes is below it - is stale for
	// three refreshes. Every gap after it is covered.
	if stale := c.run(gaps[:9]...); stale > 4 {
		t.Fatalf("the first long gap was stale for %d refreshes, want at most 4", stale)
	}
	if stale := c.run(gaps[9:]...); stale != 0 {
		t.Fatalf("the writer was stale for %d refreshes after its first long gap, want none", stale)
	}
	if bound := c.store.Health().PublishedBound; bound != 2*minutes(32.9) {
		t.Fatalf("bound %s, want twice the longest gap, %s", bound, 2*minutes(32.9))
	}
}

// A writer that stops is stale at twice its longest recent gap, and never
// later than the cap.
func TestAWriterThatStopsGoesStale(t *testing.T) {
	c := newWriterCase(t)
	c.run(append(repeat(5*time.Minute, 6), 12*time.Minute, 5*time.Minute)...)
	stopped := c.published
	if c.minute() == IndexStale {
		t.Fatal("stale right after a publish")
	}
	for c.now.Sub(stopped) <= 24*time.Minute {
		if c.minute() == IndexStale {
			t.Fatalf("stale %s after the last publish, before twice the longest gap, 24m", c.now.Sub(stopped))
		}
	}
	c.minute()
	c.minute()
	if _, reason := c.store.Usable(); reason != IndexStale {
		t.Fatalf("still %q %s after the writer stopped, want stale past 24m", reason, c.now.Sub(stopped))
	}

	long := newWriterCase(t)
	long.run(5*time.Minute, 3*time.Hour)
	long.minute()
	if bound := long.store.Health().PublishedBound; bound != 2*time.Hour {
		t.Fatalf("after a three-hour gap the bound is %s, want the 2h cap", bound)
	}
}

// Before two publishes have been seen the configured bound holds, and a
// steady fast writer is held to it as a floor.
func TestThePublishedBoundStartsAtAndNeverFallsBelowTheConfiguredOne(t *testing.T) {
	c := newWriterCase(t)
	c.minute()
	if bound := c.store.Health().PublishedBound; bound != 15*time.Minute {
		t.Fatalf("after one publish the bound is %s, want 15m", bound)
	}
	c.run(repeat(2*time.Minute, 6)...)
	if bound := c.store.Health().PublishedBound; bound != 15*time.Minute {
		t.Fatalf("a two-minute writer's bound is %s, want the 15m floor", bound)
	}
}

// Going into stale and out of it again are each told once, with the age, the
// bound and the longest gap.
func TestAStaleTransitionIsToldOnceEachWay(t *testing.T) {
	c := newWriterCase(t)
	c.run(repeat(5*time.Minute, 3)...)
	c.run(25 * time.Minute)
	c.minute()
	var into, out []Transition
	for _, transition := range c.transitions {
		switch {
		case transition.To == IndexStale:
			into = append(into, transition)
		case transition.From == IndexStale:
			out = append(out, transition)
		}
	}
	if len(into) != 1 || len(out) != 1 {
		t.Fatalf("transitions %+v, want one into stale and one out", c.transitions)
	}
	if into[0].Tenant != DefaultTenant || into[0].Bound != 15*time.Minute || into[0].MaxGap != 5*time.Minute ||
		into[0].PublishedAge <= into[0].Bound {
		t.Fatalf("into stale %+v, want the default tenant, a 15m bound, a 5m gap and an age past the bound", into[0])
	}
	if out[0].To != "" || out[0].MaxGap != 25*time.Minute {
		t.Fatalf("out of stale %+v, want usable and the 25m gap now seen", out[0])
	}
}
