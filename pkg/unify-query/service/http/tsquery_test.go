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
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/cartesian"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	tsdbInfluxdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/victoriaMetrics"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
	"github.com/stretchr/testify/assert"
)

// makeDimensions
func makeDimensions(prefix string, dCount ...int) ([]map[string]string, []string) {
	var (
		// 维度组合列表，元素为一个可用的维度组合
		dimensionList      = make([]map[string]string, 0)
		dimensionKeyList   = make([]string, 0, len(dCount))
		dimensionValueList = make([][]interface{}, 0)
	)

	// 1. 构建各个维度及其组合内容
	for i := 0; i < len(dCount); i++ {
		dimensionKey := fmt.Sprintf("%s_%d", prefix, i)
		// 构建key
		dimensionKeyList = append(dimensionKeyList, dimensionKey)
		valueList := make([]interface{}, 0, len(dCount))
		// 第N个Key，按照传入的维度个数进行模拟创建
		for vi := 0; vi < dCount[i]; vi++ {
			valueList = append(valueList, fmt.Sprintf("%d", vi))
		}
		dimensionValueList = append(dimensionValueList, valueList)
	}

	// 将dimensionValue中的内容进行笛卡尔积的组合
	c := cartesian.Iter(dimensionValueList...)
	for r := range c {
		dimensionSet := make(map[string]string)
		for index, keyName := range dimensionKeyList {
			dimensionSet[keyName] = r[index].(string)
		}
		dimensionList = append(dimensionList, dimensionSet)
	}

	return dimensionList, dimensionKeyList
}

// generateData
func generateData(metricName string, startValue int, dimensionsPrefix string, dimensionCount int, start, end int64, step time.Duration) string {
	resp := new(decoder.Response)
	resp.Results = make([]decoder.Result, 1)
	resp.Results[0].Series = make([]*decoder.Row, 0)

	// 生成列信息
	fields := []string{"_value", "_time"}
	dimensionList, dimensionKeyList := makeDimensions(dimensionsPrefix, dimensionCount)
	fields = append(fields, dimensionKeyList...)

	startTime := time.Unix(int64(math.Floor(float64(start)/60))*60, 0)
	endTime := time.Unix(int64(math.Floor(float64(end)/60))*60, 0)

	currentTime := startTime
	// 每个时间点得到一条数据
	currValue := startValue
	valueList := make([][]interface{}, 0)
	for currentTime.Before(endTime) {
		// 每个set都写入一次数据
		for _, dimensionSet := range dimensionList {
			currValue++

			timeStr := currentTime.UTC().Format("2006-01-02T15:04:05Z")
			values := []interface{}{currValue, timeStr}
			for _, dimensionKey := range dimensionKeyList {
				values = append(values, dimensionSet[dimensionKey])
			}
			valueList = append(valueList, values)
		}

		// 数字自增,时间自增
		currentTime = currentTime.Add(step)
	}
	resp.Results[0].Series = append(resp.Results[0].Series, &decoder.Row{
		Name:    metricName,
		Columns: fields,
		Values:  valueList,
	})
	result, err := json.Marshal(resp)
	if err != nil {
		return ""
	}
	return string(result)
}

