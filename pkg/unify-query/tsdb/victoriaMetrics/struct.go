// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package victoriaMetrics

import (
	"fmt"
	"strconv"
)

type Params struct {
	SQL                        string `json:"sql"`
	BkdataAuthenticationMethod string `json:"bkdata_authentication_method"`
	BkUsername                 string `json:"bk_username"`
	BkAppCode                  string `json:"bk_app_code"`
	PreferStorage              string `json:"prefer_storage"`
	BkdataDataToken            string `json:"bkdata_data_token"`
	BkAppSecret                string `json:"bk_app_secret"`
}

type ParamsQueryRange struct {
	InfluxCompatible bool   `json:"influx_compatible"`
	UseNativeOr      bool   `json:"use_native_or"`
	APIType          string `json:"api_type"`
	APIParams        struct {
		Query string `json:"query"`
		Start int64  `json:"start"`
		End   int64  `json:"end"`
		Step  int64  `json:"step"`
	} `json:"api_params"`
	ResultTableList       []string          `json:"result_table_list"`
	MetricFilterCondition map[string]string `json:"metric_filter_condition"`
}

type ParamsQuery struct {
	InfluxCompatible bool   `json:"influx_compatible"`
	UseNativeOr      bool   `json:"use_native_or"`
	APIType          string `json:"api_type"`
	APIParams        struct {
		Query   string `json:"query"`
		Time    int64  `json:"time"`
		Timeout int64  `json:"timeout"`
	} `json:"api_params"`
	ResultTableList       []string          `json:"result_table_list"`
	MetricFilterCondition map[string]string `json:"metric_filter_condition"`
}

type ParamsSeries struct {
	InfluxCompatible bool   `json:"influx_compatible"`
	UseNativeOr      bool   `json:"use_native_or"`
	APIType          string `json:"api_type"`
	APIParams        struct {
		Match string `json:"match[]"`
		Start int64  `json:"start"`
		End   int64  `json:"end"`
	} `json:"api_params"`
	ResultTableList       []string          `json:"result_table_list"`
	MetricFilterCondition map[string]string `json:"metric_filter_condition"`
}

type ParamsLabelName struct {
	InfluxCompatible bool   `json:"influx_compatible"`
	UseNativeOr      bool   `json:"use_native_or"`
	APIType          string `json:"api_type"`
	APIParams        struct {
		Match string `json:"match[]"`
		Start int64  `json:"start"`
		End   int64  `json:"end"`
	} `json:"api_params"`
	ResultTableList       []string          `json:"result_table_list"`
	MetricFilterCondition map[string]string `json:"metric_filter_condition"`
}

type ParamsLabelValues struct {
	InfluxCompatible bool   `json:"influx_compatible"`
	UseNativeOr      bool   `json:"use_native_or"`
	APIType          string `json:"api_type"`
	APIParams        struct {
		Label string `json:"label"`
	} `json:"api_params"`
	ResultTableList       []string          `json:"result_table_list"`
	MetricFilterCondition map[string]string `json:"metric_filter_condition"`
}

type Metric map[string]string

type Value []interface{}

func (f Value) Point() (t int64, v float64, err error) {
	if len(f) != 2 {
		err = fmt.Errorf("%+v length is not 2", f)
		return
	}

	switch pt := f[0].(type) {
	case float64:
		// 从秒转换为毫秒
		t = int64(pt) * 1e3
	default:
		err = fmt.Errorf("%+v type is not float64", f[0])
		return
	}

	// 值从 string 转换为 float64
	switch pv := f[1].(type) {
	case string:
		v, err = strconv.ParseFloat(pv, 64)
		if err != nil {
			err = fmt.Errorf("%+v %s", f[1], err)
			return
		}
	default:
		err = fmt.Errorf("%+v type is not string", f[0])
		return
	}

	return
}

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

// VmLableValuesResponse 查询返回结构体
type VmLableValuesResponse struct {
	Result  bool   `json:"result"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Data    struct {
		ResultTableScanRange interface{} `json:"result_table_scan_range"`
		Cluster              string      `json:"cluster"`
		TotalRecords         int         `json:"totalRecords"`
		Timetaken            float64     `json:"timetaken"`
		List                 []struct {
			Data      []string `json:"data,omitempty""`
			IsPartial bool     `json:"isPartial,omitempty"`
			Status    string   `json:"status,omitempty"`
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

// VmSeriesResponse 查询返回结构体
type VmSeriesResponse struct {
	Result  bool   `json:"result"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Data    struct {
		ResultTableScanRange interface{} `json:"result_table_scan_range"`
		Cluster              string      `json:"cluster"`
		TotalRecords         int         `json:"totalRecords"`
		Timetaken            float64     `json:"timetaken"`
		List                 []struct {
			Data      []map[string]string `json:"data,omitempty""`
			IsPartial bool                `json:"isPartial,omitempty"`
			Status    string              `json:"status,omitempty"`
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
