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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// promQLExpressionMode marks a PromQL query that is one input of an
// expression over several, rather than a query of its own.
const promQLExpressionMode = "promql"

// multiPromQLExpression says an item is the platform's multi-PromQL
// expression: more than one query, every one marked expression_mode
// promql. The platform's Access asks exactly this (Item.__init__,
// promql_multi_expression); an item of several PromQL queries without the
// mark is the older shape, read by the platform as independent queries whose
// records are concatenated, and stays refused here.
func multiPromQLExpression(configs []legacyQueryConfig) bool {
	if len(configs) < 2 {
		return false
	}
	for _, config := range configs {
		if config.ExpressionMode != promQLExpressionMode {
			return false
		}
	}
	return true
}

// compileMultiPromQL compiles a multi-PromQL expression into the one PromQL
// the platform runs for it: every query Prometheus time series under the
// same filters and interval, the expression expanded over their aliases
// (compilePromQLExpression). The single PromQL path is then the rest of it.
func (compiler *LegacyPrimaryQueryCompiler) compileMultiPromQL(source PrimaryQuerySource, configs []legacyQueryConfig) (execution.QueryPlanFacts, error) {
	queries := make([]promQLAliasQuery, 0, len(configs))
	seen := make(map[string]bool, len(configs))
	interval := int64(0)
	var filters any
	for index, config := range configs {
		if config.DataSourceLabel != "prometheus" || config.DataTypeLabel != "time_series" {
			return execution.QueryPlanFacts{}, atQueryConfig(queryConfigRejected("QUERY_PROMQL_EXPRESSION_INVALID",
				errors.New("every query of a PromQL expression must be Prometheus time series")), index)
		}
		alias := config.Alias
		if !promQLAliasPattern.MatchString(alias) || promQLReservedAlias(alias) || seen[alias] {
			return execution.QueryPlanFacts{}, atQueryConfig(queryConfigRejected("QUERY_PROMQL_EXPRESSION_INVALID",
				fmt.Errorf("invalid or duplicate PromQL query alias %q", alias)), index)
		}
		seen[alias] = true
		promql := strings.TrimFunc(config.PromQL, pythonSpace)
		if promql == "" {
			return execution.QueryPlanFacts{}, atQueryConfig(queryConfigRejected("QUERY_PROMQL_EXPRESSION_INVALID",
				fmt.Errorf("PromQL query %q is empty", alias)), index)
		}
		// No default here: the platform refuses a query of an expression
		// without its own interval at save, so none reaches the cache.
		queryInterval := config.AggInterval
		if queryInterval <= 0 {
			return execution.QueryPlanFacts{}, atQueryConfig(queryConfigRejected("QUERY_PROMQL_EXPRESSION_INVALID",
				fmt.Errorf("invalid PromQL query interval: %s", alias)), index)
		}
		if interval != 0 && queryInterval != interval {
			return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_PROMQL_EXPRESSION_INVALID",
				errors.New("PromQL queries must use the same interval"))
		}
		interval = queryInterval
		decoded, err := decodedFilters(config.FilterDict)
		if err != nil {
			return execution.QueryPlanFacts{}, atQueryConfig(queryConfigRejected("QUERY_PROMQL_MATCH_INVALID", err), index)
		}
		if index > 0 && !reflect.DeepEqual(decoded, filters) {
			return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_PROMQL_EXPRESSION_INVALID",
				errors.New("PromQL queries must use the same filters"))
		}
		filters = decoded
		queries = append(queries, promQLAliasQuery{Alias: alias, PromQL: promql})
	}
	expression, err := compilePromQLExpression(queries, source.Expression)
	if err != nil {
		return execution.QueryPlanFacts{}, queryConfigRejected("QUERY_PROMQL_EXPRESSION_INVALID", err)
	}
	compiled := configs[0]
	compiled.PromQL, compiled.AggInterval = expression, interval
	return compiler.compilePromQL(source, compiled)
}

