// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

type RelationSchema struct {
	RelationType RelationType
	Category     RelationCategory
	FromType     ResourceType
	ToType       ResourceType
	IsBelongsTo  bool
}

var schemaRegistry = []RelationSchema{
	{RelationNodeWithSystem, RelationCategoryStatic, ResourceTypeNode, ResourceTypeSystem, false},
	{RelationNodeWithPod, RelationCategoryStatic, ResourceTypeNode, ResourceTypePod, false},
	{RelationJobWithPod, RelationCategoryStatic, ResourceTypeJob, ResourceTypePod, false},
	{RelationPodWithReplicaSet, RelationCategoryStatic, ResourceTypePod, ResourceTypeReplicaSet, true},
	{RelationPodWithStatefulSet, RelationCategoryStatic, ResourceTypePod, ResourceTypeStatefulSet, true},
	{RelationDaemonSetWithPod, RelationCategoryStatic, ResourceTypeDaemonSet, ResourceTypePod, true},
	{RelationDeploymentWithReplicaSet, RelationCategoryStatic, ResourceTypeDeployment, ResourceTypeReplicaSet, true},
	{RelationPodWithService, RelationCategoryStatic, ResourceTypePod, ResourceTypeService, false},
	{RelationIngressWithService, RelationCategoryStatic, ResourceTypeIngress, ResourceTypeService, false},
	{RelationK8sAddressWithService, RelationCategoryStatic, ResourceTypeK8sAddress, ResourceTypeService, false},
	{RelationDomainWithService, RelationCategoryStatic, ResourceTypeDomain, ResourceTypeService, false},
	{RelationAPMServiceInstanceWithPod, RelationCategoryStatic, ResourceTypeAPMServiceInstance, ResourceTypePod, false},
	{RelationAPMServiceInstanceWithSystem, RelationCategoryStatic, ResourceTypeAPMServiceInstance, ResourceTypeSystem, false},
	{RelationAPMServiceWithAPMServiceInstance, RelationCategoryStatic, ResourceTypeAPMService, ResourceTypeAPMServiceInstance, true},
	{RelationContainerWithPod, RelationCategoryStatic, ResourceTypeContainer, ResourceTypePod, true},
	{RelationDataSourceWithPod, RelationCategoryStatic, ResourceTypeDataSource, ResourceTypePod, false},
	{RelationDataSourceWithNode, RelationCategoryStatic, ResourceTypeDataSource, ResourceTypeNode, false},
	{RelationBKLogConfigWithDataSource, RelationCategoryStatic, ResourceTypeBKLogConfig, ResourceTypeDataSource, false},
	{RelationBizWithSet, RelationCategoryStatic, ResourceTypeBiz, ResourceTypeSet, true},
	{RelationModuleWithSet, RelationCategoryStatic, ResourceTypeModule, ResourceTypeSet, true},
	{RelationHostWithModule, RelationCategoryStatic, ResourceTypeHost, ResourceTypeModule, true},
	{RelationHostWithSystem, RelationCategoryStatic, ResourceTypeHost, ResourceTypeSystem, false},
	{RelationAppVersionWithContainer, RelationCategoryStatic, ResourceTypeAppVersion, ResourceTypeContainer, false},
	{RelationAppVersionWithSystem, RelationCategoryStatic, ResourceTypeAppVersion, ResourceTypeSystem, false},
	{RelationContainerWithEnvironment, RelationCategoryStatic, ResourceTypeContainer, ResourceTypeEnvironment, false},
	{RelationEnvironmentWithSystem, RelationCategoryStatic, ResourceTypeEnvironment, ResourceTypeSystem, false},
	{RelationAppVersionWithGitCommit, RelationCategoryStatic, ResourceTypeAppVersion, ResourceTypeGitCommit, false},

	{RelationPodToPod, RelationCategoryDynamic, ResourceTypePod, ResourceTypePod, false},
	{RelationPodToSystem, RelationCategoryDynamic, ResourceTypePod, ResourceTypeSystem, false},
	{RelationSystemToPod, RelationCategoryDynamic, ResourceTypeSystem, ResourceTypePod, false},
	{RelationSystemToSystem, RelationCategoryDynamic, ResourceTypeSystem, ResourceTypeSystem, false},
	{RelationServiceToService, RelationCategoryDynamic, ResourceTypeService, ResourceTypeService, false},
}
