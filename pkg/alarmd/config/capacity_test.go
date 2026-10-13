// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// containerShapes spans what a deployment or a build agent can hand the
// process: a container far below the intended size, the reference, production,
// and hosts large enough to reach the pool ceiling. Deriving capacity means
// every one of these has to hold together, and a shape that only the build
// agent has is still a shape that must not fail.
func containerShapes() []CapacityInputs {
	return []CapacityInputs{
		{CPUBudget: 1, MemoryLimitBytes: 256 << 20},
		{CPUBudget: 1, MemoryLimitBytes: 512 << 20},
		{CPUBudget: 2, MemoryLimitBytes: 2 << 30},
		{CPUBudget: 4, MemoryLimitBytes: 4 << 30},
		{CPUBudget: 8, MemoryLimitBytes: 8 << 30},
		{CPUBudget: 16, MemoryLimitBytes: 32 << 30},
		{CPUBudget: 64, MemoryLimitBytes: 128 << 30},
		{CPUBudget: 256, MemoryLimitBytes: 512 << 30},
	}
}

// Deriving capacity moved a set of interacting numbers out of a file and into
// a formula. That only removes the failure if the formula cannot produce a
// combination the process rejects - otherwise the same broken combination
// arrives from a container size instead of from an operator.
func TestEveryContainerShapeDerivesARunnableConfiguration(t *testing.T) {
	for _, inputs := range containerShapes() {
		name := fmt.Sprintf("%dCPU_%dMiB", inputs.CPUBudget, inputs.MemoryLimitBytes>>20)
		t.Run(name, func(t *testing.T) {
			cfg := completePhaseTwoProductionConfig(validGoAccessConfigObject().withDerivedCapacity(inputs))
			if err := cfg.Validate(); err != nil {
				t.Fatalf("derived configuration rejected: %v", err)
			}

			storeBatch := uint64(cfg.Limits.Store.MaxKeysPerBatch)
			chunked := storeBatch * execution.StateApplyMaxChunks
			budgets := cfg.PhaseTwo.Coordinator
			// Below one Store call the process cannot apply a single batch, so
			// the smallest Slot the chunked apply is built around could never
			// complete; above the chunked product it would admit a Slot no
			// apply can carry.
			if budgets.MaxStateMutations < storeBatch || budgets.MaxStateMutations > chunked {
				t.Errorf("state mutation budget %d is outside [%d, %d]",
					budgets.MaxStateMutations, storeBatch, chunked)
			}
			if budgets.MaxGapMutations != budgets.MaxStateMutations || budgets.MaxEvents != budgets.MaxStateMutations {
				t.Errorf("budgets disagree: %+v", budgets)
			}
			if budgets.MaxSeries == 0 || budgets.MaxRetainedBytes == 0 || budgets.MaxSequencerReservations <= 0 {
				t.Errorf("empty budget in %+v", budgets)
			}

			scheduler := cfg.PhaseTwo.Scheduler
			if scheduler.ReadyQueueCapacity < scheduler.ProcessQueryPermits ||
				scheduler.RecoveryQueueCapacity < scheduler.RecoveryQueryPermits {
				t.Errorf("queues cannot feed the permits: %+v", scheduler)
			}
			// The pool is what the block profile found queueing, and queueing
			// for a connection must never be what gates execution.
			admitted := cfg.AdmittedQueryConcurrency()
			if pool := DeriveRedisPoolSize(inputs); pool <= admitted || pool <= cfg.PhaseTwo.Scheduler.ActiveExecutionLimit {
				t.Errorf("pool %d does not clear admitted concurrency %d and the %d execution slots", pool, admitted,
					cfg.PhaseTwo.Scheduler.ActiveExecutionLimit)
			}
		})
	}
}

