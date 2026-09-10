// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"runtime"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// capacitySnapshotSource is what one replica reports about its own limits.
//
// The ceilings come from the profile the process derived at startup, which is
// the same table check-config prints; the occupancy is read live. Both travel
// in the replica's snapshot so the page never has to ask the time series
// database what this deployment's limits are.
func capacitySnapshotSource(
	flights *scheduler.FlightCoordinator,
	cfg config.Config,
	rejections *fleet.RejectionTally,
	rotation func() *fleet.Rotation,
) func() *fleet.Capacity {
	// The same derivation the startup profile and check-config print, so the
	// page cannot report a ceiling the process is not actually running with.
	// The container the budgets are derived from is read once and used for both
	// the derivation and the provenance reported next to it, so the page cannot
	// show a ceiling derived from one container beside the shape of another.
	inputs := config.DetectCapacityInputs()
	derived := phaseTwoRuntimeCapacity(cfg, inputs)
	facts := struct {
		Capacity         observabilityCapacity
		MemoryLimitBytes uint64
		MemorySource     string
		GOMAXPROCS       int
	}{
		Capacity:         observabilityCapacity(derived),
		MemoryLimitBytes: inputs.MemoryLimitBytes,
		MemorySource:     inputs.MemorySource,
		GOMAXPROCS:       runtime.GOMAXPROCS(0),
	}
	budgets := snapshotBudgets(facts.Capacity)
	return func() *fleet.Capacity {
		occupancy := flights.QueryPermitOccupancy()
		held := 0
		seconds := 0.0
		for _, count := range occupancy.Inflight {
			held += count
		}
		for _, value := range occupancy.HeldSeconds {
			seconds += value
		}
		usage := config.ReadContainerUsage()
		capacity := &fleet.Capacity{
			PermitsHeld: held, PermitBudget: occupancy.Budget, PermitSeconds: seconds,
			Waiting:     occupancy.Waiting["normal"] + occupancy.Waiting["recovery"],
			QueueBudget: facts.Capacity.ReadyQueue + facts.Capacity.RecoveryQueue,
			// The limit and where it was read from travel together: a limit
			// found outside a container is a fallback, and presenting it as the
			// container's would make every budget derived from it look
			// authoritative.
			MemoryLimit: facts.MemoryLimitBytes, MemorySource: facts.MemorySource,
			CPUCores: facts.GOMAXPROCS,
			Budgets:  budgets, Rejections: rejections.Counts(),
		}
		if rotation != nil {
			capacity.Rotation = rotation()
		}
		if usage.MemoryKnown {
			capacity.MemoryUsed = usage.MemoryBytes
		}
		if usage.ThrottledKnown {
			capacity.ThrottledSeconds = usage.ThrottledSeconds
		}
		return capacity
	}
}

// observabilityCapacity names the derived table locally so this file states
// what it uses without the snapshot source depending on the startup evidence
// type, which exists for a different purpose.
type observabilityCapacity = observability.RuntimeCapacityFacts

// phaseTwoDiagnosticsConnection reuses the runtime connection but never its
// client or pool.
//
// The design allows diagnostics to share the instance and requires them not to
// share connections: the control plane pool is sized from the query permit
// count, and a diagnostic burst taking from it would slow the pipeline these
// records exist to explain. Four connections is enough for a bounded,
// fire-and-forget writer and one page read at a time.
func phaseTwoDiagnosticsConnection(connection config.RedisConnectionConfig) config.RedisConnectionConfig {
	diagnostics := connection
	diagnostics.PoolSize = phaseTwoDiagnosticsPoolSize
	return diagnostics
}

// phaseTwoDiagnosticsPoolSize is deliberately small and deliberately not
// configurable: it exists to keep diagnostics from competing with the pipeline,
// and a knob that lets it grow would remove the only guarantee it provides.
const phaseTwoDiagnosticsPoolSize = 4

// snapshotBudgets is the page's copy of the same ceilings. It is separate from
// the metric's on purpose -- one travels in the snapshot, the other is scraped
// -- and being separate is exactly why the two key sets need pinning.
func snapshotBudgets(capacity observabilityCapacity) map[string]uint64 {
	return map[string]uint64{
		string(observability.CapacityBudgetSeries):         capacity.Series,
		string(observability.CapacityBudgetRetainedBytes):  capacity.RetainedBytes,
		string(observability.CapacityBudgetStateMutations): capacity.StateMutations,
		string(observability.CapacityBudgetEvents):         capacity.Events,
		string(observability.CapacityBudgetGapMutations):   capacity.GapMutations,
	}
}
