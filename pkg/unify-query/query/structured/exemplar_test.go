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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
)

// Struct2Sql
type Struct2Sql struct {
	Params *CombinedQueryParams
	Sqls   []influxdb.SQLInfo
	Err    string
}

// TestMakeInfluxdbQueryByStruct
func TestMakeInfluxdbQueryByStruct(t *testing.T) {
	cases := map[string]Struct2Sql{
		"a1": {
			Params: &CombinedQueryParams{
				QueryList: []*QueryParams{
					{
						TableID: "db1",
						Start:   "1638877723",
						End:     "1638877923",
					},
				},
			},
			Err: "invalid database or measurement",
		},
		"a2": {
			Params: &CombinedQueryParams{
				QueryList: []*QueryParams{
					{
						TableID:   "db1.measurement1",
						Start:     "",
						End:       "1638877923",
						FieldList: []FieldName{"used"},
					},
				},
			},
			Err: "invalid start timestamp",
		},
		"a3": {
			Params: &CombinedQueryParams{
				QueryList: []*QueryParams{
					{
						TableID:   "db1.measurement1",
						Start:     "1638877923",
						End:       "",
						FieldList: []FieldName{"used"},
					},
				},
			},
			Err: "invalid end timestamp",
		},
		"a4": {
			Params: &CombinedQueryParams{
				QueryList: []*QueryParams{
					{
						TableID: "db1.measurement1",
						Start:   "1638877923",
						End:     "1638877933",
					},
				},
			},
			Err: "empty filed name",
		},
		"a5": {
			Params: &CombinedQueryParams{
				QueryList: []*QueryParams{
					{
						TableID:   "db1.measurement1",
						FieldList: []FieldName{"used1"},
						Start:     "1638877723",
						End:       "1638877923",
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "tag1",
									Value:         []string{"dd"},
									Operator:      "ne",
								},
								{
									DimensionName: "tag2",
									Value:         []string{"abcd"},
									Operator:      "eq",
								},
								{
									DimensionName: "tag3",
									Value:         []string{"1234"},
									Operator:      "eq",
								},
							},
							ConditionList: []string{"and"},
						},
					},
				},
			},
			Err: "invalid condition list",
		},
		"a6": {
			Params: &CombinedQueryParams{
				QueryList: []*QueryParams{
					{
						TableID:   "db1.measurement1",
						FieldList: []FieldName{"used1"},
						Start:     "1638877723",
						End:       "1638877923",
						Limit:     3000,
					},
				},
			},
			Sqls: []influxdb.SQLInfo{
				{
					DB:  "db1",
					SQL: `select used1::field, time as _time from measurement1 where time >= 1638877723000000000 and time < 1638877923000000000 and (bk_span_id != "" or bk_trace_id != "") limit 1024`,
				},
			},
		},
		"a7": {
			Params: &CombinedQueryParams{
				QueryList: []*QueryParams{
					{
						TableID:     "db1.measurement1",
						FieldList:   []FieldName{"used1"},
						Start:       "1638877723",
						End:         "1638877923",
						KeepColumns: []string{"tagA", "tagB", "tagC"},
						Limit:       3000,
					},
				},
			},
			Sqls: []influxdb.SQLInfo{
				{
					DB:  "db1",
					SQL: `select used1::field, tagA::tag, tagB::tag, tagC::tag, time as _time from measurement1 where time >= 1638877723000000000 and time < 1638877923000000000 and (bk_span_id != "" or bk_trace_id != "") limit 1024`,
				},
			},
		},
		"a8": {
			Params: &CombinedQueryParams{
				QueryList: []*QueryParams{
					{
						TableID:     "db1.measurement1",
						FieldList:   []FieldName{"used1"},
						Start:       "1638877723",
						End:         "1638877923",
						KeepColumns: []string{"tagA", "tagB", "tagC"},
						Limit:       3000,
					},
					{
						TableID:     "db2.measurement2",
						FieldList:   []FieldName{"used2"},
						Start:       "1638877723",
						End:         "1638877923",
						KeepColumns: []string{"tagA", "tagB"},
						Limit:       3000,
					},
				},
			},
			Sqls: []influxdb.SQLInfo{
				{
					DB:  "db1",
					SQL: `select used1::field, tagA::tag, tagB::tag, tagC::tag, time as _time from measurement1 where time >= 1638877723000000000 and time < 1638877923000000000 and (bk_span_id != "" or bk_trace_id != "") limit 1024`,
				},
				{
					DB:  "db2",
					SQL: `select used2::field, tagA::tag, tagB::tag, time as _time from measurement2 where time >= 1638877723000000000 and time < 1638877923000000000 and (bk_span_id != "" or bk_trace_id != "") limit 1024`,
				},
			},
		},
		"a9": {
			Params: &CombinedQueryParams{
				QueryList: []*QueryParams{
					{
						TableID:   "db1.measurement1",
						FieldList: []FieldName{"used1"},
						Start:     "1638877723",
						End:       "1638877923",
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "tag1",
									Value:         []string{"dd"},
									Operator:      "ne",
								},
							},
						},
						Limit: 2500,
					},
				},
			},
			Sqls: []influxdb.SQLInfo{
				{
					DB:  "db1",
					SQL: `select used1::field, time as _time from measurement1 where tag1 != 'dd' and time >= 1638877723000000000 and time < 1638877923000000000 and (bk_span_id != "" or bk_trace_id != "") limit 1024`,
				},
			},
		},
		"a10": {
			Params: &CombinedQueryParams{
				QueryList: []*QueryParams{
					{
						TableID:   "db1.measurement1",
						FieldList: []FieldName{"used1"},
						Start:     "1638877723",
						End:       "1638877923",
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "tag1",
									Value:         []string{"dd"},
									Operator:      "req",
								},
								{
									DimensionName: "tag2",
									Value:         []string{"xx"},
									Operator:      "nreq",
								},
								{
									DimensionName: "tag3",
									Value:         []string{"zz"},
									Operator:      "contains",
								},
								{
									DimensionName: "tag4",
									Value:         []string{"kk"},
									Operator:      "ncontains",
								},
							},
							ConditionList: []string{"and", "and", "and"},
						},
						Limit: 2500,
					},
				},
			},
			Sqls: []influxdb.SQLInfo{
				{
					DB:  "db1",
					SQL: `select used1::field, time as _time from measurement1 where tag1 =~ /dd/ and tag2 !~ /xx/ and tag3 = 'zz' and tag4 != 'kk' and time >= 1638877723000000000 and time < 1638877923000000000 and (bk_span_id != "" or bk_trace_id != "") limit 1024`,
				},
			},
		},
		"a11": {
			Params: &CombinedQueryParams{
				QueryList: []*QueryParams{
					{
						TableID:   "db.measurement1",
						FieldList: []FieldName{"used1", "used2"},
						Start:     "1638877723",
						End:       "1638877923",
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "tag1",
									Value:         []string{"dd"},
									Operator:      "ne",
								},
								{
									DimensionName: "tag2",
									Value:         []string{"abcd"},
									Operator:      "eq",
								},
							},
							ConditionList: []string{"and"},
						},
						Limit: 10,
					},
				},
			},
			Sqls: []influxdb.SQLInfo{
				{
					DB:  "db",
					SQL: `select used1::field, used2::field, time as _time from measurement1 where tag1 != 'dd' and tag2 = 'abcd' and time >= 1638877723000000000 and time < 1638877923000000000 and (bk_span_id != "" or bk_trace_id != "") limit 10`,
				},
			},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := MakeInfluxdbQueryByStruct(c.Params, func() int64 { return 1024 }, func() bool { return false })
			if c.Err != "" {
				assert.True(t, strings.HasPrefix(err.Error(), c.Err))
			}
			assert.Equal(t, c.Sqls, s)
		})

	}
}
