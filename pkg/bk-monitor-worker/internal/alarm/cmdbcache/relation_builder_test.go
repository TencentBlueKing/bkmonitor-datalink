// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestBuildMetrics(t *testing.T) {
	mocker.InitTestDBConfig("../../../dist/bmw.yaml")

	for name, c := range map[string]struct {
		hosts    []*AlarmHostInfo
		expected string
	}{
		"test build metrics": {
			hosts: []*AlarmHostInfo{
				{
					BkHostId:      1001,
					BkBizId:       2,
					BkHostInnerip: "127.0.0.1",
					BkCloudId:     3,
					TopoLinks: map[string][]map[string]interface{}{
						"module_2001": {
							{
								"bk_obj_id":  "module",
								"bk_inst_id": "2001",
							},
							{
								"bk_obj_id":  "set",
								"bk_inst_id": "3001",
							},
						},
						"module_2002": {
							{
								"bk_obj_id":  "module",
								"bk_inst_id": "2002",
							},
							{
								"bk_obj_id":  "set",
								"bk_inst_id": "3002",
							},
						},
					},
				},
				{
					BkHostId:      1002,
					BkBizId:       2,
					BkHostInnerip: "127.0.0.2",
					BkCloudId:     3,
					TopoLinks: map[string][]map[string]interface{}{
						"module_2001": {
							{
								"bk_obj_id":  "module",
								"bk_inst_id": "2001",
							},
							{
								"bk_obj_id":  "set",
								"bk_inst_id": "3001",
							},
						},
					},
				},
				{
					BkHostId:      1003,
					BkBizId:       2,
					BkHostInnerip: "127.0.0.3",
					BkCloudId:     3,
					TopoLinks: map[string][]map[string]interface{}{
						"module_2003": {
							{
								"bk_obj_id":  "module",
								"bk_inst_id": "2003",
							},
							{
								"bk_obj_id":  "set",
								"bk_inst_id": "3001",
							},
						},
					},
				},
			},
			expected: `agent_with_module_relation{agent_id="1001",module_id="2001"} 1
agent_with_module_relation{agent_id="1001",module_id="2002"} 1
agent_with_module_relation{agent_id="1002",module_id="2001"} 1
agent_with_module_relation{agent_id="1003",module_id="2003"} 1
agent_with_system_relation{agent_id="1001",bk_cloud_id="3",bk_target_ip="127.0.0.1"} 1
agent_with_system_relation{agent_id="1002",bk_cloud_id="3",bk_target_ip="127.0.0.2"} 1
agent_with_system_relation{agent_id="1003",bk_cloud_id="3",bk_target_ip="127.0.0.3"} 1
business_with_set_relation{biz_id="2",set_id="3001"} 1
business_with_set_relation{biz_id="2",set_id="3002"} 1
module_with_set_relation{module_id="2001",set_id="3001"} 1
module_with_set_relation{module_id="2002",set_id="3002"} 1
module_with_set_relation{module_id="2003",set_id="3001"} 1`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_ = GetRelationMetricsBuilder().BuildMetrics(c.hosts)
			assert.Equal(t, c.expected, GetRelationMetricsBuilder().SortString())
		})

	}

}
