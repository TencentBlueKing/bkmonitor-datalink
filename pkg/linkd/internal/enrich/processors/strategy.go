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
	"fmt"
	"net/url"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

// 旧 EventSourceRelease 仍可能携带此键；只校验，不参与新丰富结果。
const strategyConfigWebSaaSModuleURL = "web_saas_module_url"

// Strategy 丰富告警关联的策略信息。
type Strategy struct{}

// NewStrategy 校验并创建 Strategy Processor。
func NewStrategy(config map[string]any) (Strategy, error) {
	processor := Strategy{}
	for key, value := range config {
		if key != strategyConfigWebSaaSModuleURL {
			return Strategy{}, fmt.Errorf("strategy processor config contains unknown field %q", key)
		}
		moduleURL, ok := value.(string)
		if !ok {
			return Strategy{}, fmt.Errorf("strategy processor config %s must be a string", key)
		}
		_, err := normalizeWebSaaSModuleURL(moduleURL)
		if err != nil {
			return Strategy{}, err
		}
	}
	return processor, nil
}

// Name 返回稳定的 Processor 名称。
func (Strategy) Name() string { return rules.StrategyProcessor }

// Match 判断当前告警是否适用 Strategy。
func (Strategy) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 查询策略依赖并生成策略信息。
func (p Strategy) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	alert := scope.Facts()
	ids, diagnostics := enrich.ValidateRequiredIDs(alert)
	if len(diagnostics) != 0 {
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: diagnostics}, nil
	}
	strategy, strategyFound, strategyErr := scope.CWStrategy(ctx)
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	diagnostics = strategyDependencyDiagnostics(strategyFound, strategyErr)
	values := models.StrategyValues{StrategyID: ids.StrategyID, StrategyVersion: ids.StrategyVersion}
	if strategyFound && strategyErr == nil {
		if strategy.MonitorTemplateID != nil {
			values.MonitorTemplateID = *strategy.MonitorTemplateID
		}
		if strategy.ConfigID != nil {
			configID := strings.ReplaceAll(*strategy.ConfigID, "-", "")
			values.StrategyConfigID = configID
		}
		values.StrategyKind = strategy.Kind
		if strategy.IsDefault != nil {
			isDefault := *strategy.IsDefault
			values.IsDefault = &isDefault
		}
		if strategy.Kind != models.CWStrategyKindCloud && values.IsDefault != nil && !*values.IsDefault {
			values.StrategyInstance, _ = strategyInstanceFacts(ctx, scope, strategy)
			if err := ctx.Err(); err != nil {
				return enrich.ProcessorResult{}, err
			}
		}
		values.StrategyName = legacyKACStrategyName(strategy)
		if values.StrategyName == "" {
			values.StrategyName = strategy.Name
		}
		values.DataSource = strategy.Spec.DataSource
	}
	scope.Context().Strategy.Set(values)
	value, err := scope.Context().Strategy.JSONObject()
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	businessMatches := true
	if strategyFound && strategyErr == nil && strategy.BKBizID != nil {
		businessMatches, err = strategyBusinessMatches(ctx, scope, *strategy.BKBizID, ids.BizID)
		if contextErr := ctx.Err(); contextErr != nil {
			return enrich.ProcessorResult{}, contextErr
		}
		if err != nil {
			diagnostics = append(diagnostics, enrich.Diagnostic{
				Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyBusiness,
			})
		}
	}
	status := domain.EnrichStatusSucceeded
	if len(diagnostics) != 0 || !businessMatches {
		status = domain.EnrichStatusPartial
		if !businessMatches {
			diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyKingeyeStrategy, Fields: []string{"labels.bk_biz_id"}})
		}
	}
	return enrich.ProcessorResult{Status: status, Value: value, Diagnostics: diagnostics}, nil
}

func legacyKACStrategyName(strategy models.CWStrategy) string {
	if strategy.MonitorTemplateName != "" {
		if strategy.Spec.StrategyItem != nil && len(strategy.Spec.StrategyItem.QueryConfigs) != 0 && strategy.Spec.StrategyItem.QueryConfigs[0].PromQL != "" {
			return strategy.MonitorTemplateName
		}
		if strategy.Spec.AliasName != "" {
			return strategy.MonitorTemplateName + "-" + strategy.Spec.AliasName
		}
		return strategy.MonitorTemplateName
	}
	return strategy.Spec.Name
}

