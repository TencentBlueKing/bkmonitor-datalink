package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestQueryCooldownDispersesSynchronizedPopulation(t *testing.T) {
	period := time.Minute
	buckets := map[int]int{}
	for i := 0; i < 943; i++ {
		delay := queryCooldownDelay(execution.QueryGroupIdentity(fmt.Sprintf("query-group-%d", i)), period, 3)
		buckets[int(delay/time.Second)]++
	}
	if len(buckets) < 50 {
		t.Fatalf("population remained synchronized: %d seconds occupied", len(buckets))
	}
	for second, n := range buckets {
		if n > 40 {
			t.Fatalf("concentrated probes at second %d: %d", second, n)
		}
	}
}

func unavailableResult() execution.SlotExecutionResult {
	return execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityUnavailable}
}

func TestQueryCooldownAcrossSlotsAndRecovery(t *testing.T) {
	now := time.Unix(100, 0)
	source := &fakeSlotSource{slot: frozenSlot("query-group-1"), facts: SlotDueFacts{IntervalSeconds: 10}}
	source.slot.EarliestQueryDeadlineUnixMilli = 150_000
	executor := &scriptedExecutor{results: []execution.SlotExecutionResult{
		unavailableResult(), unavailableResult(), unavailableResult(),
		{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable},
	}}
	limits := testRecoveryLimits()
	limits.QueryUnavailableCooldown = true
	flights, err := NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: source.slot.Dispatch.OwnerFence}, source, executor, flights, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		source.slot.Contract.DuePlanSetDigest = execution.DuePlanSetDigest(fmt.Sprintf("slot-digest-%d", i))
		source.slot.DuePlanTargets.DuePlanSetDigest = source.slot.Contract.DuePlanSetDigest
		source.slot.Contract.Slot.EvaluationTime = execution.EvaluationTime(100 + i)
		source.slot.ExpectedNextSlot = source.slot.Contract.Slot.EvaluationTime
		if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
			t.Fatalf("round %d: %t %v", i, attempted, err)
		}
	}
	source.slot.Contract.Slot.EvaluationTime++
	source.slot.ExpectedNextSlot++
	source.slot.Contract.DuePlanSetDigest = "next-slot-digest"
	source.slot.DuePlanTargets.DuePlanSetDigest = source.slot.Contract.DuePlanSetDigest
	admissions := 0
	if _, attempted, _, err := runner.RunOneAdmitted(context.Background(), func(execution.Operation) (func(), bool) { admissions++; return func() {}, true }); err != nil || attempted || admissions != 0 {
		t.Fatalf("cooldown acquired execution resources: %t %d %v", attempted, admissions, err)
	}
	bound := runner.DueBound()
	if !bound.QueryCooldown || bound.Deferred || bound.NotDueUntilUnix <= now.Unix() {
		t.Fatalf("cooldown must be publication-invalidatable: %+v", bound)
	}
	if !runner.NextReadyAt().IsZero() {
		t.Fatal("query cooldown blocked configuration revalidation")
	}
	now = runner.queryCooldown.until
	if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
		t.Fatal(attempted, err)
	}
	if runner.queryCooldown.failures != 0 || !runner.queryCooldown.until.IsZero() {
		t.Fatal("valid query did not restore normal dispatch")
	}
}

