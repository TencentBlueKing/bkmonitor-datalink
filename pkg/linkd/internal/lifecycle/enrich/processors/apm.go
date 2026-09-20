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
	appName := apmApplicationName(strategy, projection.QueryConfigs[0].ResultTableID)
	appID := dimensionText(alert.Dimensions, "apm_app_id")
	appAlias := ""
	if appName != "" {
		apps, appErr := scope.APMApplications(ctx, appName)
		if appErr == nil {
			for _, app := range apps {
				if app.TenantID == alert.BKTenantID && app.Name == appName {
					if appID == "" {
						appID = fmt.Sprint(app.ID)
					}
					appAlias = app.Alias
					break
				}
			}
		} else {
			diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyAPMApplication})
		}
	}
	service := dimensionText(alert.Dimensions, rules.FieldServiceName)
	instance := dimensionText(alert.Dimensions, rules.FieldAPMInstanceID)
	modelID, modelInstID := apmModelIdentity(appID, service, instance)
	values := models.APMValues{APMAppID: apmIDValue(appID), APMAppName: appName, APMAppAlias: appAlias, APMServiceName: service, APMInstanceName: instance, APMInterfaceName: dimensionText(alert.Dimensions, rules.FieldAPMSpanName), APMNetPeerName: dimensionText(alert.Dimensions, rules.FieldAPMNetPeerName), ModelID: modelID, ModelInstID: modelInstID, BKBizID: ids.BizID}
	if appID != "" {
		values.CWLabels = []string{"bk_biz_id", fmt.Sprintf("bk_biz_id|%d", ids.BizID), "apm_app_id", "apm_app_id|" + appID}
	}
	if appID == "" {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeMissingField, Fields: []string{"dimensions.apm_app_id"}})
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
