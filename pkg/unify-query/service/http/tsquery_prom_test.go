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
	"crypto/md5"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang/mock/gomock"
	"github.com/pkg/errors"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/mocktest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// CatchSQL
func CatchSQL(_ *testing.T, ctrl *gomock.Controller, stubs *gostub.Stubs, resultSQL *[]string, align bool) (*gomock.Controller, *gostub.Stubs) {

	log.InitTestLogger()
	//ctrl := gomock.NewController(b)
	// 制造一个返回假数据的influxdb client
	mockClient := mocktest.NewMockClient(ctrl)
	mockClient.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, db, sql, precision, contentType string, chunked bool) (*decoder.Response, error) {
		*resultSQL = append(*resultSQL, sql)
		return &decoder.Response{Results: nil}, nil
	}).AnyTimes()

	_ = influxdb.InitGlobalInstance(context.Background(), &influxdb.Params{
		Timeout: 30 * time.Second,
	}, mockClient)

	stubs.Stub(&AlignInfluxdbResult, align)
	return ctrl, stubs
}

// FakePromData
func FakePromData(b *testing.T, align bool) (*gomock.Controller, *gostub.Stubs) {
	log.InitTestLogger()
	ctrl := gomock.NewController(b)
	basepath := "testfile"

	dirs, err := os.ReadDir(basepath)
	if err != nil {
		panic(err)
	}

	dataMap := make(map[string]*decoder.Response)

	for _, dir := range dirs {
		data, err := os.ReadFile(basepath + "/" + dir.Name())
		if err != nil {
			panic(err)
		}
		var resp = new(decoder.Response)
		err = json.Unmarshal(data, &resp)
		if err != nil {
			continue
		}

		// 截取指标名和表名的内容: $metric_name_$table_id，以sql命名的同时可以加载进来
		name := strings.Split(dir.Name(), ".")[0]
		dataMap[name] = resp
	}

	// 制造一个返回假数据的influxdb client
	mockClient := mocktest.NewMockClient(ctrl)
	mockClient.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, db, sql, precision, contentType string, chunked bool) (*decoder.Response, error) {
		md5Inst := md5.New()
		md5Inst.Write([]byte(sql))
		hashSql := fmt.Sprintf("%x", md5Inst.Sum([]byte("")))

		result, ok := dataMap[hashSql]
		// 由于其他单测的测试数据由另一个pr做了修改，这里暂时做个兼容
		if !ok {
			fmt.Println(len(dataMap))
			fmt.Println(sql)
			fmt.Println(hashSql)
			fmt.Println("____________________________________________________________")
			return &decoder.Response{
				Results: nil,
				Err:     fmt.Sprintf("empty response: %s", sql),
			}, nil
		}
		return result, nil
	}).AnyTimes()

	_ = influxdb.InitGlobalInstance(context.Background(), &influxdb.Params{
		Timeout: 30 * time.Second,
	}, mockClient)

	stubs := gostub.New()
	stubs.Stub(&AlignInfluxdbResult, align)

	// mock 假路由
	var tables = map[consul.DataID][]*consul.TableID{
		150001: {
			{
				DB:                 "2_bkmonitor_time_series_1500101",
				IsSplitMeasurement: true,
			},
		},
		150002: {
			{
				DB:          "system",
				Measurement: "cpu_detail",
			},
		},
		150003: {
			{
				DB:          "system",
				Measurement: "cpu_summary",
			},
		},
		1500101: {
			{
				DB:                 "2_bkmonitor_time_series_1500101",
				Measurement:        "",
				IsSplitMeasurement: true,
			},
		},
	}
	stubs.Stub(&influxdb.GetTableIDsByDataID, func(dataID consul.DataID) []*consul.TableID {
		return tables[dataID]
	})

	return ctrl, stubs
}

// TestMetricMerge
func TestMetricMerge(t *testing.T) {
	data := `{
        "query_list": [{
                "table_id": "2_bkapm_metric_apm_test_have_data.bk_apm_duration",
                "concat_name": "bk_apm_duration",
                "field_name": "bk_apm_duration",
                "function": [{
                    "method": "avg"
                }],
                "time_aggregation": {
                    "function": "avg_over_time",
                    "window": "1m0s",
                    "position": 0
                },
                "reference_name": "a",
                "conditions": {
                    "field_list": [{
                        "field_name": "bk_biz_id",
                        "value": [
                            "2"
                        ],
                        "op": "eq"
                    }]
                }
            },
            {
                "table_id": "2_bkapm_metric_apm_test_have_data.bk_apm_duration",
                "concat_name": "bk_apm_duration",
                "field_name": "bk_apm_duration",
                "function": [{
                    "method": "avg"
                }],
                "time_aggregation": {
                    "function": "avg_over_time",
                    "window": "1m0s",
                    "position": 0
                },
                "reference_name": "b",
                "conditions": {
                    "field_list": [{
                        "field_name": "bk_biz_id",
                        "value": [
                            "2"
                        ],
                        "op": "eq"
                    }]
                    }
                }
            ],
            "metric_merge": "a > 80 and b > 90 or on() vector(100)",
            "step": "1m0s",
            "start_time": "1650775300",
            "end_time": "1650781116"
    }`

	ctrl, stubs := FakePromData(t, true)
	defer ctrl.Finish()
	defer stubs.Reset()

	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	g := gin.Default()
	//g.POST("/query/ts", HandleTSQueryRequest)
	//g.POST("/query/ts/promql", HandleTsQueryPromQLDataRequest)
	g.POST("/query/ts/struct_to_promql", HandleTsQueryStructToPromQLRequest)
	//g.POST("/query/ts/promql_to_struct", HandleTsQueryPromQLToStructRequest)

	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("POST", "/query/ts/struct_to_promql", strings.NewReader(data))
	g.ServeHTTP(w1, req1)

	m := map[string]string{}
	if err := json.Unmarshal(w1.Body.Bytes(), &m); err != nil {
		panic(err)
	}
	assert.Equal(t, m["promql"], "avg(avg_over_time(bkmonitor:2_bkapm_metric_apm_test_have_data:bk_apm_duration:bk_apm_duration{bk_biz_id=\"2\"}[1m])) > 80 and avg(avg_over_time(bkmonitor:2_bkapm_metric_apm_test_have_data:bk_apm_duration:bk_apm_duration{bk_biz_id=\"2\"}[1m])) > 90 or on () vector(100)")
}

