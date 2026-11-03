package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The maintenance loop reads a Query Group's Plans from the store once, and
// again only when the lease it read them under moves. Every other tick is
// answered from memory: the read is not repeated and the owner is not asked.
// Before this every owned Query Group was read on every tick, whether or not
// any of its Plans had a schedule, which on a deployment where none did was
// a constant rate of fence checks and object reads that answered nothing.
func TestMaintenanceReadsAQueryGroupsPlansOnlyWhenItsLeaseMoves(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(10, 0), nil)
	catalog := f.m.catalog.(*maintenanceTestCatalog)
	for i := 0; i < 5; i++ {
		f.m.step(context.Background())
		f.advance(time.Second)
	}
	if catalog.reads != 1 || f.runner.entered != 1 {
		t.Fatalf("five ticks on a steady lease read the store %d times and entered the owner %d times, want once each", catalog.reads, f.runner.entered)
	}
	f.runner.revision = 2
	f.m.step(context.Background())
	if catalog.reads != 2 {
		t.Fatalf("the timeline revision moved and the Plans were not read again: %d reads", catalog.reads)
	}
	f.runner.scope = "obj-b"
	f.m.step(context.Background())
	if catalog.reads != 3 {
		t.Fatalf("the content scope moved and the Plans were not read again: %d reads", catalog.reads)
	}
	// A read that fails is retried on the next tick rather than remembered.
	catalog.err = errors.New("store unavailable")
	f.runner.revision = 3
	f.m.step(context.Background())
	f.m.step(context.Background())
	if catalog.reads != 5 {
		t.Fatalf("a failed read was not retried: %d reads", catalog.reads)
	}
}

// A Query Group none of whose Plans has a schedule needs nothing from the
// loop, and after its one read costs it nothing: no fence check, no legacy
// refresh, no close. This is the deployment-wide case wherever no strategy
// has an uptime, and it has to be free.
func TestAQueryGroupWithoutAScheduleCostsNothingAfterItsRead(t *testing.T) {
	f := newMaintenanceTestFixture(t, "", maintenanceTime(8, 0), maintenanceMatches,
		func(plan *contract.EvaluationPlanV2) {
			plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)
		})
	refresher := &countingLegacyRefresh{}
	f.m.legacyCache = refresher
	for i := 0; i < 5; i++ {
		f.m.step(context.Background())
		f.advance(time.Second)
	}
	if f.runner.entered != 1 || refresher.calls != 0 || len(f.closed()) != 0 {
		t.Fatalf("a Query Group without a schedule entered the owner %d times, refreshed %d times, closed %d batches; want 1, 0, 0",
			f.runner.entered, refresher.calls, len(f.closed()))
	}
}

type countingLegacyRefresh struct {
	calls int
	seen  []string
}

func (c *countingLegacyRefresh) Refresh(_ context.Context, tenant, business string, _ []int64) error {
	c.calls++
	c.seen = append(c.seen, tenant+"/"+business)
	return nil
}

// Every legacy Plan of a Query Group is refreshed in one round, not one Plan
// per tick: a Plan whose entries are fresh costs the refresher nothing, so
// the round's store reads are the entries that are due. Before this a Query
// Group with N legacy Plans reached each of them every N ticks, and past the
// trust window that is a Level frozen for want of a read nobody was too busy
// to make.
func TestEveryLegacyPlanOfAQueryGroupIsRefreshedEachRound(t *testing.T) {
	f := newMaintenanceTestFixture(t, "", maintenanceTime(10, 0), nil)
	catalog := f.m.catalog.(*maintenanceTestCatalog)
	second := catalog.plans[0]
	second.Identity.StrategyID = "124"
	catalog.plans = append(catalog.plans, second)
	refresher := &countingLegacyRefresh{}
	f.m.legacyCache = refresher
	f.m.step(context.Background())
	if refresher.calls != 2 {
		t.Fatalf("one round refreshed %d of two legacy Plans", refresher.calls)
	}
}

