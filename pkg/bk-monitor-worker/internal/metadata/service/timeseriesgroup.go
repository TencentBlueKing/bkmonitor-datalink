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
	"fmt"
	"math"
	"regexp"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	redisStore "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mapx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var TSDefaultStorageConfig = map[string]any{"use_default_rp": true}

var TSStorageFieldList = []map[string]any{
	{
		"field_name":        "target",
		"field_type":        "string",
		"tag":               models.ResultTableFieldTagDimension,
		"option":            map[string]any{},
		"is_config_by_user": true,
	},
}

const metricNamePattern = `^[a-zA-Z0-9_]+$`

// TimeSeriesGroupSvc time series group service
type TimeSeriesGroupSvc struct {
	*customreport.TimeSeriesGroup
}

func NewTimeSeriesGroupSvc(obj *customreport.TimeSeriesGroup) TimeSeriesGroupSvc {
	return TimeSeriesGroupSvc{
		TimeSeriesGroup: obj,
	}
}

// UpdateTimeSeriesMetrics 从远端存储中同步TS的指标和维度对应关系
func (s *TimeSeriesGroupSvc) UpdateTimeSeriesMetrics(vmRt string, queryFromBkData bool) (bool, error) {
	logger.Infof("UpdateTimeSeriesMetrics started, vm_rt: %s, query_from_bkdata: %v", vmRt, queryFromBkData)
	// 如果在白名单中，则通过计算平台获取指标数据
	if queryFromBkData {
		// 获取 vm rt及metric
		vmMetrics, err := s.QueryMetricAndDimension(vmRt)
		if err != nil {
			return false, err
		}
		return s.UpdateMetrics(*vmMetrics)
	}
	// 获取 redis 中数据，用于后续指标及tag的更新
	logger.Infof("UpdateTimeSeriesMetrics get redis data for vm_rt: %s", vmRt)
	metricInfo, err := s.GetRedisData(cfg.GlobalFetchTimeSeriesMetricIntervalSeconds)
	logger.Infof("UpdateTimeSeriesMetrics get redis data for vm_rt: %s, metric_info: %v", vmRt, metricInfo)
	if err != nil {
		logger.Errorf("UpdateTimeSeriesMetrics get redis data for vm_rt: %s, err: %v", vmRt, err)
		return false, err
	}
	if len(metricInfo) == 0 {
		logger.Infof("UpdateTimeSeriesMetrics get redis data for vm_rt: %s, metric_info is empty", vmRt)
		return false, nil
	}

	// 检查是否配置了字段白名单
	db := mysql.GetDBSession().DB
	var whitelistOption resulttable.ResultTableOption
	err = resulttable.NewResultTableOptionQuerySet(db).TableIDEq(s.TableID).BkTenantIdEq(s.BkTenantId).NameEq(models.OptionFieldWhitelist).One(&whitelistOption)
	if err == nil {
		// 存在白名单配置，解析白名单字段列表
		var whitelistFields []string
		logger.Infof("UpdateTimeSeriesMetrics: tableId->[%s],got whitelist option->[%v]", s.TableID, whitelistOption.Value)
		if err := json.Unmarshal([]byte(whitelistOption.Value), &whitelistFields); err != nil {
			logger.Errorf("UpdateTimeSeriesMetrics parse whitelist fields for table_id [%s] failed: %v", s.TableID, err)
		} else if len(whitelistFields) > 0 {
			logger.Infof("UpdateTimeSeriesMetrics applied whitelist filter for table_id [%s], whitelist_fields-> %v", s.TableID, whitelistFields)
			// 将白名单字段列表转换为map，方便查找
			whitelistMap := make(map[string]bool)
			for _, field := range whitelistFields {
				whitelistMap[field] = true
			}

			// 过滤metricInfo，只保留白名单中的字段
			var filteredMetricInfo []map[string]any
			for _, metric := range metricInfo {
				fieldName, ok := metric["field_name"].(string)
				if !ok {
					continue
				}
				if whitelistMap[fieldName] {
					filteredMetricInfo = append(filteredMetricInfo, metric)
				}
			}

			logger.Infof("UpdateTimeSeriesMetrics applied whitelist filter for table_id [%s], filtered metrics from %d to %d",
				s.TableID, len(metricInfo), len(filteredMetricInfo))
			metricInfo = filteredMetricInfo
		}
	}

	// 记录是否有更新，然后推送redis并发布通知
	return s.UpdateMetrics(metricInfo)
}

