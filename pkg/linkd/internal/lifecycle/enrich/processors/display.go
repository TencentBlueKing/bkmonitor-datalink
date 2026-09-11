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
	"sort"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

// Display 丰富告警的展示信息。
type Display struct{}

// Name 返回稳定的 Processor 名称。
func (Display) Name() string { return rules.DisplayProcessor }

// Match 判断当前告警是否适用 Display。
func (Display) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 生成告警展示字段。
func (Display) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	alert := scope.Alert()
	ids, diagnostics := enrich.ValidateRequiredIDs(alert)
	if len(diagnostics) != 0 {
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: diagnostics}, nil
	}
	strategy, found, err := scope.CWStrategyByBKStrategyID(ctx, ids.StrategyID)
	if ctx.Err() != nil {
		return enrich.ProcessorResult{}, ctx.Err()
	}
	if err != nil || !found {
		return failedDependency(rules.DependencyKingeyeStrategy), nil
	}
	projection, err := strategy.StrategyItemProjection()
	if err != nil || projection.BKBizID != ids.BizID {
		return failedDependency(rules.DependencyKingeyeStrategy), nil
	}
	objectName := cleanDisplayObject(alert, strategy, scope.Context().Resource.Values)
	itemName, err := displayItem(ctx, scope, strategy, projection)
	if err != nil {
		return failedDependency(rules.DependencyMetricLibrary), nil
	}
	additional, err := additionalDimensions(alert.ExtraData)
	if err != nil {
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: []enrich.Diagnostic{{
			Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"extra_data.additional_dimensions"},
		}}}, nil
	}
	alertDimensions, err := combinedDimensions(alert)
	if err != nil {
		return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: []enrich.Diagnostic{{
			Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"extra_data.additional_dimensions"},
		}}}, nil
	}
	dimensions, err := buildDisplayDimensions(ctx, scope, strategy, projection, alertDimensions, additional)
	if err != nil {
		return failedDependency(rules.DependencyMetricLibrary), nil
	}
	classification := rules.ClassifyDisplay(strategy)
	title := cleanDisplayTitle(classification, strategy, alert, objectName, itemName)
	dimensionText := buildDimensionText(
		dimensions,
		classification,
		scope.Context().Resource.Values,
		projection.QueryConfigs[0].ResultTableID,
		additional,
	)
	values := models.DisplayValues{
		Title: title, Content: alert.Content, Object: objectName,
		Dimensions: dimensions, DimensionText: dimensionText,
	}
	scope.Context().Display.Set(values)
	value, encodeErr := scope.Context().Display.JSONObject()
	if encodeErr != nil {
		return enrich.ProcessorResult{}, encodeErr
	}
	return enrich.ProcessorResult{Status: domain.EnrichStatusSucceeded, Value: value}, nil
}

func cleanDisplayTitle(
	classification rules.DisplayClassification,
	strategy models.CWStrategy,
	alert domain.Alert,
	objectName, itemName string,
) string {
	if strategy.Spec.AlarmAlias != "" {
		return strategy.Spec.AlarmAlias
	}
	switch classification {
	case rules.DisplayData:
		return itemName + "发生了告警"
	case rules.DisplayLogMetric:
		return alert.SubjectName + "发生了" + itemName + "告警"
	case rules.DisplayLogKeyword:
		queryString := sourceConfigString(strategy.Spec.SourceConfig, rules.FieldQueryString)
		return alert.SubjectName + "发生了【" + queryString + "】关键字告警"
	default:
		return objectName + "发生了" + itemName + "告警"
	}
}

