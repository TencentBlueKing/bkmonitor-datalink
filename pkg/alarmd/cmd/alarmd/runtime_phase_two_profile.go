// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"go.uber.org/automaxprocs/maxprocs"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

var (
	cpuBudgetOnce   sync.Once
	cpuBudgetSource string
	cpuBudgetErr    error
)

// configurePhaseTwoCPU resolves GOMAXPROCS from the container's CPU quota. It
// runs before the configuration is read, because the capacity budgets are
// derived from that budget, and it caches its result so the startup facts
// report the same source the derivation used.
func configurePhaseTwoCPU() (string, error) {
	cpuBudgetOnce.Do(func() {
		cpuBudgetSource, cpuBudgetErr = configurePhaseTwoCPUWith(setMaxprocsFromCPUQuota)
	})
	return cpuBudgetSource, cpuBudgetErr
}

// phaseTwoResolvedCPUSource reports where GOMAXPROCS came from without
// resolving it. It is the one fact that says whether the capacity table below
// it describes this container or the host it landed on, and a process that
// never resolved a quota reports an empty source rather than claiming one.
func phaseTwoResolvedCPUSource() string { return cpuBudgetSource }

func setMaxprocsFromCPUQuota(log func(string, ...interface{})) error {
	_, err := maxprocs.Set(maxprocs.Logger(log))
	return err
}

// goRuntimeBudgetSetters is what applyPhaseTwoGoRuntime writes through. The
// production pair are the runtime's own, and a test supplies its own so that
// asserting the process configures itself does not require mutating the
// collector for every other test in the binary.
type goRuntimeBudgetSetters struct {
	setMemoryLimit func(int64) int64
	setGCPercent   func(int) int
}

func runtimeGoBudgetSetters() goRuntimeBudgetSetters {
	return goRuntimeBudgetSetters{setMemoryLimit: debug.SetMemoryLimit, setGCPercent: debug.SetGCPercent}
}

// applyPhaseTwoGoRuntime installs the collector budget derived from the
// container. It is deliberately unconditional: no environment variable is
// consulted and none can override the result.
//
// The reason is not doctrine. A setting that quietly returns the collector to
// one cycle every 0.65 seconds, while the preflight table and config_loaded
// both go on reporting the derived limit, is a knob whose effect nobody can
// see - and this deployment has already been bitten once by exactly that
// shape. The derived pair is printed at startup and carried in the facts, so
// what the process runs under is stated rather than negotiated.
func applyPhaseTwoGoRuntime(derived config.DerivedGoRuntime, setters goRuntimeBudgetSetters) {
	if !derived.Applied || setters.setMemoryLimit == nil || setters.setGCPercent == nil {
		return
	}
	setters.setMemoryLimit(derived.MemoryLimitBytes)
	setters.setGCPercent(derived.GCPercent)
}

// Match the pinned library's diagnostic formats without retaining raw values
// from environment variables in startup evidence.
func configurePhaseTwoCPUWith(set func(func(string, ...interface{})) error) (string, error) {
	source := observability.CPUSourceRuntimeDefault
	err := set(func(format string, _ ...interface{}) {
		switch {
		case strings.Contains(format, "Honoring GOMAXPROCS"):
			source = observability.CPUSourceEnvironmentOverride
		case strings.Contains(format, "determined from CPU quota"):
			source = observability.CPUSourceQuota
		case strings.Contains(format, "using minimum allowed GOMAXPROCS"):
			source = observability.CPUSourceQuotaMinimum
		}
	})
	if err != nil {
		return "", fmt.Errorf("configure phase-two CPU quota: %w", err)
	}
	return source, nil
}

// printResolvedRuntimeFacts writes the same startup facts the running process
// logs as config_loaded, so a release preflight can state what the build about
// to ship will actually run under and how that differs from the build it
// replaces. Answering only "valid" made a configuration change unreviewable
// before it reached a Pod, which is where both capacity incidents were found.
//
// These are internal runtime facts, not an operating interface: a setting an
// operator has to read here is a setting that should not have been theirs to
// write. The report exists for preflight and for forensics after an incident.
//
// The facts are credential-free by construction, and cpu_source names where
// GOMAXPROCS came from, so a table printed outside the Pod says so itself
// rather than passing the host's core count off as the container's.
func printResolvedRuntimeFacts(cfg config.Config, stdout io.Writer) error {
	cpuSource, err := configurePhaseTwoCPU()
	if err != nil {
		return err
	}
	facts, err := phaseTwoRuntimeProfile(cfg.WithResolvedRedisPoolSize(), cpuSource, runtime.GOMAXPROCS(0))
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(facts)
}

