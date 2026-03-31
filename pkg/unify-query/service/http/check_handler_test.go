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
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	uquery "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// vmCheckMetricqlTestCtx 仅用于 vmCheckMetricql 单测：不跑全量路由，只满足 ToTime / ToPromExpr 所需上下文。
func vmCheckMetricqlTestCtx(t *testing.T) context.Context {
	t.Helper()
	mock.Init()
	promql.MockEngine()
	ctx := metadata.InitHashID(context.Background())
	metadata.GetQueryParams(ctx).SetStorageType(metadata.VictoriaMetricsStorageType)
	return ctx
}

// queryTsMinimalForVmCheck 构造最小 QueryTs，reference 名与 metric_merge 一致，供 vmCheckMetricql + 手工 qr 使用。
func queryTsMinimalForVmCheck(t *testing.T, ctx context.Context, metricMerge string, refNames ...string) *structured.QueryTs {
	t.Helper()
	ql := make([]*structured.Query, 0, len(refNames))
	for _, r := range refNames {
		ql = append(ql, &structured.Query{
			ReferenceName: r,
			TableID:       "stub.table",
			FieldName:     "stub",
			Step:          "60s",
		})
	}
	qts := &structured.QueryTs{
		MetricMerge: metricMerge,
		Start:       "1718865258",
		End:         "1718868858",
		Step:        "60s",
		Timezone:    "UTC",
		QueryList:   ql,
	}
	require.NoError(t, qts.ToTime(ctx))
	return qts
}

// e2eCheckContext 与 query 单测一致：mock 路由 + 空间用户，保证 ToQueryReference 与 VM 展开可用。
func e2eCheckContext(t *testing.T) context.Context {
	t.Helper()
	mock.Init()
	promql.MockEngine()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)
	metadata.SetUser(ctx, &metadata.User{SpaceUID: influxdb.SpaceUid})
	return ctx
}

func runCheckHandler(t *testing.T, req *http.Request, fn func(*gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	fn(c)
	return w
}

// expectedCheckVMPreview 与 checkQueryTsData 直查路径一致：checkQueryTsToReference + vmCheckMetricql + ToVmExpand。
func expectedCheckVMPreview(ctx context.Context, t *testing.T, qts *structured.QueryTs) (metricql string, resultTableIDs []string) {
	t.Helper()
	qr, err := checkQueryTsToReference(ctx, qts)
	require.NoError(t, err)
	metricql, err = vmCheckMetricql(ctx, qts, qr)
	require.NoError(t, err)
	vmExpand := uquery.ToVmExpand(ctx, qr)
	require.NotNil(t, vmExpand)
	require.NotEmpty(t, vmExpand.ResultTableList)
	return metricql, append([]string(nil), vmExpand.ResultTableList...)
}

func resultTableIDFromResponse(t *testing.T, v any) []string {
	t.Helper()
	arr, ok := v.([]any)
	require.True(t, ok, "result_table_id 应为 JSON 数组")
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		s, ok := x.(string)
		require.True(t, ok, "result_table_id 元素应为字符串")
		out = append(out, s)
	}
	return out
}

