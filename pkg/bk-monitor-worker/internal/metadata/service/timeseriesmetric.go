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
	"fmt"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mapx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// TimeSeriesMetricSvc time series metric service
type TimeSeriesMetricSvc struct {
	*customreport.TimeSeriesMetric
}

func NewTimeSeriesMetricSvcSvc(obj *customreport.TimeSeriesMetric) TimeSeriesMetricSvc {
	return TimeSeriesMetricSvc{
		TimeSeriesMetric: obj,
	}
}

// metricKey 用于按 (field_name, field_scope) 唯一标识指标
func metricKey(fieldName, fieldScope string) string {
	if fieldScope == "" {
		fieldScope = "default"
	}
	return fieldName + "\x00" + fieldScope
}

// BulkRefreshTSMetrics 更新或创建时序指标数据
func (s *TimeSeriesMetricSvc) BulkRefreshTSMetrics(bkTenantId string, groupId uint, tableId string, metricInfoList []map[string]any, isAutoDiscovery bool) (bool, error) {
	// 当 metricInfoList 为空时，可能是上游异常、限流或拉取失败，跳过更新操作以避免误将所有指标标记为不活跃
	if len(metricInfoList) == 0 {
		logger.Warnf("BulkRefreshTSMetrics: metricInfoList is empty for group_id [%v], skip update to avoid marking all metrics as inactive due to potential upstream issues", groupId)
		return false, nil
	}

	// 按 (field_name, field_scope) 聚合
	metricsMap := make(map[string]map[string]any)
	newRecordKeys := mapset.NewSet[string]()
	for _, m := range metricInfoList {
		fieldName, ok := m["field_name"].(string)
		if !ok || fieldName == "" {
			logger.Errorf("parse metricInfo [%v] field_name failed, skip", m)
			continue
		}
		fieldScope, _ := m["field_scope"].(string)
		if fieldScope == "" {
			fieldScope = "default"
		}
		key := metricKey(fieldName, fieldScope)
		metricsMap[key] = m
		newRecordKeys.Add(key)
	}
	db := mysql.GetDBSession().DB
	var metrics []customreport.TimeSeriesMetric
	if err := customreport.NewTimeSeriesMetricQuerySet(db).Select(
		customreport.TimeSeriesMetricDBSchema.FieldID,
		customreport.TimeSeriesMetricDBSchema.FieldName,
		customreport.TimeSeriesMetricDBSchema.FieldScope,
		customreport.TimeSeriesMetricDBSchema.IsActive,
	).GroupIDEq(groupId).All(&metrics); err != nil {
		return false, errors.Wrapf(err, "query for TimeSeriesMetric with group_id [%v] failed", groupId)
	}
	oldRecordKeys := mapset.NewSet[string]()
	oldActiveKeyToFieldID := make(map[string]uint)
	for _, m := range metrics {
		scope := m.FieldScope
		if scope == "" {
			scope = "default"
		}
		key := metricKey(m.FieldName, scope)
		oldRecordKeys.Add(key)
		if m.IsActive {
			oldActiveKeyToFieldID[key] = m.FieldID
		}
	}

	needCreateKeys := newRecordKeys.Difference(oldRecordKeys).ToSlice()
	needUpdateKeys := newRecordKeys.Intersect(oldRecordKeys).ToSlice()
	newInactiveKeys := oldRecordKeys.Difference(newRecordKeys).ToSlice()
	if len(newInactiveKeys) > 0 {
		inactiveFieldIDs := make([]uint, 0, len(newInactiveKeys))
		for _, k := range newInactiveKeys {
			if id, ok := oldActiveKeyToFieldID[k]; ok {
				// 只更新数据库中是活跃的
				inactiveFieldIDs = append(inactiveFieldIDs, id)
			}
		}
		if err := s.BulkMarkMetricsInactiveByFieldIDs(groupId, inactiveFieldIDs); err != nil {
			logger.Errorf("BulkRefreshTSMetrics: mark inactive metrics for group_id [%v] failed, %v", groupId, err)
		}
	}

	needPush := false
	var err error
	if len(needCreateKeys) != 0 {
		needPush, err = s.BulkCreateMetricsByKeys(bkTenantId, metricsMap, needCreateKeys, groupId, tableId, isAutoDiscovery)
		if err != nil {
			return false, errors.Wrapf(err, "bulk create metrics for group_id [%v] table_id [%s] failed", groupId, tableId)
		}
	}
	if len(needUpdateKeys) != 0 {
		updatePush, err := s.BulkUpdateMetricsByKeys(bkTenantId, metricsMap, needUpdateKeys, groupId, isAutoDiscovery)
		if err != nil {
			return false, errors.Wrapf(err, "bulk update metrics for group_id [%v] failed", groupId)
		}
		needPush = needPush || updatePush
	}
	return needPush, nil
}

