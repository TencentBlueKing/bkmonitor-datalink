// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mock

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"

	"github.com/jarcoal/httpmock"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

type VmRequest struct {
	BkAppCode                  string `json:"bk_app_code"`
	BkUsername                 string `json:"bk_username"`
	BkdataAuthenticationMethod string `json:"bkdata_authentication_method"`
	BkdataDataToken            string `json:"bkdata_data_token"`
	PreferStorage              string `json:"prefer_storage"`
	Sql                        string `json:"sql"`
}

type VmParams struct {
	InfluxCompatible      bool              `json:"influx_compatible"`
	UseNativeOr           bool              `json:"use_native_or"`
	ApiType               string            `json:"api_type"`
	ClusterName           string            `json:"cluster_name"`
	ApiParams             map[string]any    `json:"api_params"`
	ResultTableList       []string          `json:"result_table_list"`
	MetricFilterCondition map[string]string `json:"metric_filter_condition,omitempty"`
}

type QueryRangeParams struct {
	Query string `json:"query"`
	Start int64  `json:"start"`
	End   int64  `json:"end"`
	Step  int64  `json:"step"`
}

type QueryParams struct {
	Query   string `json:"query"`
	Time    int64  `json:"time"`
	Timeout int64  `json:"timeout"`
}

type LabelValuesParams struct {
	Label string `json:"label"`
	Match string `json:"match[]"`
	Start int64  `json:"start"`
	End   int64  `json:"end"`
	Limit int    `json:"limit"`
}

type Data struct {
	List []VmList `json:"list,omitempty"`
	SQL  string   `json:"sql"`
}

type VmList struct {
	Data      any    `json:"data,omitempty"`
	IsPartial bool   `json:"isPartial,omitempty"`
	Status    string `json:"status,omitempty"`
}

