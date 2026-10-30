// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

type resourceMetric struct {
	query    models.StrategyQueryConfig
	metadata models.MetricMetadata
	found    bool
}

// readResourceMetrics 保留全部输入的目录约束，不用首条输入替代整个表达式的对象语义。
// 无 ID 的旧 APM/拨测声明仍由各自来源规则处理，不能凭表名伪造目录引用。
func readResourceMetrics(ctx context.Context, scope *enrich.Scope, strategy models.CWStrategy, projection models.StrategyItemProjection, modelCode string) ([]resourceMetric, error) {
	metrics := make([]resourceMetric, 0, len(projection.QueryConfigs))
	for _, query := range projection.QueryConfigs {
		entry := resourceMetric{query: query}
		if query.MetricRefID == 0 && (rules.IsAPMQuery(query) || strings.HasPrefix(query.ResultTableID, "uptimecheck")) {
			metrics = append(metrics, entry)
			continue
		}
		var err error
		entry.metadata, entry.found, err = metricDefinitionMetadata(ctx, scope, strategy, query, modelCode)
		if err != nil {
			return nil, err
		}
		metrics = append(metrics, entry)
	}
	return metrics, nil
}

func resourceMetricIsAPM(metric resourceMetric) bool {
	if metric.found {
		code := metric.metadata.RefMetricsetCode
		if code == "" {
			code = metric.metadata.MetricsetCode
		}
		if code != "" {
			return rules.IsAPMMetricset(code)
		}
	}
	return rules.IsAPMQuery(metric.query)
}

func apmSourceConflict(metrics []resourceMetric) bool {
	applicationSource := ""
	for _, metric := range metrics {
		code := metric.metadata.RefMetricsetCode
		if code == "" {
			code = metric.metadata.MetricsetCode
		}
		if code == "" {
			code = metric.query.MetricsetCode
		}
		if strings.HasPrefix(code, "apm_application.") {
			if applicationSource != "" && applicationSource != code {
				return true
			}
			applicationSource = code
		}
	}
	return false
}

// readAPMResourceMetrics 仅为显式目录引用补充来源；普通旧查询没有目录身份时，
// APM 必须能直接判断不适用，不为其他来源引入目录依赖。
func readAPMResourceMetrics(ctx context.Context, scope *enrich.Scope, strategy models.CWStrategy, projection models.StrategyItemProjection) ([]resourceMetric, error) {
	metrics := make([]resourceMetric, 0, len(projection.QueryConfigs))
	for _, query := range projection.QueryConfigs {
		entry := resourceMetric{query: query}
		if query.MetricRefID > 0 {
			var err error
			entry.metadata, entry.found, err = metricDefinitionMetadata(ctx, scope, strategy, query, "")
			if err != nil {
				return nil, err
			}
		}
		metrics = append(metrics, entry)
	}
	return metrics, nil
}

func apmResourceIdentity(ctx context.Context, scope *enrich.Scope, strategy models.CWStrategy, metrics []resourceMetric, appID, service, instance string) (string, string, []enrich.Diagnostic, error) {
	if appID == "" {
		return "", "", nil, nil
	}
	dimensions, diagnostics, err := dataResourceDimensions(scope)
	if err != nil || len(diagnostics) > 0 {
		return "", "", diagnostics, err
	}
	code := dataObjectModelCode(strategy, dimensions)
	if eventCode := dimensionText(dimensions, rules.FieldModelID); eventCode != "" && code != eventCode {
		return "", "", []enrich.Diagnostic{{Code: "identity_conflict", Fields: []string{"strategy.object_model_code", "dimensions.model_id"}}}, nil
	}
	allowed, constrained := resourceMetricModels(metrics)
	if code == "" && len(allowed) == 1 {
		code = allowed[0]
	}
	if code == "" {
		code, _ = apmModelIdentity(appID, service, instance)
	}
	id := ""
	var missing []string
	switch code {
	case rules.APMApplicationModelCode:
		id = appID
	case rules.APMServiceModelCode:
		if service == "" {
			missing = []string{"dimensions.service_name"}
		} else {
			id = appID + "|" + service
		}
	case rules.APMServiceInstanceModelCode:
		if service == "" || instance == "" {
			missing = []string{"dimensions.service_name", "dimensions.bk_instance_id"}
		} else {
			id = appID + "|" + service + "|" + instance
		}
	case rules.APMInterfaceModelCode:
		// 接口自然键还含 category/span_kind，不能仅由 span_name 拼接身份。
		// 明确的标准身份经 OneModel 校验后使用，并核对其应用和服务范围。
		id = dimensionText(dimensions, rules.FieldModelInstID)
		if id == "" {
			missing = []string{"dimensions.model_id", "dimensions.model_inst_id"}
			break
		}
		entity, found, readErr := scope.Instance(ctx, enrich.InstanceQuery{ModelCode: code, InstanceID: id})
		if ctx.Err() != nil {
			return "", "", nil, ctx.Err()
		}
		if readErr != nil || !found || entity.TenantID != scope.Facts().BKTenantID || entity.ModelCode != code || entity.InstanceID != id || entity.Fields["entity_uid"] != code+"|"+id {
			return "", "", []enrich.Diagnostic{{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyOneModel}}, nil
		}
		if fmt.Sprint(firstInstanceValue(entity, "app_id")) != appID || (service != "" && firstInstanceText(entity, "service_name") != service) {
			return "", "", []enrich.Diagnostic{{Code: "identity_conflict", Fields: []string{"dimensions.model_inst_id", "dimensions.app_name", "dimensions.service_name"}}}, nil
		}
	default:
		return "", "", []enrich.Diagnostic{{Code: "identity_conflict", Fields: []string{"strategy.object_model_code", "dimensions.model_id"}}}, nil
	}
	if len(missing) > 0 {
		return "", "", []enrich.Diagnostic{{Code: enrich.DiagnosticCodeMissingField, Fields: missing}}, nil
	}
	if constrained && !slices.Contains(allowed, code) {
		return "", "", []enrich.Diagnostic{{Code: "identity_conflict", Fields: []string{"query_configs.metric_ref_id", "dimensions.model_id"}}}, nil
	}
	if explicit := dimensionText(dimensions, rules.FieldModelInstID); explicit != "" && explicit != id {
		return "", "", []enrich.Diagnostic{{Code: "identity_conflict", Fields: []string{"dimensions.model_inst_id", "dimensions.app_name", "dimensions.service_name", "dimensions.bk_instance_id"}}}, nil
	}
	return code, id, nil, nil
}

