// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"reflect"
	"testing"
)

// Every rotation count a replica reports reaches the deployment's total.
//
// The aggregation is a hand-written field-by-field copy, and this codebase's
// most frequent defect is a field that survives everywhere except one handoff.
// A count dropped here arrives on the page as zero, which reads as "this never
// happened" rather than "nobody carried it" -- and the two have opposite
// answers.
//
// Written by reflection because a check that names the fields is a second copy
// of the same list, going stale in the same edit that breaks the first.
func TestEveryRotationCountSurvivesBeingFoldedAcrossReplicas(t *testing.T) {
	build := func(seed uint64) Rotation {
		rotation := Rotation{}
		value := reflect.ValueOf(&rotation).Elem()
		for i := 0; i < value.NumField(); i++ {
			field := value.Field(i)
			switch field.Kind() {
			case reflect.Uint64:
				// Distinct per field and per replica, so folding the wrong source
				// field fails instead of passing on equal numbers.
				field.SetUint(seed * uint64(i+2))
			case reflect.Float64:
				field.SetFloat(float64(seed))
			default:
				t.Fatalf("Rotation.%s is a %s; this check knows how counts and durations fold and "+
					"a new kind needs that decision made, not skipped",
					value.Type().Field(i).Name, field.Kind())
			}
		}
		return rotation
	}

	first, second := build(3), build(5)
	total := Rotation{}
	total.fold(first)
	total.fold(second)

	got := reflect.ValueOf(total)
	for i := 0; i < got.NumField(); i++ {
		name := got.Type().Field(i).Name
		switch got.Field(i).Kind() {
		case reflect.Uint64:
			want := reflect.ValueOf(first).Field(i).Uint() + reflect.ValueOf(second).Field(i).Uint()
			if got.Field(i).Uint() != want {
				t.Errorf("%s folded to %d, want %d -- a replica's count that does not reach the "+
					"deployment total reads on the page as something that never happened",
					name, got.Field(i).Uint(), want)
			}
		case reflect.Float64:
			// One rotation's duration. Summing two replicas' would report a
			// rotation that neither of them ran.
			want := reflect.ValueOf(second).Field(i).Float()
			if got.Field(i).Float() != want {
				t.Errorf("%s folded to %v, want the larger of the two (%v): it is one rotation's "+
					"duration, not a quantity to add up", name, got.Field(i).Float(), want)
			}
		}
	}
}

// A nil total is not a panic and not a silent success.
//
// fold is called on a pointer the aggregation may not have allocated yet, and
// the guard has to stay: the alternative is a crash on the verdict route, which
// is the most-read thing the deployment serves.
func TestFoldingIntoNoRotationIsSafe(t *testing.T) {
	var absent *Rotation
	absent.fold(Rotation{Deferred: 9})
	if absent != nil {
		t.Fatal("folding into a nil rotation produced one")
	}
}
