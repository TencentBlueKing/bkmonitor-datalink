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

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/victoriaMetrics"
)

var testModel, _ = newModel(context.Background())

func TestModel_Resources(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	resources, err := testModel.resources(ctx)

	assert.Nil(t, err)
	assert.Equal(t, []cmdb.Resource{"apm_service", "apm_service_instance", "app_version", "bklogconfig", "business", "container", "datasource", "deamonset", "deployment", "domain", "git_commit", "host", "ingress", "job", "k8s_address", "module", "node", "pod", "replicaset", "service", "set", "statefulset", "system"}, resources)
}

func TestModel_GetResources(t *testing.T) {
	mock.Init()
	index := ResourcesIndex("k8s_address")
	assert.Equal(t, cmdb.Index{"bcs_cluster_id", "address"}, index)

	// 未配置该资源
	index = ResourcesIndex("clb")
	assert.Nil(t, index)
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
				{"apm_service", "apm_service_instance", "pod", "container", "app_version", "host", "system"},
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
				{"apm_service", "apm_service_instance", "system", "host", "app_version", "container", "pod"},
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
				{"pod", "container", "app_version", "host", "system"},
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
				{"node", "pod", "container", "app_version", "host", "system"},
				{"node", "datasource", "pod", "apm_service_instance", "system"},
				{"node", "datasource", "pod", "container", "app_version", "host", "system"},
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
				{"node", "pod", "container", "app_version", "host", "system"},
				{"node", "datasource", "pod", "apm_service_instance", "system"},
				{"node", "datasource", "pod", "container", "app_version", "host", "system"},
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
				{"datasource", "pod", "container", "app_version", "host", "system"},
				{"datasource", "node", "pod", "container", "app_version", "host", "system"},
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
				{"pod", "container", "app_version", "host", "system", "node"},
			},
		},
		"container info": {
			target: "container",
			matcher: cmdb.Matcher{
				"pod": "pod-1",
			},
			source: "container",
			indexMatcher: cmdb.Matcher{
				"pod": "pod-1",
			},
			expected: [][]string{
				{"container"},
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
			assert.Equal(t, c.allMatch, allMatch)
			assert.Equal(t, c.source, source)
			assert.Equal(t, c.indexMatcher, indexMatcher)

			path, err := testModel.getPaths(ctx, source, c.target, c.pathResource)
			if c.error != nil {
				assert.Equal(t, c.error.Error(), err.Error())
			} else {
				assert.Nil(t, err)
				sort.SliceStable(path, func(i, j int) bool {
					listLength := len(path[i]) < len(path[j])
					stringLength := len(strings.Join(path[i], "")) < len(strings.Join(path[j], ""))
					return listLength || stringLength
				})
				assert.Equal(t, c.expected, path)
			}
		})
	}
}

