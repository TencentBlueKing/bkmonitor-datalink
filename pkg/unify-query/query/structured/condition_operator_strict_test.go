// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/VictoriaMetrics/metricsql"
	"github.com/influxdata/influxql"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	md "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	uqQuery "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

// strictKey 是用例里条件和序列共用的维度名。
const strictKey = "k"

// strictSeries 是用例判定用的序列：strictKey 的取值，absent 表示序列没有这个维度。
// 取值里带正则元字符（a.b）和只差一个字符的值（axb），用来区分精确匹配和正则匹配、
// 整串匹配和子串匹配。
var strictSeries = []string{"a", "b", "a.b", "axb", "ab", "xa", absent}

const absent = "<absent>"

// labelValue 是序列在 strictKey 上的取值。PromQL 和 InfluxQL 都把没有这个维度当作空串
// （InfluxDB：tsdb/index.go seriesByBinaryExprStringIterator，tag 等于空串时选出不带该 tag 的序列）。
func labelValue(series string) string {
	if series == absent {
		return ""
	}
	return series
}

// promQLSemantics 按 PromQL 的定义给出条件选中的序列，不经过 UQ 的渲染代码：
// = 和 != 是精确比较；=~ 和 !~ 是整串匹配（Prometheus 和 VictoriaMetrics 都给正则加锚）；
// 多个值是任一命中。
func promQLSemantics(t *testing.T, operator string, values []string) []string {
	t.Helper()
	return selectSeries(func(v string) bool {
		switch operator {
		case ConditionEqual, ConditionExact, ConditionContains:
			return in(values, v)
		case ConditionNotEqual, ConditionNotContains:
			return !in(values, v)
		case ConditionRegEqual:
			return anyRegexp(t, values, v, true)
		case ConditionNotRegEqual:
			return !anyRegexp(t, values, v, true)
		case ConditionExisted:
			return v != ""
		case ConditionNotExisted:
			return v == ""
		}
		t.Fatalf("no PromQL semantics for %q", operator)
		return false
	})
}

// influxQLSemantics 按 InfluxQL 的定义给出条件选中的序列：= 和 != 精确比较；=~ 和 !~ 是子串匹配
// （InfluxQL 不给正则加锚）；多个值是任一命中。
func influxQLSemantics(t *testing.T, operator string, values []string) []string {
	t.Helper()
	return selectSeries(func(v string) bool {
		switch operator {
		case ConditionEqual, ConditionExact, ConditionContains:
			return in(values, v)
		case ConditionNotEqual, ConditionNotContains:
			return !in(values, v)
		case ConditionRegEqual:
			return anyRegexp(t, values, v, false)
		case ConditionNotRegEqual:
			return !anyRegexp(t, values, v, false)
		case ConditionExisted:
			return v != ""
		case ConditionNotExisted:
			return v == ""
		}
		t.Fatalf("no InfluxQL semantics for %q", operator)
		return false
	})
}

func selectSeries(match func(string) bool) []string {
	selected := make([]string, 0, len(strictSeries))
	for _, series := range strictSeries {
		if match(labelValue(series)) {
			selected = append(selected, series)
		}
	}
	return selected
}

func in(values []string, v string) bool {
	for _, value := range values {
		if value == v {
			return true
		}
	}
	return false
}

func anyRegexp(t *testing.T, patterns []string, v string, anchored bool) bool {
	t.Helper()
	for _, pattern := range patterns {
		if anchored {
			pattern = "^(?:" + pattern + ")$"
		}
		re, err := regexp.Compile(pattern)
		require.NoError(t, err)
		if re.MatchString(v) {
			return true
		}
	}
	return false
}

// vmSelected 把条件经 VMString 渲染成 VM 选择器，用 MetricsQL 解析器读回，再用 Prometheus 的标签匹配器判每条序列。
func vmSelected(t *testing.T, conditions AllConditions) ([]string, string) {
	t.Helper()
	condition, _, err := conditions.VMString("", "", false)
	require.NoError(t, err)
	expr, err := metricsql.Parse(condition.ToMatch())
	require.NoError(t, err, "selector %s", condition)
	metric, ok := expr.(*metricsql.MetricExpr)
	require.True(t, ok)
	matchers := make([][]*labels.Matcher, 0, len(metric.LabelFilterss))
	for _, filters := range metric.LabelFilterss {
		group := make([]*labels.Matcher, 0, len(filters))
		for _, filter := range filters {
			matchType := labels.MatchEqual
			switch {
			case filter.IsRegexp && filter.IsNegative:
				matchType = labels.MatchNotRegexp
			case filter.IsRegexp:
				matchType = labels.MatchRegexp
			case filter.IsNegative:
				matchType = labels.MatchNotEqual
			}
			matcher, err := labels.NewMatcher(matchType, filter.Label, filter.Value)
			require.NoError(t, err)
			group = append(group, matcher)
		}
		matchers = append(matchers, group)
	}
	return selectSeries(func(v string) bool {
		for _, group := range matchers {
			all := true
			for _, matcher := range group {
				if !matcher.Matches(v) {
					all = false
					break
				}
			}
			if all {
				return true
			}
		}
		return false
	}), string(condition)
}