// TestPromSimple
func TestPromSimple(t *testing.T) {
	// makeData()

	data := `{
		"query_list": [{
			"table_id": "db1.table1",
			"field_name": "value1",
			"function": [{
				"dimensions": ["tag1", "tag2","tag3"],
				"method": "mean"
			},{
				"method": "label_join",
				"vargs_list": ["foo",",","tag1", "tag2","tag3"]
			}],
			"reference_name": "t1",
			"dimensions": ["tag1", "tag2","tag3"],
			"driver": "",
			"time_field": "time",
			"window": "1m",
			"limit": 500,
			"offset": "2m",
			"slimit": 0,
			"soffset": 0,
			"conditions": {
				"field_list": [],
				"condition_list": []
			}
		}],
		"metric_merge":  "t1",
		"order_by": ["_time"],
				"start_time": "1622009400",
				"end_time": "1622015342",
				"step": "1m"
	}`

	// mock掉底层请求接口
	ctrl, stubs := FakePromData(t, true)
	defer stubs.Reset()
	defer ctrl.Finish()

	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	g := gin.Default()
	g.POST("/TestPromSimple/query/ts", HandleTSQueryRequest)
	g.POST("/TestPromSimple/query/ts/promql", HandleTsQueryPromQLDataRequest)
	g.POST("/TestPromSimple/query/ts/struct_to_promql", HandleTsQueryStructToPromQLRequest)
	g.POST("/TestPromSimple/query/ts/promql_to_struct", HandleTsQueryPromQLToStructRequest)

	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("POST", "/TestPromSimple/query/ts", strings.NewReader(data))
	g.ServeHTTP(w1, req1)
	fmt.Println("POST /query/ts:", w1.Body.String())

	// struct_to_promql
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/TestPromSimple/query/ts/struct_to_promql", strings.NewReader(data))
	g.ServeHTTP(w2, req2)

	m := map[string]string{}
	if err := json.Unmarshal(w2.Body.Bytes(), &m); err != nil {
		panic(err)
	}
	assert.Equal(t, m["promql"], "label_join(avg by (tag1, tag2, tag3) (bkmonitor:db1:table1:value1 offset 2m), \"foo\", \",\", \"tag1\", \"tag2\", \"tag3\")")

	// promql_to_struct
	var req = struct {
		PromQL string `json:"promql"`
	}{
		PromQL: `avg(avg_over_time(bkmonitor:db1:table1:metric1{tag1!="dd",tag1="abcd"}[2m] offset 3m)) by(tag1, tag2)`,
	}

	bs, _ := json.Marshal(req)
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("POST", "/TestPromSimple/query/ts/promql_to_struct", strings.NewReader(string(bs)))
	g.ServeHTTP(w3, req3)

	var rsp = struct {
		Data structured.CombinedQueryParams `json:"data"`
	}{}
	err := json.Unmarshal(w3.Body.Bytes(), &rsp)
	assert.NoError(t, err)
	assert.Equal(t, structured.CombinedQueryParams{
		MetricMerge: "a",
		QueryList: []*structured.QueryParams{{
			DataSource:    "bkmonitor",
			DB:            "db1",
			TableID:       "db1.table1",
			FieldName:     "metric1",
			ReferenceName: "a",
			Offset:        "3m0s",
			AggregateMethodList: []structured.AggregateMethod{{
				Method:     "mean",
				Dimensions: []string{"tag1", "tag2"},
			}},
			TimeAggregation: structured.TimeAggregation{
				Function: "avg_over_time",
				Window:   "2m0s",
			},
			Conditions: structured.Conditions{
				FieldList: []structured.ConditionField{
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
	}, rsp.Data)

	var reqParam = struct {
		PromQL string `json:"promql"`
		Start  string `json:"start"`
		End    string `json:"end"`
		Step   string `json:"step"`
	}{
		PromQL: `avg(avg_over_time(bkmonitor:db1:table1:metric1{tag1!="dd",tag2="abcd"}[2m] offset 3m)) by(tag1, tag2)`,
		Start:  "1622009400",
		End:    "1622015342",
		Step:   "1m",
	}

	bs, _ = json.Marshal(reqParam)
	w4 := httptest.NewRecorder()
	req4 := httptest.NewRequest("POST", "/TestPromSimple/query/ts/promql", strings.NewReader(string(bs)))
	g.ServeHTTP(w4, req4)
	fmt.Println("POST /query/ts/promql:", w4.Body.String())
}

// TestPromQuery
func TestPromQuery(t *testing.T) {
	testCases := map[string]struct {
		data   string
		result string
		err    error
	}{
		"a1": {
			data:   `{"query_list":[{"table_id":"system.cpu_summary","time_aggregation":{"function":"increase","window":"2m","vargs_list":[]},"field_name":"usage","reference_name":"c","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"contains"}],"condition_list":[]},"function":[{"method":"sum"},{"vargs_list":[],"method":"floor"},{"vargs_list":[3],"method":"topk","position":1}],"offset":"","offset_forward":false,"keep_columns":["_time","c"]}],"metric_merge":"c","start_time":"1665327000","end_time":"1665327900","step":"60s","space_uid":"bkcc__2","down_sample_range":"1s"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1665327000000,355],[1665327060000,185],[1665327120000,192],[1665327180000,211],[1665327240000,268],[1665327300000,251],[1665327360000,209],[1665327420000,234],[1665327480000,286],[1665327540000,194],[1665327600000,281],[1665327660000,173],[1665327720000,400],[1665327780000,262],[1665327840000,260]]}]}`,
		},
	}

	// mock掉底层请求接口，关闭对齐
	ctrl, stubs := FakePromData(t, false)
	defer stubs.Reset()
	defer ctrl.Finish()

	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		EnableNegativeOffset: true,
		LookbackDelta:        2 * time.Minute,
	})

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			resp, err := handleTSQuery(context.Background(), testCase.data, false, nil, "")
			assert.Nil(t, err)
			if err == nil {
				result, err := json.Marshal(resp)
				assert.Equal(t, testCase.err, err)
				a := string(result)
				assert.Equal(t, testCase.result, a)
			}
		})

	}
}

