// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"context"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// slotCompiledPlanKey names the compile inputs by content. PlanRevision is the
// canonical digest of the Plan itself and QueryRevision the canonical digest of
// the Query Plan facts the Plan is compiled against, dataset contract included,
// so two Slots with the same key compile the same Plan the same way. The state
// semantics are fixed when the runtime is built, which is what owns this cache.
type slotCompiledPlanKey struct {
	planRevision  string
	queryRevision execution.QueryRevision
}

// slotCompiledPlans keeps the compiled Plan of a frozen Slot contract. Freezing
// a Slot compiles every due Plan, and a Query Group freezes one Slot per
// evaluation interval for as long as it is owned, so without this the same
// unchanged Plan is compiled again on every Slot. The compiler has a cache of
// its own, but reaching it costs a canonical digest of the whole Plan, which is
// the expensive half of compiling: on a warm compiler cache a cache-key
// derivation is about 92 per cent of the call.
//
// Entries are keyed by content, so a Snapshot publication that leaves a Plan
// alone keeps its entry, and two Plans with identical semantics share one. The
// table clears rather than evicts when it reaches its bound: the population is
// the number of distinct Plan revisions this Worker owns, and reaching the
// bound means that assumption no longer holds.
type slotCompiledPlans struct {
	mu      sync.RWMutex
	results map[slotCompiledPlanKey]strategy.CompileResult
}

const slotCompiledPlanEntries = 4096

func newSlotCompiledPlans() *slotCompiledPlans {
	return &slotCompiledPlans{results: make(map[slotCompiledPlanKey]strategy.CompileResult)}
}

// compile returns the compiled Plan for key, compiling it once. A compile
// error is never remembered: it belongs to the attempt, not to the content.
func (plans *slotCompiledPlans) compile(
	ctx context.Context,
	compiler RuntimePlanCompiler,
	key slotCompiledPlanKey,
	request strategy.CompileRequest,
) (strategy.CompileResult, error) {
	if plans == nil || key.planRevision == "" || key.queryRevision == "" {
		return compiler.Compile(ctx, request)
	}
	plans.mu.RLock()
	result, found := plans.results[key]
	plans.mu.RUnlock()
	if found {
		return result, nil
	}
	result, err := compiler.Compile(ctx, request)
	if err != nil {
		return strategy.CompileResult{}, err
	}
	plans.mu.Lock()
	if len(plans.results) >= slotCompiledPlanEntries {
		plans.results = make(map[slotCompiledPlanKey]strategy.CompileResult)
	}
	plans.results[key] = result
	plans.mu.Unlock()
	return result, nil
}