// influxSelected 把条件经 ConvertToPromBuffer 渲染成 InfluxQL 的 where 条件，用 InfluxQL 解析器读回并逐条序列求值。
func influxSelected(t *testing.T, conditions AllConditions) ([]string, string) {
	t.Helper()
	buffer, err := ConvertToPromBuffer(conditions)
	require.NoError(t, err)
	where := promql.MakeOrExpression(buffer)
	expr, err := influxql.ParseExpr(where)
	require.NoError(t, err, "where %s", where)
	return selectSeries(func(v string) bool {
		eval := influxql.ValuerEval{Valuer: influxql.MapValuer{strictKey: v}}
		return eval.EvalBool(expr)
	}), where
}

func oneCondition(operator string, values []string) AllConditions {
	return AllConditions{{{DimensionName: strictKey, Operator: operator, Value: append([]string(nil), values...)}}}
}

// 每个 VM、InfluxDB 认的算子，单值和多值，渲染出来的条件选中的序列和 PromQL、InfluxQL 语义给出的一致。
// 每组至少有一条序列被选中、一条被排除，所以「全选」和「全不选」的渲染都过不了。
func TestConditionOperatorsSelectBySemanticsOnVMAndInfluxDB(t *testing.T) {
	cases := []struct {
		operator string
		values   []string
	}{
		{ConditionEqual, []string{"a.b"}},
		{ConditionEqual, []string{"a", "a.b"}},
		{ConditionExact, []string{"a.b"}},
		{ConditionExact, []string{"a", "a.b"}},
		{ConditionContains, []string{"a.b"}},
		{ConditionContains, []string{"a", "a.b"}},
		{ConditionNotEqual, []string{"a.b"}},
		{ConditionNotEqual, []string{"a", "a.b"}},
		{ConditionNotContains, []string{"a.b"}},
		{ConditionNotContains, []string{"a", "a.b"}},
		{ConditionRegEqual, []string{"a.b"}},
		{ConditionRegEqual, []string{"a", "x.*"}},
		{ConditionNotRegEqual, []string{"a.b"}},
		{ConditionNotRegEqual, []string{"a", "x.*"}},
		{ConditionExisted, nil},
		{ConditionExisted, []string{""}},
		{ConditionExisted, []string{"a", "b"}},
		{ConditionNotExisted, nil},
		{ConditionNotExisted, []string{""}},
		{ConditionNotExisted, []string{"a", "b"}},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%s %q", c.operator, c.values), func(t *testing.T) {
			wantVM := promQLSemantics(t, c.operator, c.values)
			require.NotEmpty(t, wantVM)
			require.Less(t, len(wantVM), len(strictSeries))
			gotVM, selector := vmSelected(t, oneCondition(c.operator, c.values))
			assert.Equal(t, wantVM, gotVM, "VM selector %s", selector)

			wantInflux := influxQLSemantics(t, c.operator, c.values)
			require.NotEmpty(t, wantInflux)
			require.Less(t, len(wantInflux), len(strictSeries))
			gotInflux, where := influxSelected(t, oneCondition(c.operator, c.values))
			assert.Equal(t, wantInflux, gotInflux, "InfluxQL where %s", where)
		})
	}
}

