// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A read asked to yield is timed from the yield to the moment it gave its
// permit back: the wait a formal query owes the lookback. One that stops at
// once is counted, and is no fault.
func TestAYieldedReadIsTimedFromTheYieldToItsRelease(t *testing.T) {
	f := newFixture(t)
	yield := make(chan struct{})
	f.set(func() { f.yield = yield })
	f.capture(query("qg", 1_700_000_100, minute, sourceLog), dataset("h1", steady))
	f.clock.set(f.clock.now().Add(rungDelay(0, minute)))
	f.engine.Step(context.Background()) // no answer queued: the read runs until it is asked to yield
	close(yield)
	// The release is timed as the permit comes back and the rung is counted
	// preempted after, under the lock a second time: waited for together, a
	// read of the counters between the two is not taken for the answer.
	stats := f.waitFor(func(stats Stats) bool {
		source := stats.Sources[sourceLog]
		return source.YieldReleases == 1 && source.Preempted[RungNames[0]] == 1
	})
	source := stats.Sources[sourceLog]
	if source.Preempted[RungNames[0]] != 1 || source.YieldReleaseSeconds < 0 || source.YieldReleaseMaxSeconds > RecheckTimeout.Seconds() ||
		stats.Faults[FaultYieldOverdue] != 0 {
		t.Fatalf("preempted %v, yield releases %d in %vs (max %vs), faults %v", source.Preempted, source.YieldReleases,
			source.YieldReleaseSeconds, source.YieldReleaseMaxSeconds, stats.Faults)
	}
	for name, other := range stats.Sources {
		if name != sourceLog && other.YieldReleases != 0 {
			t.Fatalf("source %s counted a yield release", name)
		}
	}
}

// A read that neither stops when asked to yield nor at its own deadline
// holds a permit a formal query waits for: past RecheckTimeout after the
// yield it is a yield_overdue fault, counted once - by the pass that finds
// it still running, not again when it finally gives the permit back - and
// its release is still timed.
func TestAReadStillHoldingItsPermitPastItsDeadlineAfterAYieldIsAFault(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_100, 0)}
	yield := make(chan struct{})
	started := make(chan struct{}, 1)
	stop := make(chan struct{})
	faults := make(chan string, 4)
	engine, err := New(Options{Now: c.now, UnspreadFirstSamples: true,
		Recheck: func(context.Context, execution.PhysicalQuerySpec, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
			started <- struct{}{}
			<-stop // honours neither the yield nor its deadline
			return execution.ProviderCompletion{}, errors.New("stopped late")
		},
		Permit: func() (func(), <-chan struct{}, string) { return func() {}, yield, "" },
		Owns:   func(execution.QueryGroupIdentity) bool { return true }, Owned: func() int { return 1 },
		OnFault: func(reason string, _ execution.QueryGroupIdentity) { faults <- reason },
	})
	if err != nil {
		t.Fatal(err)
	}
	read := engine.Begin(query("qg", 1_700_000_100, minute, sourceLog))
	read.Series(dataset("h1", steady), 10)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	c.set(c.now().Add(rungDelay(0, minute)))
	engine.Step(context.Background())
	<-started
	close(yield)
	yielded := func() bool {
		engine.mu.Lock()
		defer engine.mu.Unlock()
		return engine.groups["qg"].sample != nil && !engine.groups["qg"].sample.yieldAt.IsZero()
	}
	for deadline := time.Now().Add(5 * time.Second); !yielded(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the yield was never noted")
		}
	}
	c.set(c.now().Add(RecheckTimeout))
	engine.Step(context.Background())
	if n := engine.Stats().Faults[FaultYieldOverdue]; n != 0 {
		t.Fatalf("a read at exactly its deadline after the yield is a fault: %d", n)
	}
	c.set(c.now().Add(time.Second))
	engine.Step(context.Background())
	engine.Step(context.Background())
	if n := engine.Stats().Faults[FaultYieldOverdue]; n != 1 {
		t.Fatalf("yield_overdue %d, want the read still running past its deadline counted once", n)
	}
	if reason := <-faults; reason != FaultYieldOverdue {
		t.Fatalf("fault told as %q", reason)
	}
	close(stop)
	stats := func() Stats {
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
			if stats := engine.Stats(); stats.Sources[sourceLog].YieldReleases == 1 {
				return stats
			}
			if time.Now().After(deadline) {
				t.Fatal("the late release was not timed")
			}
		}
	}()
	source := stats.Sources[sourceLog]
	if stats.Faults[FaultYieldOverdue] != 1 || source.YieldReleaseMaxSeconds <= RecheckTimeout.Seconds() {
		t.Fatalf("after the release: yield_overdue %d, longest release %vs", stats.Faults[FaultYieldOverdue], source.YieldReleaseMaxSeconds)
	}
	if source.Samples[OutcomeFault] != 0 {
		t.Fatalf("a yield_overdue fault ended its sample: %v", source.Samples)
	}
}

