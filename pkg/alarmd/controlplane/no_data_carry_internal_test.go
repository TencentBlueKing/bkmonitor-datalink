// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// noDataCarryVersion is one version of a Plan as the no-data carry reads it:
// a CPU query grouped by host, projected and identified by the host pair,
// with no-data on and a Level that ends on no data.
func noDataCarryVersion() noDataCarrySide {
	return noDataCarrySide{
		queries: map[execution.LogicalQueryRef]execution.QueryPlanFacts{"a": {QueryList: []execution.QueryClause{{
			TableID: "system.cpu", FieldName: "usage", Dimensions: []string{"bk_target_cloud_id", "bk_target_ip"},
		}}}},
		projection: contract.InputProjectionV2{ValueFields: []string{"usage"}, DimensionFields: []string{"bk_target_cloud_id", "bk_target_ip"}},
		identity:   &contract.MonitorOutputIdentity{DimensionFields: []string{"bk_target_cloud_id", "bk_target_ip"}},
		noData: &contract.NoDataConfigV1{Continuous: 5, Level: 2, AggDimension: []string{"bk_target_cloud_id", "bk_target_ip"},
			TrackingHorizonSeconds: 86400},
		endsOnNoData: true,
	}
}

// The no-data state is carried exactly when the two versions mean the same
// thing by it, decided from the versions alone. The same query, projection
// and identity carry the end index; the memory needs the same no-data
// section on top. Every difference below is one an edit makes, and each
// refuses what it touches.
func TestTheNoDataCarryIsDecidedFromTheTwoVersionsAlone(t *testing.T) {
	for name, test := range map[string]struct {
		edit    func(*noDataCarrySide)
		carry   *execution.NoDataCarry
		outcome string
	}{
		"a detection edit": {edit: func(*noDataCarrySide) {},
			carry: &execution.NoDataCarry{From: "old", Memory: true, Ends: true}, outcome: NoDataCarryCarried},
		"another metric": {edit: func(side *noDataCarrySide) {
			side.queries = map[execution.LogicalQueryRef]execution.QueryPlanFacts{"a": {QueryList: []execution.QueryClause{{
				TableID: "system.mem", FieldName: "usage", Dimensions: []string{"bk_target_cloud_id", "bk_target_ip"}}}}}
		}, outcome: NoDataCarrySemanticsChanged},
		"another condition": {edit: func(side *noDataCarrySide) {
			side.queries = map[execution.LogicalQueryRef]execution.QueryPlanFacts{"a": {QueryList: []execution.QueryClause{{
				TableID: "system.cpu", FieldName: "usage", Dimensions: []string{"bk_target_cloud_id", "bk_target_ip"},
				Conditions: execution.QueryConditions{Fields: []execution.QueryConditionField{{Field: "device", Operator: "eq"}}}}}}}
		}, outcome: NoDataCarrySemanticsChanged},
		"another projection": {edit: func(side *noDataCarrySide) {
			side.projection.DimensionFields = []string{"bk_target_cloud_id", "bk_target_ip", "device"}
		}, outcome: NoDataCarrySemanticsChanged},
		"another alert identity": {edit: func(side *noDataCarrySide) {
			side.identity = &contract.MonitorOutputIdentity{DimensionFields: []string{"bk_target_ip"}}
		}, outcome: NoDataCarrySemanticsChanged},
		"another no-data section": {edit: func(side *noDataCarrySide) {
			changed := *side.noData
			changed.Continuous = 2
			side.noData = &changed
		}, carry: &execution.NoDataCarry{From: "old", Ends: true}, outcome: NoDataCarryEndsOnly},
		"no-data turned on": {edit: func(side *noDataCarrySide) {}, carry: &execution.NoDataCarry{From: "old", Ends: true},
			outcome: NoDataCarryEndsOnly},
	} {
		t.Run(name, func(t *testing.T) {
			before, after := noDataCarryVersion(), noDataCarryVersion()
			test.edit(&after)
			if name == "no-data turned on" {
				before.noData = nil
			}
			carry, outcome := decideNoDataCarry("old", before, after)
			if outcome != test.outcome {
				t.Fatalf("outcome = %q, want %q", outcome, test.outcome)
			}
			if (carry == nil) != (test.carry == nil) || (carry != nil && *carry != *test.carry) {
				t.Fatalf("carry = %+v, want %+v", carry, test.carry)
			}
		})
	}
}

// A version that keeps no no-data state says nothing; one that keeps only an
// end index carries it alone, and only while the query is the same.
func TestTheNoDataCarryCoversOnlyTheStateTheNewVersionKeeps(t *testing.T) {
	before, after := noDataCarryVersion(), noDataCarryVersion()
	after.noData, after.endsOnNoData = nil, false
	if carry, outcome := decideNoDataCarry("old", before, after); carry != nil || outcome != "" {
		t.Fatalf("a version without no-data state = %+v, %q; want nothing said", carry, outcome)
	}
	before.noData, after.endsOnNoData = nil, true
	if carry, outcome := decideNoDataCarry("old", before, after); carry == nil || carry.Memory || !carry.Ends || outcome != NoDataCarryCarried {
		t.Fatalf("a threshold Plan ending on no data = %+v, %q; want its end index carried", carry, outcome)
	}
	after.identity = nil
	if carry, outcome := decideNoDataCarry("old", before, after); carry != nil || outcome != "" {
		t.Fatalf("an end index with no alert identity = %+v, %q; none is kept, so nothing is said", carry, outcome)
	}
}

// A no-data carry is valid only on a warming activation whose generation
// moved, and only when it carries something.
func TestANoDataCarryIsValidOnlyOnAMovedWarmingActivation(t *testing.T) {
	plan := execution.ActivatedPlan{StateGeneration: "new", ForceWarming: true}
	for name, carry := range map[string]*execution.NoDataCarry{
		"no previous generation": {Memory: true},
		"the same generation":    {From: "new", Memory: true},
		"nothing carried":        {From: "old"},
	} {
		if err := carry.Validate(plan); err == nil {
			t.Fatalf("%s: carry %+v validated", name, carry)
		}
	}
	if err := (&execution.NoDataCarry{From: "old", Memory: true}).Validate(execution.ActivatedPlan{StateGeneration: "new"}); err == nil {
		t.Fatal("a carry on an activation that does not warm validated")
	}
	if err := (&execution.NoDataCarry{From: "old", Ends: true}).Validate(plan); err != nil {
		t.Fatalf("a valid carry was refused: %v", err)
	}
}

// The Leader's words are the ones the metric pre-creates and the normalizer
// keeps.
func TestTheNoDataCarryWordsAreTheObservedVocabulary(t *testing.T) {
	if got := observability.StateCarryResults["no_data_plan"]; !reflect.DeepEqual(got, NoDataCarryOutcomes) {
		t.Fatalf("observed no_data_plan results = %v, want %v", got, NoDataCarryOutcomes)
	}
}

// A decided no-data carry is put on its record, unless the record's Query
// Group is back from retirement.
func TestAReturningQueryGroupCarriesNoNoDataStateAcrossItsHole(t *testing.T) {
	records := []PlanActivationRecord{{}, {}}
	carry := &execution.NoDataCarry{From: "old", Memory: true}
	applyNoDataCarries(records, map[int]*execution.NoDataCarry{0: carry, 1: carry}, map[int]struct{}{1: {}})
	if records[0].Fact.Selected.NoDataCarry != carry || records[1].Fact.Selected.NoDataCarry != nil {
		t.Fatalf("records = %+v, want the carry on the first and none on the returning one", records)
	}
}
