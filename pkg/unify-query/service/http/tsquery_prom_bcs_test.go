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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang/mock/gomock"
	"github.com/hashicorp/consul/api"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/trace"
)

// makeRouterInfo
func makeRouterInfo(t *testing.T, ctrl *gomock.Controller, stubs *gostub.Stubs) (*gomock.Controller, *gostub.Stubs) {
	if stubs == nil {
		stubs = gostub.New()
	}
	if ctrl == nil {
		ctrl = gomock.NewController(t)
	}

	_ = consul.SetInstance(
		context.Background(), "", "test-unify", "http://127.0.0.1:8500",
		[]string{}, "127.0.0.1", 10205, "30s", nil,
	)
	consul.MetadataPath = "test/metadata/v1/default/data_id"
	consul.BCSInfoPath = "test/metadata/v1/default/project_id"
	consul.MetricRouterPath = "test/metadata/influxdb_metrics"

	data := map[string]api.KVPairs{
		// dataid info
		consul.MetadataPath: {
			{
				Key:   consul.MetadataPath + "/150001",
				Value: []byte(`{"bk_data_id":150001,"data_id":150001,"mq_config":{"storage_config":{"topic":"0bkmonitor_1500010","partition":1},"cluster_config":{"domain_name":"kafka.service.consul","port":9092,"schema":null,"is_ssl_verify":false,"cluster_id":1,"cluster_name":"kafka_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"kafka","auth_info":{"password":"","username":""}},"etl_config":"bk_standard_v2_time_series","result_table_list":[{"bk_biz_id":2,"result_table":"process.port","shipper_list":[{"storage_config":{"real_table_name":"port","database":"process","retention_policy_name":""},"cluster_config":{"domain_name":"influxdb-proxy.bkmonitorv3.service.consul","port":10203,"schema":null,"is_ssl_verify":false,"cluster_id":2,"cluster_name":"influx_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"influxdb","auth_info":{"password":"","username":""}}],"field_list":[{"field_name":"etcd_server_slow_apply_total","type":"float","tag":"metric","default_value":"0","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"time","type":"timestamp","tag":"timestamp","default_value":"","is_config_by_user":true,"description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","unit":"","alias_name":"","option":{}}],"schema_type":"free","option":{}}],"option":{"inject_local_time":true,"timestamp_precision":"ms","flat_batch_key":"data","metrics_report_path":"bk_bkmonitorv3_enterprise_production/metadata/influxdb_metrics/150001/time_series_metric","disable_metric_cutter":"true"},"type_label":"time_series","source_label":"custom","token":"4774c8313d74430ca68c204aa6491eee","transfer_cluster_id":"default"}`),
			},
			{
				Key:   consul.MetadataPath + "/150002",
				Value: []byte(`{"bk_data_id":150002,"data_id":150002,"mq_config":{"storage_config":{"topic":"0bkmonitor_1500020","partition":1},"cluster_config":{"domain_name":"kafka.service.consul","port":9092,"schema":null,"is_ssl_verify":false,"cluster_id":1,"cluster_name":"kafka_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"kafka","auth_info":{"password":"","username":""}},"etl_config":"bk_standard_v2_time_series","result_table_list":[{"bk_biz_id":2,"result_table":"process.port","shipper_list":[{"storage_config":{"real_table_name":"port","database":"process","retention_policy_name":""},"cluster_config":{"domain_name":"influxdb-proxy.bkmonitorv3.service.consul","port":10203,"schema":null,"is_ssl_verify":false,"cluster_id":2,"cluster_name":"influx_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"influxdb","auth_info":{"password":"","username":""}}],"field_list":[{"field_name":"m2","type":"float","tag":"metric","default_value":"0","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"time","type":"timestamp","tag":"timestamp","default_value":"","is_config_by_user":true,"description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","unit":"","alias_name":"","option":{}}],"schema_type":"free","option":{}}],"option":{"inject_local_time":true,"timestamp_precision":"ms","flat_batch_key":"data","metrics_report_path":"bk_bkmonitorv3_enterprise_production/metadata/influxdb_metrics/150002/time_series_metric","disable_metric_cutter":"true"},"type_label":"time_series","source_label":"custom","token":"4774c8313d74430ca68c204aa6491eee","transfer_cluster_id":"default"}`),
			},
			{
				Key:   consul.MetadataPath + "/150003",
				Value: []byte(`{"bk_data_id":150003,"data_id":150003,"mq_config":{"storage_config":{"topic":"0bkmonitor_1500030","partition":1},"cluster_config":{"domain_name":"kafka.service.consul","port":9092,"schema":null,"is_ssl_verify":false,"cluster_id":1,"cluster_name":"kafka_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"kafka","auth_info":{"password":"","username":""}},"etl_config":"bk_standard_v2_time_series","result_table_list":[{"bk_biz_id":2,"result_table":"process.port","shipper_list":[{"storage_config":{"real_table_name":"port","database":"process","retention_policy_name":""},"cluster_config":{"domain_name":"influxdb-proxy.bkmonitorv3.service.consul","port":10203,"schema":null,"is_ssl_verify":false,"cluster_id":2,"cluster_name":"influx_cluster1","version":null,"custom_option":"","registered_system":"_default","creator":"system","create_time":1574157128,"last_modify_user":"system","is_default_cluster":true},"cluster_type":"influxdb","auth_info":{"password":"","username":""}}],"field_list":[{"field_name":"m2","type":"float","tag":"metric","default_value":"0","is_config_by_user":true,"description":"","unit":"","alias_name":"","option":{}},{"field_name":"time","type":"timestamp","tag":"timestamp","default_value":"","is_config_by_user":true,"description":"\u6570\u636e\u4e0a\u62a5\u65f6\u95f4","unit":"","alias_name":"","option":{}}],"schema_type":"free","option":{}}],"option":{"inject_local_time":true,"timestamp_precision":"ms","flat_batch_key":"data","metrics_report_path":"bk_bkmonitorv3_enterprise_production/metadata/influxdb_metrics/150003/time_series_metric","disable_metric_cutter":"true"},"type_label":"time_series","source_label":"custom","token":"4774c8313d74430ca68c204aa6491eee","transfer_cluster_id":"default"}`),
			},
		},
		// metric info
		consul.MetricRouterPath: {
			{
				Key:   consul.MetricRouterPath + "/150001/time_series_metric",
				Value: []byte(`["etcd_server_slow_apply_total"]`),
			},
			{
				Key:   consul.MetricRouterPath + "/150001/time_series_metric/etcd_server_slow_apply_total",
				Value: []byte(`["bk_biz_id"]`),
			},
			{
				Key:   consul.MetricRouterPath + "/150002/time_series_metric",
				Value: []byte(`["m2"]`),
			},
			{
				Key:   consul.MetricRouterPath + "/150002/time_series_metric/m2",
				Value: []byte(`["bk_biz_id"]`),
			},
			{
				Key:   consul.MetricRouterPath + "/150003/time_series_metric",
				Value: []byte(`["m3"]`),
			},
			{
				Key:   consul.MetricRouterPath + "/150003/time_series_metric/m2",
				Value: []byte(`["bk_biz_id"]`),
			},
		},
		consul.BCSInfoPath: {
			{
				Key:   consul.BCSInfoPath + "/2/cluster_id/5",
				Value: []byte(`[150001, 1500011]`),
			},
		},
	}

	consul.GetDataWithPrefix = func(prefix string) (api.KVPairs, error) {
		return data[prefix], nil
	}
	consul.GetPathDataIDPath = func(metadataPath, version string) ([]string, error) {
		return []string{metadataPath}, nil
	}

	_ = consul.ReloadBCSInfo()
	reloadData, err := consul.ReloadRouterInfo()
	assert.Nil(t, err)
	influxdb.ReloadTableInfos(reloadData)
	metricData, err := consul.ReloadMetricInfo()
	assert.Nil(t, err)
	influxdb.ReloadMetricRouter(metricData)

	return ctrl, stubs
}

