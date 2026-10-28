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
	"strconv"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/basetarget"
	"linkd/internal/enrich/collect"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
	uptimeenrich "linkd/internal/enrich/uptime"
)

// MonitorResource 保留监控场景的资源推断、拓扑及分组补充；通用 Resource 不依赖这些监控契约。
type MonitorResource struct{}

// Name 返回稳定的 Processor 名称。
func (MonitorResource) Name() string { return rules.MonitorResourceProcessor }

// Match 判断当前告警是否适用 Resource。
func (MonitorResource) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 查询资源依赖并生成资源上下文。
func (MonitorResource) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	result, err := processResource(ctx, scope)
	if err != nil {
		return result, err
	}
	values := scope.Context().Resource.Values
	// 主资源失败时保留原错误与独立业务补偿；不能用主机查询掩盖失败，
	// 也不能发布缺省空对象字段覆盖来源身份。
	if result.Status != domain.EnrichStatusFailed {
		hostDiagnostics, hostErr := resourceHostContext(ctx, scope, &values)
		if hostErr != nil {
			return enrich.ProcessorResult{}, hostErr
		}
		result.Diagnostics = append(result.Diagnostics, hostDiagnostics...)
		if len(hostDiagnostics) != 0 && result.Status == domain.EnrichStatusSucceeded {
			result.Status = domain.EnrichStatusPartial
		}
		result.Value, err = resourceContextValue(scope, values)
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
	}
	// 旧 KAC 的业务名称映射独立于实例读取。保留已确认业务 ID，
	// 只在名称缺失时按同租户 cw-biz 身份补偿，不用策略的全局业务覆盖资源业务。
	if id := bizIDFromResource(values); values.ModelID != "" && values.BKBizName == "" && id > 0 {
		businessID := strconv.FormatInt(id, 10)
		business, found, readErr := scope.Instance(ctx, enrich.InstanceQuery{ModelCode: "cw-biz", InstanceID: businessID})
		if ctx.Err() != nil {
			return enrich.ProcessorResult{}, ctx.Err()
		}
		if readErr != nil {
			result.Status = domain.EnrichStatusPartial
			result.Diagnostics = appendResourceDependency(result.Diagnostics, rules.DependencyOneModel)
		} else if found && business.TenantID == scope.Facts().BKTenantID && business.ModelCode == "cw-biz" && business.InstanceID == businessID {
			fields := mergeInstanceFields(business.Fields, business.Attributes)
			if name, ok := rules.FirstStringField(fields, rules.FieldBKBizName, rules.FieldBKInstName, "display_name"); ok {
				values.BKBizName = name
				if result.Status == domain.EnrichStatusFailed {
					result.Status = domain.EnrichStatusPartial
				}
			}
		}
		result.Value, err = resourceContextValue(scope, values)
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
	}
	if result.Status == domain.EnrichStatusFailed || values.ModelID == "" || values.ModelInstID == "" {
		return result, nil
	}
	groupIDs, err := scope.DynamicGroupIDs(ctx, values.ModelID, values.ModelInstID)
	if ctx.Err() != nil {
		return enrich.ProcessorResult{}, ctx.Err()
	}
	if err != nil {
		// 资源身份已确定时，动态分组投影失败仅使该 Processor 部分成功。
		result.Status = domain.EnrichStatusPartial
		result.Diagnostics = append(result.Diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyDynamicGroup})
		return result, nil
	}
	values.DynamicGroupID = groupIDs
	result.Value, err = resourceContextValue(scope, values)
	return result, err
}

func appendResourceDependency(diagnostics []enrich.Diagnostic, dependency string) []enrich.Diagnostic {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == enrich.DiagnosticCodeDependencyInvalid && diagnostic.Dependency == dependency {
			return diagnostics
		}
	}
	return append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: dependency})
}

