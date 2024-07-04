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
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestBuildMetricsWithMultiBkBizID(t *testing.T) {
	mocker.InitTestDBConfig("../../../dist/bmw.yaml")

	for name, c := range map[string]struct {
		bkBizIDHosts []struct {
			bkBizID int
			hosts   []*AlarmHostInfo
		}
		expected string
	}{
		"test build metrics with same bkbizID": {
			bkBizIDHosts: []struct {
				bkBizID int
				hosts   []*AlarmHostInfo
			}{
				{
					bkBizID: 2,
					hosts: []*AlarmHostInfo{
						{
							BkHostId:      1001,
							BkBizId:       2,
							BkHostInnerip: "127.0.0.1",
							BkCloudId:     3,
						},
						{
							BkHostId:      1002,
							BkBizId:       2,
							BkHostInnerip: "127.0.0.2",
							BkCloudId:     3,
						},
						{
							BkHostId:      1003,
							BkBizId:       2,
							BkHostInnerip: "127.0.0.3",
							BkCloudId:     3,
						},
					},
				},
				{
					bkBizID: 2,
					hosts: []*AlarmHostInfo{
						{
							BkHostId:      1001,
							BkBizId:       2,
							BkHostInnerip: "127.0.0.4",
							BkCloudId:     3,
						},
					},
				},
				{
					bkBizID: 3,
					hosts: []*AlarmHostInfo{
						{
							BkHostId:      31001,
							BkBizId:       3,
							BkHostInnerip: "127.1.0.1",
							BkCloudId:     3,
						},
					},
				},
			},
			expected: `host_with_system_relation{bk_cloud_id="3",bk_target_ip="127.0.0.4",host_id="1001",bk_biz_id="2"} 1
host_with_system_relation{bk_cloud_id="3",bk_target_ip="127.1.0.1",host_id="31001",bk_biz_id="3"} 1`,
		},
		"test build metrics with diff bkbizID": {
			bkBizIDHosts: []struct {
				bkBizID int
				hosts   []*AlarmHostInfo
			}{
				{
					bkBizID: 2,
					hosts: []*AlarmHostInfo{
						{
							BkHostId:      1001,
							BkBizId:       2,
							BkHostInnerip: "127.0.0.1",
							BkCloudId:     3,
						},
						{
							BkHostId:      1002,
							BkBizId:       2,
							BkHostInnerip: "127.0.0.2",
							BkCloudId:     3,
						},
						{
							BkHostId:      1003,
							BkBizId:       2,
							BkHostInnerip: "127.0.0.3",
							BkCloudId:     3,
						},
					},
				},
				{
					bkBizID: 3,
					hosts: []*AlarmHostInfo{
						{
							BkHostId:      31001,
							BkBizId:       3,
							BkHostInnerip: "127.1.0.1",
							BkCloudId:     3,
						},
					},
				},
			},
			expected: `host_with_system_relation{bk_cloud_id="3",bk_target_ip="127.0.0.1",host_id="1001",bk_biz_id="2"} 1
host_with_system_relation{bk_cloud_id="3",bk_target_ip="127.0.0.2",host_id="1002",bk_biz_id="2"} 1
host_with_system_relation{bk_cloud_id="3",bk_target_ip="127.0.0.3",host_id="1003",bk_biz_id="2"} 1
host_with_system_relation{bk_cloud_id="3",bk_target_ip="127.1.0.1",host_id="31001",bk_biz_id="3"} 1`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, bh := range c.bkBizIDHosts {
				_ = GetRelationMetricsBuilder().BuildMetrics(context.Background(), bh.bkBizID, bh.hosts)
			}

			metrics := strings.Split(strings.Trim(GetRelationMetricsBuilder().String(), "\n"), "\n")
			sort.Strings(metrics)
			assert.Equal(t, c.expected, strings.Join(metrics, "\n"))
		})
	}
}

