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

// RelationType 是关系定义名（不含 namespace 前缀），取值以 schema provider 中的定义为准。
type RelationType string

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
// 资源类型使用普通前缀，字段名和值使用长度前缀，避免值中包含分隔符时产生
// 歧义；主键字段缺失时返回空字符串，未声明主键时回退到 GenerateResourceID。
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
