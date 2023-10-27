// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// TestPromQuery
func TestPromQuery(t *testing.T) {
	log.InitTestLogger()

	var testTime string = "1616554524"
	now, _ := strconv.ParseInt(testTime, 10, 64)
	testCases := []struct {
		query  structured.QueryParams
		result string
	}{{
		query: structured.QueryParams{
			TableID: "db1.test1",
			Start:   strconv.FormatInt((time.Unix(now, 0).Add(-20 * time.Second).Unix()), 10),
			End:     strconv.FormatInt((time.Unix(now, 0).Add(20 * time.Second).Unix()), 10),
			Conditions: structured.Conditions{
				FieldList: []structured.ConditionField{
					{
						DimensionName: "tag1",
						Value:         []string{"abcd"},
						Operator:      "eq",
					},
					{
						DimensionName: "tag1",
						Value:         []string{"dd"},
						Operator:      "ne",
					},
					{
						DimensionName: "tag1",
						Value:         []string{"dd", "vb"},
						Operator:      "contains",
					},
				},
				ConditionList: []string{"and", "or"},
			},
			AggregateMethodList: []structured.AggregateMethod{{
				Method:     "mean",
				ArgsList:   structured.Args{},
				VArgsList:  []interface{}{2},
				Dimensions: []string{"tagA", "tagB"},
			}},
			TimeAggregation: structured.TimeAggregation{
				Function: "avg_over_time",
				Window:   "5m",
			},
			FieldName:     "testa",
			ReferenceName: "fie_test",
			Dimensions:    []string{"tagA", "tagB"},
			Window:        "5m",
		},
		result: ``},
	}

	for index, testCase := range testCases {
		options, _ := structured.GenerateOptions(nil, true, nil, "")
		result, err := testCase.query.ToProm(context.Background(), options)
		assert.Nil(t, err, index)
		fmt.Println(result.GetExpr().String())
		// data := ast.Format(result)
		// assert.Equal(t, testCase.result, data, index)

	}
}

// TestCombinedQueryProm
func TestCombinedQueryProm(t *testing.T) {
	log.InitTestLogger()
	var testTime string = "1616554524"
	now, _ := strconv.ParseInt(testTime, 10, 64)
	testCases := []struct {
		query  structured.CombinedQueryParams
		result string
	}{
		{
			query: structured.CombinedQueryParams{
				OrderBy:     []string{"_time"},
				MetricMerge: "t2 - on(tag1, tag2) group_right() t1",
				QueryList: []*structured.QueryParams{
					{
						TableID:       "db1.test1",
						FieldName:     "value1",
						ReferenceName: "t1",
						Limit:         500,
						Window:        "1m",
						AggregateMethodList: []structured.AggregateMethod{{
							Method:     "mean",
							ArgsList:   structured.Args{},
							Dimensions: []string{"tag1", "tag2"},
						},
						},
						TimeAggregation: structured.TimeAggregation{
							Function: "avg_over_time",
							Window:   "1m",
						},
						Start: strconv.FormatInt((time.Unix(now, 0).Add(-20 * time.Second).Unix()), 10),
						End:   strconv.FormatInt((time.Unix(now, 0).Add(20 * time.Second).Unix()), 10),

						Dimensions: []string{"tag1", "tag2"},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "tag1",
									Value:         []string{"abcd"},
									Operator:      "eq",
								},
								{
									DimensionName: "tag1",
									Value:         []string{"dd"},
									Operator:      "ne",
								},
							},
							ConditionList: []string{"and"},
						},
					},
					{
						TableID:       "db1.test1",
						FieldName:     "value1",
						ReferenceName: "t2",
						Limit:         500,
						Window:        "1m",
						AggregateMethodList: []structured.AggregateMethod{{
							Method:     "mean",
							ArgsList:   structured.Args{},
							Dimensions: []string{"tag1", "tag2"},
							VArgsList:  []interface{}{1},
						},
						},
						TimeAggregation: structured.TimeAggregation{
							Function: "avg_over_time",
							Window:   "1m",
						},
						Start: strconv.FormatInt((time.Unix(now, 0).Add(-20 * time.Second).Unix()), 10),
						End:   strconv.FormatInt((time.Unix(now, 0).Add(20 * time.Second).Unix()), 10),

						Dimensions: []string{"tag1", "tag2"},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "tag1",
									Value:         []string{"abcd"},
									Operator:      "eq",
								},
								{
									DimensionName: "tag1",
									Value:         []string{"dd"},
									Operator:      "ne",
								},
							},
							ConditionList: []string{"and"},
						},
					},
				},
			},
			result: "avg by (tag1, tag2) (avg_over_time(t2{tag1!=\"dd\",tag1=\"abcd\"}[1m])) - on (tag1, tag2) group_right () avg by (tag1, tag2) (avg_over_time(t1{tag1!=\"dd\",tag1=\"abcd\"}[1m]))",
		},
		{
			query: structured.CombinedQueryParams{
				OrderBy:     []string{"_time"},
				MetricMerge: "t2",
				QueryList: []*structured.QueryParams{
					{
						TableID:       "db1.test1",
						FieldName:     "value1",
						ReferenceName: "t2",
						Limit:         500,
						Window:        "2m",
						AggregateMethodList: []structured.AggregateMethod{{
							Method:     "mean",
							ArgsList:   structured.Args{},
							Dimensions: []string{"tag1", "tag2"},
						},
						},
						TimeAggregation: structured.TimeAggregation{
							Function: "avg_over_time",
							Window:   "2m",
						},
						Start: strconv.FormatInt((time.Unix(now, 0).Add(-20 * time.Second).Unix()), 10),
						End:   strconv.FormatInt((time.Unix(now, 0).Add(20 * time.Second).Unix()), 10),

						Dimensions: []string{"tag1", "tag2"},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "tag1",
									Value:         []string{"abcd"},
									Operator:      "eq",
								},
								{
									DimensionName: "tag1",
									Value:         []string{"dd"},
									Operator:      "ne",
								},
							},
							ConditionList: []string{"and"},
						},
					},
				},
			},
			result: `avg by (tag1, tag2) (avg_over_time(t2{tag1!="dd",tag1="abcd"}[2m]))`,
		},
	}

	for index, testCase := range testCases {
		options, _ := structured.GenerateOptions(nil, false, nil, "")
		result, err := testCase.query.ToProm(context.Background(), options)
		assert.Nil(t, err, "round->[%d] must not err exists", index)
		assert.Equal(t, testCase.result, result.GetExpr().String(), "round->[%d] string result match", index)
	}
}