func MockTsDB(t *testing.T) {
	mockCurl := curl.NewMockCurl(map[string]string{
		`http://127.0.0.1:80/query?db=2_bkmonitor_time_series_1582626&q=select+count%28%22value%22%29+as+_value%2C+time+as+_time+from+container_cpu_system_seconds_total+where+time+%3E+1669717379999000000+and+time+%3C+1669717739999000000+and+bcs_cluster_id%3D%27BCS-K8S-40949%27++group+by+time%281m0s%29+tz%28%27UTC%27%29`:                                                                                                                                                        ``,
		`http://127.0.0.1/api/query_range?end=1669717680&query=count%28container_cpu_system_seconds_total_value%7Bbcs_cluster_id%3D%22BCS-K8S-40949%22%7D%29&start=1669717380&step=60`:                                                                                                                                                                                                                                                                                                   `{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[{"metric":{},"values":[[1669717380,"35895"],[1669717440,"35900"],[1669717500,"39424"],[1669717560,"41380"],[1669717620,"43604"],[1669717680,"42659"]]}]}}`,
		`http://127.0.0.1/api/query_range?end=1669717680&query=sum%28count_over_time%28container_cpu_system_seconds_total_value%7Bbcs_cluster_id%3D%22BCS-K8S-40949%22%7D%5B1m%5D+offset+-59s999ms%29%29+%2B+count%28container_cpu_system_seconds_total_value%7Bbcs_cluster_id%3D%22BCS-K8S-40949%22%7D%29&start=1669717380&step=60`:                                                                                                                                                     `{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[{"metric":{},"values":[[1669717380,"70639"],[1669717440,"74007"],[1669717500,"79092"],[1669717560,"83808"],[1669717620,"85899"],[1669717680,"85261"]]}]}}`,
		`http://127.0.0.1/api/query_range?end=1669717680&query=sum+by%28pod_name%29+%28count_over_time%28container_cpu_system_seconds_total_value%7Bbcs_cluster_id%3D%22BCS-K8S-40949%22%2Cpod_name%3D~%22actor.%2A%22%7D%5B1m%5D+offset+-59s999ms%29%29&start=1669717380&step=60`:                                                                                                                                                                                                       `{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[{"metric":{"pod_name":"actor-train-train-11291730-bot-1f42-0"},"values":[[1669717380,"2"],[1669717560,"2"],[1669717620,"2"],[1669717680,"2"]]}]}}`,
		`http://127.0.0.1/api/query_range?end=1669717680&query=sum%28count_over_time%28container_cpu_system_seconds_total_value%7Bbcs_cluster_id%3D%22BCS-K8S-40949%22%7D%5B1m%5D+offset+-59s999ms%29%29&start=1669717380&step=60`:                                                                                                                                                                                                                                                       `{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[{"metric":{},"values":[[1669717380,"34744"],[1669717440,"38107"],[1669717500,"39668"],[1669717560,"42428"],[1669717620,"42295"],[1669717680,"42602"]]}]}}`,
		`http://127.0.0.1/api/query_range?end=1669717680&query=sum%28count_over_time%28container_cpu_system_seconds_total_value%7Bbcs_cluster_id%3D%22BCS-K8S-40949%22%2Cbcs_cluster_id%3D%22BCS-K8S-40949%22%2Cbk_biz_id%3D%22930%22%7D%5B1m%5D+offset+-59s999ms%29%29&start=1669717380&step=60`:                                                                                                                                                                                        `{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[{"metric":{},"values":[[1669717380,"34744"],[1669717440,"38107"],[1669717500,"39668"],[1669717560,"42428"],[1669717620,"42295"],[1669717680,"42602"]]}]}}`,
		`http://127.0.0.1/api/query_range?end=1669717680&query=sum%28count_over_time%28container_cpu_system_seconds_total_value%7Bbcs_cluster_id%3D%22BCS-K8S-40949%22%2Cbcs_cluster_id%3D%22BCS-K8S-40949%22%2Cbk_biz_id%3D%22930%22%7D%5B1m%5D+offset+-59s999ms%29%29+%2F+count%28count_over_time%28container_cpu_system_seconds_total_value%7Bbcs_cluster_id%3D%22BCS-K8S-40949%22%2Cbcs_cluster_id%3D%22BCS-K8S-40949%22%7D%5B1m%5D+offset+-59s999ms%29%29&start=1669717380&step=60`: `{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[{"metric":{},"values":[[1669717380,"1"],[1669717440,"1"],[1669717500,"1"],[1669717560,"1"],[1669717620,"1"],[1669717680,"1"]]}]}}`,
		`http://127.0.0.1:80/query?db=system&q=select+mean%28%22metric%22%29+as+_value%2C+time+as+_time+from+cpu_summary+where+time+%3E+1629820739999000000+and+time+%3C+1630252859999000000+and+dim_0%3D%271%27++group+by+time%282m0s%29`:                                                                                                                                                                                                                                               generateData("metric", 0, "dim", 5, 1629861029, 1629861329, 2*time.Minute),
	}, log.OtLogger)

	// 加载实例
	tsdb.SetStorage("10", &tsdb.Storage{
		Type: consul.VictoriaMetricsStorageType,
		Instance: &victoriaMetrics.Instance{
			Ctx:     context.TODO(),
			Address: "127.0.0.1",
			UriPath: "api",
			Timeout: time.Minute,
			Curl:    mockCurl,
		},
	})

	tsdb.SetStorage("0", &tsdb.Storage{
		Type: consul.InfluxDBStorageType,
		Instance: tsdbInfluxdb.NewInstance(
			context.TODO(),
			tsdbInfluxdb.Options{
				Host: "127.0.0.1",
				Port: 80,
				Curl: mockCurl,
			},
		),
	})
}

