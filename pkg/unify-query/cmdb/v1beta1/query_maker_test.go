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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestMakeQuery(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	type Case struct {
		name string
		path []string

		expandShow   bool
		indexMatcher map[string]string
		expandMatch  map[string]string
		promQL       string
		step         string

		err error
	}

	cases := []Case{
		{
			name:       "level0 with info expand show",
			path:       []string{"container"},
			expandShow: true,

			indexMatcher: map[string]string{
				"container": "unify-query",
			},
			expandMatch: map[string]string{
				"version": "3.9.3269",
			},
			promQL: `count by (bcs_cluster_id, namespace, pod, container, version) (bkmonitor:container_info_relation{bcs_cluster_id!="",container="unify-query",namespace!="",pod!="",version="3.9.3269"})`,
		},
		{
			name: "level1 and 1m",
			path: []string{"pod", "node"},
			indexMatcher: map[string]string{
				"pod":            "pod1",
				"namespace":      "ns1",
				"bcs_cluster_id": "cluster1",
			},
			step:   "1m",
			promQL: `count by (bcs_cluster_id, node) (count_over_time(bkmonitor:node_with_pod_relation{bcs_cluster_id="cluster1",namespace="ns1",node!="",pod="pod1"}[1m]))`,
		},
		{
			name: "level1",
			path: []string{"pod", "node"},
			indexMatcher: map[string]string{
				"pod":            "pod1",
				"namespace":      "ns1",
				"bcs_cluster_id": "cluster1",
			},
			promQL: `count by (bcs_cluster_id, node) (bkmonitor:node_with_pod_relation{bcs_cluster_id="cluster1",namespace="ns1",node!="",pod="pod1"})`,
		},
		{
			name: "level 2",
			path: []string{"pod", "node", "system"},
			indexMatcher: map[string]string{
				"pod":            "pod1",
				"namespace":      "ns1",
				"bcs_cluster_id": "cluster1",
			},
			promQL: `count by (bk_target_ip) (bkmonitor:node_with_system_relation{bcs_cluster_id="cluster1",bk_target_ip!="",node!=""} * on (bcs_cluster_id, node) group_left () (count by (bcs_cluster_id, node) (bkmonitor:node_with_pod_relation{bcs_cluster_id="cluster1",namespace="ns1",node!="",pod="pod1"})))`,
		},
		{
			name: "level 2 and 1m with expand info",
			path: []string{"container", "pod", "node"},
			indexMatcher: map[string]string{
				"container": "unify-query",
			},
			expandMatch: map[string]string{
				"version": "3.9.3269",
			},
			step:   "1m",
			promQL: `count by (bcs_cluster_id, node) (count_over_time(bkmonitor:node_with_pod_relation{bcs_cluster_id!="",namespace!="",node!="",pod!=""}[1m]) * on (bcs_cluster_id, namespace, pod) group_left () (count by (bcs_cluster_id, namespace, pod) (count_over_time(bkmonitor:container_with_pod_relation{bcs_cluster_id!="",container="unify-query",namespace!="",pod!=""}[1m]) * on (bcs_cluster_id, namespace, pod, container) group_left () (count_over_time(bkmonitor:container_info_relation{bcs_cluster_id!="",container="unify-query",namespace!="",pod!="",version="3.9.3269"}[1m])))))`,
		},
		{
			name:       "level 2 with expand show",
			path:       []string{"pod", "node", "system"},
			expandShow: true,
			indexMatcher: map[string]string{
				"pod":            "pod1",
				"namespace":      "ns1",
				"bcs_cluster_id": "cluster1",
			},
			err: fmt.Errorf("该资源未配置 info 扩展数据"),
		},
		{
			name:       "level 3 with expand show",
			path:       []string{"pod", "node", "system", "host"},
			expandShow: true,
			indexMatcher: map[string]string{
				"pod":            "pod1",
				"namespace":      "ns1",
				"bcs_cluster_id": "cluster1",
			},
			promQL: `(count by (host_id) (bkmonitor:host_with_system_relation{bk_target_ip!="",host_id!=""} * on (bk_target_ip) group_left () (count by (bk_target_ip) (bkmonitor:node_with_system_relation{bcs_cluster_id="cluster1",bk_target_ip!="",node!=""} * on (bcs_cluster_id, node) group_left () (count by (bcs_cluster_id, node) (bkmonitor:node_with_pod_relation{bcs_cluster_id="cluster1",namespace="ns1",node!="",pod="pod1"})))))) * on (host_id) group_left (version, env_name, env_type, service_version, service_type) bkmonitor:host_info_relation{host_id!=""}`,
		},
		{
			name: "level 2 and 1m with expand info and expand show",
			path: []string{"node", "pod", "container"},
			indexMatcher: map[string]string{
				"node": "node_1",
			},
			expandShow: true,
			step:       "1m",
			promQL:     `(count by (bcs_cluster_id, namespace, pod, container) (count_over_time(bkmonitor:container_with_pod_relation{bcs_cluster_id!="",container!="",namespace!="",pod!=""}[1m]) * on (bcs_cluster_id, namespace, pod) group_left () (count by (bcs_cluster_id, namespace, pod) (count_over_time(bkmonitor:node_with_pod_relation{bcs_cluster_id!="",namespace!="",node="node_1",pod!=""}[1m]))))) * on (bcs_cluster_id, namespace, pod, container) group_left (version) count_over_time(bkmonitor:container_info_relation{bcs_cluster_id!="",container!="",namespace!="",pod!=""}[1m])`,
		},
		{
			name: "level 2 with expand info",
			path: []string{"container", "pod", "node"},
			indexMatcher: map[string]string{
				"container": "unify-query",
			},
			expandMatch: map[string]string{
				"version": "3.9.3269",
			},
			promQL: `count by (bcs_cluster_id, node) (bkmonitor:node_with_pod_relation{bcs_cluster_id!="",namespace!="",node!="",pod!=""} * on (bcs_cluster_id, namespace, pod) group_left () (count by (bcs_cluster_id, namespace, pod) (bkmonitor:container_with_pod_relation{bcs_cluster_id!="",container="unify-query",namespace!="",pod!=""} * on (bcs_cluster_id, namespace, pod, container) group_left () (bkmonitor:container_info_relation{bcs_cluster_id!="",container="unify-query",namespace!="",pod!="",version="3.9.3269"}))))`,
		},
		{
			name: "level3",
			path: []string{"node", "pod", "replicaset", "deployment"},
			indexMatcher: map[string]string{
				"node":           "node1",
				"bcs_cluster_id": "cluster1",
			},
			promQL: `count by (bcs_cluster_id, namespace, deployment) (bkmonitor:deployment_with_replicaset_relation{bcs_cluster_id="cluster1",deployment!="",namespace!="",replicaset!=""} * on (bcs_cluster_id, namespace, replicaset) group_left () (count by (bcs_cluster_id, namespace, replicaset) (bkmonitor:pod_with_replicaset_relation{bcs_cluster_id="cluster1",namespace!="",pod!="",replicaset!=""} * on (bcs_cluster_id, namespace, pod) group_left () (count by (bcs_cluster_id, namespace, pod) (bkmonitor:node_with_pod_relation{bcs_cluster_id="cluster1",namespace!="",node="node1",pod!=""})))))`,
		},
		{
			name: "level 3 with expand info",
			path: []string{"container", "pod", "node", "system"},
			indexMatcher: map[string]string{
				"container": "unify-query",
			},
			expandMatch: map[string]string{
				"version": "3.9.3269",
			},
			promQL: `count by (bk_target_ip) (bkmonitor:node_with_system_relation{bcs_cluster_id!="",bk_target_ip!="",node!=""} * on (bcs_cluster_id, node) group_left () (count by (bcs_cluster_id, node) (bkmonitor:node_with_pod_relation{bcs_cluster_id!="",namespace!="",node!="",pod!=""} * on (bcs_cluster_id, namespace, pod) group_left () (count by (bcs_cluster_id, namespace, pod) (bkmonitor:container_with_pod_relation{bcs_cluster_id!="",container="unify-query",namespace!="",pod!=""} * on (bcs_cluster_id, namespace, pod, container) group_left () (bkmonitor:container_info_relation{bcs_cluster_id!="",container="unify-query",namespace!="",pod!="",version="3.9.3269"}))))))`,
		},
		{
			name: "level4",
			path: []string{"system", "node", "pod", "replicaset", "deployment"},
			indexMatcher: map[string]string{
				"bk_target_ip": "127.0.0.1",
			},
			promQL: `count by (bcs_cluster_id, namespace, deployment) (bkmonitor:deployment_with_replicaset_relation{bcs_cluster_id!="",deployment!="",namespace!="",replicaset!=""} * on (bcs_cluster_id, namespace, replicaset) group_left () (count by (bcs_cluster_id, namespace, replicaset) (bkmonitor:pod_with_replicaset_relation{bcs_cluster_id!="",namespace!="",pod!="",replicaset!=""} * on (bcs_cluster_id, namespace, pod) group_left () (count by (bcs_cluster_id, namespace, pod) (bkmonitor:node_with_pod_relation{bcs_cluster_id!="",namespace!="",node!="",pod!=""} * on (bcs_cluster_id, node) group_left () (count by (bcs_cluster_id, node) (bkmonitor:node_with_system_relation{bcs_cluster_id!="",bk_target_ip="127.0.0.1",node!=""})))))))`,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			queryMaker := &QueryFactory{
				Path:          c.path,
				Source:        cmdb.Resource(c.path[0]),
				Target:        cmdb.Resource(c.path[len(c.path)-1]),
				Step:          c.step,
				IndexMatcher:  c.indexMatcher,
				ExpandMatcher: c.expandMatch,
				ExpandShow:    c.expandShow,
			}

			queryTs, err := queryMaker.MakeQueryTs()
			if c.err != nil {
				assert.Equal(t, err, c.err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, queryTs)

				if queryTs != nil {
					promQLString, promQLErr := queryTs.ToPromQL(ctx)
					assert.Nil(t, promQLErr)
					if promQLErr == nil {
						assert.Equal(t, c.promQL, promQLString)
					}
				}
			}
		})
	}
}