// Larger containers get more, and the growth follows the resource that bounds
// each number rather than an unrelated one.
func TestCapacityFollowsTheResourceThatBoundsIt(t *testing.T) {
	small := Default().withDerivedCapacity(CapacityInputs{CPUBudget: 4, MemoryLimitBytes: 4 << 30})
	large := Default().withDerivedCapacity(CapacityInputs{CPUBudget: 8, MemoryLimitBytes: 8 << 30})

	if large.PhaseTwo.Coordinator.MaxRetainedBytes != 2*small.PhaseTwo.Coordinator.MaxRetainedBytes {
		t.Errorf("retained bytes %d and %d do not follow the memory limit",
			small.PhaseTwo.Coordinator.MaxRetainedBytes, large.PhaseTwo.Coordinator.MaxRetainedBytes)
	}
	if large.PhaseTwo.Scheduler.ProcessQueryPermits != 2*small.PhaseTwo.Scheduler.ProcessQueryPermits {
		t.Errorf("query permits %d and %d do not follow the CPU budget",
			small.PhaseTwo.Scheduler.ProcessQueryPermits, large.PhaseTwo.Scheduler.ProcessQueryPermits)
	}
	// More memory must not buy a bigger single query: the response bound is a
	// protocol decision, not a resource one.
	if small.Limits.Store.MaxKeysPerBatch != large.Limits.Store.MaxKeysPerBatch {
		t.Errorf("the Store call bound moved with the container: %d and %d",
			small.Limits.Store.MaxKeysPerBatch, large.Limits.Store.MaxKeysPerBatch)
	}
}

// Default is a product statement, so it must read the same on a two-core build
// agent and a sixty-four-core one. Deriving it from the host would make every
// expectation in this package a property of the machine that ran it - which is
// how a green local suite reached a red build.
func TestDefaultDescribesTheReferenceContainerNotTheHost(t *testing.T) {
	reference := Default().withDerivedCapacity(ReferenceContainer())
	got := Default()

	if got.PhaseTwo.Scheduler != reference.PhaseTwo.Scheduler ||
		got.PhaseTwo.Coordinator != reference.PhaseTwo.Coordinator {
		t.Fatalf("Default() capacity = %+v / %+v, want the reference container's",
			got.PhaseTwo.Scheduler, got.PhaseTwo.Coordinator)
	}
	if got.PhaseTwo.Coordinator.MaxRetainedBytes == 0 {
		t.Fatal("the reference container derives an empty retained budget")
	}
}

// The Schedule timeline cache replaced a written 32 MiB with a share of the
// container. The share only means anything if it holds the Query Groups one
// Worker owns, so the check is stated in those terms: production measured
// 146,816 bytes per timeline and 931 owned Query Groups on an 8 CPU, 8 GiB
// container, and the replaced constant held about a quarter of them.
func TestControlTimelineCacheBudgetHoldsAnOwnedWorkingSet(t *testing.T) {
	const (
		productionTimelineBytes = 146816
		// The decoded object, charged the way controlplane charges a cache
		// entry. The persisted bytes are not held.
		productionEntryBytes = productionTimelineBytes * 3 / 2
		productionOwned      = 931
		replacedConstant     = 32 << 20
	)
	if replacedConstant/productionEntryBytes >= productionOwned {
		t.Fatal("the replaced constant already held the owned working set; the evidence for this change is wrong")
	}
	production := DeriveControlTimelineCache(CapacityInputs{CPUBudget: 8, MemoryLimitBytes: 8 << 30})
	if held := production.MaxBytes / productionEntryBytes; held < productionOwned {
		t.Fatalf("8 GiB container holds %d timelines, below the %d Query Groups one Worker owns", held, productionOwned)
	}
	for _, inputs := range containerShapes() {
		derived := DeriveControlTimelineCache(inputs)
		if derived.MaxBytes <= 0 || derived.MaxEntries <= 0 {
			t.Fatalf("%d MiB container derived %+v", inputs.MemoryLimitBytes>>20, derived)
		}
		if uint64(derived.MaxBytes) >= inputs.MemoryLimitBytes {
			t.Fatalf("%d MiB container gave the cache %d bytes", inputs.MemoryLimitBytes>>20, derived.MaxBytes)
		}
		// The entry bound comes from the same budget, so it can never be the
		// one that binds first for a timeline worth caching.
		if derived.MaxEntries*controlTimelineCacheMinEntryBytes < derived.MaxBytes {
			t.Fatalf("%d MiB container: entry bound %d is tighter than its byte bound %d",
				inputs.MemoryLimitBytes>>20, derived.MaxEntries, derived.MaxBytes)
		}
	}
}