func MockSpace(t *testing.T) {
	ctx := context.Background()
	mock.SetRedisClient(context.TODO(), "test")
	path := "tsquery_test.db"
	bucketName := "tsquery_test"
	spaceId := "bkcc__2"
	mock.SetSpaceTsDbMockData(
		ctx, path, bucketName,
		ir.SpaceInfo{
			spaceId: ir.Space{
				"system.cpu_summary": &ir.SpaceResultTable{
					TableId: "system.cpu_summary",
					Filters: []map[string]string{},
				},
				"2_bkmonitor_time_series_1582626.__default__": &ir.SpaceResultTable{
					TableId: "2_bkmonitor_time_series_1582626.__default__",
					Filters: []map[string]string{
						{"bcs_cluster_id": "BCS-K8S-40949"},
					},
				},
				"64_bkmonitor_time_series_1573412.__default__": &ir.SpaceResultTable{
					TableId: "64_bkmonitor_time_series_1573412.__default__",
					Filters: []map[string]string{},
				},
			},
		},
		ir.ResultTableDetailInfo{
			"system.cpu_summary": &ir.ResultTableDetail{
				TableId:         "system.cpu_summary",
				Fields:          []string{"metric", "metric2"},
				MeasurementType: redis.BKTraditionalMeasurement,
				StorageId:       0,
				DB:              "system",
				Measurement:     "cpu_summary",
			},
			"2_bkmonitor_time_series_1582626.__default__": &ir.ResultTableDetail{
				TableId:         "2_bkmonitor_time_series_1582626.__default__",
				Fields:          []string{"bkbcs_workqueue_adds_total", "container_cpu_usage_seconds_total_value", "container_cpu_system_seconds_total"},
				MeasurementType: redis.BkSplitMeasurement,
				StorageId:       0,
				DB:              "2_bkmonitor_time_series_1582626",
				Measurement:     "__default__",
			},
			"64_bkmonitor_time_series_1573412.__default__": &ir.ResultTableDetail{
				TableId:         "64_bkmonitor_time_series_1573412.__default__",
				Fields:          []string{"jvm_memory_bytes_used", "jvm_memory_bytes_max"},
				MeasurementType: redis.BkSplitMeasurement,
				StorageId:       0,
				DB:              "64_bkmonitor_time_series_1573412",
				Measurement:     "__default__",
				ClusterName:     "default",
			},
		},
		ir.FieldToResultTable{
			"container_cpu_system_seconds_total": ir.ResultTableList{"2_bkmonitor_time_series_1582626.__default__"},
		},
		nil,
	)
}

