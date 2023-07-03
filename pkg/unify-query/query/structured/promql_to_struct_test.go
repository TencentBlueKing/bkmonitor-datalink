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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// TestParser
func TestParser(t *testing.T) {
	log.InitTestLogger()
	stmt := `(100 - (sum(rate(a[1m]))/sum(rate(a[1m]))) * 100) OR on() vector(100)`

	parser := NewStructParser(stmt)
	_, err := parser.ParseNew()
	assert.Nil(t, err)
}

func TestNewStructParser(t *testing.T) {
	log.InitTestLogger()
	testCases := map[string]struct {
		q string
		r string
	}{
		"test unary expr": {
			q: `-sum(rate(metric_good_1{label="value"}[1m] @end()))`,
			r: `-sum(rate(metric_good_1{label="value"}[1m] @ end()))`,
		},
		"test @ modifier range-vector": {
			q: `sum(rate(metric_good_1{label="value"}[1m] @end()))`,
			r: `sum(rate(metric_good_1{label="value"}[1m] @ end()))`,
		},
		"test @ modifier vector": {
			q: `topk(3, metric_good_1 @1609746000)`,
			r: `topk(3, metric_good_1 @ 1609746000.000)`,
		},
		"test chinese and jisuan": {
			q: `topk(10, ((sum(bkmonitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_total{world_name="Dev \u670d"}) by (proto_name) - sum(bkmonitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_total{world_name="Dev \u670d"} offset 5m) by (proto_name)) / sum(bkmonitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_role_num_5m{world_name="Dev \u670d"}) by (proto_name)))`,
			r: `topk(10, ((sum by (proto_name) (bkmonitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_total{world_name="Dev 服"}) - sum by (proto_name) (bkmonitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_total{world_name="Dev 服"} offset 5m)) / sum by (proto_name) (bkmonitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_role_num_5m{world_name="Dev 服"})))`,
		},
		"test chinese": {
			q: `sum(count_over_time(bk_monitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_total{world_name=~"Dev 服"}[1m]))`,
			r: `sum(count_over_time(bkmonitor:pushgateway_nba2_exporter:default_group:trade_proto_sold_total{world_name=~"Dev 服"}[1m]))`,
		},
		"test chinese with count sum": {
			q: `1 / count(metric_good_1{world_name="Dev 中文验证"} == 1) + sum(metric_bad_2222{a=~"你瞅啥??"})`,
			r: `1 / count(metric_good_1{world_name="Dev 中文验证"} == 1) + sum(metric_bad_2222{a=~"你瞅啥??"})`,
		},
		"group right and vector": {
			q: `(100 - (sum(rate(a[1m])) / on(ip) group_right() sum(rate(a[1m]))) * 100) OR on() vector(100)`,
			r: `(100 - (sum(rate(a[1m])) / on (ip) group_right () sum(rate(a[1m]))) * 100) or on () vector(100)`,
		},
		"on vector(0)": {
			q: `(100 - (sum(rate(a[1m]))/sum(rate(a[1m]))) * 100) OR on() vector(100)`,
			r: `(100 - (sum(rate(a[1m])) / sum(rate(a[1m]))) * 100) or on () vector(100)`,
		},
		"group": {
			q: `group(container_cpu_load_average_10s)`,
			r: `group(container_cpu_load_average_10s)`,
		},
		"std var": {
			q: `stdvar(container_cpu_load_average_10s{tag="2"}) by (pod)`,
			r: `stdvar by (pod) (container_cpu_load_average_10s{tag="2"})`,
		},
		"std dev": {
			q: `stddev(container_cpu_load_average_10s{tag!="2"}) without (pod)`,
			r: `stddev without (pod) (container_cpu_load_average_10s{tag!="2"})`,
		},
		"topk": {
			q: `topk(5, container_cpu_load_average_10s) by (tag)`,
			r: `topk by (tag) (5, container_cpu_load_average_10s)`,
		},
		"count values": {
			q: `count_values("pod", container_cpu_load_average_10s) by (tag)`,
			r: `count_values by (tag) ("pod", container_cpu_load_average_10s)`,
		},
		"avg without": {
			q: `avg(container_cpu_load_average_10s{container=~"alertmanager"}) without (condition)`,
			r: `avg without (condition) (container_cpu_load_average_10s{container=~"alertmanager"})`,
		},
		"avg by and without": {
			q: `sum(avg without (pod) (container_cpu_load_average_10s{container=~"alertmanager"})) by (condition)`,
			r: `sum by (condition) (avg without (pod) (container_cpu_load_average_10s{container=~"alertmanager"}))`,
		},
		"avg avg_over_time": {
			q: `avg by (tag1, tag2) (avg_over_time(bkmonitor:metric{tag!="abc"}[1m]))`,
			r: `avg by (tag1, tag2) (avg_over_time(bkmonitor:metric{tag!="abc"}[1m]))`,
		},
		"sum count_over_time": {
			q: `sum(count_over_time(bkmonitor:db:measurement:metric{tag!="abc"}[1m])) by (tag1, tag2)`,
			r: `sum by (tag1, tag2) (count_over_time(bkmonitor:db:measurement:metric{tag!="abc"}[1m]))`,
		},
		"many func": {
			q: `sum(label_join(round(quantile_over_time(0.9, container_cpu_load_average_10s[1m]), 100), "pod1", "pod2", "pod3")) by (pod1, pod2) + histogram_quantile(0.5, count(irate(container_cpu_load_average_10s[1m])) by (pod1, pod2))`,
			r: `sum by (pod1, pod2) (label_join(round(quantile_over_time(0.9, container_cpu_load_average_10s[1m]), 100), "pod1", "pod2", "pod3")) + histogram_quantile(0.5, count by (pod1, pod2) (irate(container_cpu_load_average_10s[1m])))`,
		},
		"avg rate": {
			q: `avg by (tag1, tag2) (avg_over_time(bkmonitor:metric{tag!="abc"}[15s:15s]))`,
			r: `avg by (tag1, tag2) (avg_over_time(bkmonitor:metric{tag!="abc"}[15s:15s]))`,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			sp := NewStructParser(c.q)
			query, err := sp.ParseNew()
			assert.Nil(t, err)
			if err == nil {
				stmt, err1 := query.ToProm(context.Background(), &Option{
					IsRealFieldName: true,
					IsOnlyParse:     true,
				})
				assert.Nil(t, err1)
				promStmt := stmt.GetExpr().String()
				assert.Equal(t, c.r, promStmt)
			}
		})
	}
}

