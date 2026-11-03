// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import "testing"

// The horizon's edge is one second wide, on a target's group and on the
// whole item: tracking stops on the round where EvaluationTime - FirstAbsent
// >= H (retention contract section 9, the bound inclusive), so an absence
// one second short of H is still an ANOMALY with its start kept, and one
// exactly at H or one second past gives no verdict and is marked where it
// stopped. The ages are written out against H = 120 rather than derived
// from it, so a comparison moved by one either way fails here.
func TestTheHorizonsEdgeIsOneSecondWide(t *testing.T) {
	target := hostGroup(t, "192.0.2.1")
	whole := WholeItemGroup().Key()
	empty := Roster{Version: "v1", Source: RosterWhole}
	for _, test := range []struct {
		name    string
		age     int64
		tracked bool
	}{
		{"one second short of the horizon", 119, true},
		{"exactly at the horizon", 120, false},
		{"one second past the horizon", 121, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			at := 800 + test.age

			group := evaluate(t, horizonRound(at, staticRoster("v1", target), nil,
				map[string]GroupMemory{target.Key(): {LastSeen: 740, FirstAbsent: 800}}))
			item := evaluate(t, horizonRound(at, empty, nil, map[string]GroupMemory{whole: {FirstAbsent: 800}}))

			if test.tracked {
				wantVerdicts(t, group, map[string]Verdict{target.Key(): VerdictAnomaly})
				wantMemory(t, group, target.Key(), GroupMemory{LastSeen: 740, FirstAbsent: 800})
				wantTrackingFacts(t, group, 0, 0)
				wantVerdicts(t, item, map[string]Verdict{whole: VerdictAnomaly})
				wantMemory(t, item, whole, GroupMemory{FirstAbsent: 800})
				wantTrackingFacts(t, item, 0, 0)
				return
			}
			if verdict, judged := group.Verdicts[target.Key()]; judged {
				t.Fatalf("the target's group at age %d gave %q, want no verdict", test.age, verdict)
			}
			// A target's group is marked and kept: the roster still holds it,
			// and a forgotten one would be rebuilt as a fresh absence.
			wantMemory(t, group, target.Key(), GroupMemory{LastSeen: 740, FirstAbsent: 800, SuppressedAt: at})
			wantTrackingFacts(t, group, 1, 0)
			if verdict, judged := item.Verdicts[whole]; judged {
				t.Fatalf("the whole item at age %d gave %q, want no verdict", test.age, verdict)
			}
			wantMemory(t, item, whole, GroupMemory{FirstAbsent: 800, SuppressedAt: at})
			wantTrackingFacts(t, item, 1, 0)
		})
	}
}

// The age buckets' edges, one second either side of each: under an hour is
// older than this round and under 3600 seconds, under a day is 3600 or more
// and under 86400, a day or more is 86400 or more (AbsentAgeBuckets). No
// horizon here, so the oldest are reported and filed, not stopped.
func TestTheAgeBucketsSplitAtTheHourAndTheDay(t *testing.T) {
	now := absenceRound1
	for _, test := range []struct {
		age  int64
		want AbsentAgeBuckets
	}{
		{0, AbsentAgeBuckets{ThisRound: 1}},
		{1, AbsentAgeBuckets{UnderHour: 1}},
		{3599, AbsentAgeBuckets{UnderHour: 1}},
		{3600, AbsentAgeBuckets{UnderDay: 1}},
		{86399, AbsentAgeBuckets{UnderDay: 1}},
		{86400, AbsentAgeBuckets{DayOrMore: 1}},
	} {
		host := hostGroup(t, "192.0.2.1")
		result := evaluate(t, fullRound(now, staticRoster("v1", host), nil,
			map[string]GroupMemory{host.Key(): {LastSeen: now - test.age - 60, FirstAbsent: now - test.age}}))
		if result.Facts.AbsentAges != test.want {
			t.Errorf("an absence %d seconds old is filed as %+v, want %+v", test.age, result.Facts.AbsentAges, test.want)
		}
	}
}