// 测试对齐influxdb数据场景下的结果，该测试使用与PromQuery相同的入参，但开启了Align，导致查询语句存在offset
func TestPromQueryWithAlignInfluxdb(t *testing.T) {
	testCases := map[string]struct {
		data   string
		result string
		err    error
	}{
		"A:window 5m; B:window 1m": {
			data:   `{"query_list":[{"table_id":"system.cpu_summary","field_name":"usage","function":[{"dimensions":["bk_target_ip"],"method":"mean"}],"time_aggregation":{"function":"avg_over_time","window":"5m"},"reference_name":"A","driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"eq"}],"condition_list":[]},"keep_columns":["_time","A","bk_target_ip"],"limit":50000,"slimit":50000},{"table_id":"system.cpu_summary","field_name":"idle","function":[{"dimensions":["bk_target_ip"],"method":"mean"}],"time_aggregation":{"function":"avg_over_time","window":"1m"},"reference_name":"B","driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"eq"}],"condition_list":[]},"keep_columns":["_time","B","bk_target_ip"],"limit":50000,"slimit":50000}],"metric_merge":"A + B","order_by":["-time"],"start_time":"1665365640","end_time":"1665369240","step":"60s"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_ip"],"group_values":["127.0.0.1"],"values":[[1665365640000,9.322098751530437],[1665365700000,9.30614787162236],[1665365760000,9.306147973756742],[1665365820000,9.306148066048868],[1665365880000,9.306148131284775],[1665365940000,9.30614822386925],[1665366000000,9.49422556997883],[1665366060000,9.494225671849964],[1665366120000,9.494225741866916],[1665366180000,9.494225825572002],[1665366240000,9.494225934036828],[1665366300000,9.691300302779357],[1665366360000,9.69130038435031],[1665366420000,9.691300458826],[1665366480000,9.691300539587631],[1665366540000,9.691300616445155],[1665366600000,9.430229083684177],[1665366660000,9.430229186445262],[1665366720000,9.430229257967087],[1665366780000,9.43022934397185],[1665366840000,9.430229432241171],[1665366900000,9.39571222840889],[1665366960000,9.395712314478489],[1665367020000,9.39571238969482],[1665367080000,9.39571247045777],[1665367140000,9.395712572420244],[1665367200000,10.669523202029708],[1665367260000,10.669523279454971],[1665367320000,10.669523351283516],[1665367380000,10.66952343861199],[1665367440000,10.669523526879441],[1665367500000,9.416992449426651],[1665367560000,9.416992531613984],[1665367620000,9.416992624706959],[1665367680000,9.416992692329483],[1665367740000,9.416992785660199],[1665367800000,9.342754766512504],[1665367860000,9.342754870098176],[1665367920000,9.34275494266618],[1665367980000,9.342755026668957],[1665368040000,9.342755115677358],[1665368100000,9.284917280650042],[1665368160000,9.284917376961694],[1665368220000,9.28491746941544],[1665368280000,9.284917535786649],[1665368340000,9.28491764379658],[1665368400000,9.317353849777204],[1665368460000,9.317353924692819],[1665368520000,9.317354001963318],[1665368580000,9.317354089490873],[1665368640000,9.317354204091346],[1665368700000,10.68315763202287],[1665368760000,10.683157653914776],[1665368820000,10.683157740137998],[1665368880000,10.683157895613592],[1665368940000,10.683157899211867],[1665369000000,12.182980080392516],[1665369060000,12.182980116852349],[1665369120000,12.182980160239374],[1665369180000,12.182980224344808]]}]}`,
		},
		"A:window 1m; B:window 1m": {
			data:   `{"query_list":[{"table_id":"system.cpu_summary","field_name":"usage","function":[{"dimensions":["bk_target_ip","bk_target_cloud_id"],"method":"mean"}],"time_aggregation":{"function":"avg_over_time","window":"1m"},"reference_name":"A","dimensions":["bk_target_ip","bk_target_cloud_id"],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"eq"}],"condition_list":[]},"keep_columns":["_time","A","bk_target_ip","bk_target_cloud_id"],"limit":50000,"slimit":50000},{"table_id":"system.disk","field_name":"total","function":[{"dimensions":["bk_target_ip","bk_target_cloud_id"],"method":"mean"}],"time_aggregation":{"function":"avg_over_time","window":"1m"},"reference_name":"B","dimensions":["bk_target_ip","bk_target_cloud_id"],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"eq"}],"condition_list":[]},"keep_columns":["_time","B","bk_target_ip","bk_target_cloud_id"],"limit":50000,"slimit":50000}],"metric_merge":"A +B*10","order_by":["-time"],"start_time":"1665365640","end_time":"1665369240","step":"60s"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip"],"group_values":["0","127.0.0.1"],"values":[[1665365640000,2055421412080.3438],[1665365700000,2055421412080.47],[1665365760000,2055421412081.8572],[1665365820000,2055421412080.5522],[1665365880000,2055421412082.2922],[1665365940000,2055421412080.5374],[1665366000000,2055421412081.2603],[1665366060000,2055421412082.1274],[1665366120000,2055421412080.6475],[1665366180000,2055421412082.1619],[1665366240000,2055421412080.4524],[1665366300000,2055421412080.7363],[1665366360000,2055421412082.3254],[1665366420000,2055421412080.709],[1665366480000,2055421412083.262],[1665366540000,2055421412080.602],[1665366600000,2055421412080.5674],[1665366660000,2055421412081.8076],[1665366720000,2055421412080.9685],[1665366780000,2055421412081.8105],[1665366840000,2055421412081.1753],[1665366900000,2055421412080.7021],[1665366960000,2055421412081.735],[1665367020000,2055421412080.6091],[1665367080000,2055421412082.5269],[1665367140000,2055421412080.5837],[1665367200000,2055421412085.954],[1665367260000,2055421412082.848],[1665367320000,2055421412080.963],[1665367380000,2055421412081.9775],[1665367440000,2055421412080.7832],[1665367500000,2055421412080.4016],[1665367560000,2055421412082.4583],[1665367620000,2055421412080.5613],[1665367680000,2055421412082.0867],[1665367740000,2055421412080.7554],[1665367800000,2055421412080.951],[1665367860000,2055421412081.8662],[1665367920000,2055421412080.3506],[1665367980000,2055421412082.1116],[1665368040000,2055421412080.613],[1665368100000,2055421412080.6965],[1665368160000,2055421412082.0781],[1665368220000,2055421412080.6697],[1665368280000,2055421412081.8738],[1665368340000,2055421412080.2847],[1665368400000,2055421412081.452],[1665368460000,2055421412081.8457],[1665368520000,2055421412080.5325],[1665368580000,2055421412081.631],[1665368640000,2055421412080.3037],[1665368700000,2055421412084.883],[1665368760000,2055421412082.3994],[1665368820000,2055421412080.5715],[1665368880000,2055421412081.809],[1665368940000,2055421412082.9312],[1665369000000,2055421412083.202],[1665369060000,2055421412084.4453],[1665369120000,2055421412082.8672],[1665369180000,2055421412081.9722]]}]}`,
		},
	}
	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	// mock掉底层请求接口
	ctrl, stubs := FakePromData(t, true)
	defer stubs.Reset()
	defer ctrl.Finish()

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			resp, err := handleTSQuery(context.Background(), testCase.data, false, nil, "")
			assert.Nil(t, err, name)
			if err == nil {
				result, err := json.Marshal(resp)
				assert.Equal(t, testCase.err, err)
				fmt.Println(string(result))
				assert.Equal(t, testCase.result, string(result))
			}
		})
	}
}

