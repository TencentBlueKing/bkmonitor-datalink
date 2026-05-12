// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

var DefaultResourcePrimaryKeys = map[string][]string{
	"pod":                  {"bcs_cluster_id", "namespace", "pod"},
	"node":                 {"bcs_cluster_id", "node"},
	"container":            {"bcs_cluster_id", "namespace", "pod", "container"},
	"deployment":           {"bcs_cluster_id", "namespace", "deployment"},
	"replicaset":           {"bcs_cluster_id", "namespace", "replicaset"},
	"statefulset":          {"bcs_cluster_id", "namespace", "statefulset"},
	"daemonset":            {"bcs_cluster_id", "namespace", "daemonset"},
	"job":                  {"bcs_cluster_id", "namespace", "job"},
	"service":              {"bcs_cluster_id", "namespace", "service"},
	"ingress":              {"bcs_cluster_id", "namespace", "ingress"},
	"cluster":              {"bcs_cluster_id"},
	"namespace":            {"bcs_cluster_id", "namespace"},
	"system":               {"bk_cloud_id", "bk_target_ip"},
	"k8s_address":          {"address"},
	"domain":               {"domain"},
	"apm_service":          {"bk_biz_id", "app_name", "apm_service_name"},
	"apm_service_instance": {"bk_biz_id", "app_name", "apm_service_name", "apm_service_instance_id"},
	"datasource":           {"bk_data_id"},
	"bklogconfig":          {"bklogconfig_namespace", "bklogconfig_name"},
	"biz":                  {"bk_biz_id"},
	"set":                  {"bk_set_id"},
	"module":               {"bk_module_id"},
	"host":                 {"bk_host_id"},
	"app_version":          {"bcs_cluster_id", "namespace", "app_version"},
	"git_commit":           {"git_repo", "commit_id"},
	"environment":          {"bcs_cluster_id", "namespace", "pod", "env_name"},
}

var DefaultRelationSchemas = []RelationSchema{
	{"node_with_system", RelationCategoryStatic, "node", "system", false},
	{"node_with_pod", RelationCategoryStatic, "node", "pod", false},
	{"job_with_pod", RelationCategoryStatic, "job", "pod", false},
	{"pod_with_replicaset", RelationCategoryStatic, "pod", "replicaset", true},
	{"pod_with_statefulset", RelationCategoryStatic, "pod", "statefulset", true},
	{"daemonset_with_pod", RelationCategoryStatic, "daemonset", "pod", true},
	{"deployment_with_replicaset", RelationCategoryStatic, "deployment", "replicaset", true},
	{"pod_with_service", RelationCategoryStatic, "pod", "service", false},
	{"ingress_with_service", RelationCategoryStatic, "ingress", "service", false},
	{"k8s_address_with_service", RelationCategoryStatic, "k8s_address", "service", false},
	{"domain_with_service", RelationCategoryStatic, "domain", "service", false},
	{"apm_service_instance_with_pod", RelationCategoryStatic, "apm_service_instance", "pod", false},
	{"apm_service_instance_with_system", RelationCategoryStatic, "apm_service_instance", "system", false},
	{"apm_service_with_apm_service_instance", RelationCategoryStatic, "apm_service", "apm_service_instance", true},
	{"container_with_pod", RelationCategoryStatic, "container", "pod", true},
	{"datasource_with_pod", RelationCategoryStatic, "datasource", "pod", false},
	{"datasource_with_node", RelationCategoryStatic, "datasource", "node", false},
	{"bklogconfig_with_datasource", RelationCategoryStatic, "bklogconfig", "datasource", false},
	{"biz_with_set", RelationCategoryStatic, "biz", "set", true},
	{"module_with_set", RelationCategoryStatic, "module", "set", true},
	{"host_with_module", RelationCategoryStatic, "host", "module", true},
	{"host_with_system", RelationCategoryStatic, "host", "system", false},
	{"app_version_with_container", RelationCategoryStatic, "app_version", "container", false},
	{"app_version_with_system", RelationCategoryStatic, "app_version", "system", false},
	{"container_with_environment", RelationCategoryStatic, "container", "environment", false},
	{"environment_with_system", RelationCategoryStatic, "environment", "system", false},
	{"app_version_with_git_commit", RelationCategoryStatic, "app_version", "git_commit", false},

	{"pod_to_pod", RelationCategoryDynamic, "pod", "pod", false},
	{"pod_to_system", RelationCategoryDynamic, "pod", "system", false},
	{"system_to_pod", RelationCategoryDynamic, "system", "pod", false},
	{"system_to_system", RelationCategoryDynamic, "system", "system", false},
	{"service_to_service", RelationCategoryDynamic, "service", "service", false},
	{"apm_service_to_apm_service", RelationCategoryDynamic, "apm_service", "apm_service", false},
}

// NewDefaultStaticSchemaProvider creates a StaticSchemaProvider with default data
func NewDefaultStaticSchemaProvider() *StaticSchemaProvider {
	config := StaticProviderConfig{
		ResourcePrimaryKeys: DefaultResourcePrimaryKeys,
		RelationSchemas:     DefaultRelationSchemas,
	}
	return NewStaticSchemaProvider(config)
}
