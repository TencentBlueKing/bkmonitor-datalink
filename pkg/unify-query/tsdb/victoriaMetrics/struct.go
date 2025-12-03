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

	"github.com/spf13/cast"
)

type ParamsQueryRange struct {
	BkBizID          string `json:"bk_biz_id,omitempty"`
	InfluxCompatible bool   `json:"influx_compatible"`
	UseNativeOr      bool   `json:"use_native_or"`
	APIType          string `json:"api_type"`
	ClusterName      string `json:"cluster_name"`
	APIParams        struct {
		Query   string `json:"query"`
		Start   int64  `json:"start"`
		End     int64  `json:"end"`
		Step    int64  `json:"step"`
		NoCache int    `json:"nocache"`
	} `json:"api_params"`
	ResultTableList       []string          `json:"result_table_list,omitempty"`
	MetricFilterCondition map[string]string `json:"metric_filter_condition,omitempty"`
}

type ParamsQuery struct {
	BkBizID          string `json:"bk_biz_id,omitempty"`
	InfluxCompatible bool   `json:"influx_compatible"`
	UseNativeOr      bool   `json:"use_native_or"`
	APIType          string `json:"api_type"`
	ClusterName      string `json:"cluster_name"`
	APIParams        struct {
		Query   string `json:"query"`
		Time    int64  `json:"time"`
		Timeout int64  `json:"timeout"`
	} `json:"api_params"`
	ResultTableList       []string          `json:"result_table_list,omitempty"`
	MetricFilterCondition map[string]string `json:"metric_filter_condition,omitempty"`
}

type ParamsSeries struct {
	BkBizID          string `json:"bk_biz_id,omitempty"`
	InfluxCompatible bool   `json:"influx_compatible"`
	UseNativeOr      bool   `json:"use_native_or"`
	APIType          string `json:"api_type"`
	ClusterName      string `json:"cluster_name"`
	APIParams        struct {
		Match string `json:"match[]"`
		Start int64  `json:"start"`
		End   int64  `json:"end"`
		Limit int    `json:"limit"`
	} `json:"api_params"`
	ResultTableList       []string          `json:"result_table_list,omitempty"`
	MetricFilterCondition map[string]string `json:"metric_filter_condition,omitempty"`
}

type ParamsLabelName struct {
	InfluxCompatible bool   `json:"influx_compatible"`
	UseNativeOr      bool   `json:"use_native_or"`
	APIType          string `json:"api_type"`
	ClusterName      string `json:"cluster_name"`
	APIParams        struct {
		Match string `json:"match[]"`
		Start int64  `json:"start"`
		End   int64  `json:"end"`
	} `json:"api_params"`
	ResultTableList       []string          `json:"result_table_list,omitempty"`
	MetricFilterCondition map[string]string `json:"metric_filter_condition,omitempty"`
}

type ParamsLabelValues struct {
	BkBizID          string `json:"bk_biz_id,omitempty"`
	InfluxCompatible bool   `json:"influx_compatible"`
	UseNativeOr      bool   `json:"use_native_or"`
	APIType          string `json:"api_type"`
	ClusterName      string `json:"cluster_name"`
	APIParams        struct {
		Label string `json:"label"`
		Match string `json:"match[]"`
		Start int64  `json:"start"`
		End   int64  `json:"end"`
		Limit int    `json:"limit"`
	} `json:"api_params"`
	ResultTableList []string `json:"result_table_list,omitempty"`
}

type Metric map[string]string

type Value []any

func (f Value) Point() (t int64, v float64, err error) {
	if len(f) != 2 {
		err = fmt.Errorf("%+v length is not 2", f)
		return t, v, err
	}

	t, err = cast.ToInt64E(f[0])
	if err != nil {
		return t, v, err
	}

	// 从秒转换为毫秒
	t = t * 1e3

	// 值从 string 转换为 float64
	v, err = cast.ToFloat64E(f[1])

	return t, v, err
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
		ResultTableScanRange any     `json:"result_table_scan_range"`
		Cluster              string  `json:"cluster"`
		TotalRecords         int     `json:"totalRecords"`
		Timetaken            float64 `json:"timetaken"`
		List                 []struct {
			Data      Data   `json:"data,omitempty"`
			IsPartial bool   `json:"isPartial,omitempty"`
			Status    string `json:"status,omitempty"`
		} `json:"list,omitempty"`
		BkBizIDs             []any    `json:"bk_biz_ids,omitempty"`
		BksqlCallElapsedTime int      `json:"bksql_call_elapsed_time"`
		Device               string   `json:"device"`
		ResultTableIds       []string `json:"result_table_ids"`
		SelectFieldsOrder    []any    `json:"select_fields_order"`
		SQL                  string   `json:"sql"`
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
		ResultTableScanRange any     `json:"result_table_scan_range"`
		Cluster              string  `json:"cluster"`
		TotalRecords         int     `json:"totalRecords"`
		Timetaken            float64 `json:"timetaken"`
		List                 []struct {
			Data      []string `json:"data,omitempty"`
			IsPartial bool     `json:"isPartial,omitempty"`
			Status    string   `json:"status,omitempty"`
		} `json:"list,omitempty"`
		BkBizIDs             []string `json:"bk_biz_ids,omitempty"`
		BksqlCallElapsedTime int      `json:"bksql_call_elapsed_time"`
		Device               string   `json:"device"`
		ResultTableIds       []string `json:"result_table_ids"`
		SelectFieldsOrder    []any    `json:"select_fields_order"`
		SQL                  string   `json:"sql"`
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
		ResultTableScanRange any     `json:"result_table_scan_range"`
		Cluster              string  `json:"cluster"`
		TotalRecords         int     `json:"totalRecords"`
		Timetaken            float64 `json:"timetaken"`
		List                 []struct {
			Data      []map[string]string `json:"data,omitempty"`
			IsPartial bool                `json:"isPartial,omitempty"`
			Status    string              `json:"status,omitempty"`
		} `json:"list,omitempty"`
		BkBizIDs             []string `json:"bk_biz_ids,omitempty"`
		BksqlCallElapsedTime int      `json:"bksql_call_elapsed_time"`
		Device               string   `json:"device"`
		ResultTableIds       []string `json:"result_table_ids"`
		SelectFieldsOrder    []any    `json:"select_fields_order"`
		SQL                  string   `json:"sql"`
	} `json:"data,omitempty"`
	Errors struct {
		Error   string `json:"error"`
		QueryId string `json:"query_id"`
	} `json:"errors"`
}
