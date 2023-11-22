// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	tsdbInfluxdb "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/victoriaMetrics"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

var (
	once      sync.Once
	testModel *model
)

func init() {
	var err error
	once.Do(func() {
		ctx := context.Background()
		testModel, err = NewModel(ctx)
		if err != nil {
			panic(err)
		}
	})

	promql.NewEngine(&promql.Params{
		Timeout:              2 * time.Hour,
		MaxSamples:           500000,
		LookbackDelta:        2 * time.Minute,
		EnableNegativeOffset: true,
	})
}

func TestModel_Resources(t *testing.T) {
	ctx := context.Background()
	resources, err := testModel.resources(ctx)

	assert.Nil(t, err)
	assert.Equal(t, []cmdb.Resource{"cluster", "container", "container_cpu", "container_network", "deamonset", "deployment", "job", "namespace", "node", "pod", "replicaset", "statefulset", "system"}, resources)
}

func TestModel_GetResources(t *testing.T) {
	ctx := context.Background()
	index, err := testModel.getResource(ctx, "cluster")
	assert.Nil(t, err)
	assert.Equal(t, cmdb.Index{"bcs_cluster_id"}, index)

	index, err = testModel.getResource(ctx, "clb")
	assert.Equal(t, fmt.Errorf("resource is empty clb"), err)
}

func TestModel_GetPaths(t *testing.T) {
	ctx := context.Background()

	testCases := map[string]struct {
		target       cmdb.Resource
		matcher      cmdb.Matcher
		source       cmdb.Resource
		indexMatcher cmdb.Matcher
		expected     string
		error        error
	}{
		"container to system": {
			target: "system",
			matcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
				"namespace":      "ns-1",
				"pod":            "pod-1",
				"container":      "container-1",
				"test":           "1",
			},
			source: "pod",
			indexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
				"namespace":      "ns-1",
				"pod":            "pod-1",
			},
			expected: `[[{"V":["pod","node"]},{"V":["node","system"]}]]`,
		},
		"no target resource": {
			target: "multi_cluster",
			matcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
				"namespace":      "ns-1",
				"pod":            "pod-1",
				"container":      "container-1",
			},
			source: "container",
			indexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
				"namespace":      "ns-1",
				"pod":            "pod-1",
				"container":      "container-1",
			},
			error: fmt.Errorf("container => multi_cluster error: target vertex not reachable from source"),
		},
		"cls to ns": {
			target: "pod",
			matcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
				"demo":           "1",
			},
			source: "cluster",
			indexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
			},
			expected: `[[{"V":["cluster","node"]},{"V":["node","pod"]}]]`,
		},
		"node to system": {
			target: "system",
			matcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
				"node":           "node-1",
				"demo":           "1",
			},
			source: "node",
			indexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
				"node":           "node-1",
			},
			expected: `[[{"V":["node","system"]}]]`,
		},
	}

	for n, c := range testCases {
		t.Run(n, func(t *testing.T) {
			source, indexMatcher, err := testModel.getResourceFromMatch(ctx, c.matcher)
			assert.Nil(t, err)
			if err == nil {
				assert.Equal(t, c.source, source)
				assert.Equal(t, c.indexMatcher, indexMatcher)
				paths, err := testModel.getPaths(ctx, source, c.target, c.matcher)
				if c.error != nil {
					assert.Equal(t, c.error, err)
				} else {
					assert.Nil(t, err)
					if err == nil {
						actual, _ := json.Marshal(paths)
						assert.Equal(t, c.expected, string(actual))
					}
				}
			}

		})
	}
}

