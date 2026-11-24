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
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// expressionUnitCase is one strategy document shape and what its published
// Plan must say about the unit its values are compared in.
type expressionUnitCase struct {
	name string
	// queries is the number of query configs, each with unit memberUnit.
	queries    int
	memberUnit string
	// expressionUnit is the item's expression_unit; nil leaves it absent.
	expressionUnit *string
	prefix         string
	// wantUnit and wantPrefix are what the threshold algorithm compiles with;
	// wantBasis is the Plan's DataUnitFacts basis, "" for none (the plain
	// query-unit Plan); wantNote is the Level note, "" for none.
	wantUnit, wantPrefix, wantBasis, wantNote string
}

func unitOf(word string) *string { return &word }

// The unit a Plan compares in is the expression's declared unit when there is
// one, and otherwise what Python's detection uses: the one query's unit, or
// none for a value computed over several metrics. A unit or prefix that cannot
// be read compares raw values and is named, never refused.
//
// Every row has a partner that differs in the one fact the rule turns on, so a
// change that read the unit some other way fails a row in either direction.
func TestThePlanComparesInTheExpressionsUnitOrAsPythonDoes(t *testing.T) {
	for _, test := range []expressionUnitCase{
		// A strategy's first revision as the platform stores it without a
		// declaration: two queries whose members say ms, threshold 20 with no
		// prefix. Raw values: 20 means 20, as Python compares it -- not 20 ns,
		// which the first member's unit made it.
		{name: "two queries, member unit ms, no declaration", queries: 2, memberUnit: "ms",
			wantUnit: "", wantPrefix: "", wantBasis: controlplane.DataUnitBasisMultiQueryRaw},
		// Its partner: one query, same unit -- the query's unit, an empty
		// prefix in the smallest step, as Python reads it.
		{name: "one query, unit ms, no declaration", queries: 1, memberUnit: "ms",
			wantUnit: "ms", wantPrefix: ""},
		// The same two queries declaring ms, with the prefix the writer fills
		// in for a computed metric (the unit itself): only the declared unit
		// is used, so 20 means 20 ms.
		{name: "two queries declaring ms, prefix ms", queries: 2, memberUnit: "ms", expressionUnit: unitOf("ms"), prefix: "ms",
			wantUnit: "ms", wantPrefix: "ms", wantBasis: controlplane.DataUnitBasisExpressionUnit},
		// An empty prefix is read as written, declared or not: the unit's
		// smallest step. Nothing fills it in, so a strategy saved again
		// unchanged keeps its threshold.
		{name: "two queries declaring ms, empty prefix", queries: 2, memberUnit: "ms", expressionUnit: unitOf("ms"),
			wantUnit: "ms", wantPrefix: "", wantBasis: controlplane.DataUnitBasisExpressionUnit},
		// A unit whose steps include the empty one: the empty prefix is that
		// step (B), its partner the step named.
		{name: "one query declaring kbytes, empty prefix", queries: 1, memberUnit: "kbytes", expressionUnit: unitOf("kbytes"),
			wantUnit: "kbytes", wantPrefix: "", wantBasis: controlplane.DataUnitBasisExpressionUnit},
		{name: "one query declaring kbytes, prefix Ki", queries: 1, memberUnit: "kbytes", expressionUnit: unitOf("kbytes"), prefix: "Ki",
			wantUnit: "kbytes", wantPrefix: "Ki", wantBasis: controlplane.DataUnitBasisExpressionUnit},
		// Declared and prefixed in another step of the same unit: the prefix
		// stands.
		{name: "two queries declaring ms, prefix s", queries: 2, memberUnit: "ms", expressionUnit: unitOf("ms"), prefix: "s",
			wantUnit: "ms", wantPrefix: "s", wantBasis: controlplane.DataUnitBasisExpressionUnit},
		// A declaration is the only source: members that say percent do not
		// lend their unit to an expression declared in ms.
		{name: "members say percent, expression declares ms", queries: 2, memberUnit: "percent", expressionUnit: unitOf("ms"), prefix: "ms",
			wantUnit: "ms", wantPrefix: "ms", wantBasis: controlplane.DataUnitBasisExpressionUnit},
		// A declared empty unit says the expression has none, even over one
		// query whose unit is ms: raw values.
		{name: "one query, unit ms, declared empty", queries: 1, memberUnit: "ms", expressionUnit: unitOf(""),
			wantUnit: "", wantPrefix: "", wantBasis: controlplane.DataUnitBasisExpressionUnit},
		// A prefix on values without a unit scales nothing and is named.
		{name: "two queries, no declaration, prefix ms", queries: 2, memberUnit: "ms", prefix: "ms",
			wantUnit: "", wantPrefix: "", wantBasis: controlplane.DataUnitBasisMultiQueryRaw,
			wantNote: controlplane.ReasonUnitPrefixIgnoredNoUnit},
		// A unit the table does not define compares raw and is named; the
		// level is not taken out of service.
		{name: "one query, unknown unit", queries: 1, memberUnit: "furlongs",
			wantUnit: "", wantPrefix: "", wantBasis: controlplane.DataUnitBasisQueryUnit,
			wantNote: controlplane.ReasonDataUnitUnknownComparedRaw},
		// A prefix from another family is read as not given -- the threshold
		// in the unit's smallest step, as Python reads a suffix it cannot
		// find -- and is named.
		{name: "one query, unit ms, prefix Gi", queries: 1, memberUnit: "ms", prefix: "Gi",
			wantUnit: "ms", wantPrefix: "", wantBasis: controlplane.DataUnitBasisQueryUnit,
			wantNote: controlplane.ReasonUnitPrefixOutsideStepsReadAsSmallest},
		// A platform step the table leaves out (past an int64) is outside the
		// steps the same way.
		{name: "one query, unit bytes, prefix Yi", queries: 1, memberUnit: "bytes", prefix: "Yi",
			wantUnit: "bytes", wantPrefix: "", wantBasis: controlplane.DataUnitBasisQueryUnit,
			wantNote: controlplane.ReasonUnitPrefixOutsideStepsReadAsSmallest},
		// Its partner: a prefix in the steps is converted as written.
		{name: "one query, unit ms, prefix s", queries: 1, memberUnit: "ms", prefix: "s",
			wantUnit: "ms", wantPrefix: "s"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository, plan, facts := publishExpressionUnitDocument(t, test)
			unit, prefix := thresholdUnitOf(t, plan)
			if unit != test.wantUnit || prefix != test.wantPrefix {
				t.Fatalf("the threshold compiles with data unit %q and prefix %q, want %q and %q", unit, prefix, test.wantUnit, test.wantPrefix)
			}
			if got := plan.Plan.InputProjection.DataUnit; got != test.wantUnit {
				t.Fatalf("the Plan projects data unit %q, want %q: the projection and its algorithms must agree", got, test.wantUnit)
			}
			basis := ""
			if facts != nil {
				basis = facts.Basis
			}
			// A plain query-unit Plan with nothing to say carries no facts;
			// any other Plan says how it compares.
			if test.wantNote == "" && test.wantBasis == "" && facts != nil {
				t.Fatalf("a plain query-unit Plan carries unit facts %+v", facts)
			}
			if test.wantBasis != "" && basis != test.wantBasis {
				t.Fatalf("unit facts basis %q, want %q (facts %+v)", basis, test.wantBasis, facts)
			}
			assertAuditDispositionExact(t, repository, "1001", "PLAN", 0, controlplane.DispositionAccepted, "")
			assertNoAuditDisposition(t, repository, "1001", controlplane.DispositionConfigRejected)
			if test.wantNote == "" {
				assertNoAuditDisposition(t, repository, "1001", controlplane.DispositionConfigNoted)
				return
			}
			assertAuditDispositionExact(t, repository, "1001", "LEVEL", 1, controlplane.DispositionConfigNoted, test.wantNote)
			if len(facts.RawLevels) != 1 || facts.RawLevels[0].Reason != test.wantNote {
				t.Fatalf("unit facts raw levels %+v, want one for %s", facts.RawLevels, test.wantNote)
			}
		})
	}
}