// 测试对某个请求的inflxudb语句输出效果
func TestPromQueryWithAlignInfluxdbSQL(t *testing.T) {
	testCases := map[string]struct {
		data   string
		bizIDs []string
		result []string
		err    error
	}{
		// 二维conditions(or/contains/ncontains)
		"二维conditions": {
			data:   `{"query_list":[{"table_id":"system.disk","field_name":"in_use","reference_name":"A","dimensions":["bk_target_ip","bk_target_cloud_id","mount_point"],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"contains"},{"field_name":"device_type","value":["iso9660","tmpfs","udf"],"op":"ncontains"}],"condition_list":["and"]},"function":[{"method":"mean","dimensions":["bk_target_ip","bk_target_cloud_id","mount_point"]}],"time_aggregation":{"function":"avg_over_time","window":"1m"},"keep_columns":["_time","A","bk_target_ip","bk_target_cloud_id","mount_point"],"limit":1,"slimit":100}],"metric_merge":"A","start_time":"1626425160","end_time":"1626428760","step":"60s"}`,
			result: []string{`select mean("in_use") as _value,time as _time from "disk" where (bk_biz_id='2' and ((device_type!='iso9660' and device_type!='tmpfs') and device_type!='udf')) and time >= 1626425159999000000 and time < 1626428819998000000 group by "bk_target_ip","bk_target_cloud_id","mount_point",time(1m0s) limit 1 slimit 100`},
		},
		// or+eq/ne
		"or+eq/ne": {
			data:   `{"query_list":[{"table_id":"system.disk","field_name":"in_use","reference_name":"A","dimensions":["bk_target_ip","bk_target_cloud_id","mount_point"],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"eq"},{"field_name":"device_type","value":["iso9660","tmpfs","udf"],"op":"ne"}],"condition_list":["or"]},"function":[{"method":"mean","dimensions":["bk_target_ip","bk_target_cloud_id","mount_point"]}],"time_aggregation":{"function":"avg_over_time","window":"1m"},"keep_columns":["_time","A","bk_target_ip","bk_target_cloud_id","mount_point"],"limit":1,"slimit":100}],"metric_merge":"A","start_time":"1626425160","end_time":"1626428760","step":"60s"}`,
			result: []string{`select mean("in_use") as _value,time as _time from "disk" where (bk_biz_id='2' or ((device_type!='iso9660' and device_type!='tmpfs') and device_type!='udf')) and time >= 1626425159999000000 and time < 1626428819998000000 group by "bk_target_ip","bk_target_cloud_id","mount_point",time(1m0s) limit 1 slimit 100`},
		},
		// or+req/nreq
		"or+req/nreq": {
			data:   `{"query_list":[{"table_id":"system.cpu_summary","field_name":"usage","reference_name":"A","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_target_ip","value":["127.0.0.1","127.0.0.1"],"op":"req"},{"field_name":"bk_biz_id","value":["2"],"op":"contains"}],"condition_list":["or"]},"function":[{"method":"sum","dimensions":[]}],"time_aggregation":{"function":"sum_over_time","window":"1m"},"keep_columns":["_time","A"],"limit":1,"slimit":100}],"metric_merge":"A","start_time":"1626502800","end_time":"1626506400","step":"60s"}`,
			result: []string{`select sum("usage") as _value,time as _time from "cpu_summary" where ((bk_target_ip=~/127.0.0.1/ or bk_target_ip=~/127.0.0.1/) or bk_biz_id='2') and time >= 1626502799999000000 and time < 1626506459998000000 group by time(1m0s) limit 1 slimit 100`},
		},
		//  and+contains+req
		"and+contains+req": {
			data:   `{"query_list":[{"table_id":"system.cpu_summary","field_name":"usage","reference_name":"A","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_target_ip","value":["127.0.0.1"],"op":"req"},{"field_name":"bk_biz_id","value":["2"],"op":"contains"}],"condition_list":["and"]},"function":[{"method":"sum","dimensions":[]}],"time_aggregation":{"function":"sum_over_time","window":"1m"},"keep_columns":["_time","A"],"limit":1,"slimit":100}],"metric_merge":"A","start_time":"1626502800","end_time":"1626506400","step":"60s"}`,
			result: []string{`select sum("usage") as _value,time as _time from "cpu_summary" where (bk_target_ip=~/127.0.0.1/ and bk_biz_id='2') and time >= 1626502799999000000 and time < 1626506459998000000 group by time(1m0s) limit 1 slimit 100`},
		},
		//  and+contains+req+多值
		"and+contains+req+多值": {
			data:   `{"query_list":[{"table_id":"system.cpu_summary","field_name":"usage","reference_name":"A","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_target_ip","value":["127.0.0.1","127.0.0.1"],"op":"req"},{"field_name":"bk_biz_id","value":["2"],"op":"contains"}],"condition_list":["and"]},"function":[{"method":"sum","dimensions":[]}],"time_aggregation":{"function":"sum_over_time","window":"1m"},"keep_columns":["_time","A"],"limit":1,"slimit":100}],"metric_merge":"A","start_time":"1626502800","end_time":"1626506400","step":"60s"}`,
			result: []string{`select sum("usage") as _value,time as _time from "cpu_summary" where ((bk_target_ip=~/127.0.0.1/ or bk_target_ip=~/127.0.0.1/) and bk_biz_id='2') and time >= 1626502799999000000 and time < 1626506459998000000 group by time(1m0s) limit 1 slimit 100`},
		},
		"normal": {
			data:   `{"query_list":[{"table_id":"system.cpu_summary","field_name":"usage","reference_name":"A","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_target_ip","value":["127.0.0.1","127.0.0.1"],"op":"nreq"},{"field_name":"bk_biz_id","value":["2"],"op":"contains"}],"condition_list":["and"]},"function":[{"method":"sum","dimensions":[]}],"time_aggregation":{"function":"sum_over_time","window":"1m"},"keep_columns":["_time","A"],"limit":1,"slimit":100}],"metric_merge":"A","start_time":"1626502800","end_time":"1626506400","step":"60s"}`,
			result: []string{`select sum("usage") as _value,time as _time from "cpu_summary" where ((bk_target_ip!~/127.0.0.1/ and bk_target_ip!~/127.0.0.1/) and bk_biz_id='2') and time >= 1626502799999000000 and time < 1626506459998000000 group by time(1m0s) limit 1 slimit 100`},
		},
		// 一维conditions(纯and+eq/ne/req/nreq条件模式,此时不支持单个条件多值)
		// and+eq/ne
		"一维conditions": {
			data:   `{"query_list":[{"table_id":"system.cpu_summary","field_name":"usage","reference_name":"A","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_target_ip","value":["127.0.0.1"],"op":"ne"},{"field_name":"bk_biz_id","value":["2","3"],"op":"eq"}],"condition_list":["and"]},"function":[{"method":"sum","dimensions":[]}],"time_aggregation":{"function":"sum_over_time","window":"1m"},"keep_columns":["_time","A"],"limit":1,"slimit":100}],"metric_merge":"A","start_time":"1626502800","end_time":"1626506400","step":"60s"}`,
			result: []string{`select sum("usage") as _value,time as _time from "cpu_summary" where bk_biz_id = '2' and bk_target_ip != '127.0.0.1' and time >= 1626502799999000000 and time < 1626506459998000000 group by time(1m0s) limit 1 slimit 100`},
		},
		// and+req/nreq
		"and+req/nreq": {
			data:   `{"query_list":[{"table_id":"system.cpu_summary","field_name":"usage","reference_name":"A","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_target_ip","value":["127.0.0.1"],"op":"req"},{"field_name":"bk_biz_id","value":["2","3"],"op":"nreq"}],"condition_list":["and"]},"function":[{"method":"sum","dimensions":[]}],"time_aggregation":{"function":"sum_over_time","window":"1m"},"keep_columns":["_time","A"],"limit":1,"slimit":100}],"metric_merge":"A","start_time":"1626502800","end_time":"1626506400","step":"60s"}`,
			result: []string{`select sum("usage") as _value,time as _time from "cpu_summary" where bk_biz_id !~ /2/ and bk_target_ip =~ /127.0.0.1/ and time >= 1626502799999000000 and time < 1626506459998000000 group by time(1m0s) limit 1 slimit 100`},
		},
		"miss value": {
			data: `{"query_list":[{"table_id":"system.cpu_summary","field_name":"idle","reference_name":"a","dimensions":["bk_target_ip","bk_target_cloud_id"],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_target_ip","value":[],"op":"contains"},{"field_name":"bk_biz_id","value":["2"],"op":"contains"}],"condition_list":["and"]},"function":[{"method":"mean","dimensions":["bk_target_ip","bk_target_cloud_id"]}],"time_aggregation":{"function":"avg_over_time","window":"60s"},"keep_columns":["_time","a","bk_target_ip","bk_target_cloud_id"],"limit":50000,"slimit":50000}],"metric_merge":"a","start_time":"1628220540","end_time":"1628242140","step":"60s"}`,
			err:  errors.Wrap(structured.ErrMissingValue, "bk_target_ip"),
		},
		// tableID为空，bk_biz_id+metric 生成多个sql
		"bk_biz_id+metric without tableID": {
			data:   `{"query_list":[{"table_id":"","field_name":"m2","reference_name":"A","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_target_ip","value":["127.0.0.1"],"op":"req"},{"field_name":"bk_biz_id","value":["2"],"op":"contains"}],"condition_list":["and"]},"function":[{"method":"sum","dimensions":[]}],"time_aggregation":{"function":"sum_over_time","window":"1m"},"keep_columns":["_time","A"],"limit":1,"slimit":100}],"metric_merge":"A","start_time":"1626502800","end_time":"1626506400","step":"60s"}`,
			result: []string{`select sum("m2") as _value,time as _time from "cpu_detail" where (bk_target_ip=~/127.0.0.1/ and bk_biz_id='2') and time >= 1626502799999000000 and time < 1626506459998000000 group by time(1m0s) limit 1 slimit 100`},
		},
		"table_id with header bk_biz_id": {
			data:   `{"query_list":[{"table_id":"system.cpu_summary","field_name":"usage","reference_name":"A","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_target_ip","value":["127.0.0.1"],"op":"ne"},{"field_name":"bk_biz_id","value":["2","3"],"op":"eq"}],"condition_list":["and"]},"function":[{"method":"sum","dimensions":[]}],"time_aggregation":{"function":"sum_over_time","window":"1m"},"keep_columns":["_time","A"],"limit":1,"slimit":100}],"metric_merge":"A","start_time":"1626502800","end_time":"1626506400","step":"60s"}`,
			bizIDs: []string{"3"},
			result: []string{`select sum("usage") as _value,time as _time from "cpu_summary" where bk_biz_id = '3' and bk_target_ip != '127.0.0.1' and time >= 1626502799999000000 and time < 1626506459998000000 group by time(1m0s) limit 1 slimit 100`},
		},
		"bk_biz_id+metric with header bk_biz_id": {
			data:   `{"query_list":[{"table_id":"","field_name":"m2","reference_name":"A","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_target_ip","value":["127.0.0.1"],"op":"req"},{"field_name":"bk_biz_id","value":["3"],"op":"contains"}],"condition_list":["and"]},"function":[{"method":"sum","dimensions":[]}],"time_aggregation":{"function":"sum_over_time","window":"1m"},"keep_columns":["_time","A"],"limit":1,"slimit":100}],"metric_merge":"A","start_time":"1626502800","end_time":"1626506400","step":"60s"}`,
			bizIDs: []string{"2"},
			result: []string{`select sum("m2") as _value,time as _time from "cpu_detail" where bk_biz_id = '2' and bk_target_ip =~ /127.0.0.1/ and time >= 1626502799999000000 and time < 1626506459998000000 group by time(1m0s) limit 1 slimit 100`},
		},
		// window + offset + offsetbackword
		"window + offset + offsetbackword": {
			data:   `{"query_list":[{"table_id":"system.disk","field_name":"in_use","reference_name":"A","dimensions":["bk_target_ip","bk_target_cloud_id","mount_point"],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"contains"},{"field_name":"device_type","value":["iso9660"],"op":"ncontains"}],"condition_list":["and"]},"function":[{"method":"mean","dimensions":["bk_target_ip","bk_target_cloud_id","mount_point"]}],"time_aggregation":{"function":"avg_over_time","window":"2m"},"keep_columns":["_time","A","bk_target_ip","bk_target_cloud_id","mount_point"],"offset":"5m","offset_forward":false,"limit":20000,"slimit":100}],"metric_merge":"A","start_time":"1626425160","end_time":"1626428760","step":"60s"}`,
			result: []string{`select mean("in_use") as _value,time as _time from "disk" where (bk_biz_id='2' and device_type!='iso9660') and time >= 1626424799999000000 and time < 1626428519998000000 group by "bk_target_ip","bk_target_cloud_id","mount_point",time(2m0s) limit 20000 slimit 100`},
		},
		// window + offset + offsetbackword
		"window": {
			data:   `{"query_list":[{"table_id":"system.disk","field_name":"in_use","reference_name":"A","dimensions":["bk_target_ip","bk_target_cloud_id","mount_point"],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"contains"},{"field_name":"device_type","value":["iso9660"],"op":"ncontains"}],"condition_list":["and"]},"function":[{"method":"mean","dimensions":["bk_target_ip","bk_target_cloud_id","mount_point"]}],"time_aggregation":{"function":"avg_over_time","window":"2m"},"keep_columns":["_time","A","bk_target_ip","bk_target_cloud_id","mount_point"],"offset":"","offset_forward":false,"limit":20000,"slimit":100}],"metric_merge":"A","start_time":"1626425160","end_time":"1626428760","step":"60s"}`,
			result: []string{`select mean("in_use") as _value,time as _time from "disk" where (bk_biz_id='2' and device_type!='iso9660') and time >= 1626425099999000000 and time < 1626428819998000000 group by "bk_target_ip","bk_target_cloud_id","mount_point",time(2m0s) limit 20000 slimit 100`},
		},
	}

	ctrl, stubs := FakePromData(t, true)
	makeRouterInfo(t, ctrl, stubs)

	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})
	resultSQLs := make([]string, 0)

	// mock掉底层请求接口
	ctrl, stubs = CatchSQL(t, ctrl, stubs, &resultSQLs, true)
	defer stubs.Reset()
	defer ctrl.Finish()

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			_, err := handleTSQuery(context.Background(), testCase.data, false, testCase.bizIDs, "")
			if err != nil && testCase.err != nil {
				assert.Equal(t, testCase.err.Error(), err.Error(), name)
			} else {
				sort.Strings(resultSQLs)
				sort.Strings(testCase.result)
				assert.Equal(t, len(testCase.result), len(resultSQLs))
				for i, sql := range resultSQLs {
					assert.Equal(t, testCase.result[i], sql, name)
				}
			}
			resultSQLs = resultSQLs[:0]
		})
	}
}