// QueryMetricAndDimension RefreshMetric 更新指标
func (s *TimeSeriesGroupSvc) QueryMetricAndDimension(vmRt string) (vmRtMetrics *[]map[string]any, err error) {
	// NOTE: 现阶段仅支持 vm 存储
	vmStorage := "vm"

	metricAndDimension, err := apiservice.Bkdata.QueryMetricAndDimension(s.BkTenantId, vmStorage, vmRt)
	if err != nil {
		return nil, err
	}

	return &metricAndDimension, nil
}

// GetRedisData get data from redis
func (s *TimeSeriesGroupSvc) GetRedisData(expiredTime int) ([]map[string]any, error) {
	/*
		[{
			'field_name': 'test',
			'tag_value_list': {
				'bk_biz_id': {'last_update_time': 1662009139, 'values': []},
				'parent_scenario': {'last_update_time': 1662009139, 'values': []},
				'scenario': {'last_update_time': 1662009139, 'values': []},
				'target': {'last_update_time': 1662009139, 'values': []},
				'target_biz_id': {'last_update_time': 1662009139, 'values': []},
				'target_biz_name': {'last_update_time': 1662009139, 'values': []}
			},
			'last_modify_time': 1662009139.0
		}]
	*/
	// 获取要处理的指标和维度的标识
	logger.Infof("GetRedisData started, expired_time: %d,TimeSeriesGroupID: %v,TimeSeriesGroupName: %v", expiredTime, s.TimeSeriesGroupID, s.TimeSeriesGroupName)
	metricKey := fmt.Sprintf("%s%d", cfg.MetadataMetricDimensionMetricKeyPrefix, s.BkDataID)
	metricDimensionsKey := fmt.Sprintf("%s%d", cfg.MetadataMetricDimensionKeyPrefix, s.BkDataID)
	fetchStep := cfg.MetadataMetricDimensionMaxMetricFetchStep
	// 转换时间
	nowTime := time.Now()
	nowTimeStampStr := fmt.Sprintf("%d", nowTime.Unix())
	// NOTE: 使用ADD，参数为负值
	validBeginTimeStamp := nowTime.Add(-time.Duration(expiredTime) * time.Second).Unix()
	validBeginTimeStampStr := fmt.Sprintf("%d", validBeginTimeStamp)
	redisClient := redisStore.GetCacheRedisInstance()
	logger.Infof("GetRedisData:, metricKey: %s, metricDimensionsKey: %s, fetchStep: %d", metricKey, metricDimensionsKey, fetchStep)
	// 根据过滤参数，获取总量
	zcountVal, err := redisClient.ZCount(metricKey, validBeginTimeStampStr, nowTimeStampStr)
	logger.Infof("GetRedisData:validBeginTimeStampStr: %s,nowTimeStampStr %s, zcountVal: %d", validBeginTimeStampStr, nowTimeStampStr, zcountVal)
	if err != nil {
		return nil, fmt.Errorf("redis zcount cmd error, %v", err)
	}
	var metricInfo []map[string]any
	ceilCount := math.Ceil(float64(zcountVal) / float64(fetchStep))
	for i := 0; float64(i) < ceilCount; i++ {
		opt := goRedis.ZRangeBy{
			Min:    validBeginTimeStampStr,
			Max:    nowTimeStampStr,
			Offset: int64(i) * int64(fetchStep),
			Count:  int64(fetchStep),
		}
		// 0. 首先获取有效期内的所有 metrics
		metricsWithScores, err := redisClient.ZRangeByScoreWithScores(metricKey, &opt)
		logger.Infof("GetRedisData:metricKey: %v,metricsWithScores: %v", metricKey, metricsWithScores)
		// NOTE: 沿用python功能逻辑，容忍一步出错
		if err != nil {
			logger.Errorf(
				"GetRedisData:failed to get metrics from storage, params metricKey: %s, min: %s, max: %s",
				metricKey, validBeginTimeStampStr, nowTimeStampStr)
			continue
		}
		// 1. 获取当前这批 metrics 的 dimensions 信息
		var fields []string
		for _, m := range metricsWithScores {
			memStr := fmt.Sprintf("%v", m.Member)
			fields = append(fields, memStr)
		}
		dimensions, err := redisClient.HMGet(metricDimensionsKey, fields...)
		if err != nil {
			logger.Errorf("GetRedisData:failed to get dimensions from metrics, err: %v", err)
			continue
		}
		// 2. 尝试更新 metrics 和对应 dimensions(tags)
		for j, m := range metricsWithScores {
			// NOTE: metrics 和 dimensions 列表一一对应
			dimension := dimensions[j]
			if dimension == nil {
				continue
			}
			// 解析
			var dimensionsMap map[string]any
			if err := jsonx.Unmarshal([]byte(fmt.Sprint(dimension)), &dimensionsMap); err != nil {
				logger.Errorf("GetRedisData:failed to parse dimension from dimensions info, dimension: %v", dimension)
				continue
			}
			dimensionInfo, ok := dimensionsMap["dimensions"]
			if !ok {
				logger.Error("key: dimensions not exist")
				continue
			}
			// field name 转换为string
			memStr := fmt.Sprintf("%v", m.Member)
			metricInfo = append(
				metricInfo,
				map[string]any{
					"field_name":       memStr,
					"tag_value_list":   dimensionInfo,
					"last_modify_time": m.Score,
				},
			)
		}
	}
	return metricInfo, nil
}

