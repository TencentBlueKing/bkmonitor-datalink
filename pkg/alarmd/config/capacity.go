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
	"time"
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
	retainedMemoryDivisor = 4
	bytesPerSeries        = 4 << 10
	bytesPerMutation      = 32 << 10

	// The Go runtime's own budgets are derived from the same container limit.
	// The soft memory limit takes five eighths of it (DeriveGoRuntime); the
	// collector target is a fixed ratio, set on its own.
	goMemoryLimitNumerator   = 5
	goMemoryLimitDenominator = 8
	goGCPercent              = 300
	// executionRuntimeDivisor is the share of the container the parked
	// executions are sized under (activeExecutionCeiling): half, as it was
	// before the soft limit moved, so raising the collector's room admits no
	// more concurrent Slots than before.
	executionRuntimeDivisor = 2

	controlTimelineCacheMemoryDivisor = 16
	// The smallest Schedule timeline that can exist - one Segment carrying one
	// Plan - is charged over 2 KiB, so no real entry is smaller than this.
	controlTimelineCacheMinEntryBytes = 1 << 10

	compileCacheMemoryDivisor = 32
	// Every compile cache entry is charged a 64-byte header before anything
	// it holds (strategy.estimateCompileResultBytes), so none is smaller.
	compileCacheMinEntryBytes = 64

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

// DerivedScheduler is the query admission and queueing shape for one
// container: the bounds in force, and the ceilings the container's memory
// sets on them.
type DerivedScheduler struct {
	ActiveExecutions      int
	ProcessQueryPermits   int
	RecoveryQueryPermits  int
	ReadyQueueCapacity    int
	RecoveryQueueCapacity int
	// ActiveExecutionCeiling is the most executions this container's memory
	// can hold parked at once, and the bound none of the others exceeds: the
	// dispatcher runs this many execution slots and the Redis pool covers
	// them. It does not move while the process runs.
	ActiveExecutionCeiling int
}

// The smallest a delivered series and one of its records can be. Every
// series carries its dimension identity, a 64-character digest, and every
// record at least a millisecond timestamp, 13 digits. They are floors of the
// encoding, not estimates: a count bound at the bytes over one of them can
// only bind after the byte bound would have, unless the bytes were
// miscounted.
const (
	MinimumSeriesBytes = 64
	MinimumRecordBytes = 13
)

// The bytes one parked execution holds outside the retained pool: its decoded
// Schedule, plan set and request structures. Measured when the executions were
// unbounded - 452 parked on a 32-wide permit gate swung the heap between
// 170 MiB and 1.58 GiB, about 3 MiB each - and to be measured again from the
// in-place wait's retention reading against the heap at peak.
const bytesPerParkedExecution = 3 << 20

// DeriveScheduler is the shape a container runs with: every bound at the
// ceiling its memory sets. Only the queues are sized again once the work it
// owns is known (DeriveSchedulerShape).
//
// CPU sizes none of them. The permits once came from CPU times four, the
// executions from four per permit and the queues from thirty-two per permit,
// and the readings said each was the wrong quantity: a container at 16% of its
// CPU had 56% of its queries waiting for a permit, the execution slots were
// full while permits sat free, and of 128 slots 77.5 were inside a Redis round
// trip. A query permit is held while the query service answers and an
// execution slot while Redis and the query service answer; neither is CPU
// work. What a held slot costs this process is memory, so memory sets the
// ceiling, and the query permits are a share of it.
//
// The permits were once sized from the work's own rate - peak queries a
// second times the hold, twice over - and that sized nothing real: on a small
// deployment it sat at its floor of two while almost no query ever waited,
// and on a large one it rose with the demand, so it never stood between the
// query service and a load it could not take. A share of what this process
// can hold in memory is the bound this process actually has. What the query
// service can take is the service's own capacity, read from its metrics and
// from query_downstream_distress_total, not guessed from alarmd's demand.
//
// The ceiling is the memory the runtime may use beyond the retained pool -
// half the container less the retained bytes (executionRuntimeDivisor), not
// the soft memory limit, which has room above it for the live heap's
// overshoot - over what one parked execution holds.
func DeriveScheduler(inputs CapacityInputs) DerivedScheduler {
	ceiling := activeExecutionCeiling(inputs)
	queries := max(ceiling/startQueryShareOfCeiling, 3)
	recovery := max(queries/recoveryShareOfQueries, 1)
	permits := queries - recovery
	queue := permits * queueDepthPerQueryPermit
	return DerivedScheduler{
		ActiveExecutions:       ceiling,
		ProcessQueryPermits:    permits,
		RecoveryQueryPermits:   recovery,
		ReadyQueueCapacity:     queue,
		RecoveryQueueCapacity:  queue,
		ActiveExecutionCeiling: ceiling,
	}
}