// FakePromDataBench
func FakePromDataBench(b *testing.B, align bool) (*gomock.Controller, *gostub.Stubs) {
	log.InitTestLogger()
	ctrl := gomock.NewController(b)
	basepath := "testfile"

	dirs, err := os.ReadDir(basepath)
	if err != nil {
		panic(err)
	}

	dataMap := make(map[string]*decoder.Response)

	for _, dir := range dirs {
		data, err := os.ReadFile(basepath + "/" + dir.Name())
		if err != nil {
			panic(err)
		}
		series := make([]*decoder.Row, 0)
		err = json.Unmarshal(data, series)
		if err != nil {
			continue
		}
		dataMap[dir.Name()] = &decoder.Response{
			Results: []decoder.Result{
				{Series: series},
			},
		}
	}

	// 制造一个返回假数据的influxdb client
	log.Infof(context.TODO(), "%s set mock data %d", "tsquery_prom_test.go", len(dataMap))
	mockClient := mocktest.NewMockClient(ctrl)
	mockClient.EXPECT().Query(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, db, sql, precision, contentType string, chunked bool) (*decoder.Response, error) {
		md5Inst := md5.New()
		md5Inst.Write([]byte(sql))
		hashSql := fmt.Sprintf("%x", md5Inst.Sum([]byte("")))
		result, ok := dataMap[hashSql]
		if !ok {
			fmt.Println(sql)
			fmt.Println(hashSql)
			fmt.Println("____________________________________________________________")
			return &decoder.Response{Results: nil}, nil
		}

		return result, nil
	}).AnyTimes()

	_ = influxdb.InitGlobalInstance(context.Background(), &influxdb.Params{
		Timeout: 30 * time.Second,
	}, mockClient)

	stubs := gostub.New()
	stubs.Stub(&AlignInfluxdbResult, align)

	return ctrl, stubs
}