// The compile cache replaced a written 4096 entries and 64 MiB with a share of
// the container. The leader compiles every Plan of the deployment on each
// catalog build, a cycle that an LRU smaller than it misses on every access,
// so the check is that the share holds that cycle with room to grow: a production
// leader measured 35 MB of compiled Plans for 2,930 Plans on 8 GiB.
func TestCompileCacheBudgetHoldsTheLeadersCycle(t *testing.T) {
	const (
		productionPlans     = 2930
		productionPlanBytes = 35_000_000 / productionPlans
		replacedEntries     = 4096
		// The deployment size at which the replaced entry bound would have
		// recompiled every Plan on every build.
		grownPlans = 2 * replacedEntries
	)
	limits := Default().Limits.Compiler
	production := DeriveCompileCache(CapacityInputs{CPUBudget: 8, MemoryLimitBytes: 8 << 30}, limits.MaxCompiledPlanBytes)
	if held := production.MaxBytes / productionPlanBytes; held < grownPlans {
		t.Fatalf("8 GiB container holds %d compiled Plans, below %d", held, grownPlans)
	}
	if production.MaxEntries < grownPlans {
		t.Fatalf("8 GiB container's entry bound %d is below %d Plans", production.MaxEntries, grownPlans)
	}
	// The last shape's share is below one maximum compiled Plan, so the floor
	// is what sizes it.
	shapes := append(containerShapes(), CapacityInputs{CPUBudget: 1, MemoryLimitBytes: 8 << 20})
	for _, inputs := range shapes {
		derived := DeriveCompileCache(inputs, limits.MaxCompiledPlanBytes)
		if derived.MaxBytes < limits.MaxCompiledPlanBytes {
			t.Fatalf("%d MiB container: cache of %d bytes cannot admit one %d-byte compiled Plan",
				inputs.MemoryLimitBytes>>20, derived.MaxBytes, limits.MaxCompiledPlanBytes)
		}
		if uint64(derived.MaxBytes) > inputs.MemoryLimitBytes/compileCacheMemoryDivisor &&
			derived.MaxBytes != limits.MaxCompiledPlanBytes {
			t.Fatalf("%d MiB container gave the cache %d bytes, above its share",
				inputs.MemoryLimitBytes>>20, derived.MaxBytes)
		}
		if derived.MaxEntries*compileCacheMinEntryBytes < derived.MaxBytes {
			t.Fatalf("%d MiB container: entry bound %d is tighter than its byte bound %d",
				inputs.MemoryLimitBytes>>20, derived.MaxEntries, derived.MaxBytes)
		}
		cfg := Default().withDerivedCapacity(inputs)
		if cfg.Limits.Compiler.MaxCacheBytes != derived.MaxBytes || cfg.Limits.Compiler.MaxCacheEntries != derived.MaxEntries {
			t.Fatalf("%d MiB container configured the compiler cache %d entries, %d bytes; derived %+v",
				inputs.MemoryLimitBytes>>20, cfg.Limits.Compiler.MaxCacheEntries, cfg.Limits.Compiler.MaxCacheBytes, derived)
		}
	}
}

