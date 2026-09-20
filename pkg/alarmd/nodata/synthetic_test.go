// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const (
	synthPeriod = int64(60)
	synthNow    = int64(6000)
)

func synthRoster(groups ...Group) Roster {
	roster := Roster{Source: RosterTargetStatic, Groups: map[string]Group{}}
	for _, group := range groups {
		roster.Groups[group.Key()] = group
	}
	return roster
}

// An unavailable round produces no series at all. A point valued zero would
// advance the trigger window as a recovery and one valued one would advance it
// as an absence; producing nothing is the only reading that says this round had
// no evidence either way.
func TestSyntheticSeriesAreNotProducedForAnUnavailableRound(t *testing.T) {
	group := hostTargetGroup(HostIdentity{IP: "10.0.0.1", CloudID: "0"})
	result := Evaluate(AbsenceInput{
		EvaluationTime: synthNow, PeriodSeconds: synthPeriod,
		Completeness: execution.CompletenessPartial,
		Roster:       synthRoster(group), Memory: map[string]GroupMemory{},
	})
	if series := SyntheticSeriesFor(SyntheticInput{
		EvaluationTime: synthNow, PeriodSeconds: synthPeriod,
		Result: result, Memory: result.Memory, Roster: synthRoster(group),
	}); len(series) != 0 {
		t.Fatalf("series = %+v, want none from a round that saw nothing", series)
	}
}

// Absent is one and present is zero, and the list is ordered so a retry of the
// same Slot produces the same list rather than a set in a new order.
func TestSyntheticSeriesCarryTheVerdictAsAValueInAStableOrder(t *testing.T) {
	present := hostTargetGroup(HostIdentity{IP: "10.0.0.1", CloudID: "0"})
	absent := hostTargetGroup(HostIdentity{IP: "10.0.0.2", CloudID: "0"})
	roster := synthRoster(present, absent)
	result := Evaluate(AbsenceInput{
		EvaluationTime: synthNow, PeriodSeconds: synthPeriod, Completeness: execution.CompletenessFull,
		Present: map[string]Group{present.Key(): present}, Roster: roster, Memory: map[string]GroupMemory{},
	})

	first := SyntheticSeriesFor(SyntheticInput{
		EvaluationTime: synthNow, PeriodSeconds: synthPeriod,
		Result: result, Memory: result.Memory, Roster: roster,
	})
	second := SyntheticSeriesFor(SyntheticInput{
		EvaluationTime: synthNow, PeriodSeconds: synthPeriod,
		Result: result, Memory: result.Memory, Roster: roster,
	})
	if !reflect.DeepEqual(first, second) {
		t.Fatal("two runs over the same verdicts produced different lists")
	}
	if len(first) != 2 {
		t.Fatalf("series = %+v, want one per judged group", first)
	}
	// Ascending by group key, read off the list itself. Comparing two runs only
	// says the order does not wobble; any fixed order passes that, including the
	// reverse of this one, and the order is what makes two Slots' state mutations
	// line up.
	for index := 1; index < len(first); index++ {
		if first[index-1].Group.Key() >= first[index].Group.Key() {
			t.Fatalf("series[%d] key %q is not before series[%d] key %q",
				index-1, first[index-1].Group.Key(), index, first[index].Group.Key())
		}
	}

	byKey := map[string]SyntheticSeries{}
	for _, entry := range first {
		byKey[entry.Group.Key()] = entry
	}
	// The literals, not the constants. The trigger fires on "value >= 1" and the
	// Python backend writes 1 for absent and 0 for present; an assertion written
	// as "== AbsentValue" moves with the constant, so setting AbsentValue to 0 -
	// which silently stops every no-data alert from ever firing - would leave
	// this test green.
	if AbsentValue != 1 || PresentValue != 0 {
		t.Fatalf("AbsentValue = %d, PresentValue = %d; want 1 and 0, the values the trigger threshold "+
			"and the backend's own series are written in terms of", AbsentValue, PresentValue)
	}
	if byKey[present.Key()].Value != 0 || byKey[present.Key()].Periods != 0 {
		t.Fatalf("present group = %+v, want value 0 and no period count", byKey[present.Key()])
	}
	if byKey[absent.Key()].Value != 1 {
		t.Fatalf("absent group = %+v, want value 1", byKey[absent.Key()])
	}
	// The point is the period this Slot decides, which is one behind the Slot.
	for _, entry := range first {
		if entry.SourceTime != synthNow-synthPeriod {
			t.Fatalf("source time = %d, want the period being decided", entry.SourceTime)
		}
	}
}

