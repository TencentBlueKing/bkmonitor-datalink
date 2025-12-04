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
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/victoriaMetrics"
)

type Writer struct {
	h http.Header
	b bytes.Buffer
}

func (w *Writer) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	// TODO implement me
	panic("implement me")
}

func (w *Writer) Flush() {
	// TODO implement me
	panic("implement me")
}

func (w *Writer) CloseNotify() <-chan bool {
	// TODO implement me
	panic("implement me")
}

func (w *Writer) Status() int {
	// TODO implement me
	panic("implement me")
}

func (w *Writer) Size() int {
	// TODO implement me
	panic("implement me")
}

func (w *Writer) WriteString(s string) (int, error) {
	// TODO implement me
	panic("implement me")
}

func (w *Writer) Written() bool {
	// TODO implement me
	panic("implement me")
}

func (w *Writer) WriteHeaderNow() {
	// TODO implement me
	panic("implement me")
}

func (w *Writer) Pusher() http.Pusher {
	// TODO implement me
	panic("implement me")
}

func (w *Writer) Header() http.Header {
	return w.h
}

func (w *Writer) Write(b []byte) (int, error) {
	w.b.Write(b)
	return len(b), nil
}

func (w *Writer) WriteHeader(statusCode int) {
	w.h = http.Header{
		"code": []string{fmt.Sprintf("%d", statusCode)},
	}
}

func (w *Writer) body() string {
	return string(w.b.Bytes())
}

var _ http.ResponseWriter = (*Writer)(nil)

