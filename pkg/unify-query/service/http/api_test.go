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
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/infos"
)

type Writer struct {
	h http.Header
	b bytes.Buffer
}

func (w *Writer) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	//TODO implement me
	panic("implement me")
}

func (w *Writer) Flush() {
	//TODO implement me
	panic("implement me")
}

func (w *Writer) CloseNotify() <-chan bool {
	//TODO implement me
	panic("implement me")
}

func (w *Writer) Status() int {
	//TODO implement me
	panic("implement me")
}

func (w *Writer) Size() int {
	//TODO implement me
	panic("implement me")
}

func (w *Writer) WriteString(s string) (int, error) {
	//TODO implement me
	panic("implement me")
}

func (w *Writer) Written() bool {
	//TODO implement me
	panic("implement me")
}

func (w *Writer) WriteHeaderNow() {
	//TODO implement me
	panic("implement me")
}

func (w *Writer) Pusher() http.Pusher {
	//TODO implement me
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

var (
	_ http.ResponseWriter = (*Writer)(nil)
)

func TestAPIHandler(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	mock.SpaceRouter(ctx)

	end := time.Now()
	start := end.Add(time.Hour * -1)

	testCases := map[string]struct {
		handler func(c *gin.Context)
		method  string
		url     string
		params  gin.Params

		infoParams *infos.Params
		expected   string
	}{
		"test label values in vm": {
			handler: HandlerLabelValues,
			method:  http.MethodGet,
			url:     fmt.Sprintf(`query/ts/label/container/values?label=container&match[]=container_cpu_usage_seconds_total{bcs_cluster_id="BCS-K8S-00000", namespace="kube-system"}&start=%d&end=%d&limit=2`, start.Unix(), end.Unix()),
			params: gin.Params{
				{
					Key:   "label_name",
					Value: "container",
				},
			},
			expected: `["POD","kube-proxy"]`,
		},
		"test label values in prometheus": {
			handler: HandlerLabelValues,
			method:  http.MethodGet,
			url:     fmt.Sprintf(`query/ts/label/container/values?label=container&match[]=kube_pod_info{bcs_cluster_id="BCS-K8S-00000"}&start=%d&end=%d&limit=2`, start.Unix(), end.Unix()),
			params: gin.Params{
				{
					Key:   "label_name",
					Value: "bcs_cluster_id",
				},
			},
			expected: `["BCS-K8S-00000"]`,
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
			expected: `["container_tasks_state_value","kube_resourcequota_value"]`,
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
			expected: `{"values":{"bcs_cluster_id":["BCS-K8S-00000"],"namespace":["aiops-default","bkbase","bkmonitor-operator","blueking","flink-default","kube-system"]}}`,
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
			expected: `{"measurement":"container_cpu_usage_seconds_total_value","keys":["bcs_cluster_id","namespace"],"series":[["BCS-K8S-00000","aiops-default"]]}`,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			metadata.SetUser(ctx, "", mock.SpaceUid, "")
			url := fmt.Sprintf("http://127.0.0.1/%s", c.url)
			res, _ := json.Marshal(c.infoParams)
			body := bytes.NewReader(res)
			req, err := http.NewRequestWithContext(ctx, c.method, url, body)
			if err != nil {
				log.Fatalf(ctx, err.Error())
				return
			}

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
