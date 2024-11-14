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
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/victoriaMetrics"
)

var (
	testModel, _ = newModel(context.Background())
)

func TestModel_Resources(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	resources, err := testModel.resources(ctx)

	assert.Nil(t, err)
	assert.Equal(t, []cmdb.Resource{"apm_service", "apm_service_instance", "bklogconfig", "datasource", "deamonset", "deployment", "domain", "ingress", "job", "k8s_address", "node", "pod", "replicaset", "service", "statefulset", "system"}, resources)
}

func TestModel_GetResources(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	index, err := testModel.getResourceIndex(ctx, "k8s_address")
	assert.Nil(t, err)
	assert.Equal(t, cmdb.Index{"bcs_cluster_id", "address"}, index)

	index, err = testModel.getResourceIndex(ctx, "clb")
	assert.Equal(t, fmt.Errorf("resource is empty clb"), err)
}

func TestModel_GetPath(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	testCases := map[string]struct {
		target       cmdb.Resource
		matcher      cmdb.Matcher
		source       cmdb.Resource
		indexMatcher cmdb.Matcher
		pathResource []cmdb.Resource
		expected     [][]string
		allMatch     bool
		error        error
	}{
		"apm_service to system": {
			target: "system",
			matcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			source: "apm_service",
			indexMatcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			allMatch: false,
			expected: [][]string{
				{"apm_service", "apm_service_instance", "system"},
				{"apm_service", "apm_service_instance", "pod", "node", "system"},
				{"apm_service", "apm_service_instance", "pod", "datasource", "node", "system"},
			},
		},
		"apm_service to system through wrong service": {
			target: "system",
			matcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			pathResource: []cmdb.Resource{"service"},
			source:       "apm_service",
			indexMatcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			allMatch: false,
			error:    errors.New("empty paths with apm_service => system through [service]"),
		},
		"apm_service to pod": {
			target: "pod",
			matcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			source: "apm_service",
			indexMatcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			allMatch: false,
			expected: [][]string{
				{"apm_service", "apm_service_instance", "pod"},
				{"apm_service", "apm_service_instance", "system", "node", "pod"},
				{"apm_service", "apm_service_instance", "system", "node", "datasource", "pod"},
			},
		},
		"apm_service to system through node and pod": {
			target: "system",
			matcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			source: "apm_service",
			indexMatcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			pathResource: []cmdb.Resource{
				"node", "pod",
			},
			allMatch: false,
			error:    errors.New("empty paths with apm_service => system through [node pod]"),
		},
		"apm_service_instance to system through empty": {
			target: "system",
			matcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			source: "apm_service_instance",
			indexMatcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			pathResource: []cmdb.Resource{
				"",
			},
			allMatch: false,
			expected: [][]string{
				{"apm_service_instance", "system"},
			},
		},
		"apm_service to system through empty": {
			target: "system",
			matcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			source: "apm_service",
			indexMatcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			pathResource: []cmdb.Resource{
				"apm_service_instance", "system",
			},
			allMatch: false,
			expected: [][]string{
				{"apm_service", "apm_service_instance", "system"},
			},
		},
		"apm_service to system through pod and node": {
			target: "system",
			matcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			source: "apm_service",
			indexMatcher: cmdb.Matcher{
				"apm_application_name": "name",
			},
			pathResource: []cmdb.Resource{
				"pod", "node",
			},
			allMatch: false,
			expected: [][]string{
				{"apm_service", "apm_service_instance", "pod", "node", "system"},
			},
		},
		"container to system": {
			target: "system",
			matcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
				"namespace":      "ns-1",
				"pod":            "pod-1",
				"container":      "container-1",
				"test":           "1",
			},
			indexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
				"namespace":      "ns-1",
				"pod":            "pod-1",
			},
			source:   "pod",
			allMatch: true,
			expected: [][]string{
				{"pod", "node", "system"},
				{"pod", "datasource", "node", "system"},
				{"pod", "apm_service_instance", "system"},
			},
		},
		"no target resource": {
			target: "multi_cluster",
			matcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
				"namespace":      "ns-1",
				"pod":            "pod-1",
			},
			source: "pod",
			indexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
				"namespace":      "ns-1",
				"pod":            "pod-1",
			},
			allMatch: true,
			error:    fmt.Errorf("empty paths with pod => multi_cluster through []"),
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
			allMatch: true,
			expected: [][]string{
				{"node", "system"},
				{"node", "pod", "apm_service_instance", "system"},
				{"node", "datasource", "pod", "apm_service_instance", "system"},
			},
		},
		"node to system not all match": {
			target: "system",
			matcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
				"demo":           "1",
			},
			source: "node",
			indexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "cls",
			},
			allMatch: false,
			expected: [][]string{
				{"node", "system"},
				{"node", "pod", "apm_service_instance", "system"},
				{"node", "datasource", "pod", "apm_service_instance", "system"},
			},
		},
		"datasource to system all match": {
			target: "system",
			matcher: cmdb.Matcher{
				"bk_data_id": "1000001",
			},
			source: "datasource",
			indexMatcher: cmdb.Matcher{
				"bk_data_id": "1000001",
			},
			allMatch: true,
			expected: [][]string{
				{"datasource", "node", "system"},
				{"datasource", "pod", "node", "system"},
				{"datasource", "pod", "apm_service_instance", "system"},
				{"datasource", "node", "pod", "apm_service_instance", "system"},
			},
		},
		"pod to node": {
			target: "node",
			matcher: cmdb.Matcher{
				"pod": "pod-1",
			},
			source: "pod",
			indexMatcher: cmdb.Matcher{
				"pod": "pod-1",
			},
			expected: [][]string{
				{"pod", "node"},
				{"pod", "datasource", "node"},
				{"pod", "apm_service_instance", "system", "node"},
			},
		},
	}

	for n, c := range testCases {
		t.Run(n, func(t *testing.T) {
			var (
				source cmdb.Resource
				err    error
			)
			if c.source == "" {
				source, err = testModel.getResourceFromMatch(ctx, c.matcher)
				assert.Nil(t, err)
			} else {
				source = c.source
			}

			indexMatcher, allMatch, err := testModel.getIndexMatcher(ctx, source, c.matcher)
			assert.Nil(t, err)
			if err == nil {
				assert.Equal(t, c.allMatch, allMatch)
				assert.Equal(t, c.source, source)
				assert.Equal(t, c.indexMatcher, indexMatcher)

				path, err := testModel.getPaths(ctx, source, c.target, c.pathResource)
				if c.error != nil {
					assert.Equal(t, c.error.Error(), err.Error())
				} else {
					assert.Nil(t, err)
					if err == nil {
						sort.SliceStable(path, func(i, j int) bool {
							listLength := len(path[i]) < len(path[j])
							stringLength := len(strings.Join(path[i], "")) < len(strings.Join(path[j], ""))
							return listLength || stringLength
						})
						assert.Equal(t, c.expected, path)
					}
				}
			}
		})
	}
}

