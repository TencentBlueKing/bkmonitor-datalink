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
	"encoding/json"
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

	levels := view.Levels().Copy()
	if len(levels) != 1 {
		t.Fatalf("view levels = %+v, want only the no-data level", levels)
	}
	if levels[0].Definition().LevelID != plan.NoDataLevel().Definition().LevelID {
		t.Fatalf("view level = %d, want the no-data level %d",
			levels[0].Definition().LevelID, plan.NoDataLevel().Definition().LevelID)
	}
	for _, declared := range plan.Levels().All() {
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
	if plan.Levels().Len() == 0 {
		t.Fatal("building a view emptied the Plan's own levels")
	}
}

// A Plan that does not detect no-data has no such view, rather than an empty
// one. An empty view would be a Plan with no levels, which every reader
// downstream would treat as "nothing to evaluate" and report as success.
func TestAPlanWithoutNoDataHasNoView(t *testing.T) {
	if view := noDataPlan(t, nil).NoDataView(); view != nil {
		t.Fatalf("a Plan that detects no no-data produced a view with %d levels", view.Levels().Len())
	}
	var absent *CompiledPlan
	if view := absent.NoDataView(); view != nil {
		t.Fatal("a nil Plan produced a view")
	}
}

// The no-data view's alert identity reads the record's own dimensions, as the
// backend does: its adapter hands extract_target the event's dimension_fields
// (adapter.py:109-111), which for a no-data record are the group's
// dimensions and the tag (nodata.py:259), not the item's. A Plan whose
// identity names the host's IP therefore still files a topology or
// service-instance no-data group under that object, where reading the Plan's
// fields stopped at an empty host target.
//
// The expected keys are the backend's: extract_target and cal_dedupe_md5
// (event.py:205-217) with count_md5, for strategy 9 in business 2, written
// out rather than recomputed here.
func TestTheNoDataViewsAlertIdentityIsTheGroupsAsTheBackendReadsIt(t *testing.T) {
	plan := validPlan()
	plan.NoData = &contract.NoDataConfigV1{Continuous: 3, Level: 2}
	plan.OutputIdentity = &contract.MonitorOutputIdentity{DimensionFields: []string{"bk_target_cloud_id", "bk_target_ip"}}
	compiled := mustCompilePlan(t, newTestCompiler(t), plan)
	identity := compiled.NoDataView().OutputIdentity()
	if identity == nil {
		t.Fatal("the no-data view lost the Plan's output identity")
	}
	tag := json.RawMessage("true")
	for name, test := range map[string]struct {
		dimensions map[string]json.RawMessage
		want       string
	}{
		"a topology group":         {map[string]json.RawMessage{"bk_obj_id": json.RawMessage(`"set"`), "bk_inst_id": json.RawMessage(`"7"`), contract.NoDataDimensionTag: tag}, "ad65665afc0d09e0815bafe7ea3d5403"},
		"a service-instance group": {map[string]json.RawMessage{"bk_target_service_instance_id": json.RawMessage(`"15"`), contract.NoDataDimensionTag: tag}, "eb2ed0783e9881a0d141d8b4b854fba1"},
		"a host group":             {map[string]json.RawMessage{"bk_target_ip": json.RawMessage(`"192.0.2.1"`), "bk_target_cloud_id": json.RawMessage(`"0"`), contract.NoDataDimensionTag: tag}, "14056f88e6c665534beba9f53aadb7fe"},
		"the whole item":           {map[string]json.RawMessage{contract.NoDataDimensionTag: tag}, "64192251d1beca857fb67c521b7bf32c"},
	} {
		got, err := contract.MonitorDedupeMD5("9", "2", test.dimensions, *identity)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != test.want {
			t.Errorf("%s files under %s, want the backend's %s", name, got, test.want)
		}
	}
	// The Plan's own identity is unchanged: its threshold series are still
	// filed by the item's fields.
	if own := compiled.OutputIdentity(); own.DynamicDimensions || len(own.DimensionFields) != 2 {
		t.Fatalf("the Plan's own identity = %+v, want the item's two fields", own)
	}
	// And a Plan with no output identity -- one that publishes no alert key,
	// the compatible protocol's -- has a view with none: the view reads the
	// record for a key the Plan has, it does not give one to a Plan without.
	plan.OutputIdentity = nil
	if identity := mustCompilePlan(t, newTestCompiler(t), plan).NoDataView().OutputIdentity(); identity != nil {
		t.Fatalf("a Plan without an output identity has a no-data view with %+v", identity)
	}
}
