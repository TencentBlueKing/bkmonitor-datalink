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
