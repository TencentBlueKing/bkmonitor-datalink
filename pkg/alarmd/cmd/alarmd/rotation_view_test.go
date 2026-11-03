// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// Every count the dispatcher's walk keeps reaches the view the page reads.
//
// This handoff had no check, and the split of 被挡回 by cause was dropped here
// in a mutation run without a single test noticing. A count that does not cross
// arrives on the page as zero, which reads as "this never happened" -- the
// opposite of "nobody carried it", and the two need opposite responses.
//
// Matched by name, case-insensitively, rather than by a written-out list: the
// person who adds a count to the walk and forgets the translation is the same
// person who would forget to update a list here.
func TestEveryRotationCountReachesTheViewThePageReads(t *testing.T) {
	// Written out rather than filled by reflection: these fields are unexported,
	// and reflect refuses to set them even from inside the package. Distinct
	// values so copying the wrong source field fails rather than passing on two
	// equal numbers.
	facts := phaseTwoRotationFacts{
		startedAt: time.Unix(1, 0), completed: 71, truncated: 72,
		offered: 73, queued: 74, deferred: 75,
		deferredQueueFull: 76, deferredNotBetter: 77,
		lastSeconds: 1.5, generation: 78,
	}
	source := reflect.ValueOf(facts)

	// Every field non-zero, checked before anything else. This is what makes a
	// newly added count fail here: whoever adds one leaves it at the zero value
	// in this literal, and the test stops with the field's name rather than
	// comparing zero against zero and passing.
	for i := 0; i < source.NumField(); i++ {
		if source.Field(i).IsZero() {
			t.Fatalf("%s is not set in this fixture, so nothing below can tell whether it crosses; "+
				"give it a distinct value", source.Type().Field(i).Name)
		}
	}

	view := rotationView(facts)
	if view == nil {
		t.Fatal("no rotation view was produced")
	}
	published := reflect.ValueOf(view).Elem()

	// Fields of the walk that are deliberately not published, each with the
	// reason. Anything else missing from the view is a count the page cannot
	// see, and this fails on it by name.
	internal := map[string]string{
		"startedAt":  "the clock the duration is measured from, not a fact about the walk",
		"generation": "which rotation the counts belong to; the view carries one rotation at a time",
	}

	for i := 0; i < source.NumField(); i++ {
		name := source.Type().Field(i).Name
		if _, ok := internal[name]; ok {
			continue
		}
		field := published.FieldByNameFunc(func(candidate string) bool {
			return strings.EqualFold(candidate, name)
		})
		if !field.IsValid() {
			t.Errorf("the walk keeps %s and the view has no field for it, so it is counted every "+
				"rotation and never reaches anyone", name)
			continue
		}
		switch field.Kind() {
		case reflect.Uint64:
			if field.Uint() != source.Field(i).Uint() {
				t.Errorf("%s crossed as %d, want %d", name, field.Uint(), source.Field(i).Uint())
			}
		case reflect.Float64:
			if field.Float() != source.Field(i).Float() {
				t.Errorf("%s crossed as %v, want %v", name, field.Float(), source.Field(i).Float())
			}
		}
	}
}
