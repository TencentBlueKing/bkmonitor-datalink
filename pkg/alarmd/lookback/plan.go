// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// PlanLife is how long a plan entry lasts from when the control plane last
// renewed it: two of a sample's longest rests. A process that has just taken
// a Query Group over samples it within a rest, and its samples have read their
// deepest rung by about one more, so an entry outlives the time its new
// owner's own evidence takes to confirm or clear it, and one nobody renews -
// the evidence gone, or a control plane that stopped planning - is gone
// within two hours.
const PlanLife = 2 * restCap

// Plan is the control plane's word that a Query Group read late series at a
// recheck rung, good until ExpiresAt.
//
// What a process learns about a Query Group's late series lives in its
// memory, and a restart or a change of owner loses it: on one production
// deployment the Query Groups read directed fell from about fifty to none at
// each rollout and took an hour to an hour and a half to come back, every
// late series of those Query Groups unsupplemented meanwhile. A plan lets the
// next process start where the last one was. It is advice, never evidence: a
// Query Group a sample of which has completed here is decided by its own
// samples, and a plan the control plane withdrew or let expire takes the
// directed reads it started with it, unless a sample here has since seen the
// late series too.
type Plan struct {
	QueryGroup execution.QueryGroupIdentity
	Rung       int
	ExpiresAt  time.Time
}

// What became of a plan entry, closed: seeded, the Query Group was read
// directed at its rung from its first read here; kept_local, the Query
// Group's own samples here already decide it; expired, a seeded Query Group
// none of whose samples here saw late series left directed reading when its
// entry expired or was withdrawn.
const (
	PlanSeeded    = "seeded"
	PlanKeptLocal = "kept_local"
	PlanExpired   = "expired"
)

// PlanOutcomes is every plan outcome.
var PlanOutcomes = []string{PlanSeeded, PlanKeptLocal, PlanExpired}

// ApplyPlans replaces the plan set with plans: the whole set the control
// plane holds for this Worker, never a change to the last one. An entry
// naming a rung this build does not have is not a plan; one already past
// its expiry goes with the rest of the expired ones.
func (engine *Engine) ApplyPlans(plans []Plan) {
	if engine == nil {
		return
	}
	now := engine.options.Now()
	engine.mu.Lock()
	defer engine.mu.Unlock()
	next := make(map[execution.QueryGroupIdentity]Plan, len(plans))
	for _, plan := range plans {
		if plan.Rung < 0 || plan.Rung >= len(RungSteps) {
			continue
		}
		next[plan.QueryGroup] = plan
	}
	engine.plans = next
	engine.expirePlansLocked(now)
	for queryGroup, plan := range engine.plans {
		if state := engine.groups[queryGroup]; state != nil {
			engine.seedLocked(state, plan, now)
		}
	}
}

// seedLocked starts a Query Group's directed reads at its plan's rung, when
// nothing here says otherwise: no sample of it has completed in this
// process. Its own late series are only ever found by such a sample, so a
// Query Group read directed on its own evidence is a sampled one. A seeded
// Query Group none of whose samples has seen late series yet takes a newer
// entry's rung: the plan is the only evidence it has. Each entry is counted
// once, however many reads it is seen at.
func (engine *Engine) seedLocked(state *group, plan Plan, now time.Time) {
	if late := state.seriesLate; late != nil && late.seeded {
		if late.seen == 0 {
			late.rung = plan.Rung
		}
		return
	}
	if state.sampled {
		if !state.planKept {
			state.planKept = true
			engine.counts.plans[PlanKeptLocal]++
		}
		return
	}
	state.seriesLate = &seriesLateState{rung: plan.Rung, since: now, seeded: true}
	engine.counts.plans[PlanSeeded]++
}

// expirePlansLocked drops the entries past their expiry, and ends the
// directed reads of every seeded Query Group whose entry is gone while none
// of its own samples here has seen late series.
func (engine *Engine) expirePlansLocked(now time.Time) {
	for queryGroup, plan := range engine.plans {
		if !now.Before(plan.ExpiresAt) {
			delete(engine.plans, queryGroup)
		}
	}
	for queryGroup, state := range engine.groups {
		if late := state.seriesLate; late != nil && late.seeded && late.seen == 0 {
			if _, planned := engine.plans[queryGroup]; !planned {
				endLateSeries(state)
				engine.counts.plans[PlanExpired]++
			}
		}
	}
}

// SeriesLateRungs is every Query Group this process reads directed on its
// own evidence, with the rung its late series were last seen at: what the
// control plane plans from. A seeded Query Group none of whose samples here
// has seen late series is left out: reporting a plan back as evidence would
// keep it alive with nothing behind it.
func (engine *Engine) SeriesLateRungs() map[execution.QueryGroupIdentity]int {
	if engine == nil {
		return nil
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	rungs := make(map[execution.QueryGroupIdentity]int)
	for queryGroup, state := range engine.groups {
		if late := state.seriesLate; late != nil && (!late.seeded || late.seen > 0) {
			rungs[queryGroup] = late.rung
		}
	}
	return rungs
}
