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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func factsPlan(tenant string, revision int64, fields []string, levels ...uint32) FrozenPlan {
	plan := FrozenPlan{Identity: execution.PlanIdentity{TenantID: tenant, BusinessID: "2", StrategyID: "7"}}
	plan.Plan.StrategyRef.SnapshotRevision = revision
	plan.Plan.OutputIdentity = &contract.MonitorOutputIdentity{DimensionFields: fields}
	for _, level := range levels {
		plan.Plan.StrategyIR.Levels = append(plan.Plan.StrategyIR.Levels, contract.LevelIRV2{Definition: contract.LevelDefinitionV2{LevelID: level}})
	}
	return plan
}

// The facts are the frozen Plan's: its snapshot revision, its output
// identity's dimensions, its Levels and its no-data grouping.
func TestStrategyPlanFactsAreTheFrozenPlans(t *testing.T) {
	plan := factsPlan("t", 9, []string{"ip", "device"}, 2, 1)
	plan.Plan.NoData = &contract.NoDataConfigV1{Continuous: 5, AggDimension: []string{"ip"}, Level: 2}
	plan.Plan.StrategyFacts = &contract.StrategyFactsV2{Schema: "v2", MetricDigest: "m", ConditionMD5: "c"}
	plan.Plan.InputProjection.DimensionFields = []string{"port", "ip"}
	index := buildStrategyIndex(SnapshotPublicationRef{SnapshotRevision: "rev"}, []QueryGroup{{Identity: "qg", Plans: []FrozenPlan{plan}}},
		[]ObjectDisposition{{SourceID: "7", Scope: "PLAN", Disposition: DispositionAccepted}})
	facts, found := index.planFacts("t", "7")
	want := StrategyPlanFacts{Revision: 9, DimensionFields: []string{"device", "ip"}, HasOutputIdentity: true,
		RecordDimensionFields: []string{"ip", "port"},
		Levels:                []contract.LevelDefinitionV2{{LevelID: 1}, {LevelID: 2}}, LevelsWhole: true,
		NoData: true, NoDataAggDimension: []string{"ip"}, Agree: true,
		StrategyFacts: &contract.StrategyFactsV2{Schema: "v2", MetricDigest: "m", ConditionMD5: "c"}}
	if !found || !reflect.DeepEqual(facts, want) {
		t.Fatalf("facts %+v found %v, want %+v", facts, found, want)
	}
	if _, found := index.planFacts("other", "7"); found {
		t.Fatal("another tenant's strategy was answered with this tenant's Plan")
	}
}

// What makes an answer partial is said: a Level refused, the Plans retained,
// Plans that disagree, a no-data half suspended.
func TestStrategyPlanFactsSayWhatIsPartial(t *testing.T) {
	refused := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "qg", Plans: []FrozenPlan{factsPlan("t", 9, nil, 1)}}},
		[]ObjectDisposition{{SourceID: "7", Scope: "LEVEL", LevelID: 2, Disposition: DispositionUnsupported, Reason: "ALGORITHM_NOT_MIGRATED"}})
	if facts, _ := refused.planFacts("t", "7"); facts.LevelsWhole {
		t.Fatal("a strategy with a refused Level read as detecting every Level it declares")
	}
	noted := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "qg", Plans: []FrozenPlan{factsPlan("t", 9, nil, 1)}}},
		[]ObjectDisposition{{SourceID: "7", Scope: "LEVEL", LevelID: 1, Disposition: DispositionConfigNoted}})
	if facts, _ := noted.planFacts("t", "7"); !facts.LevelsWhole {
		t.Fatal("a Level carrying only a note read as refused")
	}
	retained := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "qg", Plans: []FrozenPlan{factsPlan("t", 9, nil, 1)}}},
		[]ObjectDisposition{{SourceID: "7", Scope: "PLAN", Disposition: DispositionStaleConfig}})
	if facts, _ := retained.planFacts("t", "7"); !facts.Retained {
		t.Fatal("a retained Plan read as the current definition")
	}
	split := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "a", Plans: []FrozenPlan{factsPlan("t", 9, []string{"ip"}, 1)}},
		{Identity: "b", Plans: []FrozenPlan{factsPlan("t", 9, []string{"device"}, 1)}}}, nil)
	if facts, _ := split.planFacts("t", "7"); facts.Agree {
		t.Fatal("Plans with different dimensions read as agreeing")
	}
	suspended := factsPlan("t", 9, nil, 1)
	suspended.NoDataSuspended = contract.ReasonNoDataRosterUnsupported
	held := buildStrategyIndex(SnapshotPublicationRef{}, []QueryGroup{{Identity: "qg", Plans: []FrozenPlan{suspended}}}, nil)
	if facts, _ := held.planFacts("t", "7"); facts.NoData || !facts.NoDataSuspended {
		t.Fatalf("facts %+v, want no-data configured and suspended", facts)
	}
}

// A process without a publication of its own answers unavailable, not "no
// such strategy".
func TestStrategyPlanFactsWithoutAPublicationAreUnavailable(t *testing.T) {
	reconciler := &SourceReconciler{}
	if _, available, found := reconciler.StrategyPlanFacts("t", "7"); available || found {
		t.Fatal("a process without a publication answered")
	}
	reconciler.strategies.replace(buildStrategyIndex(SnapshotPublicationRef{}, nil, nil))
	if _, available, found := reconciler.StrategyPlanFacts("t", "7"); !available || found {
		t.Fatalf("available %v found %v, want a publication without the strategy", available, found)
	}
}
