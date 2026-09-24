// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// shardQueryFacts is one logical query that groups by the dimension a split
// would be cut on, with whatever conditions the strategy came with.
func shardQueryFixture(t *testing.T, conditions execution.QueryConditions) execution.QueryPlanFacts {
	t.Helper()
	facts, err := execution.BuildQueryPlanFacts(execution.QueryPlanFacts{
		Provider: execution.ProviderUQ, ProviderRouteRef: "uq-primary", TenantID: "system",
		BusinessID: "2", SpaceScope: "bkcc__2",
		QueryList: []execution.QueryClause{{
			DataSource: "bk_monitor", Driver: "influxdb", TableID: "system.cpu", FieldName: "usage",
			TimeField: "time", ReferenceName: "a", Dimensions: []string{"ip", "module"},
			Conditions:      conditions,
			Functions:       []execution.QueryFunction{{Method: "default", Position: 0}},
			TimeAggregation: execution.QueryFunction{Method: "avg", Position: 0},
		}},
		MetricMerge: "a", StepMillis: 60000, AlignmentMillis: 60000,
		Normalization: execution.DatasetNormalizationSpec{
			DatasetContract: contract.DatasetContractV2{SchemaDigest: "schema", NormalizationDigest: "normalization",
				IdentityFields: []string{"ip"}, SourceTimeField: "time", ReceivedTimeField: "received_time"},
			SourceTimeUnit: execution.TimeUnitMillisecond, CanonicalSourceTimeUnit: execution.TimeUnitSecond,
			SeriesIdentityMode: execution.SeriesIdentityUQGroupKeysValuesV1, GroupKeyRule: execution.GroupKeyStripTableSuffixV1,
			ValueSelectionMode: execution.ValueSelectionResultOrFirstReferenceV1, CanonicalValueField: "value",
			ReceivedTimeMode: execution.ReceivedTimeProviderReceivedAt, Version: "v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func shardQueries(t *testing.T, conditions execution.QueryConditions) map[execution.LogicalQueryRef]execution.QueryPlanFacts {
	t.Helper()
	return map[execution.LogicalQueryRef]execution.QueryPlanFacts{"q-a": shardQueryFixture(t, conditions)}
}

func shardPlanIdentity() execution.PlanIdentity {
	return execution.PlanIdentity{TenantID: "system", BusinessID: "2", StrategyID: "4101"}
}

func threeWaySplit() controlplane.SplitPlan {
	return controlplane.SplitPlan{Plan: shardPlanIdentity(), Dimension: "ip",
		Lists: [][]string{{"192.0.2.1", "192.0.2.2"}, {"192.0.2.3"}}}
}

func shardCondition(t *testing.T, facts execution.QueryPlanFacts) execution.QueryConditionField {
	t.Helper()
	fields := facts.QueryList[0].Conditions.Fields
	if len(fields) == 0 {
		t.Fatal("the sharded query carries no condition at all")
	}
	return fields[len(fields)-1]
}

// The pieces cover every series and no series twice.
//
// That is the whole of what a split has to be. A value in two lists is a
// strategy that alerts twice; a value in none is a strategy nobody
// evaluates, and neither shows up as an error anywhere - the first reads as
// a flapping alert and the second as a quiet one.
func TestThePiecesNameEveryValueOnceAndTheLastOneNegatesThemAll(t *testing.T) {
	split := threeWaySplit()
	pieces, facts := controlplane.ShardQueries(shardPlanIdentity(), shardQueries(t, execution.QueryConditions{}), split)

	if facts.Outcome != observability.ShardQueriesBuilt {
		t.Fatalf("outcome = %q, want the queries built; facts = %+v", facts.Outcome, facts)
	}
	if len(pieces) != 3 {
		t.Fatalf("%d pieces for two value lists, want three: the last one matches everything the others do not",
			len(pieces))
	}
	seen := map[string]int{}
	for index, piece := range pieces[:2] {
		condition := shardCondition(t, piece.Queries["q-a"])
		if condition.Field != "ip" || condition.Operator != "contains" {
			t.Fatalf("piece %d selects with %q %q, want an equality on the split dimension",
				index, condition.Field, condition.Operator)
		}
		for _, value := range condition.Values {
			seen[value.StringValue]++
		}
	}
	for _, value := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		if seen[value] != 1 {
			t.Fatalf("value %q is named by %d pieces, want exactly one", value, seen[value])
		}
	}
	// The last piece is the cover: it has to exclude every value the others
	// take, or a series appears twice; and it must exclude nothing else, or
	// a value that appears after the census is evaluated by nobody.
	last := shardCondition(t, pieces[2].Queries["q-a"])
	if last.Operator != "ncontains" {
		t.Fatalf("the last piece selects with %q, want the negation", last.Operator)
	}
	if len(last.Values) != 3 {
		t.Fatalf("the last piece negates %d values, want all three the others name", len(last.Values))
	}
	if facts.FallbackValues != 3 || facts.Values != 3 {
		t.Fatalf("facts report %d values and %d negated, want three of each", facts.Values, facts.FallbackValues)
	}
}

// Each piece is its own Query Group, which is what makes a re-split of one
// piece that piece's cutover and nothing else's.
func TestEachPieceCarriesItsOwnShardAndItsOwnQueryRevision(t *testing.T) {
	pieces, _ := controlplane.ShardQueries(shardPlanIdentity(), shardQueries(t, execution.QueryConditions{}), threeWaySplit())

	revisions := map[execution.QueryRevision]int{}
	digests := map[string]int{}
	for index, piece := range pieces {
		if piece.Shard.Dimension != "ip" || piece.Shard.Index != index || piece.Shard.Count != 3 {
			t.Fatalf("piece %d carries %+v, want its index out of three on the split dimension", index, piece.Shard)
		}
		if piece.Shard.MatcherDigest == "" {
			t.Fatalf("piece %d carries no matcher digest: the per-Plan record keys take it, and without one a "+
				"re-split would keep the gap marker and no-data memory of series it no longer holds", index)
		}
		digests[piece.Shard.MatcherDigest]++
		revisions[piece.Queries["q-a"].QueryRevision]++
	}
	for digest, count := range digests {
		if count != 1 {
			t.Fatalf("matcher digest %q is carried by %d pieces, want one each", digest, count)
		}
	}
	for revision, count := range revisions {
		if count != 1 {
			t.Fatalf("query revision %q is carried by %d pieces, want one each - each piece is its own "+
				"Query Group", revision, count)
		}
	}
}

// A strategy's own conditions survive, and the matcher is joined to them
// rather than replacing them.
func TestThePiecesKeepTheStrategysOwnConditions(t *testing.T) {
	own := execution.QueryConditions{Fields: []execution.QueryConditionField{{
		Field: "env", Operator: "contains",
		Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "prod"}}}}}

	pieces, facts := controlplane.ShardQueries(shardPlanIdentity(), shardQueries(t, own), threeWaySplit())

	if facts.Outcome != observability.ShardQueriesBuilt {
		t.Fatalf("outcome = %q, want the queries built", facts.Outcome)
	}
	for index, piece := range pieces {
		fields := piece.Queries["q-a"].QueryList[0].Conditions
		if len(fields.Fields) != 2 || fields.Fields[0].Field != "env" {
			t.Fatalf("piece %d has conditions %+v, want the strategy's own kept first", index, fields.Fields)
		}
		if len(fields.Connectors) != 1 || fields.Connectors[0] != "and" {
			t.Fatalf("piece %d joins its matcher with %v, want an and", index, fields.Connectors)
		}
	}
	// And one piece's matcher never reaches another's: the source clause is
	// shared, so appending in place would give the second piece the first
	// piece's values too.
	first := shardCondition(t, pieces[0].Queries["q-a"])
	second := shardCondition(t, pieces[1].Queries["q-a"])
	if len(first.Values) != 2 || len(second.Values) != 1 {
		t.Fatalf("pieces carry %d and %d values, want two and one: a matcher appended to a shared slice "+
			"lands in every piece built after it", len(first.Values), len(second.Values))
	}
}

