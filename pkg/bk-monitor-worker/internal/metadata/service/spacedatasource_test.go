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

	"github.com/jinzhu/gorm"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
)

func TestSpaceDataSourceSvc_CreateBkccSpaceDataSource(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	bizDataIdsMap := map[int][]uint{
		0:  {10000001, 10000002},
		99: {99000001, 99000002},
	}
	db.Delete(&space.SpaceDataSource{}, "space_id in (?)", []string{"0", "99"})
	svc := NewSpaceDataSourceSvc(nil)
	err := svc.CreateBkccSpaceDataSource(bizDataIdsMap)
	assert.NoError(t, err)
	var sd1, sd2, sd space.SpaceDataSource
	err = space.NewSpaceDataSourceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCC).SpaceIdEq("99").BkDataIdEq(99000001).One(&sd1)
	assert.NoError(t, err)
	err = space.NewSpaceDataSourceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCC).SpaceIdEq("99").BkDataIdEq(99000002).One(&sd2)
	assert.NoError(t, err)
	err = space.NewSpaceDataSourceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCC).SpaceIdEq("0").One(&sd)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

}

func TestSpaceDataSourceSvc_getRealBizId(t *testing.T) {
	type fields struct {
		SpaceDataSource *space.SpaceDataSource
	}
	type args struct {
		dataName       string
		spaceUid       string
		isInTsGroup    bool
		isInEventGroup bool
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   int
	}{
		{name: "0", fields: fields{SpaceDataSource: nil}, args: args{dataName: "dt_name", spaceUid: "", isInTsGroup: false, isInEventGroup: false}, want: 0},
		{name: "ts_2", fields: fields{SpaceDataSource: nil}, args: args{dataName: "2_dt_name", spaceUid: "", isInTsGroup: true, isInEventGroup: false}, want: 2},
		{name: "event_2", fields: fields{SpaceDataSource: nil}, args: args{dataName: "dt_name_2", spaceUid: "", isInTsGroup: false, isInEventGroup: true}, want: 2},
		{name: "space_uid 2", fields: fields{SpaceDataSource: nil}, args: args{dataName: "dt_name", spaceUid: "bkcc__2", isInTsGroup: false, isInEventGroup: false}, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := &SpaceDataSourceSvc{
				SpaceDataSource: tt.fields.SpaceDataSource,
			}
			assert.Equalf(t, tt.want, sp.getRealBizId(tt.args.dataName, tt.args.spaceUid, tt.args.isInTsGroup, tt.args.isInEventGroup), "getRealBizId(%v, %v, %v, %v)", tt.args.dataName, tt.args.spaceUid, tt.args.isInTsGroup, tt.args.isInEventGroup)
		})
	}
}

func TestSpaceDataSourceSvc_refineBizDataIdMap(t *testing.T) {
	svc := NewSpaceDataSourceSvc(nil)
	bizDataMap := map[int][]uint{
		2: {2, 3},
	}
	// dataid不存在
	assert.False(t, svc.refineBizDataIdMap(bizDataMap, "2", 1))
	// bizId不存在
	assert.False(t, svc.refineBizDataIdMap(bizDataMap, "3", 1))
	// 存在
	assert.True(t, svc.refineBizDataIdMap(bizDataMap, "2", 2))
	// 被移除
	assert.NotEmpty(t, bizDataMap[2])
	assert.False(t, slicex.IsExistItem(bizDataMap[2], 2))
	// 存在，最后一个移除key
	assert.True(t, svc.refineBizDataIdMap(bizDataMap, "2", 3))
	assert.Empty(t, bizDataMap[2])
}

func TestSpaceDataSourceSvc_getBizDataIds(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	rt := resulttable.ResultTable{
		TableId:       "space_data_test_rt",
		TableNameZh:   "space_data_test_rt",
		IsCustomTable: true,
		BkBizId:       998,
		IsEnable:      true,
	}
	db.Delete(&rt, "bk_biz_id = ?", rt.BkBizId)
	err := rt.Create(db)
	assert.NoError(t, err)
	dsrt := resulttable.DataSourceResultTable{
		BkDataId: 989,
		TableId:  rt.TableId,
	}
	db.Delete(&dsrt, "table_id = ?", dsrt.TableId)
	err = dsrt.Create(db)
	svc := NewSpaceDataSourceSvc(nil)
	bizDataIdsMap, err := svc.getBizDataIds()
	assert.NoError(t, err)
	ids := bizDataIdsMap[rt.BkBizId]
	assert.ElementsMatch(t, ids, []uint{dsrt.BkDataId})
}

