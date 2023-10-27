// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/mocktest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/infos"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// fakeData
func fakeData(t *testing.T) (*gomock.Controller, *gostub.Stubs) {
	log.InitTestLogger()
	ctrl := gomock.NewController(t)
	// 制造一个返回假数据的influxdb client
	mockClient := mocktest.NewMockClient(ctrl)

	bizID := 2
	metric := "metric"
	fakeTable := map[consul.DataID]*consul.TableID{
		consul.DataID(1): {
			DB: "db1", Measurement: metric, IsSplitMeasurement: true,
		},
		consul.DataID(2): {
			DB: "db2", Measurement: metric, IsSplitMeasurement: true,
		},
	}
	tsDBRouter := influxdb.GetTsDBRouter()
	bizRouter := influxdb.GetBizRouter()
	metricRouter := influxdb.GetMetricRouter()
	for dataID, tableID := range fakeTable {
		tsDBRouter.AddTables(dataID, []*consul.TableID{tableID})
		bizRouter.AddRouter(bizID, dataID)
		metricRouter.AddRouter(metric, dataID)
	}

	mockResponse := map[string]map[string]*decoder.Response{
		`show tag keys`: {
			`db1`: {
				Results: []decoder.Result{
					{
						Series: []*decoder.Row{
							{
								Name:    "metric",
								Columns: []string{"tagKey"},
								Tags:    nil,
								Values: [][]interface{}{
									{`tagKey_db1_1`},
									{`tagKey_db1_2`},
								},
							},
						},
					},
				},
			},
			`db2`: {
				Results: []decoder.Result{
					{
						Series: []*decoder.Row{
							{
								Name:    "metric",
								Columns: []string{"tagKey"},
								Tags:    nil,
								Values: [][]interface{}{
									{`tagKey_db2_1`},
								},
							},
						},
					},
				},
			},
		},
		`show tag values`: {
			`db1`: {
				Results: []decoder.Result{
					{Series: []*decoder.Row{
						{
							Name:    "metric",
							Columns: []string{"key", "value"},
							Tags:    nil,
							Values: [][]interface{}{
								{`tagKey_db1_1`, `tagValue_db1_1`},
								{`tagKey_db1_2`, `tagValue_db1_2`},
							},
						},
					}},
				},
			},
			`db2`: {
				Results: []decoder.Result{
					{Series: []*decoder.Row{
						{
							Name:    "metric",
							Columns: []string{"key", "value"},
							Tags:    nil,
							Values: [][]interface{}{
								{`tagKey_db2_1`, `tagValue_db2_1`},
								{`tagKey_db2_1`, `tagValue_db2_2`},
							},
						},
					}},
				},
			},
		},
		`show field keys`: {
			`db1`: {
				Results: []decoder.Result{
					{Series: []*decoder.Row{
						{
							Name:    "metric",
							Columns: []string{"fieldKey", "fieldType"},
							Tags:    nil,
							Values: [][]interface{}{
								{`metric_value`, `float`},
								{`value`, `float`},
							},
						},
					}},
				},
			},
			`db2`: {
				Results: []decoder.Result{
					{Series: []*decoder.Row{
						{
							Name:    "metric",
							Columns: []string{"fieldKey", "fieldType"},
							Tags:    nil,
							Values: [][]interface{}{
								{`value`, `float`},
							},
						},
					}},
				},
			},
		},
		`show series`: {
			`db1`: {
				Results: []decoder.Result{
					{
						Series: []*decoder.Row{
							{
								Name:    "",
								Columns: []string{"key"},
								Tags:    nil,
								Values: [][]interface{}{
									{`metric,tagKey_db1_1=tagValue_db1_1,tagKey_db1_2=tagValue_db1_2`},
									{`metric,tagKey_db1_1=tagValue_db1_1,tagKey_db1_2=`},
									{`metric,tagKey_db1_1=,tagKey_db1_2=tagValue_db1_2`},
								},
							},
						},
					},
				},
			},
			`db2`: {
				Results: []decoder.Result{
					{
						Series: []*decoder.Row{
							{
								Name:    "",
								Columns: []string{"key"},
								Tags:    nil,
								Values: [][]interface{}{
									{`metric,tagKey_db2_1=tagValue_db2_1`},
									{`metric,tagKey_db2_1=tagValue_db2_2`},
								},
							},
						},
					},
				},
			},
		},
	}
	mockClient.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, db, sql, precision, contentType string, chunked bool) (*decoder.Response, error) {
			for s, r := range mockResponse {
				if strings.Contains(sql, s) {
					if resp, ok := r[db]; ok {
						return resp, nil
					}
				}
			}
			return nil, fmt.Errorf("info_test.go[195] db: %s is no in data: %d", db, len(mockResponse))
		}).AnyTimes()
	stubs := gostub.New()

	_ = influxdb.InitGlobalInstance(context.Background(), &influxdb.Params{
		Timeout: 30 * time.Second,
	}, mockClient)

	return ctrl, stubs
}