// A strategy whose conditions contain an "or" is refused, and this is the
// refusal that decides how much of a fleet value lists can cover.
//
// The condition list is flat - "A or B" is three slots with no grouping - so
// appending "and S" yields "A or B and S". Every reading of that which binds
// and more tightly than or puts a series matching A in EVERY piece: the
// strategy is evaluated N times and alerts N times, and the last piece's
// negation is wrong in the same breath. Nothing in the representation can
// say "(A or B) and S".
func TestAStrategyWithADisjunctiveConditionIsRefusedRatherThanSplitWrongly(t *testing.T) {
	disjunctive := execution.QueryConditions{
		Fields: []execution.QueryConditionField{
			{Field: "env", Operator: "contains",
				Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "prod"}}},
			{Field: "env", Operator: "contains",
				Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "staging"}}},
		},
		Connectors: []string{"or"},
	}

	pieces, facts := controlplane.ShardQueries(shardPlanIdentity(), shardQueries(t, disjunctive), threeWaySplit())

	if facts.Outcome != observability.ShardQueriesDisjunctive {
		t.Fatalf("outcome = %q, want %q", facts.Outcome, observability.ShardQueriesDisjunctive)
	}
	if len(pieces) != 0 || facts.Built != 0 {
		t.Fatalf("%d pieces built for a refused split: there is no such thing as half a split, and a piece "+
			"built beside a refusal is a piece nothing else covers", len(pieces))
	}
}

