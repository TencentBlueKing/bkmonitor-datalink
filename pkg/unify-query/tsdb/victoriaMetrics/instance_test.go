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
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

const (
	TestTime  = "2022-11-28 10:00:00"
	ParseTime = "2006-01-02 15:04:05"
)

var (
	once     sync.Once
	instance tsdb.Instance
)

var (
	end   = time.Now()
	start = end.Add(-10 * time.Minute)
	step  = time.Minute

	rts = []string{"100147_vm_100768_bkmonitor_time_series_560915"}
)

func mockData(ctx context.Context) {
	metadata.SetExpand(ctx, &metadata.VmExpand{
		ResultTableList: []string{"vm1"},
	})
}

func query(ctx context.Context, promql string, rts []string, data map[string]float64) error {
	if len(rts) > 0 {
		metadata.SetExpand(ctx, &metadata.VmExpand{
			ResultTableList: rts,
		})
		res, err := instance.Query(ctx, promql, time.Now())
		if err != nil {
			return err
		}
		if len(res) > 0 {
			for _, r := range res {
				var (
					metric string
					id     string
				)
				for _, l := range r.Metric {
					switch {
					case l.Name == "__name__":
						metric = l.Value
					case l.Name == "bcs_cluster_id":
						id = l.Value
					default:
						panic(fmt.Sprintf("%s=%s", l.Name, l.Value))
					}
				}

				if _, ok := data[metric+","+id]; !ok {
					data[metric+","+id] = r.V
				}
			}

			return nil
		}
	}
	return fmt.Errorf("empty data in %+v", rts)
}

func TestPromQL(t *testing.T) {
	ctx := context.Background()
	mock.Init()

	once.Do(func() {
		instance = &Instance{
			ContentType:      "application/json",
			InfluxCompatible: true,
			UseNativeOr:      true,
			Timeout:          time.Second * 30,
			Curl: &curl.HttpCurl{
				Log: log.DefaultLogger,
			},
		}
	})

	vectors := []string{
		`kube_node_status_allocatable_cpu_cores_value`,
		`kube_node_status_capacity_cpu_cores_value`,
		//`kube_pod_container_resource_requests_value{resource="cpu"}`,
		`kube_pod_container_resource_requests_cpu_cores_value`,
		//`kube_pod_container_resource_limits_value{resource="cpu"}`,
		`kube_pod_container_resource_limits_cpu_cores_value`,
	}
	dims := []string{
		"bcs_cluster_id",
		"__name__",
	}

	var (
		data = make(map[string]float64)
	)
	f, err := os.Open("vmrt.list")
	if err != nil {
		log.Errorf(ctx, err.Error())
	}
	defer f.Close()

	batch := 100
	br := bufio.NewReader(f)
	rts := make([]string, 0, batch)
	for {
		rt, _, readErr := br.ReadLine()
		if len(rt) > 0 {
			rts = append(rts, string(rt))
		}

		if readErr != nil || len(rts) == batch {
			promql := fmt.Sprintf(`sum({__name__=~"%s", result_table_id=~"%s"})`, strings.Join(vectors, "|"), strings.Join(rts, "|"))
			if len(dims) > 0 {
				promql = fmt.Sprintf(`%s by (%s)`, promql, strings.Join(dims, ", "))
			}

			err = query(ctx, promql, rts, data)
			if err != nil {
				log.Errorf(ctx, err.Error())
			}
			rts = rts[:0]
		}

		if readErr != nil {
			break
		}
	}

	file, err := os.Create("output.csv")
	if err != nil {
		return
	}
	defer file.Close()
	for k, v := range data {
		_, err = file.WriteString(fmt.Sprintf("%s,%.f\n", k, v))
		if err != nil {
			log.Errorf(ctx, err.Error())
		}
	}
}

