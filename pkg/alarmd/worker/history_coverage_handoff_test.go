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
	"fmt"
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
		case reflect.Int64:
			field.SetInt(int64(1000 + i))
		case reflect.Slice:
			// The named windows cross field by field below, since the two
			// sides are different types; here they only have to be present.
			if value.Type().Field(i).Name != "Windows" {
				t.Fatalf("%s is a slice this test has no fixture for; decide how it crosses", value.Type().Field(i).Name)
			}
		default:
			t.Fatalf("%s is neither a uint32 nor a string; a field of another kind needs a decision "+
				"about how it crosses, not a silent skip", value.Type().Field(i).Name)
		}
	}
	source.Levels = 200
	source.Windows = []execution.WindowCoverage{{LevelID: 5, Series: "abc", Valid: 2, Required: 9, End: 540,
		Missing: []int64{120, 180}, MissingTotal: 6, Unusable: []int64{240}, UnusableTotal: 1,
		Guarded: true, GuardReason: "CONFIG_DRIFT", Fresh: true}}

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
		if name == "Windows" {
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

// Unless it says why it summarised none. A Slot whose every series was
// resumed, or none of whose State could be loaded, has nothing to say about
// windows and something to say about itself -- and it is the reading that most
// needs saying, because the alternative is a row that reports no coverage at
// all while 249 series went unevaluated. The gate reads all three counts, so
// the one case this exists for is not the one it drops.
func TestASlotThatSummarisedNoWindowStillPublishesWhyNot(t *testing.T) {
	for name, coverage := range map[string]execution.HistoryCoverage{
		"every series resumed":       {Resumed: 227},
		"no State loadable":          {Constrained: 249},
		"a few summarised, most not": {Levels: 22, Resumed: 227},
	} {
		facts := historyCoverageFacts(coverage)
		if facts == nil {
			t.Errorf("%s: no facts published, want the round's own account of itself", name)
			continue
		}
		if facts.Resumed != coverage.Resumed || facts.Constrained != coverage.Constrained {
			t.Errorf("%s: facts = %+v, want the two counts carried", name, *facts)
		}
	}
}

// The named windows cross whole, field by field, and the primary fact rides
// the completion beside them. Written against the source struct's fields by
// name so a field added to the window on one side and forgotten on the other
// fails here rather than rendering as absent on the page.
func TestEveryNamedWindowFieldAndThePrimaryFactReachTheObservation(t *testing.T) {
	window := execution.WindowCoverage{
		Plan:    execution.PlanIdentity{TenantID: "default", BusinessID: "2", StrategyID: "4101"},
		LevelID: 5, Series: "abc", Valid: 2, Required: 9, End: 540,
		Missing: []int64{120, 180}, MissingTotal: 6, Unusable: []int64{240}, UnusableTotal: 1,
		Guarded: true, GuardReason: "CONFIG_DRIFT", Fresh: true}
	facts := historyCoverageFacts(execution.HistoryCoverage{Levels: 1, Short: 1, WorstValid: 2, WorstRequired: 9, Windows: []execution.WindowCoverage{window}})
	if facts == nil || len(facts.Windows) != 1 {
		t.Fatalf("facts = %+v, want the one named window crossed", facts)
	}
	got := reflect.ValueOf(facts.Windows[0])
	source := reflect.ValueOf(window)
	for i := 0; i < source.NumField(); i++ {
		name := source.Type().Field(i).Name
		want := source.Field(i).Interface()
		// The Plan crosses as the two of its parts a reader acts on: the
		// tenant is not on the fact because the page is already scoped to
		// one, and asserting the pair by name is what keeps a Plan added
		// here from crossing as nothing.
		if name == "Plan" {
			plan := want.(execution.PlanIdentity)
			if got.FieldByName("Strategy").String() != plan.StrategyID || got.FieldByName("Business").String() != plan.BusinessID {
				t.Errorf("the window's Plan crossed as strategy %q business %q, want %q/%q",
					got.FieldByName("Strategy").String(), got.FieldByName("Business").String(), plan.StrategyID, plan.BusinessID)
			}
			continue
		}
		target := name
		if name == "LevelID" {
			target = "Level"
		}
		field := got.FieldByName(target)
		if !field.IsValid() {
			t.Errorf("WindowCoverage.%s has no counterpart on the fact", name)
			continue
		}
		if fmt.Sprint(field.Interface()) != fmt.Sprint(want) {
			t.Errorf("%s crossed as %v, want %v", name, field.Interface(), want)
		}
	}
	// The lists are copies: a window the evaluator goes on to append to does
	// not rewrite the observation already handed out.
	window.Missing[0] = 999
	if facts.Windows[0].Missing[0] != 120 {
		t.Fatal("the observation shares the evaluator's slice")
	}
	if primaryInputFacts(nil) != nil {
		t.Fatal("a completion with no primary produced primary facts")
	}
	primary := primaryInputFacts(&execution.PrimaryInputFact{Completeness: execution.CompletenessPartial, DataState: execution.DataStateData})
	if primary == nil || primary.Completeness != "PARTIAL" || primary.DataState != "DATA" {
		t.Fatalf("primary facts = %+v, want PARTIAL with DATA", primary)
	}
}
