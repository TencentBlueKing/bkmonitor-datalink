// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Plan is published by being taken apart into an execution object and an
// output context, and is read back by being put together again from the two.
// Both halves name their fields one at a time, so a field added to
// EvaluationPlanV2 and to neither list is silently absent from every Plan the
// fleet executes -- and absent is a legal value for most of them, so nothing
// fails: the leader's own counts are computed from the Catalog in its memory
// and look healthy, while every worker compiles a Plan without the field.
//
// That is not hypothetical. NoData was carried into the object and not copied
// back out, so every worker saw a Plan that does not detect no-data, the
// no-data outcome metric reported a computed zero on all four labels, and
// nothing anywhere logged an error. The point was only visible by reading the
// two lists against the struct.
//
// So this walks EvaluationPlanV2 by reflection rather than by a list of its
// fields: a fixture written out by hand has exactly the weakness it is
// checking for. Every field is filled with a non-zero value, the Plan goes
// through the same projection and the same canonical JSON the publisher
// writes, comes back through the same assembly the worker reads, and every
// field is compared by name.
func TestEveryEvaluationPlanFieldSurvivesPublishAndReadBack(t *testing.T) {
	original := fullyPopulatedFrozenPlan(t)

	assembled := publishAndReadBack(t, original)

	before := reflect.ValueOf(original.Plan)
	after := reflect.ValueOf(assembled.Plan)
	for index := 0; index < before.NumField(); index++ {
		field := before.Type().Field(index)
		if reason, excluded := planFieldsThatDoNotRoundTrip[field.Name]; excluded {
			t.Logf("field %s is not compared: %s", field.Name, reason)
			continue
		}
		want, got := before.Field(index).Interface(), after.Field(index).Interface()
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("field %s did not survive publish and read-back:\n  published %#v\n  read back %#v\n"+
				"A Plan is taken apart into a Query Group object and an output context and put back "+
				"together from the two; a field missing from either list is dropped without an error, "+
				"and most of these fields read as legally absent afterwards.", field.Name, want, got)
		}
	}
}

// planFieldsThatDoNotRoundTrip is every EvaluationPlanV2 field that is not
// expected to come back, with the reason it does not. It is empty: every field
// of a published Plan is execution content or output context, and both are
// stored. An entry added here is a decision that a field the compiler produced
// is not worth publishing, which needs a reason someone can read.
var planFieldsThatDoNotRoundTrip = map[string]string{}

// A Plan that does not detect no-data comes back not detecting no-data.
//
// The round-trip above fills every field, so it cannot tell a field that is
// copied from one that is filled in unconditionally. This is the other half:
// absence is a real state for these fields and has to survive too, because
// NoData's absence is what says the item does not detect no-data.
func TestAPlanWithNoOptionalSectionsComesBackWithNone(t *testing.T) {
	plan := FrozenPlan{
		Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "business-1", StrategyID: "1"},
		Plan: contract.EvaluationPlanV2{
			PlanID:      "plan-1",
			StrategyRef: contract.StrategyRefV2{TenantID: "tenant", StrategyID: "1"},
		},
	}

	assembled := publishAndReadBack(t, plan)

	if assembled.Plan.NoData != nil {
		t.Fatalf("NoData = %#v, want nil: the absence of the section is what says the item does not "+
			"detect no-data, so a read-back that invents one turns every item into a no-data item",
			assembled.Plan.NoData)
	}
	if assembled.Plan.TargetScope != nil || assembled.Plan.OutputIdentity != nil ||
		assembled.Plan.SubjectFacts != nil || assembled.Plan.LegacyOutput != nil ||
		assembled.Plan.SourceCompatibility != nil {
		t.Fatalf("an optional section came back present on a Plan that had none: %#v", assembled.Plan)
	}
}

// publishAndReadBack takes a Plan through the projection and canonical JSON the
// publisher writes, and back through the assembly a worker reads. Redis is not
// in the path because Redis stores the bytes this produces; what is in the path
// is every step that names a field.
func publishAndReadBack(t *testing.T, plan FrozenPlan) FrozenPlan {
	t.Helper()
	group := QueryGroup{
		Identity:         execution.QueryGroupIdentity("qg-1"),
		ScheduleRevision: execution.ScheduleRevision("rev-1"),
		MembershipDigest: "digest-1",
		Plans:            []FrozenPlan{plan},
	}

	objectBytes, err := contract.CanonicalJSONV2(BuildQueryGroupObject(group))
	if err != nil {
		t.Fatalf("encode Query Group object: %v", err)
	}
	contextBytes, err := contract.CanonicalJSONV2(BuildOutputContext(plan))
	if err != nil {
		t.Fatalf("encode output context: %v", err)
	}
	var object QueryGroupObject
	if err := json.Unmarshal(objectBytes, &object); err != nil {
		t.Fatalf("decode Query Group object: %v", err)
	}
	var outputContext OutputContextObject
	if err := json.Unmarshal(contextBytes, &outputContext); err != nil {
		t.Fatalf("decode output context: %v", err)
	}

	assembled, err := AssembleQueryGroup(object, map[execution.PlanIdentity]OutputContextObject{
		plan.Identity: outputContext,
	})
	if err != nil {
		t.Fatalf("assemble Query Group: %v", err)
	}
	if len(assembled.Plans) != 1 {
		t.Fatalf("assembled %d Plans, want the one that was published", len(assembled.Plans))
	}
	return assembled.Plans[0]
}

