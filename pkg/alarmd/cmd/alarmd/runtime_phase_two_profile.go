// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"fmt"
	"strings"

	"go.uber.org/automaxprocs/maxprocs"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func configurePhaseTwoCPU() (string, error) {
	return configurePhaseTwoCPUWith(func(log func(string, ...interface{})) error {
		_, err := maxprocs.Set(maxprocs.Logger(log))
		return err
	})
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

func phaseTwoRuntimeProfile(cfg config.Config, cpuSource string, procs int) (observability.RuntimeConfigFacts, error) {
	facts := observability.RuntimeConfigFacts{
		Profile: "standard-conservative-v1", Source: "product_default", CPUSource: cpuSource, GOMAXPROCS: procs,
		Capacity: phaseTwoRuntimeCapacity(cfg),
	}
	if facts.Capacity != phaseTwoRuntimeCapacity(config.Default()) {
		facts.Source = "configuration_override"
	}
	// Digest the exact logged safe values, with the digest field still empty.
	digest, err := contract.DeriveCanonicalDigestV2("alarmd-runtime-config-v1", facts)
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
		ReadyQueue: s.ReadyQueueCapacity, RecoveryQueue: s.RecoveryQueueCapacity, QueuedPerQG: s.MaxQueuedItemsPerQG,
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
