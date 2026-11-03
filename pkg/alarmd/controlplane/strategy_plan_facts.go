// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"reflect"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// StrategyPlanFacts is what one strategy's Plans in the publication this
// process last made say about the alerts they raise: the revision they raise
// them under and the facts an alert raised under an earlier revision is
// judged against when the strategy was edited since.
//
// Read from the frozen Plans themselves, the same objects every Worker
// compiles and runs, so the answer is the strategy as it is detected now.
type StrategyPlanFacts struct {
	// Revision is the snapshot revision the Plans raise alerts under (the
	// alert's labels.strategy_version). Zero is a strategy whose Plans
	// publish no snapshot revision, whose alerts this answer cannot place.
	Revision int64
	// DimensionFields and DynamicDimensions are the Plans' output identity:
	// the dimensions an alert's fingerprint is built from
	// (contract.MonitorDedupeMD5). HasOutputIdentity is false for a Plan
	// that has none.
	DimensionFields   []string
	DynamicDimensions bool
	HasOutputIdentity bool
	// RecordDimensionFields are the dimensions the Plans' records carry
	// (InputProjection.DimensionFields), which reach the alert with the
	// opening event. Some reach beyond the output identity: ProcPort's
	// records carry the ports they found (bind_ip, listen, ...) and are
	// identified by the target and process alone.
	RecordDimensionFields []string
	// Levels are the declared Levels the Plans detect, in level order.
	// LevelsWhole says every Level the strategy declares is among them: no
	// Level of it was refused, withheld or kept from a previous definition.
	Levels      []contract.LevelDefinitionV2
	LevelsWhole bool
	// NoData says the Plans detect no-data, over NoDataAggDimension.
	// NoDataSuspended says the strategy configures no-data and the Plans do
	// not attach it (FrozenPlan.NoDataSuspended): configured, not switched
	// off.
	NoData             bool
	NoDataAggDimension []string
	NoDataSuspended    bool
	// StrategyFacts is the Plans' strategy facts
	// (contract.EvaluationPlanV2.StrategyFacts), nil where they have none.
	StrategyFacts *contract.StrategyFactsV2
	// Retained says the Plans are the last good ones kept in place of a
	// definition the round could not compile (StrategyLookup.Retained): what
	// runs is not the strategy's current definition.
	Retained bool
	// Agree says every Plan of the strategy under the tenant states the same
	// facts. A strategy whose Plans disagree has no one answer, and the
	// fields above are the first Plan's.
	Agree bool
}

// StrategyPlanFacts answers one strategy's facts under a tenant from the
// publication this process last made. available is false when this process
// holds no publication of its own (StrategyLookup.Available); found is false
// when the publication has no Plan of the strategy under the tenant. Memory
// only, safe from any goroutine.
func (reconciler *SourceReconciler) StrategyPlanFacts(tenantID, strategyID string) (facts StrategyPlanFacts, available, found bool) {
	index := reconciler.publishedIndex()
	if index == nil {
		return StrategyPlanFacts{}, false, false
	}
	facts, found = index.planFacts(tenantID, strategyID)
	return facts, true, found
}

func (index *strategyIndex) planFacts(tenantID, strategyID string) (StrategyPlanFacts, bool) {
	var first *StrategyPlanFacts
	agree := true
	for _, at := range index.plans[strategyID] {
		plan := &index.groups[at.group].Plans[at.plan]
		if plan.Identity.TenantID != tenantID {
			continue
		}
		facts := frozenPlanFacts(plan)
		if first == nil {
			first = &facts
			continue
		}
		if !reflect.DeepEqual(*first, facts) {
			agree = false
		}
	}
	if first == nil {
		return StrategyPlanFacts{}, false
	}
	facts := *first
	facts.Agree = agree
	facts.LevelsWhole = true
	for _, disposition := range index.dispositions[strategyID] {
		if retainingDisposition(disposition.Disposition) {
			facts.Retained = true
		}
		if disposition.Scope == "LEVEL" && disposition.Disposition != DispositionAccepted && disposition.Disposition != DispositionConfigNoted {
			facts.LevelsWhole = false
		}
	}
	return facts, true
}

func frozenPlanFacts(plan *FrozenPlan) StrategyPlanFacts {
	facts := StrategyPlanFacts{Revision: plan.Plan.StrategyRef.SnapshotRevision, NoDataSuspended: plan.NoDataSuspended != ""}
	if plan.Plan.StrategyFacts != nil {
		strategyFacts := *plan.Plan.StrategyFacts
		facts.StrategyFacts = &strategyFacts
	}
	if identity := plan.Plan.OutputIdentity; identity != nil {
		facts.HasOutputIdentity = true
		facts.DynamicDimensions = identity.DynamicDimensions
		facts.DimensionFields = sortedCopy(identity.DimensionFields)
	}
	facts.RecordDimensionFields = sortedCopy(plan.Plan.InputProjection.DimensionFields)
	for _, level := range plan.Plan.StrategyIR.Levels {
		facts.Levels = append(facts.Levels, level.Definition)
	}
	sort.Slice(facts.Levels, func(i, j int) bool { return facts.Levels[i].LevelID < facts.Levels[j].LevelID })
	if noData := plan.Plan.NoData; noData != nil {
		facts.NoData = true
		facts.NoDataAggDimension = sortedCopy(noData.AggDimension)
	}
	return facts
}

func sortedCopy(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	copied := append([]string(nil), values...)
	sort.Strings(copied)
	return copied
}
