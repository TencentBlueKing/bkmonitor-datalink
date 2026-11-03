// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package lookback

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// overlapFixture is an engine whose first permit waits for gate, with one
// sample due at its last rung (the state a probe enters: rung 5 of 6).
type overlapFixture struct {
	engine   *Engine
	gate     chan struct{}
	asked    chan struct{}
	rechecks atomic.Int64
}

func newOverlapFixture(t *testing.T) *overlapFixture {
	t.Helper()
	c := &clock{at: time.Unix(1_700_000_100, 0)}
	f := &overlapFixture{gate: make(chan struct{}), asked: make(chan struct{}, 1)}
	var permits atomic.Int64
	engine, err := New(Options{Now: c.now, UnspreadFirstSamples: true,
		Recheck: func(context.Context, execution.PhysicalQuerySpec, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
			f.rechecks.Add(1)
			return execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil
		},
		Permit: func() (func(), <-chan struct{}, string) {
			if permits.Add(1) == 1 {
				f.asked <- struct{}{}
				<-f.gate
			}
			return func() {}, make(chan struct{}), ""
		},
		Owns: func(execution.QueryGroupIdentity) bool { return true }, Owned: func() int { return 1 },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.engine = engine
	read := engine.Begin(query("qg", 1_700_000_100, minute, sourceLog))
	read.Series(dataset("h1", steady), 10)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	engine.mu.Lock()
	candidate := engine.groups["qg"].sample
	candidate.rung, candidate.planned = len(RungSteps)-1, len(RungSteps)
	engine.mu.Unlock()
	c.set(c.now().Add(rungDelay(len(RungSteps)-1, minute)))
	return f
}

// A pass over the samples runs alone. A second pass that comes while one is
// still waiting for a permit does nothing: both would launch the same due
// sample, and the read that finished first moves it past its last rung
// before the other reads it (index out of range on RungNames).
func TestAPassThatComesWhileOneIsWaitingForAPermitLaunchesNothing(t *testing.T) {
	f := newOverlapFixture(t)
	first := make(chan struct{})
	go func() { defer close(first); f.engine.Step(context.Background()) }()
	<-f.asked
	f.engine.Step(context.Background())
	f.engine.mu.Lock()
	launched := f.engine.groups["qg"].sample.running
	f.engine.mu.Unlock()
	if launched {
		// Left running, the waiting pass launches it again once its permit
		// comes, and the second read indexes past the last rung.
		t.Fatal("the overlapping pass launched the sample the waiting pass is about to launch")
	}
	close(f.gate)
	<-first
	finished := func() bool {
		f.engine.mu.Lock()
		defer f.engine.mu.Unlock()
		return f.engine.groups["qg"].sample == nil
	}
	testwait.Until(t, "the waiting pass's recheck finished the sample", finished)
	if n := f.rechecks.Load(); n != 1 {
		t.Fatalf("rechecks %d; want the last rung read once", n)
	}
}