// TestStructParser_WithSymbol
func TestStructParser_WithSymbol(t *testing.T) {
	log.InitTestLogger()
	stmt := `max(avg(avg_over_time(bkmonitor:db1:table1:metric1{tag1!="dd"}[2m] offset 3m))) without(tag1, tag2) > 3`
	parser := NewStructParser(stmt)
	_, err := parser.ParseNew()
	assert.Nil(t, err)
}

// TestStructParser
func TestStructParser(t *testing.T) {
	log.InitTestLogger()

	testCases := map[string]struct {
		in  string
		out CombinedQueryParams
	}{
		"a1": {
			in: `(100 - (sum(rate(msgbus_requests_fail_num{module="cluster",instance="$instance"}[1m]))/sum(rate(msgbus_requests_total{module="cluster",instance="$instance"}[1m]))) * 100) OR on() vector(100)`,
			out: CombinedQueryParams{
				MetricMerge: "(100 - (a/b) * 100) OR on() vector(100)",
				QueryList: []*QueryParams{
					{
						FieldName:     "msgbus_requests_fail_num",
						ReferenceName: "a",
						AggregateMethodList: []AggregateMethod{
							{
								Method:   "sum",
								Position: 0,
							},
						},
						TimeAggregation: TimeAggregation{
							Function: "rate",
							Window:   Window("1m0s"),
							Position: 0,
						},
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "module",
									Value:         []string{"cluster"},
									Operator:      "eq",
								},
								{
									DimensionName: "instance",
									Value:         []string{"$instance"},
									Operator:      "eq",
								},
							},
							ConditionList: []string{"and"},
						},
					},
					{
						FieldName:     "msgbus_requests_total",
						ReferenceName: "b",
						AggregateMethodList: []AggregateMethod{
							{
								Method:     "sum",
								Dimensions: nil,
							},
						},
						TimeAggregation: TimeAggregation{
							Function: "rate",
							Window:   Window("1m0s"),
						},
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "module",
									Value:         []string{"cluster"},
									Operator:      "eq",
								},
								{
									DimensionName: "instance",
									Value:         []string{"$instance"},
									Operator:      "eq",
								},
							},
							ConditionList: []string{"and"},
						},
					},
				},
			},
		},
		"a2": {
			in: `avg(bkmonitor:table1:metric1{tag1!="dd",tag1=~"abcd.*",tag3!~"cdef|aaa|nnn"}) by(tag1, tag2)`,
			out: CombinedQueryParams{
				MetricMerge: "a",
				QueryList: []*QueryParams{{
					FieldName:     "metric1",
					ReferenceName: "a",
					AggregateMethodList: []AggregateMethod{
						{
							Method:     "mean",
							Dimensions: []string{"tag1", "tag2"},
						},
					},
					Conditions: Conditions{
						FieldList: []ConditionField{
							{
								DimensionName: "tag1",
								Value:         []string{"dd"},
								Operator:      "ne",
							},
							{
								DimensionName: "tag1",
								Value:         []string{"abcd.*"},
								Operator:      "req",
							},
							{
								DimensionName: "tag3",
								Value:         []string{"cdef|aaa|nnn"},
								Operator:      "nreq",
							},
						},
						ConditionList: []string{"and", "and"},
					},
				}},
			},
		},
		"a3": {
			in: `max(avg(avg_over_time(bkmonitor:db1:table1:metric1{tag1!="dd",tag1=~"abcd.*",tag3!~"cdef|aaa|nnn"}[2m] offset 3m))) by(tag1, tag2) > 3`,
			out: CombinedQueryParams{
				MetricMerge: "a > 3",
				QueryList: []*QueryParams{{
					DataSource:    "bkmonitor",
					DB:            "db1",
					TableID:       TableID("db1.table1"),
					FieldName:     "metric1",
					ReferenceName: "a",
					Offset:        "3m0s",
					AggregateMethodList: []AggregateMethod{
						{
							Method: "mean",
						},
						{
							Method:     "max",
							Dimensions: []string{"tag1", "tag2"},
						},
					},
					TimeAggregation: TimeAggregation{
						Function: "avg_over_time",
						Window:   "2m0s",
					},
					Conditions: Conditions{
						FieldList: []ConditionField{
							{
								DimensionName: "tag1",
								Value:         []string{"dd"},
								Operator:      "ne",
							},
							{
								DimensionName: "tag1",
								Value:         []string{"abcd.*"},
								Operator:      "req",
							},
							{
								DimensionName: "tag3",
								Value:         []string{"cdef|aaa|nnn"},
								Operator:      "nreq",
							},
						},
						ConditionList: []string{"and", "and"},
					},
				}},
			},
		},
		"a4": {
			in: `topk(10,label_join(avg(avg_over_time(bkmonitor:db1:table1:metric1{tag1!="dd",tag1=~"abcd.*",tag3!~"cdef|aaa|nnn"}[2m]offset 3m))by(tag1,tag2)+max(bkmonitor:db1:table1:metric1{tag1!="dd",tag1=~"abcd.*",tag3!~"cdef|aaa|nnn"})by(tag1,tag2), "foo", ",", "bar1", "bar2"))`,
			out: CombinedQueryParams{
				MetricMerge: `topk(10,label_join(a+b, "foo", ",", "bar1", "bar2"))`,
				QueryList: []*QueryParams{
					{
						DataSource:    "bkmonitor",
						DB:            "db1",
						TableID:       TableID("db1.table1"),
						FieldName:     "metric1",
						ReferenceName: "a",
						Offset:        "3m0s",
						AggregateMethodList: []AggregateMethod{{
							Method:     "mean",
							Dimensions: []string{"tag1", "tag2"},
						}},
						TimeAggregation: TimeAggregation{
							Function: "avg_over_time",
							Window:   "2m0s",
						},
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "tag1",
									Value:         []string{"dd"},
									Operator:      "ne",
								},
								{
									DimensionName: "tag1",
									Value:         []string{"abcd.*"},
									Operator:      "req",
								},
								{
									DimensionName: "tag3",
									Value:         []string{"cdef|aaa|nnn"},
									Operator:      "nreq",
								},
							},
							ConditionList: []string{"and", "and"},
						},
					},
					{
						DataSource:    "bkmonitor",
						DB:            "db1",
						TableID:       TableID("db1.table1"),
						FieldName:     "metric1",
						ReferenceName: "b",
						AggregateMethodList: []AggregateMethod{{
							Method:     "max",
							Dimensions: []string{"tag1", "tag2"},
						}},
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "tag1",
									Value:         []string{"dd"},
									Operator:      "ne",
								},
								{
									DimensionName: "tag1",
									Value:         []string{"abcd.*"},
									Operator:      "req",
								},
								{
									DimensionName: "tag3",
									Value:         []string{"cdef|aaa|nnn"},
									Operator:      "nreq",
								},
							},
							ConditionList: []string{"and", "and"},
						},
					},
				},
			},
		},
		"a5": {
			in: `topk(5, avg_over_time(bkmonitor:db1:table1:metric1{tag1!="dd",tag1="abcd"}[2m])) by(tag1, tag2)`,
			out: CombinedQueryParams{
				MetricMerge: "a",
				QueryList: []*QueryParams{{
					DataSource:    "bkmonitor",
					DB:            "db1",
					TableID:       TableID("db1.table1"),
					FieldName:     "metric1",
					ReferenceName: "a",
					AggregateMethodList: []AggregateMethod{{
						Method:     "topk",
						Dimensions: []string{"tag1", "tag2"},
						VArgsList: []interface{}{
							5.,
						},
					}},
					TimeAggregation: TimeAggregation{
						Function: "avg_over_time",
						Window:   "2m0s",
					},
					Conditions: Conditions{
						FieldList: []ConditionField{
							{
								DimensionName: "tag1",
								Value:         []string{"dd"},
								Operator:      "ne",
							},
							{
								DimensionName: "tag1",
								Value:         []string{"abcd"},
								Operator:      "eq",
							},
						},
						ConditionList: []string{"and"},
					},
				}},
			},
		},
		"a6": {
			in: `label_join(topk(5, avg by(tag1, tag2, tag3) (bkmonitor:db1:table1:metric1 offset 2m)), "foo", "," , "tag1", "tag2", "tag3")`,
			out: CombinedQueryParams{
				MetricMerge: "a",
				QueryList: []*QueryParams{{
					DataSource:    "bkmonitor",
					DB:            "db1",
					TableID:       TableID("db1.table1"),
					FieldName:     "metric1",
					ReferenceName: "a",
					Offset:        "2m0s",
					AggregateMethodList: []AggregateMethod{
						{
							Method: "mean",
							Dimensions: []string{
								"tag1", "tag2", "tag3",
							},
						},
						{
							Method: "topk",
							VArgsList: []interface{}{
								5.,
							},
						},
						{
							Method: "label_join",
							VArgsList: []interface{}{
								"foo", ",", "tag1", "tag2", "tag3",
							},
						},
					},
					Conditions: Conditions{
						FieldList:     []ConditionField{},
						ConditionList: []string{},
					},
				}},
			},
		},
		"a7": {
			in: `avg by(tag1, tag2) (avg_over_time(bkmonitor:db2:table2:metric2{tag1!="dd",tag1="abcd"}[1m])) - on(tag1, tag2) group_right() avg by(tag1, tag2) (avg_over_time(bkmonitor:db1:table1:metric1{tag1!="dd",tag1="abcd"}[1m]))`,
			out: CombinedQueryParams{
				MetricMerge: "a - on(tag1, tag2) group_right() b",
				QueryList: []*QueryParams{
					{
						DataSource:    "bkmonitor",
						DB:            "db2",
						TableID:       TableID("db2.table2"),
						FieldName:     "metric2",
						ReferenceName: "a",
						AggregateMethodList: []AggregateMethod{{
							Method:     "mean",
							Dimensions: []string{"tag1", "tag2"},
						}},
						TimeAggregation: TimeAggregation{
							Function: "avg_over_time",
							Window:   "1m0s",
						},

						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "tag1",
									Value:         []string{"dd"},
									Operator:      "ne",
								},
								{
									DimensionName: "tag1",
									Value:         []string{"abcd"},
									Operator:      "eq",
								},
							},
							ConditionList: []string{"and"},
						},
					},
					{
						DataSource:    "bkmonitor",
						DB:            "db1",
						TableID:       TableID("db1.table1"),
						FieldName:     "metric1",
						ReferenceName: "b",
						AggregateMethodList: []AggregateMethod{{
							Method:     "mean",
							Dimensions: []string{"tag1", "tag2"},
						},
						},
						TimeAggregation: TimeAggregation{
							Function: "avg_over_time",
							Window:   "1m0s",
						},
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "tag1",
									Value:         []string{"dd"},
									Operator:      "ne",
								},
								{
									DimensionName: "tag1",
									Value:         []string{"abcd"},
									Operator:      "eq",
								},
							},
							ConditionList: []string{"and"},
						},
					},
				},
			},
		},
		"a8": {
			in: `avg(max_over_time(bkmonitor:db1:table1:metric1{foo!="bar"}[1m])) by(a,e) + (max(bkmonitor:db2:table2:metric2{foo1!="bar1"}) - max(sum_over_time(bkmonitor:db3:table3:metric3{foo="bar"}[5m])) by(b,f)) / min(bkmonitor:db4:table4:metric4{foo="bar"})`,
			out: CombinedQueryParams{
				MetricMerge: "a + (b - c) / d",
				QueryList: []*QueryParams{
					{
						DataSource:    "bkmonitor",
						DB:            "db1",
						TableID:       TableID("db1.table1"),
						FieldName:     "metric1",
						ReferenceName: "a",
						AggregateMethodList: []AggregateMethod{{
							Method:     "mean",
							Dimensions: []string{"a", "e"},
						}},
						TimeAggregation: TimeAggregation{
							Function: "max_over_time",
							Window:   "1m0s",
						},
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "foo",
									Value:         []string{"bar"},
									Operator:      "ne",
								},
							},
							ConditionList: []string{},
						},
					},
					{
						DataSource:    "bkmonitor",
						DB:            "db2",
						TableID:       TableID("db2.table2"),
						FieldName:     "metric2",
						ReferenceName: "b",
						AggregateMethodList: []AggregateMethod{{
							Method: "max",
						}},
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "foo1",
									Value:         []string{"bar1"},
									Operator:      "ne",
								},
							},
							ConditionList: []string{},
						},
					},
					{
						DataSource:    "bkmonitor",
						DB:            "db3",
						TableID:       TableID("db3.table3"),
						FieldName:     "metric3",
						ReferenceName: "c",
						AggregateMethodList: []AggregateMethod{{
							Method:     "max",
							Dimensions: []string{"b", "f"},
						}},
						TimeAggregation: TimeAggregation{
							Function: "sum_over_time",
							Window:   "5m0s",
						},
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "foo",
									Value:         []string{"bar"},
									Operator:      "eq",
								},
							},
							ConditionList: []string{},
						},
					},
					{
						DataSource:    "bkmonitor",
						DB:            "db4",
						TableID:       TableID("db4.table4"),
						FieldName:     "metric4",
						ReferenceName: "d",
						AggregateMethodList: []AggregateMethod{{
							Method: "min",
						}},
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "foo",
									Value:         []string{"bar"},
									Operator:      "eq",
								},
							},
							ConditionList: []string{},
						},
					},
				},
			},
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			parser := NewStructParser(testCase.in)
			ret, err := parser.ParseNew()
			assert.NoError(t, err)
			assert.Equal(t, testCase.out, ret)
		})
	}
}

