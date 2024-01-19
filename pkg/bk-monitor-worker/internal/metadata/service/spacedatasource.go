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
	"strconv"
	"strings"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mapx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// SpaceDataSourceSvc space datasource service
type SpaceDataSourceSvc struct {
	*space.SpaceDataSource
}

func NewSpaceDataSourceSvc(obj *space.SpaceDataSource) SpaceDataSourceSvc {
	return SpaceDataSourceSvc{
		SpaceDataSource: obj,
	}
}

// BulkCreateRecords 批量创建记录
func (SpaceDataSourceSvc) BulkCreateRecords(spaceType string, SpaceDataIdMap map[string][]uint, fromAuthorization bool) ([]string, error) {
	// NOTE: 当共享集群项目时，返回的数据中，共享集群即使这个项目下的专用集群，也是这个项目的共享集群, 因此，分开创建，避免相同的数据写入
	var changedSpaceIdList []string
	db := mysql.GetDBSession().DB
	for spaceId, dataids := range SpaceDataIdMap {
		var sdsList []space.SpaceDataSource
		if len(dataids) != 0 {
			if err := space.NewSpaceDataSourceQuerySet(db).Select(space.SpaceDataSourceDBSchema.BkDataId).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId).BkDataIdIn(dataids...).All(&sdsList); err != nil {
				return nil, errors.Wrapf(err, "query SpaceDataSource with space_type [%s] space_id [%s] data_id [%v] failed", spaceType, spaceId, dataids)
			}
		}
		dataidSet := mapset.NewSet[uint](dataids...)
		existDataidSet := mapset.NewSet[uint]()
		for _, sds := range sdsList {
			existDataidSet.Add(sds.BkDataId)
		}
		diffSet := dataidSet.Difference(existDataidSet)
		var changed bool
		it := diffSet.Iterator()
		for dataid := range it.C {
			sds := space.SpaceDataSource{
				SpaceTypeId:       spaceType,
				SpaceId:           spaceId,
				BkDataId:          dataid,
				FromAuthorization: fromAuthorization,
			}
			_ = metrics.MysqlCount(space.SpaceResource{}.TableName(), "BulkCreateRecords_create_SpaceDataSource", 1)
			if cfg.BypassSuffixPath != "" {
				logger.Infof("[db_diff] create SpaceDataSource space_type_id [%s] space_id [%s] bk_data_id [%v] from_authorization [%v]", spaceType, spaceId, dataid, fromAuthorization)
			} else {
				if err := sds.Create(db); err != nil {
					logger.Errorf("create SpaceDataSource with space_type_id [%s] space_id [%s] bk_data_id [%v] from_authorization [%v] failed, %v", spaceType, spaceId, dataid, fromAuthorization, err)
					continue
				}
			}
			changed = true
		}
		it.Stop()
		if changed {
			changedSpaceIdList = append(changedSpaceIdList, spaceId)
		}
	}
	return changedSpaceIdList, nil
}