// TestQueryPromByStmt
func TestQueryPromByStmt(t *testing.T) {
	log.InitTestLogger()
	data := `{"query_list":[{"table_id":"system.disk","field_name":"used","function":[{"method":"mean","dimensions":["bk_target_ip","bk_target_cloud_id"]}],"time_aggregation":{"function":"avg_over_time","window":"1m"},"reference_name":"t1","dimensions":["bk_target_ip","bk_target_cloud_id"],"driver":"","time_field":"time","limit":500,"offset":"","slimit":0,"soffset":0,"conditions":{"field_list":[],"condition_list":[]},"start_time":"1618539717","end_time":"1618543317"},{"table_id":"system.disk","field_name":"used","function":[{"method":"mean","dimensions":["bk_target_ip","bk_target_cloud_id"]}],"time_aggregation":{"function":"avg_over_time","window":"1m"},"reference_name":"t2","dimensions":["bk_target_ip","bk_target_cloud_id"],"driver":"","time_field":"time","limit":500,"offset":"","slimit":0,"soffset":0,"conditions":{"field_list":[],"condition_list":[]},"start_time":"1618539717","end_time":"1618543317"},{"table_id":"system.disk","field_name":"total","function":[{"method":"mean","dimensions":["bk_target_ip","bk_target_cloud_id","bk_biz_id"]}],"time_aggregation":{"function":"avg_over_time","window":"1m"},"reference_name":"t3","dimensions":["bk_target_ip","bk_target_cloud_id","bk_biz_id"],"driver":"","time_field":"time","limit":500,"offset":"","slimit":0,"soffset":0,"conditions":{"field_list":[],"condition_list":[]},"start_time":"1618539717","end_time":"1618543317"}],"metric_merge":"t2 - t1 + t3 * 1 ","order_by":["_time"],"start_time":"1622010798","end_time":"1622011798","window":"59s"}`
	query, err := structured.AnalysisQuery(data)
	assert.Nil(t, err)
	options, _ := structured.GenerateOptions(nil, false, nil, "")
	_, stmt, err := structured.QueryProm(context.Background(), query, options)
	assert.Nil(t, err)
	assert.NotNil(t, query)
	assert.Equal(t, "avg by (bk_target_ip, bk_target_cloud_id) (avg_over_time(t2[1m])) - avg by (bk_target_ip, bk_target_cloud_id) (avg_over_time(t1[1m])) + avg by (bk_target_ip, bk_target_cloud_id, bk_biz_id) (avg_over_time(t3[1m])) * 1", stmt)

}