// Every way a close does not happen has a name and a count, and every
// outcome has a cell whether or not it happened: a Query Group whose flight
// is always busy, one whose owner check refuses before every send, one the
// link does not settle, and one the link has nothing open for were the same
// silence as a Query Group with nothing to close.
func TestEveryWayACloseDoesNotHappenIsNamedAndCounted(t *testing.T) {
	t.Run("done", func(t *testing.T) {
		f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
		f.m.catalog.(*maintenanceTestCatalog).uncompilable = 3
		f.m.step(context.Background())
		stats := f.m.Stats()
		if len(stats) != len(observability.EffectiveCloseOutcomes) {
			t.Fatalf("stats carry %d cells, want every one of the %d outcomes", len(stats), len(observability.EffectiveCloseOutcomes))
		}
		if stats["close_done"] != 1 || stats["maintenance_plan_uncompilable"] != 3 {
			t.Fatalf("stats=%v, want one close done and three uncompilable Plans", stats)
		}
	})
	t.Run("busy keeps the Plans", func(t *testing.T) {
		f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
		f.runner.check = func(context.Context) error { return errMaintenanceBusy }
		f.m.step(context.Background())
		f.m.step(context.Background())
		if stats := f.m.Stats(); stats["maintenance_busy"] != 2 || len(f.closed()) != 0 {
			t.Fatalf("stats=%v closes=%d, want two busy outcomes and no batch", stats, len(f.closed()))
		}
		if reads := f.m.catalog.(*maintenanceTestCatalog).reads; reads != 1 {
			t.Fatalf("a busy flight made the loop read the Plans again: %d reads", reads)
		}
	})
	t.Run("precheck refused reads the Plans again", func(t *testing.T) {
		f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
		refused := errors.New("owner lost")
		f.runner.check = func(context.Context) error { return refused }
		f.m.step(context.Background())
		if stats := f.m.Stats(); stats["close_precheck_failed"] != 1 || !errors.Is(f.runner.lastErr, refused) {
			t.Fatalf("stats=%v lastErr=%v", stats, f.runner.lastErr)
		}
		f.m.step(context.Background())
		if reads := f.m.catalog.(*maintenanceTestCatalog).reads; reads != 2 {
			t.Fatalf("after the owner check refused, the Plans were not read again: %d reads", reads)
		}
	})
	t.Run("not settled", func(t *testing.T) {
		f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), maintenanceMatches)
		f.stub.busy = true
		f.m.step(context.Background())
		if stats := f.m.Stats(); stats["close_not_settled"] != 1 || stats["close_done"] != 0 {
			t.Fatalf("stats=%v", stats)
		}
	})
	t.Run("nothing matched", func(t *testing.T) {
		f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(8, 0), nil)
		f.m.step(context.Background())
		if stats := f.m.Stats(); stats["close_nothing_matched"] != 1 || stats["close_done"] != 0 || len(f.closed()) != 0 {
			t.Fatalf("stats=%v closes=%v", stats, f.closed())
		}
	})
}

// A read the executable view does not allow yet is named as such and tried
// again next tick; a read the store did not answer stays "unavailable". The
// first is every owned Query Group once or twice in a replica's first seconds
// after a rollout; under one word it read as a few hundred store failures per
// replica per roll, and hid any real one.
func TestAViewGateRefusalOfAMaintenanceReadIsNamedAndRetried(t *testing.T) {
	f := newMaintenanceTestFixture(t, maintenanceReadySnapshot, maintenanceTime(10, 0), nil)
	catalog := f.m.catalog.(*maintenanceTestCatalog)
	refusals := 2
	f.runner.enter = func() error {
		if refusals > 0 {
			refusals--
			return &scheduler.ViewNotExecutableError{Reason: "timeline_stale"}
		}
		return nil
	}
	f.m.step(context.Background())
	f.m.step(context.Background())
	if stats := f.m.Stats(); stats["view_not_executable"] != 2 || stats["unavailable"] != 0 || catalog.reads != 0 {
		t.Fatalf("stats=%v reads=%d, want two view refusals named as such, no unavailable, no read yet", stats, catalog.reads)
	}
	f.m.step(context.Background())
	if stats := f.m.Stats(); catalog.reads != 1 || stats["view_not_executable"] != 2 {
		t.Fatalf("after the view allows it the Plans were not read: stats=%v reads=%d", stats, catalog.reads)
	}
	f.runner.enter = func() error { return errors.New("store unavailable") }
	f.runner.revision = 2
	f.m.step(context.Background())
	if stats := f.m.Stats(); stats["unavailable"] != 1 {
		t.Fatalf("a store failure was not counted as unavailable: %v", stats)
	}
}
