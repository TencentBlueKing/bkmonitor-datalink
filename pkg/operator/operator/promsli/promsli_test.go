// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promsli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParsePromQLMetrics(t *testing.T) {
	type Case struct {
		q       string
		metrics []string
	}

	cases := []Case{
		{
			q: `
(
	count
	(
		deployment_with_replicaset_relation{} 
		and on(bcs_cluster_id, namespace, replicaset) 
		(
			count(
				pod_with_replicaset_relation{} 
				and on(bcs_cluster_id, namespace, pod) 
				(
					count(
						node_with_pod_relation{}
						and on (bcs_cluster_id, node)
						(
							count(
								node_with_system_relation{bk_target_ip="127.0.0.1"} + node_with_system_relation2{bk_target_ip="127.0.0.2"}
							) by (bcs_cluster_id, node)
						)
					) by (bcs_cluster_id, namespace, pod)
				)
			) by (bcs_cluster_id, namespace, replicaset)
		)
	) by (bcs_cluster_id, namespace, deployment)
)
`,
			metrics: []string{
				"deployment_with_replicaset_relation",
				"node_with_pod_relation",
				"node_with_system_relation",
				"node_with_system_relation2",
				"pod_with_replicaset_relation",
			},
		},
		{
			q:       "transfer_uptime > 0",
			metrics: []string{"transfer_uptime"},
		},
	}
	for _, c := range cases {
		assert.Equal(t, c.metrics, parsePromQLMetrics(c.q))
	}
}

func TestToPromFormat(t *testing.T) {
	type Case struct {
		Input  map[string]string
		Output string
	}

	cases := []Case{
		{
			Input:  map[string]string{"foo": "bar", "key1": "value1"},
			Output: `alert_rules{foo="bar",key1="value1"} 1`,
		},
		{
			Input:  map[string]string{"foo": "bar"},
			Output: `alert_rules{foo="bar"} 1`,
		},
		{
			Input:  map[string]string{},
			Output: "alert_rules{} 1",
		},
	}

	for _, c := range cases {
		assert.Equal(t, c.Output, toToPromFormat(c.Input))
	}
}
