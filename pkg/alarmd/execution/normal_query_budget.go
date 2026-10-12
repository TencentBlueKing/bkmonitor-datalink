// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import "context"

type normalQueryBudgetKey struct{}

// WithNormalQueryBudget marks an execution whose queries may run no longer
// than a normal round of the same Slot would let them: from the attempt's
// arrival, the span from the query's readiness to its deadline as the
// normal round plans it. A retry or replay otherwise gets its whole interval
// less the downstream reserve from its arrival, which for a Query Group read
// every minute is more than twice what a normal round has after its
// settling wait. The query cooldown pool marks every execution it lets run
// while a Query Group is in it, so the answer that takes the Query Group out
// is one its normal rounds can also get.
func WithNormalQueryBudget(ctx context.Context) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, normalQueryBudgetKey{}, true)
}

// NormalQueryBudget reports whether ctx was marked by WithNormalQueryBudget.
func NormalQueryBudget(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	marked, _ := ctx.Value(normalQueryBudgetKey{}).(bool)
	return marked
}

// PhysicalQueryClock is what one physical query had of its time and what it
// used: the budget from the moment it was sent to its deadline, and how long
// it took to come back - answered, timed out or failed. A Slot carries the
// slowest PRIMARY query's (SlotExecutionResult.PrimaryQueryClock), which is
// what says how far a backend that answered was from the budget it had.
type PhysicalQueryClock struct {
	BudgetMillis  int64 `json:"budget_ms"`
	ElapsedMillis int64 `json:"elapsed_ms"`
}
