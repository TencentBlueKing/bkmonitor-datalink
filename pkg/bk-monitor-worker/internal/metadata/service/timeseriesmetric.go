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

// BulkRefreshTSMetrics 更新或创建时序指标数据
func (s *TimeSeriesMetricSvc) BulkRefreshTSMetrics(bkTenantId string, groupId uint, tableId string, metricInfoList []map[string]any, isAutoDiscovery bool) (bool, error) {
	// 获取需要批量处理的指标名
	metricFieldNameSet := mapset.NewSet[string]()
	metricsMap := make(map[string]map[string]any)
	for _, m := range metricInfoList {
		fieldName, ok := m["field_name"].(string)
		if !ok {
			logger.Errorf("parse metricInfo [%v] field_name failed, skip", m)
			continue
		}
		metricsMap[fieldName] = m
		metricFieldNameSet.Add(fieldName)
	}
	db := mysql.GetDBSession().DB
	// 获取不存在的指标，然后批量创建
	var metrics []customreport.TimeSeriesMetric
	if err := customreport.NewTimeSeriesMetricQuerySet(db).Select(customreport.TimeSeriesMetricDBSchema.FieldName).GroupIDEq(groupId).All(&metrics); err != nil {
		return false, errors.Wrapf(err, "query for TimeSeriesMetric with group_id [%v] failed", groupId)
	}
	existFieldNameSet := mapset.NewSet[string]()
	for _, m := range metrics {
		existFieldNameSet.Add(m.FieldName)
	}

	// 获取需要批量创建的指标名
	needCreateMetricFieldNameSet := metricFieldNameSet.Difference(existFieldNameSet)
	needCreateMetricFieldNames := needCreateMetricFieldNameSet.ToSlice()
	// 获取已经存在的指标名，然后进行批量更新
	needUpdateMetricFieldNameSet := metricFieldNameSet.Difference(needCreateMetricFieldNameSet)
	needUpdateMetricFieldNames := needUpdateMetricFieldNameSet.ToSlice()

	// 针对创建时和白名单模式有更新时，推送路由数据
	needPush := false
	var err error
	if len(needCreateMetricFieldNames) != 0 {
		needPush, err = s.BulkCreateMetrics(bkTenantId, metricsMap, needCreateMetricFieldNames, groupId, tableId, isAutoDiscovery)
		if err != nil {
			return false, errors.Wrapf(err, "bulk create metrics [%v] for group_id [%v] table_id [%s] failed", needCreateMetricFieldNames, groupId, tableId)
		}
	}

	if len(needUpdateMetricFieldNames) != 0 {
		updatePush, err := s.BulkUpdateMetrics(bkTenantId, metricsMap, needUpdateMetricFieldNames, groupId, isAutoDiscovery)
		if err != nil {
			return false, errors.Wrapf(err, "bulk update metrics [%v] for group_id [%v] table_id [%s] failed", needUpdateMetricFieldNames, groupId, tableId)
		}
		needPush = needPush || updatePush
	}

	return needPush, nil
}

