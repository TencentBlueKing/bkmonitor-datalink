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
	"strings"
	"sync"

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

func setMaxprocsFromCPUQuota(log func(string, ...interface{})) error {
	_, err := maxprocs.Set(maxprocs.Logger(log))
	return err
}

// Match the pinned library's diagnostic formats without retaining raw values
// from environment variables in startup evidence.
func configurePhaseTwoCPUWith(set func(func(string, ...interface{})) error) (string, error) {
	source := "runtime_default"
	err := set(func(format string, _ ...interface{}) {
		switch {
		case strings.Contains(format, "Honoring GOMAXPROCS"):
			source = "environment_override"
		case strings.Contains(format, "determined from CPU quota"):
			source = "cpu_quota"
		case strings.Contains(format, "using minimum allowed GOMAXPROCS"):
			source = "cpu_quota_minimum"
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
	if cfg.Input.Mode != config.InputModeGoAccess {
		return nil
	}
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
	facts := observability.RuntimeConfigFacts{
		Profile: "standard-conservative-v1", Source: "product_default", CPUSource: cpuSource, GOMAXPROCS: procs,
		MemorySource: inputs.MemorySource, MemoryLimitBytes: inputs.MemoryLimitBytes,
		Capacity: phaseTwoRuntimeCapacity(cfg),
	}
	// Compare against a default that went through the same pool derivation, or
	// every deployment would report itself as an override purely because the
	// resolved pool size replaced the zero that means "derive".
	if facts.Capacity != phaseTwoRuntimeCapacity(config.Default().WithResolvedRedisPoolSize()) {
		facts.Source = "configuration_override"
	}
	// Digest the exact logged safe values, with the digest field still empty.
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-runtime-config-v2", facts)
	facts.Digest = digest
	return facts, err
}

func phaseTwoRuntimeCapacity(cfg config.Config) observability.RuntimeCapacityFacts {
	s := cfg.PhaseTwo.Scheduler
	c := cfg.PhaseTwo.Coordinator
	uq := phaseTwoUQLimits(cfg)
	return observability.RuntimeCapacityFacts{
		ExpiredRangeEnabled: s.ExpiredRangeEnabled,
		ActiveExecutions:    min(s.ActiveExecutionLimit, s.ReadyQueueCapacity), ConfiguredActiveExecutions: s.ActiveExecutionLimit,
		QueryPermits: s.ProcessQueryPermits, RecoveryQueryPermits: s.RecoveryQueryPermits,
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
		EvaluatorMaxPlans: cfg.Limits.Detect.MaxPlans, EvaluatorMaxRecords: cfg.Limits.Detect.MaxRecordsPerSeries,
		EvaluatorMaxLevels: uint64(cfg.Limits.Compiler.MaxLevelsPerPlan), EvidenceBytes: cfg.TriggerLimits().MaxEvidenceBytesPerEvent,
		OutputMessageBytes: cfg.Kafka.TriggerEvent.MaxMessageBytes,
		UQBodyBytes:        uq.MaxBodyBytes, UQSeriesBytes: uq.MaxSeriesBytes, UQSeries: uq.MaxSeries, UQRecords: uq.MaxRecords,
	}
}
