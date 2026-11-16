// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// groupedFacts is a structured metric query grouped by dimensions, every
// function aggregating by them.
func groupedFacts(dimensions ...string) execution.QueryPlanFacts {
	return execution.QueryPlanFacts{Provider: execution.ProviderUQ, QueryList: []execution.QueryClause{{
		TableID: "system.cpu_summary", Dimensions: dimensions,
		Functions: []execution.QueryFunction{{Method: "mean", Dimensions: dimensions}},
	}}}
}

// The dimensions a Slot can be sliced on are the raw group-by labels every
// query of it shares, in the order the first query groups by; the reasons a
// Slot cannot be sliced at all are named.
func TestSliceableDimensionsAreTheRawGroupByLabelsEveryQueryShares(t *testing.T) {
	aliased := groupedFacts("host", "region")
	aliased.Normalization.DimensionAliases = map[string]string{"dimensions.region": "region"}
	without := groupedFacts("host", "region")
	without.QueryList[0].Functions = append(without.QueryList[0].Functions, execution.QueryFunction{Method: "sum", Without: true, Dimensions: []string{"host"}})
	promql := groupedFacts("host")
	promql.PromQL = &execution.PromQLQuery{Expression: "sum by (host) (up)"}
	bkdata := groupedFacts("host")
	bkdata.QueryList[0].DataSource = "bkdata"
	events := groupedFacts("host")
	events.SourceSemantics = []string{"custom/event"}
	elastic := groupedFacts("host")
	elastic.TSDBMap = map[string][]execution.QueryStorage{"a": {{StorageType: "elasticsearch"}}}
	custom := groupedFacts("host")
	custom.SourceSemantics = []string{"custom/time_series"}
	// Across series: a clause's top k of every series, a metric merge's.
	topk := groupedFacts("host")
	topk.QueryList[0].Functions = append(topk.QueryList[0].Functions, execution.QueryFunction{Method: "topk", Arguments: []execution.QueryScalar{{Kind: execution.QueryScalarNumber, NumberValue: "1"}}})
	topkByHost := groupedFacts("host")
	topkByHost.QueryList[0].Functions = append(topkByHost.QueryList[0].Functions, execution.QueryFunction{Method: "topk", Dimensions: []string{"host"},
		Arguments: []execution.QueryScalar{{Kind: execution.QueryScalarNumber, NumberValue: "1"}}})
	// A time aggregation is over each series' own window, whatever its name;
	// mean is the query service's avg.
	overTime := groupedFacts("host")
	overTime.QueryList[0].TimeAggregation = execution.QueryFunction{Method: "avg", Window: "60s"}
	meanOfEvery := groupedFacts("host")
	meanOfEvery.QueryList[0].Functions = append(meanOfEvery.QueryList[0].Functions, execution.QueryFunction{Method: "mean"})
	merged := func(expression string) execution.QueryPlanFacts {
		facts := groupedFacts("host")
		facts.MetricMerge = expression
		return facts
	}
	for _, test := range []struct {
		name   string
		facts  []execution.QueryPlanFacts
		want   []string
		reason string
	}{
		{name: "shared_in_first_order", facts: []execution.QueryPlanFacts{groupedFacts("region", "host", "zone"), groupedFacts("host", "region")},
			want: []string{"region", "host"}},
		{name: "custom_time_series", facts: []execution.QueryPlanFacts{custom}, want: []string{"host"}},
		{name: "aliased_left_out", facts: []execution.QueryPlanFacts{aliased}, want: []string{"host"}},
		{name: "aggregated_without", facts: []execution.QueryPlanFacts{without}, want: []string{"region"}},
		{name: "none_shared", facts: []execution.QueryPlanFacts{groupedFacts("host"), groupedFacts("region")}, reason: "dimension_not_grouped"},
		{name: "promql_first", facts: []execution.QueryPlanFacts{bkdata, promql}, reason: "promql"},
		{name: "bkdata", facts: []execution.QueryPlanFacts{groupedFacts("host"), bkdata}, reason: "storage_dialect"},
		{name: "events", facts: []execution.QueryPlanFacts{events}, reason: "storage_dialect"},
		{name: "elasticsearch", facts: []execution.QueryPlanFacts{elastic}, reason: "storage_dialect"},
		{name: "no_queries", reason: "storage_dialect"},
		{name: "clause_topk_of_every_series", facts: []execution.QueryPlanFacts{topk}, reason: "cross_series"},
		{name: "clause_topk_by_host", facts: []execution.QueryPlanFacts{topkByHost}, want: []string{"host"}},
		{name: "time_aggregation_avg", facts: []execution.QueryPlanFacts{overTime}, want: []string{"host"}},
		{name: "clause_mean_of_every_series", facts: []execution.QueryPlanFacts{meanOfEvery}, reason: "cross_series"},
		{name: "merge_absent", facts: []execution.QueryPlanFacts{merged("absent(a)")}, reason: "cross_series"},
		{name: "merge_topk", facts: []execution.QueryPlanFacts{merged("topk(1, a)")}, reason: "cross_series"},
		{name: "merge_sum", facts: []execution.QueryPlanFacts{merged("sum(a)")}, reason: "cross_series"},
		{name: "merge_on", facts: []execution.QueryPlanFacts{merged("a / on(region) b")}, reason: "cross_series"},
		{name: "merge_group_left", facts: []execution.QueryPlanFacts{merged("a * ignoring(host) group_left b")}, reason: "cross_series"},
		{name: "merge_selector", facts: []execution.QueryPlanFacts{merged(`a{host="x"}`)}, reason: "cross_series"},
		{name: "merge_divided", facts: []execution.QueryPlanFacts{merged("a / b")}, want: []string{"host"}},
		{name: "merge_or", facts: []execution.QueryPlanFacts{merged("a or b")}, want: []string{"host"}},
		{name: "merge_abs_of_difference", facts: []execution.QueryPlanFacts{merged("abs(a - b) > 10")}, want: []string{"host"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := execution.SliceableDimensions(test.facts...)
			if test.reason != "" {
				if reason, ok := execution.InputSliceUnsupportedOf(err); !ok || reason != test.reason || got != nil {
					t.Fatalf("SliceableDimensions() = %v, %v; want refused as %s", got, err, test.reason)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("SliceableDimensions() = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

// The provider routes result tables by some dimensions before it reads a
// series - bcs_cluster_id where a query names no table, apm_application_name
// for the APM relation metrics - and a slice on one can route to no table,
// answered as the target missing: they are not sliced on. Another the
// queries group by is; with none, the Slot cannot be sliced, named. A query
// that names its table is not routed by its cluster.
func TestTheProvidersRoutingDimensionsAreNotSlicedOn(t *testing.T) {
	container := func(dimensions ...string) execution.QueryPlanFacts {
		facts := groupedFacts(dimensions...)
		facts.QueryList[0].TableID = ""
		facts.QueryList[0].FieldName = "container_cpu_usage_seconds_total"
		return facts
	}
	relation := func(dimensions ...string) execution.QueryPlanFacts {
		facts := groupedFacts(dimensions...)
		facts.QueryList[0].FieldName = "apm_service_instance_with_pod_relation"
		return facts
	}
	for _, test := range []struct {
		name   string
		facts  []execution.QueryPlanFacts
		want   []string
		reason string
	}{
		{name: "cluster_with_another", facts: []execution.QueryPlanFacts{container("bcs_cluster_id", "channel_id")}, want: []string{"channel_id"}},
		{name: "cluster_alone", facts: []execution.QueryPlanFacts{container("bcs_cluster_id")}, reason: "dimension_not_grouped"},
		{name: "cluster_in_one_query_of_two", facts: []execution.QueryPlanFacts{groupedFacts("bcs_cluster_id", "pod"), container("bcs_cluster_id", "pod")}, want: []string{"pod"}},
		{name: "cluster_of_a_named_table", facts: []execution.QueryPlanFacts{groupedFacts("bcs_cluster_id", "pod")}, want: []string{"bcs_cluster_id", "pod"}},
		{name: "application_of_a_relation", facts: []execution.QueryPlanFacts{relation("apm_application_name", "service_name")}, want: []string{"service_name"}},
		{name: "application_of_another_metric", facts: []execution.QueryPlanFacts{groupedFacts("apm_application_name", "service_name")}, want: []string{"apm_application_name", "service_name"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := execution.SliceableDimensions(test.facts...)
			if test.reason != "" {
				if reason, ok := execution.InputSliceUnsupportedOf(err); !ok || reason != test.reason || got != nil {
					t.Fatalf("SliceableDimensions() = %v, %v; want refused as %s", got, err, test.reason)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("SliceableDimensions() = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

// A slice is checked against the Slot's dimensions: on one of them it can
// be read, on any other it is refused, and the zero slice is always read.
func TestCheckInputSliceNamesADimensionOutsideTheGroupBy(t *testing.T) {
	facts := []execution.QueryPlanFacts{groupedFacts("host", "region"), groupedFacts("host")}
	on := func(dimension string) execution.InputSlice {
		return execution.InputSlice{Conditions: []execution.InputSliceCondition{{Dimension: dimension, Pattern: "^(?s:.*)a$"}}}
	}
	if err := execution.CheckInputSlice(on("host"), facts...); err != nil {
		t.Fatalf("CheckInputSlice(host) = %v, want it read", err)
	}
	if reason, ok := execution.InputSliceUnsupportedOf(execution.CheckInputSlice(on("region"), facts...)); !ok || reason != "dimension_not_grouped" {
		t.Fatalf("CheckInputSlice(region) refused as %q, want dimension_not_grouped", reason)
	}
	promql := groupedFacts("host")
	promql.PromQL = &execution.PromQLQuery{Expression: "up"}
	if err := execution.CheckInputSlice(execution.InputSlice{}, promql); err != nil {
		t.Fatalf("CheckInputSlice(zero) = %v, want the whole Query Group read", err)
	}
}

// The header's dimensions are its requirements' query facts, the primary
// first; a requirement whose facts it does not carry leaves the store
// undetermined.
func TestTheHeadersSliceableDimensionsReadItsRequirementsFacts(t *testing.T) {
	header := execution.InternalExecutionHeader{
		Requirements: []execution.DataRequirement{
			{Role: execution.InputRoleAlgorithmDependency, LogicalQueryRef: "dependency"},
			{Role: execution.InputRolePrimary, LogicalQueryRef: "primary"},
		},
		QueryFacts: map[execution.LogicalQueryRef]execution.QueryPlanFacts{
			"primary": groupedFacts("host", "region"), "dependency": groupedFacts("region", "host"),
		},
	}
	if got, err := header.SliceableDimensions(); err != nil || !reflect.DeepEqual(got, []string{"host", "region"}) {
		t.Fatalf("SliceableDimensions() = %v, %v; want the primary's order host, region", got, err)
	}
	delete(header.QueryFacts, "dependency")
	if reason, ok := execution.InputSliceUnsupportedOf(func() error { _, err := header.SliceableDimensions(); return err }()); !ok || reason != "storage_dialect" {
		t.Fatalf("SliceableDimensions() without the dependency's facts refused as %q, want storage_dialect", reason)
	}
}