// decodedFilters is a filter_dict as the platform compares two of them: an
// absent or null one is no filters.
func decodedFilters(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// legacyQueryOutputConfig is an item's query_output_config as the strategy
// cache carries it. Fields are read raw so that a value of the wrong type
// is refused, as the platform refuses it, rather than read as empty.
type legacyQueryOutputConfig struct {
	ResponseContract json.RawMessage   `json:"response_contract"`
	LegacyOutputRef  json.RawMessage   `json:"legacy_output_ref"`
	OutputList       []json.RawMessage `json:"output_list"`
}

var namedOutputRefName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// maxNamedOutputRefLength is the longest reference name an output may have.
// The platform caps the aliases of input queries at 12 characters and the
// legacy output's reference name not at all; this bound is alarmd's own,
// so that four reference names always fit an event's detection snapshot.
// The execution contract holds a compiled plan to the same bound.
const maxNamedOutputRefLength = execution.MaxNamedOutputRefLength

// namedOutputsOf is the item's query_output_config as the outputs facts asks
// the provider for, or nil for an item without one.
//
// The platform normalizes the config at save (Item.normalize_query_output_config)
// and the checks are repeated here, so that a config this build cannot ask
// for refuses the strategy by name instead of reaching the provider: a
// strategy compiled without its outputs would alert with no snapshot and
// nothing to say why.
//
// The expressions are the ones the provider is sent, translated the way the
// platform translates them: the legacy output is the query's own expression
// - the compiled PromQL, or this build's metric_merge, which is what the
// provider compares it to - and every other output names one input query:
// its full PromQL, or its query_list reference.
func namedOutputsOf(raw json.RawMessage, configs []legacyQueryConfig, facts execution.QueryPlanFacts) (*execution.NamedOutputs, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	invalid := func(err error) error { return queryConfigRejected("QUERY_OUTPUT_CONFIG_INVALID", err) }
	var config legacyQueryOutputConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, invalid(err)
	}
	contract, err := strippedConfigString(config.ResponseContract)
	if err != nil {
		return nil, invalid(err)
	}
	legacyRef, err := strippedConfigString(config.LegacyOutputRef)
	if err != nil {
		return nil, invalid(err)
	}
	if contract != execution.NamedOutputsContract {
		return nil, invalid(fmt.Errorf("response contract %q", contract))
	}
	if config.OutputList == nil || len(config.OutputList) > execution.MaxNamedOutputs {
		return nil, invalid(fmt.Errorf("%d outputs, at most %d", len(config.OutputList), execution.MaxNamedOutputs))
	}
	inputs, err := namedOutputInputs(configs, facts)
	if err != nil {
		return nil, err
	}
	outputs := &execution.NamedOutputs{LegacyRef: legacyRef, Outputs: make([]execution.NamedOutput, 0, len(config.OutputList))}
	seen := make(map[string]bool, len(config.OutputList))
	for _, entry := range config.OutputList {
		var output struct {
			ReferenceName json.RawMessage `json:"reference_name"`
			Expression    json.RawMessage `json:"expression"`
		}
		if err := json.Unmarshal(entry, &output); err != nil {
			return nil, invalid(err)
		}
		ref, err := strippedConfigString(output.ReferenceName)
		if err != nil {
			return nil, invalid(err)
		}
		expression, err := strippedConfigString(output.Expression)
		if err != nil {
			return nil, invalid(err)
		}
		if ref == "" || expression == "" || len(ref) > maxNamedOutputRefLength || !namedOutputRefName.MatchString(ref) || seen[ref] {
			return nil, invalid(fmt.Errorf("output %q", ref))
		}
		seen[ref] = true
		if ref == legacyRef {
			outputs.Outputs = append(outputs.Outputs, execution.NamedOutput{Ref: ref, Expression: legacyExpression(facts)})
			continue
		}
		input, found := inputs[strings.TrimFunc(expression, pythonSpace)]
		if !found {
			return nil, invalid(fmt.Errorf("output %q is not one input query", ref))
		}
		outputs.Outputs = append(outputs.Outputs, execution.NamedOutput{Ref: ref, Expression: input})
	}
	if legacyRef == "" || !seen[legacyRef] {
		return nil, invalid(fmt.Errorf("legacy output %q is not among the outputs", legacyRef))
	}
	return outputs, nil
}

// namedOutputInputs is what each input query's alias asks the provider for:
// its PromQL, or the query_list reference its one clause carries.
func namedOutputInputs(configs []legacyQueryConfig, facts execution.QueryPlanFacts) (map[string]string, error) {
	inputs := make(map[string]string, len(configs))
	if facts.PromQL != nil {
		for _, config := range configs {
			if config.Alias != "" {
				inputs[config.Alias] = config.PromQL
			}
		}
		return inputs, nil
	}
	references := make(map[string]int, len(facts.QueryList))
	for _, clause := range facts.QueryList {
		references[clause.ReferenceName]++
	}
	for _, config := range configs {
		if config.Alias == "" {
			continue
		}
		reference := strings.ToLower(config.Alias)
		if references[reference] != 1 {
			// The alias reads as more than one clause, or none: not one
			// reference the provider can name as an output.
			return nil, queryUnsupported("QUERY_OUTPUT_CONFIG_NOT_MIGRATED",
				fmt.Errorf("query %q is not one query_list reference", config.Alias))
		}
		inputs[config.Alias] = reference
	}
	return inputs, nil
}

func legacyExpression(facts execution.QueryPlanFacts) string {
	if facts.PromQL != nil {
		return facts.PromQL.Expression
	}
	return facts.MetricMerge
}

// strippedConfigString is a config string as the platform reads it: a JSON
// string, stripped; absent reads as empty, any other type is refused.
func strippedConfigString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", errors.New("query_output_config value is not a string")
	}
	return strings.TrimFunc(value, pythonSpace), nil
}
