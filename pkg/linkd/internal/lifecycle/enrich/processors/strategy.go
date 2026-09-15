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
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

const strategyConfigWebSaaSModuleURL = "web_saas_module_url"

// Strategy 丰富告警关联的策略信息。
type Strategy struct {
	webSaaSModuleURL string
}

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
		normalized, err := normalizeWebSaaSModuleURL(moduleURL)
		if err != nil {
			return Strategy{}, err
		}
		processor.webSaaSModuleURL = normalized
	}
	return processor, nil
}

// WebSaaSModuleURL 返回规范化后的 Web SaaS 模块基础地址。
func (p Strategy) WebSaaSModuleURL() string { return p.webSaaSModuleURL }

// Name 返回稳定的 Processor 名称。
func (Strategy) Name() string { return rules.StrategyProcessor }

// Match 判断当前告警是否适用 Strategy。
func (Strategy) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 查询策略依赖并生成策略信息。
func (p Strategy) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	alert := scope.Alert()
	ids, diagnostics := enrich.ValidateRequiredIDs(alert)
	if len(diagnostics) != 0 {
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: diagnostics}, nil
	}
	strategy, strategyFound, strategyErr := scope.CWStrategyByBKStrategyID(ctx, ids.StrategyID)
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
			if strategy.MonitorTemplateID != nil {
				if strategyURL, ok := p.buildStrategyURL(ctx, scope, strategy, configID, *strategy.MonitorTemplateID); ok {
					values.URL = strategyURL
				}
			}
		}
		values.StrategyName = strategy.Spec.Name
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

func strategyDependencyDiagnostics(strategyFound bool, strategyErr error) []enrich.Diagnostic {
	diagnostics := make([]enrich.Diagnostic, 0, 1)
	if strategyErr != nil || !strategyFound {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyKingeyeStrategy})
	}
	return diagnostics
}

func (p Strategy) buildStrategyURL(
	ctx context.Context,
	scope *enrich.Scope,
	strategy models.CWStrategy,
	strategyConfigID string,
	monitorStrategyID int64,
) (string, bool) {
	isCloudStrategy := strategy.Kind == models.CWStrategyKindCloud
	if isCloudStrategy || strategy.IsDefault != nil && *strategy.IsDefault {
		query := url.Values{
			"id":                 []string{fmt.Sprint(monitorStrategyID)},
			"strategy_config_id": []string{strategyConfigID},
		}
		return p.absoluteStrategyURL(rules.StrategyManagePath + query.Encode()), true
	}
	if strategy.ObjectModelCode == nil || *strategy.ObjectModelCode == "" {
		return "", false
	}
	queryInstance, diagnostics := resourceInstanceQuery(*strategy.ObjectModelCode, scope.Alert().Dimensions)
	if len(diagnostics) != 0 && queryInstance.InstanceID == "" && len(queryInstance.AttributeFilters) == 0 {
		return "", false
	}
	instance, found, err := scope.Instance(ctx, queryInstance)
	if err != nil || !found {
		return "", false
	}
	instanceFields := mergeInstanceFields(instance.Fields, instance.Attributes)
	bkObjID, ok := rules.HasTextField(instanceFields, rules.FieldBKObjID)
	if !ok {
		return "", false
	}
	classID, ok := fieldText(instanceFields, rules.FieldObjectModelGroupID)
	if !ok {
		return "", false
	}
	bkInstID := instance.InstanceID
	if value, exists := fieldText(instanceFields, rules.FieldBKInstID); exists {
		bkInstID = value
	} else if value, exists := fieldText(instanceFields, rules.FieldBKHostID); exists {
		bkInstID = value
	}
	query := url.Values{
		"bk_obj_id":         []string{bkObjID},
		"bk_inst_id":        []string{bkInstID},
		"strategy_id":       []string{fmt.Sprint(monitorStrategyID)},
		"strategy_item_id":  []string{strategyConfigID},
		"classId":           []string{classID},
		"object_model_code": []string{*strategy.ObjectModelCode},
	}
	return p.absoluteStrategyURL(rules.StrategyScenePath + query.Encode()), true
}

func (p Strategy) absoluteStrategyURL(path string) string {
	if p.webSaaSModuleURL == "" {
		return path
	}
	return strings.TrimSuffix(p.webSaaSModuleURL, "/") + path
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