func TestSpaceDataSourceSvc_getRealZeroBizDataId(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	db := mysql.GetDBSession().DB
	// 模拟rt
	rtEvent := resulttable.ResultTable{
		TableId:       "space_data_test_rt_zero_event_111",
		TableNameZh:   "space_data_test_rt_zero_event_111",
		IsCustomTable: true,
		BkBizId:       0,
		IsEnable:      true,
	}
	rtTs := resulttable.ResultTable{
		TableId:       "112_space_data_test_rt_zero_ts",
		TableNameZh:   "112_space_data_test_rt_zero_ts",
		IsCustomTable: true,
		BkBizId:       0,
		IsEnable:      true,
	}
	db.Delete(&resulttable.ResultTable{}, "table_id in (?)", []string{rtEvent.TableId, rtTs.TableId})
	err := rtEvent.Create(db)
	assert.NoError(t, err)
	err = rtTs.Create(db)
	assert.NoError(t, err)

	// 模拟dsrt
	dsrtEvent := resulttable.DataSourceResultTable{
		BkDataId: 990,
		TableId:  rtEvent.TableId,
	}
	dsrtTs := resulttable.DataSourceResultTable{
		BkDataId: 991,
		TableId:  rtTs.TableId,
	}
	db.Delete(&resulttable.DataSourceResultTable{}, "table_id in (?)", []string{dsrtEvent.TableId, dsrtTs.TableId})
	err = dsrtEvent.Create(db)
	assert.NoError(t, err)
	err = dsrtTs.Create(db)
	assert.NoError(t, err)

	// 模拟ds
	dsEvent := resulttable.DataSource{
		BkDataId:       dsrtEvent.BkDataId,
		DataName:       rtEvent.TableNameZh,
		EtlConfig:      models.ETLConfigTypeBkStandardV2TimeSeries,
		IsCustomSource: true,
		IsEnable:       true,
	}
	dsTs := resulttable.DataSource{
		BkDataId:       dsrtTs.BkDataId,
		DataName:       rtTs.TableId,
		EtlConfig:      models.ETLConfigTypeBkStandardV2TimeSeries,
		IsCustomSource: true,
		IsEnable:       true,
	}
	db.Delete(&resulttable.DataSource{}, "bk_data_id in (?)", []uint{dsEvent.BkDataId, dsTs.BkDataId})
	err = dsEvent.Create(db)
	assert.NoError(t, err)
	err = dsTs.Create(db)
	assert.NoError(t, err)

	// 模拟event
	eg := customreport.EventGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID: dsrtEvent.BkDataId,
			TableID:  dsrtEvent.TableId,
			IsEnable: true,
		},
		EventGroupName: dsrtEvent.TableId,
	}
	db.Delete(&eg, "table_id = ?", eg.TableID)
	err = eg.Create(db)

	//模拟ts
	ts := customreport.TimeSeriesGroup{
		CustomGroupBase: customreport.CustomGroupBase{
			BkDataID: dsrtTs.BkDataId,
			TableID:  dsrtTs.TableId,
			IsEnable: true,
		},
		TimeSeriesGroupName: dsrtTs.TableId,
	}
	db.Delete(&ts, "table_id = ?", ts.TableID)
	err = ts.Create(db)

	svc := NewSpaceDataSourceSvc(nil)
	bizDataIdsMap, bkDataIdList, err := svc.getRealZeroBizDataId()
	assert.NoError(t, err)
	assert.True(t, slicex.IsExistItem(bkDataIdList, dsrtEvent.BkDataId))
	assert.True(t, slicex.IsExistItem(bkDataIdList, dsrtTs.BkDataId))
	eventDataIds := bizDataIdsMap[111]
	assert.ElementsMatch(t, eventDataIds, []uint{eg.BkDataID})
	tsDataIds := bizDataIdsMap[112]
	assert.ElementsMatch(t, tsDataIds, []uint{ts.BkDataID})

}
