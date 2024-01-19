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
	"math"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	redisStore "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/dependentredis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mapx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var TSDefaultStorageConfig = map[string]interface{}{"use_default_rp": true}

var TSStorageFieldList = []map[string]interface{}{
	{
		"field_name":        "target",
		"field_type":        "string",
		"tag":               models.ResultTableFieldTagDimension,
		"option":            map[string]interface{}{},
		"is_config_by_user": true,
	},
}

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
func (s *TimeSeriesGroupSvc) UpdateTimeSeriesMetrics() (bool, error) {
	// 获取 redis 中数据，用于后续指标及tag的更新
	metricInfo, err := s.GetRedisData(cfg.GlobalFetchTimeSeriesMetricIntervalSeconds)
	if err != nil {
		return false, err
	}
	if len(metricInfo) == 0 {
		return false, nil
	}
	// 记录是否有更新，然后推送redis并发布通知
	return s.UpdateMetrics(metricInfo)
}

// GetRedisData get data from redis
func (s *TimeSeriesGroupSvc) GetRedisData(expiredTime int) ([]map[string]interface{}, error) {
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
	metricKey := fmt.Sprintf("%s%d", cfg.MetadataMetricDimensionMetricKeyPrefix, s.BkDataID)
	metricDimensionsKey := fmt.Sprintf("%s%d", cfg.MetadataMetricDimensionKeyPrefix, s.BkDataID)
	fetchStep := cfg.MetadataMetricDimensionMaxMetricFetchStep
	// 转换时间
	nowTime := time.Now()
	nowTimeStampStr := fmt.Sprintf("%d", nowTime.Unix())
	// NOTE: 使用ADD，参数为负值
	validBeginTimeStamp := nowTime.Add(-time.Duration(expiredTime) * time.Second).Unix()
	validBeginTimeStampStr := fmt.Sprintf("%d", validBeginTimeStamp)
	redisClient, err := redisStore.GetInstance()
	if err != nil {
		return nil, err
	}
	// 根据过滤参数，获取总量
	zcountVal, err := redisClient.ZCount(metricKey, validBeginTimeStampStr, nowTimeStampStr)
	if err != nil {
		return nil, fmt.Errorf("redis zcount cmd error, %v", err)
	}
	var metricInfo []map[string]interface{}
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
		// NOTE: 沿用python功能逻辑，容忍一步出错
		if err != nil {
			logger.Errorf(
				"failed to get metrics from storage, params metricKey: %s, min: %s, max: %s",
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
			logger.Errorf("failed to get dimensions from metrics, err: %v", err)
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
			var dimensionsMap map[string]interface{}
			if err := jsonx.Unmarshal([]byte(fmt.Sprint(dimension)), &dimensionsMap); err != nil {
				logger.Errorf("failed to parse dimension from dimensions info, dimension: %v", dimension)
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
				map[string]interface{}{
					"field_name":       memStr,
					"tag_value_list":   dimensionInfo,
					"last_modify_time": m.Score,
				},
			)
		}
	}
	return metricInfo, nil
}

// UpdateMetrics update ts metrics
func (s *TimeSeriesGroupSvc) UpdateMetrics(MetricInfoList []map[string]interface{}) (bool, error) {
	// 刷新 ts 中指标和维度
	isAutoDiscovery, err := s.IsAutoDiscovery()
	tsmSvc := NewTimeSeriesMetricSvcSvc(nil)
	// 刷新 rt 表中的指标和维度
	updated, err := tsmSvc.BulkRefreshTSMetrics(s.TimeSeriesGroupID, s.TableID, MetricInfoList, isAutoDiscovery)
	if err != nil {
		return false, errors.Wrapf(err, "BulkRefreshRtFields for table id [%s] with metric info [%v] failed", s.TableID, MetricInfoList)
	}
	// 刷新 rt 表中的指标和维度
	err = s.BulkRefreshRtFields(s.TableID, MetricInfoList)
	if err != nil {
		return false, errors.Wrapf(err, "refresh rt fields for [%s] failed", s.TableID)
	}
	return updated, nil
}

