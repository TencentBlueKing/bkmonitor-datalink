// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"encoding/json"
	"regexp"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const defaultDataScopeName = "default"

const DISABLE_SCOPE_ID = 0

// scopeDimensionInfo
type scopeDimensionInfo struct {
	dimensions        mapset.Set[string]
	createFromDefault bool
}

// isMatchAutoRules
func isMatchAutoRules(autoRulesJSON string, fieldName string) bool {
	if autoRulesJSON == "" || autoRulesJSON == "[]" {
		return false
	}
	var rules []string
	if err := json.Unmarshal([]byte(autoRulesJSON), &rules); err != nil {
		return false
	}
	for _, rule := range rules {
		re, err := regexp.Compile(rule)
		if err != nil {
			logger.Warnf("BulkRefreshTSScopes: invalid regex in auto_rules: %s", rule)
			continue
		}
		if re.MatchString(fieldName) {
			return true
		}
	}
	return false
}

// determineScopeNameForNewMetric
func determineScopeNameForNewMetric(svc *TimeSeriesGroupSvc, fieldName, fieldScope string, allScopes []customreport.TimeSeriesScope) (scopeName string, createFromDefault bool) {
	isDefault, defaultName := svc.IsDefaultScopeInfo(fieldScope)
	if isDefault {
		prefix := GetScopeNamePrefix(fieldScope)
		for i := range allScopes {
			scope := &allScopes[i]
			scopePrefix := GetScopeNamePrefix(scope.ScopeName)
			if prefix != scopePrefix {
				continue
			}
			if isMatchAutoRules(scope.AutoRules, fieldName) {
				return scope.ScopeName, false
			}
		}
		return defaultName, true
	}
	return fieldScope, false
}

// getDimensionKeysFromMetricInfo
func getDimensionKeysFromMetricInfo(item map[string]any) []string {
	tags := mapset.NewSet[string]()
	if tagValueList, ok := item["tag_value_list"].(map[string]any); ok {
		for k := range tagValueList {
			tags.Add(k)
		}
	} else if tagList, ok := item["tag_list"].([]any); ok {
		for _, t := range tagList {
			if m, ok := t.(map[string]any); ok {
				if fn, ok := m["field_name"].(string); ok && fn != "" {
					tags.Add(fn)
				}
			}
		}
	}
	return tags.ToSlice()
}

