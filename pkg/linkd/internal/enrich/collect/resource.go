// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package collect

import (
	"errors"
	"fmt"

	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

// resourceProjection 是 Collect 对 enrich.resource 的完整贡献。
type resourceProjection struct {
	Values      models.ResourceValues
	Diagnostics []enrich.Diagnostic
}

func projectResource(resolution Resolution, fallbackBizID int64) resourceProjection {
	values := models.ResourceValues{BKBizID: fallbackBizID, DynamicGroupID: []string{}, CWLabels: []string{}}
	diagnostics := make([]enrich.Diagnostic, 0, 2)

	switch resolution.taskInputState {
	case taskInputMissing:
		diagnostics = append(diagnostics, enrich.Diagnostic{
			Code: enrich.DiagnosticCodeMissingField, Fields: []string{"dimensions." + rules.FieldBKCollectConfigID},
		})
	case taskInputInvalid:
		diagnostics = append(diagnostics, enrich.Diagnostic{
			Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"dimensions." + rules.FieldBKCollectConfigID},
		})
	case taskInputValid:
		if resolution.configErr != nil || !resolution.configFound || !validConfig(resolution) {
			diagnostics = appendDependency(diagnostics, rules.DependencyCollectConfig)
			break
		}
		values.ModelID = resolution.config.BKObjectCode
		// 配置和模型可独立确认；实例不可读时仍保留模型及业务补偿，
		// 不把配置中尚未验证的实例 ID 变成资源绑定或权限标签。
		if resolution.config.FinalBizID != nil && *resolution.config.FinalBizID > 0 {
			values.BKBizID = *resolution.config.FinalBizID
		}
		applyModelContext(&values, resolution)
		if resolution.instanceErr != nil || !resolution.instanceFound || !validInstance(resolution) {
			diagnostics = appendDependency(diagnostics, rules.DependencyOneModel)
			if resolution.hostTopologyErr == nil && resolution.hostTopologyFound &&
				resolution.config.BKObjectCode == rules.HostModelCode &&
				(!resolution.instanceFound || resolution.instanceErr != nil) &&
				!errors.Is(resolution.instanceErr, enrich.ErrInvalidDataSourceResponse) {
				enrich.ApplyTopology(&values, resolution.hostTopology)
			}
			values.CWLabels = enrich.ResourceLabels(values)
			break
		}
		values = enrich.ResourceValuesFromInstance(resolution.instance, fallbackBizID)
		applyModelContext(&values, resolution)
		applyServiceAndRemoteContext(&values, resolution)
		if resolution.topologyRequired && (resolution.relatedHostErr != nil || !resolution.relatedHostFound ||
			resolution.hostTopologyErr != nil || !resolution.hostTopologyFound) {
			diagnostics = appendDependency(diagnostics, rules.DependencyCollectTopology)
		}
		values.CWLabels = enrich.ResourceLabels(values)
	}
	return resourceProjection{Values: values, Diagnostics: diagnostics}
}

func applyModelContext(values *models.ResourceValues, resolution Resolution) {
	if resolution.modelErr != nil || !resolution.modelFound || resolution.model.TenantID != resolution.tenantID ||
		resolution.model.ModelCode != resolution.config.BKObjectCode || resolution.model.ModelID == "" {
		return
	}
	enrich.ApplyModelContext(values, resolution.model)
}

func applyServiceAndRemoteContext(values *models.ResourceValues, resolution Resolution) {
	if resolution.config.FinalBizID != nil && *resolution.config.FinalBizID > 0 && values.BKBizID == nil {
		values.BKBizID = *resolution.config.FinalBizID
	}
	if !resolution.topologyRequired || resolution.relatedHostErr != nil || !resolution.relatedHostFound ||
		resolution.relatedHost.TenantID != resolution.tenantID || resolution.relatedHost.ModelCode != rules.HostModelCode || resolution.relatedHost.InstanceID == "" {
		return
	}
	// 云区域只用于主机资源；远程采集和服务实例仍可补业务拓扑，但不继承主机区域。
	enrich.ApplyHostCloudContext(values, resolution.relatedHost)
	if resolution.hostTopologyErr != nil || !resolution.hostTopologyFound {
		return
	}
	enrich.ApplyTopology(values, resolution.hostTopology)
}

func validInstance(resolution Resolution) bool {
	return resolution.instance.TenantID == resolution.tenantID &&
		resolution.instance.ModelCode == resolution.config.BKObjectCode &&
		resolution.instance.InstanceID == fmt.Sprint(resolution.config.BKInstID)
}

func appendDependency(diagnostics []enrich.Diagnostic, dependency string) []enrich.Diagnostic {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == enrich.DiagnosticCodeDependencyInvalid && diagnostic.Dependency == dependency {
			return diagnostics
		}
	}
	return append(diagnostics, enrich.Diagnostic{
		Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: dependency,
	})
}
