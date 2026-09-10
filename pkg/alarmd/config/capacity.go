// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package config

import (
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// MemoryLimitEnvironment carries the container's memory limit. The deployment
// injects it from the Pod's own resource limits, because a preflight that runs
// outside the Pod cannot read the cgroup the Pod will get and would otherwise
// derive a table the Pod never uses.
const MemoryLimitEnvironment = "ALARMD_MEMORY_LIMIT_BYTES"

// Capacity is not an operating interface. A deployment supplies the container
// it wants - CPU and memory - and every budget the process needs to size from
// that follows: query permits, queue depth, series, retained bytes, mutation
// and event budgets. Writing them into a values file made an operator
// responsible for numbers that constrain one another, and one combination that
// no Pod could run was published exactly that way.
//
// These constants are anchored to what production runs today on 8 CPU and
// 8 GiB, so deriving them replaces the written numbers with the same numbers.
// What they do not yet carry is measured scaling to other container shapes;
// that calibration changes the formulas below and no interface.
const (
	retainedMemoryDivisor    = 4
	bytesPerSeries           = 4 << 10
	bytesPerMutation         = 32 << 10
	queryPermitsPerCPU       = 4
	recoveryPermitDivisor    = 4
	queueDepthPerQueryPermit = 32

	controlTimelineCacheMemoryDivisor = 16
	// The smallest Schedule timeline that can exist - one Segment carrying one
	// Plan - is charged over 1.7 KiB, so no real entry is smaller than this.
	controlTimelineCacheMinEntryBytes = 1 << 10

	// Only local runs and unit tests reach this: a container states its limit
	// through the environment, and a host cgroup states it in the files below.
	fallbackMemoryLimitBytes = 2 << 30
)

// CapacityInputs is what the container gives the process. Both values travel
// into the startup facts so a derived budget can always be traced back to the
// container it came from, including when the source was a fallback.
type CapacityInputs struct {
	CPUBudget        int
	MemoryLimitBytes uint64
	MemorySource     string
}

// ReferenceContainer is the container the product defaults describe. Default
// has to be the same configuration on every machine - a build agent's core
// count is not a product decision - so it derives from this fixed shape, and
// only Load derives from the container the process was actually given.
func ReferenceContainer() CapacityInputs {
	return CapacityInputs{CPUBudget: 2, MemoryLimitBytes: 2 << 30, MemorySource: "product_reference"}
}

var (
	memoryLimitOnce   sync.Once
	memoryLimitBytes  uint64
	memoryLimitSource string
)

// DetectCapacityInputs reads the container's budgets. GOMAXPROCS is already
// the container's CPU quota by the time configuration is read, provided the
// CPU quota was resolved first.
func DetectCapacityInputs() CapacityInputs {
	memoryLimitOnce.Do(func() {
		memoryLimitBytes, memoryLimitSource = detectMemoryLimit()
	})
	return CapacityInputs{
		CPUBudget:        runtime.GOMAXPROCS(0),
		MemoryLimitBytes: memoryLimitBytes,
		MemorySource:     memoryLimitSource,
	}
}

func detectMemoryLimit() (uint64, string) {
	if raw, ok := os.LookupEnv(MemoryLimitEnvironment); ok {
		if limit, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64); err == nil && limit > 0 {
			return limit, "pod_limit"
		}
	}
	// A cgroup states "no limit" in its own way in each version: v2 writes the
	// word, v1 writes a number so large it cannot be a real limit.
	if raw, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		text := strings.TrimSpace(string(raw))
		if text != "max" {
			if limit, err := strconv.ParseUint(text, 10, 64); err == nil && limit > 0 {
				return limit, "cgroup_v2"
			}
		}
	}
	if raw, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
		if limit, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil &&
			limit > 0 && limit < 1<<62 {
			return limit, "cgroup_v1"
		}
	}
	return fallbackMemoryLimitBytes, "fallback_default"
}

// DerivedScheduler is the query admission and queueing shape for one container.
type DerivedScheduler struct {
	ProcessQueryPermits   int
	RecoveryQueryPermits  int
	ReadyQueueCapacity    int
	RecoveryQueueCapacity int
}