func cleanDisplayObject(
	alert domain.Alert,
	strategy models.CWStrategy,
	resource models.ResourceValues,
) string {
	classification := rules.ClassifyDisplay(strategy)
	if rules.IsLogDisplay(classification) {
		return ""
	}
	if classification == rules.DisplayCloud {
		resourceName := displayResourceName(resource)
		cloudName := resource.BKCloudName
		if cloudName == "" {
			cloudName = fmt.Sprint(resource.CloudPlatformID)
		}
		switch {
		case resourceName != "" && cloudName != "" && cloudName != "<nil>":
			return resourceName + "(" + cloudName + ")"
		case resourceName != "":
			return resourceName
		}
	}
	if rules.IsAPMTable(strategy.Spec.TableID) {
		if serviceName := rules.DimensionText(alert.Dimensions, rules.FieldServiceName); serviceName != "" {
			return serviceName
		}
	}
	if strategy.ObjectModelCode != nil && *strategy.ObjectModelCode == rules.K8sNodeModelCode {
		if alert.SubjectName != "" {
			return alert.SubjectName
		}
		if node := rules.DimensionText(alert.Dimensions, rules.FieldNode); node != "" {
			return node
		}
		if instance := rules.DimensionText(alert.Dimensions, rules.FieldInstance); instance != "" {
			return strings.SplitN(instance, ":", 2)[0]
		}
		return ""
	}
	if strategy.ObjectModelCode != nil && *strategy.ObjectModelCode == rules.HostModelCode {
		if alert.SubjectName != "" {
			return alert.SubjectName
		}
		if ip := rules.DimensionText(alert.Dimensions, rules.FieldIP); ip != "" {
			return ip
		}
		return rules.DimensionText(alert.Dimensions, rules.FieldBKTargetIP)
	}
	return alert.SubjectName
}

func displayResourceName(resource models.ResourceValues) string {
	if resource.ModelName != "" {
		return resource.ModelName
	}
	if resource.BKInstID != nil {
		value := fmt.Sprint(resource.BKInstID)
		if value != "<nil>" {
			return value
		}
	}
	return ""
}

func displayItem(
	ctx context.Context,
	scope *enrich.Scope,
	strategy models.CWStrategy,
	projection models.StrategyItemProjection,
) (string, error) {
	return cleanItem(ctx, scope, strategy, projection, projection.QueryConfigs[0])
}

