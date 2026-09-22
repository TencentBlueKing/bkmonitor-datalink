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
	"strconv"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

// Log 丰富日志指标和日志关键字告警的日志主题及查询信息。
type Log struct{}

// Name 返回稳定的 Processor 名称。
func (Log) Name() string { return rules.LogProcessor }

// Match 判断当前告警是否属于日志场景。
func (Log) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 按策略 source_config 读取日志主题和查询语句。
func (Log) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
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
		return logFailure(rules.DependencyKingeyeStrategy)
	}
	classification := rules.ClassifyDisplay(strategy)
	if !rules.IsLogDisplay(classification) {
		return enrich.ProcessorResult{Status: domain.EnrichStatusSkipped, Value: domain.JSONObject{}}, nil
	}
	query := sourceConfigString(strategy.Spec.SourceConfig, rules.FieldQueryString)
	relatedInfo, relatedValid := logRelatedInfo(alert.ExtraData)
	if !relatedValid {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"extra_data.log_related_info"}})
	}
	themeID, themeName := logTheme(strategy.Spec.SourceConfig)
	if themeID == "" {
		diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeMissingField, Fields: []string{"spec.source_config.log_theme_id"}})
	} else if parsedID, parseErr := strconv.ParseInt(themeID, 10, 64); parseErr == nil {
		theme, themeFound, themeErr := scope.LogTheme(ctx, parsedID)
		if themeErr == nil && themeFound && theme.TenantID == alert.BKTenantID && theme.ID == parsedID {
			themeName = theme.Name
		} else if (themeErr != nil && !strings.Contains(themeErr.Error(), "unavailable")) || (themeFound && (theme.TenantID != alert.BKTenantID || theme.ID != parsedID)) {
			diagnostics = append(diagnostics, enrich.Diagnostic{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: rules.DependencyLogTheme})
		}
	}
	if themeName == "" {
		themeName = sourceConfigString(strategy.Spec.SourceConfig, "log_theme_name")
	}
	values := models.LogValues{LogThemeID: logThemeValue(themeID), LogThemeName: themeName, LogQueryString: query, LogRelateInfo: relatedInfo, CWLabels: logLabels(ids.BizID, themeID)}
	if classification == rules.DisplayLogKeyword && query == "" {
		values.LogQueryString = rules.LogMetricFallback
	}
	if classification == rules.DisplayLogMetric {
		values.LogRelateInfo = ""
	}
	value, encodeErr := json.Marshal(values)
	if encodeErr != nil {
		return enrich.ProcessorResult{}, encodeErr
	}
	var resultValue domain.JSONObject
	if err := json.Unmarshal(value, &resultValue); err != nil {
		return enrich.ProcessorResult{}, err
	}
	status := domain.EnrichStatusSucceeded
	if len(diagnostics) != 0 {
		status = domain.EnrichStatusPartial
	}
	return enrich.ProcessorResult{Status: status, Value: resultValue, Diagnostics: diagnostics}, nil
}

func logFailure(dependency string) (enrich.ProcessorResult, error) {
	return enrich.ProcessorResult{Status: domain.EnrichStatusFailed, Value: domain.JSONObject{}, Diagnostics: []enrich.Diagnostic{{Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: dependency}}}, nil
}

func logTheme(source domain.JSONObject) (string, string) {
	raw := source["log_theme_id"]
	var id string
	if len(raw) != 0 {
		_ = json.Unmarshal(raw, &id)
		if id == "" {
			var number json.Number
			if json.Unmarshal(raw, &number) == nil {
				id = number.String()
			}
		}
	}
	return id, sourceConfigString(source, "log_theme_name")
}

func logThemeValue(id string) any {
	if id == "" {
		return nil
	}
	if strings.HasPrefix(id, "-") {
		return id
	}
	var value int64
	if _, err := fmt.Sscan(id, &value); err == nil {
		return value
	}
	return id
}

func logRelatedInfo(extra domain.JSONObject) (string, bool) {
	raw := extra["log_related_info"]
	if len(raw) == 0 {
		return "", true
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return "", false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func logLabels(bizID int64, themeID string) []string {
	labels := []string{"bk_biz_id", fmt.Sprintf("bk_biz_id|%d", bizID)}
	if themeID != "" {
		labels = append(labels, "log", "log|"+themeID)
	}
	return labels
}
