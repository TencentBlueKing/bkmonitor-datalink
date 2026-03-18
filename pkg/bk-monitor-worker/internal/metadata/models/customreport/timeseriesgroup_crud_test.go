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

func TestTimeSeriesGroupCRUDModel(t *testing.T) {
	mocker.InitTestDBConfig("../../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	suffix := time.Now().UnixNano()
	groupID := uint(suffix % 1000000000)

	group := TimeSeriesGroup{
		CustomGroupBase: CustomGroupBase{
			BkDataID:            uint(900000 + suffix%100000),
			BkBizID:             2,
			TableID:             fmt.Sprintf("ut.tsgroup_%d", suffix),
			IsEnable:            true,
			MaxFutureTimeOffset: -1,
		},
		BkTenantId:          "system",
		TimeSeriesGroupID:   groupID,
		TimeSeriesGroupName: fmt.Sprintf("group_%d", suffix),
	}
	db.Delete(&TimeSeriesGroup{}, "time_series_group_id = ?", groupID)

	err := group.Create(db)
	require.NoError(t, err)

	var created TimeSeriesGroup
	err = NewTimeSeriesGroupQuerySet(db).TimeSeriesGroupIDEq(groupID).One(&created)
	require.NoError(t, err)
	assert.JSONEq(t, "[]", created.MetricGroupDimensions)
	assert.Equal(t, -1, created.MaxRate)
	assert.Equal(t, "other", created.Label)

	created.TimeSeriesGroupName = "group_updated"
	created.MetricGroupDimensions = `[{"field_name":"target","default_value":"default"}]`
	err = created.Update(db, TimeSeriesGroupDBSchema.TimeSeriesGroupName, TimeSeriesGroupDBSchema.MetricGroupDimensions)
	require.NoError(t, err)

	var updated TimeSeriesGroup
	err = NewTimeSeriesGroupQuerySet(db).TimeSeriesGroupIDEq(groupID).One(&updated)
	require.NoError(t, err)
	assert.Equal(t, "group_updated", updated.TimeSeriesGroupName)
	assert.JSONEq(t, `[{"field_name":"target","default_value":"default"}]`, updated.MetricGroupDimensions)

	err = updated.Delete(db)
	require.NoError(t, err)

	var deleted TimeSeriesGroup
	err = NewTimeSeriesGroupQuerySet(db).TimeSeriesGroupIDEq(groupID).One(&deleted)
	assert.True(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestTimeSeriesGroupBatchCRUDModel(t *testing.T) {
	mocker.InitTestDBConfig("../../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB

	suffix := time.Now().UnixNano()
	baseID := uint(suffix % 1000000000)
	ids := []uint{baseID + 1, baseID + 2, baseID + 3}

	for _, id := range ids {
		db.Delete(&TimeSeriesGroup{}, "time_series_group_id = ?", id)
	}

	groups := []TimeSeriesGroup{
		{
			CustomGroupBase: CustomGroupBase{
				BkDataID:            uint(910000 + suffix%100000),
				BkBizID:             3,
				TableID:             fmt.Sprintf("ut.tsgroup_batch_%d_1", suffix),
				IsEnable:            true,
				MaxFutureTimeOffset: -1,
			},
			BkTenantId:          "system",
			TimeSeriesGroupID:   ids[0],
			TimeSeriesGroupName: "batch_group_1",
		},
		{
			CustomGroupBase: CustomGroupBase{
				BkDataID:            uint(920000 + suffix%100000),
				BkBizID:             3,
				TableID:             fmt.Sprintf("ut.tsgroup_batch_%d_2", suffix),
				IsEnable:            true,
				MaxFutureTimeOffset: -1,
			},
			BkTenantId:          "system",
			TimeSeriesGroupID:   ids[1],
			TimeSeriesGroupName: "batch_group_2",
		},
		{
			CustomGroupBase: CustomGroupBase{
				BkDataID:            uint(930000 + suffix%100000),
				BkBizID:             3,
				TableID:             fmt.Sprintf("ut.tsgroup_batch_%d_3", suffix),
				IsEnable:            true,
				MaxFutureTimeOffset: -1,
			},
			BkTenantId:          "system",
			TimeSeriesGroupID:   ids[2],
			TimeSeriesGroupName: "batch_group_3",
		},
	}
	for i := range groups {
		err := groups[i].Create(db)
		require.NoError(t, err)
	}

	updateDB := db.Model(&TimeSeriesGroup{}).
		Where("time_series_group_id IN (?)", ids).
		Updates(map[string]any{
			"time_series_group_name":  "batch_group_updated",
			"metric_group_dimensions": `[{"field_name":"target","default_value":"default"}]`,
		})
	require.NoError(t, updateDB.Error)
	assert.Equal(t, int64(3), updateDB.RowsAffected)

	var updated []TimeSeriesGroup
	err := NewTimeSeriesGroupQuerySet(db).TimeSeriesGroupIDIn(ids...).All(&updated)
	require.NoError(t, err)
	require.Len(t, updated, 3)
	for _, g := range updated {
		assert.Equal(t, "batch_group_updated", g.TimeSeriesGroupName)
		assert.JSONEq(t, `[{"field_name":"target","default_value":"default"}]`, g.MetricGroupDimensions)
	}

	deleteDB := db.Where("time_series_group_id IN (?)", ids).Delete(&TimeSeriesGroup{})
	require.NoError(t, deleteDB.Error)
	assert.Equal(t, int64(3), deleteDB.RowsAffected)
}