func buildDisplayDimensions(
	ctx context.Context,
	scope *enrich.Scope,
	strategy models.CWStrategy,
	projection models.StrategyItemProjection,
	dimensions domain.DimensionMap,
	additional domain.DimensionMap,
) ([]models.DimensionDisplay, error) {
	query := projection.QueryConfigs[0]
	objectModelCode := ""
	if strategy.ObjectModelCode != nil {
		objectModelCode = *strategy.ObjectModelCode
	}
	metadata, found, err := scope.MetricLibrary(ctx, models.MetricLibraryQuery{
		TableID: query.ResultTableID, FieldName: query.MetricField, ObjectModelCode: objectModelCode,
	})
	if err != nil {
		return nil, err
	}
	nameByKey := map[string]string{rules.FieldBKBizID: "业务", rules.FieldBKTargetCloudID: "云区域"}
	if found {
		for _, dimension := range metadata.Dimensions {
			if dimension.Key != "" {
				nameByKey[dimension.Key] = dimension.Name
			}
		}
	}

	result := make([]models.DimensionDisplay, 0, len(query.AggregateBy)+2)
	seen := make(map[string]struct{}, len(query.AggregateBy)+2)
	resource := scope.Context().Resource.Values
	appendDimension := func(key, name string) {
		value, exists := dimensions[key]
		if !exists {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		realKey := key
		realValue := value
		displayValue := displayDimensionValue(key, value, resource)
		result = append(result, models.DimensionDisplay{
			Name: name, Value: displayValue, RealKey: &realKey, RealValue: &realValue,
		})
		seen[key] = struct{}{}
	}

	// 对象模型和实例条目沿用 Converter 的固定生成顺序。
	for _, field := range []struct{ key, name string }{
		{rules.FieldCWObjectModelID, "对象模型ID"}, {rules.FieldObjectModelID, "对象模型ID"},
		{rules.FieldCWObjectModelInstID, "对象模型实例ID"}, {rules.FieldObjectModelInstID, "对象模型实例ID"},
	} {
		appendDimension(field.key, field.name)
	}
	for _, key := range query.AggregateBy {
		name, exists := nameByKey[key]
		if !exists {
			continue
		}
		appendDimension(key, name)
	}
	result = appendAdditionalDisplayDimensions(result, additional, nameByKey, resource)
	return result, nil
}

func appendAdditionalDisplayDimensions(
	entries []models.DimensionDisplay,
	additional domain.DimensionMap,
	nameByKey map[string]string,
	resource models.ResourceValues,
) []models.DimensionDisplay {
	seen := make(map[string]struct{}, len(entries)+len(additional))
	for _, entry := range entries {
		if entry.RealKey != nil {
			seen[*entry.RealKey] = struct{}{}
		}
	}
	keys := make([]string, 0, len(additional))
	for key := range additional {
		if _, exists := seen[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := additional[key]
		realKey := key
		realValue := value
		name := nameByKey[key]
		if name == "" {
			name = key
		}
		entries = append(entries, models.DimensionDisplay{
			Name: name, Value: displayDimensionValue(key, value, resource), RealKey: &realKey, RealValue: &realValue,
		})
	}
	return entries
}

func displayDimensionValue(
	key string,
	original domain.Scalar,
	resource models.ResourceValues,
) domain.Scalar {
	var translated string
	switch key {
	case rules.FieldCWObjectModelID, rules.FieldObjectModelID:
		translated = resource.ModelName
	case rules.FieldCWObjectModelInstID, rules.FieldObjectModelInstID:
		translated = fmt.Sprint(resource.BKInstID)
	case rules.FieldBKBizID:
		translated = resource.BKBizName
	case rules.FieldBKCloudID, rules.FieldBKTargetCloudID, rules.FieldCloudID:
		translated = resource.BKCloudName
	}
	if translated == "" || translated == "<nil>" {
		return original
	}
	return domain.NewStringScalar(translated)
}

func buildDimensionText(
	entries []models.DimensionDisplay,
	classification rules.DisplayClassification,
	resource models.ResourceValues,
	resultTableID string,
	additional domain.DimensionMap,
) string {
	copied := append([]models.DimensionDisplay(nil), entries...)
	sort.SliceStable(copied, func(i, j int) bool { return copied[i].Name < copied[j].Name })
	parts := make([]string, 0, len(copied))
	for _, entry := range copied {
		if entry.Name == rules.FieldBKBizID || excludeAPMDimension(entry, resultTableID) {
			continue
		}
		if entry.RealKey != nil {
			if _, exists := additional[*entry.RealKey]; exists {
				parts = append(parts, fmt.Sprintf("%s(%v)", entry.Name, rules.ScalarValue(entry.Value)))
				continue
			}
		}
		parts = appendDimensionText(parts, entry, resource)
	}
	text := strings.Join(parts, " | ")
	if classification == rules.DisplayLogKeyword {
		text = strings.ReplaceAll(text, "keyword(", "日志关键字(")
	}
	return text
}

func excludeAPMDimension(entry models.DimensionDisplay, resultTableID string) bool {
	if !rules.IsAPMTable(resultTableID) || entry.RealKey == nil {
		return false
	}
	return rules.IsAPMDimension(*entry.RealKey)
}

func appendDimensionText(
	parts []string,
	entry models.DimensionDisplay,
	resource models.ResourceValues,
) []string {
	realKey := ""
	if entry.RealKey != nil {
		realKey = strings.ToLower(*entry.RealKey)
	}
	if realKey == rules.FieldBKBizID {
		if resource.BKBizID != nil {
			return append(parts, fmt.Sprintf("%s=%v(%s)", entry.Name, resource.BKBizID, resource.BKBizName))
		}
		if rules.ScalarProvided(entry.Value) && entry.RealValue != nil {
			return append(parts, fmt.Sprintf("%s=%v(%v)", entry.Name, rules.ScalarValue(*entry.RealValue), rules.ScalarValue(entry.Value)))
		}
		return parts
	}
	if rules.IsIdentityDimension(realKey) && rules.ScalarProvided(entry.Value) && entry.RealValue != nil {
		return append(parts, fmt.Sprintf("%s=%v(%v)", entry.Name, rules.ScalarValue(*entry.RealValue), rules.ScalarValue(entry.Value)))
	}
	value := rules.ScalarValue(entry.Value)
	if !rules.ScalarProvided(entry.Value) && entry.RealValue != nil {
		value = rules.ScalarValue(*entry.RealValue)
	}
	return append(parts, fmt.Sprintf("%s=%v", entry.Name, value))
}