// A strategy queried through PromQL has no condition list to add a matcher
// to, so a value list cannot select a piece's series at all.
func TestAPromQLStrategyIsRefusedBecauseThereIsNoConditionToAddTo(t *testing.T) {
	// Built by hand rather than through the contract: what is under test is
	// the shape the transform refuses, and a PromQL Plan's own validity is
	// the contract's business and not this refusal's.
	promql := shardQueryFixture(t, execution.QueryConditions{})
	promql.QueryList = nil
	promql.MetricMerge = ""
	promql.PromQL = &execution.PromQLQuery{Expression: "sum(rate(x[1m])) by (ip)"}

	_, facts := controlplane.ShardQueries(shardPlanIdentity(),
		map[execution.LogicalQueryRef]execution.QueryPlanFacts{"q-a": promql}, threeWaySplit())

	if facts.Outcome != observability.ShardQueriesNotStructured {
		t.Fatalf("outcome = %q, want %q", facts.Outcome, observability.ShardQueriesNotStructured)
	}
}

// A dimension the query does not group by is not one the backend was asked
// to return per value, so filtering on it would select by something the
// query does not project.
func TestADimensionTheQueryDoesNotGroupByIsRefused(t *testing.T) {
	split := threeWaySplit()
	split.Dimension = "datacentre"

	_, facts := controlplane.ShardQueries(shardPlanIdentity(), shardQueries(t, execution.QueryConditions{}), split)

	if facts.Outcome != observability.ShardQueriesDimensionNotQueryable {
		t.Fatalf("outcome = %q, want %q", facts.Outcome, observability.ShardQueriesDimensionNotQueryable)
	}
}

// The piece that matches nothing else names every value the others do, so it
// is the one that meets the bound first - and past it the whole split is
// refused rather than one piece being cut short.
func TestASplitWhoseNegationIsTooLongIsRefusedWhole(t *testing.T) {
	split := threeWaySplit()
	split.Lists = [][]string{make([]string, 0, controlplane.MaxShardConditionValues+1)}
	for index := 0; index <= controlplane.MaxShardConditionValues; index++ {
		split.Lists[0] = append(split.Lists[0], fmt.Sprintf("ip-%05d", index))
	}

	pieces, facts := controlplane.ShardQueries(shardPlanIdentity(), shardQueries(t, execution.QueryConditions{}), split)

	if facts.Outcome != observability.ShardQueriesTooManyValues {
		t.Fatalf("outcome = %q, want %q", facts.Outcome, observability.ShardQueriesTooManyValues)
	}
	if len(pieces) != 0 {
		t.Fatalf("%d pieces built past the bound, want none", len(pieces))
	}
}

// Two leaders building one split build the same matchers, so the pieces have
// the same identity on both - a digest that differed would make a re-split
// read as a cutover on a leader handover.
func TestTwoLeadersBuildingOneSplitReachTheSameMatchers(t *testing.T) {
	first, _ := controlplane.ShardQueries(shardPlanIdentity(), shardQueries(t, execution.QueryConditions{}), threeWaySplit())
	for round := 0; round < 5; round++ {
		next, _ := controlplane.ShardQueries(shardPlanIdentity(), shardQueries(t, execution.QueryConditions{}), threeWaySplit())
		for index := range first {
			if first[index].Shard != next[index].Shard {
				t.Fatalf("piece %d differs between builds:\n%+v\n%+v", index, first[index].Shard, next[index].Shard)
			}
			if first[index].Queries["q-a"].QueryRevision != next[index].Queries["q-a"].QueryRevision {
				t.Fatalf("piece %d's query revision differs between builds", index)
			}
		}
	}
}

