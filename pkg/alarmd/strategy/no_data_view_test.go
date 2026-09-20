// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The no-data view is the same Plan with one level swapped in, and everything
// else about it is shared.
//
// Both halves are load-bearing. The levels have to be only the no-data one,
// because every reader downstream - the detector, the trigger, the execution
// contract's validators - asks the Plan what its levels are and evaluates
// against whatever it is told. And the rest has to be the Plan's own: the plan
// ref and the state compatibility hash key the runtime state, so a view that
// lost them would write a synthetic series' state under a Plan that does not
// exist, and the event it produced would name one too.
func TestTheNoDataViewIsTheSamePlanWithOneLevel(t *testing.T) {
	plan := noDataPlan(t, &contract.NoDataConfigV1{Continuous: 3, Level: 2})
	view := plan.NoDataView()
	if view == nil {
		t.Fatal("a Plan that detects no-data has no view of it")
	}

	levels := view.Levels()
	if len(levels) != 1 {
		t.Fatalf("view levels = %+v, want only the no-data level", levels)
	}
	if levels[0].Definition().LevelID != plan.NoDataLevel().Definition().LevelID {
		t.Fatalf("view level = %d, want the no-data level %d",
			levels[0].Definition().LevelID, plan.NoDataLevel().Definition().LevelID)
	}
	for _, declared := range plan.Levels() {
		for _, seen := range levels {
			if seen.Definition().LevelID == declared.Definition().LevelID {
				t.Fatalf("the view carries declared level %d, so a synthetic series would be asked "+
					"for data it does not have", declared.Definition().LevelID)
			}
		}
	}

	// The identity is shared. A synthetic series is this Plan's series; the two
	// are told apart by the series digest, never by the Plan.
	if view.PlanRef() != plan.PlanRef() {
		t.Fatalf("view plan ref = %+v, want the Plan's own %+v", view.PlanRef(), plan.PlanRef())
	}
	if view.Fingerprints() != plan.Fingerprints() {
		t.Fatalf("view fingerprints = %+v, want the Plan's own %+v", view.Fingerprints(), plan.Fingerprints())
	}
	if view.StrategyRef() != plan.StrategyRef() {
		t.Fatalf("view strategy ref = %+v, want the Plan's own", view.StrategyRef())
	}
	if view.EvaluationSemantics() != plan.EvaluationSemantics() {
		t.Fatalf("view semantics = %+v, want the Plan's own", view.EvaluationSemantics())
	}
	// And the view still knows it detects no-data, so asking it twice is stable.
	if view.NoDataLevel() == nil {
		t.Fatal("the view forgot which level it was built from")
	}

	// The Plan it came from is untouched: the view is a reading of it, not a
	// change to it.
	if len(plan.Levels()) == 0 {
		t.Fatal("building a view emptied the Plan's own levels")
	}
}

// A Plan that does not detect no-data has no such view, rather than an empty
// one. An empty view would be a Plan with no levels, which every reader
// downstream would treat as "nothing to evaluate" and report as success.
func TestAPlanWithoutNoDataHasNoView(t *testing.T) {
	if view := noDataPlan(t, nil).NoDataView(); view != nil {
		t.Fatalf("a Plan that detects no no-data produced a view with %d levels", len(view.Levels()))
	}
	var absent *CompiledPlan
	if view := absent.NoDataView(); view != nil {
		t.Fatal("a nil Plan produced a view")
	}
}
