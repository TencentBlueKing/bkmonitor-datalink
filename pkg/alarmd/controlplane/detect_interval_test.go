// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// withItemField sets one key of the document's item.
func withItemField(t *testing.T, document json.RawMessage, key string, value any) json.RawMessage {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(document, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["items"].([]any)[0].(map[string]any)[key] = value
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func detectIntervalCatalog(t *testing.T, document json.RawMessage) controlplane.Catalog {
	t.Helper()
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
		Strategies: []controlplane.SourceStrategy{{SourceID: "300", Document: document,
			Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
		Planner: planner,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.QueryGroups) != 1 || len(catalog.QueryGroups[0].Plans) != 1 {
		t.Fatalf("the strategy did not become one Plan: %+v", catalog.Dispositions)
	}
	return catalog
}

// executionBytes is everything an item's execution is identified by: the
// Query Group, its query, its object digest, its schedule and the Plan's
// strategy IR and requirements. Compared as bytes, not as "equal enough".
func executionBytes(t *testing.T, catalog controlplane.Catalog) []byte {
	t.Helper()
	group := catalog.QueryGroups[0]
	digest, err := controlplane.DeriveQueryGroupObjectDigest(group)
	if err != nil {
		t.Fatal(err)
	}
	plan := group.Plans[0]
	encoded, err := json.Marshal(struct {
		Identity     execution.QueryGroupIdentity
		Query        execution.QueryPlanFacts
		Object       execution.ObjectDigest
		Schedule     execution.ScheduleRevision
		PlanSchedule execution.PlanScheduleRevision
		IR           any
		Requirements any
	}{group.Identity, group.QueryPlan, digest, group.ScheduleRevision, plan.ScheduleRevision, plan.Plan.StrategyIR, plan.RequirementTemplates})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func warningsOf(catalog controlplane.Catalog) []string {
	var reasons []string
	for _, disposition := range catalog.Dispositions {
		if disposition.Disposition == controlplane.DispositionConfigNormalized && disposition.FieldPath == "items[0].detect_interval" {
			reasons = append(reasons, disposition.Reason)
		}
	}
	return reasons
}

// A detect_interval that changes nothing - equal to the aggregation interval,
// or one this build runs at the aggregation interval - compiles to the same
// bytes as an item without the field: no Query Group moves, no Segment is
// cut, no state is reset. What it does say, it says as a warning.
func TestADetectIntervalThatChangesNothingCompilesByteForByteAsNone(t *testing.T) {
	ringRatio := g4LegacyStrategyDocument(t, 300, strategy.DetectorKindSimpleRingRatio, "usage", "system.cpu", []string{"host"},
		map[string]any{"floor": 50, "ceil": nil})
	ringRatio = withItemField(t, ringRatio, "time_delay", 30)
	advanced := g4LegacyStrategyDocument(t, 300, strategy.DetectorKindAdvancedRingRatio, "usage", "system.cpu", []string{"host"},
		map[string]any{"ceil": 20, "ceil_interval": 3, "fetch_type": "last"})
	for _, test := range []struct {
		name     string
		base     json.RawMessage
		value    any
		warnings []string
	}{
		{name: "equal to the aggregation interval", base: ringRatio, value: 60},
		{name: "null", base: ringRatio, value: nil},
		{name: "an algorithm that needs history a step apart", base: advanced, value: 15, warnings: []string{controlplane.ReasonDetectIntervalAlgorithmNotSliding}},
	} {
		t.Run(test.name, func(t *testing.T) {
			without := detectIntervalCatalog(t, test.base)
			with := detectIntervalCatalog(t, withItemField(t, test.base, "detect_interval", test.value))
			if got, want := executionBytes(t, with), executionBytes(t, without); !bytes.Equal(got, want) {
				t.Fatalf("the field changed what executes:\nwith    %s\nwithout %s", got, want)
			}
			if got := warningsOf(with); !reflect.DeepEqual(got, test.warnings) {
				t.Fatalf("warnings %v, want %v", got, test.warnings)
			}
		})
	}
}

// A step a quarter of the aggregation interval: the schedule, the trigger and
// the previous point are a step; the query and every window it reads are the
// aggregation interval, read from where the request starts.
func TestAConfiguredStepRunsTheItemAtTheStepOverAggregationWindows(t *testing.T) {
	document := g4LegacyStrategyDocument(t, 300, strategy.DetectorKindSimpleRingRatio, "usage", "system.cpu", []string{"host"},
		map[string]any{"floor": 50, "ceil": nil})
	document = withItemField(t, document, "time_delay", 30)
	without := detectIntervalCatalog(t, document)
	with := detectIntervalCatalog(t, withItemField(t, document, "detect_interval", 15))

	plan := with.QueryGroups[0].Plans[0]
	semantics := plan.Plan.StrategyIR.ExecutionSemantics
	if plan.ScheduleSpec.EvaluationIntervalSeconds != 15 || semantics.EvaluationInterval != 15 || semantics.AggregationInterval != 60 ||
		semantics.QueryWindow != 60 || semantics.LatenessTolerance != 30 {
		t.Fatalf("schedule %+v semantics %+v, want a 15 s step over 60 s windows", plan.ScheduleSpec, semantics)
	}
	var trigger struct {
		StepSeconds int64 `json:"step_seconds"`
	}
	if err := json.Unmarshal(plan.Plan.StrategyIR.Levels[0].TriggerPlan.Config, &trigger); err != nil || trigger.StepSeconds != 15 {
		t.Fatalf("trigger step %d (%v), want 15", trigger.StepSeconds, err)
	}
	// The delay of 30 s is a whole number of steps, so it stays 30 s; without
	// the field it rounds up to the aggregation interval.
	primary, previous := plan.RequirementTemplates[0], plan.RequirementTemplates[1]
	if primary.RelativeWindow.StartOffsetSeconds != -90 || primary.RelativeWindow.EndOffsetSeconds != -30 ||
		previous.RelativeWindow.StartOffsetSeconds != -105 || previous.RelativeWindow.EndOffsetSeconds != -45 ||
		!reflect.DeepEqual(previous.PointOffsetsSeconds, []int64{15}) ||
		!reflect.DeepEqual(previous.NamedPoints, []execution.NamedInputPoint{{Name: "previous", OffsetSeconds: 15}}) {
		t.Fatalf("requirements %+v, want the primary over [-90,-30) and the previous detection over [-105,-45)", plan.RequirementTemplates)
	}
	query := with.QueryGroups[0].QueryPlan
	if !query.NotTimeAlign || query.QueryDelaySeconds != 30 || query.StepMillis != 60_000 || query.AlignmentMillis != 60_000 {
		t.Fatalf("query %+v, want unaligned 60 s buckets read 30 s late", query)
	}
	for _, clause := range query.QueryList {
		if clause.Offset != "59999ms" || clause.OffsetForward != "true" || clause.TimeAggregation.Window != "60s" {
			t.Fatalf("clause %+v, want a 60 s window labelled at its start by a 59999 ms forward shift", clause)
		}
	}
	base := without.QueryGroups[0].QueryPlan
	if base.NotTimeAlign || base.QueryDelaySeconds != 60 || base.QueryList[0].Offset != "" {
		t.Fatalf("the item without the field read %+v, want today's aligned query 60 s late", base)
	}
	if with.QueryGroups[0].Identity == without.QueryGroups[0].Identity {
		t.Fatal("a configured step kept the Query Group of the item without it")
	}
	if got := warningsOf(with); len(got) != 0 {
		t.Fatalf("a dividing step was warned: %v", got)
	}
}

// A detect_interval nothing can run on is refused by name, and the item keeps
// its last good Plan as any rejected configuration does.
func TestADetectIntervalNothingCanRunOnIsRefusedByName(t *testing.T) {
	document := g4LegacyStrategyDocument(t, 300, strategy.DetectorKindSimpleRingRatio, "usage", "system.cpu", []string{"host"},
		map[string]any{"floor": 50, "ceil": nil})
	for _, value := range []any{0, -15, 1.5, "15"} {
		planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := controlplane.BuildCatalog(context.Background(), controlplane.BuildRequest{
			Strategies: []controlplane.SourceStrategy{{SourceID: "300", Document: withItemField(t, document, "detect_interval", value),
				Identity: controlplane.SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}}},
			Planner: planner,
		})
		if err != nil {
			t.Fatal(err)
		}
		refused := false
		for _, disposition := range catalog.Dispositions {
			refused = refused || disposition.Disposition == controlplane.DispositionConfigRejected && disposition.Reason == controlplane.ReasonDetectIntervalInvalid &&
				disposition.FieldPath == "items[0].detect_interval"
		}
		if len(catalog.QueryGroups) != 0 || !refused {
			t.Fatalf("detect_interval %v: groups %d dispositions %+v, want refused as %s", value, len(catalog.QueryGroups), catalog.Dispositions, controlplane.ReasonDetectIntervalInvalid)
		}
	}
}

// The item as the strategy writer projects it: the step only when one is
// declared, beside a target plan that carries the writer's empty exclusion
// list. Both decode, the target plan is kept, and the step runs.
func TestTheWritersProjectedItemRunsItsDeclaredStep(t *testing.T) {
	document := g4LegacyStrategyDocument(t, 300, strategy.DetectorKindThreshold, "usage", "system.cpu", []string{"bk_host_id"},
		[]any{[]any{map[string]any{"method": "gte", "threshold": 80}}})
	document = withItemField(t, document, "target_plan", map[string]any{
		"schema_version": 1, "model_id": "cw-Host", "target_rule": "host_id", "failure_policy": "no_match",
		"static_targets": []any{map[string]any{"bk_host_id": 3}}, "dynamic_groups": []any{}, "dynamic_topologies": []any{}, "exclude": []any{},
	})
	without := detectIntervalCatalog(t, document)
	with := detectIntervalCatalog(t, withItemField(t, document, "detect_interval", 30))
	plan := with.QueryGroups[0].Plans[0]
	if plan.Plan.TargetPlan == nil || without.QueryGroups[0].Plans[0].Plan.TargetPlan == nil {
		t.Fatal("the projected target plan was not kept")
	}
	if plan.ScheduleSpec.EvaluationIntervalSeconds != 30 || without.QueryGroups[0].Plans[0].ScheduleSpec.EvaluationIntervalSeconds != 60 {
		t.Fatalf("schedules %d and %d, want the declared 30 s step and the item's 60 s without it",
			plan.ScheduleSpec.EvaluationIntervalSeconds, without.QueryGroups[0].Plans[0].ScheduleSpec.EvaluationIntervalSeconds)
	}
}

// OsRestart compares with its previous detection and with points 10 and 25
// minutes back, in that order. A step under 10 minutes keeps the order and
// runs; a step of 10 minutes or more would put the previous detection at or
// past the fixed points, and the item runs at its aggregation interval,
// byte for byte as without the field, naming the algorithm.
func TestAnOsRestartStepRunsOnlyWhileItsPreviousDetectionIsTheNearestPoint(t *testing.T) {
	document := g4LegacyStrategyDocument(t, 300, strategy.DetectorKindOsRestart, "uptime", "system.env", []string{"bk_target_ip"}, map[string]any{})
	plan := detectIntervalCatalog(t, withItemField(t, document, "detect_interval", 30)).QueryGroups[0].Plans[0]
	found := false
	for _, requirement := range plan.RequirementTemplates {
		if requirement.DatasetName == "uptime_history" {
			found = reflect.DeepEqual(requirement.PointOffsetsSeconds, []int64{30, 600, 1500})
		}
	}
	if plan.ScheduleSpec.EvaluationIntervalSeconds != 30 || !found {
		t.Fatalf("schedule %+v requirements %+v, want a 30 s step comparing 30 s, 10 and 25 minutes back", plan.ScheduleSpec, plan.RequirementTemplates)
	}
	without := detectIntervalCatalog(t, document)
	for _, step := range []int{600, 900, 3600} {
		with := detectIntervalCatalog(t, withItemField(t, document, "detect_interval", step))
		if got, want := executionBytes(t, with), executionBytes(t, without); !bytes.Equal(got, want) {
			t.Fatalf("step %d changed what executes", step)
		}
		if got := warningsOf(with); !reflect.DeepEqual(got, []string{controlplane.ReasonDetectIntervalAlgorithmNotSliding}) {
			t.Fatalf("step %d: warnings %v, want the algorithm named", step, got)
		}
	}
}