// The catalog counts how much of itself a value-list split could be
// expressed for, with the denominator beside it.
//
// This is the number that decides a design, not a deployment: if strategies
// whose own conditions contain an "or" are a large share of a real fleet,
// then hashing is the main road and value lists are the special case. A
// count without the total says nothing, so the total is counted too - the
// same reason the round's skipped count carries its own denominator.
func TestTheCatalogCountsHowMuchOfItselfAValueListCouldSplit(t *testing.T) {
	conjunctive := execution.QueryConditions{
		Fields: []execution.QueryConditionField{
			{Field: "env", Operator: "contains",
				Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "prod"}}},
			{Field: "idc", Operator: "contains",
				Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "a"}}},
		},
		Connectors: []string{"and"},
	}
	disjunctive := conjunctive
	disjunctive.Connectors = []string{"or"}
	promql := shardQueryFixture(t, execution.QueryConditions{})
	promql.QueryList = nil
	promql.PromQL = &execution.PromQLQuery{Expression: "sum(x) by (ip)"}

	plan := func(queries map[execution.LogicalQueryRef]execution.QueryPlanFacts) controlplane.FrozenPlan {
		return controlplane.FrozenPlan{Identity: shardPlanIdentity(), QueryPlans: queries}
	}
	groups := []controlplane.QueryGroup{{Plans: []controlplane.FrozenPlan{
		plan(shardQueries(t, conjunctive)),
		plan(shardQueries(t, execution.QueryConditions{})),
		plan(shardQueries(t, disjunctive)),
		plan(map[execution.LogicalQueryRef]execution.QueryPlanFacts{"q-a": promql}),
		plan(nil),
	}}}

	facts := controlplane.Shardability(groups)

	if facts.Plans != 5 {
		t.Fatalf("counted %d Plans, want the denominator: a count of what cannot be split says nothing "+
			"without how many there are", facts.Plans)
	}
	if facts.Splittable != 2 || facts.Disjunctive != 1 || facts.NotStructured != 1 || facts.NoQueries != 1 {
		t.Fatalf("census = %+v, want two splittable, one disjunctive, one PromQL and one without queries", facts)
	}
	if facts.Splittable+facts.Disjunctive+facts.NotStructured+facts.NoQueries+facts.Unrecognised != facts.Plans {
		t.Fatalf("the five counts sum to %d against %d Plans: every Plan lands in exactly one",
			facts.Splittable+facts.Disjunctive+facts.NotStructured+facts.NoQueries+facts.Unrecognised, facts.Plans)
	}
}

// A Plan is only as splittable as its least splittable query: its pieces
// have to select the same series in every one of them, or the pieces do not
// partition anything.
func TestAPlanIsOnlyAsSplittableAsItsLeastSplittableQuery(t *testing.T) {
	disjunctive := execution.QueryConditions{
		Fields: []execution.QueryConditionField{
			{Field: "env", Operator: "contains",
				Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "prod"}}},
			{Field: "env", Operator: "contains",
				Values: []execution.QueryScalar{{Kind: execution.QueryScalarString, StringValue: "staging"}}},
		},
		Connectors: []string{"or"},
	}
	mixed := map[execution.LogicalQueryRef]execution.QueryPlanFacts{
		"q-a": shardQueryFixture(t, execution.QueryConditions{}),
		"q-b": shardQueryFixture(t, disjunctive),
	}

	facts := controlplane.Shardability([]controlplane.QueryGroup{{
		Plans: []controlplane.FrozenPlan{{Identity: shardPlanIdentity(), QueryPlans: mixed}}}})

	if facts.Disjunctive != 1 || facts.Splittable != 0 {
		t.Fatalf("census = %+v, want the Plan counted as unsplittable: one conjunctive query does not make "+
			"a Plan splittable when another of its queries is not", facts)
	}
}

