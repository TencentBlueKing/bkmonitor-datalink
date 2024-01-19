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
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/dependentredis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
)

func TestTimeSeriesGroupSvc_UpdateTimeSeriesMetrics(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	tsm := customreport.TimeSeriesGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID: 22112,
			TableID:  "test_for_metric_update.base",
			IsEnable: true,
		},
		TimeSeriesGroupID:   3343,
		TimeSeriesGroupName: "test_for_metric_update_group",
	}
	db.Delete(&tsm, "bk_data_id = ?", tsm.BkDataID)
	err := tsm.Create(db)
	assert.NoError(t, err)
	db.Delete(&customreport.TimeSeriesMetric{}, "group_id = ?", tsm.TimeSeriesGroupID)
	db.Delete(&resulttable.ResultTableField{}, "table_id = ?", tsm.TableID)
	score := float64(time.Now().Add(-600 * time.Second).Unix())
	mockerClient := &mocker.RedisClientMocker{
		ZcountValue: 2,
		ZRangeByScoreWithScoresValue: []goRedis.Z{
			{Score: score, Member: "metric_a"},
			{Score: score, Member: "metric_b"},
			{Score: score - 100000, Member: "metric_expired"},
		},
		HMGetValue: []interface{}{
			"{\"dimensions\":{\"d1\":{\"last_update_time\":1685503141,\"values\":[]},\"d2\":{\"last_update_time\":1685503141,\"values\":[]}}}",
			"{\"dimensions\":{\"d3\":{\"last_update_time\":1685503141,\"values\":[]},\"d4\":{\"last_update_time\":1685503141,\"values\":[]}}}",
		},
	}
	gomonkey.ApplyFunc(dependentredis.GetInstance, func() (*dependentredis.Instance, error) {
		return &dependentredis.Instance{
			Client: mockerClient,
		}, nil
	})

	svc := NewTimeSeriesGroupSvc(&tsm)
	// 测试新增
	updated, err := svc.UpdateTimeSeriesMetrics()
	assert.NoError(t, err)
	assert.True(t, updated)
	// metric
	var metricA, metricB, metricExpired customreport.TimeSeriesMetric
	var tagListA, tagListB []string
	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(tsm.TimeSeriesGroupID).FieldNameEq("metric_a").One(&metricA)
	assert.NoError(t, err)
	err = jsonx.UnmarshalString(metricA.TagList, &tagListA)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"d1", "d2", "target"}, tagListA)
	assert.Equal(t, "test_for_metric_update.metric_a", metricA.TableID)

	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(tsm.TimeSeriesGroupID).FieldNameEq("metric_b").One(&metricB)
	assert.NoError(t, err)
	err = jsonx.UnmarshalString(metricB.TagList, &tagListB)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"d3", "d4", "target"}, tagListB)
	assert.Equal(t, "test_for_metric_update.metric_b", metricB.TableID)

	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(tsm.TimeSeriesGroupID).FieldNameEq("metric_expired").One(&metricExpired)
	assert.ErrorIs(t, gorm.ErrRecordNotFound, err)

	// rtf
	var m1, m2, mExpired, d1, d2, d3, d4 resulttable.ResultTableField
	err = resulttable.NewResultTableFieldQuerySet(db).TableIDEq(tsm.TableID).TagEq(models.ResultTableFieldTagMetric).FieldTypeEq(models.ResultTableFieldTypeFloat).IsDisabledEq(false).FieldNameEq("metric_a").One(&m1)
	assert.NoError(t, err)
	err = resulttable.NewResultTableFieldQuerySet(db).TableIDEq(tsm.TableID).TagEq(models.ResultTableFieldTagMetric).FieldTypeEq(models.ResultTableFieldTypeFloat).IsDisabledEq(false).FieldNameEq("metric_b").One(&m2)
	assert.NoError(t, err)
	err = resulttable.NewResultTableFieldQuerySet(db).TableIDEq(tsm.TableID).TagEq(models.ResultTableFieldTagMetric).FieldTypeEq(models.ResultTableFieldTypeFloat).IsDisabledEq(false).FieldNameEq("metric_expired").One(&mExpired)
	assert.ErrorIs(t, gorm.ErrRecordNotFound, err)
	err = resulttable.NewResultTableFieldQuerySet(db).TableIDEq(tsm.TableID).TagEq(models.ResultTableFieldTagDimension).FieldTypeEq(models.ResultTableFieldTypeString).FieldNameEq("d1").One(&d1)
	assert.NoError(t, err)
	err = resulttable.NewResultTableFieldQuerySet(db).TableIDEq(tsm.TableID).TagEq(models.ResultTableFieldTagDimension).FieldTypeEq(models.ResultTableFieldTypeString).FieldNameEq("d2").One(&d2)
	assert.NoError(t, err)
	err = resulttable.NewResultTableFieldQuerySet(db).TableIDEq(tsm.TableID).TagEq(models.ResultTableFieldTagDimension).FieldTypeEq(models.ResultTableFieldTypeString).FieldNameEq("d3").One(&d3)
	assert.NoError(t, err)
	err = resulttable.NewResultTableFieldQuerySet(db).TableIDEq(tsm.TableID).TagEq(models.ResultTableFieldTagDimension).FieldTypeEq(models.ResultTableFieldTypeString).FieldNameEq("d4").One(&d4)
	assert.NoError(t, err)

	// tag 不一致需要更新
	metricA.TagList = `["aaa","bbb"]`
	err = metricA.Update(db, customreport.TimeSeriesMetricDBSchema.TagList)
	assert.NoError(t, err)
	// metric状态不一致，需要更新
	m1.IsDisabled = true
	err = m1.Update(db, resulttable.ResultTableFieldDBSchema.IsDisabled)
	assert.NoError(t, err)

	// 测试修改
	updated, err = svc.UpdateTimeSeriesMetrics()
	assert.NoError(t, err)
	assert.True(t, updated)

	err = resulttable.NewResultTableFieldQuerySet(db).TableIDEq(tsm.TableID).TagEq(models.ResultTableFieldTagMetric).FieldTypeEq(models.ResultTableFieldTypeFloat).IsDisabledEq(false).FieldNameEq("metric_a").One(&m1)
	assert.NoError(t, err)

	err = customreport.NewTimeSeriesMetricQuerySet(db).GroupIDEq(tsm.TimeSeriesGroupID).FieldNameEq("metric_a").One(&metricA)
	assert.NoError(t, err)
	err = jsonx.UnmarshalString(metricA.TagList, &tagListA)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{"d1", "d2", "target"}, tagListA)
}
