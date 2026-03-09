// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package task

import (
	"context"
	"reflect"
	"testing"
	"time"

	gomonkey "github.com/agiledragon/gomonkey/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	ta "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestRefreshTimeSeriesMetric_CreatedFromBkData(t *testing.T) {
	// 初始化模拟数据库配置
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	bkTenantID := "system"

	// 准备数据
	tsGroup := customreport.TimeSeriesGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID: 22112,
			TableID:  "test_for_metric_update.base",
			IsEnable: true,
		},
		BkTenantId:          bkTenantID,
		TimeSeriesGroupID:   3343,
		TimeSeriesGroupName: "test_for_metric_update_group",
	}
	db.Delete(&customreport.TimeSeriesMetric{}, "group_id = ?", tsGroup.TimeSeriesGroupID)
	db.Delete(&tsGroup, "time_series_group_id = ?", tsGroup.TimeSeriesGroupID)
	err := tsGroup.Create(db)
	assert.NoError(t, err)

	ds := resulttable.DataSource{
		BkTenantId:  bkTenantID,
		BkDataId:    22112,
		DataName:    "test_for_metric_update_name",
		CreatedFrom: common.DataIdFromBkData,
	}
	db.Delete(&ds, "bk_data_id = ?", ds.BkDataId)
	err = db.Create(&ds).Error
	assert.NoError(t, err)

	// AccessVMRecord
	vmTableName := "vm_table_name"
	vmTable := storage.AccessVMRecord{
		BkTenantId:      bkTenantID,
		ResultTableId:   "test_for_metric_update.base",
		VmResultTableId: vmTableName,
	}
	db.Delete(&vmTable)
	err = vmTable.Create(db)
	assert.NoError(t, err)

	// Mock apiservice.Bkdata.QueryMetricAndDimension to prevent external API calls
	bkdataPatch := gomonkey.ApplyMethod(reflect.TypeOf(apiservice.Bkdata), "QueryMetricAndDimension", func(_ apiservice.BkdataService, bkTenantId string, storage string, rt string, metricGroupDimensions string) ([]map[string]any, error) {
		return []map[string]any{
			{
				"field_name": "metric_a",
				"tag_value_list": map[string]any{
					"d1":     map[string]any{"last_update_time": 1685503141},
					"d2":     map[string]any{"last_update_time": 1685503141},
					"target": map[string]any{"last_update_time": 1685503141},
				},
				"last_modify_time": float64(time.Now().Add(-600 * time.Second).Unix()),
				"is_active":        true,
			},
			{
				"field_name": "metric_b",
				"tag_value_list": map[string]any{
					"d3":     map[string]any{"last_update_time": 1685503141},
					"d4":     map[string]any{"last_update_time": 1685503141},
					"target": map[string]any{"last_update_time": 1685503141},
				},
				"last_modify_time": float64(time.Now().Add(-600 * time.Second).Unix()),
				"is_active":        true,
			},
		}, nil
	})
	defer bkdataPatch.Reset()

	// Mock Redis
	mockerClient, redisPatch := mocker.DependenceRedisMocker()
	defer redisPatch.Reset()
	mockerClient.ZcountValue = 2
	mockerClient.ZRangeByScoreWithScoresValue = append(mockerClient.ZRangeByScoreWithScoresValue, []goRedis.Z{
		{Score: float64(time.Now().Add(-600 * time.Second).Unix()), Member: "metric_a"},
		{Score: float64(time.Now().Add(-600 * time.Second).Unix()), Member: "metric_b"},
		{Score: float64(time.Now().Add(-100000 * time.Second).Unix()), Member: "metric_expired"},
	}...)
	mockerClient.HMGetValue = append(mockerClient.HMGetValue, []any{
		"{\"dimensions\":{\"d1\":{\"last_update_time\":1685503141,\"values\":[]},\"d2\":{\"last_update_time\":1685503141,\"values\":[]}}}",
		"{\"dimensions\":{\"d3\":{\"last_update_time\":1685503141,\"values\":[]},\"d4\":{\"last_update_time\":1685503141,\"values\":[]}}}",
	}...)
	// mockerClient.GetValue = []byte(`["test_for_metric_update.base"]`)

	// 直接调用方法
	ctx := context.TODO()
	task := &ta.Task{}
	err = RefreshTimeSeriesMetric(ctx, task)
	assert.NoError(t, err)

	// 验证结果
	var metricA, metricB, metricExpired customreport.TimeSeriesMetric
	var tagListA, tagListB []string
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(tsGroup.TimeSeriesGroupID).FieldNameEq("metric_a").One(&metricA)
	assert.NoError(t, err)
	err = jsonx.UnmarshalString(metricA.TagList, &tagListA)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"d1", "d2", "target"}, tagListA)
	assert.Equal(t, "test_for_metric_update.metric_a", metricA.TableID)

	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(tsGroup.TimeSeriesGroupID).FieldNameEq("metric_b").One(&metricB)
	assert.NoError(t, err)
	err = jsonx.UnmarshalString(metricB.TagList, &tagListB)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"d3", "d4", "target"}, tagListB)
	assert.Equal(t, "test_for_metric_update.metric_b", metricB.TableID)

	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(tsGroup.TimeSeriesGroupID).FieldNameEq("metric_expired").One(&metricExpired)
	assert.ErrorIs(t, gorm.ErrRecordNotFound, err)
}
