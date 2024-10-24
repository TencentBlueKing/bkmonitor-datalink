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
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/jarcoal/httpmock"
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
	InfluxCompatible bool   `json:"influx_compatible"`
	UseNativeOr      bool   `json:"use_native_or"`
	ApiType          string `json:"api_type"`
	ClusterName      string `json:"cluster_name"`
	ApiParams        struct {
		Query   string `json:"query"`
		Start   int64  `json:"start"`
		End     int64  `json:"end"`
		Step    int64  `json:"step"`
		Time    int    `json:"time"`
		Timeout int    `json:"timeout"`
	} `json:"api_params"`
	ResultTableList       []string          `json:"result_table_list"`
	MetricFilterCondition map[string]string `json:"metric_filter_condition"`
}

type Metric map[string]string
type Value []interface{}

type Series struct {
	Metric Metric  `json:"metric"`
	Value  Value   `json:"value,omitempty"`
	Values []Value `json:"values,omitempty"`
}

type Data struct {
	Result     []Series `json:"result"`
	ResultType string   `json:"resultType,omitempty"`
}

// VmResponse 查询返回结构体
type VmResponse struct {
	Result  bool   `json:"result"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Data    struct {
		ResultTableScanRange interface{} `json:"result_table_scan_range"`
		Cluster              string      `json:"cluster"`
		TotalRecords         int         `json:"totalRecords"`
		Timetaken            float64     `json:"timetaken"`
		List                 []struct {
			Data      Data   `json:"data,omitempty""`
			IsPartial bool   `json:"isPartial,omitempty"`
			Status    string `json:"status,omitempty"`
		} `json:"list,omitempty""`
		BksqlCallElapsedTime int           `json:"bksql_call_elapsed_time"`
		Device               string        `json:"device"`
		ResultTableIds       []string      `json:"result_table_ids"`
		SelectFieldsOrder    []interface{} `json:"select_fields_order"`
		SQL                  string        `json:"sql"`
	} `json:"data,omitempty"`
	Errors struct {
		Error   string `json:"error"`
		QueryId string `json:"query_id"`
	} `json:"errors"`
}

var (
	lock sync.RWMutex
	data map[string]VmResponse
)

func SetVmMockData(data map[string]VmResponse) {
	lock.Lock()
	defer lock.Unlock()
	for k, v := range data {
		data[k] = v
	}
}

func mockVmHandler(ctx context.Context) {
	url := "http://127.0.0.1:12001/bk_data/query_sync"
	httpmock.Activate()

	httpmock.RegisterResponder(http.MethodPost, url, func(r *http.Request) (w *http.Response, err error) {
		var (
			request VmRequest
			params  VmParams
		)
		err = json.NewDecoder(r.Body).Decode(&request)
		if err != nil {
			return
		}

		err = json.Unmarshal([]byte(request.Sql), &params)
		if err != nil {
			return
		}

		var key string
		switch params.ApiType {
		case "query":
			key = fmt.Sprintf("%d%s", params.ApiParams.Time, params.ApiParams.Query)
		}

		lock.RLock()
		defer lock.RUnlock()
		w, err = httpmock.NewJsonResponse(http.StatusOK, data[key])
		return
	})

}