func (s *TimeSeriesGroupSvc) filterInvalidMetrics(metricInfoList []map[string]any) []map[string]any {
	validMetricInfoList := make([]map[string]any, 0)
	compiledRegex := regexp.MustCompile(metricNamePattern)
	for _, metric := range metricInfoList {
		metricName, ok := metric["field_name"].(string)
		if !ok {
			logger.Errorf("get metric field_name from [%v] failed", metric)
			continue
		}
		if !compiledRegex.MatchString(metricName) {
			logger.Errorf("metric field_name [%s] is invalid, skip", metricName)
			continue
		}
		validMetricInfoList = append(validMetricInfoList, metric)
	}
	return validMetricInfoList
}

// UpdateMetrics update ts metrics
func (s *TimeSeriesGroupSvc) UpdateMetrics(metricInfoList []map[string]any) (bool, error) {
	isAutoDiscovery, err := s.IsAutoDiscovery()
	tsmSvc := NewTimeSeriesMetricSvcSvc(nil)
	logger.Infof("UpdateMetrics: TimeSeriesGroupId: %v,table_id: %v,isAutoDiscovery: %v", s.TimeSeriesGroupID, s.TableID, isAutoDiscovery)

	// 过滤非法的指标
	metricInfoList = s.filterInvalidMetrics(metricInfoList)

	// 刷新 ts 表中的指标和维度
	updated, err := tsmSvc.BulkRefreshTSMetrics(s.BkTenantId, s.TimeSeriesGroupID, s.TableID, metricInfoList, isAutoDiscovery)
	if err != nil {
		return false, errors.Wrapf(err, "BulkRefreshRtFields for table id [%s] with metric info [%v] failed", s.TableID, metricInfoList)
	}
	// 刷新 rt 表中的指标和维度
	err = s.BulkRefreshRtFields(s.TableID, metricInfoList)
	if err != nil {
		return false, errors.Wrapf(err, "refresh rt fields for [%s] failed", s.TableID)
	}
	return updated, nil
}

