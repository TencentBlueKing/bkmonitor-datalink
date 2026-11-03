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
	"context"
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// lookbackPlanStore is where the planner reads the Workers' lookback late
// lists and keeps its plan set (fleet.RedisStore).
type lookbackPlanStore interface {
	LoadLookbackLate(ctx context.Context, replicas []string) (map[string]map[string]int, error)
	LoadLookbackPlan(ctx context.Context, now time.Time) (map[string]fleet.LookbackPlanEntry, error)
	SaveLookbackPlan(ctx context.Context, entries map[string]fleet.LookbackPlanEntry, now time.Time) error
}

// lookbackPlanner is the control plane's lookback planning, a second-tier
// function: only the preferred control process runs it. Each round it merges
// what every Worker's lookback reads directed into its plan set, keeps the
// set in the store so a restart of the control process does not lose it -
// a rollout restarts the Workers and the control process together, and the
// Workers' lists start empty - and splits it by each Query Group's current
// owner for the view stream to send. A fallback Leader plans nothing,
// reads and writes nothing, and sends nothing: every Worker keeps what it
// holds until it expires.
//
// Only the round's goroutine calls it.
type lookbackPlanner struct {
	preferred bool
	store     lookbackPlanStore
	recorder  *metric.Recorder
	now       func() time.Time
	// entries is the plan set as planned; loaded says it was read from the
	// store in this term, and unsaved that it changed since the store last
	// took it, so a save that failed is made again on the next round rather
	// than waiting for the next change.
	entries map[string]fleet.LookbackPlanEntry
	loaded  bool
	unsaved bool
}

// newTerm makes the next round read the kept set again: what another
// Leader planned in between is the set to start from.
func (planner *lookbackPlanner) newTerm() {
	if planner != nil {
		planner.loaded, planner.entries, planner.unsaved = false, nil, false
	}
}

// plan is the round's plan set by Worker, every Worker in workers with a set
// (the empty one for a Worker it names nothing for), or nil when this
// process plans nothing or could not read what it plans from.
func (planner *lookbackPlanner) plan(ctx context.Context, owners map[execution.QueryGroupIdentity]string,
	workers []string) (map[string][]viewstream.LookbackPlan, error) {
	if planner == nil {
		return nil, nil
	}
	if !planner.preferred {
		planner.recorder.RecordControlTierPaused(metric.ControlTierLookbackPlanning)
		return nil, nil
	}
	now := planner.now()
	if !planner.loaded {
		kept, err := planner.store.LoadLookbackPlan(ctx, now)
		if err != nil {
			return nil, err
		}
		planner.entries, planner.loaded = kept, true
	}
	lists, err := planner.store.LoadLookbackLate(ctx, workers)
	if err != nil {
		return nil, err
	}
	life := lookback.PlanLife.Milliseconds()
	// Around a change of owner two replicas can both read a Query Group
	// directed on their own evidence, at different rungs, until the old one
	// forgets it: the round takes the deepest, so the entry does not flip
	// between the two - and the set is not saved and sent again - every round.
	deepest := make(map[string]int)
	for _, list := range lists {
		for queryGroup, rung := range list {
			if current, seen := deepest[queryGroup]; !seen || rung > current {
				deepest[queryGroup] = rung
			}
		}
	}
	for _, queryGroup := range sortedKeys(deepest) {
		rung := deepest[queryGroup]
		entry, kept := planner.entries[queryGroup]
		// Renewed only when its rung moved or half its life is gone, so the
		// set - and every Worker's message - does not change on every round
		// a Query Group stays late.
		if !kept || entry.Rung != rung || entry.ExpiresAtMs-now.UnixMilli() < life/2 {
			planner.entries[queryGroup] = fleet.LookbackPlanEntry{Rung: rung, ExpiresAtMs: now.UnixMilli() + life}
			planner.unsaved = true
		}
	}
	for queryGroup, entry := range planner.entries {
		if entry.ExpiresAtMs <= now.UnixMilli() {
			delete(planner.entries, queryGroup)
			planner.unsaved = true
		}
	}
	if planner.unsaved {
		if err := planner.store.SaveLookbackPlan(ctx, planner.entries, now); err != nil {
			return nil, err
		}
		planner.unsaved = false
	}
	byWorker := make(map[string][]viewstream.LookbackPlan, len(workers))
	for _, worker := range workers {
		byWorker[worker] = nil
	}
	for queryGroup, entry := range planner.entries {
		worker := owners[execution.QueryGroupIdentity(queryGroup)]
		if worker == "" {
			continue
		}
		byWorker[worker] = append(byWorker[worker], viewstream.LookbackPlan{QueryGroup: execution.QueryGroupIdentity(queryGroup),
			Class: viewstream.LookbackPlanSeriesLate, Rung: entry.Rung, ExpiresAt: time.UnixMilli(entry.ExpiresAtMs)})
	}
	for worker := range byWorker {
		sort.Slice(byWorker[worker], func(i, j int) bool { return byWorker[worker][i].QueryGroup < byWorker[worker][j].QueryGroup })
	}
	return byWorker, nil
}

// WithLookbackPlanner gives the Leader's rounds the lookback planning over
// store: planned when preferred, the control process's own class, paused
// and counted otherwise. The fleet store is opened after the ownership, so
// this comes after it.
func (runtime *productionPhaseTwoOwnership) WithLookbackPlanner(store lookbackPlanStore, preferred bool, recorder *metric.Recorder) {
	if runtime == nil || store == nil {
		return
	}
	runtime.lookbackPlanner = &lookbackPlanner{preferred: preferred, store: store, recorder: recorder, now: runtime.dependencies.Now}
}