// TestParse_StructPromQl
func TestParse_StructPromQl(t *testing.T) {
	// makeData()

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
	g.POST("/query/ts/struct_to_promql", HandleTsQueryStructToPromQLRequest)
	g.POST("/query/ts/promql_to_struct", HandleTsQueryPromQLToStructRequest)

	testCases := map[string]struct {
		params string
		expect string
		url    string
	}{
		// promql -> structure, bkmonitor:db:measurement:metric
		"a1": {
			params: `{"promql":"avg(avg_over_time(bkmonitor:system:cpu_detail:metric1{tag1!=\"dd\",tag1=\"abcd\"}[2m] offset 3m)) by(tag1, tag2)"}`,
			expect: `{"data":{"query_list":[{"data_source":"bkmonitor","db":"system","table_id":"system.cpu_detail","is_free_schema":false,"field_name":"metric1","function":[{"method":"mean","dimensions":["tag1","tag2"],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"avg_over_time","window":"2m0s","position":0,"vargs_list":null},"reference_name":"a","dimensions":null,"driver":"","time_field":"","window":"","limit":0,"offset":"3m0s","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"tag1","value":["dd"],"op":"ne"},{"field_name":"tag1","value":["abcd"],"op":"eq"}],"condition_list":["and"]},"not_combine_window":false,"keep_columns":null,"start_time":"","end_time":"","order_by":null,"AlignInfluxdbResult":false}],"metric_merge":"a","join_list":null,"order_by":null,"result_columns":null,"join_with_time":false,"keep_columns":null,"start_time":"","end_time":"","step":"","type":""}}`,
			url:    "/query/ts/promql_to_struct",
		},
		// promql -> structure, bkmonitor:metric1
		"a2": {
			params: `{"promql":"avg(avg_over_time(bkmonitor:metric1{tag1!=\"dd\",tag1=\"abcd\"}[2m] offset 3m)) by(tag1, tag2)"}`,
			expect: `{"data":{"query_list":[{"data_source":"bkmonitor","db":"","table_id":"","is_free_schema":false,"field_name":"metric1","function":[{"method":"mean","dimensions":["tag1","tag2"],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"avg_over_time","window":"2m0s","position":0,"vargs_list":null},"reference_name":"a","dimensions":null,"driver":"","time_field":"","window":"","limit":0,"offset":"3m0s","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"tag1","value":["dd"],"op":"ne"},{"field_name":"tag1","value":["abcd"],"op":"eq"}],"condition_list":["and"]},"not_combine_window":false,"keep_columns":null,"start_time":"","end_time":"","order_by":null,"AlignInfluxdbResult":false}],"metric_merge":"a","join_list":null,"order_by":null,"result_columns":null,"join_with_time":false,"keep_columns":null,"start_time":"","end_time":"","step":"","type":""}}`,
			url:    "/query/ts/promql_to_struct",
		},
		// structure -> promql, bkmonitor:db:measurement:metric
		"a3": {
			params: `{"query_list":[{"data_source":"bkmonitor","db":"","table_id":"db.measurement","is_free_schema":false,"field_name":"metric1","function":[{"method":"mean","dimensions":["tag1","tag2"],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"avg_over_time","window":"2m0s","position":0,"vargs_list":null},"reference_name":"a","dimensions":null,"driver":"","time_field":"","window":"","limit":0,"offset":"3m0s","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"tag1","value":["dd"],"op":"ne"},{"field_name":"tag1","value":["abcd"],"op":"eq"}],"condition_list":["and"]},"not_combine_window":false,"keep_columns":null,"start_time":"","end_time":"","order_by":null,"AlignInfluxdbResult":false}],"metric_merge":"a","join_list":null,"order_by":null,"result_columns":null,"join_with_time":false,"keep_columns":null,"start_time":"","end_time":"","step":"","type":""}`,
			expect: `{"promql":"avg by (tag1, tag2) (avg_over_time(bkmonitor:db:measurement:metric1{tag1!=\"dd\",tag1=\"abcd\"}[2m] offset 3m))"}`,
			url:    "/query/ts/struct_to_promql",
		},
		// structure -> promql, bkmonitor:metric1
		"a4": {
			params: `{"query_list":[{"data_source":"bkmonitor","db":"","table_id":"","is_free_schema":false,"field_name":"metric1","function":[{"method":"mean","dimensions":["tag1","tag2"],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"avg_over_time","window":"2m0s","position":0,"vargs_list":null},"reference_name":"a","dimensions":null,"driver":"","time_field":"","window":"","limit":0,"offset":"3m0s","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"tag1","value":["dd"],"op":"ne"},{"field_name":"tag1","value":["abcd"],"op":"eq"}],"condition_list":["and"]},"not_combine_window":false,"keep_columns":null,"start_time":"","end_time":"","order_by":null,"AlignInfluxdbResult":false}],"metric_merge":"a","join_list":null,"order_by":null,"result_columns":null,"join_with_time":false,"keep_columns":null,"start_time":"","end_time":"","step":"","type":""}`,
			expect: `{"promql":"avg by (tag1, tag2) (avg_over_time(bkmonitor:metric1{tag1!=\"dd\",tag1=\"abcd\"}[2m] offset 3m))"}`,
			url:    "/query/ts/struct_to_promql",
		},
	}

	type structResp struct {
		Data structured.CombinedQueryParams `json:"data"`
	}

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, testCase.url, strings.NewReader(testCase.params))
			g.ServeHTTP(w, req)

			var structResult *structResp
			var PromqlResult *promqlReq

			switch testCase.url {
			case "/query/ts/promql_to_struct":
				err := json.Unmarshal(w.Body.Bytes(), &structResult)
				assert.Nil(t, err)
				var expect *structResp
				err = json.Unmarshal([]byte(testCase.expect), &expect)
				assert.Nil(t, err)
				//assert.Equal(t, expect, structResult)
				assert.Equal(t, expect.Data.QueryList[0], structResult.Data.QueryList[0])
			case "/query/ts/struct_to_promql":
				assert.Equal(t, http.StatusOK, w.Code)
				t.Log(w.Body.String())
				err := json.Unmarshal(w.Body.Bytes(), &PromqlResult)
				assert.Nil(t, err)
				var expect *promqlReq
				err = json.Unmarshal([]byte(testCase.expect), &expect)
				assert.Nil(t, err)
				assert.Equal(t, expect, PromqlResult)
			}
		})
	}
}

