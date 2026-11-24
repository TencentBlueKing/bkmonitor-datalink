// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A merged expression filters its values when it compares them outside a
// quoted string; a PromQL query, or an expression the tokenizer cannot read,
// says nothing.
func TestAValueFilterIsAComparisonOutsideQuotes(t *testing.T) {
	for _, test := range []struct {
		name  string
		facts execution.QueryPlanFacts
		want  bool
	}{
		{name: "OsRestart's filter", facts: execution.QueryPlanFacts{MetricMerge: "a <= 3600"}, want: true},
		{name: "a comparison written by the owner", facts: execution.QueryPlanFacts{MetricMerge: "a > 0"}, want: true},
		{name: "equality", facts: execution.QueryPlanFacts{MetricMerge: "a == 1"}, want: true},
		{name: "arithmetic only", facts: execution.QueryPlanFacts{MetricMerge: "a + b * 2"}, want: false},
		{name: "a bare metric", facts: execution.QueryPlanFacts{MetricMerge: "a"}, want: false},
		{name: "a comparison inside quotes", facts: execution.QueryPlanFacts{MetricMerge: `label_replace(a, "x", "<=", "y", "")`}, want: false},
		{name: "PromQL", facts: execution.QueryPlanFacts{MetricMerge: "a <= 3600", PromQL: &execution.PromQLQuery{Expression: "up <= 1"}}, want: false},
		{name: "an unreadable expression", facts: execution.QueryPlanFacts{MetricMerge: "a <= 3600 {"}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := readsValueFilter(test.facts); got != test.want {
				t.Fatalf("readsValueFilter(%q) = %t, want %t", test.facts.MetricMerge, got, test.want)
			}
		})
	}
}

// A dependency witnesses only when it reads the primary's sources and clauses
// through its own query, filters nothing, and does so for every Level it
// serves.
func TestAWitnessReadsThePrimarysSourceUnfilteredForEveryConsumer(t *testing.T) {
	clause := []execution.QueryClause{{ReferenceName: "a"}}
	other := []execution.QueryClause{{ReferenceName: "b"}}
	primaryFacts := execution.QueryPlanFacts{SourceSemantics: []string{"bk_monitor/time_series"}, QueryList: clause, MetricMerge: "a <= 3600"}
	consumer := func(strategy string) execution.DataRequirementConsumer {
		return execution.DataRequirementConsumer{Consumer: execution.ConsumerRef{Plan: execution.PlanIdentity{StrategyID: strategy}, LevelID: 1, HasLevel: true}}
	}
	for _, test := range []struct {
		name      string
		ref       execution.LogicalQueryRef
		facts     execution.QueryPlanFacts
		consumers []execution.DataRequirementConsumer
		want      bool
	}{
		{name: "the same source, unfiltered, its own query", ref: "q-history",
			facts:     execution.QueryPlanFacts{SourceSemantics: []string{"bk_monitor/time_series"}, QueryList: clause, MetricMerge: "a"},
			consumers: []execution.DataRequirementConsumer{consumer("1")}, want: true},
		{name: "another source", ref: "q-history",
			facts:     execution.QueryPlanFacts{SourceSemantics: []string{"bk_monitor/time_series"}, QueryList: other, MetricMerge: "b"},
			consumers: []execution.DataRequirementConsumer{consumer("1")}},
		{name: "the same source, filtered", ref: "q-history",
			facts:     execution.QueryPlanFacts{SourceSemantics: []string{"bk_monitor/time_series"}, QueryList: clause, MetricMerge: "a > 0"},
			consumers: []execution.DataRequirementConsumer{consumer("1")}},
		{name: "the primary's own query", ref: "q-primary",
			facts:     execution.QueryPlanFacts{SourceSemantics: []string{"bk_monitor/time_series"}, QueryList: clause, MetricMerge: "a"},
			consumers: []execution.DataRequirementConsumer{consumer("1")}},
		{name: "shared with a Plan whose primary it does not read", ref: "q-history",
			facts:     execution.QueryPlanFacts{SourceSemantics: []string{"bk_monitor/time_series"}, QueryList: clause, MetricMerge: "a"},
			consumers: []execution.DataRequirementConsumer{consumer("1"), consumer("2")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			byID := map[execution.RequirementID]*execution.DataRequirement{
				"primary-1": {RequirementID: "primary-1", Role: execution.InputRolePrimary, LogicalQueryRef: "q-primary",
					Consumers: []execution.DataRequirementConsumer{consumer("1")}},
				"primary-2": {RequirementID: "primary-2", Role: execution.InputRolePrimary, LogicalQueryRef: "q-primary-2",
					Consumers: []execution.DataRequirementConsumer{consumer("2")}},
				"dependency": {RequirementID: "dependency", Role: execution.InputRoleAlgorithmDependency, LogicalQueryRef: test.ref,
					Consumers: test.consumers},
			}
			primaries := map[execution.RequirementID]execution.QueryPlanFacts{
				"primary-1": primaryFacts,
				"primary-2": {SourceSemantics: []string{"bk_monitor/time_series"}, QueryList: other, MetricMerge: "b"},
			}
			markUnfilteredWitnesses(byID, primaries, map[execution.RequirementID]execution.QueryPlanFacts{"dependency": test.facts})
			if got := byID["dependency"].UnfilteredWitness; got != test.want {
				t.Fatalf("witness %t, want %t", got, test.want)
			}
			if byID["primary-1"].UnfilteredWitness {
				t.Fatal("a primary marked a witness")
			}
		})
	}
}
