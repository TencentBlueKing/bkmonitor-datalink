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

// memorySourceFallback names the one memory source that is not a container's
// own statement. Budgets sized against it describe a guess, so the settings
// that make the runtime enforce a number - rather than merely reject work
// above one - refuse to act on it.
const memorySourceFallback = "fallback_default"

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

	activeExecutionsPerQueryPermit = 4

	// The Go runtime's own budgets are derived from the same container limit.
	// The soft memory limit takes half of it; the collector target crosses over
	// to that limit once the live heap reaches an eighth of it.
	goMemoryLimitDivisor = 2
	goGCLiveHeapDivisor  = 8

	controlTimelineCacheMemoryDivisor = 16
	// The smallest Schedule timeline that can exist - one Segment carrying one
	// Plan - is charged over 2 KiB, so no real entry is smaller than this.
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
	return fallbackMemoryLimitBytes, memorySourceFallback
}

// DerivedScheduler is the query admission and queueing shape for one container.
type DerivedScheduler struct {
	ActiveExecutions      int
	ProcessQueryPermits   int
	RecoveryQueryPermits  int
	ReadyQueueCapacity    int
	RecoveryQueueCapacity int
}

// DeriveScheduler sizes admission from the CPU budget. Queries spend most of
// their time waiting on the downstream, so the permit count is a multiple of
// the budget rather than equal to it; the queues only hold references, so they
// are sized to keep a full permit set fed rather than to bound memory.
//
// ActiveExecutions bounds how many Runner invocations the dispatcher may have
// outstanding at once. It was previously unbounded, and production showed what
// that buys: a Worker owning 461 Query Groups peaked at 452 outstanding
// invocations, of which the query permits below could admit 32. The other 420
// were fully prepared executions - decoded Schedule, plan set, request
// structures - parked on the permit semaphore. Over a minute those queries
// held permits for 181 seconds and waited for them for 380 seconds, so the
// unbounded fanout was not producing query throughput. It was producing a
// heap that swung between 170 MiB and 1.58 GiB, and 278 objects deep on a
// 32-wide gate.
//
// The bound has to leave the batch able to finish inside its window. A Slot
// becomes runnable at its evaluation time plus the ready delay and must
// complete by its completion deadline, which leaves 30 seconds. Measured over
// that window, one Worker's batch is 1,207 execute-seconds, of which 368 come
// from the executions that run longer than 15 seconds. Those do not belong in
// the batch arithmetic: at 11.8 per minute and about 39 seconds each they hold
// roughly 7.6 slots continuously, so they are resident occupancy the batch
// never gets back. The remaining 839 execute-seconds have to fit N slots
// alongside them, and list scheduling bounds the makespan by the work divided
// over the slots plus the longest job still in the batch:
//
//	839 / (N - 7.6) <= 30 - 15  =>  N >= 63.5
//
// Charging the whole 1,207 against the same 15-second longest job instead
// gives N >= 80.5, which is a cross-check rather than the derivation: it
// double-counts the resident tail. Neither figure can be read off the actual
// longest execution, because that is 60 seconds and already exceeds the whole
// 30-second window - a completion-deadline fault that predates any bound here
// and that no admission limit can repair.
//
// Four per query permit puts this container at 128, comfortably above the 64
// the measurement demands and 3.2 times the permit budget it has to keep fed,
// while cutting the parked pile-up from a measured 452 to 128. It also stays
// far below ReadyQueueCapacity, which is 32 per permit, so the queue is never
// the binding side of the pair.
func DeriveScheduler(inputs CapacityInputs) DerivedScheduler {
	cpu := max(inputs.CPUBudget, 1)
	permits := cpu * queryPermitsPerCPU
	queue := permits * queueDepthPerQueryPermit
	return DerivedScheduler{
		ActiveExecutions:      min(permits*activeExecutionsPerQueryPermit, queue),
		ProcessQueryPermits:   permits,
		RecoveryQueryPermits:  max(permits/recoveryPermitDivisor, 1),
		ReadyQueueCapacity:    queue,
		RecoveryQueueCapacity: queue,
	}
}

// DerivedGoRuntime is the collector's budget for one container.
type DerivedGoRuntime struct {
	MemoryLimitBytes int64
	GCPercent        int
	// Applied is false when no container stated a memory limit. A limit
	// invented from the fallback would make the collector enforce a guess
	// about a machine nobody measured, so the process keeps the Go defaults
	// and says so rather than sizing itself against a number it made up.
	Applied bool
}

// DeriveGoRuntime sizes the collector from the container's memory limit, for
// the same reason every other budget here is derived: a deployment states the
// container it wants, and what the process does inside it follows.
//
// Nothing set either of these before, and production ran on the Go defaults:
// next_gc sat at exactly twice the live heap on both replicas, so a collection
// ran every 0.65 seconds and the collector took 17.7% of the process while the
// container's 8 GiB was 87% unused. That is the whole finding - the memory was
// bought and never spent.
//
// The two settings are not interchangeable and both have to move. A soft
// memory limit alone changes nothing measurable, because a target at twice a
// 430 MiB live heap fires long before any limit worth setting. A collector
// target alone has no absolute ceiling. So the limit is the byte budget and
// derives from the container; the target is a ratio and therefore does not.
//
// Half the container is what the limit can be without becoming the next
// problem. It has to clear the largest ceiling the process already derives
// from the same number - retained bytes take a quarter - and it has to leave
// enough underneath that the runtime's own collector CPU limiter can overshoot
// into the remainder rather than the kernel reclaiming the process. Half
// leaves both: twice the retained ceiling above, and half the container below.
// It also keeps the steady heap under the memory request a deployment of this
// shape asks for, which turning the collector off entirely would not - and a
// process permanently above its request is the first one evicted when its node
// comes under pressure.
//
// The target crosses over to that limit once the live heap reaches an eighth
// of the container. Below that the ratio governs and the heap tracks what the
// process actually holds; above it the byte budget governs. Both crossover
// terms are fractions of the same limit, so the ratio between them - 300 - is
// the same on every container, which is correct for a ratio.
func DeriveGoRuntime(inputs CapacityInputs) DerivedGoRuntime {
	if inputs.MemorySource == "" || inputs.MemorySource == memorySourceFallback {
		return DerivedGoRuntime{}
	}
	limit := inputs.MemoryLimitBytes / goMemoryLimitDivisor
	if limit > uint64(math.MaxInt64) {
		limit = uint64(math.MaxInt64)
	}
	return DerivedGoRuntime{
		MemoryLimitBytes: int64(max(limit, 1)),
		GCPercent:        (goGCLiveHeapDivisor/goMemoryLimitDivisor - 1) * 100,
		Applied:          true,
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
// by the three publication paths that need them and kept by nobody - and is
// charged 3/2 of its payload, about 215 KiB at that shape, which is above
// every ratio measured across shapes and build modes rather than near the
// typical one. One sixteenth of the container makes the owned set fit twice
// over: 512 MiB on 8 GiB holds about 2,400 such timelines against the 931
// owned, which occupy 205 MiB of charge for 146 MiB of actual heap. The
// ceiling is deliberately well above the residency, because it is a ceiling:
// the cache only ever grows to the working set, and a bound that tracks the
// container leaves room for a Worker that takes on more Query Groups without
// conceding more than a sixteenth of the process to a read cache.
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