// existed、nexisted 在 VM 上渲染成对空串的不等于、等于，值不参与；以前值为空时整条被丢掉，值为 [""] 时渲染成 k=""。
func TestExistenceConditionsRenderAsEmptyMatchersOnVM(t *testing.T) {
	for _, values := range [][]string{nil, {""}, {"a", "b"}} {
		selector, _, err := oneCondition(ConditionExisted, values).VMString("", "", false)
		require.NoError(t, err)
		assert.Equal(t, `k!=""`, string(selector))

		selector, _, err = oneCondition(ConditionNotExisted, values).VMString("", "", false)
		require.NoError(t, err)
		assert.Equal(t, `k=""`, string(selector))
	}
	// 和别的条件同组时一起渲染，不是被跳过
	selector, num, err := AllConditions{{
		{DimensionName: "a", Operator: ConditionEqual, Value: []string{"1"}},
		{DimensionName: strictKey, Operator: ConditionExisted},
	}}.VMString("rt", "", false)
	require.NoError(t, err)
	assert.Equal(t, `a="1", k!="", result_table_id="rt"`, string(selector))
	assert.Equal(t, 3, num)
}

// 多值 exact 以前渲染成 k="^(a|a\.b)$"，什么都匹配不上；现在和多值 eq 一样按集合匹配。
func TestMultiValueExactRendersAsSetOnVM(t *testing.T) {
	exact, _, err := oneCondition(ConditionExact, []string{"a", "a.b"}).VMString("", "", false)
	require.NoError(t, err)
	eq, _, err := oneCondition(ConditionEqual, []string{"a", "a.b"}).VMString("", "", false)
	require.NoError(t, err)
	assert.Equal(t, eq, exact)
	assert.Equal(t, `k=~"^(a|a\\.b)$"`, string(exact))
}

// unsupportedOperators 是 VM、InfluxDB 都渲染不了的算子：比较算子（BkSql 上才有）、拼错和大小写不对的词、空算子。
var unsupportedOperators = []string{ConditionGt, ConditionGte, ConditionLt, ConditionLte, "like", "EQ", "neq", ""}

func requireUnsupported(t *testing.T, err error, operator, storage string) {
	t.Helper()
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnsupportedConditionOperator), "%v", err)
	var unsupported *UnsupportedConditionOperatorError
	require.True(t, errors.As(err, &unsupported), "%v", err)
	assert.Equal(t, operator, unsupported.Operator)
	assert.Equal(t, storage, unsupported.Storage)
}

// 不认识的算子在两种渲染上都报具名错误，单值和多值都一样；以前单值按等于渲染，多值拼成 ^(a|b)$。
func TestUnsupportedOperatorsAreRefusedByEveryRendering(t *testing.T) {
	for _, operator := range unsupportedOperators {
		for _, values := range [][]string{{"1"}, {"1", "2"}} {
			t.Run(fmt.Sprintf("%q %q", operator, values), func(t *testing.T) {
				_, _, err := oneCondition(operator, values).VMString("rt", "metric", false)
				requireUnsupported(t, err, operator, ConditionStorageVM)

				_, err = ConvertToPromBuffer(oneCondition(operator, values))
				requireUnsupported(t, err, operator, ConditionStorageInfluxDB)

				conditions := Conditions{FieldList: []ConditionField{{DimensionName: strictKey, Operator: operator, Value: values}}}
				_, _, err = conditions.ToProm()
				requireUnsupported(t, err, operator, ConditionStoragePromQL)

				_, err = oneCondition(operator, values).ToPromMatchers(context.Background(), nil)
				requireUnsupported(t, err, operator, ConditionStoragePromQL)
			})
		}
	}
}

// 不认识的算子不改变选表：MayMatchLabel 不拿它排除候选表，和改动前一样。
func TestUnsupportedOperatorsDoNotPruneCandidateTables(t *testing.T) {
	for _, operator := range unsupportedOperators {
		possible, err := oneCondition(operator, []string{"1"}).MayMatchLabel(strictKey, "2")
		require.NoError(t, err)
		assert.True(t, possible, operator)
	}
	// 阳性对照：认识的算子照常排除
	possible, err := oneCondition(ConditionEqual, []string{"1"}).MayMatchLabel(strictKey, "2")
	require.NoError(t, err)
	assert.False(t, possible)
}

// 从 PromQL 选择器转回结构化条件的两条路径只产出四种算子，转回后再渲染，匹配类型和值不变。
func TestPromMatchersRoundTripThroughConditions(t *testing.T) {
	for _, matchType := range []labels.MatchType{labels.MatchEqual, labels.MatchNotEqual, labels.MatchRegexp, labels.MatchNotRegexp} {
		for _, value := range []string{"a.b", "", "x|y"} {
			for name, toCondition := range map[string]func(labels.MatchType) string{
				"PromOperatorToConditions": PromOperatorToConditions,
				"convertOp":                convertOp,
			} {
				field := ConditionField{DimensionName: strictKey, Operator: toCondition(matchType), Value: []string{value}}
				field.ContainsToPromReg()
				got, err := field.ToPromOperator(ConditionStorageVM)
				require.NoError(t, err, name)
				assert.Equal(t, matchType, got, "%s %s %q", name, matchType, value)
				assert.Equal(t, []string{value}, field.Value, "%s %s %q", name, matchType, value)
			}
		}
	}
}