// TestQueryLimit
func TestQueryLimit(t *testing.T) {
	// 准备测试数据
	ctrl, stubs := FakePromData(t, true)
	makeRouterInfo(t, ctrl, stubs)

	oldDefaultQueryListLimit := DefaultQueryListLimit
	defer func() {
		DefaultQueryListLimit = oldDefaultQueryListLimit
	}()

	DefaultQueryListLimit = 1

	testCases := map[string]struct {
		reqParams    string
		expectResult string
		reqUrl       string
		err          string

		querylistlimit int
		maxlimit       int
		maxslimit      int
		tolerance      int
	}{
		"ts query list limit": {
			reqParams: `{"query_list":[{"data_source":"bkmonitor","concat_name":"bkmonitor:system:cpu_detail:nice","db":"system","table_id":"system.cpu_detail","field_name":"nice","time_aggregation":{"function":"rate","window":"2m0s","position":0,"vargs_list":null},"reference_name":"a","conditions":{"field_list":[{"field_name":"bk_target_ip","value":["127.0.0.1"],"op":"eq"},{"field_name":"device_name","value":["cpu0"],"op":"eq"}],"condition_list":["and"]}},{"data_source":"bkmonitor","concat_name":"bkmonitor:system:cpu_detail:idle","db":"system","table_id":"system.cpu_detail","field_name":"idle","time_aggregation":{"function":"","window":"","position":0,"vargs_list":null},"reference_name":"b","conditions":{"field_list":[{"field_name":"bk_target_ip","value":["127.0.0.1"],"op":"eq"},{"field_name":"device_name","value":["cpu0"],"op":"eq"}],"condition_list":["and"]}}],"metric_merge":"a - b","start_time":"1629806531","end_time":"1629810131","step":"600s"}`,
			reqUrl:    "/query/ts",
			err:       `{"error":"the number of query lists cannot be greater than 1"}`,
		},
		//"ts query raw influx limit > 10": {
		//	reqParams:    `{"query_list":[{"table_id":"system.cpu_summary","time_aggregation":{"function":"rate","window":"2m","vargs_list":[]},"field_name":"usage","reference_name":"a","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"contains"}],"condition_list":[]},"function":[{"method":"sum","dimensions":[]}],"offset":"","offset_forward":false,"keep_columns":["_time","a"]}],"metric_merge":"a","start_time":"1665403860","end_time":"1665407460","step":"60s","space_uid":"bkcc__2","down_sample_range":"4s"}`,
		//	reqUrl:       "/query/ts",
		//	expectResult: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1665403860000,0.07968699408822223]]}],"status":{"code":"EXCEEDS_MAXIMUM_LIMIT","data":10}}`,
		//},
		"ts query aggr for count": {
			reqParams:    `{"query_list":[{"table_id":"system.cpu_summary","time_aggregation":{"function":"count_over_time","window":"60s"},"field_name":"usage","reference_name":"b","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"contains"}],"condition_list":[]},"function":[{"method":"sum","dimensions":[]}],"offset":"","offset_forward":false,"keep_columns":["_time","b"]}],"metric_merge":"b","start_time":"1665403860","end_time":"1665407460","step":"60s","space_uid":"bkcc__2","down_sample_range":"4s"}`,
			reqUrl:       "/query/ts",
			expectResult: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1665403860000,24],[1665403920000,24],[1665403980000,24],[1665404040000,24],[1665404100000,24],[1665404160000,24],[1665404220000,24],[1665404280000,24],[1665404340000,24],[1665404400000,24],[1665404460000,24],[1665404520000,24],[1665404580000,24],[1665404640000,24]]}]}`,
		},
		"promql query aggr for count": {
			reqParams:    `{"promql":"sum(count_over_time(bkmonitor:system:cpu_summary:usage{bk_biz_id=\"2\"}[1m]))","start":"1665403860","end":"1665407460","step":"60s"}`,
			reqUrl:       "/query/ts/promql",
			expectResult: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1665403860000,24],[1665403920000,24],[1665403980000,24],[1665404040000,24],[1665404100000,24],[1665404160000,24],[1665404220000,24],[1665404280000,24],[1665404340000,24],[1665404400000,24],[1665404460000,24],[1665404520000,24],[1665404580000,24],[1665404640000,24],[1665404700000,24],[1665404760000,24],[1665404820000,24],[1665404880000,24],[1665404940000,24],[1665405000000,24],[1665405060000,24],[1665405120000,24],[1665405180000,24],[1665405240000,24],[1665405300000,24],[1665405360000,24],[1665405420000,24],[1665405480000,24],[1665405540000,24],[1665405600000,24],[1665405660000,24],[1665405720000,24],[1665405780000,24],[1665405840000,24],[1665405900000,24],[1665405960000,24],[1665406020000,24],[1665406080000,24],[1665406140000,24],[1665406200000,24],[1665406260000,24],[1665406320000,24],[1665406380000,24],[1665406440000,24],[1665406500000,24],[1665406560000,24],[1665406620000,24],[1665406680000,24],[1665406740000,24],[1665406800000,24],[1665406860000,24],[1665406920000,24],[1665406980000,24],[1665407040000,24],[1665407100000,24],[1665407160000,24],[1665407220000,24],[1665407280000,24],[1665407340000,24],[1665407400000,24]]}]}`,
		},
	}

	// 初始化promEngine
	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		EnableNegativeOffset: true,
	})

	// 准备测试接口
	g := gin.Default()
	g.Use(otelgin.Middleware(trace.ServiceName))

	g.POST("/query/ts", HandleTSQueryRequest)
	g.POST("/query/ts/promql", HandleTsQueryPromQLDataRequest)

	g.POST("/query/ts/struct_to_promql", HandleTsQueryStructToPromQLRequest)
	g.POST("/query/ts/promql_to_struct", HandleTsQueryPromQLToStructRequest)

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, testCase.reqUrl, strings.NewReader(testCase.reqParams))
			g.ServeHTTP(w, req)

			res := w.Body.String()
			if testCase.err != "" {
				assert.NotEqual(t, w.Code, http.StatusOK)
				assert.Equalf(t, testCase.err, res, "url:%s , reqParams:%s ", testCase.reqUrl, testCase.reqParams)
				return
			}
			assert.Equalf(t, testCase.expectResult, res, "url:%s , reqParams:%s ", testCase.reqUrl, testCase.reqParams)
		})
	}

}