// The dispatcher bound has to be derived, not left at a value that means "no
// bound". Production ran unbounded and peaked at 452 outstanding Runner
// invocations against 32 query permits, so the 420 in between were prepared
// executions parked on a semaphore rather than work in progress.
//
// The floor below is the measurement that decides the multiple. One Worker's
// batch has 30 seconds between the ready delay and the completion deadline,
// and 1,207 execute-seconds land in it. The executions running longer than 15
// seconds - 11.8 a minute at about 39 seconds each - are resident occupancy
// rather than batch work, so they take roughly 7.6 slots out and 368 seconds
// of work with them. List scheduling then needs
// 839/(N-7.6) <= 30-15, which is N >= 63.5. Four per permit clears that on
// every shape while staying inside the ready queue.
func TestActiveExecutionsClearTheMeasuredBatchFloor(t *testing.T) {
	const (
		productionMemory   = 8 << 30
		measuredBatchFloor = 64
	)
	for _, inputs := range containerShapes() {
		derived := DeriveScheduler(inputs)
		if derived.ActiveExecutions <= 0 || derived.ActiveExecutions != derived.ActiveExecutionCeiling {
			t.Fatalf("%d MiB: active executions = %d of a %d ceiling, want the ceiling until the work is known",
				inputs.MemoryLimitBytes>>20, derived.ActiveExecutions, derived.ActiveExecutionCeiling)
		}
		// The slots have to be able to feed every permit, and the ready queue
		// has to be able to feed every slot.
		if admitted := derived.AdmittedConcurrency(); derived.ActiveExecutions <= admitted {
			t.Fatalf("%d MiB: active executions %d cannot keep %d permits fed",
				inputs.MemoryLimitBytes>>20, derived.ActiveExecutions, admitted)
		}
		if derived.ActiveExecutions > derived.ReadyQueueCapacity {
			t.Fatalf("%d MiB: active executions %d exceed the %d ready queue",
				inputs.MemoryLimitBytes>>20, derived.ActiveExecutions, derived.ReadyQueueCapacity)
		}
		if inputs.MemoryLimitBytes == productionMemory && derived.ActiveExecutionCeiling < measuredBatchFloor {
			t.Fatalf("%d MiB: an execution ceiling of %d is below the measured batch floor of %d",
				inputs.MemoryLimitBytes>>20, derived.ActiveExecutionCeiling, measuredBatchFloor)
		}
	}
}

// No scheduler bound reads the CPU. A query permit and an execution slot are
// held while the query service and Redis answer, which no core spends; two
// containers that differ only in cores get the same shape and the same pool.
func TestTheSchedulerShapeDoesNotReadTheCPU(t *testing.T) {
	for _, memory := range []uint64{512 << 20, 4 << 30, 32 << 30} {
		few := CapacityInputs{CPUBudget: 1, MemoryLimitBytes: memory}
		many := CapacityInputs{CPUBudget: 64, MemoryLimitBytes: memory}
		if DeriveScheduler(few) != DeriveScheduler(many) || DeriveRedisPoolSize(few) != DeriveRedisPoolSize(many) {
			t.Fatalf("%d MiB: 1 core %+v pool %d, 64 cores %+v pool %d", memory>>20,
				DeriveScheduler(few), DeriveRedisPoolSize(few), DeriveScheduler(many), DeriveRedisPoolSize(many))
		}
	}
	// The ceiling is the runtime's memory beyond the retained pool over what
	// one parked execution holds: a 4 GiB container has 1 GiB of it.
	if got := DeriveScheduler(CapacityInputs{MemoryLimitBytes: 4 << 30}).ActiveExecutionCeiling; got != (1<<30)/bytesPerParkedExecution {
		t.Fatalf("4 GiB execution ceiling = %d, want %d", got, (1<<30)/bytesPerParkedExecution)
	}
}

