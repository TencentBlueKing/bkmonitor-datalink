// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package uptime

import (
	"encoding/json"
	"fmt"
	"strconv"

	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

type resourceProjection struct {
	Values      models.ResourceValues
	Diagnostics []enrich.Diagnostic
}

func projectResource(resolution Resolution, fallbackBizID int64) resourceProjection {
	values := models.ResourceValues{
		ModelID: resolution.modelCode, DynamicGroupID: []string{}, CWLabels: []string{},
	}
	diagnostics := make([]enrich.Diagnostic, 0, 2)

	switch resolution.taskInputState {
	case taskInputMissing:
		diagnostics = append(diagnostics, enrich.Diagnostic{
			Code: enrich.DiagnosticCodeMissingField, Fields: []string{"dimensions." + rules.FieldTaskID},
		})
	case taskInputInvalid:
		diagnostics = append(diagnostics, enrich.Diagnostic{
			Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"dimensions." + rules.FieldTaskID},
		})
	case taskInputValid:
		switch {
		case resolution.taskErr != nil, !resolution.taskFound, !validTask(resolution):
			diagnostics = appendDependency(diagnostics, rules.DependencyUptime)
		default:
			values.ModelInstID = strconv.FormatInt(resolution.task.ID, 10)
			values.BKBizID = resolution.task.BKBizID
		}
	}

	if values.BKBizID == nil && fallbackBizID > 0 {
		values.BKBizID = fallbackBizID
	}
	if resolution.modelErr != nil || !resolution.modelFound || !validModel(resolution) {
		diagnostics = appendDependency(diagnostics, rules.DependencyOneModel)
	} else if name, ok := rules.HasTextField(resolution.model.Fields, rules.FieldObjectModelName); ok {
		values.ModelName = name
	}
	if resolution.businessErr == nil && resolution.businessFound && validBusiness(resolution) {
		if name, ok := rules.FirstStringField(resolution.business.Attributes, rules.FieldBKBizName); ok {
			values.BKBizName = name
		}
	}
	if resolution.cloudErr == nil && resolution.cloudFound && validCloudHost(resolution) {
		if name, ok := rules.FirstStringField(resolution.cloudHost.Attributes, rules.FieldBKCloudName); ok {
			if cloudID, valid := targetCloudIdentityFromHost(resolution.cloudHost); valid && cloudID == resolution.cloudID {
				values.BKCloudID, values.BKCloudName = cloudID, name
			}
		}
	}
	values.CWLabels = enrich.ResourceLabels(values)
	return resourceProjection{Values: values, Diagnostics: diagnostics}
}

func validBusiness(resolution Resolution) bool {
	if resolution.businessID <= 0 || resolution.business.TenantID != resolution.tenantID || resolution.business.ModelCode != "cw-biz" ||
		resolution.business.InstanceID != strconv.FormatInt(resolution.businessID, 10) {
		return false
	}
	if id, exists := resolution.business.Attributes[rules.FieldBKBizID]; exists {
		return strconv.FormatInt(resolution.businessID, 10) == fmt.Sprint(id)
	}
	return true
}

func validCloudHost(resolution Resolution) bool {
	if resolution.cloudHost.TenantID != resolution.tenantID || resolution.cloudHost.ModelCode != rules.HostModelCode || resolution.cloudHost.InstanceID == "" {
		return false
	}
	for _, fields := range []map[string]any{resolution.cloudHost.Fields, resolution.cloudHost.Attributes} {
		if ip, ok := fields[rules.FieldBKHostInnerIP].(string); ok && ip != "" {
			return ip == resolution.cloudAddress
		}
	}
	return false
}

func targetCloudIdentityFromHost(host enrich.Instance) (int64, bool) {
	for _, fields := range []map[string]any{host.Fields, host.Attributes} {
		if value, exists := fields[rules.FieldBKCloudID]; exists {
			switch typed := value.(type) {
			case json.Number:
				cloudID, err := typed.Int64()
				return cloudID, err == nil && cloudID >= 0
			case int64:
				return typed, typed >= 0
			case float64:
				return int64(typed), typed >= 0 && float64(int64(typed)) == typed
			case string:
				cloudID, err := strconv.ParseInt(typed, 10, 64)
				return cloudID, err == nil && cloudID >= 0
			}
		}
	}
	return 0, false
}

func validTask(resolution Resolution) bool {
	return resolution.task.ID > 0 && resolution.task.TaskID > 0 &&
		resolution.task.BKBizID >= 0 &&
		strconv.FormatInt(resolution.task.TaskID, 10) == resolution.taskIdentity &&
		resolution.task.BKTenantID == resolution.tenantID
}

func validModel(resolution Resolution) bool {
	return resolution.model.ModelID != "" && resolution.model.ModelCode == resolution.modelCode &&
		resolution.model.TenantID == resolution.tenantID
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