// TestStructToPromql
func TestStructToPromql(t *testing.T) {
	log.InitTestLogger()
	q := func() CombinedQueryParams {
		return CombinedQueryParams{
			MetricMerge: "a",
			QueryList: []*QueryParams{{
				TableID:       "table1.measurement1",
				FieldName:     "t2",
				ReferenceName: "a",
				AggregateMethodList: []AggregateMethod{{
					Method:     "mean",
					Dimensions: []string{"tag1", "tag2"},
				}},
				TimeAggregation: TimeAggregation{
					Function: "avg_over_time",
					Window:   "2m0s",
				},
				Conditions: Conditions{
					FieldList: []ConditionField{
						{
							DimensionName: "tag1",
							Value:         []string{"dd"},
							Operator:      "ne",
						},
						{
							DimensionName: "tag1",
							Value:         []string{"abcd"},
							Operator:      "eq",
						},
					},
					ConditionList: []string{"and"},
				},
			}},
		}
	}

	testCases := map[string]struct {
		params  func(CombinedQueryParams) CombinedQueryParams
		options Option
		result  string
		err     error
	}{
		"only parse": {
			params:  func(q CombinedQueryParams) CombinedQueryParams { return q },
			options: Option{IsOnlyParse: true, IsRealFieldName: true},
			result:  `avg by (tag1, tag2) (avg_over_time(bkmonitor:table1:measurement1:t2{tag1!="dd",tag1="abcd"}[2m]))`,
		},
		"query influxdb": {
			params:  func(q CombinedQueryParams) CombinedQueryParams { return q },
			options: Option{},
			result:  `avg by (tag1, tag2) (avg_over_time(a{tag1!="dd",tag1="abcd"}[2m]))`,
		},
		"query contains": {
			params: func(q CombinedQueryParams) CombinedQueryParams {
				q.QueryList[0].Conditions.FieldList[0].Operator = "contains"
				return q
			},
			options: Option{},
			result:  `avg by (tag1, tag2) (avg_over_time(a[2m]))`,
		},
		"parse contains": {
			params: func(q CombinedQueryParams) CombinedQueryParams {
				q.QueryList[0].Conditions.FieldList[0].Operator = "contains"
				return q
			},
			options: Option{IsOnlyParse: true, IsRealFieldName: true},
			result:  `avg by (tag1, tag2) (avg_over_time(bkmonitor:table1:measurement1:t2{tag1="abcd",tag1="dd"}[2m]))`,
		},
		"parse contains two": {
			params: func(q CombinedQueryParams) CombinedQueryParams {
				q.QueryList[0].Conditions.FieldList[0] = ConditionField{
					DimensionName: "tag1",
					Value:         []string{"dd", "yy"},
					Operator:      "contains",
				}
				return q
			},
			options: Option{IsOnlyParse: true, IsRealFieldName: true},
			result:  `avg by (tag1, tag2) (avg_over_time(bkmonitor:table1:measurement1:t2{tag1="abcd",tag1=~"dd|yy"}[2m]))`,
		},
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			params := testCase.params(q())
			_, stmt, err := QueryProm(context.Background(), &params, &testCase.options)
			assert.Equal(t, testCase.err, err)
			assert.Equal(t, testCase.result, stmt)
		})
	}
}
