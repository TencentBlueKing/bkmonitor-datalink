package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

const emptyConditionMetricConfig = `{"data_source_label":"bk_monitor","data_type_label":"time_series","result_table_id":"system.cpu","metric_field":"usage","alias":"a","agg_method":"AVG","agg_interval":60,"agg_dimension":["host"],"agg_condition":%s}`
const emptyConditionLogConfig = `{"data_source_label":"bk_log_search","data_type_label":"log","index_set_id":7,"agg_method":"COUNT","alias":"a","agg_interval":60,"agg_dimension":["host"],"agg_condition":%s}`

func compileWithConditions(t *testing.T, config, conditions string) (execution.QueryPlanFacts, error) {
	t.Helper()
	planner, err := NewLegacyPrimaryQueryCompiler("uq", "UTC", LegacyQueryRuntimeFacts{})
	if err != nil {
		t.Fatal(err)
	}
	return planner.CompilePrimaryQuery(context.Background(), pollingTestSource(fmt.Sprintf(config, conditions)))
}

// A condition with no value is one UQ ignores, so the strategy compiles to
// exactly the query written without it, whichever family of source it is.
func TestAConditionWithNoValueCompilesAsIfItWereNotWritten(t *testing.T) {
	for _, config := range []string{emptyConditionMetricConfig, emptyConditionLogConfig} {
		for _, tc := range []struct{ name, with, without string }{
			{"first",
				`[{"key":"bk_biz_id","method":"eq","value":[]},{"condition":"and","key":"host","method":"eq","value":["a"]}]`,
				`[{"key":"host","method":"eq","value":["a"]}]`},
			{"the only one", `[{"key":"bk_biz_id","method":"eq","value":[]}]`, `[]`},
			{"between two joined by and",
				`[{"key":"host","method":"eq","value":["a"]},{"condition":"and","key":"bk_biz_id","method":"neq","value":[]},{"condition":"and","key":"pod","method":"reg","value":["p"]}]`,
				`[{"key":"host","method":"eq","value":["a"]},{"condition":"and","key":"pod","method":"reg","value":["p"]}]`},
			// UQ drops the connector in front of the empty condition, which here
			// is the "or": the next condition joins the group before it.
			{"carrying the or",
				`[{"key":"host","method":"eq","value":["a"]},{"condition":"or","key":"bk_biz_id","method":"eq","value":[]},{"condition":"and","key":"pod","method":"eq","value":["p"]}]`,
				`[{"key":"host","method":"eq","value":["a"]},{"condition":"and","key":"pod","method":"eq","value":["p"]}]`},
			{"last, after an or",
				`[{"key":"host","method":"eq","value":["a"]},{"condition":"or","key":"bk_biz_id","method":"eq","value":[]}]`,
				`[{"key":"host","method":"eq","value":["a"]}]`},
			{"two leading, then and",
				`[{"key":"bk_biz_id","method":"eq","value":[]},{"condition":"and","key":"bk_cloud_id","method":"include","value":[]},{"condition":"and","key":"host","method":"eq","value":["a"]}]`,
				`[{"key":"host","method":"eq","value":["a"]}]`},
		} {
			with, err := compileWithConditions(t, config, tc.with)
			if err != nil {
				t.Fatalf("%s with the empty condition: %v", tc.name, err)
			}
			without, err := compileWithConditions(t, config, tc.without)
			if err != nil {
				t.Fatalf("%s without it: %v", tc.name, err)
			}
			withBytes, _ := json.Marshal(with)
			withoutBytes, _ := json.Marshal(without)
			if string(withBytes) != string(withoutBytes) {
				t.Fatalf("%s: compiled\n%s\nwant the query without the condition\n%s", tc.name, withBytes, withoutBytes)
			}
		}
	}
}

// uqGroups is unify-query's reading of a condition list before any storage
// sees it (query/structured/condition.go, Conditions.AnalysisConditions),
// transcribed: a field with no value, other than an existence check, is
// skipped, and a kept field after the first starts a new group when the
// connector in front of it is "or". A list with no fields at all has no
// groups.
func uqGroups(keys []string, values [][]string, connectors []string) [][]string {
	if len(keys) == 0 {
		return nil
	}
	total, row := [][]string{}, []string{}
	for index, key := range keys {
		if len(values[index]) == 0 {
			continue
		}
		if index > 0 && connectors[index-1] == "or" {
			total, row = append(total, row), []string{key}
			continue
		}
		row = append(row, key)
	}
	return append(total, row)
}

