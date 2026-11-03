// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"sort"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Rejections are labelled by budget and the ceilings are published separately,
// once for the metric and once for the page. A budget whose ceiling neither
// publishes still counts rejections, and those rejections then cannot be read
// against anything; a ceiling published under a name no rejection uses joins to
// nothing either. Both were held together only by string literals in three
// packages agreeing.
//
// That is the shape where one member goes missing from every copy at once and
// nothing notices, so this pins the two published sets against the one list.
func TestPublishedCeilingsCoverExactlyTheBudgetsRejectionsUse(t *testing.T) {
	expected := make([]string, 0, len(observability.CapacityBudgets()))
	for _, budget := range observability.CapacityBudgets() {
		expected = append(expected, string(budget))
	}
	sort.Strings(expected)

	facts := observability.RuntimeConfigFacts{}
	facts.Capacity.Series = 1
	facts.Capacity.RetainedBytes = 1
	facts.Capacity.StateMutations = 1
	facts.Capacity.Events = 1
	facts.Capacity.GapMutations = 1

	for _, published := range []struct {
		name string
		keys []string
	}{
		{"the metric's ceilings", budgetKeys(loadSourceBudgets(facts))},
		{"the page's ceilings", budgetKeys(snapshotBudgets(observabilityCapacity(facts.Capacity)))},
	} {
		got := append([]string(nil), published.keys...)
		sort.Strings(got)
		if len(got) != len(expected) {
			t.Fatalf("%s publishes %v, want %v", published.name, got, expected)
		}
		for index := range got {
			if got[index] != expected[index] {
				t.Fatalf("%s publishes %v, want %v", published.name, got, expected)
			}
		}
	}
}

func budgetKeys[V float64 | uint64](budgets map[string]V) []string {
	keys := make([]string, 0, len(budgets))
	for key := range budgets {
		keys = append(keys, key)
	}
	return keys
}
