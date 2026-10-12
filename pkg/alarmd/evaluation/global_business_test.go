// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package evaluation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// clusterTarget is a Kubernetes cluster target whose one static target is
// configured with business 41.
func clusterTarget() *contract.TargetPlanV1 {
	return &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-K8s_Cluster", Rule: contract.TargetPlanRuleK8sCluster,
		Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bcs_cluster_id"}}, StaticKeys: []string{"cluster-a"},
		StaticBusinesses: map[string]string{"cluster-a": "41"}}
}

func withTarget(global bool) func(*contract.EvaluationPlanV2) {
	return func(plan *contract.EvaluationPlanV2) {
		plan.TargetPlan, plan.GlobalBusiness = clusterTarget(), global
	}
}

func recordingAttribution() (func(string), *[]string) {
	var sources []string
	return func(source string) { sources = append(sources, source) }, &sources
}

func resultEvents(result execution.EvaluationResult) []contract.TriggerEventV1 {
	var events []contract.TriggerEventV1
	for _, plan := range result.Plans {
		for _, state := range plan.StateResults {
			events = append(events, state.Events...)
		}
	}
	return events
}

func clusterRecord(value string) []contract.CanonicalRecordV2 {
	return []contract.CanonicalRecordV2{{RecordID: strings.Repeat("b", 64), SourceTime: 100, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(value)},
		Dimensions:        map[string]json.RawMessage{"host": json.RawMessage(`"h1"`), "bcs_cluster_id": json.RawMessage(`"cluster-a"`)},
		ReceivedTime:      100}}
}

// A global business Plan's threshold event is attributed where it is built,
// and the attribution is counted by its source. The event's own business -
// its identity - stays the Plan's.
func TestAGlobalBusinessThresholdEventIsAttributedWhereItIsBuilt(t *testing.T) {
	observe, sources := recordingAttribution()
	evaluator := newEvaluator(t).WithBusinessAttribution(admission.BusinessLookups{}, observe)
	plan := compiledWindowEdited(t, 1, 1, nil, false, withTarget(true))
	result, err := evaluator.Evaluate(context.Background(), requestFixtureForPlan(t, plan, clusterRecord(`60`), nil))
	if err != nil {
		t.Fatal(err)
	}
	events := resultEvents(result)
	if len(events) != 1 {
		t.Fatalf("events = %d, want the one abnormal event", len(events))
	}
	if events[0].AttributedBusinessID != "41" || events[0].BusinessID != "2" {
		t.Fatalf("event business %q attributed %q, want the Plan's 2 attributed to 41", events[0].BusinessID, events[0].AttributedBusinessID)
	}
	if len(*sources) != 1 || (*sources)[0] != contract.BusinessAttributionTarget {
		t.Fatalf("attributions counted %v, want one from the target", *sources)
	}
}

// The ordinary arm: the same Plan and record without the global flag build
// an event with no attribution, and nothing is counted.
func TestAnOrdinaryPlansEventIsNotAttributed(t *testing.T) {
	observe, sources := recordingAttribution()
	evaluator := newEvaluator(t).WithBusinessAttribution(admission.BusinessLookups{}, observe)
	plan := compiledWindowEdited(t, 1, 1, nil, false, withTarget(false))
	result, err := evaluator.Evaluate(context.Background(), requestFixtureForPlan(t, plan, clusterRecord(`60`), nil))
	if err != nil {
		t.Fatal(err)
	}
	events := resultEvents(result)
	if len(events) != 1 || events[0].AttributedBusinessID != "" || len(*sources) != 0 {
		t.Fatalf("events %+v, counted %v, want one unattributed event and no count", events, *sources)
	}
}

// The switch is the strategy's and says nothing about its business: a
// strategy in an ordinary business with the switch on, whose event names no
// target, no business and no cluster, is filed under the strategy's own
// business, and counted as the fallback it is.
func TestASwitchedOnStrategyWithNothingToAttributeKeepsItsOwnBusiness(t *testing.T) {
	observe, sources := recordingAttribution()
	evaluator := newEvaluator(t).WithBusinessAttribution(admission.BusinessLookups{}, observe)
	plan := compiledWindowEdited(t, 1, 1, nil, false, func(plan *contract.EvaluationPlanV2) { plan.GlobalBusiness = true })
	record := clusterRecord(`60`)
	delete(record[0].Dimensions, "bcs_cluster_id")
	result, err := evaluator.Evaluate(context.Background(), requestFixtureForPlan(t, plan, record, nil))
	if err != nil {
		t.Fatal(err)
	}
	events := resultEvents(result)
	if len(events) != 1 || events[0].BusinessID != "2" || events[0].AttributedBusinessID != "2" {
		t.Fatalf("events %+v, want one event of business 2 attributed to its own business 2", events)
	}
	if len(*sources) != 1 || (*sources)[0] != contract.BusinessAttributionGlobal {
		t.Fatalf("attributions counted %v, want one fallback", *sources)
	}
}

// A no-data event on a roster group goes through the same construction and
// the same attribution: the group's static target names its business.
func TestAGlobalBusinessNoDataEventIsAttributedToItsGroupsTarget(t *testing.T) {
	observe, sources := recordingAttribution()
	evaluator := newEvaluator(t).WithBusinessAttribution(admission.BusinessLookups{}, observe)
	plan := compiledWindowEdited(t, 1, 1, &contract.NoDataConfigV1{Continuous: 1, Level: 2}, false, withTarget(true))
	result, err := evaluator.Evaluate(context.Background(),
		noDataRequestFixtureOnGroup(t, plan, 1, 100, map[string]string{"bcs_cluster_id": "cluster-a"}))
	if err != nil {
		t.Fatal(err)
	}
	events := resultEvents(result)
	if len(events) != 1 || events[0].AttributedBusinessID != "41" {
		t.Fatalf("events %+v, want one no-data event attributed to 41", events)
	}
	if len(*sources) != 1 || (*sources)[0] != contract.BusinessAttributionTarget {
		t.Fatalf("attributions counted %v, want one from the target", *sources)
	}
}
