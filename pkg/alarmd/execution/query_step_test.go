// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A query step is carried only by an unaligned query and only below its
// bucket width; anything else is refused, and zero serializes as nothing.
func TestAQueryStepIsBelowTheBucketWidthOfAnUnalignedQuery(t *testing.T) {
	for _, test := range []struct {
		name      string
		step      int64
		unaligned bool
		valid     bool
	}{
		{"none", 0, false, true},
		{"none on an unaligned query", 0, true, true},
		{"a quarter of the bucket", 15_000, true, true},
		{"on an aligned query", 15_000, false, false},
		{"the bucket width", 60_000, true, false},
		{"above the bucket width", 120_000, true, false},
		{"negative", -15_000, true, false},
	} {
		facts := validQueryPlanFacts()
		facts.QueryStepMillis, facts.NotTimeAlign = test.step, test.unaligned
		built, err := execution.BuildQueryPlanFacts(facts)
		if (err == nil) != test.valid {
			t.Fatalf("%s: error %v, want valid=%v", test.name, err, test.valid)
		}
		if err != nil {
			continue
		}
		encoded, err := json.Marshal(built)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "QueryStepMillis") != (test.step != 0) {
			t.Fatalf("%s: encoded %s", test.name, encoded)
		}
	}
}

// A window of one bucket asked a step apart is asked for its first step; a
// window of several buckets ends where its last bucket inside it starts, a
// step later. Without a query step the window is asked for whole.
func TestARequestRangeEndsAStepAfterTheLastBucketInsideTheWindow(t *testing.T) {
	facts := validQueryPlanFacts()
	facts.NotTimeAlign, facts.QueryStepMillis = true, 15_000
	stepped, err := execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	for _, window := range []execution.QueryWindow{{Start: 1000, End: 1060}, {Start: 1000, End: 2560}} {
		asked := stepped.RequestRange(window)
		if asked.Start != window.Start || asked.End != window.End-60+15 {
			t.Fatalf("window %+v asked over %+v", window, asked)
		}
	}
	plain, err := execution.BuildQueryPlanFacts(validQueryPlanFacts())
	if err != nil {
		t.Fatal(err)
	}
	window := execution.QueryWindow{Start: 1000, End: 1060}
	if plain.RequestRange(window) != window || plain.RequestStepMillis() != 60_000 || stepped.RequestStepMillis() != 15_000 {
		t.Fatalf("plain asked %+v step %d, stepped step %d", plain.RequestRange(window), plain.RequestStepMillis(), stepped.RequestStepMillis())
	}
}

// A physical query asked a step apart may end before its window does, by
// exactly the part no bucket inside the window starts in, and no earlier.
func TestAPhysicalQueryAskedAStepApartMayEndBeforeItsWindow(t *testing.T) {
	facts := validQueryPlanFacts()
	facts.NotTimeAlign, facts.QueryStepMillis = true, 15_000
	stepped, err := execution.BuildQueryPlanFacts(facts)
	if err != nil {
		t.Fatal(err)
	}
	window := execution.QueryWindow{Start: 1000, End: 1060}
	one := execution.QueryWindow{Start: 1000, End: 1015}
	if _, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: stepped, LogicalWindow: window,
		ProviderRange: one, AcceptedRange: one, RequiredColumns: []string{"value"}}); err != nil {
		t.Fatalf("one step of a stepped window: %v", err)
	}
	short := execution.QueryWindow{Start: 1000, End: 1010}
	if _, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: stepped, LogicalWindow: window,
		ProviderRange: short, AcceptedRange: short, RequiredColumns: []string{"value"}}); err == nil {
		t.Fatal("a range shorter than one step was accepted")
	}
	plain, err := execution.BuildQueryPlanFacts(validQueryPlanFacts())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execution.BuildPhysicalQuerySpec(execution.PhysicalQuerySpec{PlanFacts: plain, LogicalWindow: window,
		ProviderRange: one, AcceptedRange: one, RequiredColumns: []string{"value"}}); err == nil {
		t.Fatal("a query without a step was asked for less than its window")
	}
}
