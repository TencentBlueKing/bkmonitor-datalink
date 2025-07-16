// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

var configData = &Config{
	Resource: []ResourceConf{
		{
			Name: "system",
			Index: []string{
				"bk_target_ip",
			},
		},
		{
			Name: "datasource",
			Index: []string{
				"bk_data_id",
			},
		},
		{
			Name: "node",
			Index: []string{
				"bcs_cluster_id",
				"node",
			},
		},
		{
			Name: "pod",
			Index: []string{
				"bcs_cluster_id",
				"namespace",
				"pod",
			},
		},
		{
			Name: "job",
			Index: []string{
				"bcs_cluster_id",
				"namespace",
				"job",
			},
		},
		{
			Name: "replicaset",
			Index: []string{
				"bcs_cluster_id",
				"namespace",
				"replicaset",
			},
		},
		{
			Name: "deployment",
			Index: []string{
				"bcs_cluster_id",
				"namespace",
				"deployment",
			},
		}, {
			Name: "deamonset",
			Index: []string{
				"bcs_cluster_id",
				"namespace",
				"deamonset",
			},
		},
		{
			Name: "statefulset",
			Index: []string{
				"bcs_cluster_id",
				"namespace",
				"statefulset",
			},
		},
		{
			Name: "service",
			Index: []string{
				"bcs_cluster_id",
				"namespace",
				"service",
			},
		},
		{
			Name: "ingress",
			Index: []string{
				"bcs_cluster_id",
				"namespace",
				"ingress",
			},
		},
		{
			Name: "k8s_address",
			Index: []string{
				"bcs_cluster_id",
				"address",
			},
		},
		{
			Name: "domain",
			Index: []string{
				"bcs_cluster_id",
				"domain",
			},
		},
		{
			Name: "apm_service",
			Index: []string{
				"apm_application_name",
				"apm_service_name",
			},
		},
		{
			Name: "apm_service_instance",
			Index: []string{
				"apm_application_name",
				"apm_service_name",
				"apm_service_instance_name",
			},
		},
		{
			Name: "bklogconfig",
			Index: []string{
				"bklogconfig_namespace",
				"bklogconfig_name",
			},
		},
		{
			Name: "business",
			Index: []string{
				"biz_id",
			},
		},
		{
			Name: "set",
			Index: []string{
				"set_id",
			},
		},
		{
			Name: "module",
			Index: []string{
				"module_id",
			},
		},
		{
			Name: "host",
			Index: []string{
				"host_id",
			},
		},
	},
	Relation: []RelationConf{
		{
			Resources: []string{
				"node", "system",
			},
		},
		{
			Resources: []string{
				"node", "pod",
			},
		},
		{
			Resources: []string{
				"job", "pod",
			},
		},
		{
			Resources: []string{
				"pod", "replicaset",
			},
		},
		{
			Resources: []string{
				"pod", "statefulset",
			},
		},
		{
			Resources: []string{
				"deamonset", "pod",
			},
		},
		{
			Resources: []string{
				"deployment", "replicaset",
			},
		},
		{
			Resources: []string{
				"pod", "service",
			},
		},
		{
			Resources: []string{
				"datasource", "pod",
			},
		},
		{
			Resources: []string{
				"datasource", "node",
			},
		},
		{
			Resources: []string{
				"ingress", "service",
			},
		},
		{
			Resources: []string{
				"k8s_address", "service",
			},
		},
		{
			Resources: []string{
				"domain", "service",
			},
		},
		{
			Resources: []string{
				"apm_service_instance", "system",
			},
		},
		{
			Resources: []string{
				"apm_service_instance", "pod",
			},
		},
		{
			Resources: []string{
				"apm_service", "apm_service_instance",
			},
		},
		{
			Resources: []string{
				"bklogconfig", "datasource",
			},
		},
		{
			Resources: []string{
				"business", "set",
			},
		},
		{
			Resources: []string{
				"module", "set",
			},
		},
		{
			Resources: []string{
				"host", "module",
			},
		},
		{
			Resources: []string{
				"host", "system",
			},
		},
	},
}