// VmResponse 查询返回结构体
type VmResponse struct {
	Result  bool   `json:"result"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Data    Data   `json:"data,omitempty"`
	Errors  struct {
		Error   string `json:"error"`
		QueryId string `json:"query_id"`
	} `json:"errors"`
}

var (
	Vm       = &vmResultData{}
	BkSQL    = &bkSQLResultData{}
	InfluxDB = &influxdbResultData{}
	Es       = &elasticSearchResultData{}
)

type resultData struct {
	lock sync.RWMutex
	data map[string]any
}

func (r *resultData) Set(in map[string]any) {
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.data == nil {
		r.data = make(map[string]any)
	}
	for k, v := range in {
		r.data[k] = v
	}
}

func (r *resultData) Clear() {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.data = make(map[string]any)
}

func (r *resultData) Get(k string) (any, bool) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	d, ok := r.data[k]
	return d, ok
}

type vmResultData struct {
	resultData
}

func (vr *vmResultData) Set(in map[string]any) {
	vr.lock.Lock()
	defer vr.lock.Unlock()
	if vr.data == nil {
		vr.data = make(map[string]any)
	}
	for k, v := range in {
		var data any
		switch v.(type) {
		case string:
			data = v
		default:
			rd := VmResponse{
				Result: true,
				Code:   "00",
			}
			rd.Data.List = append(rd.Data.List, VmList{Data: v})
			data = rd
		}
		vr.data[k] = data
	}
}

type bkSQLResultData struct {
	resultData
}

type influxdbResultData struct {
	resultData
}

type elasticSearchResultData struct {
	resultData
}

func mockHandler(ctx context.Context) {
	httpmock.Activate()

	log.Infof(context.Background(), "mock handler start")

	mockBKBaseHandler(ctx)
	mockInfluxDBHandler(ctx)
	mockElasticSearchHandler(ctx)

	log.Infof(context.Background(), "mock handler end")
}

const (
	EsUrlDomain     = "http://127.0.0.1:93002"
	BkBaseUrlDomain = "http://127.0.0.1:12001"
)

const (
	EsUrl     = EsUrlDomain
	BkBaseUrl = BkBaseUrlDomain + "/bk_data/query_sync"
)

var FieldType = map[string]string{
	"a":                        "keyword",
	"b":                        "keyword",
	"level":                    "keyword",
	"dtEventTimeStamp":         "date",
	"events":                   "nested",
	"events.name":              "keyword",
	"group":                    "keyword",
	"kibana_stats.kibana.name": "keyword",
	"time":                     "date",
	"timestamp":                "text",
	"type":                     "keyword",
	"user":                     "nested",
	"user.first":               "keyword",
	"user.last":                "keyword",
}

type BkSQLRequest struct {
	BkAppCode                  string `json:"bk_app_code"`
	BkUsername                 string `json:"bk_username"`
	BkdataAuthenticationMethod string `json:"bkdata_authentication_method"`
	BkdataDataToken            string `json:"bkdata_data_token"`
	Sql                        string `json:"sql"`
}

func mockElasticSearchHandler(ctx context.Context) {
	bkBaseEsUrl := BkBaseUrl + "/es"

	searchHandler := func(r *http.Request) (w *http.Response, err error) {
		body, _ := io.ReadAll(r.Body)

		d, ok := Es.Get(string(body))
		if !ok {
			err = fmt.Errorf(`es mock data is empty in "%s"`, body)
			log.Errorf(ctx, err.Error())
			return w, err
		}
		w = httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf("%s", d))
		return w, err
	}

	mappings := `{"es_index":{"mappings":{"properties":{"a":{"type":"keyword"},"time":{"type":"date"},"b":{"type":"keyword"},"level":{"type":"keyword"},"group":{"type":"keyword"},"kibana_stats":{"properties":{"kibana":{"properties":{"name":{"type":"keyword"}}}}},"timestamp":{"type":"text"},"type":{"type":"keyword"},"dtEventTimeStamp":{"type":"date"},"user":{"type":"nested","properties":{"first":{"type":"keyword"},"last":{"type":"keyword"}}},"events":{"type":"nested","properties":{"name":{"type":"keyword"}}}}}}}`
	mappingResp := httpmock.NewStringResponder(http.StatusOK, mappings)
	httpmock.RegisterResponder(http.MethodGet, bkBaseEsUrl+"/es_index/_mapping/", mappingResp)
	httpmock.RegisterResponder(http.MethodGet, EsUrl+"/es_index/_mapping/", mappingResp)

	httpmock.RegisterResponder(http.MethodPost, bkBaseEsUrl+"/es_index/_search", searchHandler)
	httpmock.RegisterResponder(http.MethodPost, EsUrl+"/es_index/_search", searchHandler)

	httpmock.RegisterResponder(http.MethodPost, bkBaseEsUrl+"/es_index/_search?scroll=5m", searchHandler)
	httpmock.RegisterResponder(http.MethodPost, EsUrl+"/es_index/_search?scroll=5m", searchHandler)

	httpmock.RegisterResponder(http.MethodPost, bkBaseEsUrl+"/_search/scroll", searchHandler)
	httpmock.RegisterResponder(http.MethodPost, EsUrl+"/_search/scroll", searchHandler)

	httpmock.RegisterResponder(http.MethodHead, EsUrl, func(request *http.Request) (*http.Response, error) {
		return httpmock.NewStringResponse(http.StatusOK, ""), nil
	})
}

func mockInfluxDBHandler(ctx context.Context) {
	host1 := "http://127.0.0.1:6371"
	host2 := "http://127.0.0.2:6371"

	httpmock.RegisterResponder(http.MethodGet, host1+"/ping", httpmock.NewBytesResponder(http.StatusNoContent, nil))
	httpmock.RegisterResponder(http.MethodGet, host2+"/ping", httpmock.NewBytesResponder(http.StatusBadGateway, nil))

	address := "http://127.0.0.1:12302"
	httpmock.RegisterResponder(http.MethodGet, address+"/query", func(r *http.Request) (w *http.Response, err error) {
		params, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return w, err
		}
		key := params.Get("q")
		d, ok := InfluxDB.Get(key)
		if !ok {
			err = fmt.Errorf(`influxdb mock data is empty in "%s"`, key)
			return w, err
		}

		switch t := d.(type) {
		case string:
			w = httpmock.NewStringResponse(http.StatusOK, t)
		default:
			w, err = httpmock.NewJsonResponse(http.StatusOK, d)
		}
		return w, err
	})
}

func mockBKBaseHandler(ctx context.Context) {

	BkSQL.Set(map[string]any{
		"SHOW CREATE TABLE `2_bklog_bkunify_query_doris`.doris": `{"result":true,"message":"成功","code":"00","data":{"result_table_scan_range":{},"cluster":"doris-test","totalRecords":18,"external_api_call_time_mills":{"bkbase_auth_api":43,"bkbase_meta_api":0,"bkbase_apigw_api":33},"resource_use_summary":{"cpu_time_mills":0,"memory_bytes":0,"processed_bytes":0,"processed_rows":0},"source":"","list":[{"Field":"thedate","Type":"int","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtimestamp","Type":"bigint","Null":"NO","Key":"YES","Default":null,"Extra":""},{"Field":"dteventtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"localtime","Type":"varchar(32)","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__shard_key__","Type":"bigint","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"__ext","Type":"variant","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"cloudid","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"file","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"gseindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"iterationindex","Type":"double","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"level","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"log","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"message","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"path","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"report_time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"serverip","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"time","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"},{"Field":"trace_id","Type":"text","Null":"YES","Key":"NO","Default":null,"Extra":"NONE"}],"stage_elapsed_time_mills":{"check_query_syntax":0,"query_db":5,"get_query_driver":0,"match_query_forbidden_config":0,"convert_query_statement":2,"connect_db":45,"match_query_routing_rule":0,"check_permission":43,"check_query_semantic":0,"pick_valid_storage":1},"select_fields_order":["Field","Type","Null","Key","Default","Extra"],"sql":"SHOW COLUMNS FROM mapleleaf_2.bklog_bkunify_query_doris_2","total_record_size":11776,"timetaken":0.096,"result_schema":[{"field_type":"string","field_name":"Field","field_alias":"Field","field_index":0},{"field_type":"string","field_name":"Type","field_alias":"Type","field_index":1},{"field_type":"string","field_name":"Null","field_alias":"Null","field_index":2},{"field_type":"string","field_name":"Key","field_alias":"Key","field_index":3},{"field_type":"string","field_name":"Default","field_alias":"Default","field_index":4},{"field_type":"string","field_name":"Extra","field_alias":"Extra","field_index":5}],"bksql_call_elapsed_time":0,"device":"doris","result_table_ids":["2_bklog_bkunify_query_doris"]},"errors":null,"trace_id":"00000000000000000000000000000000","span_id":"0000000000000000"}`,
	})

	httpmock.RegisterResponder(http.MethodPost, BkBaseUrl, func(r *http.Request) (w *http.Response, err error) {
		var (
			request VmRequest
			params  VmParams
		)
		err = json.NewDecoder(r.Body).Decode(&request)
		if err != nil {
			return w, err
		}

		if request.PreferStorage != "vm" {
			d, ok := BkSQL.Get(request.Sql)
			if !ok {
				err = fmt.Errorf(`bksql mock data is empty in "%s"`, request.Sql)
				log.Errorf(ctx, err.Error())
				return w, err
			}
			switch t := d.(type) {
			case string:
				w = httpmock.NewStringResponse(http.StatusOK, t)
			default:
				w, err = httpmock.NewJsonResponse(http.StatusOK, d)
			}

			return w, err
		}

		err = json.Unmarshal([]byte(request.Sql), &params)
		if err != nil {
			return w, err
		}

		p := params.ApiParams
		var key string
		switch params.ApiType {
		case "series":
			fallthrough
		case "labels":
			key = fmt.Sprintf("%.f%.f%s", p["start"], p["end"], p["match[]"])
		case "label_values":
			key = fmt.Sprintf("%.f%.f%s%s", p["start"], p["end"], p["label"], p["match[]"])
		case "query_range":
			key = fmt.Sprintf("%.f%.f%.f%s", p["start"], p["end"], p["step"], p["query"])
		case "query":
			key = fmt.Sprintf("%.f%s", p["time"], p["query"])
		default:
			err = fmt.Errorf("api type %s is empty ", params.ApiType)
			return w, err
		}

		key = params.ApiType + ":" + key
		d, ok := Vm.Get(key)
		if !ok {
			err = fmt.Errorf(`vm mock data is empty in "%s"`, key)
			return w, err
		}

		switch v := d.(type) {
		case string:
			w = httpmock.NewStringResponse(http.StatusOK, v)
		default:
			w, err = httpmock.NewJsonResponse(http.StatusOK, v)
		}
		return w, err
	})
}
