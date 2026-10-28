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
	"encoding/json"
	"fmt"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

// K8s 丰富 Kubernetes 维度字段，并通过 OneModel Reader 校验统一实例身份。
// K8sIdentityReader 保留场景级窄接口，具体实现复用统一实例存储。
type K8sIdentityReader interface {
	FindK8sInstance(ctx context.Context, tenantID, modelCode string, dimensions domain.DimensionMap) (enrich.Instance, bool, error)
}

var k8sModelCodes = map[string]string{
	rules.FieldBCSClusterID: "cw-K8s_Cluster", rules.FieldNamespace: "cw-K8s_Namespace",
	rules.FieldContainerName: "cw-K8s_Container", rules.FieldPodName: "cw-K8s_Pod", rules.FieldWorkloadName: "cw-K8s_Workload", rules.FieldService: "cw-K8s_Service", "node": "cw-K8s_Node",
}

var k8sIdentityFields = map[string][]string{
	"cw-K8s_Cluster":   {rules.FieldBCSClusterID},
	"cw-K8s_Namespace": {rules.FieldBCSClusterID, rules.FieldNamespace},
	"cw-K8s_Service":   {rules.FieldBCSClusterID, rules.FieldNamespace, rules.FieldService},
	"cw-K8s_Workload":  {rules.FieldBCSClusterID, rules.FieldNamespace, rules.FieldWorkloadKind, rules.FieldWorkloadName},
	"cw-K8s_Pod":       {rules.FieldBCSClusterID, rules.FieldNamespace, rules.FieldPodName},
	"cw-K8s_Container": {rules.FieldBCSClusterID, rules.FieldNamespace, rules.FieldPodName, rules.FieldContainerName},
	"cw-K8s_Node":      {rules.FieldBCSClusterID, "node"},
}

type K8s struct{}

func (K8s) Name() string { return rules.K8sProcessor }

// Match 优先使用前序步骤确定的模型；只有模型未知时才允许根据维度推断。
func (K8s) Match(ctx context.Context, scope *enrich.Scope) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	modelCode, err := k8sModelCodeForScope(scope)
	if err != nil {
		return false, err
	}
	_, known := k8sIdentityFields[modelCode]
	return known, nil
}

func (K8s) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	alert := scope.Facts()
	values := models.K8sValues{
		BCSClusterID:  dimensionText(alert.Dimensions, rules.FieldBCSClusterID),
		ClusterName:   dimensionText(alert.Dimensions, "cluster_name"),
		Namespace:     dimensionText(alert.Dimensions, rules.FieldNamespace),
		Service:       dimensionText(alert.Dimensions, rules.FieldService),
		WorkloadKind:  dimensionText(alert.Dimensions, rules.FieldWorkloadKind),
		WorkloadName:  dimensionText(alert.Dimensions, rules.FieldWorkloadName),
		PodName:       dimensionText(alert.Dimensions, rules.FieldPodName),
		ContainerName: dimensionText(alert.Dimensions, rules.FieldContainerName),
		CWLabels:      []string{},
	}
	modelCode, err := k8sModelCodeForScope(scope)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	if _, known := k8sIdentityFields[modelCode]; !known {
		return enrich.ProcessorResult{Status: domain.EnrichStatusSkipped}, nil
	}
	if missing := k8sMissingIdentityFields(modelCode, alert.Dimensions); len(missing) != 0 {
		return k8sValuesDiagnostic(scope, values, domain.EnrichStatusFailed, enrich.Diagnostic{Code: enrich.DiagnosticCodeMissingField, Fields: missing})
	}
	// 1. 主实例身份只由本次查验成功的结果发布，不能借用上下文身份。
	instance, found, err := scope.K8sInstance(ctx, modelCode, alert.Dimensions)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return enrich.ProcessorResult{}, ctxErr
	}
	invalidModels := []string{}
	if err != nil || !found || !validK8sInstance(instance, alert.BKTenantID, modelCode) {
		invalidModels = append(invalidModels, modelCode)
		instance = enrich.Instance{}
	} else {
		values.ModelID, values.ModelInstID = instance.ModelCode, instance.InstanceID
		if name := k8sClusterName(instance); name != "" {
			values.ClusterName = name
		}
	}
	// 2. 集群和命名空间按自身定位条件独立查询；主实例失效不阻断上下文。
	invalidContext, err := resolveK8sBusiness(ctx, scope, modelCode, alert.Dimensions, instance, &values)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	invalidModels = append(invalidModels, invalidContext...)
	values.CWLabels = k8sLabels(values)
	scope.Context().K8s.Set(values)
	// 3. 发布已验证的上下文，同时保留失败模型及原有身份清理规则。
	if len(invalidModels) != 0 {
		return k8sValuesDiagnostic(scope, values, domain.EnrichStatusPartial, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyOneModel, Fields: invalidModels})
	}
	return k8sValuesResult(scope.Context().K8s.Values)
}

