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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestBulkRefreshTSMetrics_UpdateScenario(t *testing.T) {
	// 初始化数据库连接
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	// 执行测试前，需要先注释掉 BeforeCreate，否则无法创建历史时间的TSMetrics

	// 准备已有的数据
	existingMetrics := []customreport.TimeSeriesMetric{
		{
			GroupID:        100376,
			FieldName:      "active_tasks",
			TagList:        `["target", "module", "location"]`,
			LastModifyTime: time.Unix(1722942000, 0), // 2024-08-06 19:00:00 UTC
		},
		{
			GroupID:        100376,
			FieldName:      "backup_tasks_count",
			TagList:        `["target", "module", "location"]`,
			LastModifyTime: time.Unix(1722942000, 0), // 2024-09-17 15:37:00 UTC
		},
	}

	for _, metric := range existingMetrics {
		db.Delete(&metric, "group_id = ? AND field_name = ?", metric.GroupID, metric.FieldName)
		err := metric.Create(db)
		require.NoError(t, err)
	}

	var insertedMetrics []customreport.TimeSeriesMetric
	err2 := db.Where("group_id = ?", 100376).Find(&insertedMetrics).Error
	require.NoError(t, err2)
	for _, metric := range insertedMetrics {
		fmt.Printf("Inserted metric: %+v\n", metric)
	}

	// 准备测试输入
	metricInfoList := []map[string]any{
		{
			"field_name":       "active_tasks",
			"last_modify_time": 1726728019, // 2024-09-19 14:40:19 UTC
			"tag_value_list": map[string]any{
				"location": map[string]any{
					"last_update_time": 1726728019,
					"values":           []any{},
				},
				"module": map[string]any{
					"last_update_time": 1726728019,
					"values":           []any{},
				},
				"target": map[string]any{
					"last_update_time": 1726728019,
					"values":           []any{},
				},
			},
		},
		{
			"field_name":       "backup_tasks_count",
			"last_modify_time": 1726728019, // 2024-09-19 14:40:19 UTC
			"tag_value_list": map[string]any{
				"location": map[string]any{
					"last_update_time": 1726728019,
					"values":           []any{},
				},
				"module": map[string]any{
					"last_update_time": 1726728019,
					"values":           []any{},
				},
				"target": map[string]any{
					"last_update_time": 1726728019,
					"values":           []any{},
				},
			},
		},
	}

	svc := &TimeSeriesMetricSvc{}

	// 调用 BulkRefreshTSMetrics
	needPush, err := svc.BulkRefreshTSMetrics("system", 100376, "test_table", metricInfoList, true)
	assert.NoError(t, err)
	assert.False(t, needPush)

	// 验证数据库中的记录
	var updatedMetrics []customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(100376).All(&updatedMetrics)
	require.NoError(t, err)
	require.Len(t, updatedMetrics, 2)

	// 验证每条记录的更新情况
	for _, metric := range updatedMetrics {
		switch metric.FieldName {
		case "active_tasks":
			// 比较时间戳是否大于已有的记录
			assert.True(t, metric.LastModifyTime.After(existingMetrics[0].LastModifyTime))

			// 确保 TagList 被正确反序列化为字符串数组
			var tagList []string
			err := json.Unmarshal([]byte(metric.TagList), &tagList)
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"target", "module", "location"}, tagList)
		case "backup_tasks_count":
			// 比较时间戳是否大于已有的记录
			assert.True(t, metric.LastModifyTime.After(existingMetrics[1].LastModifyTime))

			// 确保 TagList 被正确反序列化为字符串数组
			var tagList []string
			err := json.Unmarshal([]byte(metric.TagList), &tagList)
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"target", "module", "location"}, tagList)
		}
	}
}
