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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestTimeSeriesMetricCRUDModel(t *testing.T) {
	mocker.InitTestDBConfig("../../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	suffix := time.Now().UnixNano()
	groupID := uint(800000 + suffix%100000)
	fieldName := fmt.Sprintf("metric_%d", suffix)

	metric := TimeSeriesMetric{
		GroupID:    groupID,
		ScopeID:    1,
		TableID:    fmt.Sprintf("ut.tsmetric_%d", suffix),
		FieldScope: "default",
		FieldName:  fieldName,
		// MySQL strict mode + gorm zero-value behavior may omit false on insert.
		// Set true on create, then verify false via update.
		IsActive: true,
	}
	db.Delete(&TimeSeriesMetric{}, "group_id = ? AND field_name = ?", groupID, fieldName)

	err := metric.Create(db)
	require.NoError(t, err)

	var created TimeSeriesMetric
	err = NewTimeSeriesMetricQuerySet(db).GroupIDEq(groupID).FieldNameEq(fieldName).One(&created)
	require.NoError(t, err)
	assert.JSONEq(t, "[]", created.TagList)
	assert.JSONEq(t, "{}", created.FieldConfig)
	assert.True(t, created.IsActive)

	created.TagList = `["target","module"]`
	created.IsActive = false
	err = created.Update(db, TimeSeriesMetricDBSchema.TagList, TimeSeriesMetricDBSchema.IsActive)
	require.NoError(t, err)

	var updated TimeSeriesMetric
	err = NewTimeSeriesMetricQuerySet(db).GroupIDEq(groupID).FieldNameEq(fieldName).One(&updated)
	require.NoError(t, err)
	assert.JSONEq(t, `["target","module"]`, updated.TagList)
	assert.False(t, updated.IsActive)

	err = updated.Delete(db)
	require.NoError(t, err)

	var deleted TimeSeriesMetric
	err = NewTimeSeriesMetricQuerySet(db).GroupIDEq(groupID).FieldNameEq(fieldName).One(&deleted)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestTimeSeriesMetricBatchCRUDModel(t *testing.T) {
	mocker.InitTestDBConfig("../../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	suffix := time.Now().UnixNano()
	groupID := uint(880000 + suffix%100000)
	fieldNames := []string{
		fmt.Sprintf("batch_metric_%d_1", suffix),
		fmt.Sprintf("batch_metric_%d_2", suffix),
		fmt.Sprintf("batch_metric_%d_3", suffix),
	}
	for _, name := range fieldNames {
		db.Delete(&TimeSeriesMetric{}, "group_id = ? AND field_name = ?", groupID, name)
	}

	metrics := []TimeSeriesMetric{
		{
			GroupID:    groupID,
			ScopeID:    1,
			TableID:    fmt.Sprintf("ut.tsmetric_batch_%d", suffix),
			FieldScope: "default",
			FieldName:  fieldNames[0],
			IsActive:   true,
		},
		{
			GroupID:    groupID,
			ScopeID:    1,
			TableID:    fmt.Sprintf("ut.tsmetric_batch_%d", suffix),
			FieldScope: "default",
			FieldName:  fieldNames[1],
			IsActive:   true,
		},
		{
			GroupID:    groupID,
			ScopeID:    1,
			TableID:    fmt.Sprintf("ut.tsmetric_batch_%d", suffix),
			FieldScope: "default",
			FieldName:  fieldNames[2],
			IsActive:   true,
		},
	}
	for i := range metrics {
		err := metrics[i].Create(db)
		require.NoError(t, err)
	}

	updateDB := db.Model(&TimeSeriesMetric{}).
		Where("group_id = ? AND field_name IN (?)", groupID, fieldNames).
		Updates(map[string]any{
			"tag_list":  `["target","module"]`,
			"is_active": false,
		})
	require.NoError(t, updateDB.Error)
	assert.Equal(t, int64(3), updateDB.RowsAffected)

	var updated []TimeSeriesMetric
	err := NewTimeSeriesMetricQuerySet(db).GroupIDEq(groupID).FieldNameIn(fieldNames...).All(&updated)
	require.NoError(t, err)
	require.Len(t, updated, 3)
	for _, m := range updated {
		assert.JSONEq(t, `["target","module"]`, m.TagList)
		assert.False(t, m.IsActive)
	}

	deleteDB := db.Where("group_id = ? AND field_name IN (?)", groupID, fieldNames).Delete(&TimeSeriesMetric{})
	require.NoError(t, deleteDB.Error)
	assert.Equal(t, int64(3), deleteDB.RowsAffected)
}