// A read that honours neither its deadline nor a cancellation is still
// holding its permit after its context has ended. A yield that comes only
// then is still noted: past RecheckTimeout it is a yield_overdue fault, and
// the release, when it finally comes, is timed.
func TestAYieldAfterTheReadsContextEndedIsStillWatched(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_100, 0)}
	yield := make(chan struct{})
	started := make(chan struct{}, 1)
	stop := make(chan struct{})
	engine, err := New(Options{Now: c.now, UnspreadFirstSamples: true,
		Recheck: func(context.Context, execution.PhysicalQuerySpec, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
			started <- struct{}{}
			<-stop // honours neither its context nor the yield
			return execution.ProviderCompletion{}, errors.New("stopped late")
		},
		Permit: func() (func(), <-chan struct{}, string) { return func() {}, yield, "" },
		Owns:   func(execution.QueryGroupIdentity) bool { return true }, Owned: func() int { return 1 },
	})
	if err != nil {
		t.Fatal(err)
	}
	read := engine.Begin(query("qg", 1_700_000_100, minute, sourceLog))
	read.Series(dataset("h1", steady), 10)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	c.set(c.now().Add(rungDelay(0, minute)))
	ctx, cancel := context.WithCancel(context.Background())
	engine.Step(ctx)
	<-started
	cancel() // the read's context ends; the read does not
	time.Sleep(50 * time.Millisecond)
	close(yield)
	time.Sleep(50 * time.Millisecond)
	c.set(c.now().Add(RecheckTimeout + time.Second))
	engine.Step(context.Background())
	if n := engine.Stats().Faults[FaultYieldOverdue]; n != 1 {
		t.Fatalf("yield_overdue %d, want the yield after the read's context ended watched and counted", n)
	}
	close(stop)
	for deadline := time.Now().Add(5 * time.Second); engine.Stats().Sources[sourceLog].YieldReleases != 1; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the release after the late yield was not timed")
		}
	}
}

// A permit's yield is almost never closed: most reads finish while nobody
// waits. Such a read settles its rung and lets its watcher go, however long
// the yield stays open.
func TestAReadNeverAskedToYieldSettlesItsRung(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_100, 0)}
	engine, err := New(Options{Now: c.now, UnspreadFirstSamples: true,
		Recheck: func(ctx context.Context, _ execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
			_ = sink.ConsumeProviderSeries(ctx, execution.ProviderSeriesBatch{Dataset: dataset("h1", steady)})
			return execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil
		},
		Permit: func() (func(), <-chan struct{}, string) { return func() {}, make(chan struct{}), "" },
		Owns:   func(execution.QueryGroupIdentity) bool { return true }, Owned: func() int { return 1 },
	})
	if err != nil {
		t.Fatal(err)
	}
	read := engine.Begin(query("qg", 1_700_000_100, minute, sourceLog))
	read.Series(dataset("h1", steady), 10)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	c.set(c.now().Add(rungDelay(0, minute)))
	engine.Step(context.Background())
	for deadline := time.Now().Add(5 * time.Second); engine.Stats().Sources[sourceLog].Samples[OutcomeCompleted] != 1; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("a read no formal query asked to yield never settled: %v", engine.Stats().Sources[sourceLog].Rechecks[RungNames[0]])
		}
	}
	if source := engine.Stats().Sources[sourceLog]; source.YieldReleases != 0 || source.Preempted[RungNames[0]] != 0 {
		t.Fatalf("a read never asked to yield: yield releases %d, preempted %v", source.YieldReleases, source.Preempted)
	}
}

// A formal query that comes to wait after a read came back, while it
// gives its permit back, did not stop it: the release is not timed as a
// yield, and the rung is compared, not preempted.
func TestAYieldAfterTheReadCameBackIsNotTimed(t *testing.T) {
	c := &clock{at: time.Unix(1_700_000_100, 0)}
	engine, err := New(Options{Now: c.now, UnspreadFirstSamples: true,
		Recheck: func(ctx context.Context, _ execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
			_ = sink.ConsumeProviderSeries(ctx, execution.ProviderSeriesBatch{Dataset: dataset("h1", steady)})
			return execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil
		},
		Permit: func() (func(), <-chan struct{}, string) {
			yield := make(chan struct{})
			// The waiting formal query arrives as the permit is given back,
			// and the release lingers long enough for the watcher to take
			// the yield before a read that marked itself back only now
			// would have.
			return func() { close(yield); time.Sleep(50 * time.Millisecond) }, yield, ""
		},
		Owns: func(execution.QueryGroupIdentity) bool { return true }, Owned: func() int { return 1 },
	})
	if err != nil {
		t.Fatal(err)
	}
	read := engine.Begin(query("qg", 1_700_000_100, minute, sourceLog))
	read.Series(dataset("h1", steady), 10)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	c.set(c.now().Add(rungDelay(0, minute)))
	engine.Step(context.Background())
	var source SourceStats
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		source = engine.Stats().Sources[sourceLog]
		if source.Rechecks[RungNames[0]][RecheckCompared] == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the rung was not compared: %v", source.Rechecks[RungNames[0]])
		}
	}
	if source.YieldReleases != 0 || source.Preempted[RungNames[0]] != 0 {
		t.Fatalf("a yield after the read came back: yield releases %d, preempted %v", source.YieldReleases, source.Preempted)
	}
}

// A process's first samples come due together within the hour after it
// starts, so a group's first sample is not probed; its second, a rest
// later, is, and every fourth after that.
func TestAGroupIsFirstProbedAtItsSecondSample(t *testing.T) {
	w := &windowed{clock: &clock{at: time.Unix(1_700_006_000, 0)}, step: 60}
	engine := w.engine(t)
	query := facts(t, minute, "", execution.QueryClause{TimeAggregation: execution.QueryFunction{Method: "sum_over_time", Window: "60s"}})
	probes := func() uint64 {
		n := uint64(0)
		for _, count := range engine.Stats().Sources[sourceTimeSeries].Probes {
			n += count
		}
		return n
	}
	for sample, want := range []uint64{0, 1, 1, 1, 1, 2} {
		w.run(t, engine, 5*minute, query)
		if got := probes(); got != want {
			t.Fatalf("after sample %d: %d deep rechecks, want %d", sample+1, got, want)
		}
	}
}
