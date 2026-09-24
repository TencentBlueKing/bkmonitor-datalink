// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// MaxShardConditionValues bounds how many values one piece's matcher names.
//
// The piece that matches nothing else has to negate every value the others
// name, so it carries their sum and meets this first. It is above the
// census's own per-dimension bound for that reason: a census names at most
// MaxCensusValuesPerDimension values in total, and the negation of all of
// them has to fit.
const MaxShardConditionValues = 8192

// shardMatcherDomain names the digest that identifies a piece's matcher. A
// re-split that changes which values a piece takes changes this, and the
// per-Plan record keys take it, so the piece's gap marker and no-data memory
// start from zero exactly when its series change (decision-020 4.7.4).
const shardMatcherDomain = "alarmd-shard-matcher-v1"

// shardValueOperators are what a piece's condition is written with: the
// operator the legacy compiler maps "equals one of" and "equals none of"
// onto, so a matcher reads to the backend exactly as a user-written
// condition on the same dimension would.
const (
	shardValueOperatorIn    = "contains"
	shardValueOperatorNotIn = "ncontains"
)

// ShardedQueries is one piece: which slice of the strategy it is, and the
// queries that select that slice.
type ShardedQueries struct {
	Shard   execution.ShardRef
	Queries map[execution.LogicalQueryRef]execution.QueryPlanFacts
}

// ShardQueries turns a planned split into the queries its pieces run, or
// names the shape of the strategy that stops it.
//
// Every piece gets the same treatment on every one of the Plan's logical
// queries: a Plan whose queries selected different series per piece would
// not be a partition of anything. The last piece carries the negation of
// every value the others name, which is what makes the pieces a cover -
// including for values that appear after the census was taken.
//
// It builds and refuses; it publishes nothing and decides nothing about
// whether the split should happen. That decision is the planner's, and the
// two are apart because they fail for unrelated reasons: a strategy can be
// perfectly worth splitting and impossible to express, which is precisely
// the population that needs a matcher this one cannot write.
func ShardQueries(
	plan execution.PlanIdentity,
	queries map[execution.LogicalQueryRef]execution.QueryPlanFacts,
	split SplitPlan,
) ([]ShardedQueries, observability.ShardQueryFacts) {
	shards := len(split.Lists) + 1
	facts := observability.ShardQueryFacts{
		StrategyID: plan.StrategyID, BusinessID: plan.BusinessID,
		Dimension: split.Dimension, Shards: shards, Queries: len(queries),
	}
	if len(queries) == 0 {
		// The strategy's shape, not this build's defect: a deployment may
		// legitimately hold such a Plan, and a standing INVALID would send
		// its reader to the code.
		facts.Outcome = observability.ShardQueriesNoQueries
		return nil, facts
	}
	if split.Dimension == "" || len(split.Lists) == 0 {
		facts.Outcome = observability.ShardQueriesNotPlanned
		return nil, facts
	}
	fallback := make([]string, 0, MaxShardConditionValues)
	for _, list := range split.Lists {
		facts.Values += len(list)
		fallback = append(fallback, list...)
	}
	sort.Strings(fallback)
	facts.FallbackValues = len(fallback)
	if facts.Values > MaxShardConditionValues || facts.FallbackValues > MaxShardConditionValues {
		facts.Outcome = observability.ShardQueriesTooManyValues
		return nil, facts
	}
	for _, source := range queries {
		if outcome := queryAdmitsShardMatcher(source, split.Dimension); outcome != observability.ShardQueriesBuilt {
			facts.Outcome = outcome
			return nil, facts
		}
	}

	built := make([]ShardedQueries, 0, shards)
	for index := 0; index < shards; index++ {
		values, operator := fallback, shardValueOperatorNotIn
		if index < len(split.Lists) {
			values, operator = split.Lists[index], shardValueOperatorIn
		}
		// Zero-based, as ShardRef counts: piece zero of a split strategy has
		// the same key shape an unsplit Plan has, which is what lets a
		// strategy's records keep their bytes when it is not split.
		shard, err := shardRefOf(split.Dimension, index, shards, operator, values)
		if err != nil {
			facts.Outcome, facts.Detail = observability.ShardQueriesInvalid, observability.ShardQueryDetail(err.Error())
			return nil, facts
		}
		piece := ShardedQueries{Shard: shard, Queries: make(map[execution.LogicalQueryRef]execution.QueryPlanFacts, len(queries))}
		for ref, source := range queries {
			sharded, err := shardQueryFacts(source, shard, split.Dimension, operator, values)
			if err != nil {
				facts.Outcome, facts.Detail = observability.ShardQueriesInvalid, observability.ShardQueryDetail(err.Error())
				return nil, facts
			}
			piece.Queries[ref] = sharded
		}
		built = append(built, piece)
	}
	facts.Built, facts.Outcome = len(built), observability.ShardQueriesBuilt
	return built, facts
}

