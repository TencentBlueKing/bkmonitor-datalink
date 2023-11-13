// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package customreport

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	mapset "github.com/deckarep/golang-set"
	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	redisStore "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/dependentredis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mapx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

//go:generate goqueryset -in timeseriesgroup.go -out qs_tsgroup.go

// TimeSeriesGroup : time series group model
// gen:qs
type TimeSeriesGroup struct {
	CustomGroupBase
	TimeSeriesGroupID   uint   `json:"time_series_group_id" gorm:"unique"`
	TimeSeriesGroupName string `json:"time_series_group_name" gorm:"size:255"`
}

// TableName : 用于设置表的别名
func (TimeSeriesGroup) TableName() string {
	return "metadata_timeseriesgroup"
}

// UpdateMetricsFromRedis: update ts metrics from redis record
func (ts *TimeSeriesGroup) UpdateMetricsFromRedis() error {
	// 获取 redis 中数据，用于后续指标及tag的更新
	metricInfo, err := ts.GetRedisData()
	if err != nil {
		return err
	}
	// 更新 ts 操作
	tsm := TimeSeriesMetric{GroupID: ts.TimeSeriesGroupID}
	if err := tsm.UpdateMetrics(metricInfo); err != nil {
		return err
	}
	// 更新结果表操作
	tagSet := mapset.NewSet()
	username := config.DefaultUsername
	for _, m := range metricInfo {
		// 转换获取对应的值
		fieldName := m["field_name"].(string)
		// TODO: 暂时没有使用，先注释掉
		// label := mapx.GetValWithDefault(m, "label", models.ResultTableLabelOther)
		// 兼容传入 tag_value_list/tag_list 的情况
		tagList := mapx.GetValWithDefault(m, "tag_list", mapx.GetMapKeys(m["tag_value_list"].(map[string]interface{})))
		rtf := resulttable.ResultTableField{
			TableID:        ts.TableID,
			FieldName:      fieldName,
			Tag:            models.ResultTableFieldTagMetric,
			FieldType:      models.ResultTableFieldTypeFloat,
			Creator:        username,
			LastModifyUser: username,
			DefaultValue:   "0",
			IsConfigByUser: true,
			LastModifyTime: time.Now(),
			CreateTime:     time.Now(),
		}
		if err := rtf.UpdateMetricFieldFromTS(); err != nil {
			return err
		}
		tagSet.Union(slicex.StringList2Set(tagList.([]string)))
	}
	tagSlice := tagSet.ToSlice()
	for _, tag := range tagSlice {
		rtf := resulttable.ResultTableField{
			TableID:        ts.TableID,
			FieldName:      tag.(string),
			Tag:            models.ResultTableFieldTagDimension,
			FieldType:      models.ResultTableFieldTypeString,
			Creator:        username,
			LastModifyUser: username,
			DefaultValue:   "",
			IsConfigByUser: true,
			LastModifyTime: time.Now(),
			CreateTime:     time.Now(),
		}
		if err := rtf.UpdateMetricFieldFromTS(); err != nil {
			logger.Errorf("update field: %s dimension error: %v", tag, err)
		}
	}

	logger.Infof("table: [%s] now process metrics done", ts.TableID)
	return nil
}

// GetRedisData get data from redis
func (ts *TimeSeriesGroup) GetRedisData() ([]map[string]interface{}, error) {
	// 获取要处理的指标和维度的标识
	metricKey := fmt.Sprintf("%s%d", config.MetadataMetricDimensionMetricKeyPrefix, ts.BkDataID)
	metricDimensionsKey := fmt.Sprintf("%s%d", config.MetadataMetricDimensionKeyPrefix, ts.BkDataID)
	fetchStep := config.MetadataMetricDimensionMaxMetricFetchStep
	// 转换时间
	nowTime := time.Now()
	nowTimeStampStr := fmt.Sprintf("%d", nowTime.Unix())
	// NOTE: 使用ADD，参数为负值
	validBeginTimeStamp := nowTime.Add(
		-time.Duration(config.MetadataMetricDimensionTimeSeriesMetricExpiredDays) * time.Hour * 24,
	).Unix()
	validBeginTimeStampStr := fmt.Sprintf("%d", validBeginTimeStamp)
	ctx := context.Background()
	redisClient, err := redisStore.GetInstance(ctx)
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
			if err := json.Unmarshal([]byte(fmt.Sprint(dimension)), &dimensionsMap); err != nil {
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