// collectMetricsAndDimensions
func collectMetricsAndDimensions(svc *TimeSeriesGroupSvc, metricInfoList []map[string]any) (scopeNameToMetrics map[string][]map[string]any, scopeNameToDimensions map[string]*scopeDimensionInfo, err error) {
	type fieldKey struct {
		fieldName  string
		fieldScope string
	}
	var fieldKeys []fieldKey
	for _, m := range metricInfoList {
		fn, _ := m["field_name"].(string)
		if fn == "" {
			continue
		}
		fs, _ := m["field_scope"].(string)
		if fs == "" {
			fs = defaultDataScopeName
		}
		fieldKeys = append(fieldKeys, fieldKey{fn, fs})
	}
	if len(fieldKeys) == 0 {
		return map[string][]map[string]any{}, map[string]*scopeDimensionInfo{}, nil
	}

	db := mysql.GetDBSession().DB
	var existingMetrics []customreport.TimeSeriesMetric
	if err := customreport.NewTimeSeriesMetricQuerySet(db).Select(
		customreport.TimeSeriesMetricDBSchema.FieldID,
		customreport.TimeSeriesMetricDBSchema.FieldName,
		customreport.TimeSeriesMetricDBSchema.FieldScope,
	).GroupIDEq(svc.TimeSeriesGroupID).All(&existingMetrics); err != nil {
		return nil, nil, errors.Wrap(err, "query existing TimeSeriesMetric")
	}

	var allScopes []customreport.TimeSeriesScope
	if err := customreport.NewTimeSeriesScopeQuerySet(db).GroupIDEq(svc.TimeSeriesGroupID).OrderDescByLastModifyTime().All(&allScopes); err != nil {
		return nil, nil, errors.Wrapf(err, "query TimeSeriesScope group_id [%v]", svc.TimeSeriesGroupID)
	}

	scopeIDToName := make(map[uint]string)
	for i := range allScopes {
		scopeIDToName[allScopes[i].ID] = allScopes[i].ScopeName
	}
	scopeIDToName[DISABLE_SCOPE_ID] = ""

	existingMetricScopeMap := make(map[fieldKey]string)
	for _, m := range existingMetrics {
		fs := m.FieldScope
		if fs == "" {
			fs = defaultDataScopeName
		}
		k := fieldKey{m.FieldName, fs}
		existingMetricScopeMap[k] = scopeIDToName[m.ScopeID]
	}

	scopeNameToMetrics = make(map[string][]map[string]any)
	scopeNameToDimensions = make(map[string]*scopeDimensionInfo)

	for _, item := range metricInfoList {
		fieldName, _ := item["field_name"].(string)
		if fieldName == "" {
			continue
		}
		fieldScope, _ := item["field_scope"].(string)
		if fieldScope == "" {
			fieldScope = defaultDataScopeName
		}
		// 判断传入数据是否包含 values (tag_value_list/tag_list)
		tagList := getDimensionKeysFromMetricInfo(item)

		scopeName := existingMetricScopeMap[fieldKey{fieldName, fieldScope}]
		if scopeName == "" {
			// 不在已有指标中，或该指标已被 disabled，重新激活需重新分配分组
			newScopeName, createFromDefault := determineScopeNameForNewMetric(svc, fieldName, fieldScope, allScopes)
			if scopeNameToDimensions[newScopeName] == nil {
				scopeNameToDimensions[newScopeName] = &scopeDimensionInfo{
					dimensions:        mapset.NewSet[string](),
					createFromDefault: createFromDefault,
				}
			}
			scopeName = newScopeName
		}
		scopeNameToMetrics[scopeName] = append(scopeNameToMetrics[scopeName], item)
		if scopeNameToDimensions[scopeName] == nil {
			// 来自已有指标的 scope，已存在的指标忽略 create_from_default
			scopeNameToDimensions[scopeName] = &scopeDimensionInfo{
				dimensions:        mapset.NewSet[string](),
				createFromDefault: false,
			}
		}
		for _, dim := range tagList {
			scopeNameToDimensions[scopeName].dimensions.Add(dim)
		}
	}

	// 检查并补充缺失的默认分组
	checkedPrefixes := mapset.NewSet[string]()
	for scopeName := range scopeNameToDimensions {
		prefix := GetScopeNamePrefix(scopeName)
		if prefix == "" {
			continue
		}
		if checkedPrefixes.Contains(prefix) {
			continue
		}
		checkedPrefixes.Add(prefix)
		defaultScopeName := prefix + "||" + defaultDataScopeName
		if _, exists := scopeNameToDimensions[defaultScopeName]; !exists {
			scopeNameToDimensions[defaultScopeName] = &scopeDimensionInfo{
				dimensions:        mapset.NewSet[string](),
				createFromDefault: true,
			}
		}
	}
	return scopeNameToMetrics, scopeNameToDimensions, nil
}

