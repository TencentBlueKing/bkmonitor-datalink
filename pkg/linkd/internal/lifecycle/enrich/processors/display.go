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
	"regexp"
	"sort"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/basetarget"
	"linkd/internal/lifecycle/enrich/collect"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
	uptimeenrich "linkd/internal/lifecycle/enrich/uptime"
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
	if err != nil {
		return failedDependency(rules.DependencyKingeyeStrategy), nil
	}
	businessMatches, err := strategyBusinessMatches(ctx, scope, projection.BKBizID, ids.BizID)
	if contextErr := ctx.Err(); contextErr != nil {
		return enrich.ProcessorResult{}, contextErr
	}
	if err != nil {
		return failedDependency(rules.DependencyBusiness), nil
	}
	if !businessMatches {
		return failedDependency(rules.DependencyKingeyeStrategy), nil
	}
	classification := rules.Classify(strategy, alert.Dimensions)
	objectName := cleanDisplayObject(alert, strategy, scope.Context().Resource.Values)
	displayDiagnostics := make([]enrich.Diagnostic, 0, 1)
	var uptimeResult *uptimeenrich.Result
	if classification.BaseTarget == rules.BaseTargetUptimeCheck {
		result, uptimeErr := uptimeenrich.Enrich(ctx, scope, *strategy.ObjectModelCode, alert.Dimensions, ids.BizID)
		if uptimeErr != nil {
			return enrich.ProcessorResult{}, uptimeErr
		}
		uptimeResult = &result
		objectName = result.Object
		displayDiagnostics = append(displayDiagnostics, result.DisplayDiagnostics...)
	}
	if classification.Main == rules.MainData {
		objectName = ""
		if strings.HasPrefix(projection.QueryConfigs[0].ResultTableID, "uptimecheck") {
			result, uptimeErr := uptimeenrich.Enrich(ctx, scope, rules.UptimeModelCode, alert.Dimensions, ids.BizID)
			if uptimeErr != nil {
				return enrich.ProcessorResult{}, uptimeErr
			}
			uptimeResult = &result
			objectName = result.Object
			displayDiagnostics = append(displayDiagnostics, result.DisplayDiagnostics...)
		}
	} else if classification.BaseTarget == rules.BaseTargetCollectTask {
		result, collectErr := collect.Enrich(ctx, scope, alert.Dimensions, ids.BizID, alert.SubjectName)
		if collectErr != nil {
			return enrich.ProcessorResult{}, collectErr
		}
		objectName = result.Object
	} else if classification.Main != rules.MainData && classification.BaseTarget != rules.BaseTargetUptimeCheck {
		result, targetErr := basetarget.Enrich(ctx, scope, classification, strategy, alert.Dimensions, ids.BizID, alert.SubjectName)
		if targetErr != nil {
			return enrich.ProcessorResult{}, targetErr
		}
		objectName = result.Object
		displayDiagnostics = append(displayDiagnostics, result.DisplayDiagnostics...)
	}
	itemName, err := displayItem(ctx, scope, strategy, projection)
	if rules.IsLogDisplay(classification.Main) {
		itemName = sourceConfigString(strategy.Spec.SourceConfig, rules.FieldQueryString)
		if itemName == "" {
			itemName = rules.LogMetricFallback
		}
	}
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
	var dimensions []models.DimensionDisplay
	var metricMetadata models.MetricMetadata
	if classification.Main == rules.MainData && uptimeResult == nil {
		mapping, mappingErr := dataMetricMetadata(ctx, scope, strategy, projection)
		if mappingErr != nil {
			return failedDependency(rules.DependencyMetricLibrary), nil
		}
		metricMetadata = mapping
	}
	if uptimeResult != nil {
		dimensions = uptimeResult.Dimensions
	} else {
		dimensions, err = buildDisplayDimensions(ctx, scope, strategy, projection, alertDimensions, additional)
	}
	if err != nil {
		return failedDependency(rules.DependencyMetricLibrary), nil
	}
	title := cleanDisplayTitle(classification.Main, strategy, alert, objectName, itemName)
	dimensionText := buildDimensionText(
		dimensions,
		classification.Main,
		scope.Context().Resource.Values,
		projection.QueryConfigs[0].ResultTableID,
		additional,
	)
	content := alert.Content
	if rules.IsLogDisplay(classification.Main) {
		content = logDisplayContent(content, classification.Main, sourceConfigString(strategy.Spec.SourceConfig, rules.FieldQueryString))
	} else {
		content = applyDataContentAlgorithm(content, strategy, metricMetadata, alert.Severity)
	}
	if classification.Main == rules.MainData {
		content = enrichDataAlgorithmContent(content, metricMetadata.ValueMapping)
	}
	values := models.DisplayValues{
		Title: title, Content: content, Object: objectName,
		Dimensions: dimensions, DimensionText: dimensionText,
	}
	scope.Context().Display.Set(values)
	value, encodeErr := scope.Context().Display.JSONObject()
	if encodeErr != nil {
		return enrich.ProcessorResult{}, encodeErr
	}
	status := domain.EnrichStatusSucceeded
	if len(displayDiagnostics) != 0 {
		status = domain.EnrichStatusPartial
	}
	return enrich.ProcessorResult{Status: status, Value: value, Diagnostics: displayDiagnostics}, nil
}