func strategyDependencyDiagnostics(strategyFound bool, strategyErr error) []enrich.Diagnostic {
	diagnostics := make([]enrich.Diagnostic, 0, 1)
	if strategyErr != nil || !strategyFound {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyKingeyeStrategy})
	}
	return diagnostics
}

// strategyInstanceFacts 复用策略分类和租户作用域查询，冻结实例身份，供产品读取时组装导航。
func strategyInstanceFacts(ctx context.Context, scope *enrich.Scope, strategy models.CWStrategy) (*models.StrategyInstance, bool) {
	if strategy.ObjectModelCode == nil || *strategy.ObjectModelCode == "" {
		return nil, false
	}
	classification := rules.Classify(strategy, scope.Facts().Dimensions)
	queryInstance, ok := strategyInstanceQuery(classification.BaseTarget, *strategy.ObjectModelCode, scope.Facts().Dimensions)
	if !ok {
		return nil, false
	}
	instance, found, err := scope.Instance(ctx, queryInstance)
	if err != nil || !found {
		return nil, false
	}
	instanceFields := mergeInstanceFields(instance.Fields, instance.Attributes)
	bkObjID, ok := rules.HasTextField(instanceFields, rules.FieldBKObjID)
	if !ok {
		return nil, false
	}
	classID, ok := fieldText(instanceFields, rules.FieldObjectModelGroupID)
	if !ok {
		return nil, false
	}
	bkInstID := instance.InstanceID
	if value, exists := fieldText(instanceFields, rules.FieldBKInstID); exists {
		bkInstID = value
	} else if value, exists := fieldText(instanceFields, rules.FieldBKHostID); exists {
		bkInstID = value
	}
	return &models.StrategyInstance{
		ObjectModelCode: *strategy.ObjectModelCode, BKObjID: bkObjID, BKInstID: bkInstID, ObjectModelGroupID: classID,
	}, true
}

func strategyInstanceQuery(branch rules.BaseTargetBranch, modelCode string, dimensions domain.DimensionMap) (enrich.InstanceQuery, bool) {
	switch branch {
	case rules.BaseTargetMonitorSource:
		if value, exists := dimensions[rules.FieldBKInstID]; exists && rules.ScalarProvided(value) {
			return enrich.InstanceQuery{ModelCode: modelCode, InstanceID: rules.ScalarIdentity(value)}, true
		}
		modelValue, modelExists := dimensions[rules.FieldObjectModelID]
		instanceValue, instanceExists := dimensions[rules.FieldObjectModelInstID]
		if modelExists && instanceExists && rules.ScalarProvided(modelValue) && rules.ScalarProvided(instanceValue) {
			return enrich.InstanceQuery{ModelCode: rules.ScalarIdentity(modelValue), InstanceID: rules.ScalarIdentity(instanceValue)}, true
		}
	case rules.BaseTargetNoData:
		modelValue, modelExists := dimensions[rules.FieldModelID]
		instanceValue, instanceExists := dimensions[rules.FieldModelInstID]
		if modelExists && instanceExists && rules.ScalarIdentity(modelValue) == modelCode && rules.ScalarProvided(instanceValue) {
			return enrich.InstanceQuery{ModelCode: modelCode, InstanceID: rules.ScalarIdentity(instanceValue)}, true
		}
	case rules.BaseTargetSystemMetric:
		for _, field := range []string{rules.FieldBKInstID, rules.FieldBKHostID, rules.FieldBKTargetHostID} {
			if value, exists := dimensions[field]; exists && rules.ScalarProvided(value) {
				return enrich.InstanceQuery{ModelCode: modelCode, InstanceID: rules.ScalarIdentity(value)}, true
			}
		}
	}
	return enrich.InstanceQuery{}, false
}

func normalizeWebSaaSModuleURL(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("strategy processor config %s must be an absolute HTTP(S) URL without query or fragment", strategyConfigWebSaaSModuleURL)
	}
	return strings.TrimSuffix(value, "/") + "/", nil
}

func fieldText(fields map[string]any, name string) (string, bool) {
	value, exists := fields[name]
	if !exists || value == nil {
		return "", false
	}
	return fmt.Sprint(value), true
}
