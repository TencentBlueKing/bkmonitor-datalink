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

package basetarget

import (
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

type resourceProjection struct {
	Values      models.ResourceValues
	Diagnostics []enrich.Diagnostic
}

func projectResource(resolution resolution, fallbackBizID int64) resourceProjection {
	values := models.ResourceValues{
		ModelID: resolution.modelCode, BKBizID: fallbackBizID, DynamicGroupID: []string{}, CWLabels: []string{},
	}
	diagnostics := append([]enrich.Diagnostic(nil), resolution.queryDiagnostics...)
	if resolution.branch == rules.BaseTargetBasic {
		values.BKBizID = fallbackBizID
		if resolution.modelErr != nil || !resolution.modelFound || !validModel(resolution) {
			diagnostics = appendDependency(diagnostics, rules.DependencyOneModel)
		} else {
			enrich.ApplyModelContext(&values, resolution.model)
		}
		values.CWLabels = enrich.ResourceLabels(values)
		return resourceProjection{Values: values, Diagnostics: diagnostics}
	}
	// 模型元数据可独立于实例确认；核心实例失败时仍保留有效模型上下文。
	if resolution.modelErr == nil && resolution.modelFound && validModel(resolution) {
		enrich.ApplyModelContext(&values, resolution.model)
	}
	if resolution.inputState != inputValid {
		values.CWLabels = enrich.ResourceLabels(values)
		return resourceProjection{Values: values, Diagnostics: diagnostics}
	}
	if resolution.instanceErr != nil || !resolution.instanceFound || !validInstance(resolution) {
		diagnostics = appendDependency(diagnostics, rules.DependencyOneModel)
		if resolution.topologyErr == nil && resolution.topologyFound {
			enrich.ApplyTopology(&values, resolution.topology)
		}
		values.CWLabels = enrich.ResourceLabels(values)
		return resourceProjection{Values: values, Diagnostics: diagnostics}
	}
	// IP + 云区域是合法的替代定位方式；实例身份复核通过后，缺少输入主机 ID
	// 不再属于丰富失败。模型元数据和拓扑依赖仍在下方独立判定，不能一并降为成功。
	if resolution.branch == rules.BaseTargetSystemMetric && resolution.query.InstanceID == "" {
		diagnostics = nil
	}
	values = enrich.ResourceValuesFromInstance(resolution.instance, fallbackBizID)
	if resolution.modelErr != nil || !resolution.modelFound || !validModel(resolution) {
		diagnostics = appendDependency(diagnostics, rules.DependencyOneModel)
	} else {
		enrich.ApplyModelContext(&values, resolution.model)
	}
	if resolution.topologyRequired {
		if resolution.relatedHostErr != nil || !resolution.relatedHostFound || resolution.topologyErr != nil || !resolution.topologyFound {
			diagnostics = appendDependency(diagnostics, rules.DependencyCollectTopology)
		} else {
			enrich.ApplyTopology(&values, resolution.topology)
		}
		if resolution.relatedHostErr == nil && resolution.relatedHostFound && resolution.relatedHost.TenantID == resolution.tenantID {
			enrich.ApplyHostCloudContext(&values, resolution.relatedHost)
		}
	}
	values.CWLabels = enrich.ResourceLabels(values)
	return resourceProjection{Values: values, Diagnostics: diagnostics}
}

func validModel(resolution resolution) bool {
	return resolution.model.TenantID == resolution.tenantID && resolution.model.ModelCode == resolution.modelCode && resolution.model.ModelID != ""
}

func validInstance(resolution resolution) bool {
	return resolution.instance.TenantID == resolution.tenantID &&
		resolution.instance.ModelCode == resolution.modelCode && resolution.instance.InstanceID != "" &&
		resolution.instance.InstanceID == resolution.instanceID
}

func appendDependency(diagnostics []enrich.Diagnostic, dependency string) []enrich.Diagnostic {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == enrich.DiagnosticCodeDependencyInvalid && diagnostic.Dependency == dependency {
			return diagnostics
		}
	}
	return append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: dependency})
}

func statusForDiagnostics(diagnostics []enrich.Diagnostic, hasCoreResource bool) domain.EnrichStatus {
	if len(diagnostics) == 0 {
		return domain.EnrichStatusSucceeded
	}
	if hasCoreResource {
		return domain.EnrichStatusPartial
	}
	return domain.EnrichStatusFailed
}