func TestAPIHandler(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)

	end := time.Unix(1729859485, 0)
	start := time.Unix(1729863085, 0)
	end2d := start.Add(time.Hour * 24 * 2)

	mock.Vm.Set(map[string]any{
		//
		`label_values:17298630851729859485container{bcs_cluster_id="BCS-K8S-00000", namespace="kube-system", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value"}`: []string{
			"POD",
			"kube-proxy",
		},
		`label_values:17298630851729859485bcs_cluster_id{bcs_cluster_id="BCS-K8S-00000", result_table_id="2_bcs_prom_computation_result_table", __name__="kube_pod_info_value"}`: []string{
			"BCS-K8S-00000",
		},

		// above 1d bcs_cluster_id
		`label_values:17298630851730035885bcs_cluster_id{result_table_id="2_bcs_prom_computation_result_table", __name__=~"container_.*_value"}`: []string{
			"BCS-K8S-00000",
		},
		// above 1d namespace
		`label_values:17298630851730035885namespace{result_table_id="2_bcs_prom_computation_result_table", __name__=~"container_.*_value"}`: []string{
			"POD",
			"kube-proxy",
		},

		`query_range:1729863085172985948560topk(2, count({result_table_id="2_bcs_prom_computation_result_table"}) by (__name__))`: victoriaMetrics.Data{
			ResultType: victoriaMetrics.MatrixType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"__name__": "container_tasks_state_value",
					},
					Values: []victoriaMetrics.Value{
						{
							1693973987, "1",
						},
					},
				},
				{
					Metric: map[string]string{
						"__name__": "kube_resource_quota_value",
					},
					Values: []victoriaMetrics.Value{
						{
							1693973987, "1",
						},
					},
				},
			},
		},
		`labels:17298630851729859485{result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value"}`: []string{
			"__name__",
			"namespace",
		},
		`labels:17298630851729859485{result_table_id="2_bcs_prom_computation_result_table", __name__=~"container_.*_value"}`: []string{
			"__name__",
			"bcs_cluster_id",
			"namespace",
			"pod",
		},
		`query_range:1729863085172985948560topk(5, count({result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value"}) by (bcs_cluster_id))`: victoriaMetrics.Data{
			ResultType: victoriaMetrics.MatrixType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"bcs_cluster_id": "BCS-K8S-00000",
					},
					Values: []victoriaMetrics.Value{
						{
							1693973987000, 1,
						},
					},
				},
			},
		},
		// below 1d bcs_cluster_id
		`query_range:1729863085172985948560topk(5, count({result_table_id="2_bcs_prom_computation_result_table", __name__=~"container_.*_value"}) by (bcs_cluster_id))`: victoriaMetrics.Data{
			ResultType: victoriaMetrics.MatrixType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"bcs_cluster_id": "BCS-K8S-00000",
					},
					Values: []victoriaMetrics.Value{
						{
							1693973987000, 1,
						},
					},
				},
			},
		},
		// below 1d namespace
		`query_range:1729863085172985948560topk(5, count({result_table_id="2_bcs_prom_computation_result_table", __name__=~"container_.*_value"}) by (namespace))`: victoriaMetrics.Data{
			ResultType: victoriaMetrics.MatrixType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"namespace": "bkbase",
					},
					Values: []victoriaMetrics.Value{
						{
							1693973987, "1",
						},
					},
				},
				{
					Metric: map[string]string{
						"namespace": "kube-system",
					},
					Value: []any{
						1693973987, "1",
					},
				},
				{
					Metric: map[string]string{
						"namespace": "bkmonitor-operator",
					},
					Value: []any{
						1693973987, "1",
					},
				},
				{
					Metric: map[string]string{
						"namespace": "blueking",
					},
					Value: []any{
						1693973987, "1",
					},
				},
			},
		},
		`query_range:1729863085172985948560topk(5, count({result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value"}) by (namespace))`: victoriaMetrics.Data{
			ResultType: victoriaMetrics.MatrixType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"namespace": "bkbase",
					},
					Values: []victoriaMetrics.Value{
						{
							1693973987, "1",
						},
					},
				},
				{
					Metric: map[string]string{
						"namespace": "kube-system",
					},
					Value: []any{
						1693973987, "1",
					},
				},
				{
					Metric: map[string]string{
						"namespace": "bkmonitor-operator",
					},
					Value: []any{
						1693973987, "1",
					},
				},
				{
					Metric: map[string]string{
						"namespace": "blueking",
					},
					Value: []any{
						1693973987, "1",
					},
				},
			},
		},
		`series:17298630851729859485{result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value"}`: []map[string]string{
			{
				"__name__":       "container_cpu_usage_seconds_total_value",
				"bcs_cluster_id": "BCS-K8S-00000",
				"namespace":      "default",
			},
			{
				"__name__":       "container_cpu_usage_seconds_total_value",
				"bcs_cluster_id": "BCS-K8S-00000",
				"namespace":      "bkbase",
			},
		},
	})

	testCases := map[string]struct {
		handler func(c *gin.Context)
		method  string
		url     string
		params  gin.Params

		infoParams *Params
		expected   string
	}{
		"test label values in vm 1": {
			handler: HandlerLabelValues,
			method:  http.MethodGet,
			url:     fmt.Sprintf(`query/ts/label/container/values?label=container&match[]=container_cpu_usage_seconds_total{bcs_cluster_id="BCS-K8S-00000", namespace="kube-system"}&start=%d&end=%d&limit=2`, start.Unix(), end.Unix()),
			params: gin.Params{
				{
					Key:   "label_name",
					Value: "container",
				},
			},
			expected: `{"values":{"container":["POD","kube-proxy"]}}`,
		},
		"test label values in vm 2": {
			handler: HandlerLabelValues,
			method:  http.MethodGet,
			url:     fmt.Sprintf(`query/ts/label/container/values?label=container&match[]=kube_pod_info{bcs_cluster_id="BCS-K8S-00000"}&start=%d&end=%d&limit=2`, start.Unix(), end.Unix()),
			params: gin.Params{
				{
					Key:   "label_name",
					Value: "bcs_cluster_id",
				},
			},
			expected: `{"values":{"bcs_cluster_id":["BCS-K8S-00000"]}}`,
		},
		"test field keys in prometheus": {
			handler: HandlerFieldKeys,
			method:  http.MethodPost,
			infoParams: &Params{
				TableID: "result_table.vm",
				Start:   fmt.Sprintf("%d", start.Unix()),
				End:     fmt.Sprintf("%d", end.Unix()),
				Limit:   2,
			},
			expected: `["container_tasks_state_value","kube_resource_quota_value"]`,
		},
		"test field keys in prometheus direct": {
			handler: HandlerFieldKeys,
			method:  http.MethodPost,
			infoParams: &Params{
				TableID: "result_table.vm",
				Start:   fmt.Sprintf("%d", start.Unix()),
				End:     fmt.Sprintf("%d", end.Unix()),
				TsDBMap: map[string]structured.TsDBs{
					"a": []*query.TsDBV2{
						{
							TableID:         "result_table.vm",
							MeasurementType: "bk_split_measurement",
							DataLabel:       "vm",
							StorageID:       "2",
							VmRt:            "2_bcs_prom_computation_result_table",
							StorageType:     "victoria_metrics",
						},
					},
				},
				Limit: 2,
			},

			expected: `["container_tasks_state_value","kube_resource_quota_value"]`,
		},
		"test tag keys in prometheus": {
			handler: HandlerTagKeys,
			method:  http.MethodPost,
			infoParams: &Params{
				TableID: "result_table.vm",
				Start:   fmt.Sprintf("%d", start.Unix()),
				End:     fmt.Sprintf("%d", end.Unix()),
				Metric:  "container_cpu_usage_seconds_total",
				Limit:   2,
			},
			expected: `["__name__","namespace"]`,
		},
		"test tag keys in prometheus direct": {
			handler: HandlerTagKeys,
			method:  http.MethodPost,
			infoParams: &Params{
				TableID: "result_table.vm",
				Start:   fmt.Sprintf("%d", start.Unix()),
				End:     fmt.Sprintf("%d", end.Unix()),
				Metric:  "container_cpu_usage_seconds_total",
				Limit:   2,
				TsDBMap: map[string]structured.TsDBs{
					"a": []*query.TsDBV2{
						{
							TableID:         "result_table.vm",
							MeasurementType: "bk_split_measurement",
							DataLabel:       "vm",
							StorageID:       "2",
							VmRt:            "2_bcs_prom_computation_result_table",
							StorageType:     "victoria_metrics",
						},
					},
				},
			},

			expected: `["__name__","namespace"]`,
		},
		"test tag keys in prometheus with regex direct": {
			handler: HandlerTagKeys,
			method:  http.MethodPost,
			infoParams: &Params{
				TableID:  "result_table.vm",
				Start:    fmt.Sprintf("%d", start.Unix()),
				End:      fmt.Sprintf("%d", end.Unix()),
				Metric:   "container_.*",
				IsRegexp: true,
				Limit:    2,
				TsDBMap: map[string]structured.TsDBs{
					"a": []*query.TsDBV2{
						{
							TableID:         "result_table.vm",
							MeasurementType: "bk_split_measurement",
							DataLabel:       "vm",
							StorageID:       "2",
							VmRt:            "2_bcs_prom_computation_result_table",
							StorageType:     "victoria_metrics",
						},
					},
				},
			},
			expected: `["__name__","bcs_cluster_id","namespace","pod"]`,
		},
		"test tag values in prometheus": {
			handler: HandlerTagValues,
			method:  http.MethodPost,
			infoParams: &Params{
				TableID: "result_table.vm",
				Start:   fmt.Sprintf("%d", start.Unix()),
				End:     fmt.Sprintf("%d", end.Unix()),
				Metric:  "container_cpu_usage_seconds_total",
				Limit:   5,
				Keys:    []string{"namespace", "bcs_cluster_id"},
			},
			expected: `{"values":{"bcs_cluster_id":["BCS-K8S-00000"],"namespace":["bkbase","bkmonitor-operator","blueking","kube-system"]}}`,
		},
		"test tag values in prometheus direct": {
			handler: HandlerTagValues,
			method:  http.MethodPost,
			infoParams: &Params{
				TableID: "result_table.vm",
				Start:   fmt.Sprintf("%d", start.Unix()),
				End:     fmt.Sprintf("%d", end.Unix()),
				Metric:  "container_cpu_usage_seconds_total",
				Limit:   5,
				Keys:    []string{"namespace", "bcs_cluster_id"},
				TsDBMap: map[string]structured.TsDBs{
					"a": []*query.TsDBV2{
						{
							TableID:         "result_table.vm",
							MeasurementType: "bk_split_measurement",
							DataLabel:       "vm",
							StorageID:       "2",
							VmRt:            "2_bcs_prom_computation_result_table",
							StorageType:     "victoria_metrics",
						},
					},
				},
			},
			expected: `{"values":{"bcs_cluster_id":["BCS-K8S-00000"],"namespace":["bkbase","bkmonitor-operator","blueking","kube-system"]}}`,
		},
		"test tag values in prometheus with regex below 1d": {
			handler: HandlerTagValues,
			method:  http.MethodPost,
			infoParams: &Params{
				TableID:  "result_table.vm",
				Start:    fmt.Sprintf("%d", start.Unix()),
				End:      fmt.Sprintf("%d", end.Unix()),
				IsRegexp: true,
				Metric:   "container_.*",
				Limit:    5,
				Keys:     []string{"namespace", "bcs_cluster_id"},
			},
			expected: `{"values":{"bcs_cluster_id":["BCS-K8S-00000"],"namespace":["bkbase","bkmonitor-operator","blueking","kube-system"]}}`,
		},
		"test tag values in prometheus with regex above 1d": {
			handler: HandlerTagValues,
			method:  http.MethodPost,
			infoParams: &Params{
				TableID:  "result_table.vm",
				Start:    fmt.Sprintf("%d", start.Unix()),
				End:      fmt.Sprintf("%d", end2d.Unix()),
				IsRegexp: true,
				Metric:   "container_.*",
				Limit:    5,
				Keys:     []string{"namespace", "bcs_cluster_id"},
			},
			expected: `{"values":{"bcs_cluster_id":["BCS-K8S-00000"],"namespace":["POD","kube-proxy"]}}`,
		},
		"test series in prometheus": {
			handler: HandlerSeries,
			method:  http.MethodPost,
			infoParams: &Params{
				TableID: "result_table.vm",
				Start:   fmt.Sprintf("%d", start.Unix()),
				End:     fmt.Sprintf("%d", end.Unix()),
				Metric:  "container_cpu_usage_seconds_total",
				Limit:   1,
				Keys:    []string{"bcs_cluster_id", "namespace"},
			},
			expected: `{"measurement":"container_cpu_usage_seconds_total_value","keys":["bcs_cluster_id","namespace"],"series":[["BCS-K8S-00000","default"],["BCS-K8S-00000","bkbase"]]}`,
		},
		"test series in prometheus direct": {
			handler: HandlerSeries,
			method:  http.MethodPost,
			infoParams: &Params{
				TableID: "result_table.vm",
				Start:   fmt.Sprintf("%d", start.Unix()),
				End:     fmt.Sprintf("%d", end.Unix()),
				Metric:  "container_cpu_usage_seconds_total",
				Limit:   1,
				Keys:    []string{"bcs_cluster_id", "namespace"},
				TsDBMap: map[string]structured.TsDBs{
					"a": []*query.TsDBV2{
						{
							TableID:         "result_table.vm",
							MeasurementType: "bk_split_measurement",
							DataLabel:       "vm",
							StorageID:       "2",
							VmRt:            "2_bcs_prom_computation_result_table",
							StorageType:     "victoria_metrics",
						},
					},
				},
			},
			expected: `{"measurement":"container_cpu_usage_seconds_total_value","keys":["bcs_cluster_id","namespace"],"series":[["BCS-K8S-00000","default"],["BCS-K8S-00000","bkbase"]]}`,
		},
		"test field map in es": {
			handler: HandlerFieldMap,
			method:  http.MethodPost,
			infoParams: &Params{
				DataSource: "bklog",
				TableID:    "result_table.unify_query",
			},
			expected: `{"data":[{"alias_name":"","field_name":"__ext.container_id","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.container_image","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.container_name","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.io_kubernetes_pod","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.io_kubernetes_pod_ip","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.io_kubernetes_pod_namespace","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.io_kubernetes_pod_uid","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.io_kubernetes_workload_name","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.io_kubernetes_workload_type","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"cloudId","field_type":"integer","origin_field":"cloudId","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"dtEventTimeStamp","field_type":"date","origin_field":"dtEventTimeStamp","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"file","field_type":"keyword","origin_field":"file","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"gseIndex","field_type":"long","origin_field":"gseIndex","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"iterationIndex","field_type":"integer","origin_field":"iterationIndex","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"level","field_type":"keyword","origin_field":"level","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"log","field_type":"text","origin_field":"log","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"message","field_type":"text","origin_field":"message","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"path","field_type":"keyword","origin_field":"path","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"report_time","field_type":"keyword","origin_field":"report_time","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"serverIp","field_type":"keyword","origin_field":"serverIp","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"time","field_type":"date","origin_field":"time","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"trace_id","field_type":"keyword","origin_field":"trace_id","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]}]}`,
		},
		"test field map in es direct": {
			handler: HandlerFieldMap,
			method:  http.MethodPost,
			infoParams: &Params{
				DataSource: "bklog",
				TableID:    "result_table.unify_query",
				TsDBMap: map[string]structured.TsDBs{
					"a": []*query.TsDBV2{
						{
							TableID:     "result_table.unify_query",
							DataLabel:   "es",
							StorageID:   "3",
							StorageType: "elasticsearch",
							DB:          "unify_query",
							FieldAlias: map[string]string{
								"alias_ns": "__ext.host.bk_set_name",
							},
						},
					},
				},
			},
			expected: `{"data":[{"alias_name":"","field_name":"__ext.container_id","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.container_image","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.container_name","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.io_kubernetes_pod","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.io_kubernetes_pod_ip","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.io_kubernetes_pod_namespace","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.io_kubernetes_pod_uid","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.io_kubernetes_workload_name","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"__ext.io_kubernetes_workload_type","field_type":"keyword","origin_field":"__ext","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"cloudId","field_type":"integer","origin_field":"cloudId","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"dtEventTimeStamp","field_type":"date","origin_field":"dtEventTimeStamp","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"file","field_type":"keyword","origin_field":"file","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"gseIndex","field_type":"long","origin_field":"gseIndex","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"iterationIndex","field_type":"integer","origin_field":"iterationIndex","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"level","field_type":"keyword","origin_field":"level","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"log","field_type":"text","origin_field":"log","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"message","field_type":"text","origin_field":"message","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"path","field_type":"keyword","origin_field":"path","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"report_time","field_type":"keyword","origin_field":"report_time","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"serverIp","field_type":"keyword","origin_field":"serverIp","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"time","field_type":"date","origin_field":"time","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},{"alias_name":"","field_name":"trace_id","field_type":"keyword","origin_field":"trace_id","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]}]}`,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			metadata.SetUser(ctx, &metadata.User{SpaceUID: influxdb.SpaceUid})
			url := fmt.Sprintf("http://127.0.0.1/%s", c.url)
			res, _ := json.Marshal(c.infoParams)
			body := bytes.NewReader(res)
			req, _ := http.NewRequestWithContext(ctx, c.method, url, body)
			w := &Writer{}
			ginC := &gin.Context{
				Request: req,
				Writer:  w,
				Params:  c.params,
			}
			if c.handler != nil {
				c.handler(ginC)
				assert.Equal(t, c.expected, w.body())
			}
		})
	}
}