func TestQueryCooldownDoesNotCountDuplicateOrQueryFreeCompletion(t *testing.T) {
	now := time.Unix(100, 0)
	runner := &Runner{queryGroup: "qg", now: func() time.Time { return now }, flights: &FlightCoordinator{limits: RecoveryLimits{QueryUnavailableCooldown: true}}}
	slot := frozenSlot("qg")
	for i := 0; i < 10; i++ {
		runner.recordQueryAvailability(context.Background(), slot, unavailableResult(), 10)
	}
	if runner.queryCooldown.failures != 1 {
		t.Fatal("duplicate Slot counted")
	}
	slot.Contract.Slot.EvaluationTime++
	runner.recordQueryAvailability(context.Background(), slot, execution.SlotExecutionResult{Completed: true}, 10)
	if runner.queryCooldown.failures != 1 {
		t.Fatal("query-free completion changed evidence")
	}
	runner.queryCooldown.until = now.Add(time.Minute)
	slot.Recovery.Disposition = ReplayExpired
	if runner.deferUnavailableQuery(context.Background(), slot) {
		t.Fatal("expired finalization delayed")
	}
	runner.recordQueryAvailability(context.Background(), slot, execution.SlotExecutionResult{Completed: true, QueryAvailability: execution.QueryAvailabilityAvailable}, 10)
	if runner.queryCooldown.failures != 1 || !runner.queryCooldown.until.Equal(now.Add(time.Minute)) {
		t.Fatal("expired finalization forged recovery")
	}
}

func TestQueryCooldownMaintenanceConfigAndDisable(t *testing.T) {
	now := time.Unix(100, 0)
	runner := &Runner{queryGroup: "qg", now: func() time.Time { return now }, flights: &FlightCoordinator{limits: RecoveryLimits{QueryUnavailableCooldown: true}}}
	slot := frozenSlot("qg")
	for i := 0; i < 3; i++ {
		slot.Contract.Slot.EvaluationTime++
		runner.recordQueryAvailability(context.Background(), slot, unavailableResult(), 60)
	}
	slot.RecoveryUntilUnixMilli = now.Add(5 * time.Second).UnixMilli()
	if !runner.deferUnavailableQuery(context.Background(), slot) || !runner.queryCooldown.wakeAt.Equal(now.Add(5*time.Second)) {
		t.Fatal("cooldown passed maintenance deadline")
	}
	slot.Recovery.RecheckAtUnixMilli = now.Add(time.Second).UnixMilli()
	if !runner.deferUnavailableQuery(context.Background(), slot) || !runner.queryCooldown.wakeAt.Equal(now.Add(time.Second)) {
		t.Fatal("cooldown hid earlier replay-distance change")
	}
	// An unrelated publication is not a query/schedule change.
	slot.Contract.SnapshotRevision = "another-snapshot"
	if !runner.deferUnavailableQuery(context.Background(), slot) {
		t.Fatal("unrelated publication cleared cooldown")
	}
	slot.Contract.QueryRevision = "fixed-query"
	if runner.deferUnavailableQuery(context.Background(), slot) || runner.queryCooldown.failures != 0 {
		t.Fatal("fixed query stayed isolated")
	}
	for i := 0; i < 3; i++ {
		slot.Contract.Slot.EvaluationTime++
		runner.recordQueryAvailability(context.Background(), slot, unavailableResult(), 60)
	}
	runner.flights.limits.QueryUnavailableCooldown = false
	if runner.deferUnavailableQuery(context.Background(), slot) || runner.queryCooldown.failures != 0 {
		t.Fatal("disabled gate retained cooldown")
	}
}

func TestQueryCooldownDelayBoundedForAllPeriods(t *testing.T) {
	for _, period := range []time.Duration{10 * time.Second, time.Minute, 10 * time.Minute} {
		for _, failures := range []uint32{3, 4, 32} {
			got := queryCooldownDelay("qg", period, failures)
			capDelay := 5 * time.Minute
			if 2*period > capDelay {
				capDelay = 2 * period
			}
			if got <= period || got > capDelay {
				t.Fatalf("period=%s failures=%d delay=%s", period, failures, got)
			}
		}
	}
}

func BenchmarkQueryCooldownHealthyGate(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "enabled"
		}
		b.Run(name, func(b *testing.B) {
			runner := &Runner{flights: &FlightCoordinator{limits: RecoveryLimits{QueryUnavailableCooldown: enabled}}, now: time.Now}
			slot := frozenSlot("qg")
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				runner.deferUnavailableQuery(context.Background(), slot)
			}
		})
	}
}