// DeriveScheduler sizes admission from the CPU budget. Queries spend most of
// their time waiting on the downstream, so the permit count is a multiple of
// the budget rather than equal to it; the queues only hold references, so they
// are sized to keep a full permit set fed rather than to bound memory.
func DeriveScheduler(inputs CapacityInputs) DerivedScheduler {
	cpu := max(inputs.CPUBudget, 1)
	permits := cpu * queryPermitsPerCPU
	queue := permits * queueDepthPerQueryPermit
	return DerivedScheduler{
		ProcessQueryPermits:   permits,
		RecoveryQueryPermits:  max(permits/recoveryPermitDivisor, 1),
		ReadyQueueCapacity:    queue,
		RecoveryQueueCapacity: queue,
	}
}

// AdmittedConcurrency is how many queries this container may have in flight.
func (d DerivedScheduler) AdmittedConcurrency() int {
	return d.ProcessQueryPermits + d.RecoveryQueryPermits
}

// DerivedControlTimelineCache bounds the control plane's Schedule timeline
// cache for one container.
type DerivedControlTimelineCache struct {
	MaxEntries int
	MaxBytes   int
}

// DeriveControlTimelineCache sizes the Schedule timeline cache from the memory
// limit. The cache holds one timeline per Query Group the Worker owns, and a
// hit is the difference between reusing a decoded Schedule and decoding plus
// re-validating it: a production profile spent 58% of the process on the
// second and 0.26% on the Redis read the cache was actually saving.
//
// The number this replaces was a written 32 MiB, the same constant on every
// container. Production shows what that bought: over an hour the cache
// reported no refreshes at all - the version header never moved - and still
// missed 41% of lookups, so every one of those misses was a timeline the byte
// bound had evicted. At the observed 143 KiB per timeline the bound held about
// 230 of the 931 Query Groups that Worker owned.
//
// An entry holds the decoded object alone - the persisted bytes are read live
// by the three publication paths that need them and kept by nobody - which
// measures at 9/8 of the payload, about 161 KiB at that shape. One sixteenth
// of the container makes the owned set fit several times over: 512 MiB on
// 8 GiB holds about 3,250 such timelines against the 931 owned, and 153 MiB is
// what those 931 actually occupy. The ceiling is deliberately well above the
// residency, because it is a ceiling: the cache only ever grows to the working
// set, and a bound that tracks the container leaves room for a Worker that
// takes on more Query Groups without conceding more than a sixteenth of the
// process to a read cache.
//
// The entry bound is derived from the same budget so the two can never
// disagree: it is how many entries the byte budget could hold if every
// timeline were the smallest one that can exist. The byte bound is what binds
// in practice, which is exactly what the replaced pair of constants got wrong -
// its 4096 entries were never reached.
func DeriveControlTimelineCache(inputs CapacityInputs) DerivedControlTimelineCache {
	budget := max(inputs.MemoryLimitBytes/controlTimelineCacheMemoryDivisor, controlTimelineCacheMinEntryBytes)
	if budget > uint64(math.MaxInt) {
		budget = uint64(math.MaxInt)
	}
	return DerivedControlTimelineCache{
		MaxEntries: max(int(budget/controlTimelineCacheMinEntryBytes), 1),
		MaxBytes:   int(budget),
	}
}

// DeriveCoordinator sizes the process budgets from the memory limit. Retained
// bytes take a quarter of it, leaving the rest for the Go heap's own overhead,
// non-retained allocation and collection headroom; the remaining budgets are
// that retained figure divided by what one series or one mutation costs.
//
// The mutation, event and reservation budgets are additionally held at what a
// chunked Store apply can carry. A process budget above that product would
// admit a Slot no apply could ever complete, which is the cross-check a
// hand-written combination once failed.
// The budget is additionally floored at one Store call. A process that cannot
// hold a single batch's worth of mutations cannot complete the smallest Slot
// the chunked apply is built around, so a very small container runs with less
// headroom rather than with a budget no apply can use.
func DeriveCoordinator(inputs CapacityInputs, storeItemsPerBatch, chunkedApplyBudget uint64) PhaseTwoCoordinatorConfig {
	retained := max(inputs.MemoryLimitBytes/retainedMemoryDivisor, uint64(bytesPerMutation))
	mutations := max(retained/bytesPerMutation, storeItemsPerBatch, 1)
	if chunkedApplyBudget > 0 && mutations > chunkedApplyBudget {
		mutations = chunkedApplyBudget
	}
	return PhaseTwoCoordinatorConfig{
		MaxRetainedBytes:         retained,
		MaxSeries:                max(retained/bytesPerSeries, 1),
		MaxStateMutations:        mutations,
		MaxGapMutations:          mutations,
		MaxEvents:                mutations,
		MaxSequencerReservations: int(mutations),
	}
}
