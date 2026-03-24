// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

const (
	TSpider = "tspider"

	StatusOK     = "00"
	StatusFailed = "-1"

	RUNNING  = "running"
	FINISHED = "finished"

	ContentType   = "Content-Type"
	Authorization = "X-Bkapi-Authorization"
)

type Params struct {
	SQL           string `json:"sql"`
	PreferStorage string `json:"prefer_storage"`
}

type Result struct {
	Result  bool   `json:"result"`
	Message string `json:"message"`
	Code    string `json:"code"`
	Data    any    `json:"data"`
	Errors  struct {
		Error   string `json:"error"`
		QueryId string `json:"query_id"`
	} `json:"errors"`
}

type QueryAsyncData struct {
	TraceId              string         `json:"trace_id"`
	SpanId               string         `json:"span_id"`
	QueryId              string         `json:"query_id"`
	StatementType        string         `json:"statement_type"`
	HasPlan              bool           `json:"has_plan"`
	PreferStorage        string         `json:"prefer_storage"`
	Sql                  string         `json:"sql"`
	ResultTableScanRange map[string]any `json:"result_table_scan_range"`
	ResultTableIds       []string       `json:"result_table_ids"`
	QueryStartTime       string         `json:"query_start_time"`
}

type QuerySyncResultData struct {
	Cluster            string `json:"cluster"`
	TraceId            string `json:"trace_id"`
	TotalRecords       int    `json:"totalRecords"`
	SpanId             string `json:"span_id"`
	ResourceUseSummary struct {
		CpuTimeMills   int `json:"cpu_time_mills"`
		MemoryBytes    int `json:"memory_bytes"`
		ProcessedBytes int `json:"processed_bytes"`
		ProcessedRows  int `json:"processed_rows"`
	} `json:"resource_use_summary"`
	Source               string           `json:"source"`
	List                 []map[string]any `json:"list"`
	SelectFieldsOrder    []string         `json:"select_fields_order"`
	Sql                  string           `json:"sql"`
	TotalRecordSize      int              `json:"total_record_size"`
	Timetaken            float64          `json:"timetaken"`
	BksqlCallElapsedTime int              `json:"bksql_call_elapsed_time"`
	Device               string           `json:"device"`
	ResultTableIds       []string         `json:"result_table_ids"`
	ResultSchema         []map[string]any `json:"result_schema"`
}

type QueryAsyncResultData struct {
	TotalRecords int `json:"totalRecords"`
	Timetaken    struct {
		CheckQuerySyntax          int `json:"checkQuerySyntax"`
		CheckPermission           int `json:"checkPermission"`
		PickValidStorage          int `json:"pickValidStorage"`
		MatchQueryForbiddenConfig int `json:"matchQueryForbiddenConfig"`
		CheckQuerySemantic        int `json:"checkQuerySemantic"`
		MatchQueryRoutingRule     int `json:"matchQueryRoutingRule"`
		ConvertQueryStatement     int `json:"convertQueryStatement"`
		GetQueryDriver            int `json:"getQueryDriver"`
		ConnectDb                 int `json:"connectDb"`
		QueryDb                   int `json:"queryDb"`
		WriteCache                int `json:"writeCache"`
		PersistData               int `json:"persistData"`
		Timetaken                 int `json:"timetaken"`
	} `json:"timetaken"`
	ResultSchema []struct {
		FieldType  string `json:"field_type"`
		FieldName  string `json:"field_name"`
		FieldAlias string `json:"field_alias"`
		FieldIndex int    `json:"field_index"`
	} `json:"result_schema"`
	List              []map[string]any `json:"list"`
	TimeTaken         int              `json:"time_taken"`
	SelectFieldsOrder []string         `json:"select_fields_order"`
	Status            string           `json:"status"`
}

type QueryAsyncStateData struct {
	State   string `json:"state"`
	Message string `json:"message"`
}