// TestBCS_Query
func TestBCS_Query(t *testing.T) {
	testCases := map[string]struct {
		reqParams    string
		expectResult string
		reqUrl       string
		err          string
	}{
		// 测试promql查询接口
		// 测试 bkmonitor:db:table:metric, 可以不带bk_biz_id
		"promql-> db:table without bk_biz_id": {
			reqParams:    `{"promql":"rate(bkmonitor:system:cpu_detail:nice{bk_target_ip=\"127.0.0.1\",device_name=\"cpu0\"}[2m]) - bkmonitor:system:cpu_detail:idle{bk_target_ip=\"127.0.0.1\",device_name=\"cpu0\"}","start":"1629806531","end":"1629810131","step":"600s"}`,
			expectResult: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_biz_id","bk_cloud_id","bk_supplier_id","bk_target_cloud_id","bk_target_ip","device_name","hostname","ip"],"group_values":["2","0","0","0","127.0.0.1","cpu0","VM-1-21-centos","127.0.0.1"],"values":[[1629807000000,-0.7983333333333333],[1629807600000,-0.7],[1629808200000,-0.8],[1629808800000,-0.7],[1629809400000,-0.6]]}]}`,
			reqUrl:       "/query/ts/promql",
		},
		// 测试bkmonitor:metric, bk_biz_id+metric 获取值
		"promql-> bkmonitor:metric bk_biz_id+metric": {
			reqParams:    `{"promql":"bkmonitor:etcd_server_slow_apply_total{bk_biz_id=\"2\"}","start":"1629810830","end":"1629811070","step":"30s"}`,
			expectResult: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["__tmp_prometheus_job_name","bcs_cluster","endpoint","instance","job","monitor_type","namespace","service"],"group_values":["","BCS-K8S-40000","http-metrics","127.0.0.1:2381","kube-etcd","serviceMonitor","kube-system","po-prometheus-operator-kube-etcd"],"values":[[1629810810000,537],[1629810840000,537],[1629810870000,537],[1629810900000,537],[1629810930000,537],[1629810960000,537],[1629810990000,537],[1629811020000,537],[1629811050000,537]]}]}`,
			reqUrl:       "/query/ts/promql",
		},
		// 测试bkmonitor:metric, 不带有bk_biz_id
		"promql-> bkmonitor:metric without bk_biz_id": {
			reqParams: `{"promql":"bkmonitor:etcd_server_slow_apply_total","start":"1629810830","end":"1629811070","step":"30s"}`,
			reqUrl:    "/query/ts/promql",
			err:       `{"error":"bk_biz_id required"}`,
		},
		// 测试结构化查询接口
		// 测试 bkmonitor:db:table:metric, 可以不带bk_biz_id
		"ts-> db:table without bk_biz_id": {
			reqParams:    `{"query_list":[{"data_source":"bkmonitor","concat_name":"bkmonitor:system:cpu_detail:nice","db":"system","table_id":"system.cpu_detail","field_name":"nice","time_aggregation":{"function":"rate","window":"2m0s","position":0,"vargs_list":null},"reference_name":"a","conditions":{"field_list":[{"field_name":"bk_target_ip","value":["127.0.0.1"],"op":"eq"},{"field_name":"device_name","value":["cpu0"],"op":"eq"}],"condition_list":["and"]}},{"data_source":"bkmonitor","concat_name":"bkmonitor:system:cpu_detail:idle","db":"system","table_id":"system.cpu_detail","field_name":"idle","time_aggregation":{"function":"","window":"","position":0,"vargs_list":null},"reference_name":"b","conditions":{"field_list":[{"field_name":"bk_target_ip","value":["127.0.0.1"],"op":"eq"},{"field_name":"device_name","value":["cpu0"],"op":"eq"}],"condition_list":["and"]}}],"metric_merge":"a - b","start_time":"1629806531","end_time":"1629810131","step":"600s"}`,
			expectResult: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bk_biz_id","bk_cloud_id","bk_supplier_id","bk_target_cloud_id","bk_target_ip","device_name","hostname","ip"],"group_values":["2","0","0","0","127.0.0.1","cpu0","VM-1-21-centos","127.0.0.1"],"values":[[1629807000000,-0.7983333333333333],[1629807600000,-0.7],[1629808200000,-0.8],[1629808800000,-0.7],[1629809400000,-0.6]]}]}`,
			reqUrl:       "/query/ts",
		},
		// 测试bkmonitor:metric, bk_biz_id+metric 获取值
		"ts-> bkmonitor:metric bk_biz_id+metric": {
			reqParams:    `{"query_list":[{"data_source":"bkmonitor","concat_name":"bkmonitor:etcd_server_slow_apply_total","field_name":"etcd_server_slow_apply_total","function":null,"time_aggregation":{"function":"","window":"","position":0,"vargs_list":null},"reference_name":"a","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["2"],"op":"eq"}],"condition_list":[]}}],"metric_merge":"a","start_time":"1629810830","end_time":"1629811070","step":"30s"}`,
			expectResult: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["__tmp_prometheus_job_name","bcs_cluster","endpoint","instance","job","monitor_type","namespace","service"],"group_values":["","BCS-K8S-40000","http-metrics","127.0.0.1:2381","kube-etcd","serviceMonitor","kube-system","po-prometheus-operator-kube-etcd"],"values":[[1629810810000,537],[1629810840000,537],[1629810870000,537],[1629810900000,537],[1629810930000,537],[1629810960000,537],[1629810990000,537],[1629811020000,537],[1629811050000,537]]}]}`,
			reqUrl:       "/query/ts",
		},
		// 测试bkmonitor:metric, 不带有bk_biz_id
		"ts-> bkmonitor:metric without bk_biz_id": {
			reqParams: `{"query_list":[{"data_source":"bkmonitor","concat_name":"bkmonitor:etcd_server_slow_apply_total","field_name":"etcd_server_slow_apply_total","function":null,"time_aggregation":{"function":"","window":"","position":0,"vargs_list":null},"reference_name":"a"}],"metric_merge":"a","start_time":"1629810830","end_time":"1629811070","step":"30s"}`,
			reqUrl:    "/query/ts",
			err:       `{"error":"bk_biz_id required"}`,
		},
	}

	// 准备测试数据
	ctrl, stubs := FakePromData(t, true)
	makeRouterInfo(t, ctrl, stubs)

	// 初始化promEngine
	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		EnableNegativeOffset: true,
		LookbackDelta:        2 * time.Minute,
	})

	// 准备测试接口
	g := gin.Default()
	g.POST("/query/ts", HandleTSQueryRequest)
	g.POST("/query/ts/promql", HandleTsQueryPromQLDataRequest)
	g.POST("/query/ts/struct_to_promql", HandleTsQueryStructToPromQLRequest)
	g.POST("/query/ts/promql_to_struct", HandleTsQueryPromQLToStructRequest)

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, testCase.reqUrl, strings.NewReader(testCase.reqParams))
			g.ServeHTTP(w, req)
			if testCase.err != "" {
				assert.Equalf(t, testCase.err, w.Body.String(), "url:%s , reqParams:%s ", testCase.reqUrl, testCase.reqParams)
				return
			}
			assert.Equalf(t, testCase.expectResult, w.Body.String(), "url:%s , reqParams:%s ", testCase.reqUrl, testCase.reqParams)

		})
	}

}
