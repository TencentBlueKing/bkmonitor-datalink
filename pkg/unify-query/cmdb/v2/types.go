// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

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

const (
	FieldPeriodStart = "period_start"
	FieldPeriodEnd   = "period_end"
)

const (
	ResponseFieldResult = "result"
	ResponseFieldRoot   = "root"
	ResponseFieldTarget = "target"

	ResponseFieldHopPrefix = "hop"

	ResponseFieldEntityID   = "entity_id"
	ResponseFieldEntityType = "entity_type"
	ResponseFieldEntityData = "entity_data"
	ResponseFieldLiveness   = "liveness"

	ResponseFieldRelationID       = "relation_id"
	ResponseFieldRelationType     = "relation_type"
	ResponseFieldRelationCategory = "relation_category"
	ResponseFieldRelationLiveness = "relation_liveness"
	ResponseFieldDirection        = "direction"
)

// LivenessRecord 资源存活记录
type LivenessRecord struct {
	ID          string `json:"id"`
	ResourceID  string `json:"resource_id"`
	PeriodStart int64  `json:"period_start"`
	PeriodEnd   int64  `json:"period_end"`
	IsActive    bool   `json:"is_active"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// VisiblePeriod 可见时间段
type VisiblePeriod struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// Overlap 计算两个 VisiblePeriod 的交集
func (p *VisiblePeriod) Overlap(other *VisiblePeriod) *VisiblePeriod {
	start := p.Start
	if other.Start > start {
		start = other.Start
	}
	end := p.End
	if other.End < end {
		end = other.End
	}
	if start > end {
		return nil
	}
	return &VisiblePeriod{Start: start, End: end}
}

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

func GetLivenessRecordTableName(resourceType ResourceType) string {
	return string(resourceType) + "_liveness_record"
}

func GetRelationLivenessRecordTableName(relationType RelationType) string {
	return string(relationType) + "_liveness_record"
}

func GetLivenessIDField(resourceType ResourceType) string {
	return string(resourceType) + "_id"
}

var resourcePrimaryKeys = map[ResourceType][]string{
	ResourceTypePod:                {"bcs_cluster_id", "namespace", "pod"},
	ResourceTypeNode:               {"bcs_cluster_id", "node"},
	ResourceTypeContainer:          {"bcs_cluster_id", "namespace", "pod", "container"},
	ResourceTypeDeployment:         {"bcs_cluster_id", "namespace", "deployment"},
	ResourceTypeReplicaSet:         {"bcs_cluster_id", "namespace", "replicaset"},
	ResourceTypeStatefulSet:        {"bcs_cluster_id", "namespace", "statefulset"},
	ResourceTypeDaemonSet:          {"bcs_cluster_id", "namespace", "daemonset"},
	ResourceTypeJob:                {"bcs_cluster_id", "namespace", "job"},
	ResourceTypeService:            {"bcs_cluster_id", "namespace", "service"},
	ResourceTypeIngress:            {"bcs_cluster_id", "namespace", "ingress"},
	ResourceTypeCluster:            {"bcs_cluster_id"},
	ResourceTypeNamespace:          {"bcs_cluster_id", "namespace"},
	ResourceTypeSystem:             {"bk_cloud_id", "bk_target_ip"},
	ResourceTypeK8sAddress:         {"address"},
	ResourceTypeDomain:             {"domain"},
	ResourceTypeAPMService:         {"bk_biz_id", "app_name", "apm_service_name"},
	ResourceTypeAPMServiceInstance: {"bk_biz_id", "app_name", "apm_service_name", "apm_service_instance_id"},
	ResourceTypeDataSource:         {"bk_data_id"},
	ResourceTypeBKLogConfig:        {"bklogconfig_namespace", "bklogconfig_name"},
	ResourceTypeBiz:                {"bk_biz_id"},
	ResourceTypeSet:                {"bk_set_id"},
	ResourceTypeModule:             {"bk_module_id"},
	ResourceTypeHost:               {"bk_host_id"},
	ResourceTypeAppVersion:         {"bcs_cluster_id", "namespace", "app_version"},
	ResourceTypeGitCommit:          {"git_repo", "commit_id"},
	ResourceTypeEnvironment:        {"bcs_cluster_id", "namespace", "pod", "env_name"},
}

func GetResourcePrimaryKeys(resourceType ResourceType) []string {
	return resourcePrimaryKeys[resourceType]
}
