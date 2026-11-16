// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"slices"
	"strings"
)

// The reasons an InputSliceUnsupportedError names. The set is closed: a row
// that carries one is read by these words alone.
const (
	// InputSliceReasonPromQL is a query that is one PromQL expression: the
	// conditions would have to go into every selector, and whether a
	// dimension survives each selector's expression to the output is not
	// decided here (decision-020 batch 8, 7.3.1).
	InputSliceReasonPromQL = "promql"
	// InputSliceReasonStorageDialect is a query whose store does not read
	// the conditions' patterns as anchored RE2, or whose store cannot be
	// told from its facts.
	InputSliceReasonStorageDialect = "storage_dialect"
	// InputSliceReasonDimensionNotGrouped is a dimension sliced on that is
	// not a raw group-by label of every physical query of the Slot, or one
	// the provider routes a query's result tables by (routedBy): no
	// dimension the slices can rest on.
	InputSliceReasonDimensionNotGrouped = "dimension_not_grouped"
	// InputSliceReasonSupplement is a slice asked of a supplement, which
	// replays a kept read of the whole Query Group and sends no query the
	// conditions could go into.
	InputSliceReasonSupplement = "supplement"
	// InputSliceReasonCrossSeries is a query that combines series across
	// the dimensions sliced on: a clause function that aggregates over
	// every series (topk, bottomk, sum, ... with no grouping), or a metric
	// merge other than an element-wise combination of its references. Each
	// slice would answer its own top k, sum or match, and the union of the
	// slices would not be the whole read's answer.
	InputSliceReasonCrossSeries = "cross_series"
)

// crossSeriesFunctions are the functions that, with no grouping of their
// own, aggregate over every series they are given: PromQL's aggregation
// operators (controlplane promQLAggregations) and mean, the query service's
// name for avg that the compiler writes.
var crossSeriesFunctions = map[string]bool{
	"sum": true, "min": true, "max": true, "avg": true, "mean": true, "group": true, "stddev": true, "stdvar": true, "count": true,
	"count_values": true, "bottomk": true, "topk": true, "quantile": true, "limitk": true, "limit_ratio": true,
}

// elementwiseFunctions are the functions a metric merge may apply and stay
// element-wise: each output series is its input series' own value changed.
var elementwiseFunctions = map[string]bool{
	"abs": true, "ceil": true, "floor": true, "round": true, "ln": true, "log2": true, "log10": true,
	"sgn": true, "sqrt": true, "exp": true, "clamp": true, "clamp_min": true, "clamp_max": true,
}

// matchingModifiers are the words that make a binary operation match
// series on fewer labels than all of them, or many to one, or that group
// an aggregation: any of them can match series of two slices together.
var matchingModifiers = map[string]bool{
	"on": true, "ignoring": true, "group_left": true, "group_right": true, "by": true, "without": true,
}

// sliceableDataSources are the query-list data sources whose conditions the
// provider renders as Prometheus matchers for VictoriaMetrics: the unnamed
// source the metric compiler leaves (the provider reads it as bkmonitor) and
// bkmonitor named. bkdata goes to BkSql, which reads req as the Doris or
// MySQL dialect, and bklog and bkapm go to Elasticsearch, whose Lucene
// regexp has no anchors: both would put a series in no slice or in two.
var sliceableDataSources = map[string]bool{"": true, "bkmonitor": true}

// sliceableSourceSemantics are the platform sources a sliceable query may
// name: metric time series, which the provider reads from VictoriaMetrics. A
// query naming no source is the platform's own metric time series.
var sliceableSourceSemantics = map[string]bool{"bk_monitor/time_series": true, "custom/time_series": true}

