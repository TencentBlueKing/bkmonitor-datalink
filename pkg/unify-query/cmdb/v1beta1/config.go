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
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

var configData = &Config{
	Resource: []ResourceConf{
		{
			Name: "system",
			Index: cmdb.Index{
				"bk_target_ip",
			},
		},
		{
			Name: "datasource",
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
			Name: "container",
			Index: cmdb.Index{
				"bcs_cluster_id",
				"namespace",
				"pod",
				"container",
			},
			Info: cmdb.Index{
				"version",
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
		},
		{
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
			Name: "bklogconfig",
			Index: cmdb.Index{
				"bklogconfig_namespace",
				"bklogconfig_name",
			},
		},
		{
			Name: "business",
			Index: cmdb.Index{
				"biz_id",
			},
		},
		{
			Name: "set",
			Index: cmdb.Index{
				"set_id",
			},
		},
		{
			Name: "module",
			Index: cmdb.Index{
				"module_id",
			},
		},
		{
			Name: "app_version",
			Index: cmdb.Index{
				"app_name",
				"version",
			},
		},
		{
			Name: "git_commit",
			Index: cmdb.Index{
				"git_repo",
				"commit_id",
			},
		},
		{
			Name: "host",
			Index: cmdb.Index{
				"host_id",
			},
			Info: cmdb.Index{
				"version",
				"env_name",
				"env_type",
				"service_version",
				"service_type",
			},
		},
	},
	Relation: []RelationConf{
		{
			Resources: [2]cmdb.Resource{
				"node", "system",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"node", "pod",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"job", "pod",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"container", "pod",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"pod", "replicaset",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"pod", "statefulset",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"deamonset", "pod",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"deployment", "replicaset",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"pod", "service",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"datasource", "pod",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"datasource", "node",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"ingress", "service",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"k8s_address", "service",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"domain", "service",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"apm_service_instance", "system",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"apm_service_instance", "pod",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"apm_service", "apm_service_instance",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"bklogconfig", "datasource",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"business", "set",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"module", "set",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"host", "module",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"host", "system",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"app_version", "host",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"app_version", "container",
			},
		},
		{
			Resources: [2]cmdb.Resource{
				"app_version", "git_commit",
			},
		},
	},
}

var (
	resourceConfig = make(map[cmdb.Resource]ResourceConf)
	relations      []cmdb.Relation
)

func init() {
	for _, c := range configData.Resource {
		resourceConfig[c.Name] = c
	}
	relations = make([]cmdb.Relation, 0, len(configData.Relation))
	for _, r := range configData.Relation {
		relations = append(relations, cmdb.Relation{V: r.Resources})
	}
}

func ResourcesIndex(resources ...cmdb.Resource) cmdb.Index {
	var index cmdb.Index
	for _, r := range resources {
		index = append(index, resourceConfig[r].Index...)
	}
	sort.Strings(index)
	return index
}

func ResourcesInfo(resources ...cmdb.Resource) cmdb.Index {
	var index []string
	for _, r := range resources {
		index = append(index, resourceConfig[r].Info...)
	}
	sort.Strings(index)
	return index
}

func AllResources() map[cmdb.Resource]ResourceConf {
	return resourceConfig
}

func AllRelations() []cmdb.Relation {
	return relations
}
