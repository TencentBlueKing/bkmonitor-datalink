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
	Vm    = &vmResultData{}
	BkSQL = &bkSQLResultData{}
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
		rd := VmResponse{
			Result: true,
			Code:   "00",
		}
		rd.Data.List = append(rd.Data.List, VmList{Data: v})
		vr.data[k] = rd
	}
}

type bkSQLResultData struct {
	resultData
}

func mockHandler(ctx context.Context) {
	httpmock.Activate()

	mockVmHandler(ctx)
	mockInfluxDBHandler(ctx)
	mockBkSQL(ctx)
}

const (
	BkSQLUrl = "http://127.0.0.1:92001"
)

type BkSQLRequest struct {
	BkAppCode                  string `json:"bk_app_code"`
	BkUsername                 string `json:"bk_username"`
	BkdataAuthenticationMethod string `json:"bkdata_authentication_method"`
	BkdataDataToken            string `json:"bkdata_data_token"`
	Sql                        string `json:"sql"`
}

func mockBkSQL(ctx context.Context) {
	httpmock.RegisterResponder(http.MethodPost, BkSQLUrl, func(r *http.Request) (w *http.Response, err error) {
		var (
			request BkSQLRequest
		)
		err = json.NewDecoder(r.Body).Decode(&request)
		if err != nil {
			return
		}

		d, ok := BkSQL.Get(request.Sql)
		if !ok {
			err = fmt.Errorf(`bksql mock data is empty in "%s"`, request.Sql)
			return
		}
		w, err = httpmock.NewJsonResponse(http.StatusOK, d)
		return
	})
}

func mockInfluxDBHandler(ctx context.Context) {
	host1 := "http://127.0.0.1:6371"
	host2 := "http://127.0.0.2:6371"

	httpmock.RegisterResponder(http.MethodGet, host1+"/ping", httpmock.NewBytesResponder(http.StatusNoContent, nil))
	httpmock.RegisterResponder(http.MethodGet, host2+"/ping", httpmock.NewBytesResponder(http.StatusBadGateway, nil))
}

func mockVmHandler(ctx context.Context) {
	url := "http://127.0.0.1:12001/bk_data/query_sync"

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

		d, ok := Vm.Get(key)
		if !ok {
			err = fmt.Errorf(`vm mock data is empty in "%s"`, key)
			return
		}
		w, err = httpmock.NewJsonResponse(http.StatusOK, d)
		return
	})

}