// The bounds follow the work: each is the demand's rate times what one unit
// holds it for, twice over, inside the container's ceiling. The figures are a
// production replica's: 58 queries ready in its busiest second, a 90 ms
// answer, half a replay a second, 15 Slots ready in a second running 1.5 s,
// 700 owned Query Groups.
func TestTheSchedulerShapeFollowsTheDemand(t *testing.T) {
	ceiling := DeriveScheduler(CapacityInputs{MemoryLimitBytes: 4 << 30})
	demand := SchedulerDemand{PeakQueriesPerSecond: 58, ReplayQueriesPerSecond: 0.5, PeakSlotsPerSecond: 15,
		Hold: 90 * time.Millisecond, Execution: 1500 * time.Millisecond, OwnedQueryGroups: 700, MaxQueuedItemsPerQG: 16}
	got := DeriveSchedulerShape(ceiling, demand, DerivedScheduler{}, 0)
	want := DerivedScheduler{ProcessQueryPermits: 11, RecoveryQueryPermits: 1, ActiveExecutions: ceiling.ActiveExecutionCeiling,
		ReadyQueueCapacity: 700*16 + 1, RecoveryQueueCapacity: 700*16 + 1, ActiveExecutionCeiling: ceiling.ActiveExecutionCeiling}
	if got != want {
		t.Fatalf("shape %+v, want %+v", got, want)
	}
	// A slower service does not raise the budget: the hold is the caller's
	// lowest hourly median, and the same demand at the same hold is the same
	// shape however slow the last answers were.
	if again := DeriveSchedulerShape(ceiling, demand, DerivedScheduler{}, 0); again != got {
		t.Fatalf("the same demand derived %+v and then %+v", got, again)
	}
}

// Both sides of every bound: no demand still leaves the smallest shape that
// runs - one recovery permit, one process permit above it, an execution for
// each and one more, a queue one deeper than a Query Group may queue - and a
// demand past
// the container's memory is held at its ceiling, permits never above the
// slots that feed them.
func TestTheSchedulerShapeStaysInsideItsBounds(t *testing.T) {
	ceiling := DeriveScheduler(CapacityInputs{MemoryLimitBytes: 4 << 30})
	floor := DeriveSchedulerShape(ceiling, SchedulerDemand{MaxQueuedItemsPerQG: 16}, DerivedScheduler{}, 0)
	if floor.RecoveryQueryPermits != 1 || floor.ProcessQueryPermits != 2 || floor.ActiveExecutions != ceiling.ActiveExecutionCeiling || floor.ReadyQueueCapacity != 17 {
		t.Fatalf("no demand from nothing derived %+v, want 1/2, the slot ceiling and a queue of 17", floor)
	}
	flood := DeriveSchedulerShape(ceiling, SchedulerDemand{PeakQueriesPerSecond: 1e9, ReplayQueriesPerSecond: 1e9,
		PeakSlotsPerSecond: 1e9, Hold: time.Second, Execution: time.Second, OwnedQueryGroups: 10, MaxQueuedItemsPerQG: 16}, DerivedScheduler{}, 0)
	top := ceiling.ActiveExecutionCeiling
	if flood.ActiveExecutions != top || flood.RecoveryQueryPermits+flood.ProcessQueryPermits != top-1 {
		t.Fatalf("a flood derived %+v against a ceiling of %d, want every slot and all but one permit at it", flood, top)
	}
	// What the coordinator requires of any shape it runs under.
	for _, shape := range []DerivedScheduler{floor, flood} {
		if shape.RecoveryQueryPermits < 1 || shape.RecoveryQueryPermits >= shape.ProcessQueryPermits ||
			shape.ActiveExecutions <= shape.ProcessQueryPermits+shape.RecoveryQueryPermits ||
			shape.ReadyQueueCapacity <= 16 || shape.RecoveryQueueCapacity <= 16 {
			t.Fatalf("derived %+v, a shape the coordinator refuses", shape)
		}
	}
	// Until a hold is measured a query is taken to hold its permit for half a
	// second.
	cold := DeriveSchedulerShape(ceiling, SchedulerDemand{PeakQueriesPerSecond: 10, MaxQueuedItemsPerQG: 16}, DerivedScheduler{}, 0)
	if cold.ProcessQueryPermits != 10 {
		t.Fatalf("10 queries a second with no measured hold derived %d permits, want 10", cold.ProcessQueryPermits)
	}
}

