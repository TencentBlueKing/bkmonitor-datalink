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

// Only the one close of a group the roster stopped expecting (A8) is marked as
// the last word about it. A present group's NORMAL and the whole item's are
// said again every round they hold, and their recovery stays behind the
// open-alert gate like every healthy series': marking them would send an
// orphan recovery for every healthy group every round.
func TestOnlyTheCloseOfADroppedGroupIsMarkedAsItsLastWord(t *testing.T) {
	kept, removed, quiet := hostGroup(t, "192.0.2.1"), hostGroup(t, "192.0.2.2"), hostGroup(t, "192.0.2.3")
	roster1 := staticRoster("v1", kept, removed, quiet)
	first := evaluate(t, fullRound(absenceRound1, roster1, groupSet(quiet), map[string]GroupMemory{}))
	if len(first.Closing) != 0 {
		t.Fatalf("round 1 marked %v as closing; its NORMALs (a present group, the whole item) are said every round", first.Closing)
	}

	roster2 := staticRoster("v2", kept)
	second := evaluate(t, fullRound(absenceRound2, roster2, nil, first.Memory))
	if want := map[string]bool{removed.Key(): true}; !reflect.DeepEqual(second.Closing, want) {
		t.Fatalf("round 2 marked %v as closing, want only the group the roster dropped with its absence open", second.Closing)
	}
	series, err := SyntheticSeriesFor(SyntheticInput{EvaluationTime: absenceRound2, PeriodSeconds: 60,
		Result: second, Memory: second.Memory, Roster: roster2})
	if err != nil {
		t.Fatal(err)
	}
	closing := 0
	for _, entry := range series {
		if entry.Closing {
			closing++
			if entry.Group.Key() != removed.Key() || entry.Value != PresentValue {
				t.Fatalf("closing series %+v, want the dropped group's NORMAL", entry)
			}
		}
	}
	if closing != 1 {
		t.Fatalf("round 2's series carry %d closes, want the one", closing)
	}
}
