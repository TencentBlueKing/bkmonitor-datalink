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
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func namedOutputsTestCompiler(t *testing.T) *LegacyPrimaryQueryCompiler {
	t.Helper()
	compiler, err := NewLegacyPrimaryQueryCompiler("uq-primary-v1", "Asia/Shanghai", LegacyQueryRuntimeFacts{
		AccessBKData: new(bool), BKDataCMDBLevelTables: []string{},
		SystemDiskFilter:    LegacyRuntimeFilterFact{FieldName: "device_type", Values: []string{"iso9660", "tmpfs", "udf"}},
		SystemNetworkFilter: LegacyRuntimeFilterFact{FieldName: "device_name", Values: []string{"lo"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return compiler
}

// pythonCompileDifferences are the golden cases this build answers apart
// from the platform's compile_promql_expression, each for a reason that
// lies outside the expression compiler.
var pythonCompileDifferences = map[string]string{
	// One query is not an expression: the platform's Access runs it as a
	// single PromQL (promql_multi_expression needs more than one), and so
	// does this build.
	"one query": "single PromQL",
	// A query's agg_interval is decoded as an integer for every strategy
	// this build reads; a string is refused at decode, before any compiler.
	"string interval": "refused at decode",
}

// The platform's own compiler is the oracle: every case of the golden file,
// generated from compile_promql_expression, is compiled here to the same
// PromQL or refused the same way.
func TestAMultiPromQLExpressionCompilesAsThePlatformCompilesIt(t *testing.T) {
	raw, err := os.ReadFile("testdata/named_outputs/promql_expression_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name         string           `json:"name"`
		QueryConfigs []map[string]any `json:"query_configs"`
		Expression   string           `json:"expression"`
		PromQL       string           `json:"promql"`
		Refused      bool             `json:"refused"`
	}
	// Numbers kept as written: an interval of 60.0 has to reach the decoder
	// as 60.0, which is the case it is.
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) < 30 {
		t.Fatalf("%d golden cases, the file holds more", len(cases))
	}
	compiler := namedOutputsTestCompiler(t)
	for _, golden := range cases {
		configs := make([]json.RawMessage, 0, len(golden.QueryConfigs))
		for _, config := range golden.QueryConfigs {
			config["expression_mode"] = promQLExpressionMode
			encoded, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			configs = append(configs, encoded)
		}
		facts, err := compiler.CompilePrimaryQuery(context.Background(), PrimaryQuerySource{
			Identity: SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}, StrategyID: "104",
			ItemID: "1", QueryMD5: "query-104", Expression: golden.Expression, QueryConfigs: configs,
		})
		if _, differs := pythonCompileDifferences[golden.Name]; differs {
			continue
		}
		switch {
		case golden.Refused && err == nil:
			t.Errorf("%s: compiled to %q, the platform refuses it", golden.Name, facts.PromQL.Expression)
		case !golden.Refused && err != nil:
			t.Errorf("%s: refused (%v), the platform compiles %q", golden.Name, err, golden.PromQL)
		case !golden.Refused && facts.PromQL.Expression != golden.PromQL:
			t.Errorf("%s: compiled\n%q\nthe platform compiles\n%q", golden.Name, facts.PromQL.Expression, golden.PromQL)
		}
	}
}

// An output's expression names its input after its ends are stripped, as the
// platform strips it: space inside it is not dropped, so "a b" is not the
// query aliased "ab", where the platform fails to find it.
func TestAnOutputExpressionIsStrippedNotSquashed(t *testing.T) {
	compiler := namedOutputsTestCompiler(t)
	source := multiPromQLSource("")
	source.QueryConfigs[0] = json.RawMessage(strings.Replace(string(source.QueryConfigs[0]), `"alias":"a"`, `"alias":"ab"`, 1))
	source.Expression = "ab + b"
	outputs := func(expression string) json.RawMessage {
		return json.RawMessage(`{"response_contract":"named_outputs/v1","legacy_output_ref":"RESULT","output_list":[` +
			`{"reference_name":"A","expression":"` + expression + `"},{"reference_name":"RESULT","expression":"ab+b"}]}`)
	}
	source.QueryOutputConfig = outputs(` ab\t`)
	if _, err := compiler.CompilePrimaryQuery(context.Background(), source); err != nil {
		t.Fatalf("an expression with space at its ends: err = %v", err)
	}
	source.QueryOutputConfig = outputs(`a b`)
	var compileErr *QueryPlanCompileError
	if _, err := compiler.CompilePrimaryQuery(context.Background(), source); !errors.As(err, &compileErr) || !strings.HasPrefix(compileErr.Reason, "QUERY_OUTPUT_CONFIG_") {
		t.Fatalf("an expression with space inside: err = %v, want a QUERY_OUTPUT_CONFIG_ refusal", err)
	}
}

// An item whose queries are marked in part is not the expression: it reads as
// several PromQL queries, the retired form.
func TestAPartlyMarkedMultiPromQLItemIsTheRetiredForm(t *testing.T) {
	source := multiPromQLSource("")
	source.QueryConfigs[1] = json.RawMessage(strings.Replace(string(source.QueryConfigs[1]), `"expression_mode":"promql",`, "", 1))
	_, err := namedOutputsTestCompiler(t).CompilePrimaryQuery(context.Background(), source)
	var compileErr *QueryPlanCompileError
	if !errors.As(err, &compileErr) || compileErr.Reason != "QUERY_PROMQL_FORM_RETIRED" {
		t.Fatalf("err = %v, want QUERY_PROMQL_FORM_RETIRED", err)
	}
}

func multiPromQLSource(outputConfig string) PrimaryQuerySource {
	query := func(alias, promql string) json.RawMessage {
		return json.RawMessage(`{"data_source_label":"prometheus","data_type_label":"time_series","expression_mode":"promql",` +
			`"alias":"` + alias + `","promql":"` + promql + `","agg_interval":60}`)
	}
	return PrimaryQuerySource{
		Identity: SourceIdentity{TenantID: "tenant-a", BusinessID: "2", SpaceScope: "bkcc__2"}, StrategyID: "105",
		ItemID: "1", QueryMD5: "query-105", Expression: "a + b",
		QueryConfigs:      []json.RawMessage{query("a", "sum by (job) (up)"), query("b", "sum by (job) (down)"), query("c", "sum by (job) (queue_depth)")},
		QueryOutputConfig: json.RawMessage(outputConfig),
	}
}

// Named outputs are asked for as the provider takes them: the legacy output
// is the compiled PromQL detection reads, every other output its query's
// full PromQL - a query outside the expression included - and the outputs
// make the query a different Query Group from the same query without them.
func TestNamedOutputsOfAMultiPromQLItemAreAskedAsFullPromQL(t *testing.T) {
	compiler := namedOutputsTestCompiler(t)
	with, err := compiler.CompilePrimaryQuery(context.Background(), multiPromQLSource(`{"response_contract":" named_outputs/v1 ",
		"legacy_output_ref":"RESULT","output_list":[{"reference_name":"A","expression":"a"},{"reference_name":"B","expression":" b "},
		{"reference_name":"C","expression":"c"},{"reference_name":"RESULT","expression":"a + b"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := execution.NamedOutputs{LegacyRef: "RESULT", Outputs: []execution.NamedOutput{
		{Ref: "A", Expression: "sum by (job) (up)"}, {Ref: "B", Expression: "sum by (job) (down)"},
		{Ref: "C", Expression: "sum by (job) (queue_depth)"}, {Ref: "RESULT", Expression: "(sum by (job) (up)) + (sum by (job) (down))"},
	}}
	if with.NamedOutputs == nil || with.PromQL == nil || with.PromQL.Expression != want.Outputs[3].Expression {
		t.Fatalf("facts %+v named outputs %+v", with.PromQL, with.NamedOutputs)
	}
	for index, output := range want.Outputs {
		if with.NamedOutputs.LegacyRef != want.LegacyRef || with.NamedOutputs.Outputs[index] != output {
			t.Fatalf("named outputs %+v, want %+v", *with.NamedOutputs, want)
		}
	}
	if err := with.Validate(); err != nil {
		t.Fatalf("compiled facts do not validate: %v", err)
	}
	without, err := compiler.CompilePrimaryQuery(context.Background(), multiPromQLSource(""))
	if err != nil {
		t.Fatal(err)
	}
	withGroup, _ := deriveQueryGroupIdentity(with)
	withoutGroup, _ := deriveQueryGroupIdentity(without)
	if without.NamedOutputs != nil || withGroup == withoutGroup || with.QueryRevision == without.QueryRevision {
		t.Fatalf("named outputs did not make a different query: groups %s / %s", withGroup, withoutGroup)
	}
}

// A config this build cannot ask for refuses the strategy by name rather
// than compiling it without its outputs, which would alert with no snapshot.
func TestAnUnusableQueryOutputConfigRefusesTheStrategy(t *testing.T) {
	compiler := namedOutputsTestCompiler(t)
	outputs := func(list string) string {
		return `{"response_contract":"named_outputs/v1","legacy_output_ref":"RESULT","output_list":[` + list + `]}`
	}
	for name, config := range map[string]string{
		"other contract":      `{"response_contract":"named_outputs/v2","legacy_output_ref":"RESULT","output_list":[{"reference_name":"RESULT","expression":"a+b"}]}`,
		"legacy missing":      outputs(`{"reference_name":"A","expression":"a"}`),
		"duplicate reference": outputs(`{"reference_name":"A","expression":"a"},{"reference_name":"A","expression":"b"},{"reference_name":"RESULT","expression":"a+b"}`),
		"invalid reference":   outputs(`{"reference_name":"1A","expression":"a"},{"reference_name":"RESULT","expression":"a+b"}`),
		"not an input":        outputs(`{"reference_name":"A","expression":"a * 2"},{"reference_name":"RESULT","expression":"a+b"}`),
		"unknown alias":       outputs(`{"reference_name":"D","expression":"d"},{"reference_name":"RESULT","expression":"a+b"}`),
		"more than four":      outputs(`{"reference_name":"A","expression":"a"},{"reference_name":"B","expression":"b"},{"reference_name":"C","expression":"c"},{"reference_name":"D","expression":"a"},{"reference_name":"RESULT","expression":"a+b"}`),
		"not a string":        `{"response_contract":"named_outputs/v1","legacy_output_ref":7,"output_list":[]}`,
		"output list missing": `{"response_contract":"named_outputs/v1","legacy_output_ref":"RESULT"}`,
		"empty expression":    outputs(`{"reference_name":"A","expression":"  "},{"reference_name":"RESULT","expression":"a+b"}`),
	} {
		_, err := compiler.CompilePrimaryQuery(context.Background(), multiPromQLSource(config))
		var compileErr *QueryPlanCompileError
		if !errors.As(err, &compileErr) || !strings.HasPrefix(compileErr.Reason, "QUERY_OUTPUT_CONFIG_") {
			t.Errorf("%s: err = %v, want a QUERY_OUTPUT_CONFIG_ refusal", name, err)
		}
	}
}

// Several PromQL queries without the expression mark are the older shape,
// which the platform reads as independent queries; it is retired, and stays
// refused here as such.
func TestSeveralUnmarkedPromQLQueriesStayRefused(t *testing.T) {
	source := multiPromQLSource("")
	for index := range source.QueryConfigs {
		source.QueryConfigs[index] = json.RawMessage(strings.Replace(string(source.QueryConfigs[index]), `"expression_mode":"promql",`, "", 1))
	}
	_, err := namedOutputsTestCompiler(t).CompilePrimaryQuery(context.Background(), source)
	var compileErr *QueryPlanCompileError
	if !errors.As(err, &compileErr) || compileErr.Reason != "QUERY_PROMQL_FORM_RETIRED" {
		t.Fatalf("err = %v, want QUERY_PROMQL_FORM_RETIRED", err)
	}
}

// The structured flavour names each input by its query_list reference, and
// its legacy output is this build's metric_merge - the expression the
// provider compares it to - whatever case the config wrote.
func TestNamedOutputsOfAStructuredItemNameQueryListReferences(t *testing.T) {
	sources := identityGoldenSources()
	source := sources["structured two queries"]
	source.QueryOutputConfig = json.RawMessage(`{"response_contract":"named_outputs/v1","legacy_output_ref":"C",
		"output_list":[{"reference_name":"A","expression":"a"},{"reference_name":"B","expression":"b"},
		{"reference_name":"C","expression":"A / B * 100"}]}`)
	facts, err := namedOutputsTestCompiler(t).CompilePrimaryQuery(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if facts.NamedOutputs == nil || facts.NamedOutputs.Outputs[0].Expression != "a" || facts.NamedOutputs.Outputs[1].Expression != "b" ||
		facts.NamedOutputs.Outputs[2].Expression != facts.MetricMerge {
		t.Fatalf("named outputs %+v, metric_merge %q", facts.NamedOutputs, facts.MetricMerge)
	}
}

// A reference name is at most 64 characters, the legacy output's as much as
// any: alarmd's own bound, so that four names always fit an event's
// detection snapshot. 64 compiles; 65 is refused by name.
func TestAReferenceNamePastSixtyFourCharactersIsRefused(t *testing.T) {
	compiler := namedOutputsTestCompiler(t)
	config := func(legacy string) string {
		return `{"response_contract":"named_outputs/v1","legacy_output_ref":"` + legacy + `","output_list":[` +
			`{"reference_name":"A","expression":"a"},{"reference_name":"` + legacy + `","expression":"a + b"}]}`
	}
	fits := "R" + strings.Repeat("x", maxNamedOutputRefLength-1)
	compiled, err := compiler.CompilePrimaryQuery(context.Background(), multiPromQLSource(config(fits)))
	if err != nil || compiled.NamedOutputs == nil || compiled.NamedOutputs.LegacyRef != fits {
		t.Fatalf("a %d-character reference name: %v", len(fits), err)
	}
	_, err = compiler.CompilePrimaryQuery(context.Background(), multiPromQLSource(config(fits+"x")))
	var compileErr *QueryPlanCompileError
	if !errors.As(err, &compileErr) || compileErr.Reason != "QUERY_OUTPUT_CONFIG_INVALID" {
		t.Fatalf("a %d-character reference name: err = %v, want QUERY_OUTPUT_CONFIG_INVALID", len(fits)+1, err)
	}
}
