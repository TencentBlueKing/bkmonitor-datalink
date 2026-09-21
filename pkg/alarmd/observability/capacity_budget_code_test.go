// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import "testing"

// Every budget has a code a reader can parse, and no two share one.
//
// The defect this is here for was a budget being published as its own label
// value: lower case, which the code grammar refuses, so the fleet normalised
// it away and the page kept only the free text. It went unnoticed because
// nothing compared the two spellings -- the code was whatever the label
// happened to be, and the label is not wrong, it is just not a code.
//
// Driven off the published list rather than a list written here, so a budget
// added later is covered by this the moment it exists. That is the half that
// matters: a hand-written list beside a hand-written switch is two copies that
// agree until somebody adds to one.
func TestEveryCapacityBudgetHasAParsableAndDistinctFailureCode(t *testing.T) {
	seen := make(map[string]CapacityBudget, len(CapacityBudgets())+1)
	// The catch-all too: a rejection by a budget this build does not recognise
	// still reaches the page, and it reaching it as an unparsable code is the
	// same defect wearing the unrecognised case.
	for _, budget := range append(CapacityBudgets(), CapacityBudgetOther) {
		code := CapacityBudgetFailureCode(budget)
		if !ValidQueryFailureCode(code) {
			t.Fatalf("budget %q publishes code %q, which the failure code grammar refuses: "+
				"fleet normalises it to OTHER and the page is left with the free text", budget, code)
		}
		if other, clash := seen[code]; clash {
			t.Fatalf("budgets %q and %q share code %q; a rejection could not say which one it was",
				other, budget, code)
		}
		seen[code] = budget
	}
}