func k8sValuesDiagnostic(scope *enrich.Scope, values models.K8sValues, status domain.EnrichStatus, diagnostic enrich.Diagnostic) (enrich.ProcessorResult, error) {
	result, err := k8sValuesResult(values)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	result.Status, result.Diagnostics = status, []enrich.Diagnostic{diagnostic}
	if values.ModelID != "" && values.ModelInstID != "" {
		// 仅上下文降级时，已查验成功的主实例身份必须保留。
		return result, nil
	}
	state, err := scope.EvaluationState()
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	document := state.Document()
	readIdentity := func(field string) string {
		for _, root := range []string{"labels", "extra_data", "dimensions"} {
			fields, _ := document[root].(map[string]any)
			if value, exists := fields[field]; exists {
				text, _ := value.(string)
				return text
			}
		}
		return ""
	}
	model, instance := readIdentity(rules.FieldModelID), readIdentity(rules.FieldModelInstID)
	if (model != "" && instance != "") || (model == "" && instance == "") {
		return result, nil
	}
	// K8s 的模型必须与实例成对输出。只清理当前有效视图中的残缺身份，
	// 查询失败不能抹掉前序步骤已确定的完整身份，也不能修改原始 dimensions。
	// 显式空 labels 遮蔽 extra_data 中的旧值；partial 保证清理补丁不会被 failed 回滚。
	if status == domain.EnrichStatusFailed {
		// 维度缺失时原本不发布上下文；这里只发布身份清理，避免空展示值覆盖前序结果。
		result.Value = domain.JSONObject{}
	}
	result.Value[rules.FieldModelID] = json.RawMessage(`""`)
	result.Value[rules.FieldModelInstID] = json.RawMessage(`""`)
	result.Status = domain.EnrichStatusPartial
	return result, nil
}

func k8sValuesResult(values models.K8sValues) (enrich.ProcessorResult, error) {
	encoded, err := json.Marshal(values)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	var value domain.JSONObject
	if err := json.Unmarshal(encoded, &value); err != nil {
		return enrich.ProcessorResult{}, err
	}
	// 查询成功才发布新的身份；失败分支由 k8sValuesDiagnostic 区分
	// 已有完整身份和需要清理的残缺身份，不能用缺省零值无条件覆盖。
	if values.ModelID == "" || values.ModelInstID == "" {
		delete(value, rules.FieldModelID)
		delete(value, rules.FieldModelInstID)
	}
	return enrich.ProcessorResult{Status: domain.EnrichStatusSucceeded, Value: value}, nil
}

// k8sModelCodeForScope 为匹配和处理提供同一模型选择规则，不使用可能已被规则覆盖的 Resource 缓存。
// 优先读取当前 evaluation 的 labels、extra_data、dimensions；模型未知时才按原始维度推断。
func k8sModelCodeForScope(scope *enrich.Scope) (string, error) {
	state, err := scope.EvaluationState()
	if err != nil {
		return "", err
	}
	document := state.Document()
	for _, root := range []string{"labels", "extra_data", "dimensions"} {
		fields, _ := document[root].(map[string]any)
		if model, ok := fields[rules.FieldModelID].(string); ok && model != "" {
			return model, nil
		}
	}
	return k8sModelCode(scope.Facts().Dimensions), nil
}

func k8sModelCode(dimensions domain.DimensionMap) string {
	if dimensionText(dimensions, rules.FieldWorkloadName) != "" && dimensionText(dimensions, rules.FieldWorkloadKind) == "Pod" {
		return "cw-K8s_Pod"
	}
	for _, field := range []string{rules.FieldContainerName, rules.FieldPodName, rules.FieldWorkloadName, rules.FieldService, "node", rules.FieldNamespace, rules.FieldBCSClusterID} {
		if dimensionText(dimensions, field) != "" {
			return k8sModelCodes[field]
		}
	}
	return ""
}

func k8sMissingIdentityFields(modelCode string, dimensions domain.DimensionMap) []string {
	if modelCode == "cw-K8s_Namespace" {
		if dimensionText(dimensions, rules.FieldBCSClusterID) == "" || dimensionText(dimensions, rules.FieldNamespace) == "" {
			return []string{"dimensions." + rules.FieldBCSClusterID, "dimensions." + rules.FieldNamespace}
		}
		return nil
	}
	missing := make([]string, 0)
	for _, field := range k8sIdentityFields[modelCode] {
		if dimensionText(dimensions, field) == "" {
			missing = append(missing, "dimensions."+field)
		}
	}
	return missing
}