func activeExecutionCeiling(inputs CapacityInputs) int {
	runtimeBytes := inputs.MemoryLimitBytes / executionRuntimeDivisor
	retained := inputs.MemoryLimitBytes / retainedMemoryDivisor
	if runtimeBytes <= retained {
		return minimumActiveExecutions
	}
	return int(min(max((runtimeBytes-retained)/bytesPerParkedExecution, minimumActiveExecutions), uint64(math.MaxInt32)))
}

// The smallest shape that can run: one recovery permit, one process permit
// above it, an execution for each and one more.
const minimumActiveExecutions = 4

// Half the execution slots may hold a query - a profile of full slots found
// about a third in one - and a quarter of those are recovery permits. Each
// permit gets thirty-two queue places until the work is known; the queues are
// then sized from the owned Query Groups.
const (
	startQueryShareOfCeiling = 2
	recoveryShareOfQueries   = 4
	queueDepthPerQueryPermit = 32
)

// SchedulerDemand is the work a process owns and how fast it is served. Only
// the owned Query Groups size anything (the queues); the rest is reported
// beside the bounds, so a reader sees how far the work is from them.
type SchedulerDemand struct {
	// PeakQueriesPerSecond is the most normal queries this process's owned
	// Query Groups make ready in one second.
	PeakQueriesPerSecond float64
	// ReplayQueriesPerSecond is the rate of queries replays and retries ask
	// a recovery permit for.
	ReplayQueriesPerSecond float64
	// PeakSlotsPerSecond is the most Slots made ready in one second. It is
	// reported, not sized from: the slots are the memory ceiling.
	PeakSlotsPerSecond float64
	// Hold is how long a query holds its permit when the query service is
	// not under pressure (the lowest hourly median); zero before any.
	Hold time.Duration
	// Execution is how long a Slot's execution runs, its Redis round trips
	// included; zero before any. Reported, not sized from.
	Execution time.Duration
	// OwnedQueryGroups is how many Query Groups the process owns.
	OwnedQueryGroups int
	// MaxQueuedItemsPerQG is how many entries one Query Group may have queued.
	MaxQueuedItemsPerQG int
}