// BulkMarkMetricsInactiveByFieldIDs 按 field_id 批量标记指标为不活跃
func (s *TimeSeriesMetricSvc) BulkMarkMetricsInactiveByFieldIDs(groupId uint, fieldIDs []uint) error {
	if len(fieldIDs) == 0 {
		return nil
	}
	db := mysql.GetDBSession().DB
	for _, chunk := range slicex.ChunkSlice(fieldIDs, 100) {
		updater := customreport.NewTimeSeriesMetricQuerySet(db).
			GroupIDEq(groupId).
			FieldIDIn(chunk...).
			IsActiveEq(true).
			GetUpdater()
		if err := updater.SetIsActive(false).SetLastModifyTime(time.Now()).Update(); err != nil {
			return errors.Wrapf(err, "BulkMarkMetricsInactiveByFieldIDs group_id [%v] field_ids [%v] failed", groupId, chunk)
		}
		logger.Infof("BulkMarkMetricsInactiveByFieldIDs: marked %d TimeSeriesMetrics inactive for group_id [%v]", len(chunk), groupId)
	}
	return nil
}

// BulkCreateMetricsByKeys 按 (field_name, field_scope) key 批量创建指标，并写入 scope_id
func (s *TimeSeriesMetricSvc) BulkCreateMetricsByKeys(bkTenantId string, metricMap map[string]map[string]any, keys []string, groupId uint, tableId string, isAutoDiscovery bool) (bool, error) {
	db := mysql.GetDBSession().DB
	for _, key := range keys {
		metricInfo, ok := metricMap[key]
		if !ok {
			continue
		}
		fieldName, _ := metricInfo["field_name"].(string)
		fieldScope, _ := metricInfo["field_scope"].(string)
		if fieldScope == "" {
			fieldScope = "default"
		}
		tagList, err := s.getMetricTagFromMetricInfo(metricInfo)
		if err != nil {
			logger.Errorf("getMetricTagFromMetricInfo from [%#v] failed, %v", metricInfo, tagList)
		}
		isActive, ok := metricInfo["is_active"].(bool)
		if !ok {
			isActive = true
		}
		// 当指标是禁用的, 且未开启自动发现，则跳过记录
		if !isActive && !isAutoDiscovery {
			continue
		}
		tagListStr, _ := jsonx.MarshalString(tagList)
		realTableId := fmt.Sprintf("%s.%s", strings.Split(tableId, ".")[0], fieldName)
		tsm := customreport.TimeSeriesMetric{
			GroupID:        groupId,
			TableID:        realTableId,
			FieldName:      fieldName,
			FieldScope:     fieldScope,
			TagList:        tagListStr,
			LastModifyTime: time.Now(),
			IsActive:       isActive,
		}
		if sid, ok := metricInfo["scope_id"]; ok {
			switch v := sid.(type) {
			case uint:
				tsm.ScopeID = v
			case float64:
				tsm.ScopeID = uint(v)
			}
		}
		if err := tsm.Create(db); err != nil {
			logger.Errorf("create TimeSeriesMetric group_id [%v] field_name [%s] field_scope [%s] failed, %v", groupId, fieldName, fieldScope, err)
			continue
		}
		logger.Infof("created TimeSeriesMetric group_id [%v] table_id [%s] field_name [%s] field_scope [%s]", tsm.GroupID, tsm.TableID, tsm.FieldName, tsm.FieldScope)
	}
	return true, nil
}