func processResource(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	alert := scope.Facts()
	_, diagnostics := enrich.ValidateStrategyIdentity(alert)
	bizID := enrich.OptionalBusinessID(alert)
	if len(diagnostics) != 0 {
		value, err := resourceContextValue(scope, models.ResourceValues{})
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: value, Diagnostics: diagnostics}, nil
	}
	dimensions := alert.Dimensions.Clone()
	strategy, strategyFound, strategyErr := scope.CWStrategy(ctx)
	if ctx.Err() != nil {
		return enrich.ProcessorResult{}, ctx.Err()
	}
	if strategyErr != nil || !strategyFound {
		return failedDependency(rules.DependencyKingeyeStrategy), nil
	}
	classification := rules.Classify(strategy, dimensions)
	if rules.IsLogDisplay(classification.Main) {
		return resourceScenarioResult(scope, models.ResourceValues{BKBizID: bizID, DynamicGroupID: []string{}, CWLabels: []string{}}, nil, false)
	}
	if classification.Main == rules.MainData {
		return processDataResource(ctx, scope, strategy, bizID)
	}
	if strategy.ObjectModelCode == nil || *strategy.ObjectModelCode == "" {
		return failedDependency(rules.DependencyKingeyeStrategy), nil
	}
	if classification.BaseTarget == rules.BaseTargetNoData {
		_, knownK8s := k8sIdentityFields[*strategy.ObjectModelCode]
		_, hasModel := dimensions[rules.FieldModelID]
		_, hasInstance := dimensions[rules.FieldModelInstID]
		if knownK8s && !hasModel && !hasInstance {
			return processK8sResource(ctx, scope, *strategy.ObjectModelCode, bizID)
		}
	}
	if classification.BaseTarget == rules.BaseTargetUptimeCheck {
		result, err := uptimeenrich.Enrich(ctx, scope, *strategy.ObjectModelCode, dimensions, bizID)
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
		return resourceScenarioResult(scope, result.Resource, result.ResourceDiagnostics, false)
	}
	if classification.BaseTarget == rules.BaseTargetCollectTask {
		result, err := collect.Enrich(ctx, scope, dimensions, bizID, alert.SubjectName)
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
		return resourceScenarioResult(scope, result.Resource, result.ResourceDiagnostics, false)
	}
	result, err := basetarget.Enrich(ctx, scope, classification, strategy, dimensions, bizID, alert.SubjectName)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	return resourceScenarioResult(scope, result.Resource, result.ResourceDiagnostics, classification.BaseTarget != rules.BaseTargetBasic)
}

