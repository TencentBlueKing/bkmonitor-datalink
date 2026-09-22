// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

// Package basetarget resolves MonitorSource, NoData, SystemMetric, and Basic
// branches into the shared resource and display output groups.
package basetarget

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

// Result 是 BaseTarget 场景对 resource 和 display 分组的完整贡献。
type Result struct {
	Resource            models.ResourceValues
	ResourceDiagnostics []enrich.Diagnostic
	Object              string
	DisplayDiagnostics  []enrich.Diagnostic
}

// Enrich 解析指定 BaseTarget 分支，并在 Scope 内复用 Resource/Display 的场景结果。
func Enrich(
	ctx context.Context,
	scope *enrich.Scope,
	classification rules.Classification,
	strategy models.CWStrategy,
	dimensions domain.DimensionMap,
	fallbackBizID int64,
	subjectName string,
) (Result, error) {
	key := "basetarget:" + string(classification.BaseTarget)
	cached, err := scope.Scenario(key, func() (any, error) {
		resolution, resolveErr := resolve(ctx, scope, classification.BaseTarget, strategy, dimensions, fallbackBizID)
		if resolveErr != nil {
			return nil, resolveErr
		}
		resource := projectResource(resolution, fallbackBizID)
		return Result{
			Resource: resource.Values, ResourceDiagnostics: resource.Diagnostics,
			Object: projectDisplay(resolution, subjectName),
		}, nil
	})
	if err != nil {
		return Result{}, err
	}
	return cached.(Result), nil
}

type inputState uint8

const (
	inputMissing inputState = iota
	inputInvalid
	inputValid
)

type resolution struct {
	tenantID         string
	branch           rules.BaseTargetBranch
	modelCode        string
	instanceID       string
	query            enrich.InstanceQuery
	queryDiagnostics []enrich.Diagnostic
	inputState       inputState

	model      enrich.Model
	modelFound bool
	modelErr   error

	instance      enrich.Instance
	instanceFound bool
	instanceErr   error

	topologyRequired bool
	relatedHost      enrich.Instance
	relatedHostFound bool
	relatedHostErr   error
	topology         models.ResourceTopology
	topologyFound    bool
	topologyErr      error
}