// BulkCreateMetrics 批量创建指标
func (s *TimeSeriesMetricSvc) BulkCreateMetrics(bkTenantId string, metricMap map[string]map[string]any, metricNames []string, groupId uint, tableId string, isAutoDiscovery bool) (bool, error) {
	db := mysql.GetDBSession().DB
	for _, name := range metricNames {
		metricInfo, ok := metricMap[name]
		if !ok {
			// 如果获取不到指标数据，则跳过
			continue
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

		tagListStr, err := jsonx.MarshalString(tagList)
		if err != nil {
			logger.Errorf("marshal tagList [%v] failed, %v", tagList, err)
		}
		realTableId := fmt.Sprintf("%s.%s", strings.Split(tableId, ".")[0], name)
		tsm := customreport.TimeSeriesMetric{
			GroupID:        groupId,
			TableID:        realTableId,
			FieldName:      name,
			TagList:        tagListStr,
			LastModifyTime: time.Now(),
		}

		if err := tsm.Create(db); err != nil {
			logger.Errorf("create TimeSeriesMetric group_id [%v] table_id [%s] field_name [%s] tag_list [%s] failed, %v", tsm.GroupID, tsm.TableID, tsm.FieldName, tsm.TagList, err)
			continue
		}
		logger.Infof("created TimeSeriesMetric group_id [%v] table_id [%s] field_name [%s] tag_list [%s]", tsm.GroupID, tsm.TableID, tsm.FieldName, tsm.TagList)
	}
	return true, nil
}

// BulkUpdateMetrics 批量更新指标，针对记录仅更新最后更新时间和 tag 字段
func (s *TimeSeriesMetricSvc) BulkUpdateMetrics(bkTenantId string, metricMap map[string]map[string]any, metricNames []string, groupId uint, isAutoDiscovery bool) (bool, error) {
	db := mysql.GetDBSession().DB
	var tsmList []customreport.TimeSeriesMetric
	for _, chunkMetricNameList := range slicex.ChunkSlice(metricNames, 0) {
		var tempList []customreport.TimeSeriesMetric
		if err := customreport.NewTimeSeriesMetricQuerySet(db).FieldNameIn(chunkMetricNameList...).GroupIDEq(groupId).All(&tempList); err != nil {
			return false, errors.Wrapf(err, "BulkUpdateMetrics：query TimeSeriesMetric with group_id [%v], filed_name [%v] failed", groupId, chunkMetricNameList)
		}
		tsmList = append(tsmList, tempList...)
	}
	updated := false
	whiteListDisabledMetricSet := mapset.NewSet[string]()
	// 组装更新的数据
	for _, tsm := range tsmList {
		metricInfo, ok := metricMap[tsm.FieldName]
		if !ok {
			// 如果获取不到指标数据，则跳过
			continue
		}
		lastModifyTime, ok := metricInfo["last_modify_time"].(float64)
		logger.Infof("BulkUpdateMetrics: group_id:[%v],table_id:[%v],last_modify_time [%v]", tsm.GroupID, tsm.TableID, lastModifyTime)
		if !ok {
			logger.Errorf("BulkUpdateMetrics: group_id:[%v],table_id:[%v],last_modify_time [%v] is nil", tsm.GroupID, tsm.TableID, lastModifyTime)
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
				whiteListDisabledMetricSet.Add(tsm.FieldName)
			}
		}
		// 标识是否需要更新
		isNeedUpdate := false
		// 先设置最后更新时间 1 天更新一次，减少对 db 的操作
		if lastTime.Sub(tsm.LastModifyTime).Hours() >= 24 {
			logger.Infof("BulkUpdateMetrics: group_id:[%v],table_id:[%v],last_modify_time [%v],last modify time larger than 24 hours,need update", tsm.GroupID, tsm.TableID, lastModifyTime)
			isNeedUpdate = true
			tsm.LastModifyTime = lastTime
		}
		// NOTE: 仅当时间变更超过有效期阈值时，才进行更新
		if lastTime.Sub(tsm.LastModifyTime).Hours() >= float64(config.GlobalTimeSeriesMetricExpiredSeconds/3600) {
			logger.Infof("BulkUpdateMetrics: group_id:[%v],table_id:[%v],last_modify_time [%v],last modify time larger than 30 days,need update", tsm.GroupID, tsm.TableID, lastModifyTime)
			updated = true
		}

		// 如果 tag 不一致，则进行更新
		tagList, err := s.getMetricTagFromMetricInfo(metricInfo)
		if err != nil {
			logger.Errorf("BulkUpdateMetrics:getMetricTagFromMetricInfo from [%#v] failed, %v", metricInfo, tagList)
			continue
		}
		var dbTagList []string
		if err := jsonx.UnmarshalString(tsm.TagList, &dbTagList); err != nil {
			logger.Errorf("BulkUpdateMetrics:TimeSeriesMetric group_id [%v] table_id [%s] has wrong format tag_list [%s]", tsm.GroupID, tsm.TableID, tsm.TagList)
			continue
		}
		if !mapset.NewSet(dbTagList...).Equal(mapset.NewSet(tagList...)) {
			isNeedUpdate = true
			tagListStr, err := jsonx.MarshalString(tagList)
			if err != nil {
				logger.Errorf("BulkUpdateMetrics:marshal tagList for [%v] failed, %v", tagList, err)
				continue
			}
			tsm.TagList = tagListStr
		}
		if isNeedUpdate {
			if err := tsm.Update(db, customreport.TimeSeriesMetricDBSchema.TagList, customreport.TimeSeriesMetricDBSchema.LastModifyTime); err != nil {
				logger.Errorf("BulkUpdateMetrics:update TimeSeriesMetric group_id [%v] field_name [%s] with tag_list [%s] last_modify_time [%v] failed, %v", tsm.GroupID, tsm.FieldName, tsm.TagList, tsm.LastModifyTime, err)
				continue
			}
			logger.Infof("BulkUpdateMetrics:updated TimeSeriesMetric group_id [%v] field_name [%s] with tag_list [%s] last_modify_time [%v]", tsm.GroupID, tsm.FieldName, tsm.TagList, tsm.LastModifyTime)
		}
	}
	// 白名单模式，如果存在需要禁用的指标，则需要删除；应该不会太多，直接删除
	disabledList := whiteListDisabledMetricSet.ToSlice()
	if len(disabledList) != 0 {
		if err := customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(groupId).FieldNameIn(disabledList...).Delete(); err != nil {
			logger.Errorf("BulkUpdateMetrics:delete whiteList disabeld TimeSeriesMetric with group_id [%v] field_name [%v] failed, %v", groupId, disabledList, err)
		}
	}
	// 自动发现且有更新时需要推送路由数据
	return updated && isAutoDiscovery, nil
}

// 获取 tags
func (*TimeSeriesMetricSvc) getMetricTagFromMetricInfo(metricInfo map[string]any) ([]string, error) {
	tags := mapset.NewSet[string]()
	// 当前从redis中取出的metricInfo只有tag_value_list
	if tagValues, ok := metricInfo["tag_value_list"].(map[string]any); ok {
		tags.Append(mapx.GetMapKeys(tagValues)...)
	} else {
		return nil, errors.Errorf("metricInfo [%#v] parse tag_value_list failed", metricInfo)
	}
	// 添加特殊字段，兼容先前逻辑
	tags.Add("target")
	return tags.ToSlice(), nil
}