func TestModel_GetResourceMatcher(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)

	timestamp := int64(1693973987)
	mock.Vm.Set(map[string]any{
		"query:1693973987(count by (bk_target_ip) (a))": victoriaMetrics.Data{
			ResultType: victoriaMetrics.VectorType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"bk_target_ip": "127.0.0.1",
					},
					Value: []any{
						1693973987, "1",
					},
				},
			},
		},
		"query:1693973987count by (bcs_cluster_id, namespace, pod) (b and on (bcs_cluster_id, node) (count by (bcs_cluster_id, node) (a)))": victoriaMetrics.Data{
			ResultType: victoriaMetrics.VectorType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"bcs_cluster_id": "BCS-K8S-00000",
						"namespace":      "bkmonitor-operator",
						"pod":            "bkm-pod-1",
					},
					Value: []any{
						1693973987, "1",
					},
				},
				{
					Metric: map[string]string{
						"bcs_cluster_id": "BCS-K8S-00000",
						"namespace":      "bkmonitor-operator",
						"pod":            "bkm-pod-2",
					},
					Value: []any{
						1693973987, "1",
					},
				},
			},
		},
		"query:1693973987count by (bk_target_ip) (b and on (apm_application_name, apm_service_name, apm_service_instance_name) (count by (apm_application_name, apm_service_name, apm_service_instance_name) (a)))": victoriaMetrics.Data{
			ResultType: victoriaMetrics.VectorType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"bk_target_ip": "127.0.0.1",
					},
					Value: []any{
						1693973987, "1",
					},
				},
			},
		},
		"query:1693973987count by (bk_target_ip) (b and on (bcs_cluster_id, node) (count by (bcs_cluster_id, node) (a)))": victoriaMetrics.Data{
			ResultType: victoriaMetrics.VectorType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"bk_target_ip": "127.0.0.1",
					},
					Value: []any{
						1693973987, 1,
					},
				},
			},
		},
	})

	testCases := map[string]struct {
		source       cmdb.Resource
		target       cmdb.Resource
		matcher      cmdb.Matcher
		pathResource []cmdb.Resource

		expected struct {
			source     cmdb.Resource
			sourceInfo cmdb.Matcher
			targetList cmdb.Matchers
		}
		error error
	}{
		"vm node to system": {
			target: "system",
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
			target: "system",
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
			target: "pod",
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
			target: "system",
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

	for n, c := range testCases {
		t.Run(n, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			metadata.SetUser(ctx, "", influxdb.SpaceUid, "skip")
			source, matcher, _, rets, err := testModel.QueryResourceMatcher(ctx, "", influxdb.SpaceUid, timestamp, c.target, c.source, c.matcher, c.pathResource)
			assert.Nil(t, err)
			if err != nil {
				log.Errorf(ctx, err.Error())
			} else {
				assert.Equal(t, c.expected.source, source)
				assert.Equal(t, c.expected.sourceInfo, matcher)
				assert.Equal(t, c.expected.targetList, rets)
			}
		})
	}
}

