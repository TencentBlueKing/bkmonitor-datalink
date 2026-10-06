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

// The absences a round reports are filed by how long each has been open, from
// the same FirstAbsent the horizon reads: one that began this round, one under
// an hour, one under a day, one a day or more -- two in one bucket so a count
// of buckets cannot pass for a count of absences. A group the horizon stops
// this round is not an absence reported and is in no bucket, and one that
// reported is in none either.
func TestAbsencesAreFiledByHowLongTheyHaveBeenOpen(t *testing.T) {
	now := absenceRound1
	hosts := []Group{}
	for _, ip := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3", "192.0.2.4", "192.0.2.5", "192.0.2.6", "192.0.2.7"} {
		hosts = append(hosts, hostGroup(t, ip))
	}
	memory := map[string]GroupMemory{
		// Began this round: no start yet in the memory.
		hosts[0].Key(): {LastSeen: now - 60},
		// Under an hour, twice.
		hosts[1].Key(): {LastSeen: now - 3600, FirstAbsent: now - 1800},
		hosts[2].Key(): {LastSeen: now - 3600, FirstAbsent: now - 60},
		// Under a day.
		hosts[3].Key(): {LastSeen: now - 86400, FirstAbsent: now - 7200},
		// A day or more, twice.
		hosts[4].Key(): {LastSeen: now - 172800, FirstAbsent: now - 86400},
		hosts[5].Key(): {LastSeen: now - 172800, FirstAbsent: now - 100000},
		// Reports this round: no bucket.
		hosts[6].Key(): {LastSeen: now - 60},
	}
	input := fullRound(now, staticRoster("v1", hosts...), groupSet(hosts[6]), memory)
	result := evaluate(t, input)
	want := AbsentAgeBuckets{ThisRound: 1, UnderHour: 2, UnderDay: 1, DayOrMore: 2}
	if result.Facts.AbsentAges != want {
		t.Fatalf("ages = %+v, want %+v", result.Facts.AbsentAges, want)
	}
	if result.Facts.Absent != want.Total() {
		t.Fatalf("absent = %d, ages total %d", result.Facts.Absent, want.Total())
	}

	// With a one-day horizon the two a day or more old are stopped this round:
	// expired, not reported, and in no bucket -- the buckets count what was
	// reported, and the horizon's effect shows as the last bucket emptying.
	input.TrackingHorizonSeconds = 86400
	horizoned := evaluate(t, input)
	want = AbsentAgeBuckets{ThisRound: 1, UnderHour: 2, UnderDay: 1, DayOrMore: 0}
	if horizoned.Facts.AbsentAges != want || horizoned.Facts.Expired != 2 {
		t.Fatalf("under a one-day horizon ages = %+v expired = %d, want %+v and 2", horizoned.Facts.AbsentAges, horizoned.Facts.Expired, want)
	}

	// The whole-item absence is filed too, by its own start.
	whole := evaluate(t, fullRound(now, historyRoster("v1"), nil, map[string]GroupMemory{
		WholeItemGroup().Key(): {FirstAbsent: now - 5000},
	}))
	if whole.Facts.AbsentAges != (AbsentAgeBuckets{UnderDay: 1}) {
		t.Fatalf("whole-item ages = %+v, want one under a day", whole.Facts.AbsentAges)
	}
}
