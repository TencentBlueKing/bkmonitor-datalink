// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"fmt"
	"testing"

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
			if pool := DeriveRedisPoolSize(inputs.CPUBudget); pool <= admitted {
				t.Errorf("pool %d does not clear admitted concurrency %d", pool, admitted)
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
		productionCPU      = 8
		measuredBatchFloor = 64
	)
	for _, inputs := range containerShapes() {
		derived := DeriveScheduler(inputs)
		if derived.ActiveExecutions <= 0 {
			t.Fatalf("%d CPU: active executions = %d, want a bound rather than none",
				inputs.CPUBudget, derived.ActiveExecutions)
		}
		// Below the permit budget the slots cannot keep the permits fed; above
		// the ready queue the queue would bind first and the pair would be
		// describing two different limits.
		if admitted := derived.AdmittedConcurrency(); derived.ActiveExecutions <= admitted {
			t.Fatalf("%d CPU: active executions %d cannot keep %d permits fed",
				inputs.CPUBudget, derived.ActiveExecutions, admitted)
		}
		if derived.ActiveExecutions > derived.ReadyQueueCapacity {
			t.Fatalf("%d CPU: active executions %d exceed the %d ready queue",
				inputs.CPUBudget, derived.ActiveExecutions, derived.ReadyQueueCapacity)
		}
		if inputs.CPUBudget == productionCPU && derived.ActiveExecutions < measuredBatchFloor {
			t.Fatalf("%d CPU: active executions %d are below the measured batch floor of %d",
				inputs.CPUBudget, derived.ActiveExecutions, measuredBatchFloor)
		}
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