// processDataResource 先迁移普通时序中有权威指标目录模型、无实例身份的用例。
// 资源身份由指标库确定；没有已确认的实例定位字段时保留空 model_inst_id。
func processDataResource(ctx context.Context, scope *enrich.Scope, strategy models.CWStrategy, bizID int64) (enrich.ProcessorResult, error) {
	projection, err := strategy.StrategyItemProjection()
	if err != nil {
		return failedDependency(rules.DependencyKingeyeStrategy), nil
	}
	query := projection.QueryConfigs[0]
	if strings.HasPrefix(query.ResultTableID, "uptimecheck") {
		result, err := uptimeenrich.Enrich(ctx, scope, rules.UptimeModelCode, scope.Facts().Dimensions, bizID)
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
		return resourceScenarioResult(scope, result.Resource, result.ResourceDiagnostics, false)
	}
	if rules.IsAPMQuery(query) {
		return processAPMResource(ctx, scope, strategy, query, bizID)
	}
	modelCode := dataObjectModelCode(strategy, scope.Facts().Dimensions)
	metadata, found, readErr := metricDefinitionMetadata(ctx, scope, strategy, query, modelCode)
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	if readErr != nil {
		return failedDependency(rules.DependencyMetricCatalog), nil
	}
	catalogModel := ""
	if found {
		catalogModel = metadata.ObjectModelCode
	}
	// 普通时序必须由指标目录确认对象模型；PromQL 声明可以没有目录模型，
	// 目录只在能可靠定位时补充。
	if !query.IsPromQL() && (!found || catalogModel == "") {
		return failedDependency(rules.DependencyMetricCatalog), nil
	}
	if modelCode == "" {
		modelCode = catalogModel
	}
	if modelCode == "" && query.IsPromQL() {
		// 目录不可用时只能用事件已确认的身份，不能根据表达式或表名猜测模型。
		modelCode = promQLSubjectModelCode(scope.Facts())
	}
	if catalogModel != "" && catalogModel != modelCode {
		return failedDependency(rules.DependencyMetricCatalog), nil
	}
	values := models.ResourceValues{ModelID: modelCode, BKBizID: bizID, DynamicGroupID: []string{}}
	if modelCode == "" {
		// PromQL 无法确定对象模型时保留业务与来源，让展示和指标继续生成，
		// 不把目录未命中或身份缺口报告成目录故障。
		values.CWLabels = enrich.ResourceLabels(values)
		return resourceScenarioResult(scope, values, []enrich.Diagnostic{{
			Code: enrich.DiagnosticCodeMissingField, Fields: []string{"dimensions." + rules.FieldModelID},
		}}, false)
	}
	var business enrich.Instance
	var businessFound bool
	var businessErr error
	if bizID > 0 {
		business, businessFound, businessErr = scope.Instance(ctx, enrich.InstanceQuery{ModelCode: "cw-biz", InstanceID: fmt.Sprint(bizID)})
	}
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	if businessErr == nil && businessFound && business.TenantID == scope.Facts().BKTenantID && business.ModelCode == "cw-biz" && business.InstanceID == fmt.Sprint(bizID) {
		if name, ok := rules.FirstStringField(business.Attributes, rules.FieldBKBizName); ok {
			values.BKBizName = name
		}
	}
	model, modelFound, modelErr := scope.ModelByCode(ctx, values.ModelID)
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	diagnostics := []enrich.Diagnostic{}
	if modelErr != nil || !modelFound || model.TenantID != scope.Facts().BKTenantID || model.ModelCode != values.ModelID || model.ModelID == "" {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyOneModel})
	} else {
		enrich.ApplyModelContext(&values, model)
	}
	values.CWLabels = enrich.ResourceLabels(values)
	// 目录查询可以只保存 metric_ref_id 而没有物理表名；主机身份已由目录
	// 确认后统一解析实例，不能再用声明表名筛掉合法的主机事件。
	if values.ModelID == rules.HostModelCode {
		return processDataHostResource(ctx, scope, values, scope.Facts().Dimensions)
	}
	if _, knownK8s := k8sIdentityFields[values.ModelID]; knownK8s {
		_, hasModel := scope.Facts().Dimensions[rules.FieldModelID]
		_, hasInstance := scope.Facts().Dimensions[rules.FieldModelInstID]
		if !hasModel && !hasInstance {
			return processK8sResource(ctx, scope, values.ModelID, bizID)
		}
	}
	if values.ModelID != rules.OtherModelCode && values.ModelID != rules.HostModelCode {
		if instanceID, provided, valid := dataInstanceIdentity(scope.Facts().Dimensions, values.ModelID); provided {
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

// K8s 使用集群、命名空间、node/pod 等原生身份；无数据标记不能使这些
// 事件额外依赖整数模型实例 ID。Resource 与 K8s Processor 共用 Scope 的查询结果。
func processK8sResource(ctx context.Context, scope *enrich.Scope, code string, bizID int64) (enrich.ProcessorResult, error) {
	base, err := basetarget.Enrich(ctx, scope, rules.Classification{BaseTarget: rules.BaseTargetBasic}, models.CWStrategy{ObjectModelCode: &code}, scope.Facts().Dimensions, bizID, scope.Facts().SubjectName)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	values, diagnostics := base.Resource, base.ResourceDiagnostics
	if missing := k8sMissingIdentityFields(code, scope.Facts().Dimensions); len(missing) != 0 {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeMissingField, Fields: missing})
		return resourceScenarioResult(scope, values, diagnostics, true)
	}
	instance, found, readErr := scope.K8sInstance(ctx, code, scope.Facts().Dimensions)
	if ctx.Err() != nil {
		return enrich.ProcessorResult{}, ctx.Err()
	}
	if readErr != nil || !found || instance.TenantID != scope.Facts().BKTenantID || instance.ModelCode != code || instance.InstanceID == "" {
		return resourceScenarioResult(scope, values, appendResourceDependency(diagnostics, rules.DependencyOneModel), true)
	}
	resource := enrich.ResourceValuesFromInstance(instance, bizID)
	resource.ModelName, resource.BKObjID = values.ModelName, values.BKObjID
	resource.CWLabels = enrich.ResourceLabels(resource)
	return resourceScenarioResult(scope, resource, diagnostics, true)
}

func processAPMResource(ctx context.Context, scope *enrich.Scope, strategy models.CWStrategy, query models.StrategyQueryConfig, bizID int64) (enrich.ProcessorResult, error) {
	dimensions, err := combinedDimensions(scope.Facts())
	if err != nil {
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: []enrich.Diagnostic{{Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"extra_data.additional_dimensions"}}}}, nil
	}
	application, diagnostics, err := resolveAPMApplication(ctx, scope, strategy, query.ResultTableID, bizID)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	bizID = application.BKBizID
	service := rules.DimensionText(dimensions, rules.FieldServiceName)
	instance := rules.DimensionText(dimensions, rules.FieldAPMInstanceID)
	modelID, modelInstID := apmModelIdentity(application.ID, service, instance)
	values := models.ResourceValues{ModelID: modelID, ModelInstID: modelInstID, BKInstID: apmIDValue(application.ID), BKBizID: bizID, DynamicGroupID: []string{}}
	if application.ID == "" {
		return resourceScenarioResult(scope, values, diagnostics, true)
	}
	// APM 身份由应用维度确定，展示名称仍读取同租户的模型注册表，不能因跳过
	// 普通 DATA 资源分支而遗漏；模型元数据失败时保留已确认身份并标记部分成功。
	model, modelFound, modelErr := scope.ModelByCode(ctx, modelID)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return enrich.ProcessorResult{}, ctxErr
	}
	modelName, hasModelName := rules.FirstStringField(model.Fields, rules.FieldObjectModelName)
	if modelErr != nil || !modelFound || model.TenantID != scope.Facts().BKTenantID || model.ModelCode != modelID || model.ModelID == "" || !hasModelName {
		diagnostics = appendResourceDependency(diagnostics, rules.DependencyOneModel)
	} else {
		values.ModelName = modelName
	}
	business, found, readErr := scope.Instance(ctx, enrich.InstanceQuery{ModelCode: "cw-biz", InstanceID: fmt.Sprint(bizID)})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return enrich.ProcessorResult{}, ctxErr
	}
	if readErr == nil && found && business.TenantID == scope.Facts().BKTenantID && business.ModelCode == "cw-biz" && business.InstanceID == fmt.Sprint(bizID) {
		if name, ok := rules.FirstStringField(business.Attributes, rules.FieldBKBizName); ok {
			values.BKBizName = name
		}
	}
	values.CWLabels = apmLabels(bizID, application.ID)
	return resourceScenarioResult(scope, values, diagnostics, true)
}

