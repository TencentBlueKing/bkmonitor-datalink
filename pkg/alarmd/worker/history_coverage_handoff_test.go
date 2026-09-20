// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Every window count computed during evaluation reaches the observation.
//
// This is the handoff, and the handoff is where this codebase loses things. The
// same defect has now been found and fixed more than a dozen times in different
// files: a field that discriminates between two situations needing opposite
// responses is computed where the decision is made, used there, and dropped on
// the way up -- leaving a page that states one of the two situations for both,
// with nothing on it that disagrees.
//
// Written by reflection rather than field by field on purpose. A check that
// names the fields is a second copy of the same hand-written list and goes stale
// in the same edit that breaks the first: whoever adds a field to the coverage
// and forgets the translation is not going to remember to add it here either.
// This fails on the field they forgot, by name, without anyone updating it.
func TestEveryWindowCountReachesTheObservation(t *testing.T) {
	// Distinct non-zero values so a translation that copies the wrong source
	// field fails rather than passing on a coincidence. Levels is largest
	// because Short, Empty and Guarded are counted over the same windows.
	// The one string, the reason the first unusable Level gave, crosses as
	// itself: it is a code the page shows, not a count.
	source := execution.HistoryCoverage{}
	value := reflect.ValueOf(&source).Elem()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		switch field.Kind() {
		case reflect.Uint32:
			field.SetUint(uint64(90 + i))
		case reflect.String:
			field.SetString("REASON_" + value.Type().Field(i).Name)
		default:
			t.Fatalf("%s is neither a uint32 nor a string; a field of another kind needs a decision "+
				"about how it crosses, not a silent skip", value.Type().Field(i).Name)
		}
	}
	source.Levels = 200

	facts := historyCoverageFacts(source)
	if facts == nil {
		t.Fatal("no facts were produced for a coverage with windows in it")
	}

	published := reflect.ValueOf(facts).Elem()
	for i := 0; i < value.NumField(); i++ {
		name := value.Type().Field(i).Name
		got := published.FieldByName(name)
		if !got.IsValid() {
			t.Errorf("HistoryCoverage.%s has no counterpart on HistoryCoverageFacts, so it is "+
				"computed during evaluation and never leaves the worker", name)
			continue
		}
		if !reflect.DeepEqual(got.Interface(), value.Field(i).Interface()) {
			t.Errorf("%s crossed as %v, want %v -- the value on the page would not be the value "+
				"the evaluation made", name, got.Interface(), value.Field(i).Interface())
		}
	}
}

// A Slot that summarised nothing is not a Slot whose windows were all complete.
//
// Both report zero short. The first has to stay distinguishable, and absence is
// the only thing that says it: a facts value with every count at zero reads as
// "measured, and nothing was wrong".
func TestASlotThatSummarisedNoWindowPublishesNoCounts(t *testing.T) {
	if facts := historyCoverageFacts(execution.HistoryCoverage{}); facts != nil {
		t.Fatalf("facts = %+v for a Slot that summarised no window, want none: zeros here read as "+
			"a measurement that found nothing wrong", facts)
	}
}