// resourceMetricModels 对各输入的非空适用对象集合求交集；空集合表示目录未关联，
// 不提供对象选择依据。非空集合互不相交时明确报告冲突，不任取首个模型。
func resourceMetricModels(metrics []resourceMetric) ([]string, bool) {
	var selected []string
	constrained := false
	for _, metric := range metrics {
		if !metric.found || len(metric.metadata.ModelIDs) == 0 {
			continue
		}
		if !constrained {
			selected = slices.Clone(metric.metadata.ModelIDs)
			constrained = true
			continue
		}
		selected = slices.DeleteFunc(selected, func(code string) bool { return !slices.Contains(metric.metadata.ModelIDs, code) })
	}
	slices.Sort(selected)
	return slices.Compact(selected), constrained
}

// dataResourceDimensions 只在本地副本加入当前 evaluation 的标准身份；完整的一对
// 必须来自同一位置，重复声明必须一致。不能拼接 labels 的模型与 dimensions 的实例。
func dataResourceDimensions(scope *enrich.Scope) (domain.DimensionMap, []enrich.Diagnostic, error) {
	state, err := scope.EvaluationState()
	if err != nil {
		return nil, nil, err
	}
	document := state.Document()
	dimensions := scope.Facts().Dimensions.Clone()
	if dimensions == nil {
		dimensions = domain.DimensionMap{}
	}
	model, instance := "", ""
	var primaryFields []string
	for _, root := range []string{"labels", "extra_data", "dimensions"} {
		fields, _ := document[root].(map[string]any)
		modelValue, hasModel := fields[rules.FieldModelID]
		instanceValue, hasInstance := fields[rules.FieldModelInstID]
		if !hasModel && !hasInstance {
			continue
		}
		paths := []string{root + "." + rules.FieldModelID, root + "." + rules.FieldModelInstID}
		code, codeOK := modelValue.(string)
		id, idOK := instanceValue.(string)
		if !hasModel || !hasInstance || !codeOK || !idOK || !resourceIdentityText(code, 128) || !resourceIdentityText(id, 1024) {
			return dimensions, []enrich.Diagnostic{{Code: enrich.DiagnosticCodeInvalidField, Fields: paths}}, nil
		}
		if model != "" && (model != code || instance != id) {
			return dimensions, []enrich.Diagnostic{{Code: "identity_conflict", Fields: append(primaryFields, paths...)}}, nil
		}
		model, instance, primaryFields = code, id, paths
	}
	if model != "" {
		dimensions[rules.FieldModelID] = domain.NewStringScalar(model)
		dimensions[rules.FieldModelInstID] = domain.NewStringScalar(instance)
	}
	return dimensions, nil, nil
}

func dataResourceConflict(fields ...string) enrich.ProcessorResult {
	return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: []enrich.Diagnostic{{Code: "identity_conflict", Fields: fields}}}
}