func phaseTwoRuntimeProfile(cfg config.Config, cpuSource string, procs int) (observability.RuntimeConfigFacts, error) {
	inputs := config.DetectCapacityInputs()
	// There is no longer an override to report: no file can state a capacity
	// budget, so every budget below came from the container named by
	// cpu_source and memory_source.
	facts := observability.RuntimeConfigFacts{
		Profile: "standard-conservative-v1", Source: "container_derived", CPUSource: cpuSource, GOMAXPROCS: procs,
		MemorySource: inputs.MemorySource, MemoryLimitBytes: inputs.MemoryLimitBytes,
		Capacity: phaseTwoRuntimeCapacity(cfg, inputs),
		Storage: observability.RuntimeStorageFacts{
			OwnStore:      cfg.Redis.Connection().Destination(),
			StrategyCache: cfg.StrategySourceRedis().Destination(),
			CMDBCache:     cfg.CMDBCacheRedis().Destination(),
			LegacyService: cfg.Kafka.LegacyAdapter.ServiceRedis.Destination(),

			OutputProtocol:      cfg.OutputProtocol(),
			PlatformKeyPrefix:   cfg.PlatformKeyPrefix(),
			StrategyCachePrefix: cfg.PhaseTwo.Control.StrategyCachePrefix,
			OwnStorePrefix:      cfg.Redis.StatePrefix,

			DynamicConfig:             dynamicConfigDestination(cfg),
			TargetGroup:               targetGroupDestination(cfg),
			DynamicGroupKeyPrefix:     targetGroupPrefix(cfg),
			PlatformSettingsKeyPrefix: cfg.PhaseTwo.PlatformSettings.RedisKeyPrefix,
		},
		Linkd:     observability.RuntimeLinkdFacts{ConsoleConfigured: cfg.PhaseTwo.Linkd.ConsoleURL != ""},
		UQCodec:   phaseTwoUQCodecFacts(),
		Retention: phaseTwoRuntimeRetention(cfg),
	}
	// Digest the exact logged safe values, with the digest field still empty.
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-runtime-config-v2", facts)
	facts.Digest = digest
	return facts, err
}

func phaseTwoUQCodecFacts() observability.RuntimeUQCodecFacts {
	return observability.RuntimeUQCodecFacts{Mode: "shared_schema", Scope: "ordinary_execute_non_polling"}
}

// phaseTwoRuntimeRetention is the retention lengths from the functions the
// deployment is assembled with, and their inputs, so the facts cannot say
// one thing while the Slot source and admission use another.
func phaseTwoRuntimeRetention(cfg config.Config) observability.RuntimeRetentionFacts {
	seconds := func(duration time.Duration) int64 { return int64(duration / time.Second) }
	reserve := cfg.PhaseTwo.Access.DownstreamExecutionReserve.Duration()
	return observability.RuntimeRetentionFacts{
		CatalogSeconds:                    seconds(phaseTwoCatalogRetention(cfg)),
		ObjectLimitSeconds:                seconds(phaseTwoObjectRetentionLimit(cfg)),
		SnapshotMinimumSeconds:            seconds(phaseTwoSnapshotMinimumRetention(cfg, phaseTwoMaxSupportedEvaluationInterval-reserve)),
		CatalogKeyCadenceSeconds:          seconds(phaseTwoMaxSupportedEvaluationInterval),
		CatalogTTLSeconds:                 seconds(cfg.PhaseTwo.Control.CatalogTTL.Duration()),
		RedisMaxTTLSeconds:                seconds(cfg.Redis.MaxTTL.Duration()),
		RedisRestartMarginSeconds:         seconds(cfg.Redis.RestartMargin.Duration()),
		DownstreamExecutionReserveSeconds: seconds(reserve),
		PublicationDelayAllowanceSeconds:  seconds(phaseTwoPublicationDelayAllowance(cfg)),
		MaxReplayAgeSeconds:               seconds(cfg.PhaseTwo.Scheduler.MaxReplayAge.Duration()),
		PostRecoveryTerminalDelaySeconds:  seconds(phaseTwoPostRecoveryTerminalDelay(cfg)),
	}
}