func TestMakeQuery(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	type Case struct {
		Name    string
		Path    []string
		Matcher map[string]string
		promQL  string
		step    time.Duration
	}

	cases := []Case{
		{
			Name: "level1 and 1m",
			Path: []string{"pod", "node"},
			Matcher: map[string]string{
				"pod":            "pod1",
				"namespace":      "ns1",
				"bcs_cluster_id": "cluster1",
			},
			step:   time.Minute,
			promQL: `(count by (bcs_cluster_id, node) (count_over_time(bkmonitor:node_with_pod_relation{bcs_cluster_id="cluster1",namespace="ns1",node!="",pod="pod1"}[1m])))`,
		},
		{
			Name: "level1",
			Path: []string{"pod", "node"},
			Matcher: map[string]string{
				"pod":            "pod1",
				"namespace":      "ns1",
				"bcs_cluster_id": "cluster1",
			},
			promQL: `(count by (bcs_cluster_id, node) (bkmonitor:node_with_pod_relation{bcs_cluster_id="cluster1",namespace="ns1",node!="",pod="pod1"}))`,
		},
		{
			Name: "level2",
			Path: []string{"pod", "node", "system"},
			Matcher: map[string]string{
				"pod":            "pod1",
				"namespace":      "ns1",
				"bcs_cluster_id": "cluster1",
			},
			promQL: `count by (bk_target_ip) (bkmonitor:node_with_system_relation{bcs_cluster_id="cluster1",bk_target_ip!="",node!=""} and on (bcs_cluster_id, node) (count by (bcs_cluster_id, node) (bkmonitor:node_with_pod_relation{bcs_cluster_id="cluster1",namespace="ns1",node!="",pod="pod1"})))`,
		},
		{
			Name: "level3",
			Path: []string{"node", "pod", "replicaset", "deployment"},
			Matcher: map[string]string{
				"node":           "node1",
				"bcs_cluster_id": "cluster1",
			},
			promQL: `count by (bcs_cluster_id, namespace, deployment) (bkmonitor:deployment_with_replicaset_relation{bcs_cluster_id="cluster1",deployment!="",namespace!="",replicaset!=""} and on (bcs_cluster_id, namespace, replicaset) count by (bcs_cluster_id, namespace, replicaset) (bkmonitor:pod_with_replicaset_relation{bcs_cluster_id="cluster1",namespace!="",pod!="",replicaset!=""} and on (bcs_cluster_id, namespace, pod) (count by (bcs_cluster_id, namespace, pod) (bkmonitor:node_with_pod_relation{bcs_cluster_id="cluster1",namespace!="",node="node1",pod!=""}))))`,
		},
		{
			Name: "level4",
			Path: []string{"system", "node", "pod", "replicaset", "deployment"},
			Matcher: map[string]string{
				"bk_target_ip": "127.0.0.1",
			},
			promQL: `count by (bcs_cluster_id, namespace, deployment) (bkmonitor:deployment_with_replicaset_relation{bcs_cluster_id!="",deployment!="",namespace!="",replicaset!=""} and on (bcs_cluster_id, namespace, replicaset) count by (bcs_cluster_id, namespace, replicaset) (bkmonitor:pod_with_replicaset_relation{bcs_cluster_id!="",namespace!="",pod!="",replicaset!=""} and on (bcs_cluster_id, namespace, pod) count by (bcs_cluster_id, namespace, pod) (bkmonitor:node_with_pod_relation{bcs_cluster_id!="",namespace!="",node!="",pod!=""} and on (bcs_cluster_id, node) (count by (bcs_cluster_id, node) (bkmonitor:node_with_system_relation{bcs_cluster_id!="",bk_target_ip="127.0.0.1",node!=""})))))`,
		},
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			queryTs, err := testModel.makeQuery(ctx, "", c.Path, c.Matcher, c.step)
			assert.NoError(t, err)
			assert.NotNil(t, queryTs)

			if queryTs != nil {
				promQLString, promQLErr := queryTs.ToPromQL(ctx)
				assert.Nil(t, promQLErr)
				if promQLErr == nil {
					assert.Equal(t, c.promQL, promQLString)
				}
			}
		})
	}
}
