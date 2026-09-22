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
	"strings"

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
	"cw-K8s_Cluster":   {},
	"cw-K8s_Namespace": {rules.FieldBCSClusterID, rules.FieldNamespace},
	"cw-K8s_Service":   {rules.FieldBCSClusterID, rules.FieldNamespace, rules.FieldService},
	"cw-K8s_Workload":  {rules.FieldBCSClusterID, rules.FieldNamespace, rules.FieldWorkloadKind, rules.FieldWorkloadName},
	"cw-K8s_Pod":       {rules.FieldBCSClusterID, rules.FieldNamespace, rules.FieldPodName},
	"cw-K8s_Container": {rules.FieldBCSClusterID, rules.FieldNamespace, rules.FieldPodName, rules.FieldContainerName},
	"cw-K8s_Node":      {rules.FieldBCSClusterID, "node"},
}

type K8s struct{}

func (K8s) Name() string { return rules.K8sProcessor }

func (K8s) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

func (K8s) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	if err := ctx.Err(); err != nil {
		return enrich.ProcessorResult{}, err
	}
	alert := scope.Alert()
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
	modelCode := k8sModelCode(alert.Dimensions)
	if modelCode != "" {
		if missing := k8sMissingIdentityFields(modelCode, alert.Dimensions); len(missing) != 0 {
			return k8sValuesDiagnostic(values, domain.EnrichStatusFailed, enrich.Diagnostic{Code: enrich.DiagnosticCodeMissingField, Fields: missing})
		}
		instance, found, err := scope.K8sInstance(ctx, modelCode, alert.Dimensions)
		if err != nil && strings.Contains(err.Error(), "unavailable") {
			scope.Context().K8s.Set(values)
			return k8sValuesResult(scope.Context().K8s.Values)
		}
		if err != nil || !found || instance.TenantID != alert.BKTenantID || instance.ModelCode != modelCode || instance.InstanceID == "" {
			return k8sValuesDiagnostic(values, domain.EnrichStatusPartial, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyOneModel})
		}
		values.ModelID, values.ModelInstID = instance.ModelCode, instance.InstanceID
		values.ClusterName = firstInstanceText(instance, "cluster_name", "name", "display_name")
		values.BKBizID, values.BKBizName, values.ClusterName = resolveK8sBusiness(ctx, scope, modelCode, alert.Dimensions, instance, values.ClusterName)
		values.CWLabels = k8sLabels(values)
	}
	scope.Context().K8s.Set(values)
	return k8sValuesResult(scope.Context().K8s.Values)
}

func k8sValuesDiagnostic(values models.K8sValues, status domain.EnrichStatus, diagnostic enrich.Diagnostic) (enrich.ProcessorResult, error) {
	result, err := k8sValuesResult(values)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	result.Status, result.Diagnostics = status, []enrich.Diagnostic{diagnostic}
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
	return enrich.ProcessorResult{Status: domain.EnrichStatusSucceeded, Value: value}, nil
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
	if modelCode == "cw-K8s_Cluster" {
		return nil
	}
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

func resolveK8sBusiness(ctx context.Context, scope *enrich.Scope, modelCode string, dimensions domain.DimensionMap, instance enrich.Instance, clusterName string) (any, string, string) {
	candidates := make([]enrich.Instance, 0, 3)
	if modelCode == "cw-K8s_Namespace" {
		candidates = append(candidates, instance)
	}
	if dimensionText(dimensions, rules.FieldNamespace) != "" && modelCode != "cw-K8s_Namespace" {
		if value, found, err := scope.K8sInstance(ctx, "cw-K8s_Namespace", dimensions); err == nil && found {
			candidates = append(candidates, value)
		}
	}
	if modelCode == "cw-K8s_Cluster" {
		candidates = append(candidates, instance)
	} else if dimensionText(dimensions, rules.FieldBCSClusterID) != "" {
		if value, found, err := scope.K8sInstance(ctx, "cw-K8s_Cluster", dimensions); err == nil && found {
			candidates = append(candidates, value)
		}
	}
	candidates = append(candidates, instance)
	for _, candidate := range candidates {
		if bizID := firstInstanceValue(candidate, "bk_biz_id"); bizID != nil {
			return bizID, firstInstanceText(candidate, "bk_biz_name"), firstInstanceText(candidate, "cluster_name", "name", "display_name")
		}
	}
	for _, candidate := range candidates {
		if bizID := firstUniqueBusinessID(firstInstanceValue(candidate, "bk_biz_ids")); bizID != nil {
			return bizID, firstInstanceText(candidate, "bk_biz_name"), firstInstanceText(candidate, "cluster_name", "name", "display_name")
		}
	}
	if value, exists := scope.Alert().Labels[rules.FieldBKBizID]; exists {
		return scalarAsIdentity(value), "", clusterName
	}
	return nil, "", clusterName
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
