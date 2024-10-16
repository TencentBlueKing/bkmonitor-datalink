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
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/infos"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
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

var _ http.ResponseWriter = (*Writer)(nil)

func TestAPIHandler(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	mock.SpaceRouter(ctx)

	testCases := map[string]struct {
		handler  func(c *gin.Context)
		method   string
		url      string
		body     io.Reader
		params   gin.Params
		expected string
	}{
		"test label values in vm": {
			handler: HandlerLabelValues,
			method:  http.MethodGet,
			url:     "query/ts/label/container/values?label=container&match%5B%5D=bkmonitor:result_table:vm:field%7Bbcs_cluster_id%3D%22cls_1%22%2Cnamespace%3D%22perf-master-test-main%22%2C+container%21%3D%22POD%22%7D",
			params: gin.Params{
				{
					Key:   "label_name",
					Value: "container",
				},
			},
			expected: `[]`,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			metadata.SetUser(ctx, "", mock.SpaceUid, "")
			url := fmt.Sprintf("http://127.0.0.1/%s", c.url)
			req, err := http.NewRequestWithContext(ctx, c.method, url, c.body)
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

func TestQueryInfo(t *testing.T) {
	ctx := context.Background()
	mockCurl := mockData(ctx, "api_test", "query_info")

	tcs := map[string]struct {
		spaceUid string
		key      infos.InfoType
		params   *infos.Params

		data string
		err  error
	}{
		"influxdb field keys": {
			key:      infos.FieldKeys,
			spaceUid: consul.InfluxDBStorageType,
			params: &infos.Params{
				Metric: redis.BkSplitMeasurement,
			},
			data: `http://127.0.0.1:80/query?db=bk_split_measurement&q=show+measurements`,
		},
		"vm field keys": {
			key:      infos.FieldKeys,
			spaceUid: consul.VictoriaMetricsStorageType,
			params: &infos.Params{
				TableID: "a.b_1",
				Metric:  redis.BkSplitMeasurement,
			},
			data: `{"sql":"{\"influx_compatible\":true,\"api_type\":\"label_values\",\"api_params\":{\"label\":\"__name__\"},\"result_table_group\":{\"bk_split_measurement_value\":[\"victoria_metrics\"]},\"metric_filter_condition\":null,\"metric_alias_mapping\":null}","bkdata_authentication_method":"token","bk_username":"admin","bk_app_code":"","prefer_storage":"vm","bkdata_data_token":"","bk_app_secret":""}`,
		},
		"influxdb tag keys": {
			key:      infos.TagKeys,
			spaceUid: consul.InfluxDBStorageType,
			params: &infos.Params{
				Metric: redis.BkSplitMeasurement,
				Conditions: structured.Conditions(struct {
					FieldList     []structured.ConditionField
					ConditionList []string
				}{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "a",
							Value:         []string{"b"},
							Operator:      structured.Contains,
						},
						{
							DimensionName: "b",
							Value:         []string{"c"},
							Operator:      structured.Contains,
						},
					},
					ConditionList: []string{
						structured.ConditionAnd,
					},
				}),
				Start: "0",
				End:   "600",
			},
			data: `http://127.0.0.1:80/query?db=bk_split_measurement&q=show+tag+keys+from+bk_split_measurement+where+time+%3E+0+and+time+%3C+600000000000+and+%28a%3D%27b%27+and+b%3D%27c%27%29+and+bk_split_measurement%3D%27bk_split_measurement%27`,
		},
		"vm tag keys": {
			key:      infos.TagKeys,
			spaceUid: consul.VictoriaMetricsStorageType,
			params: &infos.Params{
				Metric: redis.BkSplitMeasurement,
				Conditions: structured.Conditions(struct {
					FieldList     []structured.ConditionField
					ConditionList []string
				}{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "a",
							Value:         []string{"b"},
							Operator:      structured.Contains,
						},
						{
							DimensionName: "b",
							Value:         []string{"c"},
							Operator:      structured.Contains,
						},
					},
					ConditionList: []string{
						structured.ConditionAnd,
					},
				}),
				Start: "0",
				End:   "600",
			},
			data: `{"sql":"{\"influx_compatible\":true,\"api_type\":\"labels\",\"api_params\":{\"match[]\":\"bk_split_measurement_value{a=\\\"b\\\",b=\\\"c\\\",bk_split_measurement=\\\"bk_split_measurement\\\"}\",\"start\":0,\"end\":600},\"result_table_group\":{\"bk_split_measurement_value\":[\"victoria_metrics\"]},\"metric_filter_condition\":null,\"metric_alias_mapping\":null}","bkdata_authentication_method":"token","bk_username":"admin","bk_app_code":"","prefer_storage":"vm","bkdata_data_token":"","bk_app_secret":""}`,
		},
		"influxdb tag values": {
			key:      infos.TagValues,
			spaceUid: consul.InfluxDBStorageType,
			params: &infos.Params{
				Metric: redis.BkSplitMeasurement,
				Conditions: structured.Conditions(struct {
					FieldList     []structured.ConditionField
					ConditionList []string
				}{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "a",
							Value:         []string{"b"},
							Operator:      structured.Contains,
						},
						{
							DimensionName: "b",
							Value:         []string{"c"},
							Operator:      structured.Contains,
						},
					},
					ConditionList: []string{
						structured.ConditionAnd,
					},
				}),
				Keys:  []string{"c"},
				Start: "0",
				End:   "600",
			},
			data: `http://127.0.0.1:80/query?db=bk_split_measurement&q=select+count%28value%29+from+bk_split_measurement+where+time+%3E+0+and+time+%3C+600000000000+and+%28a%3D%27b%27+and+b%3D%27c%27%29+and+bk_split_measurement%3D%27bk_split_measurement%27+group+by+c`,
		},
		"vm tag values": {
			key:      infos.TagValues,
			spaceUid: consul.VictoriaMetricsStorageType,
			params: &infos.Params{
				Metric: redis.BkSplitMeasurement,
				Conditions: structured.Conditions(struct {
					FieldList     []structured.ConditionField
					ConditionList []string
				}{
					FieldList: []structured.ConditionField{
						{
							DimensionName: "a",
							Value:         []string{"b"},
							Operator:      structured.Contains,
						},
						{
							DimensionName: "b",
							Value:         []string{"c"},
							Operator:      structured.Contains,
						},
					},
					ConditionList: []string{
						structured.ConditionAnd,
					},
				}),
				Keys:  []string{"c"},
				Start: "0",
				End:   "600",
			},
			data: `{"sql":"{\"influx_compatible\":true,\"api_type\":\"series\",\"api_params\":{\"match[]\":\"bk_split_measurement_value{a=\\\"b\\\",b=\\\"c\\\",bk_split_measurement=\\\"bk_split_measurement\\\"}\",\"start\":0,\"end\":600},\"result_table_group\":{\"bk_split_measurement_value\":[\"victoria_metrics\"]},\"metric_filter_condition\":null,\"metric_alias_mapping\":null}","bkdata_authentication_method":"token","bk_username":"admin","bk_app_code":"","prefer_storage":"vm","bkdata_data_token":"","bk_app_secret":""}`,
		},
	}

	for n, c := range tcs {
		t.Run(n, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			metadata.SetUser(ctx, c.spaceUid, c.spaceUid, "skip")

			log.Infof(ctx, "going")

			_, err := queryInfo(ctx, c.key, c.params)
			if c.err != nil {
				assert.Equal(t, c.err, err)
			} else {
				if c.spaceUid == consul.VictoriaMetricsStorageType {
					assert.Equal(t, c.data, string(mockCurl.Opts.Body))
				} else {
					assert.Equal(t, c.data, mockCurl.Opts.Body)
				}
			}
		})
	}

}
