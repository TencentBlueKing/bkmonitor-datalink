// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/basetarget"
	"linkd/internal/lifecycle/enrich/collect"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
	uptimeenrich "linkd/internal/lifecycle/enrich/uptime"
)

// Resource 丰富告警关联的资源信息，实例定位统一读取 OneModel Elasticsearch。
type Resource struct{}

// Name 返回稳定的 Processor 名称。
func (Resource) Name() string { return rules.ResourceProcessor }

// Match 判断当前告警是否适用 Resource。
func (Resource) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 查询资源依赖并生成资源上下文。
func (Resource) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	alert := scope.Alert()
	ids, diagnostics := enrich.ValidateRequiredIDs(alert)
	if len(diagnostics) != 0 {
		value, err := resourceContextValue(scope, models.ResourceValues{})
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: value, Diagnostics: diagnostics}, nil
	}
	dimensions := alert.Dimensions.Clone()
	strategy, strategyFound, strategyErr := scope.CWStrategyByBKStrategyID(ctx, ids.StrategyID)
	if ctx.Err() != nil {
		return enrich.ProcessorResult{}, ctx.Err()
	}
	if strategyErr != nil || !strategyFound {
		return failedDependency(rules.DependencyKingeyeStrategy), nil
	}
	classification := rules.Classify(strategy, dimensions)
	if rules.IsLogDisplay(classification.Main) {
		return resourceScenarioResult(scope, models.ResourceValues{DynamicGroupID: []string{}, CWLabels: []string{}}, nil, false)
	}
	if classification.Main == rules.MainData {
		return processDataResource(ctx, scope, strategy, ids.BizID)
	}
	if strategy.ObjectModelCode == nil || *strategy.ObjectModelCode == "" {
		return failedDependency(rules.DependencyKingeyeStrategy), nil
	}
	if classification.BaseTarget == rules.BaseTargetUptimeCheck {
		result, err := uptimeenrich.Enrich(ctx, scope, *strategy.ObjectModelCode, dimensions, ids.BizID)
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
		return resourceScenarioResult(scope, result.Resource, result.ResourceDiagnostics, false)
	}
	if classification.BaseTarget == rules.BaseTargetCollectTask {
		result, err := collect.Enrich(ctx, scope, dimensions, ids.BizID, alert.SubjectName)
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
		return resourceScenarioResult(scope, result.Resource, result.ResourceDiagnostics, false)
	}
	result, err := basetarget.Enrich(ctx, scope, classification, strategy, dimensions, ids.BizID, alert.SubjectName)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	return resourceScenarioResult(scope, result.Resource, result.ResourceDiagnostics, classification.BaseTarget != rules.BaseTargetBasic)
}

// processDataResource 先迁移普通时序中有权威 MetricLibrary 模型、无实例身份的用例。
// 资源身份由指标库确定；没有已确认的实例定位字段时保留空 model_inst_id。
func processDataResource(ctx context.Context, scope *enrich.Scope, strategy models.CWStrategy, bizID int64) (enrich.ProcessorResult, error) {
	projection, err := strategy.StrategyItemProjection()
	if err != nil {
		return failedDependency(rules.DependencyKingeyeStrategy), nil
	}
	query := projection.QueryConfigs[0]
	if strings.HasPrefix(query.ResultTableID, "uptimecheck") {
		result, err := uptimeenrich.Enrich(ctx, scope, rules.UptimeModelCode, scope.Alert().Dimensions, bizID)
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
		return resourceScenarioResult(scope, result.Resource, result.ResourceDiagnostics, false)
	}
	if rules.IsAPMTable(query.ResultTableID) {
		return processAPMResource(ctx, scope, strategy, query, bizID)
	}
	modelCode := dataObjectModelCode(strategy, scope.Alert().Dimensions)
	metadata, found, readErr := scope.MetricLibrary(ctx, models.MetricLibraryQuery{TableID: query.ResultTableID, FieldName: query.MetricField, ObjectModelCode: modelCode})
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	if readErr != nil || !found || metadata.ObjectModelCode == "" {
		return failedDependency(rules.DependencyMetricLibrary), nil
	}
	if modelCode != "" && modelCode != metadata.ObjectModelCode {
		return failedDependency(rules.DependencyMetricLibrary), nil
	}
	values := models.ResourceValues{ModelID: metadata.ObjectModelCode, BKBizID: bizID, DynamicGroupID: []string{}}
	business, businessFound, businessErr := scope.Instance(ctx, enrich.InstanceQuery{ModelCode: "cw-biz", InstanceID: fmt.Sprint(bizID)})
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	if businessErr == nil && businessFound && business.TenantID == scope.Alert().BKTenantID && business.ModelCode == "cw-biz" && business.InstanceID == fmt.Sprint(bizID) {
		if name, ok := rules.FirstStringField(business.Attributes, rules.FieldBKBizName); ok {
			values.BKBizName = name
		}
	}
	model, modelFound, modelErr := scope.ModelByCode(ctx, values.ModelID)
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	diagnostics := []enrich.Diagnostic{}
	if modelErr != nil || !modelFound || model.TenantID != scope.Alert().BKTenantID || model.ModelCode != values.ModelID || model.ModelID == "" {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyOneModel})
	} else {
		enrich.ApplyModelContext(&values, model)
	}
	values.CWLabels = enrich.ResourceLabels(values)
	if values.ModelID == rules.HostModelCode && strings.HasPrefix(query.ResultTableID, rules.SystemTablePrefix) {
		return processDataHostResource(ctx, scope, values, scope.Alert().Dimensions)
	}
	if values.ModelID != rules.OtherModelCode && values.ModelID != rules.HostModelCode {
		if instanceID, provided, valid := dataInstanceIdentity(scope.Alert().Dimensions, values.ModelID); provided {
			if !valid {
				return resourceScenarioResult(scope, values, append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"dimensions.model_id", "dimensions.model_inst_id"}}), true)
			}
			return processDataModelInstance(ctx, scope, values, instanceID, diagnostics)
		}
		// 上游尚未提供 canonical 身份时保留模型与业务，等待 DATA 普通实例样例确认。
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeMissingField, Fields: []string{"dimensions.model_id", "dimensions.model_inst_id"}})
	}
	return resourceScenarioResult(scope, values, diagnostics, false)
}