// The tag is on every synthetic series' identity fields, which is what keeps a
// no-data group away from the real series of the same item.
//
// It asserts the tag is the JSON boolean, and then asserts why that matters:
// the text "true" - the form a map[string]string forces and the form this code
// carried until the hash was checked against Python - hashes to something else.
// An earlier version of this test asserted the tag was the text "true", which
// is the code's own choice restated rather than a fact about the backend, and
// it held the wrong form in place while passing.
func TestSyntheticSeriesIdentityFieldsAlwaysCarryTheTagAsABoolean(t *testing.T) {
	group := hostTargetGroup(HostIdentity{IP: "10.0.0.1", CloudID: "0"})
	fields := SyntheticSeries{Group: group}.IdentityFields()
	if string(fields[contract.NoDataDimensionTag]) != "true" {
		t.Fatalf("tag = %s, want the JSON boolean true", fields[contract.NoDataDimensionTag])
	}
	if string(fields[HostIPDimension]) != `"10.0.0.1"` || string(fields[HostCloudDimension]) != `"0"` {
		t.Fatalf("fields = %v, want the group's own pairs as JSON strings beside the tag", fields)
	}
	whole := SyntheticSeries{Group: WholeItemGroup()}.IdentityFields()
	if len(whole) != 1 || string(whole[contract.NoDataDimensionTag]) != "true" {
		t.Fatalf("whole-item fields = %v, want only the tag", whole)
	}

	// count_md5 hashes str() of each value, so Python's True and the text "True"
	// flatten together while the text "true" does not. Stating it as two hashes
	// that must differ is what makes a future change back to a string form fail
	// here rather than in production, where a wrong anomaly_id matches no record
	// and reads as a brand new anomaly every round.
	boolean, err := contract.PythonObjectMD5(whole)
	if err != nil {
		t.Fatal(err)
	}
	text, err := contract.PythonObjectMD5(map[string]json.RawMessage{contract.NoDataDimensionTag: json.RawMessage(`"true"`)})
	if err != nil {
		t.Fatal(err)
	}
	if boolean == text {
		t.Fatal("the boolean tag and the text \"true\" hash alike; this test can no longer tell the forms apart")
	}
}

// The period count follows the backend's two checkpoints and its choice between
// them: since the last point when there was one, and from the first absent
// round otherwise.
func TestSyntheticSeriesCountPeriodsTheWayTheBackendReportsThem(t *testing.T) {
	for name, test := range map[string]struct {
		memory GroupMemory
		want   int64
	}{
		// Seen four periods ago: four periods without data.
		"seen before": {memory: GroupMemory{LastSeen: synthNow - 4*synthPeriod, FirstAbsent: synthNow - 3*synthPeriod}, want: 4},
		"seen once":   {memory: GroupMemory{LastSeen: synthNow - synthPeriod, FirstAbsent: synthNow}, want: 1},
		// Never seen: counted from the round it was first called absent, which
		// is one on that round and grows from there.
		"never seen, first round": {memory: GroupMemory{FirstAbsent: synthNow}, want: 1},
		"never seen, fifth round": {memory: GroupMemory{FirstAbsent: synthNow - 4*synthPeriod}, want: 5},
		// Neither checkpoint: the round it is first judged, which is one.
		"nothing remembered": {memory: GroupMemory{}, want: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if got := absentPeriods(test.memory, synthNow, synthPeriod); got != test.want {
				t.Fatalf("absentPeriods(%+v) = %d, want %d", test.memory, got, test.want)
			}
		})
	}
}

// A period of zero cannot produce a count, and must not divide by it either.
func TestSyntheticSeriesPeriodCountRefusesAZeroPeriod(t *testing.T) {
	if got := absentPeriods(GroupMemory{LastSeen: 1}, synthNow, 0); got != 0 {
		t.Fatalf("absentPeriods with no period = %d, want 0", got)
	}
}

// The whole-item verdict is not a roster group, and still produces its series.
func TestSyntheticSeriesIncludeTheWholeItemVerdict(t *testing.T) {
	result := Evaluate(AbsenceInput{
		EvaluationTime: synthNow, PeriodSeconds: synthPeriod, Completeness: execution.CompletenessFull,
		Roster: Roster{Source: RosterHistory, Groups: map[string]Group{}}, Memory: map[string]GroupMemory{},
	})
	series := SyntheticSeriesFor(SyntheticInput{
		EvaluationTime: synthNow, PeriodSeconds: synthPeriod,
		Result: result, Memory: result.Memory, Roster: Roster{Groups: map[string]Group{}},
	})
	if len(series) != 1 || series[0].Group.Key() != WholeItemGroup().Key() || series[0].Value != AbsentValue {
		t.Fatalf("series = %+v, want one absent whole-item series", series)
	}
}