func TestModel_GetResourceMatcher(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)

	timestamp := "1693973987"
	mock.Vm.Set(map[string]any{
		// system to pod
		"query:1693973987count by (bcs_cluster_id, namespace, pod) (b * on (bcs_cluster_id, node) group_left () (count by (bcs_cluster_id, node) (a)))": victoriaMetrics.Data{
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
		// pod_name to system through apm service instance
		"query:1693973987count by (bk_target_ip) (b * on (apm_application_name, apm_service_name, apm_service_instance_name) group_left () (count by (apm_application_name, apm_service_name, apm_service_instance_name) (a)))": victoriaMetrics.Data{
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
		// vm node to system
		// node to system
		"query:1693973987count by (bk_target_ip) (a)": victoriaMetrics.Data{
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
		// container info
		"query:1693973987count by (bcs_cluster_id, namespace, pod, container, version) (a)": victoriaMetrics.Data{
			ResultType: victoriaMetrics.VectorType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"bcs_cluster_id": "BCS-K8S-00000",
						"namespace":      "bkmonitor-operator",
						"pod":            "bkm-pod-2",
						"version":        "1.2.3",
					},
					Value: []any{
						1693973987, "1",
					},
				},
			},
		},
	})

	testCases := map[string]struct {
		source       cmdb.Resource
		target       cmdb.Resource
		indexMatcher cmdb.Matcher

		expandMatcher  cmdb.Matcher
		targetInfoShow bool

		pathResource []cmdb.Resource

		expectedTargetList cmdb.Matchers
		expectedSource     cmdb.Resource
		expectedSourceInfo cmdb.Matcher
		expectedTarget     cmdb.Resource
		expectedPath       []string
		error              error
	}{
		"vm node to system": {
			target: "system",
			indexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00000",
				"node":           "node-127-0-0-1",
				"demo":           "1",
			},
			expectedPath: []string{"node", "system"},
			expectedTargetList: cmdb.Matchers{
				{
					"bk_target_ip": "127.0.0.1",
				},
			},
			expectedSource: "node",
			expectedSourceInfo: map[string]string{
				"bcs_cluster_id": "BCS-K8S-00000",
				"node":           "node-127-0-0-1",
			},
			expectedTarget: "system",
		},
		"node to system": {
			target: "system",
			indexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00000",
				"node":           "node-127-0-0-1",
				"demo":           "1",
			},
			expectedPath: []string{"node", "system"},
			expectedTargetList: cmdb.Matchers{
				{
					"bk_target_ip": "127.0.0.1",
				},
			},
			expectedSource: "node",
			expectedSourceInfo: map[string]string{
				"bcs_cluster_id": "BCS-K8S-00000",
				"node":           "node-127-0-0-1",
			},
			expectedTarget: "system",
		},
		"system to pod": {
			target: "pod",
			indexMatcher: cmdb.Matcher{
				"bk_target_ip":   "127.0.0.1",
				"bcs_cluster_id": "BCS-K8S-00000",
			},
			expectedPath: []string{"system", "node", "pod"},
			expectedTargetList: cmdb.Matchers{
				{
					"bcs_cluster_id": "BCS-K8S-00000",
					"namespace":      "bkmonitor-operator",
					"pod":            "bkm-pod-1",
				},
				{
					"bcs_cluster_id": "BCS-K8S-00000",
					"namespace":      "bkmonitor-operator",
					"pod":            "bkm-pod-2",
				},
			},
			expectedSource: "system",
			expectedSourceInfo: map[string]string{
				"bk_target_ip": "127.0.0.1",
			},
			expectedTarget: "pod",
		},
		"pod_name to system through apm service instance": {
			target: "system",
			indexMatcher: cmdb.Matcher{
				"bcs_cluster_id": "BCS-K8S-00000",
				"namespace":      "bkmonitor-operator",
				"pod_name":       "bkm-pod-1",
			},
			expectedPath: []string{"pod", "apm_service_instance", "system"},
			expectedTargetList: cmdb.Matchers{
				{
					"bk_target_ip": "127.0.0.1",
				},
			},
			expectedSource: "pod",
			expectedSourceInfo: map[string]string{
				"bcs_cluster_id": "BCS-K8S-00000",
				"namespace":      "bkmonitor-operator",
				"pod":            "bkm-pod-1",
			},
			expectedTarget: "system",
		},
		"container info": {
			source: "container",
			indexMatcher: cmdb.Matcher{
				"container": "container",
			},
			targetInfoShow: true,
			expectedPath:   []string{"container"},
			expectedTargetList: cmdb.Matchers{
				{
					"bcs_cluster_id": "BCS-K8S-00000",
					"namespace":      "bkmonitor-operator",
					"pod":            "bkm-pod-2",
					"version":        "1.2.3",
				},
			},
			expectedSource: "container",
			expectedSourceInfo: map[string]string{
				"container": "container",
			},
			expectedTarget: "container",
		},
	}

	for n, c := range testCases {
		t.Run(n, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			metadata.SetUser(ctx, &metadata.User{SpaceUID: influxdb.SpaceUid, SkipSpace: "skip"})
			source, sourceInfo, path, target, rets, err := testModel.QueryResourceMatcher(ctx, "", influxdb.SpaceUid, timestamp, c.target, c.source, c.indexMatcher, c.expandMatcher, c.targetInfoShow, c.pathResource)
			assert.Nil(t, err)
			if err == nil {
				assert.Equal(t, c.expectedPath, path)
				assert.Equal(t, c.expectedTargetList, rets)
				assert.Equal(t, c.expectedSource, source)
				assert.Equal(t, c.expectedSourceInfo, sourceInfo)
				assert.Equal(t, c.expectedTarget, target)
			}
		})
	}
}