// conditionOperatorUnsupportedCount 读 unify_query_condition_operator_unsupported_total 在 operator、storage 上的值。
func conditionOperatorUnsupportedCount(t *testing.T, operator, storage string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "unify_query_condition_operator_unsupported_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			got := map[string]string{}
			for _, label := range metric.GetLabel() {
				got[label.GetName()] = label.GetValue()
			}
			if got["operator"] == operator && got["storage"] == storage {
				return metric.GetCounter().GetValue()
			}
		}
	}
	return 0
}

// strictTsDBs 是落到四种存储上的结果表：VM（带 VmRt）、InfluxDB、ES、Doris（BkSql）。
func strictTsDBs() map[string]*uqQuery.TsDBV2 {
	return map[string]*uqQuery.TsDBV2{
		md.VictoriaMetricsStorageType: {
			TableID:         "result_table.vm",
			StorageID:       "2",
			StorageType:     md.VictoriaMetricsStorageType,
			VmRt:            "2_bcs_prom_computation_result_table",
			MeasurementType: redis.BkSplitMeasurement,
			DataLabel:       "vm",
		},
		md.InfluxDBStorageType: {
			TableID:         "result_table.influxdb",
			StorageID:       "1",
			StorageType:     md.InfluxDBStorageType,
			DB:              "result_table",
			Measurement:     "influxdb",
			ClusterName:     "default",
			MeasurementType: redis.BkSplitMeasurement,
			DataLabel:       "influxdb",
		},
		md.ElasticsearchStorageType: {
			TableID:     "result_table.es",
			StorageID:   "3",
			StorageType: md.ElasticsearchStorageType,
			DB:          "es_index",
			Measurement: "__default__",
			DataLabel:   "es",
		},
		md.BkSqlStorageType: {
			TableID:     "result_table.doris",
			StorageID:   "4",
			StorageType: md.BkSqlStorageType,
			StorageName: "doris_default",
			ClusterName: "doris_default",
			DB:          "bkbase_table",
			Measurement: "doris",
			DataLabel:   "doris",
		},
	}
}

func strictQuery(storage, operator string) *Query {
	query := &Query{
		TableID:       TableID(strictTsDBs()[storage].TableID),
		FieldName:     "kube_pod_info",
		ReferenceName: "a",
		Conditions: Conditions{
			FieldList:     []ConditionField{{DimensionName: strictKey, Operator: operator, Value: []string{"1"}}},
			ConditionList: []string{},
		},
	}
	if storage == md.ElasticsearchStorageType || storage == md.BkSqlStorageType {
		query.DataSource = BkLog
		query.FieldName = "dtEventTimeStamp"
	}
	return query
}

func strictContext() context.Context {
	ctx := md.InitHashID(context.Background())
	start := time.Unix(1500, 0)
	md.GetQueryParams(ctx).SetTime(start, start, time.Unix(2500, 0), time.Minute, "s", "UTC")
	return ctx
}

// 查询落到 VM、InfluxDB 时，比较算子整条报具名错误并计数一次；落到 ES、Doris 时照旧生成查询、不计数。
func TestComparisonOperatorsFailOnlyOnVMAndInfluxDB(t *testing.T) {
	mock.Init()
	for _, operator := range []string{ConditionGt, ConditionGte, ConditionLt, ConditionLte} {
		for _, storage := range []string{md.VictoriaMetricsStorageType, md.InfluxDBStorageType} {
			t.Run(operator+" on "+storage, func(t *testing.T) {
				before := conditionOperatorUnsupportedCount(t, operator, storage)
				metric, err := strictQuery(storage, operator).ToQueryMetric(strictContext(), influxdb.SpaceUid, TsDBs{strictTsDBs()[storage]})
				requireUnsupported(t, err, operator, storage)
				assert.Nil(t, metric)
				assert.Equal(t, before+1, conditionOperatorUnsupportedCount(t, operator, storage))
			})
		}
		for _, storage := range []string{md.ElasticsearchStorageType, md.BkSqlStorageType} {
			t.Run(operator+" on "+storage, func(t *testing.T) {
				before := totalConditionOperatorUnsupported(t)
				metric, err := strictQuery(storage, operator).ToQueryMetric(strictContext(), influxdb.SpaceUid, TsDBs{strictTsDBs()[storage]})
				require.NoError(t, err)
				require.Len(t, metric.QueryList, 1)
				assert.Equal(t, storage, metric.QueryList[0].StorageType)
				assert.Equal(t, md.AllConditions{{{DimensionName: strictKey, Operator: operator, Value: []string{"1"}}}}, metric.QueryList[0].AllConditions)
				assert.Equal(t, before, totalConditionOperatorUnsupported(t))
			})
		}
	}
}

