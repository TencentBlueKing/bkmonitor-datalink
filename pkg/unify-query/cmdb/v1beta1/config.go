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
			Name: "data_source",
			Index: cmdb.Index{
				"bk_data_id",
			},
		},
		{
			Name: "node",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"node",
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
			Name: "job",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"namespace",
				"job",
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
			Name: "deamonset",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"namespace",
				"deamonset",
			},
		},
		{
			Name: "statefulset",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"namespace",
				"statefulset",
			},
		},
		{
			Name: "service",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"namespace",
				"service",
			},
		},
		{
			Name: "ingress",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"namespace",
				"ingress",
			},
		},
		{
			Name: "k8s_address",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"address",
			},
		},
		{
			Name: "domain",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"domain",
			},
		},
		{
			Name: "apm_service",
			Index: cmdb.Index{
				"apm_application_name",
				"apm_service_name",
			},
		},
		{
			Name: "apm_service_instance",
			Index: cmdb.Index{
				"apm_application_name",
				"apm_service_name",
				"apm_service_instance_name",
			},
		},
		{
			Name: "bk_log_config",
			Index: cmdb.Index{
				"bk_log_config_namespace",
				"bk_log_config_name",
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
				"job", "pod",
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
				"deamonset", "pod",
			},
		},
		{
			Resources: []cmdb.Resource{
				"deployment", "replicaset",
			},
		},
		{
			Resources: []cmdb.Resource{
				"pod", "service",
			},
		},
		{
			Resources: []cmdb.Resource{
				"data_source", "pod",
			},
		},
		{
			Resources: []cmdb.Resource{
				"data_source", "node",
			},
		},
		{
			Resources: []cmdb.Resource{
				"ingress", "service",
			},
		},
		{
			Resources: []cmdb.Resource{
				"k8s_address", "service",
			},
		},
		{
			Resources: []cmdb.Resource{
				"domain", "service",
			},
		},
		{
			Resources: []cmdb.Resource{
				"apm_service_instance", "system",
			},
		},
		{
			Resources: []cmdb.Resource{
				"apm_service_instance", "pod",
			},
		},
		{
			Resources: []cmdb.Resource{
				"apm_service", "apm_service_instance",
			},
		},
		{
			Resources: []cmdb.Resource{
				"bk_log_config", "data_source",
			},
		},
	},
}