// DeriveSchedulerShape sizes the queues from the work and keeps every other
// bound at the ceiling the container's memory sets.
//
//   - Execution slots stay at the ceiling. They bound how many executions are
//     parked in memory at once, which is what the ceiling measures. Sized from
//     demand they hid the demand: fewer slots ran fewer Slots, which asked for
//     fewer permits, which read as less demand - stuck at the floor on a
//     production deployment within a minute of starting.
//   - Process and recovery permits stay at their share of the ceiling. They
//     do not move with the demand: a bound that follows the demand at twice
//     its size is never the constraint, and on a small deployment its floor
//     read as one.
//   - Queues: every owned Query Group queued as deep as one may be.
func DeriveSchedulerShape(ceiling DerivedScheduler, demand SchedulerDemand) DerivedScheduler {
	top := max(ceiling.ActiveExecutionCeiling, minimumActiveExecutions)
	queued := max(demand.MaxQueuedItemsPerQG, 1)
	queue := max(demand.OwnedQueryGroups, 1)*queued + 1
	return DerivedScheduler{
		ActiveExecutions:       top,
		ProcessQueryPermits:    ceiling.ProcessQueryPermits,
		RecoveryQueryPermits:   ceiling.RecoveryQueryPermits,
		ReadyQueueCapacity:     queue,
		RecoveryQueueCapacity:  queue,
		ActiveExecutionCeiling: top,
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
// The limit is five eighths of the container, derived from measured usage
// (decision-019 §5.2). The live heap rises at most about 1.5 bytes per
// retained byte charged; with one Query Group at its share of the retained
// pool beside its measured co-tenants, the live heap is near 1.4 GiB on a
// 4 GiB container. Five eighths gives the collector about 1.8 times that
// before it runs continuously - the room it had at half the container
// before the share grew - and the container, counting the measured non-heap
// memory and a sampling allowance, stays within about 3.3 GiB.
//
// The collector target is a fixed 300 percent, set apart from the limit: at
// 2 to 4 percent collector CPU in bursts a higher target buys nothing and
// only raises the peak. The limit also bounds the observation memory line,
// which reads it live (memoryline), so its consumers gain the same room;
// they still refuse at the line.
func DeriveGoRuntime(inputs CapacityInputs) DerivedGoRuntime {
	if inputs.MemorySource == "" || inputs.MemorySource == memorySourceFallback {
		return DerivedGoRuntime{}
	}
	limit := inputs.MemoryLimitBytes / goMemoryLimitDenominator * goMemoryLimitNumerator
	if limit > uint64(math.MaxInt64) {
		limit = uint64(math.MaxInt64)
	}
	return DerivedGoRuntime{
		MemoryLimitBytes: int64(max(limit, 1)),
		GCPercent:        goGCPercent,
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

// DerivedCompileCache bounds the Plan compile cache for one container.
type DerivedCompileCache struct {
	MaxEntries int
	MaxBytes   int
}

// DeriveCompileCache sizes the Plan compile cache from the memory limit. The
// cache holds one compiled Plan per Plan the process compiles: on a Worker the
// Plans of the Query Groups it owns, each Slot; on the leader every Plan of
// the deployment, on every catalog build. That second access is a cycle over
// the whole set, and a least-recently-used cache that is smaller than a cycle
// misses on every access, so the bound is a cliff rather than a slope: the
// written 4096 entries and 64 MiB it replaces were fine at the 2,930 Plans of a
// production deployment and would have recompiled every Plan on every build one deployment
// size later, with nothing reporting it.
//
// One thirty-second of the container holds that cycle many times over: the
// leader's compiled Plans measured 35 MB of live heap for 2,930 Plans, about
// 12 KiB each, so 8 GiB holds some twenty thousand. It is a ceiling, not a
// reservation - the cache only grows to the Plans the process compiles.
// Floored at one maximum compiled Plan, which the cache must always admit.
//
// The entry bound comes from the same budget, as for the timeline cache: how
// many entries it could hold if every one were the smallest that can exist,
// so it never binds before the bytes do.
func DeriveCompileCache(inputs CapacityInputs, maxCompiledPlanBytes int) DerivedCompileCache {
	budget := max(inputs.MemoryLimitBytes/compileCacheMemoryDivisor, uint64(max(maxCompiledPlanBytes, compileCacheMinEntryBytes)))
	if budget > uint64(math.MaxInt) {
		budget = uint64(math.MaxInt)
	}
	return DerivedCompileCache{
		MaxEntries: max(int(budget/compileCacheMinEntryBytes), 1),
		MaxBytes:   int(budget),
	}
}

// DeriveCoordinator sizes the process budgets from the memory limit. Retained
// bytes take a quarter of it, leaving the rest for the Go heap's own overhead,
// non-retained allocation and collection headroom, and they are the working
// limit: what bounds how much of a Slot this process will hold at once.
//
// The counts are not a second memory budget. They were one - every count was
// retained bytes divided by an assumed 32 KiB per mutation - and measuring what
// a mutation actually costs found 3.5 KiB for a light shape against 28.9 KiB
// for a heavy one, a 9x spread around a single constant. A count derived that
// way stops a light Slot at a ninth of the memory it was allowed and lets a
// heavy one through at nearly all of it, so the number that was supposed to
// protect memory was the one deciding throughput, and the memory budget it was
// standing in for was never reached. Bytes are measured per object, so they do
// the memory work directly and the counts stop pretending to.
//
// What the counts are now is a runaway guardrail: the point past which a Slot
// cannot be delivered whatever memory says. For State and Gap mutations that
// point is what a chunked Store apply can carry - max_keys_per_batch items per
// call, StateApplyMaxChunks calls - because a Slot above it admits work no
// apply could ever complete. Events do not take that path: one WriteBatch
// carries a Slot's events with no chunking, so their own wall is what a single
// output batch can push, which nothing has measured. Until it is, they are held
// at the same guardrail, which is sound in the safe direction - every event
// hangs off a State result, and those are capped there - and is called out
// rather than left looking derived. That measurement and the retained-bytes
// divisor are the same kind of open question, and both wait on the first real
// readings of what a Slot uses.
//
// The guardrail is floored at one Store call. A process that cannot hold a
// single batch's worth of mutations cannot complete the smallest Slot the
// chunked apply is built around, so a very small container runs with less
// headroom rather than with a budget no apply can use.
//
// The series count is not a budget. It was the retained bytes over 4 KiB, a
// guess at a series' size, and was checked before the bytes: a Slot of light
// series was turned away by count while most of its bytes were free, and a
// container under 8 GiB met the count first at a half-million-series target.
// The bytes govern. What is left of the count is a runaway guardrail at the
// smallest size a series can have, so it binds only where the byte accounting
// itself has gone wrong.
func DeriveCoordinator(inputs CapacityInputs, storeItemsPerBatch, chunkedApplyBudget uint64) PhaseTwoCoordinatorConfig {
	retained := max(inputs.MemoryLimitBytes/retainedMemoryDivisor, uint64(bytesPerMutation))
	guardrail := max(chunkedApplyBudget, storeItemsPerBatch, 1)
	return PhaseTwoCoordinatorConfig{
		MaxRetainedBytes:  retained,
		MaxSeries:         max(retained/MinimumSeriesBytes, 1),
		MaxStateMutations: guardrail,
		MaxGapMutations:   guardrail,
		MaxEvents:         guardrail,
		// Sequencer reservations are held with the mutations they sequence, so
		// the guardrail bounds them too. It is an int, and a guardrail beyond
		// what an int holds is not a guardrail on this platform.
		MaxSequencerReservations: int(min(guardrail, uint64(math.MaxInt))),
	}
}
