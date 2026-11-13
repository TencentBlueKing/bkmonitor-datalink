// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import "testing"

// Every array a group's reading counts in is as long as the words it counts
// by: a word added to a list and not to its array is an index past the end
// the first time the reading is read (GroupReading), in production.
func TestEveryGroupCountIsAsLongAsItsWords(t *testing.T) {
	var counts groupCounts
	for _, pair := range []struct {
		name         string
		words, slots int
	}{
		{"hold classes", len(GroupReadHoldClasses), len(counts.compared)},
		{"rungs compared", len(RungNames), len(counts.compared[0])},
		{"rungs changed", len(RungNames), len(counts.changed[0])},
		{"sample classes", len(SampleClasses), len(counts.classes)},
		{"ignored reasons", len(ReadHoldIgnoredReasons), len(counts.ignored)},
		{"earlier-read outcomes", len(EarlierReadOutcomes), len(counts.earlier)},
		{"overtaken paths", len(EarlierOvertakenPaths), len(counts.overtaken)},
		{"distances ahead", len(EarlierAheadSlots), len(counts.ahead)},
		{"skip reasons", len(EarlierSkipReasons), len(counts.skipped)},
		{"probe round outcomes", len(FloorProbeRoundOutcomes), len(counts.probeRounds)},
	} {
		if pair.words != pair.slots {
			t.Errorf("%s: %d words counted in %d slots", pair.name, pair.words, pair.slots)
		}
	}
}
