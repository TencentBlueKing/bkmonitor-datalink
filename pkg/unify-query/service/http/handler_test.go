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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/infos"
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

		infoParams *infos.Params
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
			infoParams: &infos.Params{
				TableID: "result_table.vm",
				Start:   fmt.Sprintf("%d", start.Unix()),
				End:     fmt.Sprintf("%d", end.Unix()),
				Limit:   2,
			},
			expected: `["container_tasks_state_value","kube_resource_quota_value"]`,
		},
		"test tag keys in prometheus": {
			handler: HandlerTagKeys,
			method:  http.MethodPost,
			infoParams: &infos.Params{
				TableID: "result_table.vm",
				Start:   fmt.Sprintf("%d", start.Unix()),
				End:     fmt.Sprintf("%d", end.Unix()),
				Metric:  "container_cpu_usage_seconds_total",
				Limit:   2,
			},
			expected: `["__name__","namespace"]`,
		},
		"test tag keys in prometheus with regex": {
			handler: HandlerTagKeys,
			method:  http.MethodPost,
			infoParams: &infos.Params{
				TableID:  "result_table.vm",
				Start:    fmt.Sprintf("%d", start.Unix()),
				End:      fmt.Sprintf("%d", end.Unix()),
				Metric:   "container_.*",
				IsRegexp: true,
				Limit:    2,
			},
			expected: `["__name__","bcs_cluster_id","namespace","pod"]`,
		},
		"test tag values in prometheus": {
			handler: HandlerTagValues,
			method:  http.MethodPost,
			infoParams: &infos.Params{
				TableID: "result_table.vm",
				Start:   fmt.Sprintf("%d", start.Unix()),
				End:     fmt.Sprintf("%d", end.Unix()),
				Metric:  "container_cpu_usage_seconds_total",
				Limit:   5,
				Keys:    []string{"namespace", "bcs_cluster_id"},
			},
			expected: `{"values":{"bcs_cluster_id":["BCS-K8S-00000"],"namespace":["bkbase","bkmonitor-operator","blueking","kube-system"]}}`,
		},
		"test tag values in prometheus with regex below 1d": {
			handler: HandlerTagValues,
			method:  http.MethodPost,
			infoParams: &infos.Params{
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
			infoParams: &infos.Params{
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
			infoParams: &infos.Params{
				TableID: "result_table.vm",
				Start:   fmt.Sprintf("%d", start.Unix()),
				End:     fmt.Sprintf("%d", end.Unix()),
				Metric:  "container_cpu_usage_seconds_total",
				Limit:   1,
				Keys:    []string{"bcs_cluster_id", "namespace"},
			},
			expected: `{"measurement":"container_cpu_usage_seconds_total_value","keys":["bcs_cluster_id","namespace"],"series":[["BCS-K8S-00000","default"],["BCS-K8S-00000","bkbase"]]}`,
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

func TestQueryHandler(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)

	end := time.Unix(1741060043, 0)
	start := time.Unix(1741056443, 0)

	mock.Vm.Set(map[string]any{
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

	testCases := map[string]struct {
		handler  func(c *gin.Context)
		promql   string
		expected string
		step     string
		instant  bool
	}{
		"test_query_vm_1": {
			handler:  HandlerQueryPromQL,
			promql:   `count(container_cpu_usage_seconds_total) by (bcs_cluster_id)`,
			step:     "10m",
			expected: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bcs_cluster_id"],"group_values":["BCS-K8S-00000"],"values":[[1729602000000,2042],[1729602600000,2056],[1729603200000,1995],[1729603800000,2008],[1729604400000,1978],[1729605000000,2001],[1729605600000,2052]]}],"is_partial":false}`,
		},
		"test_query_vm_2": {
			handler:  HandlerQueryPromQL,
			promql:   `sum(kube_pod_info) by (bcs_cluster_id)`,
			step:     "30m",
			instant:  true,
			expected: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bcs_cluster_id"],"group_values":["BCS-K8S-00000"],"values":[[1729608144000,1172]]}],"is_partial":false}`,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			metadata.SetUser(ctx, &metadata.User{SpaceUID: influxdb.SpaceUid})
			queryPromQL := &structured.QueryPromQL{
				PromQL:  c.promql,
				Start:   fmt.Sprintf("%d", start.Unix()),
				End:     fmt.Sprintf("%d", end.Unix()),
				Step:    c.step,
				Instant: c.instant,
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