// The collector's budget is the other half of the same idea: a container states
// its memory and everything the process does inside it follows. Production ran
// with neither setting, so next_gc sat at exactly twice a 430 MiB live heap,
// a collection ran every 0.65 seconds and the collector took 17.7% of the
// process while 87% of the container's 8 GiB went unused.
func TestGoRuntimeBudgetFollowsTheContainerAndClearsItsOwnCeilings(t *testing.T) {
	for _, inputs := range containerShapes() {
		inputs.MemorySource = "pod_limit"
		derived := DeriveGoRuntime(inputs)
		if !derived.Applied || derived.MemoryLimitBytes <= 0 || derived.GCPercent <= 100 {
			t.Fatalf("%d MiB container: go runtime budget = %+v, want an applied limit and a target above the Go default",
				inputs.MemoryLimitBytes>>20, derived)
		}
		// A soft limit at or below a ceiling the process already derives from
		// the same number would put a Slot admitted at its cap straight into
		// the collector's limit.
		coordinator := DeriveCoordinator(inputs, 1, 0)
		if uint64(derived.MemoryLimitBytes) <= coordinator.MaxRetainedBytes {
			t.Fatalf("%d MiB container: soft limit %d does not clear the %d retained ceiling",
				inputs.MemoryLimitBytes>>20, derived.MemoryLimitBytes, coordinator.MaxRetainedBytes)
		}
		if uint64(derived.MemoryLimitBytes) <= uint64(DeriveControlTimelineCache(inputs).MaxBytes) {
			t.Fatalf("%d MiB container: soft limit %d does not clear the timeline cache budget",
				inputs.MemoryLimitBytes>>20, derived.MemoryLimitBytes)
		}
		// And it has to leave the container enough room that the runtime's own
		// collector CPU limiter can overshoot into the remainder instead of the
		// kernel reclaiming the process.
		if uint64(derived.MemoryLimitBytes) > inputs.MemoryLimitBytes/2 {
			t.Fatalf("%d MiB container: soft limit %d leaves no reserve",
				inputs.MemoryLimitBytes>>20, derived.MemoryLimitBytes)
		}
	}
}

// A limit nobody stated must not become a limit the collector enforces. The
// fallback describes a machine no deployment measured, so budgets that only
// refuse work above a ceiling may use it and settings that make the runtime
// act on a number may not.
func TestGoRuntimeBudgetRefusesAGuessedContainer(t *testing.T) {
	guessed := CapacityInputs{CPUBudget: 8, MemoryLimitBytes: fallbackMemoryLimitBytes, MemorySource: memorySourceFallback}
	if derived := DeriveGoRuntime(guessed); derived.Applied ||
		derived.MemoryLimitBytes != 0 || derived.GCPercent != 0 {
		t.Fatalf("go runtime budget from a fallback limit = %+v, want none", derived)
	}
	if derived := DeriveGoRuntime(CapacityInputs{CPUBudget: 8, MemoryLimitBytes: 8 << 30}); derived.Applied {
		t.Fatalf("go runtime budget from an unnamed memory source = %+v, want none", derived)
	}
}

// The mutation guardrail does not come from memory any more, and the only way
// to state that is to show it not moving when memory does.
//
// It used to be retained bytes divided by an assumed 32 KiB per mutation, so a
// container with twice the memory admitted twice the mutations. Measuring what
// a mutation actually costs found 3.5 KiB for a light shape against 28.9 KiB
// for a heavy one - a 9x spread around that one constant - which made the
// count, not the byte budget, the thing deciding throughput, and made it decide
// differently for shapes that use the same memory.
//
// The bound that reads "between one Store call and the chunked product" is
// satisfied by both the old derivation and the new one, so it cannot say which
// is in force. This can: every container shape gets the same guardrail while
// the byte budget it used to be derived from keeps tracking the container.
func TestTheMutationGuardrailIsNotDerivedFromMemory(t *testing.T) {
	shapes := containerShapes()
	if len(shapes) < 2 {
		t.Fatal("this case needs two container shapes to compare")
	}
	var guardrail uint64
	retainedSeen := make(map[uint64]struct{}, len(shapes))
	for _, inputs := range shapes {
		cfg := completePhaseTwoProductionConfig(validGoAccessConfigObject().withDerivedCapacity(inputs))
		budgets := cfg.PhaseTwo.Coordinator
		chunked := uint64(cfg.Limits.Store.MaxKeysPerBatch) * execution.StateApplyMaxChunks
		if budgets.MaxStateMutations != chunked {
			t.Fatalf("%d MiB: state guardrail %d, want the chunked apply product %d",
				inputs.MemoryLimitBytes>>20, budgets.MaxStateMutations, chunked)
		}
		if guardrail == 0 {
			guardrail = budgets.MaxStateMutations
		} else if budgets.MaxStateMutations != guardrail {
			t.Fatalf("%d MiB: state guardrail %d, want the same %d every shape derives; "+
				"a guardrail that moves with the container is still a memory budget",
				inputs.MemoryLimitBytes>>20, budgets.MaxStateMutations, guardrail)
		}
		retainedSeen[budgets.MaxRetainedBytes] = struct{}{}
	}
	// The other half: the working limit does still follow the container, so the
	// case above is about where the count comes from rather than about a
	// derivation that stopped reading its inputs altogether.
	if len(retainedSeen) < 2 {
		t.Fatalf("retained byte budgets %v, want them to differ across container shapes: "+
			"that is the budget that is supposed to track memory", retainedSeen)
	}
}