func (s *SpaceDataSourceSvc) SyncBkccSpaceDataSource() error {
	bizDataIdsMap, err := s.getBizDataIds()
	if err != nil {
		return errors.Wrapf(err, "getBizDataIds failed")
	}
	// 针对 0 业务按照规则转换为所属业务
	// NOTE: 因为 0 业务的数据源不一定是 bkcc 类型，需要忽略掉更新
	realBizDataIds, zeroDataIdList, err := s.getRealZeroBizDataId()
	if err != nil {
		return errors.Wrapf(err, "getRealZeroBizDataId failed")
	}
	db := mysql.GetDBSession().DB
	_ = metrics.MysqlCount(resulttable.DataSource{}.TableName(), "SyncBkccSpaceDataSource_updateZeroDataId", float64(len(zeroDataIdList)))
	if cfg.BypassSuffixPath != "" {
		for _, chunkDataIds := range slicex.ChunkSlice(zeroDataIdList, 0) {
			logger.Infof("[db_diff] updated DataSource for [%v](exclude[%v]) with is_platform_data_id [true] space_type_id [%s]", chunkDataIds, models.SkipDataIdListForBkcc, models.SpaceTypeBKCC)
		}
	} else {
		for _, chunkDataIds := range slicex.ChunkSlice(zeroDataIdList, 0) {
			if err := resulttable.NewDataSourceQuerySet(db).BkDataIdNotIn(models.SkipDataIdListForBkcc...).BkDataIdIn(chunkDataIds...).GetUpdater().SetIsPlatformDataId(true).SetSpaceTypeId(models.SpaceTypeBKCC).Update(); err != nil {
				logger.Errorf("update DataSourc for [%v] with is_platform_data_id [true] space_type_id [%s] failed, %v", chunkDataIds, models.SpaceTypeBKCC, err)
				continue
			}
			logger.Infof("updated DataSource for [%v](exclude[%v]) with is_platform_data_id [true] space_type_id [%s]", chunkDataIds, models.SkipDataIdListForBkcc, models.SpaceTypeBKCC)
		}
	}

	// 过滤掉已经存在的data id
	var spdsList []space.SpaceDataSource
	if err := space.NewSpaceDataSourceQuerySet(db).Select(space.SpaceDataSourceDBSchema.SpaceId, space.SpaceDataSourceDBSchema.BkDataId).SpaceTypeIdEq(models.SpaceTypeBKCC).All(&spdsList); err != nil {
		return errors.Wrapf(err, "query SpaceDataSource with bkcc failed")
	}
	for _, spds := range spdsList {
		isExist := s.refineBizDataIdMap(bizDataIdsMap, spds.SpaceId, spds.BkDataId)
		if !isExist {
			s.refineBizDataIdMap(realBizDataIds, spds.SpaceId, spds.BkDataId)
		}
	}

	if err := s.CreateBkccSpaceDataSource(bizDataIdsMap); err != nil {
		return errors.Wrapf(err, "CreateBkccSpaceDataSource with bizDataIdsMap [%#v] failed", bizDataIdsMap)
	}

	if err := s.CreateBkccSpaceDataSource(realBizDataIds); err != nil {
		return errors.Wrapf(err, "CreateBkccSpaceDataSource with realBizDataIds [%#v] failed", realBizDataIds)
	}

	bizIdList := mapx.GetMapKeys(bizDataIdsMap)
	bizIdList = append(bizIdList, mapx.GetMapKeys(realBizDataIds)...)
	bizIdList = slicex.RemoveDuplicate(&bizIdList)
	// 组装数据，推送 redis 功能
	spaceRedisSvc := NewSpaceRedisSvc(0)
	for _, bizId := range bizIdList {
		if bizId != 0 {
			if err := spaceRedisSvc.PushAndPublishSpaceRouter(models.SpaceTypeBKCC, strconv.Itoa(bizId), nil); err != nil {
				logger.Errorf("PushAndPublishSpaceRouter for [%s__%v] failed", models.SpaceTypeBKCC, bizId)
				continue
			}
		}
	}
	return nil
}

func (s *SpaceDataSourceSvc) refineBizDataIdMap(dataIdMap map[int][]uint, spaceIdStr string, bkDataId uint) bool {
	var isExist bool
	spaceId, err := strconv.Atoi(spaceIdStr)
	if err != nil {
		logger.Errorf("refineBizDataIdMap int(spaceIdStr) failed, %v", err)
		return false
	}
	spaceDataIds := dataIdMap[spaceId]
	spaceDataIds = slicex.RemoveDuplicate(&spaceDataIds)
	if slicex.IsExistItem(spaceDataIds, bkDataId) {
		spaceDataIds = slicex.RemoveItem(spaceDataIds, bkDataId)
		isExist = true
	}
	if len(spaceDataIds) == 0 {
		delete(dataIdMap, spaceId)
	} else {
		dataIdMap[spaceId] = spaceDataIds
	}
	return isExist
}

