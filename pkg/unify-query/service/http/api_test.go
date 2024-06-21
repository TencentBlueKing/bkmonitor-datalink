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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/infos"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

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
			ctx, _ = context.WithCancel(ctx)
			metadata.SetUser(ctx, c.spaceUid, c.spaceUid)

			_, err := queryInfo(ctx, c.key, c.params)
			if c.err != nil {
				assert.Equal(t, c.err, err)
			} else {
				if c.spaceUid == consul.VictoriaMetricsStorageType {
					assert.Equal(t, c.data, string(mockCurl.Params))
				} else {
					assert.Equal(t, c.data, mockCurl.Url)
				}
			}
		})
	}

}