// TestPromQueryBasic
func TestPromQueryBasic(t *testing.T) {

	var err error

	MockSpace(t)
	MockTsDB(t)

	// 基于当前点开始，获取10个点进行累加取平均值
	// 该计算结果应与下面单元测试的一分钟聚合结果吻合

	testCases := map[string]struct {
		spaceUid string
		data     string
		result   string
		err      error
	}{
		"a1_space_vm_field": {
			spaceUid: "bkcc__2",
			data:     `{"query_list":[{"table_id":"","field_name":"container_cpu_system_seconds_total","time_aggregation":{"function":"count_over_time","window":"60s"},"reference_name":"a","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[],"condition_list":[]},"function":[{"method":"sum","dimensions":[]}],"offset":"","offset_forward":false,"keep_columns":["_time","a"]}],"metric_merge":"a","start_time":"1669717380","end_time":"1669717680","step":"60s","space_uid":"bkcc__2","down_sample_range":""}`,
			result:   `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1669717380000,34744],[1669717440000,38107],[1669717500000,39668],[1669717560000,42428],[1669717620000,42295],[1669717680000,42602]]}]}`,
		},
		"a2_space_vm_field_where_and": {
			spaceUid: "bkcc__2",
			data:     `{"query_list":[{"table_id":"","field_name":"container_cpu_system_seconds_total","time_aggregation":{"function":"count_over_time","window":"60s"},"reference_name":"a","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["930"],"op":"contains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-40949"],"op":"contains"}],"condition_list":["and"]},"function":[{"method":"sum","dimensions":[]}],"offset":"","offset_forward":false,"keep_columns":["_time","a"]}],"metric_merge":"a","start_time":"1669717380","end_time":"1669717680","step":"60s","space_uid":"bkcc__2","down_sample_range":""}`,
			result:   `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1669717380000,34744],[1669717440000,38107],[1669717500000,39668],[1669717560000,42428],[1669717620000,42295],[1669717680000,42602]]}]}`,
		},

		"a1_space_vm_table_id_sum_by": {
			spaceUid: "bkcc__2",
			data:     `{"query_list":[{"table_id":"2_bkmonitor_time_series_1582626.__default__","field_name":"container_cpu_system_seconds_total","time_aggregation":{"function":"count_over_time","window":"60s"},"reference_name":"a","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bk_biz_id","value":["930"],"op":"contains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-40949"],"op":"contains"}],"condition_list":["and"]},"function":[{"method":"sum","dimensions":[]}],"offset":"","offset_forward":false,"keep_columns":["_time","a"]},{"table_id":"2_bkmonitor_time_series_1582626.__default__","field_name":"container_cpu_system_seconds_total","time_aggregation":{"function":"count_over_time","window":"60s"},"reference_name":"b","dimensions":[],"driver":"influxdb","time_field":"time","conditions":{"field_list":[{"field_name":"bcs_cluster_id","value":["BCS-K8S-40949"],"op":"contains"}],"condition_list":[]},"function":[{"method":"count","dimensions":[]}],"offset":"","offset_forward":false,"keep_columns":["_time","b"]}],"metric_merge":"a / b","start_time":"1669717380","end_time":"1669717680","step":"60s","space_uid":"bkcc__2","down_sample_range":""}`,
			result:   `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1669717380000,1],[1669717440000,1],[1669717500000,1],[1669717560000,1],[1669717620000,1],[1669717680000,1]]}]}`,
		},
	}
	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})

	// mock掉底层请求接口
	ctrl, stubs := FakePromData(t, true)
	defer stubs.Reset()
	defer ctrl.Finish()

	for name, testCase := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx := mock.Init(context.Background())
			query := &structured.QueryTs{}
			err = json.Unmarshal([]byte(testCase.data), &query)
			assert.Nil(t, err)
			if err == nil {
				query.SpaceUid = testCase.spaceUid
				resp, err := queryTs(ctx, query)
				if testCase.err != nil {
					assert.Equal(t, testCase.err, err)
				} else {
					assert.Nil(t, err)
					if err == nil {
						result, err2 := json.Marshal(resp)
						assert.Nil(t, err2)
						a := string(result)
						assert.Equal(t, testCase.result, a)
					}
				}
			}

		})
	}
}