// phaseTwoRuntimeCapacity takes the container inputs as well as the
// configuration because not every derived budget lands in a configuration
// field. The control timeline cache is installed straight into the repository
// at assembly, so the table has to derive it here from the same container the
// assembly will.
func phaseTwoRuntimeCapacity(cfg config.Config, inputs config.CapacityInputs) observability.RuntimeCapacityFacts {
	s := cfg.PhaseTwo.Scheduler
	c := cfg.PhaseTwo.Coordinator
	uq := phaseTwoUQLimits(cfg)
	timelineCache := config.DeriveControlTimelineCache(inputs)
	goRuntime := config.DeriveGoRuntime(inputs)
	return observability.RuntimeCapacityFacts{
		DerivedActiveExecutions:   s.ActiveExecutionLimit,
		EffectiveActiveExecutions: min(s.ActiveExecutionLimit, s.ReadyQueueCapacity),
		QueryPermits:              s.ProcessQueryPermits, RecoveryQueryPermits: s.RecoveryQueryPermits,
		RedisPoolSize: cfg.Redis.PoolSize,
		ReadyQueue:    s.ReadyQueueCapacity, RecoveryQueue: s.RecoveryQueueCapacity, QueuedPerQG: s.MaxQueuedItemsPerQG,
		TickNS: int64(s.TickInterval), ReplaySlots: s.MaxReplaySlots, ReplayAgeNS: int64(s.MaxReplayAge),
		RetryMinNS: int64(s.RetryMinDelay), RetryMaxNS: int64(s.RetryMaxDelay),
		SequencerReservations: c.MaxSequencerReservations, Series: c.MaxSeries, RetainedBytes: c.MaxRetainedBytes,
		StateMutations: c.MaxStateMutations, Events: c.MaxEvents, GapMutations: c.MaxGapMutations, GapFacts: c.MaxGapMutations,
		SlotStateMutations: execution.SlotMutationCap(uint64(cfg.Limits.Store.MaxKeysPerBatch), c.MaxStateMutations),
		SlotGapMutations:   execution.SlotMutationCap(uint64(cfg.Limits.Store.MaxKeysPerBatch), c.MaxGapMutations),
		StateApplyChunks:   execution.StateApplyMaxChunks,
		StoreMaxValueBytes: cfg.Limits.Codec.MaxEncodedBytes, StoreMaxItems: cfg.Limits.Store.MaxKeysPerBatch,
		ControlTimelineCacheBytes:   timelineCache.MaxBytes,
		ControlTimelineCacheEntries: timelineCache.MaxEntries,
		GoMemoryLimitBytes:          goRuntime.MemoryLimitBytes,
		GoGCPercent:                 goRuntime.GCPercent,
		EvaluatorMaxPlans:           cfg.Limits.Detect.MaxPlans, EvaluatorMaxRecords: cfg.Limits.Detect.MaxRecordsPerSeries,
		EvaluatorMaxLevels: uint64(cfg.Limits.Compiler.MaxLevelsPerPlan), EvidenceBytes: cfg.TriggerLimits().MaxEvidenceBytesPerEvent,
		OutputMessageBytes: cfg.Kafka.TriggerEvent.MaxMessageBytes,
		UQBodyBytes:        uq.MaxBodyBytes, UQSeriesBytes: uq.MaxSeriesBytes, UQSeries: uq.MaxSeries, UQRecords: uq.MaxRecords,
	}
}

// dynamicConfigDestination names the distribution's instance, or the fact
// that the deployment renders none.
func dynamicConfigDestination(cfg config.Config) string {
	connection, configured := cfg.DynamicConfigRedis()
	if !configured {
		return "not_configured"
	}
	return connection.Destination()
}

func targetGroupDestination(cfg config.Config) string {
	connection, configured := cfg.TargetGroupRedis()
	if !configured {
		return "not_configured"
	}
	return connection.Destination()
}

func targetGroupPrefix(cfg config.Config) string {
	prefix, _ := cfg.DynamicGroupKeyPrefix()
	return prefix
}
