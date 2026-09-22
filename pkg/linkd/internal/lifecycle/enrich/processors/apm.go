package processors

import (
	"context"
	"fmt"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

// APM 丰富 APM 应用、服务、服务实例和接口维度。
type APM struct{}

func (APM) Name() string                                       { return rules.APMProcessor }
func (APM) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

func (APM) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	alert := scope.Alert()
	ids, diagnostics := enrich.ValidateRequiredIDs(alert)
	if len(diagnostics) != 0 {
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: diagnostics}, nil
	}
	strategy, found, err := scope.CWStrategyByBKStrategyID(ctx, ids.StrategyID)
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	if err != nil || !found {
		return failedDependency(rules.DependencyKingeyeStrategy), nil
	}
	projection, err := strategy.StrategyItemProjection()
	if err != nil || !rules.IsAPMTable(projection.QueryConfigs[0].ResultTableID) {
		return enrich.ProcessorResult{Status: domain.EnrichStatusSkipped, Value: domain.JSONObject{}}, nil
	}
	application, applicationDiagnostics, err := resolveAPMApplication(ctx, scope, strategy, projection.QueryConfigs[0].ResultTableID, ids.BizID)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	diagnostics = append(diagnostics, applicationDiagnostics...)
	service := dimensionText(alert.Dimensions, rules.FieldServiceName)
	instance := dimensionText(alert.Dimensions, rules.FieldAPMInstanceID)
	modelID, modelInstID := apmModelIdentity(application.ID, service, instance)
	values := models.APMValues{APMAppID: apmIDValue(application.ID), APMAppName: application.Name, APMAppAlias: application.Alias, APMServiceName: service, APMInstanceName: instance, APMInterfaceName: dimensionText(alert.Dimensions, rules.FieldAPMSpanName), APMNetPeerName: dimensionText(alert.Dimensions, rules.FieldAPMNetPeerName), ModelID: modelID, ModelInstID: modelInstID, BKBizID: ids.BizID}
	if application.ID != "" {
		values.CWLabels = apmLabels(ids.BizID, application.ID)
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
	ID    string
	Name  string
	Alias string
}

// resolveAPMApplication 使用 alarmd 实际输入中的 additional_dimensions.app_name
// 定位租户和业务内的 APM 应用；旧 dimensions.apm_app_id 仅保留为兼容回退。
func resolveAPMApplication(ctx context.Context, scope *enrich.Scope, strategy models.CWStrategy, tableID string, bizID int64) (apmApplicationProjection, []enrich.Diagnostic, error) {
	alert := scope.Alert()
	projection := apmApplicationProjection{ID: dimensionText(alert.Dimensions, rules.FieldAPMAppID)}
	diagnostics := []enrich.Diagnostic{}
	additional, err := additionalDimensions(alert.ExtraData)
	if err != nil {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"extra_data.additional_dimensions"}})
	} else {
		projection.Name = dimensionText(additional, "app_name")
	}
	if projection.Name == "" {
		projection.Name = apmApplicationName(strategy, tableID)
	}
	if projection.Name == "" {
		if projection.ID == "" {
			diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeMissingField, Fields: []string{"extra_data.additional_dimensions.app_name"}})
		}
		return projection, diagnostics, nil
	}
	apps, readErr := scope.APMApplications(ctx, bizID, projection.Name)
	if err := ctx.Err(); err != nil {
		return apmApplicationProjection{}, nil, err
	}
	if readErr != nil {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyAPMApplication})
		return projection, diagnostics, nil
	}
	for _, app := range apps {
		if app.TenantID != alert.BKTenantID || app.BKBizID != bizID || app.Name != projection.Name || app.ID <= 0 {
			continue
		}
		projection.ID = fmt.Sprint(app.ID)
		projection.Alias = app.Alias
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