func mockData(ctx context.Context) *curl.TestCurl {
	mockCurl := curl.NewMockCurl(map[string]string{
		`http://127.0.0.1:80/query?chunk_size=10&chunked=true&db=2_bkmonitor_time_series_1572864&q=select+%22value%22+as+_value%2C+time+as+_time%2C%2A%3A%3Atag+from+node_with_system_relation+where+time+%3E+1693973867000000000+and+time+%3C+1693973987000000000+and+%28bcs_cluster_id%3D%27BCS-K8S-00000%27+and+node%3D%27node-127-0-0-1%27%29++limit+100000000+slimit+100000000+tz%28%27UTC%27%29`: `{"results":[{"series":[{"name":"node_with_system_relation","columns":["_time","_value","bcs_cluster_id","bk_biz_id","bk_endpoint_index","bk_endpoint_url","bk_instance","bk_job","bk_monitor_name","bk_monitor_namespace","bk_target_ip","endpoint","instance","job","monitor_type","namespace","node","pod","service"],"values":[[1693973874000000000,1,"BCS-K8S-00000","2","0","http://127.0.0.1:8080/relation/metrics","127.0.0.1:8080","bkmonitor-operator-operator","bkmonitor-operator-operator","bkmonitor-operator","127.0.0.1","http","127.0.0.1:8080","bkmonitor-operator-operator","ServiceMonitor","bkmonitor-operator","node-127-0-0-1","bkm-operator-6b4768bb58-lxhnr","bkmonitor-operator-operator"],[1693973934000000000,1,"BCS-K8S-00000","2","0","http://127.0.0.1:8080/relation/metrics","127.0.0.1:8080","bkmonitor-operator-operator","bkmonitor-operator-operator","bkmonitor-operator","127.0.0.1","http","127.0.0.1:8080","bkmonitor-operator-operator","ServiceMonitor","bkmonitor-operator","node-127-0-0-1","bkm-operator-6b4768bb58-lxhnr","bkmonitor-operator-operator"]]}]}]}
`,
		`http://127.0.0.1:80/query?chunk_size=10&chunked=true&db=2_bkmonitor_time_series_1572864&q=select+%22value%22+as+_value%2C+time+as+_time%2C%2A%3A%3Atag+from+node_with_system_relation+where+time+%3E+1693973867000000000+and+time+%3C+1693973987000000000+and+bk_target_ip%3D%27127.0.0.1%27++limit+100000000+slimit+100000000+tz%28%27UTC%27%29`: `{"results":[{"series":[{"name":"node_with_system_relation","columns":["_time","_value","bcs_cluster_id","bk_biz_id","bk_endpoint_index","bk_endpoint_url","bk_instance","bk_job","bk_monitor_name","bk_monitor_namespace","bk_target_ip","endpoint","instance","job","monitor_type","namespace","node","pod","service"],"values":[[1693973874000000000,1,"BCS-K8S-00000","2","0","http://127.0.0.1:8080/relation/metrics","127.0.0.1:8080","bkmonitor-operator-operator","bkmonitor-operator-operator","bkmonitor-operator","127.0.0.1","http","127.0.0.1:8080","bkmonitor-operator-operator","ServiceMonitor","bkmonitor-operator","node-127-0-0-1","bkm-operator-6b4768bb58-lxhnr","bkmonitor-operator-operator"],[1693973934000000000,1,"BCS-K8S-00000","2","0","http://127.0.0.1:8080/relation/metrics","127.0.0.1:8080","bkmonitor-operator-operator","bkmonitor-operator-operator","bkmonitor-operator","127.0.0.1","http","127.0.0.1:8080","bkmonitor-operator-operator","ServiceMonitor","bkmonitor-operator","node-127-0-0-1","bkm-operator-6b4768bb58-lxhnr","bkmonitor-operator-operator"]]}]}]}
`,
		`http://127.0.0.1:80/query?chunk_size=10&chunked=true&db=2_bkmonitor_time_series_1572864&q=select+%22value%22+as+_value%2C+time+as+_time%2C%2A%3A%3Atag+from+node_with_pod_relation+where+time+%3E+1693973867000000000+and+time+%3C+1693973987000000000+and+%28bcs_cluster_id%3D%27BCS-K8S-00000%27+and+node%3D%27node-127-0-0-1%27%29++limit+100000000+slimit+100000000+tz%28%27UTC%27%29`: `{"results":[{"series":[{"name":"node_with_pod_relation","columns":["_time","_value","bcs_cluster_id","bk_biz_id","bk_endpoint_index","bk_endpoint_url","bk_instance","bk_job","bk_monitor_name","bk_monitor_namespace","bk_target_ip","endpoint","instance","job","monitor_type","namespace","node","pod","service"],"values":[[1693973874000000000,1,"BCS-K8S-00000","2","0","http://127.0.0.1:8080/relation/metrics","127.0.0.1:8080","bkmonitor-operator-operator","bkmonitor-operator-operator","bkmonitor-operator","127.0.0.1","http","127.0.0.1:8080","bkmonitor-operator-operator","ServiceMonitor","bkmonitor-operator","node-127-0-0-1","bkm-pod-1","bkmonitor-operator-operator"],[1693973934000000000,1,"BCS-K8S-00000","2","0","http://127.0.0.1:8080/relation/metrics","127.0.0.1:8080","bkmonitor-operator-operator","bkmonitor-operator-operator","bkmonitor-operator","127.0.0.1","http","127.0.0.1:8080","bkmonitor-operator-operator","ServiceMonitor","bkmonitor-operator","node-127-0-0-1","bkm-pod-2","bkmonitor-operator-operator"]]}]}]}
`,
		`http://127.0.0.1:80/query?chunk_size=10&chunked=true&db=2_bkmonitor_time_series_1572864&q=select+%22value%22+as+_value%2C+time+as+_time%2C%2A%3A%3Atag+from+node_with_pod_relation+where+time+%3E+1693973867000000000+and+time+%3C+1693973987000000000+and+%28bcs_cluster_id%3D%27BCS-K8S-00000%27+and+%28namespace%3D%27bkmonitor-operator%27+and+pod%3D%27bkm-pod-1%27%29%29++limit+100000000+slimit+100000000+tz%28%27UTC%27%29`: `{"results":[{"series":[{"name":"node_with_pod_relation","columns":["_time","_value","bcs_cluster_id","bk_biz_id","bk_endpoint_index","bk_endpoint_url","bk_instance","bk_job","bk_monitor_name","bk_monitor_namespace","bk_target_ip","endpoint","instance","job","monitor_type","namespace","node","pod","service"],"values":[[1693973874000000000,1,"BCS-K8S-00000","2","0","http://127.0.0.1:8080/relation/metrics","127.0.0.1:8080","bkmonitor-operator-operator","bkmonitor-operator-operator","bkmonitor-operator","127.0.0.1","http","127.0.0.1:8080","bkmonitor-operator-operator","ServiceMonitor","bkmonitor-operator","node-127-0-0-1","bkm-pod-1","bkmonitor-operator-operator"],[1693973934000000000,1,"BCS-K8S-00000","2","0","http://127.0.0.1:8080/relation/metrics","127.0.0.1:8080","bkmonitor-operator-operator","bkmonitor-operator-operator","bkmonitor-operator","127.0.0.1","http","127.0.0.1:8080","bkmonitor-operator-operator","ServiceMonitor","bkmonitor-operator","node-127-0-0-1","bkm-pod-2","bkmonitor-operator-operator"]]}]}]}
`,
		`http://127.0.0.1:80/query?chunk_size=10&chunked=true&db=2_bkmonitor_time_series_1572864&q=select+%22value%22+as+_value%2C+time+as+_time%2C%2A%3A%3Atag+from+node_with_system_relation+where+time+%3E+1693973867000000000+and+time+%3C+1693973987000000000+and+%28%28bcs_cluster_id%3D%27BCS-K8S-00000%27+and+node%3D%27node-127-0-0-1%27%29+or+%28bcs_cluster_id%3D%27BCS-K8S-00000%27+and+node%3D%27node-127-0-0-1%27%29%29++limit+100000000+slimit+100000000+tz%28%27UTC%27%29`: `{"results":[{"series":[{"name":"node_with_system_relation","columns":["_time","_value","bcs_cluster_id","bk_biz_id","bk_endpoint_index","bk_endpoint_url","bk_instance","bk_job","bk_monitor_name","bk_monitor_namespace","bk_target_ip","endpoint","instance","job","monitor_type","namespace","node","pod","service"],"values":[[1693973874000000000,1,"BCS-K8S-00000","2","0","http://127.0.0.1:8080/relation/metrics","127.0.0.1:8080","bkmonitor-operator-operator","bkmonitor-operator-operator","bkmonitor-operator","127.0.0.1","http","127.0.0.1:8080","bkmonitor-operator-operator","ServiceMonitor","bkmonitor-operator","node-127-0-0-1","bkm-operator-6b4768bb58-lxhnr","bkmonitor-operator-operator"],[1693973934000000000,1,"BCS-K8S-00000","2","0","http://127.0.0.1:8080/relation/metrics","127.0.0.1:8080","bkmonitor-operator-operator","bkmonitor-operator-operator","bkmonitor-operator","127.0.0.1","http","127.0.0.1:8080","bkmonitor-operator-operator","ServiceMonitor","bkmonitor-operator","node-127-0-0-1","bkm-operator-6b4768bb58-lxhnr","bkmonitor-operator-operator"]]}]}]}
`,
		`victoria_metric/api`: `{}`,
	}, nil)

	metadata.GetQueryRouter().MockSpaceUid(consul.VictoriaMetricsStorageType)

	vmStorageID := "1"
	vmStorageIDInt := int64(1)
	influxdbStorageID := "2"
	influxdbStorageIDInt := int64(2)

	tsdb.SetStorage(vmStorageID, &tsdb.Storage{
		Type: consul.VictoriaMetricsStorageType,
		Instance: &victoriaMetrics.Instance{
			Ctx:                  ctx,
			Address:              "victoria_metric",
			UriPath:              "api",
			Curl:                 mockCurl,
			InfluxCompatible:     true,
			AuthenticationMethod: "token",
		},
	})
	tsdb.SetStorage(influxdbStorageID, &tsdb.Storage{
		Type: consul.InfluxDBStorageType,
		Instance: tsdbInfluxdb.NewInstance(
			context.TODO(),
			tsdbInfluxdb.Options{
				Host:      "127.0.0.1",
				Port:      80,
				Curl:      mockCurl,
				ChunkSize: 10,
				MaxSlimit: 1e8,
				MaxLimit:  1e8,
				Timeout:   time.Hour,
			},
		),
	})
	mock.SetRedisClient(ctx, "test_model")
	mock.SetSpaceTsDbMockData(
		ctx, "v1beat1_test", "v1beat1_test",
		ir.SpaceInfo{
			consul.InfluxDBStorageType: ir.Space{
				"db.measurement": &ir.SpaceResultTable{
					TableId: "db.measurement",
					Filters: []map[string]string{},
				},
			},
			consul.VictoriaMetricsStorageType: ir.Space{
				"db_vm.measurement": &ir.SpaceResultTable{
					TableId: "db_vm.measurement",
					Filters: []map[string]string{},
				},
			},
		},
		ir.ResultTableDetailInfo{
			"db.measurement": &ir.ResultTableDetail{
				Fields:          []string{"node_with_system_relation", "node_with_pod_relation"},
				MeasurementType: redis.BkSplitMeasurement,
				DataLabel:       "datalabel",
				StorageId:       influxdbStorageIDInt,
				DB:              "2_bkmonitor_time_series_1572864",
				Measurement:     "__default__",
			},
			"db_vm.measurement": &ir.ResultTableDetail{
				Fields:          []string{"node_with_system_relation", "node_with_pod_relation"},
				MeasurementType: redis.BkSplitMeasurement,
				DataLabel:       "datalabel",
				StorageId:       vmStorageIDInt,
				DB:              "db_vm",
				Measurement:     "__default__",
				VmRt:            "2_bkmonitor_time_series_1572864_vm_rt",
			},
		},
		ir.FieldToResultTable{
			"node_with_system_relation": ir.ResultTableList{"db.measurement", "db_vm.measurement"},
			"node_with_pod_relation":    ir.ResultTableList{"db.measurement", "db_vm.measurement"},
		},
		nil,
	)
	return mockCurl
}

