// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

// defaultResourceDefinitions 默认资源定义，覆盖 CMDB、K8s、APM、应用版本等资源。
// Required=true 的字段为索引字段（主键），Required=false 的字段为扩展信息字段。
var defaultResourceDefinitions = []*ResourceDefinition{
	{
		Namespace: NamespaceAll,
		Name:      "system",
		Fields: []FieldDefinition{
			{Name: "bk_target_ip", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "datasource",
		Fields: []FieldDefinition{
			{Name: "bk_data_id", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "node",
		Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "node", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "container",
		Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "namespace", Required: true},
			{Name: "pod", Required: true},
			{Name: "container", Required: true},
			{Name: "version", Required: false},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "pod",
		Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "namespace", Required: true},
			{Name: "pod", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "job",
		Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "namespace", Required: true},
			{Name: "job", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "replicaset",
		Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "namespace", Required: true},
			{Name: "replicaset", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "deployment",
		Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "namespace", Required: true},
			{Name: "deployment", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "daemonset",
		Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "namespace", Required: true},
			{Name: "daemonset", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "statefulset",
		Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "namespace", Required: true},
			{Name: "statefulset", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "service",
		Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "namespace", Required: true},
			{Name: "service", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "ingress",
		Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "namespace", Required: true},
			{Name: "ingress", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "k8s_address",
		Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "address", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "domain",
		Fields: []FieldDefinition{
			{Name: "bcs_cluster_id", Required: true},
			{Name: "domain", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "apm_service",
		Fields: []FieldDefinition{
			{Name: "apm_application_name", Required: true},
			{Name: "apm_service_name", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "apm_service_instance",
		Fields: []FieldDefinition{
			{Name: "apm_application_name", Required: true},
			{Name: "apm_service_name", Required: true},
			{Name: "apm_service_instance_name", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "bklogconfig",
		Fields: []FieldDefinition{
			{Name: "bklogconfig_namespace", Required: true},
			{Name: "bklogconfig_name", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "business",
		Fields: []FieldDefinition{
			{Name: "bk_biz_id", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "set",
		Fields: []FieldDefinition{
			{Name: "bk_set_id", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "module",
		Fields: []FieldDefinition{
			{Name: "bk_module_id", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "app_version",
		Fields: []FieldDefinition{
			{Name: "app_name", Required: true},
			{Name: "version", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "git_commit",
		Fields: []FieldDefinition{
			{Name: "git_repo", Required: true},
			{Name: "commit_id", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "p4_changelist",
		Fields: []FieldDefinition{
			{Name: "p4_port", Required: true},
			{Name: "changelist_id", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "svn_revision",
		Fields: []FieldDefinition{
			{Name: "svn_repo", Required: true},
			{Name: "revision", Required: true},
		},
	},
	{
		Namespace: NamespaceAll,
		Name:      "host",
		Fields: []FieldDefinition{
			{Name: "bk_host_id", Required: true},
			{Name: "version", Required: false},
			{Name: "env_name", Required: false},
			{Name: "env_type", Required: false},
			{Name: "service_version", Required: false},
			{Name: "service_type", Required: false},
		},
	},
}

// defaultRelationDefinitions 默认关联定义，覆盖 CMDB、K8s、APM、应用版本等资源间的关联。
var defaultRelationDefinitions = []*RelationDefinition{
	{Namespace: NamespaceAll, Name: "node_system", FromResource: "node", ToResource: "system"},
	{Namespace: NamespaceAll, Name: "node_pod", FromResource: "node", ToResource: "pod"},
	{Namespace: NamespaceAll, Name: "job_pod", FromResource: "job", ToResource: "pod"},
	{Namespace: NamespaceAll, Name: "container_pod", FromResource: "container", ToResource: "pod"},
	{Namespace: NamespaceAll, Name: "pod_replicaset", FromResource: "pod", ToResource: "replicaset"},
	{Namespace: NamespaceAll, Name: "pod_statefulset", FromResource: "pod", ToResource: "statefulset"},
	{Namespace: NamespaceAll, Name: "daemonset_pod", FromResource: "daemonset", ToResource: "pod"},
	{Namespace: NamespaceAll, Name: "deployment_replicaset", FromResource: "deployment", ToResource: "replicaset"},
	{Namespace: NamespaceAll, Name: "pod_service", FromResource: "pod", ToResource: "service"},
	{Namespace: NamespaceAll, Name: "datasource_pod", FromResource: "datasource", ToResource: "pod"},
	{Namespace: NamespaceAll, Name: "datasource_node", FromResource: "datasource", ToResource: "node"},
	{Namespace: NamespaceAll, Name: "ingress_service", FromResource: "ingress", ToResource: "service"},
	{Namespace: NamespaceAll, Name: "k8s_address_service", FromResource: "k8s_address", ToResource: "service"},
	{Namespace: NamespaceAll, Name: "domain_service", FromResource: "domain", ToResource: "service"},
	{Namespace: NamespaceAll, Name: "apm_service_instance_system", FromResource: "apm_service_instance", ToResource: "system"},
	{Namespace: NamespaceAll, Name: "apm_service_instance_pod", FromResource: "apm_service_instance", ToResource: "pod"},
	{Namespace: NamespaceAll, Name: "apm_service_apm_service_instance", FromResource: "apm_service", ToResource: "apm_service_instance"},
	{Namespace: NamespaceAll, Name: "bklogconfig_datasource", FromResource: "bklogconfig", ToResource: "datasource"},
	{Namespace: NamespaceAll, Name: "business_set", FromResource: "business", ToResource: "set"},
	{Namespace: NamespaceAll, Name: "module_set", FromResource: "module", ToResource: "set"},
	{Namespace: NamespaceAll, Name: "host_module", FromResource: "host", ToResource: "module"},
	{Namespace: NamespaceAll, Name: "host_system", FromResource: "host", ToResource: "system"},
	{Namespace: NamespaceAll, Name: "app_version_host", FromResource: "app_version", ToResource: "host"},
	{Namespace: NamespaceAll, Name: "app_version_container", FromResource: "app_version", ToResource: "container"},
	{Namespace: NamespaceAll, Name: "app_version_git_commit", FromResource: "app_version", ToResource: "git_commit"},
	{Namespace: NamespaceAll, Name: "app_version_p4_changelist", FromResource: "app_version", ToResource: "p4_changelist"},
	{Namespace: NamespaceAll, Name: "app_version_svn_revision", FromResource: "app_version", ToResource: "svn_revision"},
}

// DefaultStaticProviderConfig 返回包含完整默认 schema 的 StaticProviderConfig。
// unify-query 和 bk-monitor-worker 在 static 模式下都应使用此配置初始化 SchemaProvider。
func DefaultStaticProviderConfig() StaticProviderConfig {
	resourcePrimaryKeys := make(map[string][]string, len(defaultResourceDefinitions))
	for _, rd := range defaultResourceDefinitions {
		var keys []string
		for _, f := range rd.Fields {
			if f.Required {
				keys = append(keys, f.Name)
			}
		}
		resourcePrimaryKeys[rd.Name] = keys
	}

	relationSchemas := make([]RelationSchema, 0, len(defaultRelationDefinitions))
	for _, rd := range defaultRelationDefinitions {
		relationSchemas = append(relationSchemas, RelationSchema{
			RelationName: RelationName(rd.Name),
			Category:     RelationCategoryStatic,
			FromType:     ResourceType(rd.FromResource),
			ToType:       ResourceType(rd.ToResource),
		})
	}

	return StaticProviderConfig{
		ResourcePrimaryKeys: resourcePrimaryKeys,
		RelationSchemas:     relationSchemas,
	}
}

// DefaultResourceDefinitions 返回默认资源定义列表（含 Info 字段）。
// 供需要完整 FieldDefinition 信息（包括非主键字段）的组件使用。
func DefaultResourceDefinitions() []*ResourceDefinition {
	return defaultResourceDefinitions
}

// DefaultRelationDefinitions 返回默认关联定义列表。
func DefaultRelationDefinitions() []*RelationDefinition {
	return defaultRelationDefinitions
}