func processAPMResource(ctx context.Context, scope *enrich.Scope, strategy models.CWStrategy, query models.StrategyQueryConfig, bizID int64) (enrich.ProcessorResult, error) {
	dimensions := scope.Alert().Dimensions
	appID := rules.DimensionText(dimensions, "apm_app_id")
	service := rules.DimensionText(dimensions, rules.FieldServiceName)
	instance := rules.DimensionText(dimensions, rules.FieldAPMInstanceID)
	modelID, modelInstID := apmModelIdentity(appID, service, instance)
	values := models.ResourceValues{ModelID: modelID, ModelInstID: modelInstID, BKInstID: appID, BKBizID: bizID, DynamicGroupID: []string{}}
	if appID == "" {
		return resourceScenarioResult(scope, values, []enrich.Diagnostic{{Code: enrich.DiagnosticCodeMissingField, Fields: []string{"dimensions.apm_app_id"}}}, true)
	}
	business, found, err := scope.Instance(ctx, enrich.InstanceQuery{ModelCode: "cw-biz", InstanceID: fmt.Sprint(bizID)})
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	if err == nil && found && business.TenantID == scope.Alert().BKTenantID && business.ModelCode == "cw-biz" && business.InstanceID == fmt.Sprint(bizID) {
		if name, ok := rules.FirstStringField(business.Attributes, rules.FieldBKBizName); ok {
			values.BKBizName = name
		}
	}
	values.CWLabels = enrich.ResourceLabels(values)
	return resourceScenarioResult(scope, values, nil, true)
}

func dataObjectModelCode(strategy models.CWStrategy, dimensions domain.DimensionMap) string {
	if strategy.ObjectModelCode != nil && *strategy.ObjectModelCode != "" {
		return *strategy.ObjectModelCode
	}
	value, ok := dimensions[rules.FieldModelID]
	if !ok {
		return ""
	}
	modelCode, ok := value.StringValue()
	if !ok {
		return ""
	}
	return modelCode
}

// dataInstanceIdentity 只接受显式 canonical 模型代码及非空、不透明的实例字符串，
// 避免将旧 Meta 主键或浮点 JSON 数字重解释为 OneModel 实例身份。
func dataInstanceIdentity(dimensions domain.DimensionMap, modelCode string) (string, bool, bool) {
	model, hasModel := dimensions[rules.FieldModelID]
	instance, hasInstance := dimensions[rules.FieldModelInstID]
	if !hasModel && !hasInstance {
		return "", false, false
	}
	code, codeOK := model.StringValue()
	id, idOK := instance.StringValue()
	if !hasModel || !hasInstance || !codeOK || code != modelCode || !idOK || id == "" {
		return "", true, false
	}
	return id, true, true
}