// publishExpressionUnitDocument publishes the real threshold document shaped
// as test says, through the Redis source and the reconciler, and returns the
// repository and the strategy's published Plan.
// publishExpressionUnitDocument publishes the case's strategy and returns
// the Plan as the published objects hold it, and the unit facts the
// Leader's lookup answers for it - where a reader (strategy.view) reads
// them; the objects a Worker runs do not carry them.
func publishExpressionUnitDocument(t *testing.T, test expressionUnitCase) (*controlplane.RedisCatalogRepository, controlplane.FrozenPlan, *controlplane.DataUnitFacts) {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(realThresholdDocuments(t)[0], &value); err != nil {
		t.Fatal(err)
	}
	item := value["items"].([]any)[0].(map[string]any)
	first := item["query_configs"].([]any)[0].(map[string]any)
	first["unit"] = test.memberUnit
	queries := []any{first}
	if test.queries == 2 {
		second := map[string]any{}
		for key, field := range first {
			second[key] = field
		}
		second["alias"], second["metric_field"] = "b", "count"
		queries = append(queries, second)
		item["expression"] = "a / b"
	}
	item["query_configs"] = queries
	if test.expressionUnit != nil {
		item["expression_unit"] = *test.expressionUnit
	}
	for _, raw := range item["algorithms"].([]any) {
		raw.(map[string]any)["unit_prefix"] = test.prefix
	}
	document, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	client := newControlplaneRedis(t)
	ctx := context.Background()
	if err := client.Set(ctx, "bkmonitor.cache.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "bkmonitor.cache.strategy_1001", string(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	planner, err := controlplane.NewLegacyPrimaryQueryCompiler("uq-primary-v1", "UTC", testLegacyQueryRuntimeFacts())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:expression-unit", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	compiler, stateSemantics := runtimePlanCompiler(t)
	reconciler, err := controlplane.NewSourceReconciler(repository, compiler, stateSemantics)
	if err != nil {
		t.Fatal(err)
	}
	published, err := reconciler.Refresh(ctx, newRedisStrategySource(t, client), planner)
	if err != nil || published.Status != controlplane.SourceRefreshPublished {
		t.Fatalf("publish = (%#v, %v)", published, err)
	}
	snapshot, err := loadPublishedSnapshot(ctx, repository, published.Publication)
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := plansByStrategy(snapshot)["1001"]
	if !ok {
		t.Fatalf("strategy 1001 was not published: %+v", snapshot)
	}
	if plan.DataUnitFacts != nil {
		t.Fatalf("the published object carries unit facts %+v: a Worker would decode and cache them", plan.DataUnitFacts)
	}
	lookup := reconciler.LookupStrategy("1001")
	if len(lookup.Plans) != 1 {
		t.Fatalf("the lookup lists %d Plans of strategy 1001, want one", len(lookup.Plans))
	}
	return repository, plan, lookup.Plans[0].DataUnitFacts
}

// thresholdUnitOf is the data unit and prefix the Plan's first threshold
// algorithm compiles with, read from its config.
func thresholdUnitOf(t *testing.T, plan controlplane.FrozenPlan) (string, string) {
	t.Helper()
	for _, level := range plan.Plan.StrategyIR.Levels {
		for _, algorithm := range level.DetectPlan.Algorithms {
			if algorithm.Type != "Threshold" {
				continue
			}
			var config struct {
				DataUnit            string `json:"data_unit"`
				ThresholdUnitPrefix string `json:"threshold_unit_prefix"`
			}
			if err := json.Unmarshal(algorithm.Config, &config); err != nil {
				t.Fatal(err)
			}
			return config.DataUnit, config.ThresholdUnitPrefix
		}
	}
	t.Fatal("the Plan has no threshold algorithm")
	return "", ""
}