// TestHandleTsQueryInfosRequest
func TestHandleTsQueryInfosRequest(t *testing.T) {
	ctx := context.Background()

	ctrl, stubs := fakeData(t)
	defer stubs.Reset()
	defer ctrl.Finish()

	params := &infos.Params{
		Metric:  "metric",
		TableID: "",
		Keys: []string{
			"tagKey_db1_1",
			"tagKey_db1_2",
			"tagKey_db2_1",
		},
		Conditions: structured.Conditions{
			FieldList: []structured.ConditionField{
				{
					DimensionName: structured.BizID,
					Value:         []string{"2"},
					Operator:      structured.ConditionEqual,
				},
			},
		},
		Limit: 10000,
		Start: "1654478773",
		End:   "1654482373",
	}

	testCases := map[infos.InfoType]struct {
		expected interface{}
	}{
		infos.TagKeys: {
			expected: []string{
				"tagKey_db1_1",
				"tagKey_db1_2",
				"tagKey_db2_1",
			},
		},
		infos.TagValues: {
			expected: &TagValuesData{Values: map[string][]string{
				"tagKey_db1_1": {
					"tagValue_db1_1",
				},
				"tagKey_db1_2": {
					"tagValue_db1_2",
				},
				"tagKey_db2_1": {
					"tagValue_db2_1",
					"tagValue_db2_2",
				},
			}},
		},
		infos.FieldKeys: {
			expected: []string{
				"metric_value",
				"value",
			},
		},
		infos.Series: {
			expected: []*SeriesData{
				{
					Measurement: "metric",
					Keys: []string{
						"tagKey_db1_1",
						"tagKey_db1_2",
						"tagKey_db2_1",
					},
					Series: [][]string{
						{"tagValue_db1_1", "tagValue_db1_2", ""},
						{"tagValue_db1_1", "", ""},
						{"", "tagValue_db1_2", ""},
						{"", "", "tagValue_db2_1"},
						{"", "", "tagValue_db2_2"},
					},
				},
			},
		},
	}

	for infoType, c := range testCases {
		t.Run(string(infoType), func(t *testing.T) {
			result, err := infos.QueryAsync(ctx, infoType, params, "")
			assert.Nil(t, err)
			data, err := convertInfoData(ctx, infoType, params, result)
			assert.Nil(t, err)
			switch infoType {
			case infos.TagKeys:
				actual := data.([]string)
				sort.Strings(actual)
				assert.Equal(t, c.expected, actual)
			case infos.TagValues:
				actual := data.(*TagValuesData)
				assert.Equal(t, c.expected, actual)
			case infos.FieldKeys:
				actual := data.([]string)
				sort.Strings(actual)
				assert.Equal(t, c.expected, actual)
			case infos.Series:
				actual := data.([]*SeriesData)
				expected := c.expected.([]*SeriesData)
				assert.Equal(t, len(expected), len(actual))
				for i, e := range expected {
					item := actual[i]
					assert.Equal(t, e.Measurement, item.Measurement)
					sort.Slice(item.Series, func(i, j int) bool {
						return strings.Join(item.Series[i], "|") < strings.Join(item.Series[j], "|")
					})
					assert.Equal(t, e, item)
				}
			default:
				panic(fmt.Errorf("error: %s", infoType))
			}
		})
	}
}