// BulkRefreshRtFields 批量刷新结果表打平的指标和维度
func (s *TimeSeriesGroupSvc) BulkRefreshRtFields(tableId string, metricInfoList []map[string]any) error {
	metricTagInfo, err := s.refineMetricTags(metricInfoList)
	if err != nil {
		return errors.Wrap(err, "refineMetricTags failed")
	}
	db := mysql.GetDBSession().DB
	// 通过结果表过滤到到指标和维度
	// NOTE: 因为 `ResultTableField` 字段是打平的，如果指标或维度已经存在，则以存在的数据为准
	var existRTFields []resulttable.ResultTableField
	if err := resulttable.NewResultTableFieldQuerySet(db).Select(resulttable.ResultTableFieldDBSchema.FieldName).BkTenantIdEq(s.BkTenantId).TableIDEq(tableId).All(&existRTFields); err != nil {
		return errors.Wrapf(err, "query ResultTableField with table_id [%s] failed", tableId)
	}
	// 组装结果表包含的字段数据，包含指标和维度
	existFields := mapset.NewSet[string]()
	for _, field := range existRTFields {
		existFields.Add(field.FieldName)
	}

	// 过滤需要创建或更新的指标
	metricMap, ok := metricTagInfo["metricMap"].(map[string]bool)
	if !ok {
		return errors.New("parse metricMap failed")
	}
	metricSet := mapset.NewSet(mapx.GetMapKeys(metricMap)...)
	needCreateMetricSet := metricSet.Difference(existFields)
	needUpdateMetricSet := metricSet.Difference(needCreateMetricSet)
	if err := s.BulkCreateOrUpdateMetrics(tableId, metricMap, needCreateMetricSet.ToSlice(), needUpdateMetricSet.ToSlice()); err != nil {
		return errors.Wrapf(err, "BulkCreateOrUpdateMetrics for table_id [%s] failed", tableId)
	}

	// 过滤需要创建或更新的维度
	tagMap, ok := metricTagInfo["tagMap"].(map[string]string)
	if !ok {
		return errors.New("parse tagMap failed")
	}
	tagSet := mapset.NewSet(mapx.GetMapKeys(tagMap)...)
	needCreateTagSet := tagSet.Difference(existFields).Difference(needCreateMetricSet)
	needUpdateTagSet := tagSet.Difference(needCreateTagSet)
	isUpdateDescription, ok := metricTagInfo["isUpdateDescription"].(bool)
	if !ok {
		return errors.New("parse is_update_description failed")
	}
	if err := s.BulkCreateOrUpdateTags(tableId, tagMap, needCreateTagSet.ToSlice(), needUpdateTagSet.ToSlice(), isUpdateDescription); err != nil {
		return errors.Wrapf(err, "BulkCreateOrUpdateMetrics for table_id [%s] failed", tableId)
	}
	logger.Infof("bulk refresh rt fields for table_id [%s] successfully", tableId)
	return nil
}

// 去除重复的维度
func (s *TimeSeriesGroupSvc) refineMetricTags(metricInfoList []map[string]any) (map[string]any, error) {
	metricMap := make(map[string]bool)
	tagMap := make(map[string]string)
	// 标识是否需要更新描述
	isUpdateDescription := true
	for _, item := range metricInfoList {
		fieldName, ok := item["field_name"].(string)
		if !ok {
			logger.Errorf("get metric field_name from [%v] failed", metricMap)
			continue
		}
		isActive, ok := item["is_active"].(bool)
		if !ok {
			isActive = true
		}
		// 格式: {field_name: 是否禁用}
		// NOTE: 取反为了方便存储和transfer使用
		metricMap[fieldName] = !isActive
		// 现版本只有 tag_value_list 的情况
		if tagValue, ok := item["tag_value_list"].(map[string]any); ok {
			isUpdateDescription = false
			for tag := range tagValue {
				tagMap[tag] = ""
			}
		} else {
			logger.Errorf("get metric tag_value_list from [%v] failed", metricMap)
			continue
		}
	}
	return map[string]any{
		"isUpdateDescription": isUpdateDescription,
		"metricMap":           metricMap,
		"tagMap":              tagMap,
	}, nil
}