func TestRealQueryRange(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	vmRT := "2_bcs_prom_computation_result_table"
	metric := "container_cpu_usage_seconds_total"

	a := "a"
	metricFilterCondition := map[string]string{
		a: fmt.Sprintf(`__name__="%s_value", result_table_id="%s"`, metric, vmRT),
	}

	timeout := time.Minute

	ins := &Instance{
		ctx:         ctx,
		Timeout:     timeout,
		ContentType: "application/json",

		Curl: &curl.HttpCurl{Log: log.DefaultLogger},

		InfluxCompatible: true,
		UseNativeOr:      true,
	}

	testCase := map[string]struct {
		q string
		e *metadata.VmExpand
	}{
		"test_1": {
			q: `count(a)`,
			e: &metadata.VmExpand{
				ResultTableList: []string{
					vmRT,
				},
				// condition 需要进行二次转义
				MetricFilterCondition: metricFilterCondition,
			},
		},
	}

	for n, c := range testCase {
		t.Run(n, func(t *testing.T) {
			metadata.SetExpand(ctx, c.e)
			res, err := ins.Query(ctx, c.q, end)
			if err != nil {
				panic(err)
			}
			fmt.Println(res)
		})
	}
}

func TestInstance_Query_Url(t *testing.T) {
	mock.Init()

	mockCurl := curl.NewMockCurl(map[string]string{
		`http://127.0.0.1/api/{"sql":"{\"influx_compatible\":false,\"use_native_or\":false,\"api_type\":\"query\",\"cluster_name\":\"\",\"api_params\":{\"query\":\"count(container_cpu_system_seconds_total_value)\",\"time\":1669600800,\"timeout\":0},\"result_table_list\":[\"vm1\"],\"metric_filter_condition\":null}","bkdata_authentication_method":"","bk_app_code":"","prefer_storage":"vm","bkdata_data_token":""}`: `{
    "result": true,
    "message": "成功",
    "code": "00",
    "data": {
        "list": [
            {
                "status": "success",
                "isPartial": false,
                "data": {
                    "resultType": "vector",
                    "result": [
                        {
                            "metric": {},
                            "value": [
                                1716522171,
                                "169.52247191011236"
                            ]
                        }
                    ]
                },
                "stats": {
                    "seriesFetched": "40"
                }
            }
        ],
        "select_fields_order": [],
        "sql": "count(container_cpu_system_seconds_total_value)",
        "total_record_size": 1704,
        "device": "vm"
    }
}`,
		`http://127.0.0.1/api/{"sql":"{\"influx_compatible\":false,\"use_native_or\":false,\"api_type\":\"query\",\"cluster_name\":\"\",\"api_params\":{\"query\":\"count by (__bk_db__, bk_biz_id, bcs_cluster_id) (container_cpu_system_seconds_total_value{})\",\"time\":1669600800,\"timeout\":0},\"result_table_list\":[\"vm1\"],\"metric_filter_condition\":null}","bkdata_authentication_method":"","bk_app_code":"","prefer_storage":"vm","bkdata_data_token":""}`: `{
"result": true,
    "message": "成功",
    "code": "00",
    "data": {
        "list": [
            {"status":"success","isPartial":false,"data":{"resultType":"vector","result":[{"metric":{"__bk_db__":"mydb","bcs_cluster_id":"BCS-K8S-40949","bk_biz_id":"930"},"value":[1669600800,"31949"]}]}}
        ],
        "select_fields_order": [],
        "sql": "count(container_cpu_system_seconds_total_value)",
        "total_record_size": 1704,
        "device": "vm"
	}
}`,
		`http://127.0.0.1/api/{"sql":"{\"influx_compatible\":false,\"use_native_or\":false,\"api_type\":\"query\",\"cluster_name\":\"\",\"api_params\":{\"query\":\"sum(111gggggggggggggggg11\",\"time\":1669600800,\"timeout\":0},\"result_table_list\":[\"vm1\"],\"metric_filter_condition\":null}","bkdata_authentication_method":"","bk_app_code":"","prefer_storage":"vm","bkdata_data_token":""}`: `{
    "result": false,
    "message": "BKPromqlApi 接口调用异常",
    "code": "1532618",
    "data": null,
    "errors": {
        "error": "Failed to convert promql with influx filter"
    }
}`,
	}, log.DefaultLogger)

	ctx := metadata.InitHashID(context.Background())
	ins := &Instance{
		ctx:  ctx,
		Curl: mockCurl,
	}
	mockData(ctx)

	endTime, _ := time.ParseInLocation(ParseTime, TestTime, time.Local)

	testCases := map[string]struct {
		promql   string
		expected string
		err      error
	}{
		"count": {
			promql:   `count(container_cpu_system_seconds_total_value)`,
			expected: `[{"metric":{},"value":[1716522171,"169.52247191011236"]}]`,
		},
		"count rate metric": {
			promql:   `count by (__bk_db__, bk_biz_id, bcs_cluster_id) (container_cpu_system_seconds_total_value{})`,
			expected: `[{"metric":{"__bk_db__":"mydb","bcs_cluster_id":"BCS-K8S-40949","bk_biz_id":"930"},"value":[1669600800,"31949"]}]`,
		},
		"error metric 1": {
			promql: `sum(111gggggggggggggggg11`,
			err:    errors.New(`BKPromqlApi 接口调用异常, Failed to convert promql with influx filter, `),
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			data, err := ins.Query(ctx, c.promql, endTime)
			if c.err != nil {
				assert.Equal(t, c.err, err)
			} else {
				assert.Nil(t, err)
				res, err1 := json.Marshal(data)
				assert.Nil(t, err1)
				assert.Equal(t, c.expected, string(res))
			}

		})
	}
}