func processDataModelInstance(ctx context.Context, scope *enrich.Scope, values models.ResourceValues, instanceID string, diagnostics []enrich.Diagnostic) (enrich.ProcessorResult, error) {
	instance, found, readErr := scope.Instance(ctx, enrich.InstanceQuery{ModelCode: values.ModelID, InstanceID: instanceID})
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	if readErr != nil || !found || instance.TenantID != scope.Alert().BKTenantID || instance.ModelCode != values.ModelID || instance.InstanceID != instanceID {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyOneModel})
		return resourceScenarioResult(scope, values, diagnostics, true)
	}
	resource := enrich.ResourceValuesFromInstance(instance, bizIDFromResource(values))
	if values.ModelName != "" {
		resource.ModelName = values.ModelName
	}
	if values.BKObjID != nil {
		resource.BKObjID = values.BKObjID
	}
	resource.CWLabels = enrich.ResourceLabels(resource)
	return resourceScenarioResult(scope, resource, diagnostics, true)
}

// processDataHostResource 按 KAC DATA system 主机身份优先级解析资源；主键和地址查询
// 均由 OneModel Reader 做租户与 entity_uid 复核，实例未命中时保留已确认的模型与业务。
func processDataHostResource(ctx context.Context, scope *enrich.Scope, values models.ResourceValues, dimensions domain.DimensionMap) (enrich.ProcessorResult, error) {
	query, ok := dataHostQuery(dimensions)
	if !ok {
		return resourceScenarioResult(scope, values, []enrich.Diagnostic{{Code: enrich.DiagnosticCodeMissingField, Fields: []string{"dimensions.bk_target_host_id", "dimensions.bk_target_ip", "dimensions.bk_target_cloud_id"}}}, true)
	}
	instance, found, err := scope.Instance(ctx, query)
	if ctx.Err() != nil {
		return enrich.ProcessorResult{}, ctx.Err()
	}
	if err != nil || !found || instance.TenantID != scope.Alert().BKTenantID || instance.ModelCode != rules.HostModelCode || instance.InstanceID == "" || query.InstanceID != "" && query.InstanceID != instance.InstanceID {
		return resourceScenarioResult(scope, values, []enrich.Diagnostic{{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyOneModel}}, true)
	}
	resource := enrich.ResourceValuesFromInstance(instance, bizIDFromResource(values))
	resource.ModelName = values.ModelName
	resource.BKObjID = values.BKObjID
	resource.CWLabels = enrich.ResourceLabels(resource)
	return resourceScenarioResult(scope, resource, nil, true)
}

func bizIDFromResource(values models.ResourceValues) int64 {
	if id, ok := values.BKBizID.(int64); ok {
		return id
	}
	return 0
}

func dataHostQuery(dimensions domain.DimensionMap) (enrich.InstanceQuery, bool) {
	if value, ok := dimensions[rules.FieldBKTargetHostID]; ok {
		if id, valid := dataPositiveIdentity(value); valid {
			return enrich.InstanceQuery{ModelCode: rules.HostModelCode, InstanceID: id}, true
		}
	}
	ip, exists := dimensions[rules.FieldBKTargetIP]
	cloud, cloudExists := dimensions[rules.FieldBKTargetCloudID]
	ipText, ipValid := ip.StringValue()
	cloudID, cloudValid := dataCloudID(cloud)
	if !exists || !cloudExists || !ipValid || ipText == "" || !cloudValid {
		return enrich.InstanceQuery{}, false
	}
	return enrich.InstanceQuery{ModelCode: rules.HostModelCode, AttributeFilters: []enrich.InstanceAttributeFilter{
		{Field: rules.FieldBKHostInnerIP, Type: enrich.InstanceAttributeKeyword, Value: ipText},
		{Field: rules.FieldBKCloudID, Type: enrich.InstanceAttributeLong, Value: cloudID},
	}}, true
}

func dataPositiveIdentity(value domain.Scalar) (string, bool) {
	if number, ok := value.NumberValue(); ok && number > 0 && number < float64(math.MaxInt64) && math.Trunc(number) == number {
		return strconv.FormatInt(int64(number), 10), true
	}
	if text, ok := value.StringValue(); ok {
		id, err := strconv.ParseInt(text, 10, 64)
		return text, err == nil && id > 0 && strconv.FormatInt(id, 10) == text
	}
	return "", false
}

func dataCloudID(value domain.Scalar) (int64, bool) {
	if number, ok := value.NumberValue(); ok && number >= 0 && number < float64(math.MaxInt64) && math.Trunc(number) == number {
		return int64(number), true
	}
	if text, ok := value.StringValue(); ok {
		id, err := strconv.ParseInt(text, 10, 64)
		return id, err == nil && id >= 0 && strconv.FormatInt(id, 10) == text
	}
	return 0, false
}