func TestEndToEndHandlerCheckQueryTs_Success_VMPreview(t *testing.T) {
	ctx := e2eCheckContext(t)
	// system.cpu_detail / system.disk 在 router_mock 中走 must-vm-query，路由为 VM；IsDirectQuery 为 true，返回单条 VM 预览。
	body := []byte(`{
		"space_uid": "bkcc__2",
		"query_list": [
			{"table_id": "system.cpu_detail", "field_name": "usage", "reference_name": "a"},
			{"table_id": "system.disk", "field_name": "usage", "reference_name": "b"}
		],
		"metric_merge": "a + b",
		"start_time": "1718865258",
		"end_time": "1718868858",
		"step": "1m"
	}`)
	var qts structured.QueryTs
	require.NoError(t, json.Unmarshal(body, &qts))
	wantMql, wantRT := expectedCheckVMPreview(ctx, t, &qts)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/check/query/ts", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	w := runCheckHandler(t, req, HandlerCheckQueryTs)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp struct {
		Data    []map[string]any `json:"data"`
		TraceID string           `json:"trace_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1, "直查 VM 路径应产出一条预览")
	assert.Equal(t, metadata.VictoriaMetricsStorageType, resp.Data[0]["storage_type"])
	gotMql, ok := resp.Data[0]["metricql"].(string)
	require.True(t, ok)
	assert.Equal(t, wantMql, gotMql)
	assert.Equal(t, wantRT, resultTableIDFromResponse(t, resp.Data[0]["result_table_id"]))
}

func TestEndToEndHandlerCheckQueryTs_Success_VMPreview_Division(t *testing.T) {
	ctx := e2eCheckContext(t)
	body := []byte(`{
		"space_uid": "bkcc__2",
		"query_list": [
			{"table_id": "system.cpu_detail", "field_name": "usage", "reference_name": "a"},
			{"table_id": "system.disk", "field_name": "usage", "reference_name": "b"}
		],
		"metric_merge": "a / b",
		"start_time": "1718865258",
		"end_time": "1718868858",
		"step": "1m"
	}`)
	var qts structured.QueryTs
	require.NoError(t, json.Unmarshal(body, &qts))
	wantMql, wantRT := expectedCheckVMPreview(ctx, t, &qts)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/check/query/ts", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	w := runCheckHandler(t, req, HandlerCheckQueryTs)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp struct {
		Data    []map[string]any `json:"data"`
		TraceID string           `json:"trace_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	gotMql, ok := resp.Data[0]["metricql"].(string)
	require.True(t, ok)
	assert.Equal(t, wantMql, gotMql)
	assert.Contains(t, gotMql, "/", "除法运算符应保留")
	assert.Equal(t, wantRT, resultTableIDFromResponse(t, resp.Data[0]["result_table_id"]))
}