func TestInstance_QueryRange_Url(t *testing.T) {
	log.InitTestLogger()
	ctx := context.Background()

	mockCurl := curl.NewMockCurl(map[string]string{
		`http://127.0.0.1/api/query_range?end=1669600800&query=count%28kube_pod_container_resource_limits_value%29&start=1669600500&step=60`:                                                          `{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[{"metric":{},"values":[[1669600500,"61305"],[1669600560,"61305"],[1669600620,"61305"],[1669600680,"61311"],[1669600740,"61311"],[1669600800,"61314"]]}]}}`,
		`http://127.0.0.1/api/query_range?end=1669600800&query=count+by+%28__bk_db__%2C+bk_biz_id%2C+bcs_cluster_id%29+%28container_cpu_system_seconds_total_value%7B%7D%29&start=1669600500&step=60`: `{"status":"success","isPartial":false,"data":{"resultType":"matrix","result":[{"metric":{"__bk_db__":"mydb","bcs_cluster_id":"BCS-K8S-40949","bk_biz_id":"930"},"values":[[1669600500,"31949"],[1669600560,"31949"],[1669600620,"31949"],[1669600680,"31949"],[1669600740,"31949"],[1669600800,"31949"]]}]}}`,
		`http://127.0.0.1/api/query_range?end=1669600800&query=sum%28111gggggggggggggggg11&start=1669600500&step=60`:                                                                                  `{"status":"error","errorType":"422","error":"error when executing query=\"sum(111gggggggggggggggg11\" on the time range (start=1669600500000, end=1669600800000, step=60000): argList: unexpected token \"gggggggggggggggg11\"; want \",\", \")\"; unparsed data: \"gggggggggggggggg11\""}`,
		`http://127.0.0.1/api/query_range?end=1669600800&query=top%28sum%28kube_pod_container_resource_limits_value%29%29&start=1669600500&step=60`:                                                   `{"status":"error","errorType":"422","error":"unknown func \"top\""}`,
	}, log.DefaultLogger)

	ins := &Instance{
		ctx:     ctx,
		Timeout: time.Minute,
		Curl:    mockCurl,
	}
	mockData(ctx)

	leftTime := time.Minute * -5

	endTime, _ := time.ParseInLocation(ParseTime, TestTime, time.Local)
	startTime := endTime.Add(leftTime)
	stepTime := time.Minute

	testCases := map[string]struct {
		promql   string
		expected string
		err      error
	}{
		"count": {
			promql:   `count(kube_pod_container_resource_limits_value)`,
			expected: `[{"metric":{},"values":[[1669600500,"61305"],[1669600560,"61305"],[1669600620,"61305"],[1669600680,"61311"],[1669600740,"61311"],[1669600800,"61314"]]}]`,
		},
		"count rate metric": {
			promql:   `count by (__bk_db__, bk_biz_id, bcs_cluster_id) (container_cpu_system_seconds_total_value{})`,
			expected: `[{"metric":{"__bk_db__":"mydb","bcs_cluster_id":"BCS-K8S-40949","bk_biz_id":"930"},"values":[[1669600500,"31949"],[1669600560,"31949"],[1669600620,"31949"],[1669600680,"31949"],[1669600740,"31949"],[1669600800,"31949"]]}]`,
		},
		"error metric 1": {
			promql: `sum(111gggggggggggggggg11`,
			err:    errors.New(`error when executing query="sum(111gggggggggggggggg11" on the time range (start=1669600500000, end=1669600800000, step=60000): argList: unexpected token "gggggggggggggggg11"; want ",", ")"; unparsed data: "gggggggggggggggg11"`),
		},
		"error metric 2": {
			promql: `top(sum(kube_pod_container_resource_limits_value))`,
			err:    errors.New(`unknown func "top"`),
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			data, err := ins.QueryRange(ctx, c.promql, startTime, endTime, stepTime)
			if c.err != nil {
				assert.Equal(t, c.err, err)
			} else {
				assert.Nil(t, err)
				res, err1 := json.Marshal(data)
				assert.Nil(t, err1)
				assert.Equal(t, c.expected, string(res))
			}

		})
	}
}

