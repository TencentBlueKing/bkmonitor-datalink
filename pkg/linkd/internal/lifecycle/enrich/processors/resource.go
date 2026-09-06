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

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
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
	strategy, strategyFound, strategyErr := scope.CWStrategyByBKStrategyID(ctx, ids.StrategyID)
	if ctx.Err() != nil {
		return enrich.ProcessorResult{}, ctx.Err()
	}
	if strategyErr != nil || !strategyFound || strategy.ObjectModelCode == nil || *strategy.ObjectModelCode == "" {
		return failedDependency(rules.DependencyKingeyeStrategy), nil
	}
	query, queryDiagnostics := resourceInstanceQuery(*strategy.ObjectModelCode, alert.Dimensions)
	if len(queryDiagnostics) != 0 && len(query.Filters) == 0 {
		value, err := resourceContextValue(scope, models.ResourceValues{})
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
		return enrich.ProcessorResult{Status: domain.EnrichStatusPartial, Value: value, Diagnostics: queryDiagnostics}, nil
	}
	instance, found, err := scope.Instance(ctx, query)
	if ctx.Err() != nil {
		return enrich.ProcessorResult{}, ctx.Err()
	}
	if err != nil {
		return failedDependency(rules.DependencyOneModel), nil
	}
	if !found {
		value, encodeErr := resourceContextValue(scope, models.ResourceValues{})
		if encodeErr != nil {
			return enrich.ProcessorResult{}, encodeErr
		}
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: value}, nil
	}
	values := resourceValues(instance, ids.BizID)
	value, encodeErr := resourceContextValue(scope, values)
	if encodeErr != nil {
		return enrich.ProcessorResult{}, encodeErr
	}
	status := domain.EnrichStatusSucceeded
	if len(queryDiagnostics) != 0 {
		status = domain.EnrichStatusPartial
	}
	return enrich.ProcessorResult{Status: status, Value: value, Diagnostics: queryDiagnostics}, nil
}

func resourceInstanceQuery(modelCode string, dimensions domain.DimensionMap) (enrich.InstanceQuery, []enrich.Diagnostic) {
	fields := []string{rules.FieldBKInstID}
	if modelCode == rules.HostModelCode {
		fields = append(fields, rules.FieldBKHostID, rules.FieldBKTargetHostID)
	}
	for _, field := range fields {
		value, exists := dimensions[field]
		if !exists || !rules.ScalarProvided(value) {
			continue
		}
		return enrich.InstanceQuery{
			ModelCode: modelCode,
			Filters:   map[string]any{rules.FieldCWObjectModelInstID: rules.ScalarIdentity(value)},
		}, nil
	}
	if modelCode == rules.HostModelCode {
		ip, ipExists := rules.DimensionValue(dimensions, rules.FieldBKTargetIP)
		cloudID, cloudExists := rules.DimensionValue(dimensions, rules.FieldBKTargetCloudID)
		if ipExists || cloudExists {
			if !ipExists {
				ip = ""
			}
			if !cloudExists {
				cloudID = float64(-1)
			}
			return enrich.InstanceQuery{
					ModelCode: modelCode,
					Filters: map[string]any{
						rules.FieldBKHostInnerIP: ip,
						rules.FieldBKCloudID:     cloudID,
					},
				}, []enrich.Diagnostic{{Code: enrich.DiagnosticCodeMissingField, Fields: dimensionPaths(
					rules.FieldBKInstID, rules.FieldBKHostID, rules.FieldBKTargetHostID,
				)}}
		}
	}
	return enrich.InstanceQuery{}, []enrich.Diagnostic{{
		Code: enrich.DiagnosticCodeMissingField, Fields: dimensionPaths(fields[0]),
	}}
}

func dimensionPaths(fields ...string) []string {
	paths := make([]string, len(fields))
	for index, field := range fields {
		paths[index] = "dimensions." + field
	}
	return paths
}

func resourceContextValue(scope *enrich.Scope, values models.ResourceValues) (domain.JSONObject, error) {
	scope.Context().Resource.Set(values)
	return scope.Context().Resource.JSONObject()
}

func resourceValues(instance enrich.Instance, fallbackBizID int64) models.ResourceValues {
	fields := instance.Fields
	values := models.ResourceValues{ModelID: instance.ModelCode, ModelInstID: instance.InstanceID}
	values.BKObjID, _ = rules.FirstField(fields, rules.FieldBKObjID)
	if value, exists := rules.FirstField(fields, rules.FieldObjectModelName, rules.FieldBKObjName); exists {
		values.ModelName = fmt.Sprint(value)
	}
	values.BKInstID, _ = rules.FirstField(fields, rules.FieldBKInstID, rules.FieldBKHostID)
	values.BKBizID, _ = rules.FirstField(fields, rules.FieldBKBizID)
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
