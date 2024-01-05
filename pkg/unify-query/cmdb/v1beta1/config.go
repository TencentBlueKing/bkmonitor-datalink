// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"

var configData = &Config{
	Resource: []ResourceConf{
		{
			Name: "system",
			Index: cmdb.Index{
				"bk_target_ip",
			},
		},
		{
			Name: "node",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"node",
			},
		},
		//{
		//	Name: "cluster",
		//	Index: cmdb.Index{
		//		"bcs_cluster_id",
		//	},
		//},
		//{
		//	Name: "namespace",
		//	Index: cmdb.Index{
		//		"bcs_cluster_id",
		//		"namespace",
		//	},
		//},
		{
			Name: "container",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"namespace",
				"pod",
				"container",
			},
		},
		{
			Name: "pod",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"namespace",
				"pod",
			},
		},
		{
			Name: "replicaset",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"namespace",
				"replicaset",
			},
		},
		{
			Name: "deployment",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"namespace",
				"deployment",
			},
		}, {
			Name: "statefulset",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"namespace",
				"statefulset",
			},
		}, {
			Name: "deamonset",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"namespace",
				"deamonset",
			},
		},
	},
	Relation: []RelationConf{
		{
			Resources: []cmdb.Resource{
				"node", "system",
			},
		},
		{
			Resources: []cmdb.Resource{
				"node", "pod",
			},
		},
		{
			Resources: []cmdb.Resource{
				"pod", "replicaset",
			},
		},
		{
			Resources: []cmdb.Resource{
				"pod", "statefulset",
			},
		},
		{
			Resources: []cmdb.Resource{
				"pod", "deamonset",
			},
		},
		{
			Resources: []cmdb.Resource{
				"replicaset", "deployment",
			},
		},
	},
}