// The one refusal that is this build's defect carries what the contract
// actually said.
//
// The change that added it was made because the first thing INVALID ever did
// was stop me with nothing to act on - and nothing was guarding it, so
// replacing the message with an empty string left every test green. A
// refusal a reader cannot act on is the same as no refusal, and the
// assertion has to be on the message being the contract's own, not merely
// on its being non-empty: "error" is non-empty too.
func TestAContractRefusalCarriesWhatTheContractSaid(t *testing.T) {
	// Query facts this transform's own checks admit - structured, all "and",
	// grouping by the split dimension - and the query contract refuses when
	// they are rebuilt. That is exactly the shape INVALID is for: the
	// transform produced something the layer below will not take.
	refused := shardQueryFixture(t, execution.QueryConditions{})
	refused.MetricMerge = ""

	pieces, facts := controlplane.ShardQueries(shardPlanIdentity(),
		map[execution.LogicalQueryRef]execution.QueryPlanFacts{"q-a": refused}, threeWaySplit())

	if facts.Outcome != observability.ShardQueriesInvalid {
		t.Fatalf("outcome = %q, want %q; facts = %+v", facts.Outcome, observability.ShardQueriesInvalid, facts)
	}
	if len(pieces) != 0 {
		t.Fatalf("%d pieces built past a contract refusal, want none", len(pieces))
	}
	if !strings.Contains(facts.Detail, "structured query facts") {
		t.Fatalf("detail = %q, want the contract's own words: a reader met by a bare INVALID has no field, "+
			"no rule and nothing to do - which is what this refusal did to me the first time it fired",
			facts.Detail)
	}
}

// The three things that are not this build's defect say so under their own
// words, and carry no message because there is none to carry.
//
// They shared INVALID once. A deployment holding one Plan without queries
// then had a standing count of "the build is broken", and a caller handing
// over an unplanned split got the same word as a contract violation.
func TestTheRefusalsThatAreNotDefectsHaveTheirOwnWords(t *testing.T) {
	for name, testCase := range map[string]struct {
		queries map[execution.LogicalQueryRef]execution.QueryPlanFacts
		split   controlplane.SplitPlan
		want    string
	}{
		"a Plan with no queries at all": {
			queries: map[execution.LogicalQueryRef]execution.QueryPlanFacts{},
			split:   threeWaySplit(), want: observability.ShardQueriesNoQueries},
		"a split with no dimension": {
			queries: nil, split: controlplane.SplitPlan{Plan: shardPlanIdentity(), Lists: [][]string{{"a"}}},
			want: observability.ShardQueriesNotPlanned},
		"a split with no value lists": {
			queries: nil, split: controlplane.SplitPlan{Plan: shardPlanIdentity(), Dimension: "ip"},
			want: observability.ShardQueriesNotPlanned},
	} {
		t.Run(name, func(t *testing.T) {
			queries := testCase.queries
			if queries == nil {
				queries = shardQueries(t, execution.QueryConditions{})
			}
			_, facts := controlplane.ShardQueries(shardPlanIdentity(), queries, testCase.split)
			if facts.Outcome != testCase.want {
				t.Fatalf("outcome = %q, want %q", facts.Outcome, testCase.want)
			}
			if facts.Detail != "" {
				t.Fatalf("detail = %q on a refusal that is not a contract violation: the message exists "+
					"for the one word that has one", facts.Detail)
			}
		})
	}
}

// A Plan this census cannot classify lands in its own cell, and the cells
// still sum to the denominator.
//
// The word shardabilityOf returns for "no queries" and the word the
// transform returns for a contract violation used to be the same one, joined
// by a silent rename in this switch. The natural next change - making
// shardabilityOf say INVALID for a query that really is invalid - would then
// have been counted as a Plan with no queries: under-reported, with no line
// to look at.
func TestAPlanThisCensusCannotClassifyHasItsOwnCell(t *testing.T) {
	facts := controlplane.Shardability([]controlplane.QueryGroup{{Plans: []controlplane.FrozenPlan{
		{Identity: shardPlanIdentity(), QueryPlans: shardQueries(t, execution.QueryConditions{})},
		{Identity: shardPlanIdentity(), QueryPlans: nil},
	}}})

	if facts.Plans != 2 || facts.Splittable != 1 || facts.NoQueries != 1 {
		t.Fatalf("census = %+v, want one splittable and one without queries", facts)
	}
	if facts.Unrecognised != 0 {
		t.Fatalf("census = %+v, want nothing unclassified for words this build knows", facts)
	}
	if facts.Splittable+facts.Disjunctive+facts.NotStructured+facts.NoQueries+facts.Unrecognised != facts.Plans {
		t.Fatalf("the five cells sum to %d against %d Plans: every Plan lands in exactly one",
			facts.Splittable+facts.Disjunctive+facts.NotStructured+facts.NoQueries+facts.Unrecognised, facts.Plans)
	}
}