// IsAutoDiscovery 判断是否是自动发现, True：是自动发现/False：不是自动发现（插件白名单模式）
func (s *TimeSeriesGroupSvc) IsAutoDiscovery() (bool, error) {
	if s.TimeSeriesGroup == nil {
		return false, errors.New("TimeSeriesGroup can not be nil")
	}
	db := mysql.GetDBSession().DB

	count, err := resulttable.NewResultTableOptionQuerySet(db).TableIDEq(s.TableID).BkTenantIdEq(s.BkTenantId).NameEq(models.OptionEnableFieldBlackList).ValueEq("false").Count()
	if err != nil {
		return false, errors.Wrapf(err, "query NewResultTableOptionQuerySet with table_id [%s] name [%s] value [%s] failed", s.TableID, models.OptionEnableFieldBlackList, "false")
	}
	return count == 0, nil
}

// BulkCreateOrUpdateMetrics 批量创建或更新字段
func (s *TimeSeriesGroupSvc) BulkCreateOrUpdateMetrics(tableId string, metricMap map[string]bool, needCreateMetrics, needUpdateMetrics []string) error {
	logger.Infof("bulk create or update rt metrics for table_id [%s]", tableId)
	db := mysql.GetDBSession().DB
	for _, metric := range needCreateMetrics {
		defaultValue := "0"
		var isDisabled, ok bool
		isDisabled, ok = metricMap[metric]
		if !ok {
			isDisabled = false
		}
		rtf := resulttable.ResultTableField{
			BkTenantId:     s.BkTenantId,
			TableID:        tableId,
			FieldName:      metric,
			FieldType:      models.ResultTableFieldTypeFloat,
			Tag:            models.ResultTableFieldTagMetric,
			IsConfigByUser: true,
			DefaultValue:   &defaultValue,
			Creator:        "system",
			LastModifyUser: "system",
			IsDisabled:     isDisabled,
		}

		if err := rtf.Create(db); err != nil {
			logger.Errorf("create ResultTableField table_id [%s] field_name [%s], failed, %v", rtf.TableID, rtf.FieldName, err)
			continue
		}
		logger.Infof("created ResultTableField table_id [%s] field_name [%s]", rtf.TableID, rtf.FieldName)
	}
	logger.Infof("bulk create metrics for table_id [%s] successfully", tableId)
	// 开始批量更新
	var updateRecords []resulttable.ResultTableField
	var updateRTFs []resulttable.ResultTableField
	for _, chunkMetrics := range slicex.ChunkSlice(needUpdateMetrics, 0) {
		var tempList []resulttable.ResultTableField
		if err := resulttable.NewResultTableFieldQuerySet(db).BkTenantIdEq(s.BkTenantId).TableIDEq(tableId).TagEq(models.ResultTableFieldTagMetric).FieldNameIn(chunkMetrics...).All(&tempList); err != nil {
			return errors.Wrapf(err, "query ResultTableField with table_id [%s] field_name [%v] tag [%s] failed", tableId, chunkMetrics, models.ResultTableFieldTagMetric)
		}
		updateRTFs = append(updateRTFs, tempList...)
	}
	for _, rtf := range updateRTFs {
		expectMetricStatus, ok := metricMap[rtf.FieldName]
		if !ok {
			expectMetricStatus = false
		}
		if rtf.IsDisabled != expectMetricStatus {
			rtf.IsDisabled = expectMetricStatus
			rtf.LastModifyTime = time.Now().UTC()
			updateRecords = append(updateRecords, rtf)

			if err := rtf.Update(db, resulttable.ResultTableFieldDBSchema.IsDisabled, resulttable.ResultTableFieldDBSchema.LastModifyTime); err != nil {
				logger.Errorf("update ResultTableField table_id [%v] field_name [%s] with is_disabled [%v] last_modify_time [%v] failed, %v", rtf.TableID, rtf.FieldName, rtf.IsDisabled, rtf.LastModifyTime, err)
				continue
			}
			logger.Infof("update ResultTableField table_id [%v] field_name [%s] with is_disabled [%v] last_modify_time [%v]", rtf.TableID, rtf.FieldName, rtf.IsDisabled, rtf.LastModifyTime)
		}
	}
	logger.Infof("batch update metrics for table_id [%s] successfully", tableId)
	return nil
}