// BulkUpdateMetricsByKeys 按 (field_name, field_scope) key 批量更新指标
func (s *TimeSeriesMetricSvc) BulkUpdateMetricsByKeys(bkTenantId string, metricMap map[string]map[string]any, keys []string, groupId uint, isAutoDiscovery bool) (bool, error) {
	db := mysql.GetDBSession().DB
	keySet := mapset.NewSet(keys...)
	var allTSMetricList []customreport.TimeSeriesMetric
	// 查询该group_id下的所有记录，只拉取需要的字段，然后在内存中过滤，避免SQL中的IN操作
	if err := customreport.NewTimeSeriesMetricQuerySet(db).Select(
		customreport.TimeSeriesMetricDBSchema.FieldID,
		customreport.TimeSeriesMetricDBSchema.FieldName,
		customreport.TimeSeriesMetricDBSchema.FieldScope,
		customreport.TimeSeriesMetricDBSchema.TagList,
		customreport.TimeSeriesMetricDBSchema.LastModifyTime,
		customreport.TimeSeriesMetricDBSchema.IsActive,
		customreport.TimeSeriesMetricDBSchema.ScopeID,
	).GroupIDEq(groupId).All(&allTSMetricList); err != nil {
		return false, errors.Wrapf(err, "BulkUpdateMetricsByKeys: query TimeSeriesMetric group_id [%v] failed", groupId)
	}
	// 在内存中根据keys进行过滤
	tsmList := make([]customreport.TimeSeriesMetric, 0, len(allTSMetricList))
	for i := range allTSMetricList {
		k := metricKey(allTSMetricList[i].FieldName, allTSMetricList[i].FieldScope)
		if keySet.Contains(k) {
			tsmList = append(tsmList, allTSMetricList[i])
		}
	}
	updated := false
	whiteListDisabledMetricSet := mapset.NewSet[uint]()
	for _, tsm := range tsmList {
		key := metricKey(tsm.FieldName, tsm.FieldScope)
		metricInfo, ok := metricMap[key]
		if !ok {
			continue
		}
		lastModifyTime, ok := metricInfo["last_modify_time"].(float64)
		if !ok {
			lastModifyTime = float64(time.Now().Unix())
		}
		lastTime := time.Unix(int64(lastModifyTime), 0)
		// 当指标是禁用的, 如果开启自动发现 则需要时间设置为 1970; 否则，跳过记录
		isActive, ok := metricInfo["is_active"].(bool)
		if !ok {
			isActive = true
		}
		if !isActive {
			if isAutoDiscovery {
				lastTime = time.Unix(0, 0).UTC()
			} else {
				whiteListDisabledMetricSet.Add(tsm.FieldID)
			}
		}
		// 标识是否需要更新
		isNeedUpdate := false
		// 先设置最后更新时间 1 天更新一次，减少对 db 的操作
		if lastTime.Sub(tsm.LastModifyTime).Hours() >= 24 {
			logger.Infof("BulkUpdateMetrics: group_id:[%v],last_modify_time [%v],last modify time larger than 24 hours,need update", groupId, lastModifyTime)
			isNeedUpdate = true
			tsm.LastModifyTime = lastTime
		}
		// NOTE: 仅当时间变更超过有效期阈值时，才进行更新
		if lastTime.Sub(tsm.LastModifyTime).Hours() >= float64(config.GlobalTimeSeriesMetricExpiredSeconds/3600) {
			logger.Infof("BulkUpdateMetrics: group_id:[%v],last_modify_time [%v],last modify time larger than 30 days,need update", groupId, lastModifyTime)
			updated = true
		}

		// 如果 tag 不一致，则进行更新
		tagList, err := s.getMetricTagFromMetricInfo(metricInfo)
		if err != nil {
			logger.Errorf("BulkUpdateMetrics: group_id:[%v],getMetricTagFromMetricInfo from [%#v] failed, %v", groupId, metricInfo, err)
			continue
		}
		var dbTagList []string
		if jsonx.UnmarshalString(tsm.TagList, &dbTagList) == nil {
			if !mapset.NewSet(dbTagList...).Equal(mapset.NewSet(tagList...)) {
				isNeedUpdate = true
				tagListStr, _ := jsonx.MarshalString(tagList)
				tsm.TagList = tagListStr
			}
		}
		needUpdateIsActive := false
		if tsm.IsActive != isActive {
			isNeedUpdate = true
			needUpdateIsActive = true
			tsm.IsActive = isActive
		}
		if scopeID, ok := metricInfo["scope_id"]; ok && tsm.ScopeID == 0 {
			switch v := scopeID.(type) {
			case uint:
				tsm.ScopeID = v
			case float64:
				tsm.ScopeID = uint(v)
			}
			isNeedUpdate = true
		}
		if isNeedUpdate {
			updateFields := []customreport.TimeSeriesMetricDBSchemaField{customreport.TimeSeriesMetricDBSchema.TagList, customreport.TimeSeriesMetricDBSchema.LastModifyTime}
			if needUpdateIsActive {
				updateFields = append(updateFields, customreport.TimeSeriesMetricDBSchema.IsActive)
			}
			if tsm.ScopeID != 0 {
				updateFields = append(updateFields, customreport.TimeSeriesMetricDBSchema.ScopeID)
			}
			if tsm.Update(db, updateFields...) != nil {
				logger.Errorf("BulkUpdateMetrics:update TimeSeriesMetric group_id [%v] field_name [%s] field_scope [%s] scope_id [%v] with tag_list [%s] last_modify_time [%v] is_active [%v] failed, %v", groupId, tsm.FieldName, tsm.FieldScope, tsm.ScopeID, tsm.TagList, tsm.LastModifyTime, tsm.IsActive, err)
				continue
			}
			updated = true
			logger.Infof("BulkUpdateMetrics:updated TimeSeriesMetric group_id [%v] field_name [%s] field_scope [%s] scope_id [%v] with tag_list [%s] last_modify_time [%v] is_active [%v]", groupId, tsm.FieldName, tsm.FieldScope, tsm.ScopeID, tsm.TagList, tsm.LastModifyTime, tsm.IsActive)
		}
	}
	disabledList := whiteListDisabledMetricSet.ToSlice()
	if len(disabledList) > 0 {
		_ = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(groupId).FieldIDIn(disabledList...).Delete()
		logger.Infof("BulkUpdateMetrics:delete TimeSeriesMetric group_id [%v] [%v] metrics", groupId, len(disabledList))
	}
	return updated && isAutoDiscovery, nil
}

// 获取 tags
func (*TimeSeriesMetricSvc) getMetricTagFromMetricInfo(metricInfo map[string]any) ([]string, error) {
	tags := mapset.NewSet[string]()
	// 当前从redis中取出的metricInfo只有tag_value_list
	if tagValues, ok := metricInfo["tag_value_list"].(map[string]any); ok {
		tags.Append(mapx.GetMapKeys(tagValues)...)
	} else if tagList, ok := metricInfo["tag_list"].([]any); ok {
		for _, t := range tagList {
			if tagInfo, ok := t.(map[string]any); ok {
				if fn, ok := tagInfo["field_name"].(string); ok && fn != "" {
					tags.Add(fn)
				}
			}
		}
	}
	// 添加特殊字段，兼容先前逻辑
	tags.Add("target")
	return tags.ToSlice(), nil
}