// 获取业务对应的数据源关联关系
func (*SpaceDataSourceSvc) getBizDataIds() (map[int][]uint, error) {
	db := mysql.GetDBSession().DB
	// 同时排除掉小于等于 0 的数据
	var rtList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId, resulttable.ResultTableDBSchema.BkBizId).BkBizIdGt(0).All(&rtList); err != nil {
		return nil, errors.Wrapf(err, "query ResultTable failed")
	}
	var tableIds []string
	rtBizIdMap := make(map[string]int)
	for _, rt := range rtList {
		tableIds = append(tableIds, rt.TableId)
		rtBizIdMap[rt.TableId] = rt.BkBizId
	}
	rtDataIdMap := make(map[string]uint)
	for _, chunkTableIds := range slicex.ChunkSlice(tableIds, 0) {
		var tempList []resulttable.DataSourceResultTable
		if err := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.BkDataId, resulttable.DataSourceResultTableDBSchema.TableId).TableIdIn(chunkTableIds...).All(&tempList); err != nil {
			return nil, errors.Wrapf(err, "query DataSourceResultTable failed")
		}
		for _, dsrt := range tempList {
			rtDataIdMap[dsrt.TableId] = dsrt.BkDataId
		}
	}
	// 格式{biz_id: [data_id]}
	bizDataIdsMap := make(map[int][]uint)
	for rtName, biz := range rtBizIdMap {
		dataId, ok := rtDataIdMap[rtName]
		if !ok {
			logger.Warnf("result table [%s] not found data id", rtName)
			continue
		}
		if dataIds, ok := bizDataIdsMap[biz]; ok {
			bizDataIdsMap[biz] = append(dataIds, dataId)
		} else {
			bizDataIdsMap[biz] = []uint{dataId}
		}
	}
	return bizDataIdsMap, nil
}

// 获取数据源归属的业务
func (s *SpaceDataSourceSvc) getRealZeroBizDataId() (map[int][]uint, []uint, error) {
	/*
		1. 查询数据源对应的rt，如果bk_biz_id为0，则为所属业务
		2. 如果为0，则需要查询 tsgroup 或者 eventgroup, 并且设置为平台级ID
			- 如果在 tsgroup 中，则需要按照 data_name 以"_"拆分，取最后一个为业务ID
			- 如果在 eventgroup 中，则需要按照 data_name 以"_"拆分，去第一个为业务ID
	*/
	db := mysql.GetDBSession().DB
	// 获取业务ID为 0 的结果表
	var rtList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId).BkBizIdEq(0).All(&rtList); err != nil {
		return nil, nil, errors.Wrapf(err, "query ResultTable failed")
	}
	var tableIds []string
	for _, rt := range rtList {
		tableIds = append(tableIds, rt.TableId)
	}
	var dataIdList []uint
	for _, chunkTableIds := range slicex.ChunkSlice(tableIds, 0) {
		var tempList []resulttable.DataSourceResultTable
		if err := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.BkDataId).TableIdIn(chunkTableIds...).All(&tempList); err != nil {
			logger.Error("query DataSourceResultTable failed")
			continue
		}
		for _, dsrt := range tempList {
			dataIdList = append(dataIdList, dsrt.BkDataId)
		}
	}
	dataIdList = slicex.RemoveDuplicate(&dataIdList)
	var dsList []resulttable.DataSource
	var tsDataIdList []uint
	var eventDataIdList []uint
	for _, chunkDataIds := range slicex.ChunkSlice(dataIdList, 0) {
		// 通过 etl_conf 过滤指定的类型
		var dsTempList []resulttable.DataSource
		if err := resulttable.NewDataSourceQuerySet(db).Select(resulttable.DataSourceDBSchema.BkDataId, resulttable.DataSourceDBSchema.DataName, resulttable.DataSourceDBSchema.SpaceUid).EtlConfigIn(models.SpaceDataSourceETLList...).BkDataIdIn(chunkDataIds...).All(&dsTempList); err != nil {
			return nil, nil, errors.Wrapf(err, "query DataSourceResultTable failed")
		}
		dsList = append(dsList, dsTempList...)
		// 查询 tsgroup 是否有对应的数据源ID
		var tsTempList []customreport.TimeSeriesGroup
		if err := customreport.NewTimeSeriesGroupQuerySet(db).Select(customreport.TimeSeriesGroupDBSchema.BkDataID).BkDataIDIn(chunkDataIds...).All(&tsTempList); err != nil {
			return nil, nil, errors.Wrapf(err, "query TimeSeriesGroup failed")
		}
		for _, ts := range tsTempList {
			tsDataIdList = append(tsDataIdList, ts.BkDataID)
		}
		// 查询 eventgroup 是否有对应的数据源ID
		var eventTempList []customreport.EventGroup
		if err := customreport.NewEventGroupQuerySet(db).Select(customreport.EventGroupDBSchema.BkDataID).BkDataIDIn(chunkDataIds...).All(&eventTempList); err != nil {
			return nil, nil, errors.Wrapf(err, "query EventGroup failed")
		}
		for _, event := range eventTempList {
			eventDataIdList = append(eventDataIdList, event.BkDataID)
		}
	}
	bizDataIdsMap := make(map[int][]uint)
	// 如果在 ts group 中，则通过data name 拆分，获取biz_id
	for _, ds := range dsList {
		isInTsGroup := slicex.IsExistItem(tsDataIdList, ds.BkDataId)
		isInEventGroup := slicex.IsExistItem(eventDataIdList, ds.BkDataId)
		bizId := s.getRealBizId(ds.DataName, ds.SpaceUid, isInTsGroup, isInEventGroup)
		if dataIds, ok := bizDataIdsMap[bizId]; ok {
			bizDataIdsMap[bizId] = append(dataIds, ds.BkDataId)
		} else {
			bizDataIdsMap[bizId] = []uint{ds.BkDataId}
		}
	}
	return bizDataIdsMap, dataIdList, nil
}

