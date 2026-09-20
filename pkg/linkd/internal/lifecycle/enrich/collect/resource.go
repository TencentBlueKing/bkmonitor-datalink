// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package collect

import (
	"fmt"

	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

// resourceProjection 是 Collect 对 enrich.resource 的完整贡献。
type resourceProjection struct {
	Values      models.ResourceValues
	Diagnostics []enrich.Diagnostic
}

func projectResource(resolution Resolution, fallbackBizID int64) resourceProjection {
	values := models.ResourceValues{DynamicGroupID: []string{}, CWLabels: []string{}}
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
		values.ModelInstID = fmt.Sprint(resolution.config.BKInstID)
		if resolution.instanceErr != nil || !resolution.instanceFound || !validInstance(resolution) {
			diagnostics = appendDependency(diagnostics, rules.DependencyOneModel)
			break
		}
		values = enrich.ResourceValuesFromInstance(resolution.instance, fallbackBizID)
		applyModelContext(&values, resolution)
		applyCloudHostContext(&values, resolution)
		applyServiceAndRemoteContext(&values, resolution)
		if resolution.topologyRequired && (resolution.relatedHostErr != nil || !resolution.relatedHostFound ||
			resolution.hostTopologyErr != nil || !resolution.hostTopologyFound) {
			diagnostics = appendDependency(diagnostics, rules.DependencyCollectTopology)
		}
		values.CWLabels = enrich.ResourceLabels(values)
		values.DynamicGroupID = dynamicGroupIDs(resolution)
	}
	return resourceProjection{Values: values, Diagnostics: diagnostics}
}

func applyModelContext(values *models.ResourceValues, resolution Resolution) {
	if resolution.modelErr != nil || !resolution.modelFound {
		return
	}
	enrich.ApplyModelContext(values, resolution.model)
}

func applyCloudHostContext(values *models.ResourceValues, resolution Resolution) {
	if !resolution.cloudHostLookup || resolution.cloudHostErr != nil || !resolution.cloudHostFound ||
		resolution.cloudHost.TenantID != resolution.tenantID || resolution.cloudHost.ModelCode != rules.HostModelCode {
		return
	}
	applyRelatedHostCloudContext(values, resolution.cloudHost)
}

func applyServiceAndRemoteContext(values *models.ResourceValues, resolution Resolution) {
	if resolution.config.FinalBizID != nil && *resolution.config.FinalBizID > 0 && values.BKBizID == nil {
		values.BKBizID = *resolution.config.FinalBizID
	}
	if !resolution.topologyRequired || resolution.relatedHostErr != nil || !resolution.relatedHostFound {
		return
	}
	// KAC 的云区域名称来自全局映射。OneModel 关联主机已携带同一 CMDB 属性，
	// 因此可在请求内直接补齐名称，避免为同一事实新增独立外部查询。
	applyRelatedHostCloudContext(values, resolution.relatedHost)
	if resolution.hostTopologyErr != nil || !resolution.hostTopologyFound {
		return
	}
	enrich.ApplyTopology(values, resolution.hostTopology)
}

func applyRelatedHostCloudContext(values *models.ResourceValues, host enrich.Instance) {
	fields := make(map[string]any, len(host.Fields)+len(host.Attributes))
	for key, value := range host.Attributes {
		fields[key] = value
	}
	for key, value := range host.Fields {
		fields[key] = value
	}
	hostCloudID, found := rules.FirstField(fields, rules.FieldBKCloudID)
	if !found || hostCloudID == nil {
		return
	}
	// 保留资源实例自身的云区域身份；关联主机只为相同 ID 补名称。
	// 将不同主机的名称拼到资源 ID 上会产生错误的云区域展示。
	if values.BKCloudID != nil && fmt.Sprint(values.BKCloudID) != fmt.Sprint(hostCloudID) {
		return
	}
	if values.BKCloudID == nil {
		values.BKCloudID = hostCloudID
	}
	if values.BKCloudName == "" {
		if value, found := rules.FirstField(fields, rules.FieldBKCloudName); found && value != nil {
			name := fmt.Sprint(value)
			if name != "" && name != "<nil>" {
				values.BKCloudName = name
			}
		}
	}
}

// dynamicGroupIDs 是动态分组的稳定投影点。Redis key 与租户连接映射尚待实现，
// 当前返回已确认的合法空列表；接入后保持 ResourceProjector 调用点不变。
func dynamicGroupIDs(Resolution) []string { return []string{} }

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
