// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// What happened when a planned split was turned into queries. The planner
// says a split would be even; this says whether the strategy's own query can
// express one, which is a different question and answered later.
//
// Kept here because both ends read it: the transform in controlplane, and the
// metric labels pre-created from the same list.
const (
	// ShardQueriesBuilt is a split the queries express: every piece carries a
	// condition selecting its values, and one carries the negation of all of
	// them.
	ShardQueriesBuilt = "BUILT"
	// ShardQueriesNotStructured is a strategy querying through PromQL. There
	// is no condition list to add a matcher to, so a value list cannot select
	// a piece's series at all. Splitting one needs the expression rewritten,
	// which is not a thing to do to a user's PromQL.
	ShardQueriesNotStructured = "NOT_STRUCTURED"
	// ShardQueriesDisjunctive is the structural one, and the one that decides
	// how much of a fleet value lists can ever cover.
	//
	// Conditions are a flat list of fields joined by connectors, with no
	// grouping: "A or B" is three slots, not a tree. Appending "and S" to it
	// yields "A or B and S", and every reading of that which binds and more
	// tightly than or means a series matching A lands in EVERY piece - the
	// strategy is evaluated N times and alerts N times. The fallback piece's
	// negation is wrong in the same breath. Nothing in the representation can
	// say "(A or B) and S", so the split is refused rather than expressed
	// wrongly.
	ShardQueriesDisjunctive = "DISJUNCTIVE"
	// ShardQueriesDimensionNotQueryable is a split dimension the query does
	// not group by. The census counts dimensions off the series that came
	// back; a dimension that is not in the clause's own group-by is not one
	// the backend was asked to return per value, and filtering on it would
	// select by something the query does not project.
	ShardQueriesDimensionNotQueryable = "DIMENSION_NOT_QUERYABLE"
	// ShardQueriesTooManyValues is a value list longer than one condition may
	// carry. The piece that matches nothing else has to name every value the
	// others do, so it is the one that reaches this first.
	ShardQueriesTooManyValues = "TOO_MANY_VALUES"
	// ShardQueriesNotPlanned is a split with no dimension or no value lists:
	// the caller handed over something the planner never produced. Neither
	// the strategy's shape nor a contract refusal - nothing was asked for.
	ShardQueriesNotPlanned = "NOT_PLANNED"
	// ShardQueriesNoQueries is a Plan carrying no query facts at all. That
	// is the strategy's shape, and it is the one thing a deployment can
	// legitimately have a standing count of.
	ShardQueriesNoQueries = "NO_QUERIES"
	// ShardQueriesInvalid is a transform that produced facts the query
	// contract refuses, and ONLY that. It is this build's defect rather than
	// the strategy's shape, and it is counted apart for the same reason an
	// unrecognised outcome is: the two send different people to different
	// places.
	//
	// It carried three meanings once - this, a Plan with no queries, and a
	// split nobody planned - and only this one ever has a message to carry,
	// so the other two reached a reader as a bare INVALID saying "the build
	// is broken, go read code". A deployment holding one Plan without
	// queries had a standing count of it.
	ShardQueriesInvalid = "INVALID"
)

// ShardQueryOutcomes is the label set the transform's metric is pre-created
// with, so a zero is a reading rather than a series nobody wrote.
func ShardQueryOutcomes() []string {
	return []string{
		ShardQueriesBuilt, ShardQueriesNotStructured, ShardQueriesDisjunctive,
		ShardQueriesDimensionNotQueryable, ShardQueriesTooManyValues,
		ShardQueriesNotPlanned, ShardQueriesNoQueries, ShardQueriesInvalid,
	}
}