// BulkCreateOrUpdateTags 批量创建或更新tag
func (s *TimeSeriesGroupSvc) BulkCreateOrUpdateTags(tableId string, tagMap map[string]string, needCreateTags, needUpdateTags []string, isUpdateDescription bool) error {
	logger.Infof("bulk create or update rt tag for table_id [%s]", tableId)
	db := mysql.GetDBSession().DB
	for _, tag := range needCreateTags {
		defaultValue := ""
		description, _ := tagMap[tag]
		rtf := resulttable.ResultTableField{
			BkTenantId:     s.BkTenantId,
			TableID:        tableId,
			FieldName:      tag,
			Description:    description,
			FieldType:      models.ResultTableFieldTypeString,
			Tag:            models.ResultTableFieldTagDimension,
			IsConfigByUser: true,
			DefaultValue:   &defaultValue,
			Creator:        "system",
			LastModifyUser: "system",
			IsDisabled:     false,
		}

		if err := rtf.Create(db); err != nil {
			logger.Errorf("create ResultTableField table_id [%s] field_name [%s] description [%s], failed, %v", rtf.TableID, rtf.FieldName, rtf.Description, err)
			continue
		}
		logger.Infof("created ResultTableField table_id [%s] field_name [%s] description [%s]", rtf.TableID, rtf.FieldName, rtf.Description)
	}
	logger.Infof("bulk create tags for table_id [%s] successfully", tableId)
	// 开始批量更新
	var updateRTFs []resulttable.ResultTableField
	for _, chunkMetrics := range slicex.ChunkSlice(needUpdateTags, 0) {
		var tempList []resulttable.ResultTableField
		if err := resulttable.NewResultTableFieldQuerySet(db).BkTenantIdEq(s.BkTenantId).TableIDEq(tableId).TagIn(models.ResultTableFieldTagDimension, models.ResultTableFieldTagTimestamp, models.ResultTableFieldTagGroup).FieldNameIn(chunkMetrics...).All(&tempList); err != nil {
			return errors.Wrapf(err, "query ResultTableField with table_id [%s] field_name [%v] tag [%s,%s,%s] failed", tableId, chunkMetrics, models.ResultTableFieldTagDimension, models.ResultTableFieldTagTimestamp, models.ResultTableFieldTagGroup)
		}
		updateRTFs = append(updateRTFs, tempList...)
	}
	if !isUpdateDescription {
		return nil
	}
	for _, rtf := range updateRTFs {
		expectTagDescription, _ := tagMap[rtf.FieldName]
		if rtf.Description != expectTagDescription {
			rtf.Description = expectTagDescription
			rtf.LastModifyTime = time.Now().UTC()

			if err := rtf.Update(db, resulttable.ResultTableFieldDBSchema.Description, resulttable.ResultTableFieldDBSchema.LastModifyTime); err != nil {
				logger.Errorf("update ResultTableField table_id [%v] field_name [%s] with description [%s] last_modify_time [%v] failed, %v", rtf.TableID, rtf.FieldName, rtf.Description, rtf.LastModifyTime, err)
				continue
			}
			logger.Infof("update ResultTableField table_id [%v] field_name [%s] with description [%s] last_modify_time [%v]", rtf.TableID, rtf.FieldName, rtf.Description, rtf.LastModifyTime)
		}
	}
	logger.Infof("batch update tags for table_id [%s] successfully", tableId)
	return nil
}