func TestBuildMetrics(t *testing.T) {
	mocker.InitTestDBConfig("../../../dist/bmw.yaml")

	for name, c := range map[string]struct {
		bkBizID    int
		hosts      []*AlarmHostInfo
		clearHosts []*AlarmHostInfo
		expected   string
	}{
		"test build metrics": {
			bkBizID: 2,
			hosts: []*AlarmHostInfo{
				{
					BkHostId:      1001,
					BkBizId:       2,
					BkHostInnerip: "127.0.0.1",
					BkCloudId:     3,
					TopoLinks: map[string][]map[string]interface{}{
						"module|2001": {
							{
								"bk_obj_id":  "module",
								"bk_inst_id": 2001,
							},
							{
								"bk_obj_id":  "set",
								"bk_inst_id": 3001,
							},
							{
								"bk_obj_id":  "biz",
								"bk_inst_id": 2,
							},
						},
						"module|2002": {
							{
								"bk_obj_id":  "module",
								"bk_inst_id": 2002,
							},
							{
								"bk_obj_id":  "set",
								"bk_inst_id": 3002,
							},
							{
								"bk_obj_id":  "biz",
								"bk_inst_id": 2,
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
								"bk_inst_id": 2001,
							},
							{
								"bk_obj_id":  "set",
								"bk_inst_id": 3001,
							},
							{
								"bk_obj_id":  "biz",
								"bk_inst_id": 2,
							},
						},
					},
				},
				{
					BkHostId:      1004,
					BkBizId:       2,
					BkHostInnerip: "127.0.0.4",
					BkCloudId:     3,
					TopoLinks: map[string][]map[string]interface{}{
						"module_2001": {
							{
								"bk_obj_id":  "module",
								"bk_inst_id": 2001,
							},
							{
								"bk_obj_id":  "set",
								"bk_inst_id": 3001,
							},
							{
								"bk_obj_id":  "biz",
								"bk_inst_id": 2,
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
								"bk_inst_id": 2003,
							},
							{
								"bk_obj_id":  "set",
								"bk_inst_id": 3001,
							},
							{
								"bk_obj_id":  "biz",
								"bk_inst_id": 2,
							},
						},
					},
				},
			},
			expected: `business_with_set_relation{biz_id="2",set_id="3001",bk_biz_id="2"} 1
business_with_set_relation{biz_id="2",set_id="3002",bk_biz_id="2"} 1
host_with_module_relation{host_id="1001",module_id="2002",bk_biz_id="2"} 1
host_with_module_relation{host_id="1002",module_id="2001",bk_biz_id="2"} 1
host_with_module_relation{host_id="1003",module_id="2003",bk_biz_id="2"} 1
host_with_module_relation{host_id="1004",module_id="2001",bk_biz_id="2"} 1
host_with_system_relation{bk_cloud_id="3",bk_target_ip="127.0.0.1",host_id="1001",bk_biz_id="2"} 1
host_with_system_relation{bk_cloud_id="3",bk_target_ip="127.0.0.2",host_id="1002",bk_biz_id="2"} 1
host_with_system_relation{bk_cloud_id="3",bk_target_ip="127.0.0.3",host_id="1003",bk_biz_id="2"} 1
host_with_system_relation{bk_cloud_id="3",bk_target_ip="127.0.0.4",host_id="1004",bk_biz_id="2"} 1
module_with_set_relation{module_id="2001",set_id="3001",bk_biz_id="2"} 1
module_with_set_relation{module_id="2002",set_id="3002",bk_biz_id="2"} 1
module_with_set_relation{module_id="2003",set_id="3001",bk_biz_id="2"} 1`,
		},
		"test build and clear metrics": {
			bkBizID: 2,
			hosts: []*AlarmHostInfo{
				{
					BkHostId:      1001,
					BkBizId:       2,
					BkHostInnerip: "127.0.0.1",
					BkCloudId:     3,
					TopoLinks: map[string][]map[string]interface{}{
						"module|2001": {
							{
								"bk_obj_id":  "module",
								"bk_inst_id": 2001,
							},
							{
								"bk_obj_id":  "set",
								"bk_inst_id": 3001,
							},
							{
								"bk_obj_id":  "biz",
								"bk_inst_id": 2,
							},
						},
						"module|2002": {
							{
								"bk_obj_id":  "module",
								"bk_inst_id": 2002,
							},
							{
								"bk_obj_id":  "set",
								"bk_inst_id": 3002,
							},
							{
								"bk_obj_id":  "biz",
								"bk_inst_id": 2,
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
								"bk_inst_id": 2001,
							},
							{
								"bk_obj_id":  "set",
								"bk_inst_id": 3001,
							},
							{
								"bk_obj_id":  "biz",
								"bk_inst_id": 2,
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
								"bk_inst_id": 2003,
							},
							{
								"bk_obj_id":  "set",
								"bk_inst_id": 3001,
							},
							{
								"bk_obj_id":  "biz",
								"bk_inst_id": 2,
							},
						},
					},
				},
			},
			clearHosts: []*AlarmHostInfo{
				{
					BkBizId:  2,
					BkHostId: 1001,
				},
				{
					BkBizId:  2,
					BkHostId: 1002,
				},
			},
			expected: `business_with_set_relation{biz_id="2",set_id="3001",bk_biz_id="2"} 1
host_with_module_relation{host_id="1003",module_id="2003",bk_biz_id="2"} 1
host_with_system_relation{bk_cloud_id="3",bk_target_ip="127.0.0.3",host_id="1003",bk_biz_id="2"} 1
module_with_set_relation{module_id="2003",set_id="3001",bk_biz_id="2"} 1`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_ = GetRelationMetricsBuilder().BuildMetrics(context.Background(), c.bkBizID, c.hosts)
			GetRelationMetricsBuilder().ClearMetricsWithHostID(c.clearHosts...)

			metrics := strings.Split(strings.Trim(GetRelationMetricsBuilder().String(), "\n"), "\n")
			sort.Strings(metrics)

			actual := strings.Join(metrics, "\n")
			assert.Equal(t, c.expected, actual)
		})

	}

}