// promQLSubjectModelCode 从事件里已确认的 CMDB 主机 Subject 推导对象模型。
// PromQL 声明没有对象模型时，这是唯一可不依赖指标目录得到的资源模型；
// 其他 Subject 类型留给后续单独确认的定位规则。
func promQLSubjectModelCode(event domain.Event) string {
	if event.SubjectSystem == "cmdb" && event.SubjectType == "host" && event.SubjectID != "" {
		return rules.HostModelCode
	}
	return ""
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
	if readErr != nil || !found || instance.TenantID != scope.Facts().BKTenantID || instance.ModelCode != values.ModelID || instance.InstanceID != instanceID {
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

// processDataHostResource 复用目标类主机的实例、模型和拓扑补偿；
// 指标目录已确认模型为主机，不能因 DATA 分类而跳过相同资源的降级边界。
func processDataHostResource(ctx context.Context, scope *enrich.Scope, values models.ResourceValues, dimensions domain.DimensionMap) (enrich.ProcessorResult, error) {
	code := rules.HostModelCode
	if _, provided, valid := dataInstanceIdentity(dimensions, code); provided && !valid {
		return resourceScenarioResult(scope, values, []enrich.Diagnostic{{Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"dimensions.model_id", "dimensions.model_inst_id"}}}, true)
	}
	branch := rules.BaseTargetSystemMetric
	if rules.ScalarProvided(dimensions[rules.FieldNoDataDimension]) {
		branch = rules.BaseTargetNoData
	} else if _, hasModel := dimensions[rules.FieldModelID]; hasModel {
		branch = rules.BaseTargetMonitorSource
	} else if _, hasInstance := dimensions[rules.FieldModelInstID]; hasInstance {
		branch = rules.BaseTargetMonitorSource
	}
	result, err := basetarget.Enrich(ctx, scope, rules.Classification{Main: rules.MainData, BaseTarget: branch}, models.CWStrategy{ObjectModelCode: &code}, dimensions, bizIDFromResource(values), scope.Facts().SubjectName)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	if result.Resource.BKBizName == "" && fmt.Sprint(result.Resource.BKBizID) == fmt.Sprint(values.BKBizID) {
		result.Resource.BKBizName = values.BKBizName
	}
	return resourceScenarioResult(scope, result.Resource, result.ResourceDiagnostics, true)
}

func bizIDFromResource(values models.ResourceValues) int64 {
	id, _ := strconv.ParseInt(fmt.Sprint(values.BKBizID), 10, 64)
	return id
}

func resourceScenarioResult(scope *enrich.Scope, values models.ResourceValues, diagnostics []enrich.Diagnostic, requiresInstance bool) (enrich.ProcessorResult, error) {
	value, err := resourceContextValue(scope, values)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	status := domain.EnrichStatusSucceeded
	if len(diagnostics) != 0 {
		status = domain.EnrichStatusPartial
		// 已确认模型的名称/CMDB 映射可以独立输出。实例缺失仍保留诊断，
		// 但不能让 failed 丢弃这些补丁，也不能伪造实例绑定。
		if requiresInstance && values.ModelInstID == "" && values.ModelName == "" && values.BKObjID == nil {
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

// combinedDimensions 返回独立的完整维度视图；APM 可以仅携带附加维度，
// 因此缺失 dimensions 也须能合并，且不得回写原始事件或静默覆盖重名键。
func combinedDimensions(alert domain.Event) (domain.DimensionMap, error) {
	combined := alert.Dimensions.Normalize()
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