func logDisplayContent(content string, classification rules.DisplayClassification, query string) string {
	content = strings.SplitN(content, ",关联信息", 2)[0]
	if classification == rules.DisplayLogMetric {
		return content
	}
	if strings.Contains(content, "无数据") {
		text := content
		if index := strings.LastIndex(text, ")"); index >= 0 && index+1 < len(text) {
			text = text[index+1:]
		}
		return fmt.Sprintf("【%s】关键字%s", query, text)
	}
	pattern := regexp.MustCompile(`(?:[<>=!]=?|大于等于|大于|小于等于|小于|等于|不等于)\s*\d+\.?\d*\s*,\s*当前值\s*\d+`)
	match := pattern.FindString(content)
	if match == "" {
		match = content
	} else if index := strings.Index(content, match); index >= 0 {
		match = content[index:]
	}
	return fmt.Sprintf("匹配到【%s】关键字次数 %s", query, match)
}

func dataMetricMetadata(ctx context.Context, scope *enrich.Scope, strategy models.CWStrategy, projection models.StrategyItemProjection) (models.MetricMetadata, error) {
	query := projection.QueryConfigs[0]
	objectModelCode := ""
	if strategy.ObjectModelCode != nil {
		objectModelCode = *strategy.ObjectModelCode
	}
	metricQuery := models.MetricLibraryQuery{TableID: query.ResultTableID, FieldName: query.MetricField, ObjectModelCode: objectModelCode}
	if strategy.Kind != models.CWStrategyKindCloud && strategy.Spec.FieldTag == models.CWStrategyFieldTagDerivedMetric {
		metricQuery.TableID = ""
		metricQuery.FieldName = strategy.Spec.FieldName
		metricQuery.FieldTag = models.CWStrategyFieldTagDerivedMetric
	}
	metadata, found, err := scope.MetricLibrary(ctx, metricQuery)
	if err != nil || !found {
		return models.MetricMetadata{}, err
	}
	if len(projection.QueryConfigs) > 1 || hasFunctions(projection.Functions) || hasFunctions(query.Functions) {
		return models.MetricMetadata{}, nil
	}
	return metadata, nil
}

func applyDataContentAlgorithm(content string, strategy models.CWStrategy, metadata models.MetricMetadata, severity string) string {
	for _, item := range metadata.ValueMapping {
		if item.OriginalValue == "" || item.MappedValue == "" {
			continue
		}
		content = replaceMetricValue(content, item.OriginalValue, item.MappedValue)
	}
	if isDataThresholdContent(content) {
		return appendMetricUnit(content, effectiveMetricUnit(strategy, metadata, severity))
	}
	return content
}

func enrichDataAlgorithmContent(content string, mapping []models.MetricValueMapping) string {
	for _, item := range mapping {
		if item.OriginalValue == "" || item.MappedValue == "" {
			continue
		}
		content = annotateAlgorithmValues(content, item.OriginalValue, item.MappedValue)
	}
	return content
}

func annotateAlgorithmValues(content, original, mapped string) string {
	parts := strings.Split(content, ",")
	for index, part := range parts {
		if strings.Contains(part, "无数据") || strings.Contains(part, "无数据上报") {
			continue
		}
		if strings.Contains(part, "同一时刻绝对值") || strings.Contains(part, "前一时刻值") {
			parts[index] = replaceMetricValue(part, original, mapped)
			continue
		}
		if strings.Contains(part, "较前一时刻") || strings.Contains(part, "时间点的均值") || strings.Contains(part, "上周同一时刻") || strings.Contains(part, "同一时刻差值") {
			parts[index] = replaceMetricValue(part, original, mapped)
		}
	}
	return strings.Join(parts, ",")
}

func isDataThresholdContent(content string) bool {
	return regexp.MustCompile(`\s[><=!]`).MatchString(content) &&
		!strings.Contains(content, "前一时刻值") && !strings.Contains(content, "同一时刻绝对值")
}

func effectiveMetricUnit(strategy models.CWStrategy, metadata models.MetricMetadata, severity string) string {
	for _, algorithm := range strategy.Spec.StrategyDetectAlgorithms {
		if algorithm.LevelStatus != severity || algorithm.AlgorithmConfig == nil {
			continue
		}
		if raw, ok := algorithm.AlgorithmConfig["algorithmUnit"]; ok {
			var unit string
			if json.Unmarshal(raw, &unit) == nil {
				if unit == "NONE" {
					return ""
				}
				if unit != "" {
					return unit
				}
			}
		}
	}
	switch metadata.Unit {
	case "bytes":
		return "B"
	case "percent", "percentunit":
		return "%"
	default:
		return metadata.Unit
	}
}

func appendMetricUnit(content, unit string) string {
	if unit == "" {
		return content
	}
	pattern := `(?i)(>=|<=|>|<|=|current value is)\s*(-?[0-9]+(?:\.[0-9]+)?)`
	re := regexp.MustCompile(pattern)
	return re.ReplaceAllStringFunc(content, func(match string) string {
		return match + unit
	})
}

func replaceMetricValue(content, original, mapped string) string {
	pattern := `(^|[^[:alnum:]_.-])(` + regexp.QuoteMeta(original) + `)($|[^[:alnum:]_.-])`
	re := regexp.MustCompile(pattern)
	return re.ReplaceAllStringFunc(content, func(match string) string {
		index := strings.Index(match, original)
		if index < 0 {
			return match
		}
		end := index + len(original)
		return match[:end] + "(" + mapped + ")" + match[end:]
	})
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
		{rules.FieldModelID, "对象模型ID"}, {rules.FieldObjectModelID, "对象模型ID"},
		{rules.FieldModelInstID, "对象模型实例ID"}, {rules.FieldObjectModelInstID, "对象模型实例ID"},
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
	case rules.FieldModelID, rules.FieldObjectModelID:
		translated = resource.ModelName
	case rules.FieldModelInstID, rules.FieldObjectModelInstID:
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
