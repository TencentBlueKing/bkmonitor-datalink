// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"encoding/json"
	"math"
	"math/big"
	"os"
	"slices"
	"testing"
)

// platformUnits is testdata/bkmonitor_units.json: each unit id the platform
// defines (bk-monitor core/unit/init_data.py, the source the alert link's
// units_registry is generated from), its steps, the step it is written in and
// each step's factor to the base, as the platform's ScaledUnits computes them.
type platformUnits struct {
	Source string `json:"source"`
	Units  map[string]struct {
		Suffixes     []string `json:"suffixes"`
		DefaultIndex int      `json:"default_index"`
		Factors      []string `json:"factors"`
	} `json:"units"`
}

// unsupportedSteps are the platform's steps the table leaves out: their
// factors pass an int64 (1024^7, 1024^8, 1000^7, 1000^8), no strategy on
// either environment uses one, and a threshold at that scale has no real
// case (2026-10-28). A prefix in one is read as not given -- the
// threshold in the smallest step, as Python reads a suffix it cannot find
// -- and named (UNIT_PREFIX_OUTSIDE_STEPS_READ_AS_SMALLEST).
var unsupportedSteps = map[string]bool{"Zi": true, "Yi": true, "Z": true, "Y": true}

// The unit table is the platform's: every id it defines, the step it is
// written in, every step and its factor -- save the known unsupported steps,
// which are exactly the ones whose factor passes an int64 -- and the table
// defines no id the platform does not. A unit the table lacks compares raw
// where the platform converts, and a step it lacks reads a threshold as not
// given: both drift silently without this.
func TestTheUnitTableIsThePlatformsUnitDefinitions(t *testing.T) {
	payload, err := os.ReadFile("testdata/bkmonitor_units.json")
	if err != nil {
		t.Fatal(err)
	}
	var platform platformUnits
	if err := json.Unmarshal(payload, &platform); err != nil {
		t.Fatal(err)
	}
	if len(platform.Units) != 52 {
		t.Fatalf("the platform's list has %d ids, want the 52 the alert link's registry is generated from (%s)", len(platform.Units), platform.Source)
	}
	maxInt64 := new(big.Int).SetInt64(math.MaxInt64)
	for id, want := range platform.Units {
		spec, ok := unitSpecFor(id)
		if !ok {
			t.Errorf("the platform defines %q and the table does not", id)
			continue
		}
		var suffixes []string
		var factors []*big.Int
		for i, suffix := range want.Suffixes {
			factor, _ := new(big.Int).SetString(want.Factors[i], 10)
			if past := factor.Cmp(maxInt64) > 0; past != unsupportedSteps[suffix] {
				t.Errorf("%q step %q: past an int64 %t, listed unsupported %t", id, suffix, past, unsupportedSteps[suffix])
			}
			if unsupportedSteps[suffix] {
				continue
			}
			suffixes, factors = append(suffixes, suffix), append(factors, factor)
		}
		if !slices.Equal(spec.suffixes, suffixes) || spec.defaultIndex != want.DefaultIndex || len(spec.factorsToBase) != len(factors) {
			t.Errorf("%q: steps %v at %d, the platform's supported %v at %d", id, spec.suffixes, spec.defaultIndex, suffixes, want.DefaultIndex)
			continue
		}
		for i, factor := range factors {
			if big.NewInt(spec.factorsToBase[i]).Cmp(factor) != 0 {
				t.Errorf("%q step %q: factor %d, the platform's %s", id, suffixes[i], spec.factorsToBase[i], factor)
			}
		}
	}
	var table []string
	for id := range identityUnits {
		table = append(table, id)
	}
	for id := range binaryUnits {
		table = append(table, id)
	}
	for id := range decimalUnits {
		table = append(table, id)
	}
	for id := range timeUnits {
		table = append(table, id)
	}
	for id := range countUnits {
		table = append(table, id)
	}
	table = append(table, percentUnits...)
	for _, id := range table {
		if _, ok := platform.Units[id]; !ok && id != "" {
			t.Errorf("the table defines %q, which the platform does not", id)
		}
	}
	// A prefix in an unsupported step is outside the unit's steps, so the
	// control plane reads it as not given.
	for _, test := range []struct{ unit, prefix string }{{"bytes", "Yi"}, {"kbytes", "Zi"}, {"decbytes", "Y"}, {"bps", "Z"}} {
		if UnitPrefixInSteps(test.unit, test.prefix) {
			t.Errorf("%s with %s reads as in its steps", test.unit, test.prefix)
		}
	}
}