// 阳性对照：同一张 VM 表上认识的算子照常生成查询，existed 渲染进选择器。
func TestKnownOperatorsStillBuildVMQueries(t *testing.T) {
	mock.Init()
	for operator, want := range map[string]string{
		ConditionEqual:   `k="1"`,
		ConditionExisted: `k!=""`,
	} {
		metric, err := strictQuery(md.VictoriaMetricsStorageType, operator).ToQueryMetric(strictContext(), influxdb.SpaceUid, TsDBs{strictTsDBs()[md.VictoriaMetricsStorageType]})
		require.NoError(t, err, operator)
		require.Len(t, metric.QueryList, 1)
		assert.Equal(t, md.VictoriaMetricsStorageType, metric.QueryList[0].StorageType)
		assert.True(t, strings.HasPrefix(string(metric.QueryList[0].VmCondition), want+", "), "%s: %s", operator, metric.QueryList[0].VmCondition)
	}
}

func totalConditionOperatorUnsupported(t *testing.T) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	total := 0.0
	for _, family := range families {
		if family.GetName() == "unify_query_condition_operator_unsupported_total" {
			for _, metric := range family.GetMetric() {
				total += metric.GetCounter().GetValue()
			}
		}
	}
	return total
}

// struct_to_promql 和 check 走 ToPromQL：比较算子报错并按 promql 计数。
func TestComparisonOperatorsFailInStructToPromQL(t *testing.T) {
	mock.Init()
	before := conditionOperatorUnsupportedCount(t, ConditionGt, ConditionStoragePromQL)
	queryTs := &QueryTs{QueryList: []*Query{strictQuery(md.VictoriaMetricsStorageType, ConditionGt)}, MetricMerge: "a"}
	_, err := queryTs.ToPromQL(strictContext())
	requireUnsupported(t, err, ConditionGt, ConditionStoragePromQL)
	assert.Equal(t, before+1, conditionOperatorUnsupportedCount(t, ConditionGt, ConditionStoragePromQL))

	// 阳性对照
	queryTs = &QueryTs{QueryList: []*Query{strictQuery(md.VictoriaMetricsStorageType, ConditionNotExisted)}, MetricMerge: "a"}
	promQL, err := queryTs.ToPromQL(strictContext())
	require.NoError(t, err)
	assert.Contains(t, promQL, `k=""`)
}

// 计数的 operator 标签只有固定取值，原始算子不进标签。
func TestUnsupportedOperatorCountKeepsLabelsBounded(t *testing.T) {
	mock.Init()
	for _, operator := range []string{"like", "EQ", "", "x=\"1\"; drop"} {
		_, err := strictQuery(md.VictoriaMetricsStorageType, operator).ToQueryMetric(strictContext(), influxdb.SpaceUid, TsDBs{strictTsDBs()[md.VictoriaMetricsStorageType]})
		requireUnsupported(t, err, operator, md.VictoriaMetricsStorageType)
	}
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	operators := map[string]struct{}{}
	for _, family := range families {
		if family.GetName() != "unify_query_condition_operator_unsupported_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				switch label.GetName() {
				case "operator":
					operators[label.GetValue()] = struct{}{}
				case "storage":
					assert.Contains(t, []string{ConditionStorageVM, ConditionStorageInfluxDB, ConditionStoragePromQL}, label.GetValue())
				}
			}
		}
	}
	got := make([]string, 0, len(operators))
	for operator := range operators {
		got = append(got, operator)
	}
	sort.Strings(got)
	require.Contains(t, got, "other")
	for _, operator := range got {
		assert.Contains(t, []string{"gt", "gte", "lt", "lte", "other"}, operator)
	}
}