// Shardability counts a whole catalog by whether a value-list split could be
// expressed for each Plan, which is the question that decides whether value
// lists or hashing is the main road.
//
// Judged without a dimension: a Plan is counted as splittable when its
// queries are structured and conjunctive, which is what a matcher needs
// before any particular dimension is chosen. Whether the chosen dimension is
// grouped by is a per-split question and is answered by ShardQueries.
func Shardability(groups []QueryGroup) observability.ShardabilityFacts {
	facts := observability.ShardabilityFacts{}
	for _, group := range groups {
		for _, plan := range group.Plans {
			facts.Plans++
			facts.Count(shardabilityOf(plan.QueryPlans))
		}
	}
	return facts
}

// shardabilityOf is the worst answer any of a Plan's queries gives: a Plan
// whose pieces have to select the same series in every one of its queries is
// only as splittable as its least splittable query.
func shardabilityOf(queries map[execution.LogicalQueryRef]execution.QueryPlanFacts) string {
	if len(queries) == 0 {
		return observability.ShardQueriesNoQueries
	}
	answer := observability.ShardQueriesBuilt
	for _, facts := range queries {
		switch {
		case facts.PromQL != nil || len(facts.QueryList) == 0:
			return observability.ShardQueriesNotStructured
		default:
			for _, clause := range facts.QueryList {
				for _, connector := range clause.Conditions.Connectors {
					if connector != "and" {
						answer = observability.ShardQueriesDisjunctive
					}
				}
			}
		}
	}
	return answer
}

// queryAdmitsShardMatcher says whether one logical query can carry a matcher
// on this dimension at all.
func queryAdmitsShardMatcher(facts execution.QueryPlanFacts, dimension string) string {
	if facts.PromQL != nil || len(facts.QueryList) == 0 {
		return observability.ShardQueriesNotStructured
	}
	for _, clause := range facts.QueryList {
		for _, connector := range clause.Conditions.Connectors {
			if connector != "and" {
				// See ShardQueriesDisjunctive: the list has no grouping, so a
				// conjunct appended after an "or" does not bind to the whole.
				return observability.ShardQueriesDisjunctive
			}
		}
		grouped := false
		for _, name := range clause.Dimensions {
			if name == dimension {
				grouped = true
				break
			}
		}
		if !grouped {
			return observability.ShardQueriesDimensionNotQueryable
		}
	}
	return observability.ShardQueriesBuilt
}

// shardQueryFacts is one query of one piece: the same query with the piece's
// matcher joined to its conditions, rebuilt so the contract validates it and
// gives it its own revision.
//
// Its own revision matters beyond hygiene: the revision is part of the Query
// Group's identity, so each piece lands in its own group and a re-split of
// one piece is that group's cutover and nothing else's.
func shardQueryFacts(
	source execution.QueryPlanFacts, shard execution.ShardRef, dimension, operator string, values []string,
) (execution.QueryPlanFacts, error) {
	sharded := source
	sharded.QueryRevision = ""
	sharded.Shard = &shard
	sharded.QueryList = make([]execution.QueryClause, len(source.QueryList))
	copy(sharded.QueryList, source.QueryList)
	for index := range sharded.QueryList {
		sharded.QueryList[index].Conditions = withShardCondition(
			sharded.QueryList[index].Conditions, dimension, operator, values)
	}
	return execution.BuildQueryPlanFacts(sharded)
}

// withShardCondition joins the piece's matcher to a clause's conditions with
// "and". Copied rather than appended in place: the source clause is one the
// other pieces are about to be built from, and appending to a shared slice
// would give the second piece the first piece's matcher as well.
func withShardCondition(
	source execution.QueryConditions, dimension, operator string, values []string,
) execution.QueryConditions {
	scalars := make([]execution.QueryScalar, 0, len(values))
	for _, value := range values {
		scalars = append(scalars, execution.QueryScalar{Kind: execution.QueryScalarString, StringValue: value})
	}
	result := execution.QueryConditions{
		Fields:     make([]execution.QueryConditionField, 0, len(source.Fields)+1),
		Connectors: make([]string, 0, len(source.Fields)),
	}
	result.Fields = append(result.Fields, source.Fields...)
	result.Connectors = append(result.Connectors, source.Connectors...)
	if len(result.Fields) > 0 {
		result.Connectors = append(result.Connectors, "and")
	}
	result.Fields = append(result.Fields, execution.QueryConditionField{
		Field: dimension, Operator: operator, Values: scalars})
	return result
}

// shardRefOf names one piece. The digest is taken over what the piece
// actually matches - the dimension, the operator and the values - so two
// pieces of one split differ in it, and a re-split that moves a value from
// one piece to another changes it for both.
func shardRefOf(dimension string, index, count int, operator string, values []string) (execution.ShardRef, error) {
	digest, err := contract.DeriveCanonicalDigestV2(shardMatcherDomain, struct {
		Dimension string   `json:"dimension"`
		Operator  string   `json:"operator"`
		Values    []string `json:"values"`
	}{Dimension: dimension, Operator: operator, Values: values})
	if err != nil {
		return execution.ShardRef{}, err
	}
	shard := execution.ShardRef{Dimension: dimension, Index: index, Count: count, MatcherDigest: digest}
	if err := shard.Validate(); err != nil {
		return execution.ShardRef{}, err
	}
	return shard, nil
}