func mockInstance(ctx context.Context) {
	instance = &Instance{
		ctx:              ctx,
		ContentType:      "application/json",
		InfluxCompatible: true,
		UseNativeOr:      true,
		Timeout:          time.Minute,
		Curl:             &curl.HttpCurl{Log: log.DefaultLogger},
	}
}

func TestInstance_QueryRange(t *testing.T) {
	ctx := context.Background()
	mock.Init()
	mockInstance(ctx)

	for i, c := range []struct {
		promQL  string
		filters map[string]string
	}{
		{
			promQL: `count(a[1m] offset -59s999ms) by (bcs_cluster_id)`,
			filters: map[string]string{
				`a`: `result_table_id="100147_vm_100768_bkmonitor_time_series_560915", __name__="container_cpu_usage_seconds_total_value", bcs_cluster_id="BCS-K8S-41264"`,
			},
		},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			metadata.SetExpand(ctx, &metadata.VmExpand{
				ResultTableList:       rts,
				MetricFilterCondition: c.filters,
			})
			res, err := instance.QueryRange(ctx, c.promQL, start, end, step)
			assert.Nil(t, err)
			log.Infof(ctx, "%+v", res)
		})
	}
}

func TestInstance_Query(t *testing.T) {
	ctx := context.Background()
	mock.Init()
	mockInstance(ctx)

	for i, c := range []struct {
		promQL  string
		filters map[string]string
	}{
		{
			promQL: `count(a[1m] offset -59s999ms) by (bcs_cluster_id)`,
			filters: map[string]string{
				`a`: `result_table_id="100147_vm_100768_bkmonitor_time_series_560915", __name__="container_cpu_usage_seconds_total_value", bcs_cluster_id="BCS-K8S-41264"`,
			},
		},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			metadata.SetExpand(ctx, &metadata.VmExpand{
				ResultTableList:       rts,
				MetricFilterCondition: c.filters,
			})
			res, err := instance.Query(ctx, c.promQL, end)
			assert.Nil(t, err)
			log.Infof(ctx, "%+v", res)
		})
	}
}

func TestInstance_LabelNames(t *testing.T) {
	ctx := context.Background()
	mock.Init()
	mockInstance(ctx)

	lbl, _ := labels.NewMatcher(labels.MatchEqual, labels.MetricName, "a")

	for i, c := range []struct {
		filters map[string]string
	}{
		{
			filters: map[string]string{
				`a`: `result_table_id="100147_vm_100768_bkmonitor_time_series_560915", __name__="container_cpu_usage_seconds_total_value", bcs_cluster_id="BCS-K8S-41264"`,
			},
		},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			metadata.SetExpand(ctx, &metadata.VmExpand{
				ResultTableList:       rts,
				MetricFilterCondition: c.filters,
			})
			res, err := instance.LabelNames(ctx, nil, start, end, lbl)
			assert.Nil(t, err)
			log.Infof(ctx, "%+v", res)
		})
	}
}

func TestInstance_LabelValues(t *testing.T) {
	ctx := context.Background()
	mock.Init()
	mockInstance(ctx)

	lbl, _ := labels.NewMatcher(labels.MatchEqual, labels.MetricName, "a")

	for i, c := range []struct {
		filters map[string]string
	}{
		{
			filters: map[string]string{
				`a`: `result_table_id="100147_vm_100768_bkmonitor_time_series_560915", __name__="container_cpu_usage_seconds_total_value", bcs_cluster_id="BCS-K8S-41264"`,
			},
		},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			metadata.SetExpand(ctx, &metadata.VmExpand{
				ResultTableList:       rts,
				MetricFilterCondition: c.filters,
			})
			res, err := instance.LabelValues(ctx, nil, "namespace", start, end, lbl)
			assert.Nil(t, err)
			log.Infof(ctx, "%+v", res)
		})
	}
}