// The compile-time point ceiling comes from the representation the store
// writes, and under the framed record the configured limit is the binding one.
//
// The original finding was a gap the other way: max_required_history_points is
// 4096, the JSON envelope ran out around 2100 points for one Level, and nothing
// anywhere held the smaller number - so a Plan configured between them compiled
// cleanly and then had every state write refused, per series, with nothing to
// say why. Deriving the ceiling here rather than writing it beside the
// configured one is what let it follow the representation when the store
// started writing framed records, which is what closed the gap.
//
// So the assertion is now that 4096 is reachable at every Level count a Plan
// may compile. It is not a formality: if a later representation change put the
// ceiling back under the configured limit, the silent per-series refusal comes
// back exactly as it was, and this is the case that would say so.
func TestTheRetainedPointCeilingComesFromTheRepresentation(t *testing.T) {
	cfg := completePhaseTwoProductionConfig(validGoAccessConfigObject().withDerivedCapacity(containerShapes()[0]))
	limits := cfg.CompilerLimits()
	if len(limits.MaxRetainedPointsByLevels) <= limits.MaxLevelsPerPlan {
		t.Fatalf("ceilings %v do not cover every Level count a Plan may compile, including the extra "+
			"Level a no-data Plan adds", limits.MaxRetainedPointsByLevels)
	}
	single := limits.MaxRetainedPointsByLevels[1]
	if single == 0 {
		t.Fatal("no ceiling derived for a single-Level Plan")
	}
	// Every Level count, not just one: the binding ceiling is the lowest, which
	// is the most crowded record, and a case that only reads the single-Level
	// number would pass while a Plan at the configured limit was refused for
	// having more Levels than the case looked at.
	for count := 1; count < len(limits.MaxRetainedPointsByLevels); count++ {
		ceiling := limits.MaxRetainedPointsByLevels[count]
		if ceiling == 0 {
			continue
		}
		if ceiling < limits.MaxRequiredHistoryPoints {
			t.Fatalf("ceiling for %d Levels is %d, below the configured %d: a Plan asking for the limit it "+
				"is allowed to ask for would compile and then have every state write refused per series",
				count, ceiling, limits.MaxRequiredHistoryPoints)
		}
	}
	// More Levels share one record, so each one leaves room for fewer points.
	for count := 2; count < len(limits.MaxRetainedPointsByLevels); count++ {
		previous, current := limits.MaxRetainedPointsByLevels[count-1], limits.MaxRetainedPointsByLevels[count]
		if current == 0 || previous == 0 {
			continue
		}
		if current >= previous {
			t.Fatalf("ceiling for %d Levels (%d) is not below the one for %d (%d); every Level writes its "+
				"facts on every point, so it has to fall", count, current, count-1, previous)
		}
	}
	// The strategy behind decision-019 retains 1469 points on one Level, and
	// this must not stop it: a bound that refuses what runs today is a worse
	// failure than the one it prevents.
	if single < 1469 {
		t.Fatalf("single-Level ceiling %d would refuse a 1469-point window that stores cleanly today", single)
	}
}