// ShardabilityFacts is the whole catalog counted by whether a value-list
// split could be expressed for each Plan at all, taken once per publication.
//
// It exists to answer one design question with a number instead of an
// opinion: value lists cannot express a split for a strategy whose own
// conditions contain an "or", and if that is a large share of a real fleet
// then hashing is the main road and value lists are the special case, not
// the other way round. The count is over every Plan in the catalog rather
// than over the ones a split was planned for, because the question is about
// the population, not about today's heaviest objects.
//
// Counted where the catalog is published, so it costs one pass over
// something already in hand and cannot drift from what the fleet runs.
type ShardabilityFacts struct {
	// Plans is the denominator: every Plan the publication carries.
	Plans int `json:"plans"`
	// Splittable is the Plans a value-list matcher could be added to.
	Splittable int `json:"splittable"`
	// Disjunctive is the Plans whose own conditions contain a connector
	// other than "and". These cannot be split by a value list at all -
	// appending a conjunct to a flat, ungrouped condition list changes what
	// the existing conditions mean.
	Disjunctive int `json:"disjunctive"`
	// NotStructured is the Plans querying through PromQL, which have no
	// condition list to add anything to.
	NotStructured int `json:"not_structured"`
	// NoQueries is a Plan carrying no query facts at all, counted rather
	// than folded into either answer.
	NoQueries int `json:"no_queries"`
	// Unrecognised is a Plan this build could not classify - an answer
	// outside the four above. Its own cell rather than folded into any of
	// them: folded, a word added on one side of this census and not the
	// other would be counted as whatever the fold chose, and under-reported
	// with no line to look at. The five sum to Plans.
	Unrecognised int `json:"unrecognised"`
}

// ShardabilityCell is one cell of the catalog's shardability census: which
// answer, and how many Plans gave it.
type ShardabilityCell struct {
	Answer string
	Plans  int
}

// Cells is the census as its five cells, in a fixed order. The metric is
// pre-created from the answers of an empty census and written from the
// answers of a real one, so a cell cannot be added to one and missing from
// the other.
func (facts ShardabilityFacts) Cells() []ShardabilityCell {
	return []ShardabilityCell{
		{Answer: "splittable", Plans: facts.Splittable},
		{Answer: "disjunctive", Plans: facts.Disjunctive},
		{Answer: "not_structured", Plans: facts.NotStructured},
		{Answer: "no_queries", Plans: facts.NoQueries},
		{Answer: "unrecognised", Plans: facts.Unrecognised},
	}
}

// Count files one Plan's answer in the cell it belongs to.
//
// A step of its own so the cell for an answer this build does not know can
// be reached by a test. It cannot be reached through the census itself:
// every word the classifier can return is named in this switch, so the
// default is unreachable by construction today - and the sum of the cells
// is the same whichever cell an answer is folded into, so the identity over
// them is blind to the fold as well.
//
// The cell exists precisely because someone will add a word later, and the
// day it first matters is the day it first becomes reachable. Marked
// unreachable instead, nothing would remind that person to look here.
//
// The fold that was there before counted an unclassifiable Plan as
// splittable, which is the optimistic direction: a planner would then go and
// cut something nothing could classify.
func (facts *ShardabilityFacts) Count(answer string) {
	switch answer {
	case ShardQueriesBuilt:
		facts.Splittable++
	case ShardQueriesNotStructured:
		facts.NotStructured++
	case ShardQueriesDisjunctive:
		facts.Disjunctive++
	case ShardQueriesNoQueries:
		facts.NoQueries++
	default:
		facts.Unrecognised++
	}
}

// ShardQueryFacts is one attempt to express a planned split as queries.
type ShardQueryFacts struct {
	Outcome    string `json:"outcome"`
	StrategyID string `json:"strategy_id,omitempty"`
	BusinessID string `json:"business_id,omitempty"`
	Dimension  string `json:"dimension,omitempty"`
	// Shards is how many pieces were asked for and Built how many the queries
	// came out for. They differ only on a refusal, and the pair is what says
	// whether a refusal stopped the whole split or one piece of it - there is
	// no such thing as half a split, so any difference is the whole thing
	// refused.
	Shards int `json:"shards"`
	Built  int `json:"built"`
	// Values is how many values the pieces name between them, and
	// FallbackValues how many the piece that matches nothing else has to
	// negate. The second is the one that meets a bound first.
	Values         int `json:"values"`
	FallbackValues int `json:"fallback_values"`
	// Queries is how many of the Plan's logical queries the matcher was added
	// to. A Plan with several is one where every one of them has to select
	// the same series, or the pieces do not partition anything.
	Queries int `json:"queries"`
	// Detail is what the query contract said, for the one outcome that is
	// this build's defect rather than the strategy's shape. A refusal a
	// reader cannot act on is the same as no refusal: INVALID on its own
	// names no file, no field and no rule. Bounded, because it is an error
	// string from a layer below.
	Detail string `json:"detail,omitempty"`
}

// MaxShardQueryDetail bounds the carried error text.
const MaxShardQueryDetail = 256

// ShardQueryDetail trims a message to what a line may carry.
func ShardQueryDetail(message string) string {
	if len(message) > MaxShardQueryDetail {
		return message[:MaxShardQueryDetail]
	}
	return message
}