// SliceableDimensions is the dimensions a Slot whose physical queries run
// with facts can be sliced on: the group-by labels every query clause of
// every one of them carries, as the store has them, in the order the first
// clause groups by. It is an InputSliceUnsupportedError when the Slot cannot
// be sliced at all: a PromQL query, a store outside the regex dialect the
// slices' partition rests on, or no dimension every query groups by that
// the provider does not route by.
//
// A dimension has to be in every query's group-by because the conditions
// filter before aggregating: grouped by it, filtering on it keeps whole
// groups and changes no aggregated value. It has to be the store's own label
// because the conditions are read by the store: a label renamed on the way
// out names nothing the store has. And it cannot be one the provider routes
// result tables by (routedBy): a slice on it can route to no table.
func SliceableDimensions(facts ...QueryPlanFacts) ([]string, error) {
	if len(facts) == 0 {
		return nil, &InputSliceUnsupportedError{Reason: InputSliceReasonStorageDialect}
	}
	for _, fact := range facts {
		if fact.PromQL != nil {
			return nil, &InputSliceUnsupportedError{Reason: InputSliceReasonPromQL}
		}
	}
	for _, fact := range facts {
		if !sliceableDialect(fact) {
			return nil, &InputSliceUnsupportedError{Reason: InputSliceReasonStorageDialect}
		}
	}
	for _, fact := range facts {
		if crossSeries(fact) {
			return nil, &InputSliceUnsupportedError{Reason: InputSliceReasonCrossSeries}
		}
	}
	var candidates []string
	for _, dimension := range facts[0].QueryList[0].Dimensions {
		if dimension != "" && !slices.Contains(candidates, dimension) && groupedByEvery(dimension, facts) && !routedBy(dimension, facts) {
			candidates = append(candidates, dimension)
		}
	}
	if len(candidates) == 0 {
		return nil, &InputSliceUnsupportedError{Reason: InputSliceReasonDimensionNotGrouped}
	}
	return candidates, nil
}

// CheckInputSlice is whether slice can be read of a Slot whose physical
// queries run with facts: nil for the zero slice, which every Slot can be
// read in, and otherwise the InputSliceUnsupportedError SliceableDimensions
// names, or dimension_not_grouped for a condition on a dimension outside
// them.
func CheckInputSlice(slice InputSlice, facts ...QueryPlanFacts) error {
	if slice.IsZero() {
		return nil
	}
	if err := slice.Validate(); err != nil {
		return err
	}
	dimensions, err := SliceableDimensions(facts...)
	if err != nil {
		return err
	}
	for _, condition := range slice.Conditions {
		if !slices.Contains(dimensions, condition.Dimension) {
			return &InputSliceUnsupportedError{Reason: InputSliceReasonDimensionNotGrouped}
		}
	}
	return nil
}

// SliceableDimensions is SliceableDimensions of the query facts of the
// header's requirements, the primary requirements' first so the order is
// the Plan's own group-by. A requirement whose facts the header does not
// carry leaves the store undetermined, and a store that cannot be told is
// not one the slices can rest on.
func (header InternalExecutionHeader) SliceableDimensions() ([]string, error) {
	facts := make([]QueryPlanFacts, 0, len(header.Requirements))
	for _, primary := range []bool{true, false} {
		for _, requirement := range header.Requirements {
			if (requirement.Role == InputRolePrimary) != primary {
				continue
			}
			fact, found := header.QueryFacts[requirement.LogicalQueryRef]
			if !found {
				return nil, &InputSliceUnsupportedError{Reason: InputSliceReasonStorageDialect}
			}
			facts = append(facts, fact)
		}
	}
	return SliceableDimensions(facts...)
}

// sliceableDialect is whether every clause of facts is read by
// VictoriaMetrics through the provider's Prometheus matchers, as far as the
// facts tell: a structured UQ query with no Elasticsearch routing, of metric
// time series only, from a data source that routes there.
func sliceableDialect(facts QueryPlanFacts) bool {
	if facts.Provider != ProviderUQ || len(facts.TSDBMap) != 0 || len(facts.QueryList) == 0 {
		return false
	}
	for _, source := range facts.SourceSemantics {
		if !sliceableSourceSemantics[source] {
			return false
		}
	}
	for _, clause := range facts.QueryList {
		if !sliceableDataSources[clause.DataSource] {
			return false
		}
	}
	return true
}

