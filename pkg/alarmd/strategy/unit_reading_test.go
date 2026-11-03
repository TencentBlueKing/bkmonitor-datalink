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

// unitDefaultPrefix is the step dataUnit itself is written in -- "ms" for
// ms, "Ki" for kbytes, "x100%" for percentunit -- and "" for a unit without
// steps or one the table does not define. A threshold given with this prefix
// is a threshold in the data's own unit.
func unitDefaultPrefix(dataUnit string) string {
	spec, ok := unitSpecOf(dataUnit)
	if !ok || spec.identity {
		return ""
	}
	return spec.suffixes[spec.defaultIndex]
}

// The control plane decides how a Level reads its unit before compiling it,
// from three questions to the unit table. Each is pinned in both directions.
func TestTheUnitTableAnswersWhatTheControlPlaneAsks(t *testing.T) {
	for _, test := range []struct {
		unit, prefix   string
		known, inSteps bool
		hasSteps       bool
		defaultPrefix  string
	}{
		{unit: "ms", prefix: "s", known: true, inSteps: true, hasSteps: true, defaultPrefix: "ms"},
		{unit: "ms", prefix: "Gi", known: true, inSteps: false, hasSteps: true, defaultPrefix: "ms"},
		{unit: "ms", prefix: "", known: true, inSteps: true, hasSteps: true, defaultPrefix: "ms"},
		{unit: "kbytes", prefix: "Mi", known: true, inSteps: true, hasSteps: true, defaultPrefix: "Ki"},
		{unit: "percentunit", prefix: "%", known: true, inSteps: true, hasSteps: true, defaultPrefix: "x100%"},
		{unit: "Time||ms", prefix: "s", known: true, inSteps: true, hasSteps: true, defaultPrefix: "ms"},
		// No steps: any prefix is nothing, and there is no step to default to.
		{unit: "", prefix: "%", known: true, inSteps: true, hasSteps: false, defaultPrefix: ""},
		{unit: "none", prefix: "ms", known: true, inSteps: true, hasSteps: false, defaultPrefix: ""},
		// Not in the table: nothing can be answered for it.
		{unit: "furlongs", prefix: "", known: false, inSteps: false, hasSteps: false, defaultPrefix: ""},
		{unit: "us", prefix: "", known: false, inSteps: false, hasSteps: false, defaultPrefix: ""},
	} {
		if got := UnitKnown(test.unit); got != test.known {
			t.Errorf("UnitKnown(%q) = %t, want %t", test.unit, got, test.known)
		}
		if got := UnitPrefixInSteps(test.unit, test.prefix); got != test.inSteps {
			t.Errorf("UnitPrefixInSteps(%q, %q) = %t, want %t", test.unit, test.prefix, got, test.inSteps)
		}
		if got := UnitHasSteps(test.unit); got != test.hasSteps {
			t.Errorf("UnitHasSteps(%q) = %t, want %t", test.unit, got, test.hasSteps)
		}
		if got := unitDefaultPrefix(test.unit); got != test.defaultPrefix {
			t.Errorf("unitDefaultPrefix(%q) = %q, want %q", test.unit, got, test.defaultPrefix)
		}
	}
}

// A threshold given in the unit's own step compares raw: the value and the
// threshold are scaled by the same factor. That is how a Level whose prefix
// cannot be read compares raw without leaving its Plan's unit. The partner
// rows show what an empty prefix does instead: the threshold in the smallest
// step, so 20 against ms is 20 ns.
func TestAThresholdInTheUnitsOwnStepComparesRaw(t *testing.T) {
	for _, unit := range []string{"ms", "s", "kbytes", "MBs", "percentunit", "percent", "bytes", "none", ""} {
		normalizer, threshold, ok := compileUnitNormalizer(unit, unitDefaultPrefix(unit))
		if !ok {
			t.Fatalf("compileUnitNormalizer(%q, own step) refused", unit)
		}
		if normalizer.sourceMultiplier != threshold {
			t.Errorf("unit %q in its own step scales the value by %d and the threshold by %d, want equal", unit, normalizer.sourceMultiplier, threshold)
		}
	}
	normalizer, threshold, ok := compileUnitNormalizer("ms", "")
	if !ok || normalizer.sourceMultiplier != 1_000_000 || threshold != 1 {
		t.Fatalf("ms with an empty prefix = value %d threshold %d (ok %t), want value 1000000 threshold 1", normalizer.sourceMultiplier, threshold, ok)
	}
}