func resourceScenarioResult(scope *enrich.Scope, values models.ResourceValues, diagnostics []enrich.Diagnostic, requiresInstance bool) (enrich.ProcessorResult, error) {
	value, err := resourceContextValue(scope, values)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	status := domain.EnrichStatusSucceeded
	if len(diagnostics) != 0 {
		status = domain.EnrichStatusPartial
		if requiresInstance && values.ModelInstID == "" {
			status = domain.EnrichStatusFailed
		}
	}
	return enrich.ProcessorResult{Status: status, Value: value, Diagnostics: diagnostics}, nil
}

func additionalDimensions(extraData domain.JSONObject) (domain.DimensionMap, error) {
	raw, exists := extraData["additional_dimensions"]
	if !exists {
		return domain.DimensionMap{}, nil
	}
	var additional domain.DimensionMap
	if err := json.Unmarshal(raw, &additional); err != nil {
		return nil, err
	}
	if err := additional.Validate(); err != nil {
		return nil, err
	}
	return additional, nil
}

func combinedDimensions(alert domain.Alert) (domain.DimensionMap, error) {
	combined := alert.Dimensions.Clone()
	additional, err := additionalDimensions(alert.ExtraData)
	if err != nil {
		return nil, err
	}
	for key, value := range additional {
		if _, exists := combined[key]; exists {
			return nil, fmt.Errorf("duplicate dimension key %q", key)
		}
		combined[key] = value
	}
	return combined, nil
}

func resourceContextValue(scope *enrich.Scope, values models.ResourceValues) (domain.JSONObject, error) {
	scope.Context().Resource.Set(values)
	return scope.Context().Resource.JSONObject()
}

func resourceValues(instance enrich.Instance, fallbackBizID int64) models.ResourceValues {
	fields := mergeInstanceFields(instance.Fields, instance.Attributes)
	values := models.ResourceValues{ModelID: instance.ModelCode, ModelInstID: instance.InstanceID}
	values.BKObjID, _ = rules.FirstField(fields, rules.FieldBKObjID)
	if value, exists := rules.FirstField(fields, rules.FieldModelName, rules.FieldObjectModelName, rules.FieldBKObjName); exists {
		values.ModelName = fmt.Sprint(value)
	}
	values.BKInstID, _ = rules.FirstField(fields, rules.FieldBKInstID, rules.FieldBKHostID)
	values.BKBizID, _ = rules.FirstField(fields, rules.FieldBKBizID)
	if values.BKBizID == nil {
		values.BKBizID = firstBusinessID(fields["bk_biz_ids"])
	}
	if values.BKBizID == nil {
		values.BKBizID = fallbackBizID
	}
	if value, exists := rules.FirstField(fields, rules.FieldBKBizName); exists {
		values.BKBizName = fmt.Sprint(value)
	}
	values.BKSetID, _ = rules.FirstField(fields, rules.FieldBKSetID)
	if value, exists := rules.FirstField(fields, rules.FieldBKSetName); exists {
		values.BKSetName = fmt.Sprint(value)
	}
	values.BKModuleID, _ = rules.FirstField(fields, rules.FieldBKModuleID)
	if value, exists := rules.FirstField(fields, rules.FieldBKModuleName); exists {
		values.BKModuleName = fmt.Sprint(value)
	}
	values.BKCloudID, _ = rules.FirstField(fields, rules.FieldBKCloudID)
	if value, exists := rules.FirstField(fields, rules.FieldBKCloudName); exists {
		values.BKCloudName = fmt.Sprint(value)
	}
	values.CloudPlatformID, _ = rules.FirstField(fields, rules.FieldCloudPlatformID)
	values.DynamicGroupID = []string{}
	values.CWLabels = []string{}
	return values
}

// firstBusinessID 仅在统一实例具有唯一业务归属时提供 bk_biz_id；多业务归属继续使用来源业务兜底。
func firstBusinessID(value any) any {
	switch values := value.(type) {
	case []any:
		if len(values) == 1 {
			return values[0]
		}
	case []int64:
		if len(values) == 1 {
			return values[0]
		}
	case []int:
		if len(values) == 1 {
			return values[0]
		}
	}
	return nil
}

// mergeInstanceFields 让统一实例根字段保持优先级，并补入 attributes 中的来源属性。
func mergeInstanceFields(root, attributes map[string]any) map[string]any {
	fields := make(map[string]any, len(root)+len(attributes))
	for key, value := range attributes {
		fields[key] = value
	}
	for key, value := range root {
		fields[key] = value
	}
	return fields
}
