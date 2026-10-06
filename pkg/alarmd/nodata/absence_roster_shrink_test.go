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
	"reflect"
	"testing"
)

// A8, the other direction: target groups back to an item that expects nothing.
//
// The contract cases pin the direction the ruling was written from -- a host
// leaving the target, and a whole-item absence turning into target groups.
// This is the same event running the other way: the strategy's no-data
// dimensions stop naming bk_target_ip, so the roster goes back to expecting
// nothing, and the host groups that had absences open are stranded exactly as
// the whole-item group was.
//
// It is here because the reason does not distinguish them. Once the roster
// stops expecting a group, no round will ever say NORMAL for it again, so the
// one closing verdict is the last chance the alert standing on it has. An
// implementation that closed only the cases the ruling names would leave this
// one open forever, and nothing in the contract cases would say so.
func TestAbsence_A8_TargetGroupsLeftOpenAreClosedWhenTheItemExpectsNothing(t *testing.T) {
	first, second := hostGroup(t, "10.0.0.1"), hostGroup(t, "10.0.0.2")
	opened := evaluate(t, fullRound(absenceRound1, staticRoster("v1", first, second), nil, map[string]GroupMemory{}))
	wantVerdicts(t, opened, map[string]Verdict{first.Key(): VerdictAnomaly, second.Key(): VerdictAnomaly})

	whole := WholeItemGroup().Key()
	shrunk := evaluate(t, fullRound(absenceRound2, Roster{Version: "v2", Source: RosterWhole}, nil, opened.Memory))

	wantVerdicts(t, shrunk, map[string]Verdict{
		whole: VerdictAnomaly, first.Key(): VerdictNormal, second.Key(): VerdictNormal,
	})
	for _, group := range []Group{first, second} {
		if entry, remembered := shrunk.Memory[group.Key()]; remembered {
			t.Fatalf("Memory[%s] = %+v, want the closed absence forgotten: the item no longer expects "+
				"this group, so nothing will speak about it again and a memory of it would only make a "+
				"history roster claim it back", group.Key(), entry)
		}
	}
	wantMemory(t, shrunk, whole, GroupMemory{FirstAbsent: absenceRound2})
	if shrunk.Facts.Absent != 1 {
		t.Fatalf("Facts.Absent = %d, want 1: the whole item is the absence this round found, and the "+
			"two closing verdicts end absences rather than being them", shrunk.Facts.Absent)
	}

	// And the item stays closed on the groups: the next round says nothing
	// about them, so one alert does not reopen on the next evaluation.
	next := evaluate(t, fullRound(absenceRound3, Roster{Version: "v2", Source: RosterWhole}, nil, shrunk.Memory))
	wantVerdicts(t, next, map[string]Verdict{whole: VerdictAnomaly})
}

// The closing NORMAL reaches the wire as the group it closes, not as the item.
//
// The verdict alone is not the point of A8; the point is that the alert
// standing on that group ends, and the alert is found by the identity the
// point carries. The synthetic series is where the group key becomes that
// identity, and the closed group is by construction the one group the roster
// cannot supply -- so it is rebuilt from its key.
//
// Getting this wrong is quiet and worse than not closing at all: a recovery
// built from the whole-item group would end whatever alert stands on the item
// while leaving the host's own alert exactly where it was.
func TestASyntheticClosingRecoveryCarriesTheGroupItCloses(t *testing.T) {
	dropped := hostGroup(t, "10.0.0.2")
	kept := hostGroup(t, "10.0.0.1")
	roster := staticRoster("v2", kept)
	result := AbsenceResult{
		Verdicts: map[string]Verdict{kept.Key(): VerdictAnomaly, dropped.Key(): VerdictNormal},
		Memory:   map[string]GroupMemory{kept.Key(): {FirstAbsent: absenceRound1}},
		Roster:   roster,
	}

	series := SyntheticSeriesFor(SyntheticInput{
		EvaluationTime: absenceRound2, PeriodSeconds: absencePeriod,
		Result: result, Memory: result.Memory, Roster: roster,
	})

	var closing *SyntheticSeries
	for index := range series {
		if series[index].Group.Key() == dropped.Key() {
			closing = &series[index]
		}
	}
	if closing == nil {
		keys := make([]string, 0, len(series))
		for _, entry := range series {
			keys = append(keys, entry.Group.Key())
		}
		t.Fatalf("no series for the closed group %q; got %v. A closing verdict that produces no point, "+
			"or produces one under another identity, leaves the alert it was made for standing",
			dropped.Key(), keys)
	}
	if closing.Value != PresentValue || closing.Periods != 0 {
		t.Fatalf("closing series = %+v, want the present value and no period count: it is the end of an "+
			"absence, not one", *closing)
	}
	if !reflect.DeepEqual(closing.Group.Dimensions(), dropped.Dimensions()) {
		t.Fatalf("closing series dimensions = %v, want %v", closing.Group.Dimensions(), dropped.Dimensions())
	}
}
