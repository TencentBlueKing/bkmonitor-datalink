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
	"strings"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

// APM 丰富 APM 应用、服务、服务实例和接口维度。
type APM struct{}

func (APM) Name() string { return rules.APMProcessor }

func (APM) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

func (APM) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	alert := scope.Facts()
	_, diagnostics := enrich.ValidateStrategyIdentity(alert)
	if len(diagnostics) != 0 {
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: diagnostics}, nil
	}
	strategy, found, err := scope.CWStrategy(ctx)
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	if err != nil || !found {
		return failedDependency(rules.DependencyKingeyeStrategy), nil
	}
	projection, err := strategy.StrategyItemProjection()
	if err != nil || !rules.IsAPMQuery(projection.QueryConfigs[0]) {
		return enrich.ProcessorResult{Status: domain.EnrichStatusSkipped, Value: domain.JSONObject{}}, nil
	}
	bizID, diagnostics := enrich.ValidateBusinessID(alert)
	if len(diagnostics) != 0 {
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: diagnostics}, nil
	}
	dimensions, err := combinedDimensions(alert)
	if err != nil {
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: []enrich.Diagnostic{{Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"extra_data.additional_dimensions"}}}}, nil
	}
	application, applicationDiagnostics, err := resolveAPMApplication(ctx, scope, strategy, projection.QueryConfigs[0].ResultTableID, bizID)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	diagnostics = append(diagnostics, applicationDiagnostics...)
	bizID = application.BKBizID
	service := dimensionText(dimensions, rules.FieldServiceName)
	instance := dimensionText(dimensions, rules.FieldAPMInstanceID)
	modelID, modelInstID := apmModelIdentity(application.ID, service, instance)
	values := models.APMValues{APMAppID: apmIDValue(application.ID), APMAppName: application.Name, APMAppAlias: application.Alias, APMServiceName: service, APMInstanceName: instance, APMInterfaceName: dimensionText(dimensions, rules.FieldAPMSpanName), APMNetPeerName: dimensionText(dimensions, rules.FieldAPMNetPeerName), ModelID: modelID, ModelInstID: modelInstID, BKBizID: bizID}
	if application.ID != "" {
		values.CWLabels = apmLabels(bizID, application.ID)
	}
	scope.Context().APM.Set(values)
	value, encodeErr := scope.Context().APM.JSONObject()
	if encodeErr != nil {
		return enrich.ProcessorResult{}, encodeErr
	}
	status := domain.EnrichStatusSucceeded
	if len(diagnostics) != 0 {
		status = domain.EnrichStatusPartial
	}
	return enrich.ProcessorResult{Status: status, Value: value, Diagnostics: diagnostics}, nil
}

func apmLabels(bizID int64, appID string) []string {
	return []string{"bk_biz_id", fmt.Sprintf("bk_biz_id|%d", bizID), "apm_app_id", "apm_app_id|" + appID}
}

type apmApplicationProjection struct {
	ID      string
	Name    string
	Alias   string
	BKBizID int64
}

// resolveAPMApplication 使用 dimensions 或 additional_dimensions 中的 app_name
// 定位已确认业务范围内的 APM 应用；两处维度冲突时不得任选其一。
// 全局业务在同租户内要求名称唯一，资源业务取自命中的应用记录。
// 旧 dimensions.apm_app_id 仅保留为兼容回退。
func resolveAPMApplication(ctx context.Context, scope *enrich.Scope, strategy models.CWStrategy, tableID string, bizID int64) (apmApplicationProjection, []enrich.Diagnostic, error) {
	alert := scope.Facts()
	projection := apmApplicationProjection{ID: dimensionText(alert.Dimensions, rules.FieldAPMAppID), BKBizID: bizID}
	diagnostics := []enrich.Diagnostic{}
	dimensions, err := combinedDimensions(alert)
	if err != nil {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"extra_data.additional_dimensions"}})
	} else {
		projection.Name = dimensionText(dimensions, "app_name")
	}
	if projection.Name == "" {
		projection.Name = apmApplicationName(strategy, tableID)
	}
	// 先验证业务身份，再明确查询范围；不能把缺失业务当成全局业务。
	if bizID <= 0 {
		_, businessDiagnostics := enrich.ValidateBusinessID(alert)
		return projection, append(diagnostics, businessDiagnostics...), nil
	}
	global, businessFound, businessErr := scope.IsGlobalBusiness(ctx, bizID)
	if err := ctx.Err(); err != nil {
		return apmApplicationProjection{}, nil, err
	}
	if businessErr != nil || !businessFound {
		projection.ID, projection.BKBizID = "", 0
		return projection, append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyBusiness}), nil
	}
	if global {
		// 全局业务只界定查询范围，不能作为应用所属业务写回资源。
		projection.ID, projection.BKBizID = "", 0
	}
	if projection.Name == "" {
		if projection.ID == "" {
			diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeMissingField, Fields: []string{"dimensions.app_name", "extra_data.additional_dimensions.app_name"}})
		}
		return projection, diagnostics, nil
	}
	apps, readErr := scope.APMApplications(ctx, bizID, projection.Name, global)
	if err := ctx.Err(); err != nil {
		return apmApplicationProjection{}, nil, err
	}
	if readErr != nil {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyAPMApplication})
		return projection, diagnostics, nil
	}
	if global && len(apps) > 1 {
		return projection, append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyAPMApplication}), nil
	}
	for _, app := range apps {
		if app.TenantID != alert.BKTenantID || app.BKBizID <= 0 || (!global && app.BKBizID != bizID) || app.Name != projection.Name || app.ID <= 0 {
			continue
		}
		projection.ID = fmt.Sprint(app.ID)
		projection.Alias = app.Alias
		projection.BKBizID = app.BKBizID
		return projection, diagnostics, nil
	}
	diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyAPMApplication})
	return projection, diagnostics, nil
}

func apmApplicationName(strategy models.CWStrategy, tableID string) string {
	if strings.HasPrefix(strategy.Spec.DataSource, "应用-") {
		return strings.TrimPrefix(strategy.Spec.DataSource, "应用-")
	}
	if index := strings.Index(tableID, "_bkapm_metric_"); index >= 0 {
		value := tableID[index+len("_bkapm_metric_"):]
		if dot := strings.LastIndex(value, "."); dot >= 0 {
			value = value[:dot]
		}
		return value
	}
	return ""
}

func apmIDValue(id string) any {
	if id == "" {
		return nil
	}
	var value int64
	if _, err := fmt.Sscan(id, &value); err == nil {
		return value
	}
	return id
}

func apmModelIdentity(appID, service, instance string) (string, string) {
	if appID == "" || appID == "0" {
		return "", ""
	}
	if service != "" && instance != "" {
		return rules.APMServiceInstanceModelCode, strings.Join([]string{appID, service, instance}, "|")
	}
	if service != "" {
		return rules.APMServiceModelCode, appID + "|" + service
	}
	return rules.APMApplicationModelCode, appID
}