func TestEndToEndHandlerCheckQueryTs_Success_MetricMergeSingleRef(t *testing.T) {
	ctx := e2eCheckContext(t)
	// 单引用 metric_merge=a：展开后 metricql 为单路 PromQL，仍为 VM 直查预览一条。
	body := []byte(`{
		"space_uid": "bkcc__2",
		"query_list": [
			{"table_id": "system.cpu_detail", "field_name": "usage", "reference_name": "a"}
		],
		"metric_merge": "a",
		"start_time": "1718865258",
		"end_time": "1718868858",
		"step": "1m"
	}`)
	var qtsSingle structured.QueryTs
	require.NoError(t, json.Unmarshal(body, &qtsSingle))
	wantMql, wantRT := expectedCheckVMPreview(ctx, t, &qtsSingle)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/check/query/ts", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	w := runCheckHandler(t, req, HandlerCheckQueryTs)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp struct {
		Data    []map[string]any `json:"data"`
		TraceID string           `json:"trace_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	assert.Equal(t, metadata.VictoriaMetricsStorageType, resp.Data[0]["storage_type"])
	gotMql, ok := resp.Data[0]["metricql"].(string)
	require.True(t, ok)
	assert.Equal(t, wantMql, gotMql)
	assert.Equal(t, wantRT, resultTableIDFromResponse(t, resp.Data[0]["result_table_id"]))
}

// TestVmCheckMetricql_metricFilterCondition 与 internal/query/query_reference_test 中 VmCondition 风格一致：
// metricql 中花括号内容应等于 ToVmExpand(ctx, qr).MetricFilterCondition[ref]（单段或 or 拼接），而非手写另一套标签。
func TestVmCheckMetricql_metricFilterCondition(t *testing.T) {
	for name, tc := range map[string]struct {
		metricMerge string
		refs        []string
		qr          metadata.QueryReference
		// assertExactBrace 为 true 时要求整段 {MetricFilterCondition} 连续出现（单 ref 或 or 顺序与 ToVmExpand 一致时用）
		assertExactBrace map[string]bool
		// wantSubstrings 顺序无关地检查子串（多 or 且 set 顺序不稳定时用）
		wantSubstrings []string
	}{
		"disk_style_single_a": {
			metricMerge: "a",
			refs:        []string{"a"},
			qr: metadata.QueryReference{
				"a": {{
					QueryList: []*metadata.Query{
						{
							VmRt:        "100147_ieod_system_disk_raw",
							VmCondition: `bk_biz_id="2", result_table_id="100147_ieod_system_disk_raw", __name__="usage_value"`,
						},
					},
				}},
			},
			assertExactBrace: map[string]bool{"a": true},
		},
		"default1_style_ab": {
			metricMerge: "a + b",
			refs:        []string{"a", "b"},
			qr: metadata.QueryReference{
				"a": {{
					QueryList: []*metadata.Query{
						{
							VmRt:        "vm_result_table",
							VmCondition: `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table"`,
						},
						{
							VmRt:        "vm_result_table_1",
							VmCondition: `__name__="bkmonitor:container_cpu_usage_seconds_total_value", result_table_id="vm_result_table_1"`,
						},
					},
				}},
				"b": {{
					QueryList: []*metadata.Query{
						{
							VmRt:        "vm_result_table",
							VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table"`,
						},
						{
							VmRt:        "vm_result_table_1",
							VmCondition: `__name__="bkmonitor:kube_pod_container_resource_requests_value", result_table_id="vm_result_table_1"`,
						},
					},
				}},
			},
			wantSubstrings: []string{
				`__name__="bkmonitor:container_cpu_usage_seconds_total_value"`,
				`__name__="bkmonitor:kube_pod_container_resource_requests_value"`,
				`result_table_id="vm_result_table"`,
				`result_table_id="vm_result_table_1"`,
			},
		},
		// 复杂 metric_merge：多组 sum by、除法、常数；与 attributes.query-match 风格一致，验证 \ba\b/\bb\b 只替换裸引用。
		"complex_sum_by_div_mul_daemonset": {
			metricMerge: `sum by (bcs_cluster_id, namespace, daemonset) (a) / sum by (bcs_cluster_id, namespace, daemonset) (b) * 100`,
			refs:        []string{"a", "b"},
			qr: metadata.QueryReference{
				"a": {{
					QueryList: []*metadata.Query{
						{
							VmRt:        "k8s_workload_daemonset",
							VmCondition: `__name__="bkmonitor:daemonset_current_scheduled_value", result_table_id="k8s_workload_daemonset", bcs_cluster_id="cls-demo"`,
						},
					},
				}},
				"b": {{
					QueryList: []*metadata.Query{
						{
							VmRt:        "k8s_workload_daemonset",
							VmCondition: `__name__="bkmonitor:daemonset_desired_scheduled_value", result_table_id="k8s_workload_daemonset", bcs_cluster_id="cls-demo"`,
						},
					},
				}},
			},
			assertExactBrace: map[string]bool{"a": true, "b": true},
			wantSubstrings: []string{
				`sum by (bcs_cluster_id, namespace, daemonset)`,
				` / `,
				`* 100`,
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := vmCheckMetricqlTestCtx(t)
			qts := queryTsMinimalForVmCheck(t, ctx, tc.metricMerge, tc.refs...)
			got, err := vmCheckMetricql(ctx, qts, tc.qr)
			require.NoError(t, err)

			vmExp := uquery.ToVmExpand(ctx, tc.qr)
			require.NotNil(t, vmExp)

			for ref, exact := range tc.assertExactBrace {
				if !exact {
					continue
				}
				f := vmExp.MetricFilterCondition[ref]
				require.NotEmpty(t, f, "ref %s", ref)
				assert.Contains(t, got, "{"+f+"}", "metricql 应嵌入 MetricFilterCondition[%s]", ref)
			}
			for _, sub := range tc.wantSubstrings {
				assert.Contains(t, got, sub)
			}
		})
	}
}

// TestEndToEndHandlerCheckQueryTs_MetricFilterCondition_diskRaw 走真实 check + mock 路由：metricql 中花括号须含与 query_reference_test 同类的 Vm 标签（bk_biz_id、result_table_id、__name__）。
func TestEndToEndHandlerCheckQueryTs_MetricFilterCondition_diskRaw(t *testing.T) {
	ctx := e2eCheckContext(t)
	body := []byte(`{
		"space_uid": "bkcc__2",
		"query_list": [{
			"table_id": "system.disk",
			"field_name": "usage",
			"reference_name": "a",
			"function": [{"method": "mean", "dimensions": ["bk_target_ip", "bk_target_cloud_id", "mount_point"]}],
			"time_aggregation": {"function": "avg_over_time", "window": "60s"},
			"conditions": {}
		}],
		"metric_merge": "a",
		"start_time": "1718865258",
		"end_time": "1718868858",
		"step": "60s",
		"timezone": "Asia/Shanghai"
	}`)
	var qts structured.QueryTs
	require.NoError(t, json.Unmarshal(body, &qts))

	qr, err := checkQueryTsToReference(ctx, &qts)
	require.NoError(t, err)
	vmExp := uquery.ToVmExpand(ctx, qr)
	require.NotNil(t, vmExp)
	wantA := vmExp.MetricFilterCondition["a"]
	require.NotEmpty(t, wantA)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/check/query/ts", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	w := runCheckHandler(t, req, HandlerCheckQueryTs)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	gotMql, ok := resp.Data[0]["metricql"].(string)
	require.True(t, ok)
	assert.Contains(t, gotMql, "{"+wantA+"}", "metricql 应与 ToVmExpand.MetricFilterCondition[a] 一致")
	assert.Contains(t, gotMql, `bk_biz_id="2"`)
	assert.Contains(t, gotMql, `100147_ieod_system_disk_raw`)
	assert.Contains(t, gotMql, `usage_value`)
	assert.Contains(t, gotMql, "avg_over_time(")
}