func TestModel_GetResourceMatcher(t *testing.T) {
	ctx := context.Background()

	mockData(ctx)

	testCases := map[string]struct {
		spaceUid string
		target   cmdb.Resource
		matcher  cmdb.Matcher

		expected struct {
			source     cmdb.Resource
			sourceInfo cmdb.Matcher

			targetList cmdb.Matchers
		}
		error error
	}{
		"vm node to system": {
			spaceUid: consul.VictoriaMetricsStorageType,
			target:   "system",
			matcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00000",
				"node":           "node-127-0-0-1",
				"demo":           "1",
			},
			expected: struct {
				source     cmdb.Resource
				sourceInfo cmdb.Matcher
				targetList cmdb.Matchers
			}{
				source: "node",
				sourceInfo: cmdb.Matcher{
					"bcs_cluster_id": "BCS-K8S-00000",
					"node":           "node-127-0-0-1",
				},
				targetList: cmdb.Matchers{
					cmdb.Matcher{
						"bk_target_ip": "127.0.0.1",
					},
				},
			},
		},
		"node to system": {
			spaceUid: consul.InfluxDBStorageType,
			target:   "system",
			matcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00000",
				"node":           "node-127-0-0-1",
				"demo":           "1",
			},
			expected: struct {
				source     cmdb.Resource
				sourceInfo cmdb.Matcher
				targetList cmdb.Matchers
			}{
				source: "node",
				sourceInfo: cmdb.Matcher{
					"bcs_cluster_id": "BCS-K8S-00000",
					"node":           "node-127-0-0-1",
				},
				targetList: cmdb.Matchers{
					cmdb.Matcher{
						"bk_target_ip": "127.0.0.1",
					},
				},
			},
		},
		"system to pod": {
			spaceUid: consul.InfluxDBStorageType,
			target:   "pod",
			matcher: cmdb.Matcher{
				"bk_target_ip":   "127.0.0.1",
				"bcs_cluster_id": "BCS-K8S-00000",
			},
			expected: struct {
				source     cmdb.Resource
				sourceInfo cmdb.Matcher
				targetList cmdb.Matchers
			}{
				source: "system",
				sourceInfo: cmdb.Matcher{
					"bk_target_ip": "127.0.0.1",
				},
				targetList: cmdb.Matchers{
					cmdb.Matcher{
						"bcs_cluster_id": "BCS-K8S-00000",
						"namespace":      "bkmonitor-operator",
						"pod":            "bkm-pod-1",
					},
					cmdb.Matcher{
						"bcs_cluster_id": "BCS-K8S-00000",
						"namespace":      "bkmonitor-operator",
						"pod":            "bkm-pod-2",
					},
				},
			},
		},
		"pod_name to system": {
			spaceUid: consul.InfluxDBStorageType,
			target:   "system",
			matcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00000",
				"namespace":      "bkmonitor-operator",
				"pod_name":       "bkm-pod-1",
			},
			expected: struct {
				source     cmdb.Resource
				sourceInfo cmdb.Matcher
				targetList cmdb.Matchers
			}{
				source: "pod",
				sourceInfo: cmdb.Matcher{
					"bcs_cluster_id": "BCS-K8S-00000",
					"namespace":      "bkmonitor-operator",
					"pod":            "bkm-pod-1",
				},
				targetList: cmdb.Matchers{
					cmdb.Matcher{
						"bk_target_ip": "127.0.0.1",
					},
				},
			},
		},
	}

	timestamp := int64(1693973987)
	for n, c := range testCases {
		t.Run(n, func(t *testing.T) {
			metadata.SetUser(ctx, c.spaceUid, c.spaceUid)
			source, indexMatcher, matchers, err := testModel.GetResourceMatcher(ctx, "", c.spaceUid, timestamp, c.target, c.matcher)
			assert.Nil(t, err)
			if err == nil {
				assert.Equal(t, c.expected.source, source)
				assert.Equal(t, c.expected.sourceInfo, indexMatcher)
				assert.Equal(t, c.expected.targetList, matchers)
			}
		})
	}
}