func resolve(
	ctx context.Context,
	scope *enrich.Scope,
	branch rules.BaseTargetBranch,
	strategy models.CWStrategy,
	dimensions domain.DimensionMap,
	fallbackBizID int64,
) (resolution, error) {
	result := resolution{tenantID: scope.Alert().BKTenantID, branch: branch, inputState: inputValid}
	if strategy.ObjectModelCode != nil {
		result.modelCode = *strategy.ObjectModelCode
	}
	if branch == rules.BaseTargetBasic {
		result.model, result.modelFound, result.modelErr = scope.ModelByCode(ctx, result.modelCode)
		if err := ctx.Err(); err != nil {
			return resolution{}, err
		}
		return result, nil
	}

	var err error
	result.query, result.queryDiagnostics, result.inputState, err = branchQuery(branch, result.modelCode, dimensions, fallbackBizID)
	if err != nil {
		return resolution{}, err
	}
	if result.inputState != inputValid || result.query.ModelCode == "" ||
		result.query.InstanceID == "" && len(result.query.AttributeFilters) == 0 {
		return result, nil
	}
	result.modelCode = result.query.ModelCode
	result.instanceID = result.query.InstanceID
	result.model, result.modelFound, result.modelErr = scope.ModelByCode(ctx, result.modelCode)
	if err := ctx.Err(); err != nil {
		return resolution{}, err
	}
	result.instance, result.instanceFound, result.instanceErr = scope.Instance(ctx, result.query)
	if err := ctx.Err(); err != nil {
		return resolution{}, err
	}
	if result.instanceErr != nil || !result.instanceFound {
		return result, nil
	}
	// 带主键的查询必须保留请求身份用于投影前复核；地址查询只有在
	// OneModel Reader 已验证响应后，才以返回主键作为资源身份。
	if result.instanceID != "" && result.instance.InstanceID != result.instanceID {
		return result, nil
	}
	if result.instance.TenantID != result.tenantID || result.instance.ModelCode != result.modelCode || result.instance.InstanceID == "" {
		return result, nil
	}
	result.instanceID = result.instance.InstanceID

	result.topologyRequired = result.modelCode == rules.HostModelCode || !instanceHasBusiness(result.instance)
	if !result.topologyRequired {
		return result, nil
	}
	if result.modelCode == rules.HostModelCode {
		result.relatedHost, result.relatedHostFound = result.instance, true
		result.topology, result.topologyFound, result.topologyErr = scope.HostTopology(ctx, result.instance.InstanceID)
		if err := ctx.Err(); err != nil {
			return resolution{}, err
		}
		return result, nil
	}
	if result.modelErr == nil && result.modelFound {
		if relation, ok := rules.FirstStringField(result.model.Fields, rules.FieldHostRelatedField); ok {
			result.relatedHost, result.relatedHostFound, result.relatedHostErr = scope.RelatedHost(
				ctx, result.modelCode, result.instance.InstanceID, relation,
			)
			if result.relatedHostErr == nil && result.relatedHostFound &&
				result.relatedHost.TenantID == result.tenantID && result.relatedHost.ModelCode == rules.HostModelCode && result.relatedHost.InstanceID != "" {
				result.topology, result.topologyFound, result.topologyErr = scope.HostTopology(ctx, result.relatedHost.InstanceID)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return resolution{}, err
	}
	return result, nil
}

func branchQuery(
	branch rules.BaseTargetBranch,
	strategyModelCode string,
	dimensions domain.DimensionMap,
	fallbackBizID int64,
) (enrich.InstanceQuery, []enrich.Diagnostic, inputState, error) {
	switch branch {
	case rules.BaseTargetMonitorSource:
		return monitorSourceQuery(strategyModelCode, dimensions, fallbackBizID)
	case rules.BaseTargetNoData:
		return noDataQuery(strategyModelCode, dimensions, fallbackBizID)
	case rules.BaseTargetSystemMetric:
		return systemMetricQuery(strategyModelCode, dimensions)
	case rules.BaseTargetBasic:
		return enrich.InstanceQuery{}, nil, inputValid, nil
	default:
		return enrich.InstanceQuery{}, []enrich.Diagnostic{{Code: enrich.DiagnosticCodeClassificationFailed}}, inputInvalid, nil
	}
}

func monitorSourceQuery(modelCode string, dimensions domain.DimensionMap, bizID int64) (enrich.InstanceQuery, []enrich.Diagnostic, inputState, error) {
	if value, exists := dimensions[rules.FieldBKInstID]; exists && rules.ScalarProvided(value) {
		identity, valid := positiveIntegerIdentity(value)
		if !valid {
			return enrich.InstanceQuery{}, invalidField(rules.FieldBKInstID), inputInvalid, nil
		}
		return serviceInstanceQuery(modelCode, identity, bizID), nil, inputValid, nil
	}
	modelValue, modelExists := dimensions[rules.FieldObjectModelID]
	instanceValue, instanceExists := dimensions[rules.FieldObjectModelInstID]
	if !modelExists || !instanceExists || !rules.ScalarProvided(modelValue) || !rules.ScalarProvided(instanceValue) {
		return enrich.InstanceQuery{}, missingFields(rules.FieldObjectModelID, rules.FieldObjectModelInstID), inputMissing, nil
	}
	modelIdentity := rules.ScalarIdentity(modelValue)
	instanceIdentity, valid := positiveIntegerIdentity(instanceValue)
	if modelIdentity == "" || !valid {
		return enrich.InstanceQuery{}, invalidField(rules.FieldObjectModelInstID), inputInvalid, nil
	}
	return serviceInstanceQuery(modelIdentity, instanceIdentity, bizID), nil, inputValid, nil
}

func noDataQuery(modelCode string, dimensions domain.DimensionMap, bizID int64) (enrich.InstanceQuery, []enrich.Diagnostic, inputState, error) {
	modelValue, modelExists := dimensions[rules.FieldModelID]
	instanceValue, instanceExists := dimensions[rules.FieldModelInstID]
	missing := make([]string, 0, 2)
	if !modelExists || !rules.ScalarProvided(modelValue) {
		missing = append(missing, "dimensions."+rules.FieldModelID)
	}
	if !instanceExists || !rules.ScalarProvided(instanceValue) {
		missing = append(missing, "dimensions."+rules.FieldModelInstID)
	}
	if len(missing) != 0 {
		return enrich.InstanceQuery{}, []enrich.Diagnostic{{Code: enrich.DiagnosticCodeMissingField, Fields: missing}}, inputMissing, nil
	}
	dimensionModelCode, ok := modelValue.StringValue()
	if !ok || dimensionModelCode == "" || dimensionModelCode != modelCode {
		return enrich.InstanceQuery{}, invalidField(rules.FieldModelID), inputInvalid, nil
	}
	instanceID, valid := positiveIntegerIdentity(instanceValue)
	if !valid {
		return enrich.InstanceQuery{}, invalidField(rules.FieldModelInstID), inputInvalid, nil
	}
	return serviceInstanceQuery(modelCode, instanceID, bizID), nil, inputValid, nil
}

func systemMetricQuery(modelCode string, dimensions domain.DimensionMap) (enrich.InstanceQuery, []enrich.Diagnostic, inputState, error) {
	for _, field := range []string{rules.FieldBKInstID, rules.FieldBKHostID, rules.FieldBKTargetHostID} {
		value, exists := dimensions[field]
		if !exists || !rules.ScalarProvided(value) {
			continue
		}
		identity, valid := positiveIntegerIdentity(value)
		if !valid {
			return enrich.InstanceQuery{}, invalidField(field), inputInvalid, nil
		}
		return enrich.InstanceQuery{ModelCode: modelCode, InstanceID: identity}, nil, inputValid, nil
	}
	ip, _ := rules.DimensionValue(dimensions, rules.FieldBKTargetIP)
	cloudID, cloudExists := rules.DimensionValue(dimensions, rules.FieldBKTargetCloudID)
	if ip == nil {
		ip = ""
	}
	if !cloudExists {
		cloudID = float64(-1)
	}
	query := enrich.InstanceQuery{ModelCode: modelCode, AttributeFilters: []enrich.InstanceAttributeFilter{
		{Field: rules.FieldBKHostInnerIP, Type: enrich.InstanceAttributeKeyword, Value: ip},
		{Field: rules.FieldBKCloudID, Type: enrich.InstanceAttributeLong, Value: cloudID},
	}}
	return query, []enrich.Diagnostic{{Code: enrich.DiagnosticCodeMissingField, Fields: []string{
		"dimensions." + rules.FieldBKInstID,
		"dimensions." + rules.FieldBKHostID,
		"dimensions." + rules.FieldBKTargetHostID,
	}}}, inputValid, nil
}

func serviceInstanceQuery(modelCode, instanceID string, bizID int64) enrich.InstanceQuery {
	query := enrich.InstanceQuery{ModelCode: modelCode, InstanceID: instanceID}
	if rules.IsServiceInstanceModel(modelCode) && bizID > 0 {
		query.AttributeFilters = append(query.AttributeFilters, enrich.InstanceAttributeFilter{
			Field: rules.FieldCWBIZID, Type: enrich.InstanceAttributeLong, Value: bizID,
		})
	}
	return query
}

func positiveIntegerIdentity(value domain.Scalar) (string, bool) {
	if text, ok := value.StringValue(); ok {
		parsed, err := strconv.ParseInt(text, 10, 64)
		if err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != text {
			return "", false
		}
		return text, true
	}
	number, ok := value.NumberValue()
	if !ok || number <= 0 || math.Trunc(number) != number || number > math.MaxInt64 {
		return "", false
	}
	return fmt.Sprintf("%.0f", number), true
}

func invalidField(field string) []enrich.Diagnostic {
	return []enrich.Diagnostic{{Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"dimensions." + field}}}
}

func missingFields(fields ...string) []enrich.Diagnostic {
	paths := make([]string, len(fields))
	for index, field := range fields {
		paths[index] = "dimensions." + field
	}
	return []enrich.Diagnostic{{Code: enrich.DiagnosticCodeMissingField, Fields: paths}}
}

func instanceHasBusiness(instance enrich.Instance) bool {
	fields := mergeInstanceFields(instance)
	return rules.FirstStringFieldValue(fields, rules.FieldBKBizID, "bk_biz_ids", rules.FieldCWBIZID) != ""
}

func mergeInstanceFields(instance enrich.Instance) map[string]any {
	fields := make(map[string]any, len(instance.Fields)+len(instance.Attributes))
	for key, value := range instance.Attributes {
		fields[key] = value
	}
	for key, value := range instance.Fields {
		fields[key] = value
	}
	return fields
}
