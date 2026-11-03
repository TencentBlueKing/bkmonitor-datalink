// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Objects on one replica, each inside its own share, together past the
// pool: the reading is the sum of each object's peak -- not its mean, which
// on a bimodal object is the wrong statistic -- against the pool the
// completion rows carry, with the window's pool refusals beside it, counted
// whether or not the refused object is one this summary tracks. An object
// the pool refuses every round has no completion row, and its peak is what
// the refusal says it asked for: without that the object filling the pool
// is the one object with no peak, and "60% and thirteen refusals" cannot be
// read off one row. Before any row has carried the pool's size the share is
// not a number.
func TestTheReplicasRetainedReadingIsTheSumOfPeaksAgainstThePoolWithItsRefusals(t *testing.T) {
	now := time.Unix(600, 0)
	c := NewCostSummary(CostSummaryOptions{ProcessID: "process-a", Window: time.Minute, GroupCapacity: 4, PlanCapacity: 8, MetadataBytes: 4096, TopN: 3, Now: func() time.Time { return now }})
	group := func(key, strategy string) CostGroup {
		return CostGroup{QueryGroupKey: key, QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", Members: []CostPlanIdentity{{StrategyID: strategy, BusinessID: "2"}}}
	}
	c.Reconcile([]CostGroup{group("bimodal", "4101"), group("steady", "4102"), group("quiet", "4103"), group("starved", "4104")}, true)
	ctx := context.Background()
	const mib = 1 << 20
	completed := func(key string, retained, limit uint64) {
		c.Observe(ctx, Observation{Component: ComponentScheduler, Stage: StageSlotCompleted, Result: ResultSuccess, Duration: time.Second,
			Trace:           TraceFields{QueryGroupKey: key, EvaluationTime: 590},
			SlotBudgetUsage: &SlotBudgetUsageFacts{RetainedBytes: retained, RetainedBytesLimit: limit}})
	}
	refused := func(key, reason string, budget CapacityBudget, held, requested uint64) {
		own := held
		c.Observe(ctx, Observation{Component: ComponentResource, Stage: StageResourceHard, Result: ResultPaused,
			ReasonCode: ReasonCode(reason), CapacityBudget: budget, Err: errors.New("budget"),
			CapacityRejection: &CapacityRejectionFacts{Phase: "normal_input", OwnUsed: &own, SharedUsed: 1 << 30, Requested: requested, Limit: 1 << 30},
			Trace:             TraceFields{QueryGroupKey: key, EvaluationTime: 590}})
	}

	// No row has carried the pool yet: a sum, and no share.
	completed("steady", 300*mib, 0)
	c.Publish(now)
	if r := c.Snapshot().Retained; r.PeakSumBytes != 300*mib || r.LimitKnown || r.PeakShare != 0 || r.GroupsWithPeak != 1 {
		t.Fatalf("before any row carried the pool: %+v, want the one peak, no limit and no share", r)
	}

	completed("bimodal", 175*mib, 1<<30)
	completed("bimodal", 404*mib, 1<<30)
	completed("steady", 300*mib, 1<<30)
	completed("quiet", 0, 1<<30)
	for i := 0; i < 13; i++ {
		// Pool refusals: mostly the object that never gets a completion row
		// -- it held 50 MiB and asked for 300 more when the pool refused --
		// and some on an object this summary does not track at all.
		key := "starved"
		if i%3 == 0 {
			key = "stranger"
		}
		refused(key, contract.ReasonResourceHardStop, CapacityBudgetRetainedBytes, 50*mib, 300*mib)
	}
	refused("bimodal", contract.ReasonQGBudgetShareExceeded, CapacityBudgetRetainedBytes, 404*mib, 200*mib)
	// Neither of these is a refusal of this pool, and neither moves a peak.
	refused("bimodal", contract.ReasonSlotBudgetExceeded, CapacityBudgetRetainedBytes, 900*mib, 900*mib)
	refused("bimodal", contract.ReasonResourceHardStop, CapacityBudgetSeries, 900*mib, 900*mib)
	// A nested holder's refusal withholds its own figure: not comparable to
	// the other samples, so not a peak -- and not folded as the increment
	// alone either, which the max would merely happen to hide.
	c.Observe(ctx, Observation{Component: ComponentResource, Stage: StageResourceHard, Result: ResultPaused,
		ReasonCode: ReasonCode(contract.ReasonResourceHardStop), CapacityBudget: CapacityBudgetRetainedBytes, Err: errors.New("budget"),
		CapacityRejection: &CapacityRejectionFacts{Phase: "query_free", Requested: 900 * mib, SharedUsed: 1 << 30, Limit: 1 << 30},
		Trace:             TraceFields{QueryGroupKey: "steady", EvaluationTime: 590}})
	// A completion whose stream was never built carries the zero account:
	// it does not unlearn the pool.
	completed("quiet", 0, 0)
	c.Publish(now)
	s := c.Snapshot()
	r := s.Retained
	// 404 (bimodal's peak; the share refusal asked for 604 and is the same
	// object's peak now) + 300 (steady) + 350 (starved: held plus refused).
	const wantSum = 604*mib + 300*mib + 350*mib
	if r.PeakSumBytes != wantSum || r.GroupsWithPeak != 3 {
		t.Fatalf("peak sum = %d over %d objects, want %d over 3: the bimodal object's refused 604, not its completions' mean; the starved object's 350 from its refusal; nothing for the quiet one", r.PeakSumBytes, r.GroupsWithPeak, wantSum)
	}
	if !r.LimitKnown || r.LimitBytes != 1<<30 || r.PeakShare != float64(wantSum)/float64(1<<30) {
		t.Fatalf("limit/share = %+v, want the pool the rows carried and %d/1024 MiB", r, wantSum/mib)
	}
	if r.HardStops != 14 || r.ShareStops != 1 {
		t.Fatalf("refusals = %d pool / %d share, want 14 / 1: the per-Slot cap and another budget's refusal are not this pool's, the nested holder's is", r.HardStops, r.ShareStops)
	}
	// The same numbers on the object rows, and the ranking that says which
	// objects the sum is made of, largest peak first.
	rows := map[string]CostContributor{}
	for _, row := range s.Contributors {
		if row.Scope == "query_group" {
			rows[row.Group.QueryGroupKey] = row
		}
	}
	if rows["bimodal"].Current.RetainedBytesPeak != 604*mib || rows["bimodal"].Current.RetainedHardStops != 0 || rows["bimodal"].Current.RetainedShareStops != 1 {
		t.Fatalf("bimodal row = %+v, want peak 604 MiB from the share refusal, no pool refusal, 1 share refusal", rows["bimodal"].Current)
	}
	if rows["starved"].Current.RetainedBytesPeak != 350*mib || rows["starved"].Current.RetainedHardStops != 8 || rows["starved"].Current.RunReturns != 0 {
		t.Fatalf("starved row = %+v, want a 350 MiB peak from its refusals alone, 8 of them (5 went to the stranger), and no completion", rows["starved"].Current)
	}
	if rows["steady"].Current.RetainedBytesPeak != 300*mib || rows["steady"].Current.RetainedHardStops != 1 {
		t.Fatalf("steady row = %+v, want its 300 MiB completion peak untouched by the nested holder's 900 MiB increment, and that refusal counted", rows["steady"].Current)
	}
	for _, ranking := range s.Rankings {
		if ranking.Scope != "query_group" || ranking.Dimension != "retained_bytes_peak" {
			continue
		}
		order := []string{}
		for _, index := range ranking.Indexes {
			order = append(order, s.Contributors[index].Group.QueryGroupKey)
		}
		if len(order) != 3 || order[0] != "bimodal" || order[1] != "starved" || order[2] != "steady" {
			t.Fatalf("retained_bytes_peak ranking = %v, want bimodal, starved, steady and no row for the object with no peak", order)
		}
		return
	}
	t.Fatal("no retained_bytes_peak ranking")
}