func TestModel_GetResourceMatcherRange(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())
	influxdb.MockSpaceRouter(ctx)

	start := "1693973987"
	end := "1693974407"
	step := "1m"

	mock.Vm.Set(map[string]any{
		// host info
		"query_range:1693973987169397440760count by (bk_host_id, version, env_name, env_type, service_version, service_type) (count_over_time(a[1m]))": victoriaMetrics.Data{
			ResultType: victoriaMetrics.MatrixType,
			Result: []victoriaMetrics.Series{
				{
					Metric: map[string]string{
						"host_id":         "12345",
						"version":         "1.2.2",
						"env_name":        "my",
						"env_type":        "test",
						"service_version": "1.2.2",
						"service_type":    "test",
					},
					Values: []victoriaMetrics.Value{
						{1693973987, "1"},
						{1693974047, "1"},
						{1693974107, "1"},
						{1693974167, "1"},
					},
				},
				{
					Metric: map[string]string{
						"host_id":         "12345",
						"version":         "1.2.3",
						"env_name":        "my",
						"env_type":        "test",
						"service_version": "1.2.3",
						"service_type":    "test",
					},
					Values: []victoriaMetrics.Value{
						{1693974107, "1"},
						{1693974167, "1"},
						{1693974327, "1"},
						{1693974407, "1"},
					},
				},
			},
		},
	})

	testCases := map[string]struct {
		source       cmdb.Resource
		target       cmdb.Resource
		indexMatcher cmdb.Matcher

		expandMatcher  cmdb.Matcher
		targetInfoShow bool

		pathResource []cmdb.Resource

		expectedTargetList []cmdb.MatchersWithTimestamp
		expectedSource     cmdb.Resource
		expectedSourceInfo cmdb.Matcher
		expectedTarget     cmdb.Resource
		expectedPath       []string
		error              error
	}{
		"host info": {
			source: "host",
			indexMatcher: cmdb.Matcher{
				"bk_host_id": "12345",
			},
			targetInfoShow: true,
			expectedPath:   []string{"host"},
			expectedSource: "host",
			expectedSourceInfo: map[string]string{
				"bk_host_id": "12345",
			},
			expectedTarget: "host",
			expectedTargetList: []cmdb.MatchersWithTimestamp{
				{
					Timestamp: 1693973987000,
					Matchers: cmdb.Matchers{
						{
							"host_id":         "12345",
							"version":         "1.2.2",
							"env_name":        "my",
							"env_type":        "test",
							"service_version": "1.2.2",
							"service_type":    "test",
						},
					},
				},
				{
					Timestamp: 1693974047000,
					Matchers: cmdb.Matchers{
						{
							"host_id":         "12345",
							"version":         "1.2.2",
							"env_name":        "my",
							"env_type":        "test",
							"service_version": "1.2.2",
							"service_type":    "test",
						},
					},
				},
				{
					Timestamp: 1693974107000,
					Matchers: cmdb.Matchers{
						{
							"host_id":         "12345",
							"version":         "1.2.2",
							"env_name":        "my",
							"env_type":        "test",
							"service_version": "1.2.2",
							"service_type":    "test",
						},
						{
							"host_id":         "12345",
							"version":         "1.2.3",
							"env_name":        "my",
							"env_type":        "test",
							"service_version": "1.2.3",
							"service_type":    "test",
						},
					},
				},
				{
					Timestamp: 1693974167000,
					Matchers: cmdb.Matchers{
						{
							"host_id":         "12345",
							"version":         "1.2.2",
							"env_name":        "my",
							"env_type":        "test",
							"service_version": "1.2.2",
							"service_type":    "test",
						},
						{
							"host_id":         "12345",
							"version":         "1.2.3",
							"env_name":        "my",
							"env_type":        "test",
							"service_version": "1.2.3",
							"service_type":    "test",
						},
					},
				},
				{
					Timestamp: 1693974327000,
					Matchers: cmdb.Matchers{
						{
							"host_id":         "12345",
							"version":         "1.2.3",
							"env_name":        "my",
							"env_type":        "test",
							"service_version": "1.2.3",
							"service_type":    "test",
						},
					},
				},
				{
					Timestamp: 1693974407000,
					Matchers: cmdb.Matchers{
						{
							"host_id":         "12345",
							"version":         "1.2.3",
							"env_name":        "my",
							"env_type":        "test",
							"service_version": "1.2.3",
							"service_type":    "test",
						},
					},
				},
			},
		},
	}

	for n, c := range testCases {
		t.Run(n, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			metadata.SetUser(ctx, &metadata.User{SpaceUID: influxdb.SpaceUid, SkipSpace: "skip"})
			source, sourceInfo, path, target, rets, err := testModel.QueryResourceMatcherRange(ctx, "", influxdb.SpaceUid, step, start, end, c.target, c.source, c.indexMatcher, c.expandMatcher, c.targetInfoShow, c.pathResource)
			assert.Nil(t, err)
			if err == nil {
				assert.Equal(t, c.expectedPath, path)
				assert.Equal(t, c.expectedTargetList, rets)
				assert.Equal(t, c.expectedSource, source)
				assert.Equal(t, c.expectedSourceInfo, sourceInfo)
				assert.Equal(t, c.expectedTarget, target)
			}
		})
	}
}