// doBulkRefreshTSScopes : 批量刷新 TimeSeriesScope
func doBulkRefreshTSScopes(groupID uint, scopeNameToDimensions map[string]*scopeDimensionInfo) error {
	db := mysql.GetDBSession().DB
	var existingScopes []customreport.TimeSeriesScope
	if err := customreport.NewTimeSeriesScopeQuerySet(db).GroupIDEq(groupID).All(&existingScopes); err != nil {
		return errors.Wrapf(err, "query existing scopes group_id [%v]", groupID)
	}
	existsScopeNameToObj := make(map[string]*customreport.TimeSeriesScope)
	for i := range existingScopes {
		existsScopeNameToObj[existingScopes[i].ScopeName] = &existingScopes[i]
	}

	var scopesToCreate []customreport.TimeSeriesScope
	var scopesToUpdate []*customreport.TimeSeriesScope

	for scopeName, info := range scopeNameToDimensions {
		dimensions := info.dimensions.ToSlice()
		createFromDefault := info.createFromDefault
		scope, exists := existsScopeNameToObj[scopeName]
		if exists {
			var dimensionConfig map[string]any
			if scope.DimensionConfig != "" {
				_ = json.Unmarshal([]byte(scope.DimensionConfig), &dimensionConfig)
			}
			if dimensionConfig == nil {
				dimensionConfig = make(map[string]any)
			}
			newDims := false
			for _, dim := range dimensions {
				if _, has := dimensionConfig[dim]; !has {
					dimensionConfig[dim] = map[string]any{}
					newDims = true
				}
			}
			if newDims {
				configJSON, _ := json.Marshal(dimensionConfig)
				scope.DimensionConfig = string(configJSON)
				scopesToUpdate = append(scopesToUpdate, scope)
			}
		} else {
			dimensionConfig := make(map[string]any)
			for _, dim := range dimensions {
				dimensionConfig[dim] = map[string]any{}
			}
			configJSON, _ := json.Marshal(dimensionConfig)
			autoRulesJSON, _ := json.Marshal([]string{})
			createFrom := "data"
			if createFromDefault {
				createFrom = "default"
			}
			scopesToCreate = append(scopesToCreate, customreport.TimeSeriesScope{
				GroupID:         groupID,
				ScopeName:       scopeName,
				DimensionConfig: string(configJSON),
				AutoRules:       string(autoRulesJSON),
				CreateFrom:      createFrom,
			})
		}
	}
	if len(scopesToCreate) > 0 {
		// 项目是 github.com/jinzhu/gorm v1.9.16，这个版本对 slice 批量创建并不稳定，容易触发你这个 panic：reflect.Value.Interface on zero Value
		tx := db.Begin()
		if tx.Error != nil {
			return errors.Wrap(tx.Error, "begin tx for create TimeSeriesScope")
		}

		for i := range scopesToCreate {
			if err := tx.Create(&scopesToCreate[i]).Error; err != nil {
				tx.Rollback()
				return errors.Wrap(err, "create TimeSeriesScope")
			}
		}

		if err := tx.Commit().Error; err != nil {
			return errors.Wrap(err, "commit tx for create TimeSeriesScope")
		}
	}
	logger.Infof("doBulkRefreshTSScopes: create TimeSeriesScope success: len(%d)", len(scopesToCreate))

	for _, scope := range scopesToUpdate {
		if err := scope.Update(db, customreport.TimeSeriesScopeDBSchema.DimensionConfig); err != nil {
			logger.Warnf("doBulkRefreshTSScopes: update scope [%s] dimension_config failed: %v", scope.ScopeName, err)
		}
	}
	logger.Infof("doBulkRefreshTSScopes: update TimeSeriesScope success: len(%d)", len(scopesToUpdate))
	return nil
}

// BulkRefreshTSScopes ：批量刷新 scope 并返回包含 scope_id 的指标列表
func BulkRefreshTSScopes(svc *TimeSeriesGroupSvc, metricInfoList []map[string]any) ([]map[string]any, error) {
	if len(metricInfoList) == 0 {
		return metricInfoList, nil
	}
	scopeNameToMetrics, scopeNameToDimensions, err := collectMetricsAndDimensions(svc, metricInfoList)
	if err != nil {
		return nil, err
	}
	if err := doBulkRefreshTSScopes(svc.TimeSeriesGroupID, scopeNameToDimensions); err != nil {
		return nil, err
	}

	db := mysql.GetDBSession().DB
	var allScopes []customreport.TimeSeriesScope
	if err := customreport.NewTimeSeriesScopeQuerySet(db).GroupIDEq(svc.TimeSeriesGroupID).Select(
		customreport.TimeSeriesScopeDBSchema.ScopeName, customreport.TimeSeriesScopeDBSchema.ID).All(&allScopes); err != nil {
		return nil, errors.Wrapf(err, "query scopes after refresh")
	}
	scopeNameToID := make(map[string]uint)
	for i := range allScopes {
		scopeNameToID[allScopes[i].ScopeName] = allScopes[i].ID
	}

	// 为每个指标添加 scope_id
	newMetricInfoList := make([]map[string]any, 0, len(metricInfoList))
	for scopeName, metrics := range scopeNameToMetrics {
		scopeID := scopeNameToID[scopeName]
		for _, metricInfo := range metrics {
			newItem := make(map[string]any)
			for k, v := range metricInfo {
				newItem[k] = v
			}
			newItem["scope_id"] = scopeID
			newMetricInfoList = append(newMetricInfoList, newItem)
		}
	}
	return newMetricInfoList, nil
}