// BenchmarkTestProm
func BenchmarkTestProm(b *testing.B) {
	runtime.GOMAXPROCS(1)
	testCases := []struct {
		data   string
		result string
		err    error
	}{
		{
			data:   `{"query_list":[{"table_id":"system.cpu_summary","field_name":"usage","function":[{"dimensions":["bk_target_ip","bk_target_cloud_id"],"method":"mean"}],"time_aggregation":{"function":"avg_over_time","window":"1m"},"reference_name":"A","dimensions":["bk_target_ip","bk_target_cloud_id"],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"eq"}],"condition_list":[]},"keep_columns":["_time","A","bk_target_ip","bk_target_cloud_id"],"limit":50000,"slimit":50000},{"table_id":"system.io","field_name":"util","function":[{"dimensions":["bk_target_ip","bk_target_cloud_id","device_name"],"method":"mean"}],"time_aggregation":{"function":"avg_over_time","window":"1m"},"reference_name":"B","dimensions":["bk_target_ip","bk_target_cloud_id","device_name"],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"eq"}],"condition_list":[]},"keep_columns":["_time","B","bk_target_ip","bk_target_cloud_id","device_name"],"limit":50000,"slimit":50000},{"table_id":"system.disk","field_name":"free","function":[{"dimensions":["bk_target_ip","bk_target_cloud_id"],"method":"mean"}],"time_aggregation":{"function":"avg_over_time","window":"1m"},"reference_name":"C","dimensions":["bk_target_ip","bk_target_cloud_id"],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"eq"},{"field_name":"device_type","value":["iso9660","tmpfs","udf"],"op":"ne"}],"condition_list":["and"]},"keep_columns":["_time","C","bk_target_ip","bk_target_cloud_id"],"limit":50000,"slimit":50000}],"metric_merge":"B + A + C","order_by":["-time"],"start_time":"1629861029","end_time":"1629861329","step":"60s"}`,
			result: `{"series":[{"name":"_result0","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","sr0"],"values":[[1629861029000,72408472236.1359],[1629861089000,72408416257.54337],[1629861149000,72408363010.12248],[1629861209000,72408294742.95152],[1629861269000,72408238764.30147]]},{"name":"_result1","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda"],"values":[[1629861029000,72408472236.1423],[1629861089000,72408416257.54878],[1629861149000,72408363010.12811],[1629861209000,72408294742.95763],[1629861269000,72408238764.30734]]},{"name":"_result2","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda1"],"values":[[1629861029000,72408472236.1423],[1629861089000,72408416257.54884],[1629861149000,72408363010.12813],[1629861209000,72408294742.95766],[1629861269000,72408238764.30736]]},{"name":"_result3","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vdb"],"values":[[1629861029000,72408472236.1366],[1629861089000,72408416257.54427],[1629861149000,72408363010.12308],[1629861209000,72408294742.95209],[1629861269000,72408238764.30211]]},{"name":"_result4","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","sr0"],"values":[[1629861029000,77752220331.23433],[1629861089000,77752182101.88417],[1629861149000,77752146603.63512],[1629861209000,77752105643.16745],[1629861269000,77752070144.55922]]},{"name":"_result5","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda"],"values":[[1629861029000,77752220331.23949],[1629861089000,77752182101.88902],[1629861149000,77752146603.64015],[1629861209000,77752105643.17326],[1629861269000,77752070144.56442]]},{"name":"_result6","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda1"],"values":[[1629861029000,77752220331.23949],[1629861089000,77752182101.88905],[1629861149000,77752146603.64017],[1629861209000,77752105643.1733],[1629861269000,77752070144.56444]]},{"name":"_result7","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vdb"],"values":[[1629861029000,77752220331.23502],[1629861089000,77752182101.88452],[1629861149000,77752146603.63544],[1629861209000,77752105643.16798],[1629861269000,77752070144.5598]]},{"name":"_result8","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","sr0"],"values":[[1629861029000,47153846280.30545],[1629861089000,47152947208.64652],[1629861149000,47152844812.8276],[1629861209000,47152711688.57716],[1629861269000,47152543752.25978]]},{"name":"_result9","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda"],"values":[[1629861029000,47153846280.31683],[1629861089000,47152947208.668976],[1629861149000,47152844812.84035],[1629861209000,47152711688.58993],[1629861269000,47152543752.27264]]},{"name":"_result10","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda1"],"values":[[1629861029000,47153846280.31683],[1629861089000,47152947208.669586],[1629861149000,47152844812.8404],[1629861209000,47152711688.58993],[1629861269000,47152543752.27264]]},{"name":"_result11","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vdb"],"values":[[1629861029000,47153846280.3069],[1629861089000,47152947208.64839],[1629861149000,47152844812.82908],[1629861209000,47152711688.57966],[1629861269000,47152543752.26168]]},{"name":"_result12","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","sr0"],"values":[[1629861029000,70515513345.5685],[1629861089000,70515478529.83731],[1629861149000,70515408898.40201],[1629861209000,70515372033.61937],[1629861269000,70515335169.53487]]},{"name":"_result13","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda"],"values":[[1629861029000,70515513345.5738],[1629861089000,70515478529.8428],[1629861149000,70515408898.408],[1629861209000,70515372033.62596],[1629861269000,70515335169.54202]]},{"name":"_result14","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda1"],"values":[[1629861029000,70515513345.5738],[1629861089000,70515478529.8428],[1629861149000,70515408898.408],[1629861209000,70515372033.62596],[1629861269000,70515335169.54202]]},{"name":"_result15","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vdb"],"values":[[1629861029000,70515513345.57008],[1629861089000,70515478529.83908],[1629861149000,70515408898.40346],[1629861209000,70515372033.62122],[1629861269000,70515335169.5366]]},{"name":"_result16","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","sr0"],"values":[[1629861029000,68788742153.11833],[1629861089000,68787884040.55273],[1629861149000,68787030024.96898],[1629861209000,68786149384.59729],[1629861269000,68785293321.14572]]},{"name":"_result17","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda"],"values":[[1629861029000,68788742153.12253],[1629861089000,68787884040.55739],[1629861149000,68787030024.97281],[1629861209000,68786149384.60211],[1629861269000,68785293321.15031]]},{"name":"_result18","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda1"],"values":[[1629861029000,68788742153.12253],[1629861089000,68787884040.55739],[1629861149000,68787030024.97281],[1629861209000,68786149384.60211],[1629861269000,68785293321.15031]]},{"name":"_result19","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vdb"],"values":[[1629861029000,68788742153.11989],[1629861089000,68787884040.55403],[1629861149000,68787030024.97025],[1629861209000,68786149384.59859],[1629861269000,68785293321.1469]]},{"name":"_result20","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","sr0"],"values":[[1629861029000,62283161636.116486],[1629861089000,62275334167.1067],[1629861149000,62146172978.38818],[1629861209000,62224177174.80421],[1629861269000,62222305305.35389]]},{"name":"_result21","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda"],"values":[[1629861029000,62283161636.14239],[1629861089000,62275334167.14231],[1629861149000,62146172978.414734],[1629861209000,62224177174.83389],[1629861269000,62222305305.380806]]},{"name":"_result22","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda1"],"values":[[1629861029000,62283161636.142456],[1629861089000,62275334167.142365],[1629861149000,62146172978.4148],[1629861209000,62224177174.83403],[1629861269000,62222305305.380844]]},{"name":"_result23","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vdb"],"values":[[1629861029000,62283161636.121155],[1629861089000,62275334167.111916],[1629861149000,62146172978.3945],[1629861209000,62224177174.8129],[1629861269000,62222305305.35809]]},{"name":"_result24","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","sr0"],"values":[[1629861029000,70323492918.17474],[1629861089000,70321045544.92625],[1629861149000,70276716593.79784],[1629861209000,70192031788.2382],[1629861269000,70273499175.92781]]},{"name":"_result25","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda"],"values":[[1629861029000,70323492918.18535],[1629861089000,70321045544.9366],[1629861149000,70276716593.81082],[1629861209000,70192031788.24857],[1629861269000,70273499175.9375]]},{"name":"_result26","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda1"],"values":[[1629861029000,70323492918.18532],[1629861089000,70321045544.9366],[1629861149000,70276716593.81082],[1629861209000,70192031788.2486],[1629861269000,70273499175.9375]]},{"name":"_result27","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vdb"],"values":[[1629861029000,70323492918.17616],[1629861089000,70321045544.9276],[1629861149000,70276716593.79921],[1629861209000,70192031788.28676],[1629861269000,70273499175.93205]]},{"name":"_result28","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","sr0"],"values":[[1629861029000,135258910721.26942],[1629861089000,135258828801.20221],[1629861149000,135258755073.1525],[1629861209000,135258673153.21951],[1629861269000,135258599425.23622]]},{"name":"_result29","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda"],"values":[[1629861029000,135258910721.27849],[1629861089000,135258828801.21034],[1629861149000,135258755073.16013],[1629861209000,135258673153.22646],[1629861269000,135258599425.24338]]},{"name":"_result30","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["0","127.0.0.1","vda1"],"values":[[1629861029000,135258910721.27852],[1629861089000,135258828801.21037],[1629861149000,135258755073.16016],[1629861209000,135258673153.22649],[1629861269000,135258599425.24344]]}]}`,
		},
	}

	// mock掉底层请求接口
	ctrl, stubs := FakePromDataBench(b, true)
	defer stubs.Reset()
	defer ctrl.Finish()
	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	gin.SetMode(gin.ReleaseMode)
	g := gin.Default()
	g.POST("/BenchmarkTestProm/query/ts", HandleTSQueryRequest)
	wg := new(sync.WaitGroup)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, testCase := range testCases {
			wg.Add(1)
			go func(testCase struct {
				data   string
				result string
				err    error
			}) {
				defer wg.Done()
				w := httptest.NewRecorder()
				req := httptest.NewRequest("POST", "/BenchmarkTestProm/query/ts", strings.NewReader(testCase.data))
				g.ServeHTTP(w, req)
				assert.Equal(b, 200, w.Code)
				assert.Equal(b, testCase.result, w.Body.String())
			}(testCase)

		}
	}

	wg.Wait()
}
