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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

const (
	testGroupID  = 100
	testTableID  = "test_is_active.__default__"
	testTenantID = "system"
)

// setupTestData 设置测试数据，返回清理函数
func setupTestData(t *testing.T, groupID uint, metrics []customreport.TimeSeriesMetric) func() {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	// 清理旧数据
	db.Delete(&customreport.TimeSeriesMetric{}, "group_id = ?", groupID)

	// 创建测试数据
	for _, metric := range metrics {
		// 确保所有必需字段都有默认值
		if metric.Label == "" {
			metric.Label = ""
		}
		if metric.LastIndex == 0 {
			metric.LastIndex = 0
		}
		if metric.LastModifyTime.IsZero() {
			metric.LastModifyTime = time.Now()
		}

		// 记录原始的 IsActive 值
		originalIsActive := metric.IsActive

		// 使用原生 SQL 插入，避免 GORM 的默认值干扰
		// 直接插入所有字段，包括 is_active
		err := db.Exec(`
			INSERT INTO metadata_timeseriesmetric 
			(group_id, table_id, field_name, tag_list, last_modify_time, last_index, label, is_active)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, metric.GroupID, metric.TableID, metric.FieldName, metric.TagList,
			metric.LastModifyTime, metric.LastIndex, metric.Label, originalIsActive).Error
		require.NoError(t, err)

		// 验证插入是否成功
		var checkMetric customreport.TimeSeriesMetric
		err = db.Where("group_id = ? AND field_name = ?", metric.GroupID, metric.FieldName).First(&checkMetric).Error
		require.NoError(t, err)
		if !originalIsActive {
			require.False(t, checkMetric.IsActive, fmt.Sprintf("Failed to set is_active=false for metric %s", metric.FieldName))
		}
	}

	// 返回清理函数
	return func() {
		db.Delete(&customreport.TimeSeriesMetric{}, "group_id = ?", groupID)
	}
}

// createMetricInfo 创建测试用的 metricInfo
func createMetricInfo(fieldName string, currTime int64) map[string]any {
	return map[string]any{
		"field_name":       fieldName,
		"last_modify_time": float64(currTime),
		"tag_value_list": map[string]any{
			"endpoint": map[string]any{
				"last_update_time": currTime,
				"values":           []any{},
			},
			"target": map[string]any{
				"last_update_time": currTime,
				"values":           []any{},
			},
		},
		"is_active": true, // 从 Redis/BkData 获取的指标都是活跃的
	}
}

// TestCreateMetricWithIsActiveTrue 测试新创建的指标，is_active 字段应该设置为 True
func TestCreateMetricWithIsActiveTrue(t *testing.T) {
	cleanup := setupTestData(t, testGroupID, []customreport.TimeSeriesMetric{})
	defer cleanup()

	currTime := time.Now().Unix()
	metricInfoList := []map[string]any{
		createMetricInfo("new_metric1", currTime),
	}

	svc := &TimeSeriesMetricSvc{}
	_, err := svc.BulkRefreshTSMetrics(testTenantID, testGroupID, testTableID, metricInfoList, true)
	require.NoError(t, err)

	// 验证新创建的指标存在且 is_active=True
	db := mysql.GetDBSession().DB
	var newMetric customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("new_metric1").One(&newMetric)
	require.NoError(t, err)
	assert.True(t, newMetric.IsActive, "newly created metric should have is_active=true")

	// 验证总数
	var count int64
	db.Model(&customreport.TimeSeriesMetric{}).Where("group_id = ?", testGroupID).Count(&count)
	assert.Equal(t, int64(1), count)
}

// TestUpdateExistingMetricToActive 测试更新已存在的指标，如果指标在返回列表中，is_active 应该更新为 True
func TestUpdateExistingMetricToActive(t *testing.T) {
	tagListStr, _ := jsonx.MarshalString([]string{"tag1", "tag2"})
	cleanup := setupTestData(t, testGroupID, []customreport.TimeSeriesMetric{
		{
			GroupID:   testGroupID,
			TableID:   "test_is_active.metric3",
			FieldName: "metric3",
			TagList:   tagListStr,
			IsActive:  false, // 原本是非活跃状态
		},
	})
	defer cleanup()

	currTime := time.Now().Unix()
	// metric3 原本是 is_active=False，现在在返回列表中，应该更新为 True
	metricInfoList := []map[string]any{
		createMetricInfo("metric3", currTime),
	}

	// 验证更新前 metric3 是 False
	db := mysql.GetDBSession().DB
	var metric3Before customreport.TimeSeriesMetric
	err := customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric3").One(&metric3Before)
	require.NoError(t, err)
	assert.False(t, metric3Before.IsActive, "metric3 should be false before update")

	svc := &TimeSeriesMetricSvc{}
	_, err = svc.BulkRefreshTSMetrics(testTenantID, testGroupID, testTableID, metricInfoList, true)
	require.NoError(t, err)

	// 验证更新后 metric3 是 True
	var metric3After customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric3").One(&metric3After)
	require.NoError(t, err)
	assert.True(t, metric3After.IsActive, "metric3 should be true after update")
}

// TestSetMetricToInactiveWhenNotInList 测试不在返回列表中的已存在指标，is_active 应该设置为 False
func TestSetMetricToInactiveWhenNotInList(t *testing.T) {
	tagListStr, _ := jsonx.MarshalString([]string{"tag1", "tag2"})
	cleanup := setupTestData(t, testGroupID, []customreport.TimeSeriesMetric{
		{
			GroupID:   testGroupID,
			TableID:   "test_is_active.metric1",
			FieldName: "metric1",
			TagList:   tagListStr,
			IsActive:  true,
		},
		{
			GroupID:   testGroupID,
			TableID:   "test_is_active.metric2",
			FieldName: "metric2",
			TagList:   tagListStr,
			IsActive:  true,
		},
		{
			GroupID:   testGroupID,
			TableID:   "test_is_active.metric3",
			FieldName: "metric3",
			TagList:   tagListStr,
			IsActive:  false,
		},
	})
	defer cleanup()

	currTime := time.Now().Unix()
	// 只返回 metric1，metric2 和 metric3 不在列表中
	metricInfoList := []map[string]any{
		createMetricInfo("metric1", currTime),
	}

	// 验证更新前 metric2 是 True
	db := mysql.GetDBSession().DB
	var metric2Before customreport.TimeSeriesMetric
	err := customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric2").One(&metric2Before)
	require.NoError(t, err)
	assert.True(t, metric2Before.IsActive, "metric2 should be true before update")

	svc := &TimeSeriesMetricSvc{}
	_, err = svc.BulkRefreshTSMetrics(testTenantID, testGroupID, testTableID, metricInfoList, true)
	require.NoError(t, err)

	// 验证 metric1 仍然是 True（在列表中）
	var metric1 customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric1").One(&metric1)
	require.NoError(t, err)
	assert.True(t, metric1.IsActive, "metric1 should be true (in the list)")

	// 验证 metric2 更新为 False（不在列表中）
	var metric2After customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric2").One(&metric2After)
	require.NoError(t, err)
	assert.False(t, metric2After.IsActive, "metric2 should be false (not in the list)")

	// 验证 metric3 仍然是 False（不在列表中，且原本就是 False）
	var metric3 customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric3").One(&metric3)
	require.NoError(t, err)
	assert.False(t, metric3.IsActive, "metric3 should be false (not in the list)")
}

// TestMixedScenarioActiveAndInactive 测试混合场景：部分指标在返回列表中，部分不在
func TestMixedScenarioActiveAndInactive(t *testing.T) {
	tagListStr, _ := jsonx.MarshalString([]string{"tag1", "tag2"})
	cleanup := setupTestData(t, testGroupID, []customreport.TimeSeriesMetric{
		{
			GroupID:   testGroupID,
			TableID:   "test_is_active.metric1",
			FieldName: "metric1",
			TagList:   tagListStr,
			IsActive:  true,
		},
		{
			GroupID:   testGroupID,
			TableID:   "test_is_active.metric2",
			FieldName: "metric2",
			TagList:   tagListStr,
			IsActive:  true,
		},
		{
			GroupID:   testGroupID,
			TableID:   "test_is_active.metric3",
			FieldName: "metric3",
			TagList:   tagListStr,
			IsActive:  false,
		},
	})
	defer cleanup()

	currTime := time.Now().Unix()
	// 返回 metric1 和 metric2，不返回 metric3
	metricInfoList := []map[string]any{
		createMetricInfo("metric1", currTime),
		createMetricInfo("metric2", currTime),
	}

	svc := &TimeSeriesMetricSvc{}
	_, err := svc.BulkRefreshTSMetrics(testTenantID, testGroupID, testTableID, metricInfoList, true)
	require.NoError(t, err)

	// 验证在列表中的指标是 True
	db := mysql.GetDBSession().DB
	var metric1 customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric1").One(&metric1)
	require.NoError(t, err)
	assert.True(t, metric1.IsActive, "metric1 should be true (in the list)")

	var metric2 customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric2").One(&metric2)
	require.NoError(t, err)
	assert.True(t, metric2.IsActive, "metric2 should be true (in the list)")

	// 验证不在列表中的指标是 False
	var metric3 customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric3").One(&metric3)
	require.NoError(t, err)
	assert.False(t, metric3.IsActive, "metric3 should be false (not in the list)")
}

// TestCreateAndUpdateMetricsTogether 测试同时创建新指标和更新已存在指标的场景
func TestCreateAndUpdateMetricsTogether(t *testing.T) {
	tagListStr, _ := jsonx.MarshalString([]string{"tag1", "tag2"})
	cleanup := setupTestData(t, testGroupID, []customreport.TimeSeriesMetric{
		{
			GroupID:   testGroupID,
			TableID:   "test_is_active.metric1",
			FieldName: "metric1",
			TagList:   tagListStr,
			IsActive:  true,
		},
		{
			GroupID:   testGroupID,
			TableID:   "test_is_active.metric2",
			FieldName: "metric2",
			TagList:   tagListStr,
			IsActive:  true,
		},
		{
			GroupID:   testGroupID,
			TableID:   "test_is_active.metric3",
			FieldName: "metric3",
			TagList:   tagListStr,
			IsActive:  false,
		},
	})
	defer cleanup()

	currTime := time.Now().Unix()
	// 包含新指标和已存在的指标
	metricInfoList := []map[string]any{
		createMetricInfo("metric1", currTime),     // 已存在，在列表中
		createMetricInfo("new_metric2", currTime), // 新指标
	}

	db := mysql.GetDBSession().DB
	initialCount := int64(0)
	db.Model(&customreport.TimeSeriesMetric{}).Where("group_id = ?", testGroupID).Count(&initialCount)
	assert.Equal(t, int64(3), initialCount)

	svc := &TimeSeriesMetricSvc{}
	_, err := svc.BulkRefreshTSMetrics(testTenantID, testGroupID, testTableID, metricInfoList, true)
	require.NoError(t, err)

	// 验证新指标创建成功且 is_active=True
	var newMetric customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("new_metric2").One(&newMetric)
	require.NoError(t, err)
	assert.True(t, newMetric.IsActive, "new metric should have is_active=true")

	// 验证已存在的指标仍然是 True
	var metric1 customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric1").One(&metric1)
	require.NoError(t, err)
	assert.True(t, metric1.IsActive, "metric1 should be true (in the list)")

	// 验证不在列表中的指标更新为 False
	var metric2 customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric2").One(&metric2)
	require.NoError(t, err)
	assert.False(t, metric2.IsActive, "metric2 should be false (not in the list)")

	var metric3 customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric3").One(&metric3)
	require.NoError(t, err)
	assert.False(t, metric3.IsActive, "metric3 should be false (not in the list)")

	// 验证总数增加了
	finalCount := int64(0)
	db.Model(&customreport.TimeSeriesMetric{}).Where("group_id = ?", testGroupID).Count(&finalCount)
	assert.Equal(t, int64(4), finalCount)
}

// TestEmptyMetricListSkipsUpdate 测试当返回的指标列表为空时，应该跳过更新以避免误操作
// 这是为了防止上游异常、限流或拉取失败时，误将所有指标标记为不活跃
func TestEmptyMetricListSkipsUpdate(t *testing.T) {
	tagListStr, _ := jsonx.MarshalString([]string{"tag1", "tag2"})
	cleanup := setupTestData(t, testGroupID, []customreport.TimeSeriesMetric{
		{
			GroupID:   testGroupID,
			TableID:   "test_is_active.metric1",
			FieldName: "metric1",
			TagList:   tagListStr,
			IsActive:  true,
		},
		{
			GroupID:   testGroupID,
			TableID:   "test_is_active.metric2",
			FieldName: "metric2",
			TagList:   tagListStr,
			IsActive:  true,
		},
		{
			GroupID:   testGroupID,
			TableID:   "test_is_active.metric3",
			FieldName: "metric3",
			TagList:   tagListStr,
			IsActive:  false,
		},
	})
	defer cleanup()

	// 返回空列表（模拟上游异常）
	metricInfoList := []map[string]any{}

	// 验证更新前所有指标的状态
	db := mysql.GetDBSession().DB
	var metric1Before customreport.TimeSeriesMetric
	err := customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric1").One(&metric1Before)
	require.NoError(t, err)
	assert.True(t, metric1Before.IsActive, "metric1 should be true before update")

	var metric2Before customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric2").One(&metric2Before)
	require.NoError(t, err)
	assert.True(t, metric2Before.IsActive, "metric2 should be true before update")

	svc := &TimeSeriesMetricSvc{}
	needPush, err := svc.BulkRefreshTSMetrics(testTenantID, testGroupID, testTableID, metricInfoList, true)
	require.NoError(t, err)
	assert.False(t, needPush, "should not need to push when skipping update")

	// 验证所有指标状态保持不变（没有被误标记为 inactive）
	var metric1After customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric1").One(&metric1After)
	require.NoError(t, err)
	assert.True(t, metric1After.IsActive, "metric1 should remain true (not updated)")

	var metric2After customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric2").One(&metric2After)
	require.NoError(t, err)
	assert.True(t, metric2After.IsActive, "metric2 should remain true (not updated)")

	var metric3 customreport.TimeSeriesMetric
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(testGroupID).FieldNameEq("metric3").One(&metric3)
	require.NoError(t, err)
	assert.False(t, metric3.IsActive, "metric3 should remain false (not updated)")
}

// TestBulkRefreshTSMetrics_UpdateScenario 原有的测试用例，保留用于兼容性
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
			"is_active": true,
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
			"is_active": true,
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
			// 验证 is_active 字段
			assert.True(t, metric.IsActive, "active_tasks should be active")
		case "backup_tasks_count":
			// 比较时间戳是否大于已有的记录
			assert.True(t, metric.LastModifyTime.After(existingMetrics[1].LastModifyTime))

			// 确保 TagList 被正确反序列化为字符串数组
			var tagList []string
			err := json.Unmarshal([]byte(metric.TagList), &tagList)
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"target", "module", "location"}, tagList)
			// 验证 is_active 字段
			assert.True(t, metric.IsActive, "backup_tasks_count should be active")
		}
	}
}
