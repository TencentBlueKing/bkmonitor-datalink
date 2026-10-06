// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import "testing"

// A threshold prefix converts where the unit has a scale, and does nothing
// where it does not.
//
// The second half is the one that was wrong. A unit with no scale -- a bare
// number, a count, a temperature -- has one suffix, the empty one, and any
// prefix configured against it failed to appear in that table. The compile
// refused, the level came out LEVEL_INVALID, and the strategy never ran. The
// other implementation converts nothing for these units, so the prefix has no
// effect there and the strategy runs: alarmd was refusing a configuration that
// is merely pointless.
//
// The first half is why this is not "accept whatever the prefix says". Where
// the unit does have a scale the prefix changes the number being compared
// against, and an unrecognised one would mean comparing against a threshold
// nobody meant. Both halves are in the same table, because a change that only
// stopped refusing would pass a table holding only the second.
func TestAThresholdPrefixScalesOnlyWhereTheUnitHasAScale(t *testing.T) {
	const giga = int64(1) << 30
	for _, test := range []struct {
		name     string
		dataUnit string
		prefix   string
		want     int64
		refused  bool
	}{
		// Units with a scale: the prefix selects it.
		{name: "percent with a percent prefix", dataUnit: "percent", prefix: "%", want: 1},
		{name: "bits per second with a mega prefix", dataUnit: "bps", prefix: "M", want: 1_000_000},
		{name: "bytes with a gibi prefix", dataUnit: "bytes", prefix: "Gi", want: giga},

		// Units with no scale: the prefix is nothing, not an error. Each of
		// these took a whole level out of service before.
		{name: "no unit at all with a percent prefix", dataUnit: "", prefix: "%", want: 1},
		{name: "none with a percent prefix", dataUnit: "none", prefix: "%", want: 1},
		{name: "short with a nanosecond prefix", dataUnit: "short", prefix: "ns", want: 1},
		{name: "celsius with a prefix that means nothing here", dataUnit: "celsius", prefix: "Gi", want: 1},

		// And a scale that does not have the prefix it was given is still a
		// refusal: this is the case the change must not swallow.
		{name: "bits per second with a gibi prefix", dataUnit: "bps", prefix: "Gi", refused: true},
		{name: "bytes with a percent prefix", dataUnit: "bytes", prefix: "%", refused: true},
		{name: "a unit nobody defines", dataUnit: "furlongs", prefix: "", refused: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, multiplier, ok := compileUnitNormalizer(test.dataUnit, test.prefix)
			if ok == test.refused {
				t.Fatalf("compileUnitNormalizer(%q, %q) ok=%t, want refused=%t", test.dataUnit, test.prefix, ok, test.refused)
			}
			if test.refused {
				return
			}
			if multiplier != test.want {
				t.Fatalf("compileUnitNormalizer(%q, %q) multiplier=%d, want %d",
					test.dataUnit, test.prefix, multiplier, test.want)
			}
		})
	}
}
