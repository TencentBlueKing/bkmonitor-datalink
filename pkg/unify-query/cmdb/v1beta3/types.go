// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"fmt"
	"sort"
	"strings"
)

type ResourceType string

const (
	ResourceTypePod         ResourceType = "pod"
	ResourceTypeNode        ResourceType = "node"
	ResourceTypeContainer   ResourceType = "container"
	ResourceTypeDeployment  ResourceType = "deployment"
	ResourceTypeReplicaSet  ResourceType = "replicaset"
	ResourceTypeStatefulSet ResourceType = "statefulset"
	ResourceTypeDaemonSet   ResourceType = "daemonset"
	ResourceTypeJob         ResourceType = "job"
	ResourceTypeService     ResourceType = "service"
	ResourceTypeIngress     ResourceType = "ingress"
	ResourceTypeCluster     ResourceType = "cluster"
	ResourceTypeNamespace   ResourceType = "namespace"

	ResourceTypeSystem     ResourceType = "system"
	ResourceTypeK8sAddress ResourceType = "k8s_address"
	ResourceTypeDomain     ResourceType = "domain"

	ResourceTypeAPMService         ResourceType = "apm_service"
	ResourceTypeAPMServiceInstance ResourceType = "apm_service_instance"

	ResourceTypeDataSource  ResourceType = "datasource"
	ResourceTypeBKLogConfig ResourceType = "bklogconfig"

	ResourceTypeBiz    ResourceType = "biz"
	ResourceTypeSet    ResourceType = "set"
	ResourceTypeModule ResourceType = "module"
	ResourceTypeHost   ResourceType = "host"

	ResourceTypeAppVersion  ResourceType = "app_version"
	ResourceTypeGitCommit   ResourceType = "git_commit"
	ResourceTypeEnvironment ResourceType = "environment"
)

type RelationType string

const (
	RelationNodeWithSystem           RelationType = "node_with_system"
	RelationNodeWithPod              RelationType = "node_with_pod"
	RelationJobWithPod               RelationType = "job_with_pod"
	RelationPodWithReplicaSet        RelationType = "pod_with_replicaset"
	RelationPodWithStatefulSet       RelationType = "pod_with_statefulset"
	RelationDaemonSetWithPod         RelationType = "daemonset_with_pod"
	RelationDeploymentWithReplicaSet RelationType = "deployment_with_replicaset"
	RelationPodWithService           RelationType = "pod_with_service"
	RelationIngressWithService       RelationType = "ingress_with_service"

	RelationK8sAddressWithService RelationType = "k8s_address_with_service"
	RelationDomainWithService     RelationType = "domain_with_service"

	RelationAPMServiceInstanceWithPod        RelationType = "apm_service_instance_with_pod"
	RelationAPMServiceInstanceWithSystem     RelationType = "apm_service_instance_with_system"
	RelationAPMServiceWithAPMServiceInstance RelationType = "apm_service_with_apm_service_instance"

	RelationContainerWithPod RelationType = "container_with_pod"

	RelationDataSourceWithPod         RelationType = "datasource_with_pod"
	RelationDataSourceWithNode        RelationType = "datasource_with_node"
	RelationBKLogConfigWithDataSource RelationType = "bklogconfig_with_datasource"

	RelationBizWithSet     RelationType = "biz_with_set"
	RelationModuleWithSet  RelationType = "module_with_set"
	RelationHostWithModule RelationType = "host_with_module"
	RelationHostWithSystem RelationType = "host_with_system"

	RelationAppVersionWithContainer  RelationType = "app_version_with_container"
	RelationAppVersionWithSystem     RelationType = "app_version_with_system"
	RelationContainerWithEnvironment RelationType = "container_with_environment"
	RelationEnvironmentWithSystem    RelationType = "environment_with_system"
	RelationAppVersionWithGitCommit  RelationType = "app_version_with_git_commit"

	RelationPodToPod         RelationType = "pod_to_pod"
	RelationPodToSystem      RelationType = "pod_to_system"
	RelationSystemToPod      RelationType = "system_to_pod"
	RelationSystemToSystem   RelationType = "system_to_system"
	RelationServiceToService RelationType = "service_to_service"
)

type RelationCategory string

const (
	RelationCategoryStatic  RelationCategory = "static"
	RelationCategoryDynamic RelationCategory = "dynamic"
)

type TraversalDirection string

const (
	DirectionOutbound TraversalDirection = "outbound"
	DirectionInbound  TraversalDirection = "inbound"
	DirectionBoth     TraversalDirection = "both"
)

// GenerateResourceID 生成资源ID，格式: {resource_type}:⟨key1=value1,key2=value2,...⟩
func GenerateResourceID(resourceType ResourceType, labels map[string]string) string {
	if len(labels) == 0 {
		return string(resourceType) + ":⟨⟩"
	}

	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(labels))
	for _, k := range keys {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, labels[k]))
	}

	return fmt.Sprintf("%s:⟨%s⟩", resourceType, strings.Join(pairs, ","))
}

// generateResourceIdentityKey 按 schema 声明的主键字段生成资源身份。
// 资源类型、字段名和值都使用长度前缀编码，避免值中包含分隔符时产生歧义。
func generateResourceIdentityKey(resourceType ResourceType, fields []string, labels map[string]string) string {
	if len(fields) == 0 {
		return GenerateResourceID(resourceType, labels)
	}

	var builder strings.Builder
	builder.WriteString(string(resourceType))
	for _, field := range fields {
		value, ok := labels[field]
		if !ok {
			return ""
		}
		fmt.Fprintf(&builder, ":%d:%s=%d:%s", len(field), field, len(value), value)
	}
	return builder.String()
}
