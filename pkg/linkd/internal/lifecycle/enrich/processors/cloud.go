package processors

import (
	"context"
	"encoding/json"
	"strconv"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

// CloudResourceProcessor 丰富云平台策略关联的云资源。
type CloudResourceProcessor struct{}

func (CloudResourceProcessor) Name() string                                       { return "cloud_resource" }
func (CloudResourceProcessor) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

func (CloudResourceProcessor) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	alert := scope.Alert()
	ids, diagnostics := enrich.ValidateRequiredIDs(alert)
	if len(diagnostics) != 0 {
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: diagnostics}, nil
	}
	strategy, found, err := scope.CWStrategyByBKStrategyID(ctx, ids.StrategyID)
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	if err != nil || !found || strategy.Kind != models.CWStrategyKindCloud {
		return cloudFailure(rules.DependencyKingeyeStrategy)
	}
	cloudID, ok := cloudString(alert.Dimensions, rules.FieldCloudID)
	instanceID, instanceOK := cloudString(alert.Dimensions, rules.FieldInstanceID)
	resourceType, typeOK := cloudString(alert.Dimensions, rules.FieldTargetType)
	if !ok || !instanceOK || !typeOK {
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: []enrich.Diagnostic{{Code: enrich.DiagnosticCodeMissingField, Fields: []string{rules.FieldCloudID, rules.FieldInstanceID, rules.FieldTargetType}}}}, nil
	}
	resource, found, err := scope.CloudResource(ctx, cloudID, resourceType, instanceID)
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	if err != nil {
		return cloudFailure(rules.DependencyCloudResource)
	}
	if !found || resource.TenantID != alert.BKTenantID || resource.CloudID != cloudID || resource.ResourceType != resourceType || resource.InstanceID != instanceID {
		return cloudFailure(rules.DependencyCloudResource)
	}
	values := models.ResourceValues{ModelID: resource.ObjectModelCode, ModelInstID: resource.ModelInstanceID, ModelName: resource.Name, BKBizID: ids.BizID, BKCloudID: resource.CloudID, BKCloudName: resource.CloudName, CloudPlatformID: resource.CloudID}
	values.CWLabels = enrich.ResourceLabels(values)
	encoded, err := json.Marshal(values)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	var value domain.JSONObject
	if err := json.Unmarshal(encoded, &value); err != nil {
		return enrich.ProcessorResult{}, err
	}
	return enrich.ProcessorResult{Status: domain.EnrichStatusSucceeded, Value: value}, nil
}

func cloudFailure(dependency string) (enrich.ProcessorResult, error) {
	return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: []enrich.Diagnostic{{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: dependency}}}, nil
}
func cloudString(dimensions domain.DimensionMap, field string) (string, bool) {
	value, ok := dimensions[field]
	if !ok {
		return "", false
	}
	if text, ok := value.StringValue(); ok && text != "" {
		return text, true
	}
	if number, ok := value.NumberValue(); ok {
		return strconv.FormatFloat(number, 'f', -1, 64), true
	}
	return "", false
}