func (*SpaceDataSourceSvc) getRealBizId(dataName, spaceUid string, isInTsGroup, isInEventGroup bool) int {
	var bizIdStr string
	if spaceUid != "" {
		// 如果 space_uid 有值，则直接取里面的 space_id 作为业务 id
		splits := strings.Split(spaceUid, "__")
		bizIdStr = splits[len(splits)-1]
	} else if isInTsGroup {
		// 如果在tsGroup中，获取拆分后的第一个值
		bizIdStr = strings.Split(dataName, "_")[0]
	} else if isInEventGroup {
		splits := strings.Split(dataName, "_")
		bizIdStr = splits[len(splits)-1]
	} else {
		bizIdStr = "0"
	}
	bizId, _ := strconv.Atoi(bizIdStr)
	return bizId
}

// CreateBkccSpaceDataSource 批量创建空间和数据源的关系
func (s *SpaceDataSourceSvc) CreateBkccSpaceDataSource(bizDataIdsMap map[int][]uint) error {
	db := mysql.GetDBSession().DB
	tx := db.Begin()
	var dataIdList []uint
	for bizId, dataIds := range bizDataIdsMap {
		// 忽略 0 业务的数据源，不创建
		if bizId == 0 {
			continue
		}
		dataIdList = append(dataIdList, dataIds...)
		for _, id := range slicex.RemoveDuplicate(&dataIds) {
			sd := space.SpaceDataSource{
				SpaceTypeId: models.SpaceTypeBKCC,
				SpaceId:     strconv.Itoa(bizId),
				BkDataId:    id,
			}
			_ = metrics.MysqlCount(space.SpaceDataSource{}.TableName(), "CreateBkccSpaceDataSource_create_ds", 1)
			if cfg.BypassSuffixPath != "" {
				logger.Infof("[db_diff] create SpaceDataSource space_type_id [%s] space_id [%s] bk_data_id [%v]", sd.SpaceTypeId, sd.SpaceId, sd.BkDataId)
			} else {
				if err := sd.Create(tx); err != nil {
					tx.Rollback()
					return errors.Wrapf(err, "create SpaceDataSource with space_type [%s] biz [%v] bk_data_id [%v] failed, rollback", models.SpaceTypeBKCC, bizId, id)
				}
			}
		}
	}
	// 设置数据源类型为 bkcc
	if len(dataIdList) != 0 {
		_ = metrics.MysqlCount(resulttable.DataSource{}.TableName(), "CreateBkccSpaceDataSource_update_ds", float64(len(dataIdList)))
		if cfg.BypassSuffixPath != "" {
			logger.Infof("[db_diff] updated DataSource with space_type [%s] for bk_data_id [%v]", models.SpaceTypeBKCC, dataIdList)
		} else {
			if err := resulttable.NewDataSourceQuerySet(tx).BkDataIdIn(dataIdList...).GetUpdater().SetSpaceTypeId(models.SpaceTypeBKCC).Update(); err != nil {
				tx.Rollback()
				return errors.Wrapf(err, "update DataSource with space_type [%s] for bk_data_id [%v] failed, rollback", models.SpaceTypeBKCC, dataIdList)
			}
			logger.Infof("updated DataSource with space_type [%s] for bk_data_id [%v]", models.SpaceTypeBKCC, dataIdList)
		}
	}
	tx.Commit()
	return nil
}