func TestQueryRawWithHandler(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)

	mock.Es.Set(map[string]any{
		`{"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1764074673129,"include_lower":true,"include_upper":true,"to":1764078273129}}}}},"size":2,"sort":[{"dtEventTimeStamp":{"order":"desc"}}]}`:      `{"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10,"relation":"eq"},"hits":[{"_type":"_doc","_id":"1","_source":{"dtEventTimeStamp":"2025-11-25T12:07:16.747332000Z"}},{"_type":"_doc","_id":"2","_source":{"dtEventTimeStamp":"2025-11-25T12:07:37.747332000Z"}}]}}`,
		`{"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1764074673129,"include_lower":true,"include_upper":true,"to":1764078273129}}}}},"size":2,"sort":[{"dtEventTimeStampNanos":{"order":"desc"}}]}`: `{"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10,"relation":"eq"},"hits":[{"_type":"_doc","_id":"3","_source":{"dtEventTimeStamp":"1764065418831","dtEventTimeStampNanos":"2025-11-25T12:07:18.747332000Z"}},{"_type":"_doc","_id":"4","_source":{"dtEventTimeStamp":"1764065418831","dtEventTimeStampNanos":"2025-11-25T12:07:38.747332000Z"}}]}}`,
	})

	testCases := map[string]struct {
		body     string
		expected string
	}{
		"test_1": {
			body:     `{"space_uid":"bkcc__2","query_list":[{"data_source":"bklog","table_id":"nano","field_name":"dtEventTimeStamp","is_regexp":false,"function":[],"time_aggregation":{},"is_dom_sampled":false,"reference_name":"a","conditions":{},"query_string":"*","sql":"","is_prefix":false}],"metric_merge":"a","order_by":["-dtEventTimeStamp","-gseIndex","-iterationIndex"],"start_time":"1764074673129","end_time":"1764078273129","step":"1m","timezone":"Asia/Shanghai","instant":false,"not_time_align":false,"limit":2,"highlight":{"enable":true}}`,
			expected: `{"total":20,"list":[{"__data_label":"","__doc_id":"4","__index":"","__result_table":"nano.nano","_time":"1764065418831","dtEventTimeStamp":"2025-11-25T12:07:38.747332000Z","dtEventTimeStampNanos":"2025-11-25T12:07:38.747332000Z"},{"__data_label":"","__doc_id":"2","__index":"","__result_table":"nano.millisecond","_time":"2025-11-25T12:07:37.747332000Z","dtEventTimeStamp":"2025-11-25T12:07:37.747332000Z"}],"done":false,"status":null,"result_table_options":{"nano.millisecond|3":{"from":0},"nano.nano|3":{"from":0}}}`,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			body := bytes.NewBufferString(c.body)
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "", body)
			w := &Writer{}
			ginC := &gin.Context{
				Request: req,
				Writer:  w,
			}

			HandlerQueryRaw(ginC)
			b := w.body()
			assert.Equal(t, c.expected, b)
		})
	}
}