func firstInstanceValue(instance enrich.Instance, field string) any {
	if value, ok := instance.Fields[field]; ok {
		return value
	}
	if value, ok := instance.Attributes[field]; ok {
		return value
	}
	return nil
}

func firstInstanceText(instance enrich.Instance, fields ...string) string {
	for _, field := range fields {
		if value := firstInstanceValue(instance, field); value != nil {
			text := fmt.Sprint(value)
			if text != "" {
				return text
			}
		}
	}
	return ""
}

func validK8sInstance(instance enrich.Instance, tenant, model string) bool {
	return instance.TenantID == tenant && instance.ModelCode == model && instance.InstanceID != ""
}

func k8sClusterName(instance enrich.Instance) string {
	if instance.ModelCode == "cw-K8s_Cluster" {
		return firstInstanceText(instance, "cluster_name", "name", "display_name")
	}
	// Namespace、Pod 和容器的 name 是对象名，不是集群名。
	return firstInstanceText(instance, "cluster_name")
}

func resolveK8sBusiness(ctx context.Context, scope *enrich.Scope, modelCode string, dimensions domain.DimensionMap, instance enrich.Instance, values *models.K8sValues) ([]string, error) {
	candidates := make([]enrich.Instance, 0, 3)
	invalidModels := []string{}
	for _, contextModel := range []string{"cw-K8s_Namespace", "cw-K8s_Cluster"} {
		if contextModel == modelCode {
			if instance.InstanceID != "" {
				candidates = append(candidates, instance)
			}
			continue
		}
		if len(k8sMissingIdentityFields(contextModel, dimensions)) != 0 {
			continue
		}
		value, found, err := scope.K8sInstance(ctx, contextModel, dimensions)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if err != nil || !found || !validK8sInstance(value, scope.Facts().BKTenantID, contextModel) {
			invalidModels = append(invalidModels, contextModel)
			continue
		}
		candidates = append(candidates, value)
	}
	if instance.InstanceID != "" && modelCode != "cw-K8s_Namespace" && modelCode != "cw-K8s_Cluster" {
		candidates = append(candidates, instance)
	}
	// 名称与业务分别选择；业务缺失不能阻止集群取名，也不能返回对象 name。
	for _, candidate := range candidates {
		if name := k8sClusterName(candidate); name != "" {
			values.ClusterName = name
			break
		}
	}
	for _, candidate := range candidates {
		if bizID := firstInstanceValue(candidate, "bk_biz_id"); bizID != nil {
			values.BKBizID, values.BKBizName = bizID, firstInstanceText(candidate, "bk_biz_name")
			return invalidModels, nil
		}
	}
	for _, candidate := range candidates {
		if bizID := firstUniqueBusinessID(firstInstanceValue(candidate, "bk_biz_ids")); bizID != nil {
			values.BKBizID, values.BKBizName = bizID, firstInstanceText(candidate, "bk_biz_name")
			return invalidModels, nil
		}
	}
	if value, exists := scope.Facts().Labels[rules.FieldBKBizID]; exists {
		values.BKBizID = scalarAsIdentity(value)
	}
	return invalidModels, nil
}

func scalarAsIdentity(value domain.Scalar) any {
	if number, ok := value.NumberValue(); ok {
		return number
	}
	if text, ok := value.StringValue(); ok {
		return text
	}
	return nil
}

func firstUniqueBusinessID(value any) any {
	switch values := value.(type) {
	case []any:
		if len(values) == 1 {
			return values[0]
		}
	case []int64:
		if len(values) == 1 {
			return values[0]
		}
	case []int:
		if len(values) == 1 {
			return values[0]
		}
	}
	return nil
}

func k8sLabels(values models.K8sValues) []string {
	labels := []string{}
	if values.BKBizID != nil {
		text := fmt.Sprint(values.BKBizID)
		if text != "" && text != "0" {
			labels = append(labels, "bk_biz_id", "bk_biz_id|"+text)
		}
	}
	if values.BCSClusterID != "" {
		labels = append(labels, "cluster_id", "cluster_id|"+values.BCSClusterID)
	}
	if values.Namespace != "" {
		labels = append(labels, "namespace_id", "cluster_id|"+values.BCSClusterID+"&namespace_id|"+values.Namespace)
	}
	return labels
}

func dimensionText(dimensions domain.DimensionMap, field string) string {
	value, exists := dimensions[field]
	if !exists {
		return ""
	}
	return rules.ScalarIdentity(value)
}
