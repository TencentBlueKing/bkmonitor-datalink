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

// Strategy 丰富告警关联的策略信息。
type Strategy struct{}

// Name 返回稳定的 Processor 名称。
func (Strategy) Name() string { return rules.StrategyProcessor }

// Match 判断当前告警是否适用 Strategy。
func (Strategy) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 查询策略依赖并生成策略信息。
func (Strategy) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	alert := scope.Alert()
	ids, diagnostics := enrich.ValidateRequiredIDs(alert)
	if len(diagnostics) != 0 {
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: diagnostics}, nil
	}
	history, historyFound, historyErr := scope.BKStrategyHistory(ctx, ids.StrategyID, ids.HistoryID)
	strategy, strategyFound, strategyErr := scope.CWStrategyByBKStrategyID(ctx, ids.StrategyID)
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	var snapshot models.BkStrategySnapshot
	if historyFound && historyErr == nil {
		snapshot, historyErr = history.Snapshot()
		if historyErr != nil {
			historyFound = false
		}
	}
	diagnostics = strategyDependencyDiagnostics(historyFound, historyErr, strategyFound, strategyErr)
	values := models.StrategyValues{BKStrategyID: ids.StrategyID}
	if strategyFound && strategyErr == nil {
		if strategy.MonitorTemplateID != nil {
			values.MonitorTemplateID = *strategy.MonitorTemplateID
		}
		if strategy.ConfigID != nil {
			configID := strings.ReplaceAll(*strategy.ConfigID, "-", "")
			values.StrategyConfigID = configID
			if strategy.MonitorTemplateID != nil {
				if strategyURL, ok := buildStrategyURL(ctx, scope, strategy, configID, *strategy.MonitorTemplateID); ok {
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
	status := domain.EnrichStatusSucceeded
	if len(diagnostics) != 0 || (historyFound && snapshot.BKBizID != ids.BizID) {
		status = domain.EnrichStatusPartial
		if historyFound && snapshot.BKBizID != ids.BizID {
			diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyPlatformStrategyHistory, Fields: []string{"labels.bk_biz_id"}})
		}
	}
	return enrich.ProcessorResult{Status: status, Value: value, Diagnostics: diagnostics}, nil
}

func strategyDependencyDiagnostics(historyFound bool, historyErr error, strategyFound bool, strategyErr error) []enrich.Diagnostic {
	diagnostics := make([]enrich.Diagnostic, 0, 2)
	if historyErr != nil || !historyFound {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyPlatformStrategyHistory})
	}
	if strategyErr != nil || !strategyFound {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyKingeyeStrategy})
	}
	return diagnostics
}

// TODO 当前只生成站内相对路径。完成条件：从 Linkd 配置或部署环境读取旧
// settings.KINGEYE_WEB_SAAS_MODULE_URL 对应的 Web SaaS 基础地址，经校验和规范化后
// 注入 Strategy Processor，并为默认、云和实例策略生成完整绝对 URL。
func buildStrategyURL(
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
		return rules.StrategyManagePath + query.Encode(), true
	}
	if strategy.ObjectModelCode == nil || *strategy.ObjectModelCode == "" {
		return "", false
	}
	queryInstance, diagnostics := resourceInstanceQuery(*strategy.ObjectModelCode, scope.Alert().Dimensions)
	if len(diagnostics) != 0 && len(queryInstance.Filters) == 0 {
		return "", false
	}
	instance, found, err := scope.Instance(ctx, queryInstance)
	if err != nil || !found {
		return "", false
	}
	bkObjID, ok := rules.HasTextField(instance.Fields, rules.FieldBKObjID)
	if !ok {
		return "", false
	}
	classID, ok := fieldText(instance.Fields, rules.FieldObjectModelGroupID)
	if !ok {
		return "", false
	}
	bkInstID := instance.InstanceID
	if value, exists := fieldText(instance.Fields, rules.FieldBKInstID); exists {
		bkInstID = value
	} else if value, exists := fieldText(instance.Fields, rules.FieldBKHostID); exists {
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
	return rules.StrategyScenePath + query.Encode(), true
}

func fieldText(fields map[string]any, name string) (string, bool) {
	value, exists := fields[name]
	if !exists || value == nil {
		return "", false
	}
	return fmt.Sprint(value), true
}
