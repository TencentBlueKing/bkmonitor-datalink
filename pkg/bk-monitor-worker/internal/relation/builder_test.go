// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

type bizInfo struct {
	bkBizID  int
	resource string
	infos    []*Info
}

func TestBuildMetricsWithMultiBkBizID(t *testing.T) {
	mocker.InitTestDBConfig("../../dist/bmw.yaml")

	for name, c := range map[string]struct {
		bkBizIDHosts []bizInfo
		expected     string
	}{
		"测试相同业务 id, 扩展信息从自身获取 指标生成规则": {
			bkBizIDHosts: []bizInfo{
				{
					bkBizID:  2,
					resource: Set,
					infos: []*Info{
						{
							ID:       "3001",
							Resource: Set,
							Label: map[string]string{
								"set_id": "3001",
							},
							Expands: map[string]map[string]string{
								Host: {
									"version": "v0.0.1",
								},
								Set: {
									"version": "v0.0.2",
								},
							},
						},
					},
				},
				{
					bkBizID:  2,
					resource: Module,
					infos: []*Info{
						{
							ID:       "2001",
							Resource: Module,
							Label: map[string]string{
								"module_id": "2001",
							},
						},
					},
				},
				{
					bkBizID:  2,
					resource: Host,
					infos: []*Info{
						{
							ID:       "1001",
							Resource: Host,
							Label: map[string]string{
								"host_id": "1001",
							},
							Expands: map[string]map[string]string{
								Host: {
									"version": "v0.0.3",
								},
								Set: {
									"version": "v0.0.4",
								},
							},
							Links: []Link{
								{
									{
										Resource: Module,
										ID:       "2001",
										Label: map[string]string{
											"module_id": "2001",
										},
									},
								},
								{
									{
										Resource: Set,
										ID:       "3001",
										Label: map[string]string{
											"set_id": "3001",
										},
									},
								},
							},
						},
						{
							ID:       "127.0.0.1|3",
							Resource: System,
							Label: map[string]string{
								"bk_target_ip": "127.0.0.1",
								"bk_cloud_id":  "3",
							},
							Links: []Link{
								{
									{
										Resource: Host,
										ID:       "1001",
										Label: map[string]string{
											"host_id": "1001",
										},
									},
								},
							},
						},
					},
				},
				{
					bkBizID:  2,
					resource: Host,
					infos: []*Info{
						{
							ID:       "1002",
							Resource: Host,
							Label: map[string]string{
								"host_id": "1002",
							},
							Expands: map[string]map[string]string{
								Host: {
									"version": "v0.0.3",
								},
								Set: {
									"version": "v0.0.4",
								},
							},
							Links: []Link{
								{
									{
										Resource: Module,
										ID:       "2001",
										Label: map[string]string{
											"module_id": "2001",
										},
									},
								},
								{
									{
										Resource: "bad",
										ID:       "4001",
										Label: map[string]string{
											"bad_id": "4001",
										},
									},
								},
								{
									{
										Resource: Set,
										ID:       "3001",
										Label: map[string]string{
											"set_id": "3001",
										},
									},
								},
							},
						},
						{
							ID:       "127.0.0.2|3",
							Resource: System,
							Label: map[string]string{
								"bk_target_ip": "127.0.0.2",
								"bk_cloud_id":  "3",
							},
							Links: []Link{
								{
									{
										Resource: Host,
										ID:       "1002",
										Label: map[string]string{
											"host_id": "1002",
										},
									},
								},
							},
						},
					},
				},
			},
			expected: `bad_with_module_relation{bad_id="4001",bk_biz_id="2",module_id="2001"} 1
bad_with_set_relation{bad_id="4001",bk_biz_id="2",set_id="3001"} 1
host_info_relation{bk_biz_id="2",host_id="1001",version="v0.0.3"} 1
host_info_relation{bk_biz_id="2",host_id="1002",version="v0.0.3"} 1
host_with_module_relation{bk_biz_id="2",host_id="1001",module_id="2001"} 1
host_with_module_relation{bk_biz_id="2",host_id="1002",module_id="2001"} 1
host_with_system_relation{bk_biz_id="2",bk_cloud_id="3",bk_target_ip="127.0.0.1",host_id="1001"} 1
host_with_system_relation{bk_biz_id="2",bk_cloud_id="3",bk_target_ip="127.0.0.2",host_id="1002"} 1
module_with_set_relation{bk_biz_id="2",module_id="2001",set_id="3001"} 1
set_info_relation{bk_biz_id="2",set_id="3001",version="v0.0.2"} 1`,
		},
		"测试相同业务 id，扩展信息从上游获取，指标生成规则": {
			bkBizIDHosts: []bizInfo{
				{
					bkBizID:  2,
					resource: Set,
					infos: []*Info{
						{
							ID:       "3001",
							Resource: Set,
							Label: map[string]string{
								"set_id": "3001",
							},
							Expands: map[string]map[string]string{
								Host: {
									"version": "v0.0.1",
								},
								Module: {
									"version": "v0.1.1",
								},
								Set: {
									"version": "v0.0.2",
								},
							},
						},
					},
				},
				{
					bkBizID:  2,
					resource: Module,
					infos: []*Info{
						{
							ID:       "2001",
							Resource: Module,
							Label: map[string]string{
								"module_id": "2001",
							},
							Links: []Link{
								{
									{
										Resource: Set,
										ID:       "3001",
										Label: map[string]string{
											"set_id": "3001",
										},
									},
								},
							},
						},
					},
				},
				{
					bkBizID:  2,
					resource: Host,
					infos: []*Info{
						{
							ID:       "1001",
							Resource: Host,
							Label: map[string]string{
								"host_id": "1001",
							},
							Links: []Link{
								{
									{
										Resource: Module,
										ID:       "2001",
										Label: map[string]string{
											"module_id": "2001",
										},
									},
								},
								{
									{
										Resource: Set,
										ID:       "3001",
										Label: map[string]string{
											"set_id": "3001",
										},
									},
								},
							},
						},
						{
							ID:       "127.0.0.1|3",
							Resource: System,
							Label: map[string]string{
								"bk_target_ip": "127.0.0.1",
								"bk_cloud_id":  "3",
							},
							Links: []Link{
								{
									{
										Resource: Host,
										ID:       "1001",
										Label: map[string]string{
											"host_id": "1001",
										},
									},
								},
							},
						},
					},
				},
				{
					bkBizID:  2,
					resource: Host,
					infos: []*Info{
						{
							ID:       "1002",
							Resource: Host,
							Label: map[string]string{
								"host_id": "1002",
							},
							Links: []Link{
								{
									{
										Resource: Module,
										ID:       "2001",
										Label: map[string]string{
											"module_id": "2001",
										},
									},
								},
								{
									{
										Resource: Set,
										ID:       "3001",
										Label: map[string]string{
											"set_id": "3001",
										},
									},
								},
							},
						},
						{
							ID:       "127.0.0.2|3",
							Resource: System,
							Label: map[string]string{
								"bk_target_ip": "127.0.0.2",
								"bk_cloud_id":  "3",
							},
							Links: []Link{
								{
									{
										Resource: Host,
										ID:       "1002",
										Label: map[string]string{
											"host_id": "1002",
										},
									},
								},
							},
						},
					},
				},
			},
			expected: `host_info_relation{bk_biz_id="2",host_id="1001",version="v0.0.1"} 1
host_info_relation{bk_biz_id="2",host_id="1002",version="v0.0.1"} 1
host_with_module_relation{bk_biz_id="2",host_id="1001",module_id="2001"} 1
host_with_module_relation{bk_biz_id="2",host_id="1002",module_id="2001"} 1
host_with_system_relation{bk_biz_id="2",bk_cloud_id="3",bk_target_ip="127.0.0.1",host_id="1001"} 1
host_with_system_relation{bk_biz_id="2",bk_cloud_id="3",bk_target_ip="127.0.0.2",host_id="1002"} 1
module_info_relation{bk_biz_id="2",module_id="2001",version="v0.1.1"} 1
module_with_set_relation{bk_biz_id="2",module_id="2001",set_id="3001"} 1
set_info_relation{bk_biz_id="2",set_id="3001",version="v0.0.2"} 1`,
		},
		"测试不同业务 id 下的指标生成规则": {
			bkBizIDHosts: []bizInfo{
				{
					bkBizID:  3,
					resource: Set,
					infos: []*Info{
						{
							ID:       "3001",
							Resource: Set,
							Label: map[string]string{
								"set_id": "3001",
							},
							Expands: map[string]map[string]string{
								Host: {
									"version": "v0.0.1",
								},
								Set: {
									"version": "v0.0.2",
								},
							},
						},
					},
				},
				{
					bkBizID:  2,
					resource: Host,
					infos: []*Info{
						{
							ID:       "1001",
							Resource: Host,
							Label: map[string]string{
								"host_id": "1001",
							},
							Expands: map[string]map[string]string{
								Host: {
									"version": "v0.0.3",
								},
								Set: {
									"version": "v0.0.4",
								},
							},
							Links: []Link{
								{
									{
										Resource: Module,
										ID:       "2001",
										Label: map[string]string{
											"module_id": "3001",
										},
									},
								},
								{
									{
										Resource: Set,
										ID:       "3001",
										Label: map[string]string{
											"set_id": "3001",
										},
									},
								},
							},
						},
						{
							ID:       "127.0.0.1|3",
							Resource: System,
							Label: map[string]string{
								"bk_target_ip": "127.0.0.1",
								"bk_cloud_id":  "3",
							},
							Links: []Link{
								{
									{
										Resource: Host,
										ID:       "1001",
										Label: map[string]string{
											"host_id": "1001",
										},
									},
								},
							},
						},
					},
				},
				{
					bkBizID:  2,
					resource: Host,
					infos: []*Info{
						{
							ID:       "1002",
							Resource: Host,
							Label: map[string]string{
								"host_id": "1002",
							},
							Expands: map[string]map[string]string{
								Host: {
									"version": "v0.0.3",
								},
								Set: {
									"version": "v0.0.4",
								},
							},
							Links: []Link{
								{
									{
										Resource: Module,
										ID:       "2001",
										Label: map[string]string{
											"module_id": "2001",
										},
									},
								},
								{
									{
										Resource: Set,
										ID:       "3001",
										Label: map[string]string{
											"set_id": "3001",
										},
									},
								},
							},
						},
						{
							ID:       "127.0.0.2|3",
							Resource: System,
							Label: map[string]string{
								"bk_target_ip": "127.0.0.2",
								"bk_cloud_id":  "3",
							},
							Links: []Link{
								{
									{
										Resource: Host,
										ID:       "1002",
										Label: map[string]string{
											"host_id": "1002",
										},
									},
								},
							},
						},
					},
				},
			},
			expected: `host_info_relation{bk_biz_id="2",host_id="1001",version="v0.0.3"} 1
host_info_relation{bk_biz_id="2",host_id="1002",version="v0.0.3"} 1
host_with_module_relation{bk_biz_id="2",host_id="1001",module_id="3001"} 1
host_with_module_relation{bk_biz_id="2",host_id="1002",module_id="2001"} 1
host_with_system_relation{bk_biz_id="2",bk_cloud_id="3",bk_target_ip="127.0.0.1",host_id="1001"} 1
host_with_system_relation{bk_biz_id="2",bk_cloud_id="3",bk_target_ip="127.0.0.2",host_id="1002"} 1
module_with_set_relation{bk_biz_id="2",module_id="2001",set_id="3001"} 1
module_with_set_relation{bk_biz_id="2",module_id="3001",set_id="3001"} 1
set_info_relation{bk_biz_id="3",set_id="3001",version="v0.0.2"} 1`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			rmb := newRelationMetricsBuilder()
			for _, bh := range c.bkBizIDHosts {
				err := rmb.BuildInfosCache(context.Background(), bh.bkBizID, bh.resource, bh.infos)
				assert.Nil(t, err)
			}

			metrics := strings.Split(strings.Trim(rmb.String(), "\n"), "\n")
			sort.Strings(metrics)
			assert.Equal(t, c.expected, strings.Join(metrics, "\n"))
		})
	}
}
