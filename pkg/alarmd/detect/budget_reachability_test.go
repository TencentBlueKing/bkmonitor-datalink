// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package detect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// admitPlans checks the three Plan budgets in a fixed order, and the three
// quantities they bound are ordered by construction:
//
//	len(groups) <= len(records) <= SelectedCount()
//
// collectSelectedRecords appends one record per selected slot and only when
// the slot is valid, and groupSelectedRecords emits one group per distinct
// dimension digest. selected_records_per_plan is checked first, against the
// largest of the three. So series_per_plan and records_per_series can only
// fire when their budget is strictly below the record budget - at equal
// budgets the record check has already returned.
//
// Every other test of these two budgets sets one of them to 1 and leaves the
// record budget generous, which constructs the reachability it then observes.
// That is the right way to test what the branch does, and it is why nothing
// noticed that the shipped defaults set all three to the same number. This
// test covers the other half: at equal budgets the two refinements are not
// rare, they are unreachable.
func TestPlanRefinementBudgetsCannotFireWhileEqualToTheRecordBudget(t *testing.T) {
	shapes := map[string]func(int) []fixtureRecord{
		"every record its own series": distinctSeriesFixtureRecords,
		"every record in one series":  singleSeriesFixtureRecords,
	}
	for name, shape := range shapes {
		for _, budget := range []uint64{1, 2, 3, 8} {
			t.Run(fmt.Sprintf("%s/budget=%d", name, budget), func(t *testing.T) {
				limits := generousLimits()
				limits.MaxSelectedRecordsPerPlan = budget
				limits.MaxSeriesPerPlan = budget
				limits.MaxRecordsPerSeries = budget
				// One more record than any of the three budgets admits, so a
				// Plan budget must fire; which one is the question.
				budgetError := evaluateForBudgetError(t, shape(int(budget)+1), limits)
				if budgetError.Budget != "selected_records_per_plan" {
					t.Fatalf("budget = %q, want selected_records_per_plan: a refinement fired at an equal budget",
						budgetError.Budget)
				}
			})
		}
	}
}

// The sweep above is only worth reading if this harness can see a refinement
// fire at all. Without this, "no refinement fired" is equally well explained
// by a harness that cannot reach them, and the sweep would be vacuously green.
func TestPlanRefinementBudgetsFireWhenBelowTheRecordBudget(t *testing.T) {
	series := generousLimits()
	series.MaxSelectedRecordsPerPlan = 4
	series.MaxSeriesPerPlan = 2
	if got := evaluateForBudgetError(t, distinctSeriesFixtureRecords(3), series).Budget; got != "series_per_plan" {
		t.Fatalf("budget = %q, want series_per_plan", got)
	}

	records := generousLimits()
	records.MaxSelectedRecordsPerPlan = 4
	records.MaxRecordsPerSeries = 2
	if got := evaluateForBudgetError(t, singleSeriesFixtureRecords(3), records).Budget; got != "records_per_series" {
		t.Fatalf("budget = %q, want records_per_series", got)
	}
}

func evaluateForBudgetError(t testing.TB, records []fixtureRecord, limits ExecutionLimits) *BudgetError {
	t.Helper()
	plans := []contract.EvaluationPlanV2{
		fixturePlan("1001", []contract.LevelIRV2{fixtureLevel(1, 1, contract.LevelConnectorAND,
			fixtureThresholdAlgorithm("GT", "50", "percent", ""))}),
	}
	envelope := fixtureEnvelope(t, plans, records, contract.QueryCompletenessFull)
	input, executions, digest := fixtureExecutions(t, envelope)
	evaluator := newTestEvaluator(t)
	_, err := evaluator.Evaluate(context.Background(), EvaluateRequest{
		Completeness: input.Execution().Completeness, DatasetContractDigest: digest,
		Plans: executions, Limits: limits,
	})
	var budgetError *BudgetError
	if !errors.As(err, &budgetError) {
		t.Fatalf("Evaluate() error = %v, want BudgetError", err)
	}
	if budgetError.Scope != BudgetScopePlan {
		t.Fatalf("scope = %s, want PLAN", budgetError.Scope)
	}
	return budgetError
}

func distinctSeriesFixtureRecords(count int) []fixtureRecord {
	records := make([]fixtureRecord, count)
	for index := range records {
		records[index] = fixtureRecord{
			host: fmt.Sprintf("host-%d", index), sourceTime: 100, value: json.RawMessage(`60`),
		}
	}
	return records
}

func singleSeriesFixtureRecords(count int) []fixtureRecord {
	records := make([]fixtureRecord, count)
	for index := range records {
		records[index] = fixtureRecord{
			host: "host", sourceTime: int64(100 + index*60), value: json.RawMessage(`60`),
		}
	}
	return records
}