// BulkRefreshRtFields 批量刷新结果表打平的指标和维度
func (s *TimeSeriesGroupSvc) BulkRefreshRtFields(tableId string, metricInfoList []map[string]interface{}) error {
	metricTagInfo, err := s.refineMetricTags(metricInfoList)
	if err != nil {
		return errors.Wrap(err, "refineMetricTags failed")
	}
	db := mysql.GetDBSession().DB
	// 通过结果表过滤到到指标和维度
	var existRTFields []resulttable.ResultTableField
	if err := resulttable.NewResultTableFieldQuerySet(db).Select(resulttable.ResultTableFieldDBSchema.FieldName, resulttable.ResultTableFieldDBSchema.Tag).TableIDEq(tableId).All(&existRTFields); err != nil {
		return errors.Wrapf(err, "query ResultTableField with table_id [%s] failed", tableId)
	}
	existMetricSet := mapset.NewSet[string]()
	existTagSet := mapset.NewSet[string]()
	for _, field := range existRTFields {
		if field.Tag == models.ResultTableFieldTagMetric {
			existMetricSet.Add(field.FieldName)
		} else if slicex.IsExistItem([]string{models.ResultTableFieldTagDimension, models.ResultTableFieldTagTimestamp, models.ResultTableFieldTagGroup, models.ResultTableFieldTagMetric}, field.Tag) {
			existTagSet.Add(field.FieldName)
		}
	}

	// 过滤需要创建或更新的指标
	metricMap, ok := metricTagInfo["metricMap"].(map[string]bool)
	if !ok {
		return errors.New("parse metricMap failed")
	}
	metricSet := mapset.NewSet[string](mapx.GetMapKeys(metricMap)...)
	needCreateMetricSet := metricSet.Difference(existMetricSet)
	needUpdateMetricSet := metricSet.Difference(needCreateMetricSet)
	if err := s.BulkCreateOrUpdateMetrics(tableId, metricMap, needCreateMetricSet.ToSlice(), needUpdateMetricSet.ToSlice()); err != nil {
		return errors.Wrapf(err, "BulkCreateOrUpdateMetrics for table_id [%s] failed", tableId)
	}

	// 过滤需要创建或更新的维度
	tagMap, ok := metricTagInfo["tagMap"].(map[string]string)
	if !ok {
		return errors.New("parse tagMap failed")
	}
	tagSet := mapset.NewSet[string](mapx.GetMapKeys(tagMap)...)
	needCreateTagSet := tagSet.Difference(existTagSet)
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
func (s *TimeSeriesGroupSvc) refineMetricTags(metricInfoList []map[string]interface{}) (map[string]interface{}, error) {
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
		if tagValue, ok := item["tag_value_list"].(map[string]interface{}); ok {
			isUpdateDescription = false
			for tag := range tagValue {
				tagMap[tag] = ""
			}
		} else {
			logger.Errorf("get metric tag_value_list from [%v] failed", metricMap)
			continue
		}
	}
	return map[string]interface{}{
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

	count, err := resulttable.NewResultTableOptionQuerySet(db).TableIDEq(s.TableID).NameEq(models.OptionEnableFieldBlackList).ValueEq("false").Count()
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
		if err := resulttable.NewResultTableFieldQuerySet(db).TableIDEq(tableId).TagEq(models.ResultTableFieldTagMetric).FieldNameIn(chunkMetrics...).All(&tempList); err != nil {
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
		if err := resulttable.NewResultTableFieldQuerySet(db).TableIDEq(tableId).TagIn(models.ResultTableFieldTagDimension, models.ResultTableFieldTagTimestamp, models.ResultTableFieldTagGroup).FieldNameIn(chunkMetrics...).All(&tempList); err != nil {
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

func (s TimeSeriesGroupSvc) MetricConsulPath() string {
	return fmt.Sprintf("%s/metadata/influxdb_metrics/%v/time_series_metric", cfg.StorageConsulPathPrefix, s.BkDataID)
}

func (s TimeSeriesGroupSvc) CreateCustomGroup(bkDataId uint, bkBizId int, customGroupName, label, operator string, isSplitMeasurement bool, defaultStorageConfig map[string]interface{}, additionalOptions map[string][]string) (*customreport.TimeSeriesGroup, error) {
	err := s.PreCheck(label, bkDataId, customGroupName, bkBizId)
	if err != nil {
		return nil, err
	}
	tableId := s.MakeTableId(bkBizId, bkDataId)
	tsGroup := customreport.TimeSeriesGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID:           bkDataId,
			BkBizID:            bkBizId,
			TableID:            tableId,
			MaxRate:            -1,
			Label:              label,
			IsEnable:           true,
			IsDelete:           false,
			Creator:            operator,
			CreateTime:         time.Now(),
			LastModifyUser:     operator,
			LastModifyTime:     time.Now(),
			IsSplitMeasurement: false,
		},
		TimeSeriesGroupName: customGroupName,
	}
	db := mysql.GetDBSession().DB
	if err := tsGroup.Create(db); err != nil {
		return nil, err
	}
	tsGroupSvc := NewTimeSeriesGroupSvc(&tsGroup)
	logger.Infof("TimeSeriesGroup [%v] now is created from data_id [%v] by operator [%s]", tsGroupSvc.TimeSeriesGroupID, bkDataId, operator)
	// 创建一个关联的存储关系
	for k, v := range TSDefaultStorageConfig {
		defaultStorageConfig[k] = v
	}
	option := map[string]interface{}{"is_split_measurement": isSplitMeasurement}
	for k, v := range additionalOptions {
		option[k] = v
	}
	// 清除历史 DataSourceResultTable 数据
	if err := db.Delete(&resulttable.DataSourceResultTable{}, "bk_data_id = ?", bkDataId).Error; err != nil {
		return nil, err
	}
	rtSvc := NewResultTableSvc(nil)
	err = rtSvc.CreateResultTable(
		tsGroup.BkDataID,
		tsGroup.BkBizID,
		tableId,
		tsGroup.TimeSeriesGroupName,
		true,
		models.ResultTableSchemaTypeFree,
		operator,
		models.StorageTypeInfluxdb,
		defaultStorageConfig,
		TSStorageFieldList,
		true,
		map[string]interface{}{},
		label,
		option,
	)
	if err != nil {
		return nil, err
	}
	// 需要为datasource增加option，否则transfer无法得知需要拆解的字段内容
	dsOptions := []map[string]string{
		{"name": "metrics_report_path", "value": tsGroupSvc.MetricConsulPath()},
		{"name": "disable_metric_cutter", "value": "true"},
		{"name": "flat_batch_key", "value": "data"},
	}
	tx := db.Begin()
	for _, dsOption := range dsOptions {
		if err := NewDataSourceOptionSvc(nil).CreateOption(bkDataId, dsOption["name"], dsOption["value"], "system", tx); err != nil {
			tx.Rollback()
			return nil, err
		}
	}
	tx.Commit()
	if err != nil {
		return nil, err
	}
	// 刷新配置到节点管理，通过节点管理下发配置到采集器
	// todo 做异步调用 RefreshCustomReportConfig(bkBizId)

	return &tsGroup, nil
}

// PreCheck 参数检查
func (TimeSeriesGroupSvc) PreCheck(label string, bkDataId uint, customGroupName string, bkBizId int) error {
	db := mysql.GetDBSession().DB
	// 确认label是否存在
	count, err := resulttable.NewLabelQuerySet(db).LabelTypeEq(models.LabelTypeResultTable).LabelIdEq(label).Count()
	if err != nil {
		return err
	}
	if count == 0 {
		return errors.Errorf("label [%s] is not exists as a rt label", label)
	}
	// 判断同一个data_id是否已经被其他事件绑定了
	count, err = customreport.NewTimeSeriesGroupQuerySet(db).BkDataIDEq(bkDataId).Count()
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.Errorf("bk_data_id [%v] is already used by other custom group, use it first?", bkDataId)
	}
	// 判断同一个业务下是否有重名的custom_group_name
	count, err = customreport.NewTimeSeriesGroupQuerySet(db).BkBizIDEq(bkBizId).IsDeleteEq(false).TimeSeriesGroupNameEq(customGroupName).Count()
	if err != nil {
		return err
	}
	if count != 0 {
		return errors.Errorf("biz_id [%v] already has TimeSeriesGroup [%s], should change TimeSeriesGroupName and try again", bkBizId, customGroupName)
	}
	return nil
}

func (s TimeSeriesGroupSvc) MakeTableId(bkBizId int, bkDataId uint) string {
	if bkBizId != 0 {
		return fmt.Sprintf("%v_bkmonitor_time_series_%v.%v", bkBizId, bkDataId, models.TSGroupDefaultMeasurement)
	}
	return fmt.Sprintf("bkmonitor_time_series_%v.%v", bkDataId, models.TSGroupDefaultMeasurement)
}