// The provider's routing dimensions: the query service compares a query's
// conditions with these result table properties when it routes, before it
// reads any series, and drops a table the conditions do not match. A slice
// condition on one of them can leave no table at all, which the provider
// answers SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS - the query's target missing -
// for a slice that is only empty. The slice is not read as empty here: the
// dimension is not sliced on.
//
// A contract with the query service's routing (unify-query 3.9.4896,
// query/structured/space.go and condition.go); a change there is a change
// here:
//   - bcs_cluster_id against each table's cluster, in the container route,
//     which a query naming no table takes (space.go:155-180, isK8s at
//     :386,410-412; ClusterID in condition_field.go:58). The empty slice's
//     negated pattern drops every table with a cluster.
//   - apm_application_name against each table's label, for the three APM
//     relation metrics, whatever table is named (space.go:113-128;
//     ApmApplicationName in condition_field.go:59).
//
// table_id_conditions, the third comparison (space.go:138-150,369-372),
// reads a field this provider's requests never carry.
const (
	routingClusterDimension     = "bcs_cluster_id"
	routingApplicationDimension = "apm_application_name"
)

// apmRelationMetrics are the metrics the query service routes by
// apm_application_name (space.go:113-128).
var apmRelationMetrics = map[string]bool{
	"apm_service_with_apm_service_instance_relation": true,
	"apm_service_instance_with_pod_relation":         true,
	"apm_service_instance_with_system_relation":      true,
}

// routedBy is whether the provider routes any clause of facts by dimension:
// bcs_cluster_id where the clause names no table (no database before its
// first dot), apm_application_name where it reads an APM relation metric.
func routedBy(dimension string, facts []QueryPlanFacts) bool {
	for _, fact := range facts {
		for _, clause := range fact.QueryList {
			switch dimension {
			case routingClusterDimension:
				if database, _, _ := strings.Cut(clause.TableID, "."); database == "" {
					return true
				}
			case routingApplicationDimension:
				if apmRelationMetrics[clause.FieldName] {
					return true
				}
			}
		}
	}
	return false
}

// groupedByEvery is whether dimension is a raw group-by label of every
// clause of every facts: in the clause's group-by and in the group-by of
// every aggregating function that names one, removed by no function that
// aggregates without it, and neither renamed to nor from by the facts'
// normalization.
func groupedByEvery(dimension string, facts []QueryPlanFacts) bool {
	for _, fact := range facts {
		for from, to := range fact.Normalization.DimensionAliases {
			if from == dimension || to == dimension {
				return false
			}
		}
		for _, clause := range fact.QueryList {
			if !slices.Contains(clause.Dimensions, dimension) {
				return false
			}
			for _, function := range append([]QueryFunction{clause.TimeAggregation}, clause.Functions...) {
				if len(function.Dimensions) == 0 {
					continue
				}
				if slices.Contains(function.Dimensions, dimension) == function.Without {
					return false
				}
			}
		}
	}
	return true
}

// crossSeries is whether facts combine series across any dimension: a
// clause function aggregating every series with no grouping, or a metric
// merge that is not element-wise. A clause's time aggregation folds each
// series' own points over its window and never crosses series.
func crossSeries(facts QueryPlanFacts) bool {
	for _, clause := range facts.QueryList {
		for _, function := range clause.Functions {
			if crossSeriesFunctions[strings.ToLower(function.Method)] && len(function.Dimensions) == 0 && !function.Without {
				return true
			}
		}
	}
	return !elementwiseMerge(facts.MetricMerge)
}

// elementwiseMerge is whether a metric merge expression combines its
// references series by series: references, numbers, arithmetic and
// comparison operators, and, or, unless and bool, and the element-wise
// functions, nothing else. Binary operators without a matching modifier
// match series on all their labels, the dimension sliced on among them, so
// no two slices' series meet. Anything it cannot read - a selector, a range,
// a string, a modifier, another function - is taken to cross series. The
// empty expression is the references joined by or.
func elementwiseMerge(expression string) bool {
	s := strings.ToLower(expression)
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '(' || c == ')' || c == ',' || strings.IndexByte("+-*/%^=!<>.", c) >= 0:
			i++
		case c >= '0' && c <= '9':
			i++
		case c == '_' || (c >= 'a' && c <= 'z'):
			j := i
			for j < len(s) && (s[j] == '_' || (s[j] >= 'a' && s[j] <= 'z') || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			word := s[i:j]
			k := j
			for k < len(s) && (s[k] == ' ' || s[k] == '\t' || s[k] == '\n') {
				k++
			}
			called := k < len(s) && s[k] == '('
			switch {
			case matchingModifiers[word] || crossSeriesFunctions[word]:
				return false
			case called && !elementwiseFunctions[word]:
				return false
			}
			i = j
		default:
			return false
		}
	}
	return true
}