func TestQueryReferenceWithHandler(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	mock.Es.Set(map[string]any{
		// 多个point
		`{"aggregations":{"serverIp":{"aggregations":{"_value":{"value_count":{"field":"serverIp"}}},"terms":{"field":"serverIp","missing":" ","order":[{"_value":"desc"}],"size":20}}},"query":{"bool":{"filter":[{"exists":{"field":"serverIp"}},{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1761980445276,"include_lower":true,"include_upper":true,"to":1764572445277}}}]}},"size":0}`:                                                                                               `{"took":620,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"serverIp":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"10.0.6.4","doc_count":48,"_value":{"value":48}},{"key":"10.0.6.18","doc_count":24,"_value":{"value":24}},{"key":"10.0.6.33","doc_count":53,"_value":{"value":53}},{"key":"10.0.6.18","doc_count":24,"_value":{"value":24}},{"key":"10.0.7.93","doc_count":13961116,"_value":{"value":13961116}}]}}}`,
		`{"aggregations":{"serverIp":{"aggregations":{"_value":{"value_count":{"field":"serverIp"}}},"terms":{"field":"serverIp","missing":" ","order":[{"_value":"desc"}],"size":20}}},"query":{"bool":{"filter":[{"exists":{"field":"serverIp"}},{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1761980445276,"include_lower":true,"include_upper":true,"to":1764572445277}}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"test"}}]}},"size":0}`: `{"took":6,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"serverIp":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"10.0.6.33","doc_count":42768,"_value":{"value":42768}}]}}}`})
	mock.Es1.Set(map[string]any{
		`{"aggregations":{"serverIp":{"aggregations":{"_value":{"value_count":{"field":"serverIp"}}},"terms":{"field":"serverIp","missing":" ","order":[{"_value":"desc"}],"size":20}}},"query":{"bool":{"filter":[{"exists":{"field":"serverIp"}},{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1761980445276,"include_lower":true,"include_upper":true,"to":1764572445277}}}]}},"size":0}`: `{"took":6,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"serverIp":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"10.0.6.33","doc_count":42768,"_value":{"value":42768}}]}}}`,
	})

	testCases := map[string]struct {
		body     string
		expected string
	}{
		"metrics merge issue": {
			body:     `{"query_list":[{"data_source":"bklog","reference_name":"a","dimensions":[],"time_field":"time","conditions":{"field_list":[{"field_name":"serverIp","value":[""],"op":"ne"}],"condition_list":[]},"query_string":"*","function":[{"method":"count","dimensions":["serverIp"]}],"table_id":"result_table.es","field_name":"serverIp","limit":20},{"data_source":"bklog","reference_name":"a","dimensions":[],"time_field":"time","conditions":{"field_list":[{"field_name":"serverIp","value":[""],"op":"ne"}],"condition_list":[]},"query_string":"test","function":[{"method":"count","dimensions":["serverIp"]}],"table_id":"result_table.es_1","field_name":"serverIp","limit":20}],"metric_merge":"a","order_by":["-_value"],"step":"1d","space_uid":"bkcc__2","start_time":"1761980445276","end_time":"1764572445277","down_sample_range":"","timezone":"Asia/Shanghai","bk_biz_id":2}`,
			expected: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["serverIp"],"group_values":["10.0.6.18"],"values":[[1761980445276,24]]},{"name":"_result1","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["serverIp"],"group_values":["10.0.6.33"],"values":[[1761980445276,42821]]},{"name":"_result2","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["serverIp"],"group_values":["10.0.6.4"],"values":[[1761980445276,48]]},{"name":"_result3","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["serverIp"],"group_values":["10.0.7.93"],"values":[[1761980445276,13961116]]}],"is_partial":false}`,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			body := bytes.NewBufferString(c.body)
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "", body)
			w := &Writer{}
			ginC := &gin.Context{
				Request: req,
				Writer:  w,
			}

			HandlerQueryReference(ginC)
			b := w.body()
			assert.Equal(t, c.expected, b)
		})
	}
}