// Every list of up to four conditions, each with or without a value and
// joined by and or or: the compiled conditions read in UQ as the same groups
// as the written ones, or the strategy is refused by name exactly when UQ
// would read an empty group in front of an "or".
func TestCompiledConditionsReadInUQAsTheWrittenOnes(t *testing.T) {
	for size := 1; size <= 4; size++ {
		for shape := 0; shape < 1<<(2*size-1); shape++ {
			keys := make([]string, size)
			values := make([][]string, size)
			connectors := make([]string, 0, size-1)
			written := make([]string, 0, size)
			for index := range keys {
				keys[index] = fmt.Sprintf("k%d", index)
				values[index] = []string{}
				if shape>>index&1 == 1 {
					values[index] = []string{"v"}
				}
				value, _ := json.Marshal(values[index])
				connector := ""
				if index > 0 {
					connector = "and"
					if shape>>(size+index-1)&1 == 1 {
						connector = "or"
					}
					connectors = append(connectors, connector)
				}
				written = append(written, fmt.Sprintf(`{"condition":%q,"key":%q,"method":"eq","value":%s}`, connector, keys[index], value))
			}
			want := uqGroups(keys, values, connectors)
			emptyGroup := false
			for _, group := range want {
				emptyGroup = emptyGroup || (len(group) == 0 && len(want) > 1)
			}
			facts, err := compileWithConditions(t, emptyConditionMetricConfig, "["+strings.Join(written, ",")+"]")
			var failure *QueryPlanCompileError
			if emptyGroup {
				if !errors.As(err, &failure) || failure.Disposition != DispositionConfigRejected || failure.Reason != "QUERY_CONDITION_EMPTY_GROUP" {
					t.Fatalf("%v joined by %v: %v, want QUERY_CONDITION_EMPTY_GROUP", values, connectors, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("%v joined by %v: %v", values, connectors, err)
			}
			compiled := facts.QueryList[0].Conditions
			compiledKeys := make([]string, 0, len(compiled.Fields))
			compiledValues := make([][]string, 0, len(compiled.Fields))
			for _, field := range compiled.Fields {
				compiledKeys = append(compiledKeys, field.Field)
				compiledValues = append(compiledValues, []string{field.Values[0].StringValue})
			}
			got := uqGroups(compiledKeys, compiledValues, compiled.Connectors)
			// Every condition empty: UQ reads one empty group, the compiled query
			// has none. The two render alike -- VictoriaMetrics puts only the
			// table and metric in an empty group, which is what it sends for no
			// conditions (AllConditions.VMString), and BkSql renders neither.
			if len(want) == 1 && len(want[0]) == 0 {
				want = nil
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%v joined by %v: compiled reads as %v in UQ, written as %v", values, connectors, got, want)
			}
		}
	}
}

// The platform's log and event sources send an existence check with the one
// empty value, whatever was written (_parse_conditions); UQ's storages that
// run it read no value from it. A metric source sends it as written, and one
// with no value is refused as before: alarmd's query has no condition without
// a value.
func TestAnExistenceCheckIsSentAsThePlatformSendsIt(t *testing.T) {
	for _, written := range []string{`[]`, `["x"]`} {
		facts, err := compileWithConditions(t, emptyConditionLogConfig, `[{"key":"trace_id","method":"exists","value":`+written+`}]`)
		if err != nil {
			t.Fatalf("log exists %s: %v", written, err)
		}
		field := facts.QueryList[0].Conditions.Fields[0]
		if field.Operator != "existed" || len(field.Values) != 1 || field.Values[0].StringValue != "" {
			t.Fatalf("log exists %s compiled as %+v, want existed with the one empty value", written, field)
		}
	}
	_, err := compileWithConditions(t, emptyConditionMetricConfig, `[{"key":"host","method":"existed","value":[]}]`)
	var failure *QueryPlanCompileError
	if !errors.As(err, &failure) || failure.Disposition != DispositionConfigRejected || failure.Reason != "QUERY_CONDITION_INVALID" {
		t.Fatalf("metric existed with no value: %v, want QUERY_CONDITION_INVALID", err)
	}
}

// The platform joins with "or" only on "or" and with "and" on anything else,
// and UQ refuses a connector that is neither.
func TestConnectorsAreReadAsThePlatformReadsThem(t *testing.T) {
	for written, want := range map[string]string{"or": "or", "and": "and", "": "and", "AND": "and", "OR": "and"} {
		facts, err := compileWithConditions(t, emptyConditionMetricConfig,
			`[{"key":"host","method":"eq","value":["a"]},{"condition":"`+written+`","key":"pod","method":"eq","value":["p"]}]`)
		if err != nil {
			t.Fatalf("connector %q: %v", written, err)
		}
		if got := facts.QueryList[0].Conditions.Connectors; len(got) != 1 || got[0] != want {
			t.Fatalf("connector %q compiled as %v, want %q", written, got, want)
		}
	}
	// Nothing reads the first condition's connector, the platform deletes it
	// and UQ never looks, so an "or" there is no empty group.
	facts, err := compileWithConditions(t, emptyConditionMetricConfig,
		`[{"condition":"or","key":"host","method":"eq","value":["a"]},{"condition":"and","key":"pod","method":"eq","value":["p"]}]`)
	if err != nil {
		t.Fatalf("an or on the first condition: %v", err)
	}
	if got := facts.QueryList[0].Conditions.Connectors; len(got) != 1 || got[0] != "and" {
		t.Fatalf("an or on the first condition compiled as %v, want [and]", got)
	}
}

// A wildcard method on a log or event source is sent as contains, or
// ncontains, matched as a wildcard (_parse_conditions); UQ has no wildcard
// operator.
func TestAWildcardMethodIsSentAsAWildcardMatch(t *testing.T) {
	for method, want := range map[string]string{"wildcard": "contains", "nwildcard": "ncontains"} {
		facts, err := compileWithConditions(t, emptyConditionLogConfig, `[{"key":"path","method":"`+method+`","value":["/api/*"]}]`)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		field := facts.QueryList[0].Conditions.Fields[0]
		if field.Operator != want || field.Wildcard != "true" || field.Values[0].StringValue != "/api/*" {
			t.Fatalf("%s compiled as %+v, want %s matched as a wildcard", method, field, want)
		}
	}
}