// fullyPopulatedFrozenPlan builds a Plan whose EvaluationPlanV2 has no zero
// field, by reflection.
//
// By reflection and not by hand: a hand-written fixture has to be updated when
// a field is added, which is the same thing the publisher and the reader have
// to do, and a guard that fails the same way as the thing it guards is not a
// guard. The identity is set afterwards because the assembly matches the Plan
// to its output context by it.
func fullyPopulatedFrozenPlan(t *testing.T) FrozenPlan {
	t.Helper()
	var plan contract.EvaluationPlanV2
	value := reflect.ValueOf(&plan).Elem()
	fillNonZero(t, value, "EvaluationPlanV2", 0)
	if zero := zeroFieldsOf(value); len(zero) > 0 {
		t.Fatalf("the fixture left %s at its zero value; a field this filler cannot populate is a field "+
			"the round-trip cannot check, so the filler has to learn it rather than the check skip it",
			strings.Join(zero, ", "))
	}
	// The IR's own reference is the Plan's. The publisher stores it stripped to
	// the identity and the assembly restores the revisions from the output
	// context, which is a real reduction in what is written rather than a
	// field being lost -- but it only holds because the two are the same
	// reference. A fixture that fills them independently asks the round-trip
	// to preserve a state a compiled Plan does not have, and would report a
	// design as a defect.
	plan.StrategyIR.StrategyRef = plan.StrategyRef
	identity := execution.PlanIdentity{
		TenantID: plan.StrategyRef.TenantID, BusinessID: "business-1", StrategyID: plan.StrategyRef.StrategyID,
	}
	return FrozenPlan{Identity: identity, Plan: plan}
}

func zeroFieldsOf(value reflect.Value) []string {
	var zero []string
	for index := 0; index < value.NumField(); index++ {
		if value.Field(index).IsZero() {
			zero = append(zero, value.Type().Field(index).Name)
		}
	}
	return zero
}

// fillNonZero gives every settable field under value a value that is not its
// zero, deep enough that a pointer section is allocated and its own fields are
// filled. Cycles are bounded by depth rather than by a visited set: these are
// contract types, and a depth that runs out is a contract that grew a shape
// this needs to learn about.
func fillNonZero(t *testing.T, value reflect.Value, path string, depth int) {
	t.Helper()
	if depth > 12 {
		t.Fatalf("%s is deeper than this filler goes; the contract grew a shape the round-trip guard "+
			"does not know how to populate", path)
	}
	if !value.CanSet() {
		return
	}
	// A raw JSON field has to stay parseable: filling it with bytes would make
	// the publisher's encoder fail and the round-trip would never run.
	if value.Type() == reflect.TypeOf(json.RawMessage(nil)) {
		value.Set(reflect.ValueOf(json.RawMessage(`"filled"`)))
		return
	}
	switch value.Kind() {
	case reflect.String:
		value.SetString(path)
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(int64(7 + depth))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(uint64(7 + depth))
	case reflect.Float32, reflect.Float64:
		value.SetFloat(float64(7 + depth))
	case reflect.Ptr:
		value.Set(reflect.New(value.Type().Elem()))
		fillNonZero(t, value.Elem(), path, depth+1)
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			if field.PkgPath != "" {
				// Unexported, so neither reflection nor JSON can carry it; a
				// contract type that hides state is a separate problem.
				continue
			}
			fillNonZero(t, value.Field(index), fmt.Sprintf("%s.%s", path, field.Name), depth+1)
		}
	case reflect.Slice:
		element := reflect.New(value.Type().Elem()).Elem()
		fillNonZero(t, element, path+"[0]", depth+1)
		value.Set(reflect.Append(reflect.MakeSlice(value.Type(), 0, 1), element))
	case reflect.Array:
		for index := 0; index < value.Len(); index++ {
			fillNonZero(t, value.Index(index), fmt.Sprintf("%s[%d]", path, index), depth+1)
		}
	case reflect.Map:
		key := reflect.New(value.Type().Key()).Elem()
		fillNonZero(t, key, path+".key", depth+1)
		element := reflect.New(value.Type().Elem()).Elem()
		fillNonZero(t, element, path+".value", depth+1)
		mapValue := reflect.MakeMap(value.Type())
		mapValue.SetMapIndex(key, element)
		value.Set(mapValue)
	default:
		t.Fatalf("%s is a %s, which this filler does not populate", path, value.Kind())
	}
}