// The series count is a guardrail implied by the bytes, not a budget beside
// them: at the smallest a series can encode, the count allows every series
// the retained bytes can hold, so a Slot of light series meets the byte bound
// and never the count. The old divisor of 4 KiB turned such a Slot away by
// count with most of its bytes free.
func TestTheSeriesCountNeverBindsBeforeTheBytes(t *testing.T) {
	for _, inputs := range containerShapes() {
		budgets := DeriveCoordinator(inputs, 1, 1)
		if budgets.MaxSeries != max(budgets.MaxRetainedBytes/MinimumSeriesBytes, 1) {
			t.Fatalf("%d MiB: series guardrail %d for %d retained bytes", inputs.MemoryLimitBytes>>20, budgets.MaxSeries, budgets.MaxRetainedBytes)
		}
		// A Slot of 200-byte series that fills the retained bytes is within
		// the count.
		if light := budgets.MaxRetainedBytes / 200; light > budgets.MaxSeries {
			t.Fatalf("%d MiB: %d series of 200 bytes fit the bytes but not the count %d", inputs.MemoryLimitBytes>>20, light, budgets.MaxSeries)
		}
	}
}

// Execution slots are the memory ceiling, whatever the demand: they bound
// how many executions can be parked in memory at once, which is what the
// ceiling measures, and an execution slot is a goroutine waiting on Redis or
// the query service. Sized from demand they hid the demand: fewer slots ran
// fewer Slots, which asked for fewer permits, which read as less demand.
func TestExecutionSlotsStayAtTheMemoryCeiling(t *testing.T) {
	ceiling := DeriveScheduler(CapacityInputs{MemoryLimitBytes: 4 << 30})
	for _, demand := range []SchedulerDemand{
		{MaxQueuedItemsPerQG: 16},
		{PeakQueriesPerSecond: 60, PeakSlotsPerSecond: 580, Hold: 54 * time.Millisecond, Execution: 4 * time.Millisecond, MaxQueuedItemsPerQG: 16},
	} {
		if got := DeriveSchedulerShape(ceiling, demand, DerivedScheduler{}, 0).ActiveExecutions; got != ceiling.ActiveExecutionCeiling {
			t.Fatalf("demand %+v derived %d execution slots, want the %d ceiling", demand, got, ceiling.ActiveExecutionCeiling)
		}
	}
}

// The permit budget comes down by at most half a derivation and never below
// the permits held now: a minute that measured little - a replica just
// started, a lull - does not take the budget from a full one to its floor in
// one step, and lowering never takes back what is running.
func TestThePermitBudgetComesDownByHalvesAndNotBelowWhatIsHeld(t *testing.T) {
	ceiling := DeriveScheduler(CapacityInputs{MemoryLimitBytes: 4 << 30})
	previous := ceiling
	previous.ProcessQueryPermits = 64
	idle := SchedulerDemand{Hold: 50 * time.Millisecond, MaxQueuedItemsPerQG: 16}
	if got := DeriveSchedulerShape(ceiling, idle, previous, 0).ProcessQueryPermits; got != 32 {
		t.Fatalf("an idle minute after 64 permits derived %d, want 32", got)
	}
	if got := DeriveSchedulerShape(ceiling, idle, previous, 40).ProcessQueryPermits; got != 40 {
		t.Fatalf("with 40 permits held an idle minute derived %d, want 40", got)
	}
	busy := idle
	busy.PeakQueriesPerSecond = 1000
	if got := DeriveSchedulerShape(ceiling, busy, previous, 0).ProcessQueryPermits; got != 100 {
		t.Fatalf("1000 queries a second at 50 ms derived %d permits, want 100 at once", got)
	}
	// The recovery permits come down the same way.
	previous.RecoveryQueryPermits = 16
	if got := DeriveSchedulerShape(ceiling, idle, previous, 0).RecoveryQueryPermits; got != 8 {
		t.Fatalf("an idle minute after 16 recovery permits derived %d, want 8", got)
	}
}