// OR 条件无法经 ToPromQL 内联的 Conditions.ToProm，check 直查应 400。
func TestEndToEndHandlerCheckQueryTs_OrConditionsRejected(t *testing.T) {
	ctx := e2eCheckContext(t)
	body := []byte(`{
		"space_uid": "bkcc__2",
		"query_list": [{
			"table_id": "result_table.vm",
			"field_name": "container_cpu_usage_seconds_total",
			"reference_name": "a",
			"conditions": {
				"field_list": [
					{"field_name": "namespace", "value": ["ns1"], "op": "eq"},
					{"field_name": "namespace", "value": ["ns2"], "op": "eq"}
				],
				"condition_list": ["or"]
			}
		}],
		"metric_merge": "a",
		"start_time": "1718865258",
		"end_time": "1718868858",
		"step": "1m"
	}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/check/query/ts", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	w := runCheckHandler(t, req, HandlerCheckQueryTs)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var errResp ErrResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.NotEmpty(t, errResp.Err)
}

func TestEndToEndHandlerCheckQueryTs_BadJSON(t *testing.T) {
	ctx := e2eCheckContext(t)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/check/query/ts", bytes.NewReader([]byte(`not-json`)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	w := runCheckHandler(t, req, HandlerCheckQueryTs)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.NotEmpty(t, errResp.Err)
}

func TestEndToEndHandlerCheckQueryTs_EmptyMetricMerge(t *testing.T) {
	ctx := e2eCheckContext(t)
	body := []byte(`{
		"space_uid": "bkcc__2",
		"query_list": [
			{"table_id": "system.cpu_detail", "field_name": "usage", "reference_name": "a"}
		],
		"metric_merge": "",
		"start_time": "1718865258",
		"end_time": "1718868858",
		"step": "1m"
	}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/check/query/ts", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	w := runCheckHandler(t, req, HandlerCheckQueryTs)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.NotEmpty(t, errResp.Err)
}

func TestEndToEndHandlerCheckQueryPromQL_Success_VMPreview(t *testing.T) {
	ctx := e2eCheckContext(t)
	body := []byte(`{
		"promql": "datasource:result_table:vm:container_cpu_usage_seconds_total{}",
		"start": "1718865258",
		"end": "1718868858",
		"step": "1m"
	}`)
	var qp structured.QueryPromQL
	require.NoError(t, json.Unmarshal(body, &qp))
	queryTs, err := promQLToStruct(ctx, &qp)
	require.NoError(t, err)
	if u := metadata.GetUser(ctx); u != nil && u.SpaceUID != "" {
		queryTs.SpaceUid = u.SpaceUID
	}
	wantMql, wantRT := expectedCheckVMPreview(ctx, t, queryTs)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1/check/query/ts/promql", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	w := runCheckHandler(t, req, HandlerCheckQueryPromQL)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var resp struct {
		Data    []map[string]any `json:"data"`
		TraceID string           `json:"trace_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	assert.Equal(t, metadata.VictoriaMetricsStorageType, resp.Data[0]["storage_type"])
	gotMql, ok := resp.Data[0]["metricql"].(string)
	require.True(t, ok)
	assert.Equal(t, wantMql, gotMql)
	assert.Equal(t, wantRT, resultTableIDFromResponse(t, resp.Data[0]["result_table_id"]))
}