func TestPromQLQueryHandler(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)

	end := time.Unix(1741060043, 0)
	start := time.Unix(1741056443, 0)

	mock.Vm.Set(map[string]any{
		`query_range:17410564431741060043600count by (bcs_cluster_id) (a)`: victoriaMetrics.Data{
			ResultType: victoriaMetrics.MatrixType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"bcs_cluster_id": "BCS-K8S-00000",
					},
					Values: []victoriaMetrics.Value{
						{
							1741056443, "2042",
						},
						{
							1741057043, "2056",
						},
						{
							1741057643, "1995",
						},
						{
							1741058243, "2008",
						},
						{
							1741058843, "1978",
						},
						{
							1741059443, "2001",
						},
						{
							1741060043, "2052",
						},
					},
				},
			},
		},
		`query_range:17410560001741060043600count by (bcs_cluster_id) (a)`: victoriaMetrics.Data{
			ResultType: victoriaMetrics.MatrixType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"bcs_cluster_id": "BCS-K8S-00000",
					},
					Values: []victoriaMetrics.Value{
						{
							1729602000, "2042",
						},
						{
							1729602600, "2056",
						},
						{
							1729603200, "1995",
						},
						{
							1729603800, "2008",
						},
						{
							1729604400, "1978",
						},
						{
							1729605000, "2001",
						},
						{
							1729605600, "2052",
						},
					},
				},
			},
		},
		`query:1741060043sum by (bcs_cluster_id) (a)`: victoriaMetrics.Data{
			ResultType: victoriaMetrics.VectorType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"bcs_cluster_id": "BCS-K8S-00000",
					},
					Value: victoriaMetrics.Value{
						1729608144, "1172",
					},
				},
			},
		},
	})

	mock.BkSQL.Set(map[string]any{
		"SELECT `RoomId`, MAX(`Peakcpuutilpct`) AS `_value_`, MAX(FLOOR((dtEventTimeStamp + 0) / 60000) * 60000 - 0) AS `_timestamp_` FROM `2_DSMonitorGamePlayInfo` WHERE `dtEventTimeStamp` >= 1763719559999 AND `dtEventTimeStamp` < 1763719619999 AND `dtEventTime` >= '2025-11-21 18:05:59' AND `dtEventTime` <= '2025-11-21 18:07:00' AND `thedate` = '20251121' AND `RoomId` = '30895627249454038' GROUP BY `RoomId`, (FLOOR((dtEventTimeStamp + 0) / 60000) * 60000 - 0) ORDER BY `_timestamp_` ASC LIMIT 2000005": "{\"result\":true,\"message\":\"成功\",\"code\":\"00\",\"data\":{\"result_table_scan_range\":{\"18970_DSMonitorGamePlayInfo\":{\"start\":\"2025112100\",\"end\":\"2025112123\"}},\"cluster\":\"cag_mysql\",\"totalRecords\":1,\"external_api_call_time_mills\":{\"bkbase_auth_api\":22,\"bkbase_meta_api\":0,\"bkbase_apigw_api\":0},\"resource_use_summary\":{\"cpu_time_mills\":0,\"memory_bytes\":0,\"processed_bytes\":0,\"processed_rows\":0},\"source\":\"\",\"list\":[{\"RoomId\":30895627249454038,\"_value_\":3860,\"_timestamp_\":1763719560000}],\"bk_biz_ids\":[],\"stage_elapsed_time_mills\":{\"check_query_syntax\":2,\"query_db\":8,\"get_query_driver\":0,\"match_query_forbidden_config\":0,\"convert_query_statement\":3,\"connect_db\":11,\"match_query_routing_rule\":0,\"check_permission\":23,\"check_query_semantic\":1,\"pick_valid_storage\":1},\"select_fields_order\":[\"RoomId\",\"_value_\",\"_timestamp_\"],\"sql\":\"SELECT `RoomId`, MAX(`Peakcpuutilpct`) AS `_value_`, MAX(((FLOOR((`dtEventTimeStamp` + 28800000) / 60000)) * 60000) - 28800000) AS `_timestamp_` FROM mapleleaf_18970.DSMonitorGamePlayInfo_18970 WHERE (((((`dtEventTimeStamp` >= 1763719559999) AND (`dtEventTimeStamp` < 1763719619999)) AND ((`dtEventTime` >= '2025-11-21 18:05:59') AND (`dtEventTimeStamp` >= 1763719559000))) AND ((`dtEventTime` <= '2025-11-21 18:07:00') AND (`dtEventTimeStamp` <= 1763719620999))) AND ((`thedate` = '20251121') AND ((`dtEventTimeStamp` >= 1763654400000) AND (`dtEventTimeStamp` < 1763740800000)))) AND (`RoomId` = '30895627249454038') GROUP BY `RoomId`, ((FLOOR((`dtEventTimeStamp` + 28800000) / 60000)) * 60000) - 28800000 ORDER BY `_timestamp_` LIMIT 2000005\",\"total_record_size\":584,\"trino_cluster_host\":\"\",\"timetaken\":0.049,\"result_schema\":[{\"field_type\":\"long\",\"field_name\":\"__c0\",\"field_alias\":\"RoomId\",\"field_index\":0},{\"field_type\":\"long\",\"field_name\":\"__c1\",\"field_alias\":\"_value_\",\"field_index\":1},{\"field_type\":\"double\",\"field_name\":\"__c2\",\"field_alias\":\"_timestamp_\",\"field_index\":2}],\"bksql_call_elapsed_time\":0,\"device\":\"mysql\",\"result_table_ids\":[\"18970_DSMonitorGamePlayInfo\"]},\"errors\":null,\"trace_id\":\"9c5650ade38cf54ee69411d5d660520a\",\"span_id\":\"80e32704cdfd939e\"}",
	})

	testCases := map[string]struct {
		handler      func(c *gin.Context)
		promql       string
		expected     string
		step         string
		start        time.Time
		end          time.Time
		instant      bool
		notTimeAlign bool
	}{
		"test_query_vm_1": {
			handler:  HandlerQueryPromQL,
			promql:   `count(container_cpu_usage_seconds_total) by (bcs_cluster_id)`,
			step:     "10m",
			expected: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bcs_cluster_id"],"group_values":["BCS-K8S-00000"],"values":[[1729602000000,2042],[1729602600000,2056],[1729603200000,1995],[1729603800000,2008],[1729604400000,1978],[1729605000000,2001],[1729605600000,2052]]}],"is_partial":false}`,
		},
		"test_query_vm_1 and not time align": {
			handler:      HandlerQueryPromQL,
			promql:       `count(container_cpu_usage_seconds_total) by (bcs_cluster_id)`,
			step:         "10m",
			notTimeAlign: true,
			expected:     `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bcs_cluster_id"],"group_values":["BCS-K8S-00000"],"values":[[1741056443000,2042],[1741057043000,2056],[1741057643000,1995],[1741058243000,2008],[1741058843000,1978],[1741059443000,2001],[1741060043000,2052]]}],"is_partial":false}`,
		},
		"test_query_vm_2 and instant": {
			handler:  HandlerQueryPromQL,
			promql:   `sum(kube_pod_info) by (bcs_cluster_id)`,
			step:     "30m",
			instant:  true,
			expected: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bcs_cluster_id"],"group_values":["BCS-K8S-00000"],"values":[[1729608144000,1172]]}],"is_partial":false}`,
		},
		"test promql bkdata": {
			handler:  HandlerQueryPromQL,
			promql:   `sum(increase({__name__=~"bkdata:.*trace.*:span_name", span_name=~"handler-query-.*"}[3h])) by (span_name)`,
			step:     "3h",
			expected: `{"series":[],"is_partial":false}`,
		},
		"test promql by bkdata with long dim": {
			handler:  HandlerQueryPromQL,
			promql:   `max by (RoomId) (max_over_time(bkdata:2_DSMonitorGamePlayInfo:Peakcpuutilpct{RoomId="30895627249454038"}[1m]))`,
			end:      time.Unix(1763719560, 0),
			start:    time.Unix(1763719860, 0),
			step:     "1m",
			instant:  true,
			expected: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["RoomId"],"group_values":["30895627249454038"],"values":[[1763719560000,3860]]}],"is_partial":false}`,
		},
	}

	promql.MockEngine()

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			metadata.SetUser(ctx, &metadata.User{SpaceUID: influxdb.SpaceUid})

			queryPromQL := &structured.QueryPromQL{
				PromQL:       c.promql,
				Start:        fmt.Sprintf("%d", start.Unix()),
				End:          fmt.Sprintf("%d", end.Unix()),
				Step:         c.step,
				Instant:      c.instant,
				NotTimeAlign: c.notTimeAlign,
				Timezone:     "Asia/Shanghai",
			}

			if !c.start.IsZero() {
				queryPromQL.Start = fmt.Sprintf("%d", c.start.Unix())
			}
			if !c.end.IsZero() {
				queryPromQL.End = fmt.Sprintf("%d", c.end.Unix())
			}

			res, _ := json.Marshal(queryPromQL)
			body := bytes.NewReader(res)
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "", body)
			w := &Writer{}
			ginC := &gin.Context{
				Request: req,
				Writer:  w,
			}
			if c.handler != nil {
				c.handler(ginC)
				b := w.body()
				assert.Equal(t, c.expected, b)
			}
		})
	}
}
