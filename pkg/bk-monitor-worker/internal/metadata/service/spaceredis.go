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
	"strings"
	"sync"
	"time"

	mapset "github.com/deckarep/golang-set"
	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/bcs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/optionx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/stringx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// SpaceRedisSvc 空间Redis service
type SpaceRedisSvc struct {
	goroutineLimit int
}

func NewSpaceRedisSvc(goroutineLimit int) SpaceRedisSvc {
	if goroutineLimit <= 0 {
		goroutineLimit = 10
	}
	return SpaceRedisSvc{goroutineLimit: goroutineLimit}
}

func (s SpaceRedisSvc) PushAndPublishSpaceRouter(spaceType, spaceId string, tableIdList []string) error {
	logger.Infof("start to push and publish space_type [%s], space_id [%s] table_ids [%v] router", spaceType, spaceId, tableIdList)
	pusher := NewSpacePusher()
	// 获取空间下的结果表，如果不存在，则获取空间下的所有
	if len(tableIdList) == 0 {
		tableDataIdMap, err := pusher.GetSpaceTableIdDataId(spaceType, spaceId, nil, nil, nil)
		if err != nil {
			return errors.Wrap(err, "get space table id dataid failed")
		}
		for tableId := range tableDataIdMap {
			tableIdList = append(tableIdList, tableId)
		}
	}
	// 更新空间下的结果表相关数据
	db := mysql.GetDBSession().DB
	if spaceType != "" && spaceId != "" {
		// 更新相关数据到 redis
		if err := pusher.PushSpaceTableIds(spaceType, spaceId, true); err != nil {
			return err
		}
	} else {
		// NOTE: 现阶段仅针对 bkcc 类型做处理
		var spList []space.Space
		if err := space.NewSpaceQuerySet(db).SpaceTypeIdEq(models.SpaceTypeBKCC).Select(space.SpaceDBSchema.SpaceId).All(&spList); err != nil {
			return err
		}
		wg := &sync.WaitGroup{}
		ch := make(chan bool, s.goroutineLimit)
		wg.Add(len(spList))
		for _, sp := range spList {
			ch <- true
			go func(sp space.Space, wg *sync.WaitGroup, ch chan bool) {
				defer func() {
					<-ch
					wg.Done()
				}()
				if err := pusher.PushSpaceTableIds(models.SpaceTypeBKCC, sp.SpaceId, false); err != nil {
					logger.Errorf("push space [%s__%s] to redis error, %v", models.SpaceTypeBKCC, sp.SpaceId, err)
				} else {
					logger.Infof("push space [%s__%s] to redis success", models.SpaceTypeBKCC, sp.SpaceId)
				}
				return
			}(sp, wg, ch)
		}
		wg.Wait()
	}
	// 更新数据
	if err := pusher.PushDataLabelTableIds(nil, tableIdList, true); err != nil {
		return err
	}
	if err := pusher.PushTableIdDetail(tableIdList, true); err != nil {
		return err
	}
	logger.Infof("push and publish space_type: %s, space_id: %s router successfully", spaceType, spaceId)
	return nil
}

type SpacePusher struct{}

func NewSpacePusher() *SpacePusher {
	return &SpacePusher{}
}

// GetSpaceTableIdDataId 获取空间下的结果表和数据源信息
func (s SpacePusher) GetSpaceTableIdDataId(spaceType, spaceId string, tableIdList []string, excludeDataIdList []uint, options *optionx.Options) (map[string]uint, error) {
	if options == nil {
		options = optionx.NewOptions(nil)
	}
	options.SetDefault("includePlatformDataId", true)
	db := mysql.GetDBSession().DB
	if len(tableIdList) != 0 {
		var dsrtList []resulttable.DataSourceResultTable
		for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
			var tempList []resulttable.DataSourceResultTable
			qs := resulttable.NewDataSourceResultTableQuerySet(db).TableIdIn(chunkTableIdList...)
			if len(excludeDataIdList) != 0 {
				qs = qs.BkDataIdNotIn(excludeDataIdList...)
			}
			if err := qs.All(&tempList); err != nil {
				return nil, err
			}
			dsrtList = append(dsrtList, tempList...)
		}
		dataMap := make(map[string]uint)
		for _, dsrt := range dsrtList {
			dataMap[dsrt.TableId] = dsrt.BkDataId
		}
		return dataMap, nil
	}
	// 否则，查询空间下的所有数据源，再过滤对应的结果表
	var spdsList []space.SpaceDataSource
	qs := space.NewSpaceDataSourceQuerySet(db).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId)
	// 获取是否授权数据
	if fromAuthorization, ok := options.GetBool("fromAuthorization"); ok {
		qs = qs.FromAuthorizationEq(fromAuthorization)
	}
	if err := qs.All(&spdsList); err != nil {
		return nil, err
	}
	dataIdSet := mapset.NewSet()
	for _, spds := range spdsList {
		dataIdSet.Add(spds.BkDataId)
	}
	// 过滤包含全局空间级的数据源
	if includePlatformDataId, _ := options.GetBool("includePlatformDataId"); includePlatformDataId {
		dataIds, err := s.getPlatformDataIds(spaceType)
		if err != nil {
			return nil, err
		}
		dataIdSet = dataIdSet.Union(slicex.UintList2Set(dataIds))
	}

	// 排除元素
	if len(excludeDataIdList) != 0 {
		dataIdSet = dataIdSet.Difference(slicex.UintList2Set(excludeDataIdList))
	}
	dataIdList := slicex.UintSet2List(dataIdSet)
	if len(dataIdList) == 0 {
		return map[string]uint{}, nil
	}
	dataMap := make(map[string]uint)
	var dsrtList []resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(db).BkDataIdIn(dataIdList...).All(&dsrtList); err != nil {
		return nil, err
	}
	for _, dsrt := range dsrtList {
		dataMap[dsrt.TableId] = dsrt.BkDataId
	}
	return dataMap, nil
}

// PushDataLabelTableIds 推送 data_label 及对应的结果表
func (s SpacePusher) PushDataLabelTableIds(dataLabelList, tableIdList []string, isPublish bool) error {
	logger.Infof("start to push data_label table_id data, data_label_list [%v], table_id_list [%v]", dataLabelList, tableIdList)
	tableIds, err := s.refineTableIds(tableIdList)
	if err != nil {
		return err
	}
	db := mysql.GetDBSession().DB
	// 过滤掉结果表数据标签为空或者为 None 的记录
	var rtList []resulttable.ResultTable
	if len(tableIds) != 0 {
		for _, chunkTableIds := range slicex.ChunkSlice(tableIds, 0) {
			var tempList []resulttable.ResultTable
			qs := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.DataLabel, resulttable.ResultTableDBSchema.TableId).TableIdIn(chunkTableIds...).DataLabelNe("").DataLabelIsNotNull()
			if len(dataLabelList) != 0 {
				qs = qs.DataLabelIn(dataLabelList...)
			}
			if err := qs.All(&tempList); err != nil {
				return err
			}
			rtList = append(rtList, tempList...)
		}

	}
	dlRtsMap := make(map[string][]string)
	for _, rt := range rtList {
		if rts, ok := dlRtsMap[*rt.DataLabel]; ok {
			dlRtsMap[*rt.DataLabel] = append(rts, rt.TableId)
		} else {
			dlRtsMap[*rt.DataLabel] = []string{rt.TableId}
		}

	}
	if len(dlRtsMap) != 0 {
		client := redis.GetInstance()
		for dl, rts := range dlRtsMap {
			rtsStr, err := jsonx.MarshalString(rts)
			if err != nil {
				return err
			}
			if err := client.HSet(cfg.DataLabelToResultTableKey, dl, rtsStr); err != nil {
				return err
			}
			if isPublish {
				if err := client.Publish(cfg.DataLabelToResultTableChannel, dl); err != nil {
					return err
				}
			}
		}
	}
	logger.Infof("push redis data_label_to_result_table")
	return nil
}

// 提取写入到influxdb或vm的结果表数据
func (s SpacePusher) refineTableIds(tableIdList []string) ([]string, error) {
	db := mysql.GetDBSession().DB
	// 过滤写入 influxdb 的结果表
	var influxdbStorageList []storage.InfluxdbStorage
	qs := storage.NewInfluxdbStorageQuerySet(db).Select(storage.InfluxdbStorageDBSchema.TableID)
	if len(tableIdList) != 0 {
		for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
			var tempList []storage.InfluxdbStorage

			qsTemp := qs.TableIDIn(chunkTableIdList...)
			if err := qsTemp.All(&tempList); err != nil {
				return nil, err
			}
			influxdbStorageList = append(influxdbStorageList, tempList...)
		}
	} else {
		if err := qs.All(&influxdbStorageList); err != nil {
			return nil, err
		}
	}

	// 过滤写入 vm 的结果表
	var vmRecordList []storage.AccessVMRecord
	qs2 := storage.NewAccessVMRecordQuerySet(db).Select(storage.AccessVMRecordDBSchema.ResultTableId)
	if len(tableIdList) != 0 {
		for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
			var tempList []storage.AccessVMRecord
			qsTemp := qs2.ResultTableIdIn(chunkTableIdList...)
			if err := qsTemp.All(&tempList); err != nil {
				return nil, err
			}
			vmRecordList = append(vmRecordList, tempList...)
		}
	} else {
		if err := qs2.All(&vmRecordList); err != nil {
			return nil, err
		}
	}

	var tableIds []string
	for _, i := range influxdbStorageList {
		tableIds = append(tableIds, i.TableID)
	}
	for _, i := range vmRecordList {
		tableIds = append(tableIds, i.ResultTableId)
	}
	tableIds = slicex.RemoveDuplicate(&tableIds)
	return tableIds, nil
}

// PushTableIdDetail 推送结果表的详细信息
func (s SpacePusher) PushTableIdDetail(tableIdList []string, isPublish bool) error {
	logger.Infof("start to push table_id detail data, table_id_list [%v]", tableIdList)
	tableIdDetail, err := s.getTableInfoForInfluxdbAndVm(tableIdList)
	if err != nil {
		return err
	}
	if len(tableIdDetail) == 0 {
		logger.Infof("not found table from influxdb or vm")
		return nil
	}
	var tableIds []string
	for tableId := range tableIdDetail {
		tableIds = append(tableIds, tableId)
	}
	db := mysql.GetDBSession().DB
	// 获取结果表类型
	var rtList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId, resulttable.ResultTableDBSchema.SchemaType, resulttable.ResultTableDBSchema.DataLabel).TableIdIn(tableIds...).All(&rtList); err != nil {
		return err
	}
	tableIdRtMap := make(map[string]resulttable.ResultTable)
	for _, rt := range rtList {
		tableIdRtMap[rt.TableId] = rt
	}

	var dsrtList []resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.TableId, resulttable.DataSourceResultTableDBSchema.BkDataId).TableIdIn(tableIds...).All(&dsrtList); err != nil {
		return err
	}
	tableIdDataIdMap := make(map[string]uint)
	for _, dsrt := range dsrtList {
		tableIdDataIdMap[dsrt.TableId] = dsrt.BkDataId
	}

	// 获取结果表对应的类型
	measurementTypeMap, err := s.getMeasurementTypeByTableId(tableIds, rtList, tableIdDataIdMap)
	if err != nil {
		return err
	}
	// 再追加上结果表的指标数据、集群 ID、类型
	tableIdClusterIdMap, err := s.getTableIdClusterId(tableIds)
	if err != nil {
		return err
	}
	tableIdFields, err := s.composeTableIdFields(tableIds)
	if err != nil {
		return err
	}

	client := redis.GetInstance()

	for tableId, detail := range tableIdDetail {
		var ok bool
		// fields
		detail["fields"], ok = tableIdFields[tableId]
		if !ok {
			detail["fields"] = []string{}
		}
		// data_label
		rt, ok := tableIdRtMap[tableId]
		if !ok {
			detail["data_label"] = ""
		} else {
			detail["data_label"] = rt.DataLabel
		}
		detail["measurement_type"] = measurementTypeMap[tableId]
		detail["bcs_cluster_id"] = tableIdClusterIdMap[tableId]
		detail["bk_data_id"] = tableIdDataIdMap[tableId]
		detailStr, err := jsonx.MarshalString(detail)
		if err != nil {
			return err
		}
		// 推送数据
		if err := client.HSet(cfg.ResultTableDetailKey, tableId, detailStr); err != nil {
			return err
		}
		if isPublish {
			if err := client.Publish(cfg.ResultTableDetailChannel, tableId); err != nil {
				return err
			}
		}
	}
	logger.Info("push redis result_table_detail")
	return nil

}

type InfluxdbTableData struct {
	InfluxdbProxyStorageId uint     `json:"influxdb_proxy_storage_id"`
	Database               string   `json:"database"`
	RealTableName          string   `json:"real_table_name"`
	TagsKey                []string `json:"tags_key"`
}

// 获取influxdb 和 vm的结果表
func (s SpacePusher) getTableInfoForInfluxdbAndVm(tableIdList []string) (map[string]map[string]interface{}, error) {
	logger.Infof("start to push table_id detail data, table_id_list [%v]", tableIdList)
	db := mysql.GetDBSession().DB

	var influxdbStorageList []storage.InfluxdbStorage
	if len(tableIdList) != 0 {
		// 如果结果表存在，则过滤指定的结果表
		for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
			var tempList []storage.InfluxdbStorage
			if err := storage.NewInfluxdbStorageQuerySet(db).TableIDIn(chunkTableIdList...).All(&tempList); err != nil {
				return nil, err
			}
			influxdbStorageList = append(influxdbStorageList, tempList...)
		}
	} else {
		if err := storage.NewInfluxdbStorageQuerySet(db).All(&influxdbStorageList); err != nil {
			return nil, err
		}
	}

	influxdbTableMap := make(map[string]InfluxdbTableData)
	for _, i := range influxdbStorageList {
		tagsKey := make([]string, 0)
		if i.PartitionTag != "" {
			tagsKey = strings.Split(i.PartitionTag, ",")
		}
		influxdbTableMap[i.TableID] = InfluxdbTableData{
			InfluxdbProxyStorageId: i.InfluxdbProxyStorageId,
			Database:               i.Database,
			RealTableName:          i.RealTableName,
			TagsKey:                tagsKey,
		}
	}
	// 获取vm集群名信息
	var vmCLusterList []storage.ClusterInfo
	if err := storage.NewClusterInfoQuerySet(db).Select(storage.ClusterInfoDBSchema.ClusterID, storage.ClusterInfoDBSchema.ClusterName).ClusterTypeEq(models.StorageTypeVM).All(&vmCLusterList); err != nil {
		return nil, err
	}
	vmClusterIdNameMap := make(map[uint]string)
	for _, c := range vmCLusterList {
		vmClusterIdNameMap[c.ClusterID] = c.ClusterName
	}

	var vmRecordList []storage.AccessVMRecord
	if len(tableIdList) != 0 {
		// 如果结果表存在，则过滤指定的结果表
		for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
			var tempList []storage.AccessVMRecord
			if err := storage.NewAccessVMRecordQuerySet(db).Select(storage.AccessVMRecordDBSchema.ResultTableId, storage.AccessVMRecordDBSchema.VmClusterId, storage.AccessVMRecordDBSchema.VmResultTableId).ResultTableIdIn(chunkTableIdList...).All(&tempList); err != nil {
				return nil, err
			}
			vmRecordList = append(vmRecordList, tempList...)
		}
	} else {
		if err := storage.NewAccessVMRecordQuerySet(db).Select(storage.AccessVMRecordDBSchema.ResultTableId, storage.AccessVMRecordDBSchema.VmClusterId, storage.AccessVMRecordDBSchema.VmResultTableId).All(&vmRecordList); err != nil {
			return nil, err
		}
	}
	vmTableMap := make(map[string]map[string]interface{})
	for _, record := range vmRecordList {
		vmTableMap[record.ResultTableId] = map[string]interface{}{"vm_rt": record.VmResultTableId, "storage_name": vmClusterIdNameMap[record.VmClusterId]}
	}

	// 获取proxy关联的集群信息
	var influxdbProxyStorageList []storage.InfluxdbProxyStorage
	if err := storage.NewInfluxdbProxyStorageQuerySet(db).Select(storage.InfluxdbProxyStorageDBSchema.ID, storage.InfluxdbProxyStorageDBSchema.ProxyClusterId, storage.InfluxdbProxyStorageDBSchema.InstanceClusterName).All(&influxdbProxyStorageList); err != nil {
		return nil, err
	}
	storageClusterMap := make(map[uint]storage.InfluxdbProxyStorage)
	for _, p := range influxdbProxyStorageList {
		storageClusterMap[p.ID] = p
	}

	tableIdInfo := make(map[string]map[string]interface{})

	for tableId, detail := range influxdbTableMap {
		storageCluster := storageClusterMap[detail.InfluxdbProxyStorageId]

		tableIdInfo[tableId] = map[string]interface{}{
			"storage_id":   storageCluster.ProxyClusterId,
			"storage_name": "",
			"cluster_name": storageCluster.InstanceClusterName,
			"db":           detail.Database,
			"measurement":  detail.RealTableName,
			"vm_rt":        "",
			"tags_key":     detail.TagsKey,
		}
	}
	// 处理 vm 的数据信息
	for tableId, detail := range vmTableMap {
		if _, ok := tableIdInfo[tableId]; ok {
			tableIdInfo[tableId]["vm_rt"] = detail["vm_rt"]
			tableIdInfo[tableId]["storage_name"] = detail["storage_name"]
		} else {
			detail["cluster_name"] = ""
			detail["db"] = ""
			detail["measurement"] = ""
			detail["tags_key"] = []string{}
			tableIdInfo[tableId] = detail
		}
	}
	return tableIdInfo, nil

}

// 通过结果表Id, 获取对应的 option 配置, 通过 option 转到到 measurement 类型
func (s SpacePusher) getMeasurementTypeByTableId(tableIdList []string, tableList []resulttable.ResultTable, tableDataIdMap map[string]uint) (map[string]string, error) {
	if len(tableIdList) == 0 {
		return make(map[string]string), nil
	}
	db := mysql.GetDBSession().DB
	// 过滤对应关系，用以进行判断单指标单表、多指标单表
	var rtoList []resulttable.ResultTableOption
	for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
		var tempList []resulttable.ResultTableOption
		if err := resulttable.NewResultTableOptionQuerySet(db).Select(resulttable.ResultTableOptionDBSchema.TableID, resulttable.ResultTableOptionDBSchema.Value).TableIDIn(chunkTableIdList...).NameEq(models.OptionIsSplitMeasurement).All(&tempList); err != nil {
			return nil, err
		}
		rtoList = append(rtoList, tempList...)
	}

	rtoMap := make(map[string]bool)
	for _, rto := range rtoList {
		var value bool
		if err := jsonx.UnmarshalString(rto.Value, &value); err != nil {
			return nil, err
		}
		rtoMap[rto.TableID] = value
	}

	var bkDataIdList []uint
	for _, bkDataId := range tableDataIdMap {
		bkDataIdList = append(bkDataIdList, bkDataId)
	}
	bkDataIdList = slicex.RemoveDuplicate(&bkDataIdList)
	// 过滤数据源对应的 etl_config
	dataIdEtlMap := make(map[uint]string)
	var dsList []resulttable.DataSource
	if len(bkDataIdList) != 0 {
		if err := resulttable.NewDataSourceQuerySet(db).Select(resulttable.DataSourceDBSchema.BkDataId, resulttable.DataSourceDBSchema.EtlConfig).BkDataIdIn(bkDataIdList...).All(&dsList); err != nil {
			return nil, err
		}
	}
	for _, ds := range dsList {
		dataIdEtlMap[ds.BkDataId] = ds.EtlConfig
	}

	// 获取到对应的类型
	measurementTypeMap := make(map[string]string)
	tableIdCutterMap, err := NewResultTableSvc(nil).GetTableIdCutter(tableIdList)
	if err != nil {
		return nil, err
	}
	for _, table := range tableList {
		bkDataId := tableDataIdMap[table.TableId]
		etlConfig := dataIdEtlMap[bkDataId]
		// 获取是否禁用指标切分模式
		isDisableMetricCutter := tableIdCutterMap[table.TableId]
		measurementTypeMap[table.TableId] = s.getMeasurementType(table.SchemaType, rtoMap[table.TableId], isDisableMetricCutter, etlConfig)
	}
	return measurementTypeMap, nil
}

// 获取表类型
func (s SpacePusher) getMeasurementType(schemaType string, isSplitMeasurement, isDisableMetricCutter bool, etlConfig string) string {
	// - 当 schema_type 为 fixed 时，为多指标单表
	if schemaType == models.ResultTableSchemaTypeFixed {
		return models.MeasurementTypeBkTraditional
	}
	// - 当 schema_type 为 free 时，
	if schemaType == models.ResultTableSchemaTypeFree {
		// - 如果 is_split_measurement 为 True, 则为单指标单表
		if isSplitMeasurement {
			return models.MeasurementTypeBkSplit
		}
		// - is_split_measurement 为 False
		// - 如果etl_config 不为`bk_standard_v2_time_series`
		if etlConfig != models.ETLConfigTypeBkStandardV2TimeSeries {
			return models.MeasurementTypeBkExporter
		}
		// - etl_config 为`bk_standard_v2_time_series`，
		// - 如果 is_disable_metric_cutter 为 False，则为固定 metric_name，metric_value
		if !isDisableMetricCutter {
			return models.MeasurementTypeBkExporter
		}
		// - 否则为自定义多指标单表
		return models.MeasurementTypeBkStandardV2TimeSeries

	}
	return models.MeasurementTypeBkTraditional
}

// 组装结果表对应的指标数据
func (s SpacePusher) composeTableIdFields(tableIds []string) (map[string][]string, error) {
	if len(tableIds) == 0 {
		return make(map[string][]string), nil
	}
	db := mysql.GetDBSession().DB
	// 过滤到对应的结果表字段
	var rtfList []resulttable.ResultTableField
	if err := resulttable.NewResultTableFieldQuerySet(db).Select(resulttable.ResultTableFieldDBSchema.TableID, resulttable.ResultTableFieldDBSchema.FieldName).TagEq(models.ResultTableFieldTagMetric).TableIDIn(tableIds...).All(&rtfList); err != nil {
		return nil, err
	}
	tableIdFieldMap := make(map[string][]string)
	for _, field := range rtfList {
		if fieldList, ok := tableIdFieldMap[field.TableID]; ok {
			tableIdFieldMap[field.TableID] = append(fieldList, field.FieldName)
		} else {
			tableIdFieldMap[field.TableID] = []string{field.FieldName}
		}
	}
	// 根据 option 过滤是否有开启黑名单，如果开启黑名单，则指标会有过期时间
	var rtoList []resulttable.ResultTableOption
	if err := resulttable.NewResultTableOptionQuerySet(db).Select(resulttable.ResultTableOptionDBSchema.TableID).TableIDIn(tableIds...).NameEq(models.OptionEnableFieldBlackList).ValueEq("false").All(&rtoList); err != nil {
		return nil, err
	}
	var whiteTableIdList []string
	for _, o := range rtoList {
		whiteTableIdList = append(whiteTableIdList, o.TableID)
	}
	whiteTableIdList = slicex.RemoveDuplicate(&whiteTableIdList)
	// 剩余的结果表，需要判断是否时序的，然后根据过期时间过滤数据

	logger.Infof("white table_id list: %v", whiteTableIdList)

	tableIdList := slicex.StringSet2List(slicex.StringList2Set(tableIds).Difference(slicex.StringList2Set(whiteTableIdList)))
	if len(tableIdList) == 0 {
		return make(map[string][]string), nil
	}

	tsInfo, err := s.filterTsInfo(tableIdList)
	if err != nil {
		return nil, err
	}
	// 组装结果表对应的指标数据
	tableIdMetrics := make(map[string][]string)
	var existTableIdList []string
	for tableId, groupId := range tsInfo.TableIdTsGroupIdMap {
		if metrics, ok := tsInfo.GroupIdFieldsMap[groupId]; ok {
			tableIdMetrics[tableId] = metrics
		} else {
			tableIdMetrics[tableId] = []string{}
		}
		existTableIdList = append(existTableIdList, tableId)
	}
	// 处理非时序结果表指标
	for tableId, fieldList := range tableIdFieldMap {
		if !stringx.StringInSlice(tableId, existTableIdList) {
			tableIdMetrics[tableId] = fieldList
		}
	}

	return tableIdMetrics, nil
}

type TsInfo struct {
	TableIdTsGroupIdMap map[string]uint
	GroupIdFieldsMap    map[uint][]string
}

// 根据结果表获取对应的时序数据
func (s SpacePusher) filterTsInfo(tableIds []string) (*TsInfo, error) {
	if len(tableIds) == 0 {
		return nil, nil
	}
	db := mysql.GetDBSession().DB
	var tsGroupList []customreport.TimeSeriesGroup
	if err := customreport.NewTimeSeriesGroupQuerySet(db).TableIDIn(tableIds...).All(&tsGroupList); err != nil {
		return nil, err
	}
	if len(tsGroupList) == 0 {
		return nil, nil
	}

	var tsGroupIdList []uint
	TableIdTsGroupIdMap := make(map[string]uint)
	var tsGroupTableId []string
	for _, group := range tsGroupList {
		tsGroupIdList = append(tsGroupIdList, group.TimeSeriesGroupID)
		TableIdTsGroupIdMap[group.TableID] = group.TimeSeriesGroupID
		tsGroupTableId = append(tsGroupTableId, group.TableID)
	}

	// NOTE: 针对自定义时序，过滤掉历史废弃的指标，时间在`TIME_SERIES_METRIC_EXPIRED_SECONDS`的为有效数据
	// 其它类型直接获取所有指标和维度
	beginTime := time.Now().UTC().Add(-time.Duration(cfg.GlobalTimeSeriesMetricExpiredSeconds) * time.Second)
	var tsmList []customreport.TimeSeriesMetric
	if len(tsGroupIdList) != 0 {
		if err := customreport.NewTimeSeriesMetricQuerySet(db).Select(customreport.TimeSeriesMetricDBSchema.FieldName, customreport.TimeSeriesMetricDBSchema.GroupID).GroupIDIn(tsGroupIdList...).LastModifyTimeGte(beginTime).All(&tsmList); err != nil {
			return nil, err
		}
	}
	groupIdFieldsMap := make(map[uint][]string)
	for _, metric := range tsmList {
		if fieldList, ok := groupIdFieldsMap[metric.GroupID]; ok {
			groupIdFieldsMap[metric.GroupID] = append(fieldList, metric.FieldName)
		} else {
			groupIdFieldsMap[metric.GroupID] = []string{metric.FieldName}
		}
	}

	return &TsInfo{
		TableIdTsGroupIdMap: TableIdTsGroupIdMap,
		GroupIdFieldsMap:    groupIdFieldsMap,
	}, nil

}

// 获取结果表对应的集群 ID
func (s SpacePusher) getTableIdClusterId(tableIds []string) (map[string]string, error) {
	if len(tableIds) == 0 {
		return make(map[string]string), nil
	}
	db := mysql.GetDBSession().DB
	var dsrtList []resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.BkDataId, resulttable.DataSourceResultTableDBSchema.TableId).TableIdIn(tableIds...).All(&dsrtList); err != nil {
		return nil, err
	}
	if len(dsrtList) == 0 {
		return make(map[string]string), nil
	}
	var dataIds []uint
	for _, dsrt := range dsrtList {
		dataIds = append(dataIds, dsrt.BkDataId)
	}
	// 过滤到集群的数据源，仅包含两类，集群内置和集群自定义
	qs := bcs.NewBCSClusterInfoQuerySet(db).StatusEq(models.BcsClusterStatusRunning)
	dataIds = slicex.RemoveDuplicate(&dataIds)
	var clusterListA []bcs.BCSClusterInfo
	if err := qs.Select(bcs.BCSClusterInfoDBSchema.K8sMetricDataID, bcs.BCSClusterInfoDBSchema.ClusterID).K8sMetricDataIDIn(dataIds...).All(&clusterListA); err != nil {
		return nil, err
	}

	var clusterListB []bcs.BCSClusterInfo
	if err := qs.Select(bcs.BCSClusterInfoDBSchema.CustomMetricDataID, bcs.BCSClusterInfoDBSchema.ClusterID).CustomMetricDataIDIn(dataIds...).All(&clusterListB); err != nil {
		return nil, err
	}

	dataIdClusterIdMap := make(map[uint]string)
	for _, c := range clusterListA {
		dataIdClusterIdMap[c.K8sMetricDataID] = c.ClusterID
	}
	for _, c := range clusterListB {
		dataIdClusterIdMap[c.CustomMetricDataID] = c.ClusterID
	}
	// 组装结果表到集群的信息
	tableIdClusterIdMap := make(map[string]string)
	for _, dsrt := range dsrtList {
		tableIdClusterIdMap[dsrt.TableId] = dataIdClusterIdMap[dsrt.BkDataId]
	}
	return tableIdClusterIdMap, nil
}

// PushSpaceTableIds 推送空间及对应的结果表和过滤条件
func (s SpacePusher) PushSpaceTableIds(spaceType, spaceId string, isPublish bool) error {
	logger.Infof("start to push space table_id data, space_type [%s], space_id [%s]", spaceType, spaceId)
	if spaceType == models.SpaceTypeBKCC {
		if err := s.pushBkccSpaceTableIds(spaceType, spaceId, nil); err != nil {
			return err
		}
	} else if spaceType == models.SpaceTypeBKCI {
		// 开启容器服务，则需要处理集群+业务+构建机+其它(在当前空间下创建的插件、自定义上报等)
		if err := s.pushBkciSpaceTableIds(spaceType, spaceId); err != nil {
			return err
		}

	} else if spaceType == models.SpaceTypeBKSAAS {
		if err := s.pushBksaasSpaceTableIds(spaceType, spaceId, nil); err != nil {
			return err
		}

	}
	// 如果指定要更新，则通知
	if isPublish {
		client := redis.GetInstance()
		if err := client.Publish(cfg.SpaceToResultTableChannel, fmt.Sprintf("%s__%s", spaceType, spaceId)); err != nil {
			return err
		}
	}
	logger.Infof("push space table_id data successfully, space_type [%s], space_id [%s]", spaceType, spaceId)

	return nil

}

// 推送 bkcc 类型空间数据
func (s SpacePusher) pushBkccSpaceTableIds(spaceType, spaceId string, options *optionx.Options) error {
	if options == nil {
		options = optionx.NewOptions(nil)
	}
	logger.Infof("start to push bkcc space table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	values, err := s.composeData(spaceType, spaceId, nil, nil, options)
	if err != nil {
		return err
	}
	if len(values) != 0 {
		client := redis.GetInstance()
		redisKey := fmt.Sprintf("%s__%s", spaceType, spaceId)
		valuesStr, err := jsonx.MarshalString(values)
		if err != nil {
			return errors.Wrapf(err, "push bkcc space [%s] marshal valued [%v] failed", redisKey, values)
		}
		if err := client.HSet(cfg.SpaceToResultTableKey, redisKey, valuesStr); err != nil {
			return errors.Wrapf(err, "push bkcc space [%s] value [%v] failed", redisKey, valuesStr)

		}
	}
	logger.Infof("push redis space_to_result_table, space_type [%s], space_id [%s] success", spaceType, spaceId)
	return nil
}

// 推送 bcs 类型空间下的关联业务的数据
func (s SpacePusher) pushBkciSpaceTableIds(spaceType, spaceId string) error {
	logger.Infof("start to push biz of bcs space table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	values, err := s.composeBcsSpaceBizTableIds(spaceType, spaceId)
	if err != nil {
		return err
	}
	bcsValues, err := s.composeBcsSpaceClusterTableIds(spaceType, spaceId)
	for tid, value := range bcsValues {
		values[tid] = value
	}
	bkciLevelValues, err := s.composeBkciLevelTableIds(spaceType, spaceId)
	for tid, value := range bkciLevelValues {
		values[tid] = value
	}
	bkciOtherValues, err := s.composeBkciOtherTableIds(spaceType, spaceId)
	for tid, value := range bkciOtherValues {
		values[tid] = value
	}
	bkciCrossValues, err := s.composeBkciCrossTableIds(spaceType, spaceId)
	for tid, value := range bkciCrossValues {
		values[tid] = value
	}
	// 推送数据
	if len(values) != 0 {
		client := redis.GetInstance()
		redisKey := fmt.Sprintf("%s__%s", spaceType, spaceId)
		valuesStr, err := jsonx.MarshalString(values)
		if err != nil {
			return errors.Wrapf(err, "push bkci space [%s] marshal valued [%v] failed", redisKey, values)
		}
		if err := client.HSet(cfg.SpaceToResultTableKey, redisKey, valuesStr); err != nil {
			return errors.Wrapf(err, "push bkci space [%s] value [%v] failed", redisKey, valuesStr)

		}
	}
	logger.Infof("push redis space_to_result_table, space_type [%s], space_id [%s] success", spaceType, spaceId)
	return nil
}

// 推送 bksaas 类型空间下的数据
func (s SpacePusher) pushBksaasSpaceTableIds(spaceType, spaceId string, tableIdList []string) error {
	logger.Infof("start to push bksaas space table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	values, err := s.composeBksaasSpaceClusterTableIds(spaceType, spaceId, tableIdList)
	if err != nil {
		return err
	}
	bksaasOtherValues, err := s.composeBksaasOtherTableIds(spaceType, spaceId, tableIdList)
	for tid, value := range bksaasOtherValues {
		values[tid] = value
	}
	// 推送数据
	if len(values) != 0 {
		client := redis.GetInstance()
		redisKey := fmt.Sprintf("%s__%s", spaceType, spaceId)
		valuesStr, err := jsonx.MarshalString(values)
		if err != nil {
			return errors.Wrapf(err, "push bksaas space [%s] marshal valued [%v] failed", redisKey, values)
		}
		if err := client.HSet(cfg.SpaceToResultTableKey, redisKey, valuesStr); err != nil {
			return errors.Wrapf(err, "push bksaas space [%s] value [%v] failed", redisKey, valuesStr)

		}
	}
	logger.Infof("push redis space_to_result_table, space_type [%s], space_id [%s]", spaceType, spaceId)
	return nil
}

// 获取平台级 data id
func (SpacePusher) getPlatformDataIds(spaceType string) ([]uint, error) {
	// 获取平台级的数据源
	// 仅针对当前空间类型，比如 bkcc，特殊的是 all 类型
	db := mysql.GetDBSession().DB
	var bkDataIdList []uint
	var dsList []resulttable.DataSource
	qs := resulttable.NewDataSourceQuerySet(db).Select(resulttable.DataSourceDBSchema.BkDataId, resulttable.DataSourceDBSchema.SpaceTypeId).IsPlatformDataIdEq(true)
	// 针对 bkcc 类型，这要是插件，不属于某个业务空间，也没有传递空间类型，因此，需要包含 all 类型
	if spaceType != "" && spaceType != models.SpaceTypeBKCC {
		qs = qs.SpaceTypeIdEq(spaceType)
	}
	if err := qs.All(&dsList); err != nil {
		return nil, err
	}
	for _, ds := range dsList {
		bkDataIdList = append(bkDataIdList, ds.BkDataId)
	}
	return bkDataIdList, nil
}

type DataIdDetail struct {
	EtlConfig        string `json:"etl_config"`
	SpaceUid         string `json:"space_uid"`
	IsPlatformDataId bool   `json:"is_platform_data_id"`
}

func (s SpacePusher) composeData(spaceType, spaceId string, tableIdList []string, defaultFilters []map[string]interface{}, options *optionx.Options) (map[string]map[string]interface{}, error) {
	if options == nil {
		options = optionx.NewOptions(nil)
	}
	options.SetDefault("includePlatformDataId", true)

	includePlatformDataId, _ := options.GetBool("includePlatformDataId")
	// 过滤到对应的结果表
	ops := optionx.NewOptions(map[string]interface{}{"includePlatformDataId": includePlatformDataId})
	if need, ok := options.GetBool("fromAuthorization"); ok {
		ops.Set("fromAuthorization", need)
	}
	tableIdDataId, err := s.GetSpaceTableIdDataId(spaceType, spaceId, tableIdList, nil, ops)
	if err != nil {
		return nil, err
	}
	valueData := make(map[string]map[string]interface{})
	// 如果为空，返回默认值
	if len(tableIdDataId) == 0 {
		logger.Errorf("space_type [%s], space_id [%s] not found table_id and data_id", spaceType, spaceId)
		return valueData, nil
	}
	var tableIds []string
	for tableId := range tableIdDataId {
		tableIds = append(tableIds, tableId)
	}
	// 提取仅包含写入 influxdb 和 vm 的结果表
	tableIds, err = s.refineTableIds(tableIds)
	// 再一次过滤，过滤到有链路的结果表，并且写入 influxdb 或 vm 的数据
	tableIdDataIdMap := make(map[string]uint)
	var dataIdList []uint
	for _, tableId := range tableIds {
		dataId := tableIdDataId[tableId]
		tableIdDataIdMap[tableId] = dataId
		dataIdList = append(dataIdList, dataId)
	}
	if len(dataIdList) == 0 {
		return valueData, nil
	}
	db := mysql.GetDBSession().DB
	var dsList []resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(db).Select(resulttable.DataSourceDBSchema.BkDataId, resulttable.DataSourceDBSchema.EtlConfig, resulttable.DataSourceDBSchema.SpaceUid, resulttable.DataSourceDBSchema.IsPlatformDataId).BkDataIdIn(dataIdList...).All(&dsList); err != nil {
		return nil, err
	}
	// 获取datasource的信息，避免后续每次都去查询db
	dataIdDetail := make(map[uint]*DataIdDetail)
	for _, ds := range dsList {
		dataIdDetail[ds.BkDataId] = &DataIdDetail{
			EtlConfig:        ds.EtlConfig,
			SpaceUid:         ds.SpaceUid,
			IsPlatformDataId: ds.IsPlatformDataId,
		}
	}
	// 判断是否添加过滤条件
	var rtList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId, resulttable.ResultTableDBSchema.SchemaType, resulttable.ResultTableDBSchema.DataLabel).TableIdIn(tableIds...).All(&rtList); err != nil {
		return nil, err
	}
	// 获取结果表对应的类型
	measurementTypeMap, err := s.getMeasurementTypeByTableId(tableIds, rtList, tableIdDataIdMap)
	if err != nil {
		return nil, err
	}
	// 获取空间所属的数据源 ID
	var spdsList []space.SpaceDataSource
	if err := space.NewSpaceDataSourceQuerySet(db).Select(space.SpaceDataSourceDBSchema.BkDataId).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId).FromAuthorizationEq(false).All(&spdsList); err != nil {
		return nil, err
	}
	for _, tid := range tableIds {
		// NOTE: 特殊逻辑，忽略跨空间类型的 bkci 的结果表
		if strings.HasPrefix(tid, models.Bkci1001TableIdPrefix) {
			continue
		}
		// NOTE: 特殊逻辑，针对 `dbm_system` 开头的结果表，设置过滤条件为空
		if strings.HasPrefix(tid, models.Dbm1001TableIdPrefix) {
			// 如果不允许访问，则需要跳过
			if !stringx.StringInSlice(fmt.Sprintf("%s__%s", spaceType, spaceId), cfg.GlobalAccessDbmRtSpaceUid) {
				continue
			}
			valueData[tid] = map[string]interface{}{"filters": []interface{}{}}
			continue
		}
		// 如果查询不到类型，则忽略
		measurementType, ok := measurementTypeMap[tid]
		if !ok {
			logger.Errorf("table_id [%s] not find measurement type", tid)
			continue
		}
		// 如果没有对应的结果表，则忽略
		dataId, ok := tableIdDataIdMap[tid]
		if !ok {
			logger.Errorf("table_id [%s] not found data_id", tid)
			continue
		}
		detail := dataIdDetail[dataId]
		var isExistSpace bool
		for _, spds := range spdsList {
			if spds.BkDataId == dataId {
				isExistSpace = true
				break
			}
		}
		// 拼装过滤条件, 如果有指定，则按照指定数据设置过滤条件
		if len(defaultFilters) != 0 {
			valueData[tid] = map[string]interface{}{"filters": defaultFilters}
		} else {
			filters := make([]map[string]interface{}, 0)
			if s.isNeedFilterForBkcc(measurementType, spaceType, spaceId, detail, isExistSpace) {
				filters = append(filters, map[string]interface{}{"bk_biz_id": spaceId})
			}
			valueData[tid] = map[string]interface{}{"filters": filters}
		}
	}
	return valueData, nil
}

// 针对业务类型空间判断是否需要添加过滤条件
func (s SpacePusher) isNeedFilterForBkcc(measurementType, spaceType, spaceId string, dataIdDetail *DataIdDetail, isExistSpace bool) bool {
	if dataIdDetail == nil {
		return true
	}

	// 为防止查询范围放大，先功能开关控制，针对归属到具体空间的数据源，不需要添加过滤条件
	if !cfg.GlobalIsRestrictDsBelongSpace && (dataIdDetail.SpaceUid == fmt.Sprintf("%s__%s", spaceType, spaceId)) {
		return false
	}

	// 如果不是自定义时序或exporter，则不需要关注类似的情况，必须增加过滤条件
	tsMeasurementTypes := []string{models.MeasurementTypeBkSplit, models.MeasurementTypeBkStandardV2TimeSeries, models.MeasurementTypeBkExporter}
	if dataIdDetail.EtlConfig != models.ETLConfigTypeBkStandardV2TimeSeries {
		var exist bool
		for _, tp := range tsMeasurementTypes {
			if tp == measurementType {
				exist = true
				break
			}
		}
		if !exist {
			return true
		}
	}
	// 对自定义插件的处理，兼容黑白名单对类型的更改
	// 黑名单时，会更改为单指标单表
	if measurementType == models.MeasurementTypeBkExporter || (dataIdDetail.EtlConfig == models.ETLConfigTypeBkExporter && measurementType == models.MeasurementTypeBkSplit) {
		// 如果space_id与data_id所属空间UID相同，则不需要过滤
		if dataIdDetail.SpaceUid == fmt.Sprintf("%s__%s", spaceType, spaceId) {
			return false
		}
		return true
	}
	// 可以执行到以下代码，必然是自定义时序的数据源
	// 1. 非公共的(全空间或指定空间类型)自定义时序，查询时，不需要任何查询条件
	if !dataIdDetail.IsPlatformDataId {
		return false
	}

	// 2. 公共自定义时序，如果属于当前space，不需要添加过滤条件
	if isExistSpace {
		return false
	}
	// 3. 此时，必然是自定义时序，且是公共的平台数据源，同时非该当前空间下，需要添加过滤条件
	return true
}

// 推送 bcs 类型空间下的集群数据
func (s SpacePusher) composeBcsSpaceBizTableIds(spaceType, spaceId string) (map[string]map[string]interface{}, error) {
	logger.Infof("start to push cluster of bcs space table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	// 首先获取关联业务的数据
	resourceType := models.SpaceTypeBKCC
	db := mysql.GetDBSession().DB
	var sr space.SpaceResource
	if err := space.NewSpaceResourceQuerySet(db).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId).ResourceTypeEq(resourceType).One(&sr); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Errorf("space: [%s__%s], resource_type [%s] not found", spaceType, spaceId, resourceType)
			return make(map[string]map[string]interface{}), nil
		}
		return nil, err
	}
	// 获取空间关联的业务，注意这里业务 ID 为字符串类型
	var bizIdStr string
	if sr.ResourceId != nil {
		bizIdStr = *sr.ResourceId
	}
	options := optionx.NewOptions(map[string]interface{}{"includePlatformDataId": true, "fromAuthorization": false})
	values, err := s.composeData(resourceType, bizIdStr, nil, []map[string]interface{}{{"bk_biz_id": bizIdStr}}, options)
	if err != nil {
		return nil, errors.Wrapf(err, "composeData for [%s_%s] failed", resourceType, bizIdStr)
	}
	// bkci只能访问业务下system.开头的结果表
	systemValues := make(map[string]map[string]interface{})
	for k, v := range values {
		if strings.HasPrefix(k, models.SystemTableIdPrefix) {
			systemValues[k] = v
		}
	}
	return systemValues, nil
}

func (s SpacePusher) composeBksaasSpaceClusterTableIds(spaceType, spaceId string, tableIdList []string) (map[string]map[string]interface{}, error) {
	logger.Infof("start to push cluster of bksaas space table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	// 获取空间的集群数据
	resourceType := models.SpaceTypeBKSAAS
	// 优先进行判断项目相关联的容器资源，减少等待
	db := mysql.GetDBSession().DB
	var sr space.SpaceResource
	if err := space.NewSpaceResourceQuerySet(db).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId).ResourceTypeEq(resourceType).ResourceIdEq(spaceId).One(&sr); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Errorf("space: [%s__%s], resource_type [%s] not found", spaceType, spaceId, resourceType)
			return make(map[string]map[string]interface{}), nil
		}
		return nil, err
	}
	var resList []map[string]interface{}
	if err := jsonx.UnmarshalString(sr.DimensionValues, &resList); err != nil {
		return nil, errors.Wrap(err, "unmarshal space resource dimension failed")
	}
	// 如果关键维度数据为空，同样返回默认
	if len(resList) == 0 {
		return make(map[string]map[string]interface{}), nil
	}
	// 获取集群的数据, 格式: {cluster_id: {"bcs_cluster_id": xxx, "namespace": xxx}}
	clusterInfoMap := make(map[string]interface{})
	var clusterIdList []string
	for _, res := range resList {
		resOptions := optionx.NewOptions(res)
		clusterId, ok := resOptions.GetString("cluster_id")
		if !ok {
			return nil, errors.Errorf("parse space resource dimension values failed, %v", res)
		}
		clusterType, ok := resOptions.GetString("cluster_type")
		if !ok {
			clusterType = models.BcsClusterTypeSingle
		}
		namespaceList, _ := resOptions.GetStringSlice("namespace")

		if clusterType == models.BcsClusterTypeShared && len(namespaceList) != 0 {
			var nsDataList []map[string]interface{}
			for _, ns := range namespaceList {
				nsDataList = append(nsDataList, map[string]interface{}{"bcs_cluster_id": clusterId, "namespace": ns})
			}
			clusterInfoMap[clusterId] = nsDataList
		} else if clusterType == models.BcsClusterTypeSingle {
			clusterInfoMap[clusterId] = []map[string]interface{}{{"bcs_cluster_id": clusterInfoMap, "namespace": nil}}
		}
		clusterIdList = append(clusterIdList, clusterId)
	}
	dataIdClusterIdMap, err := s.getClusterDataIds(clusterIdList, tableIdList)
	if err != nil {
		return nil, err
	}
	if len(dataIdClusterIdMap) == 0 {
		logger.Errorf("space [%s__%s] not found cluster", spaceType, spaceId)
		return make(map[string]map[string]interface{}), nil
	}
	var dataIdList []uint
	for dataId := range dataIdClusterIdMap {
		dataIdList = append(dataIdList, dataId)
	}
	// 获取结果表及数据源
	tableIdDataIdMap, err := s.getResultTablesByDataIds(dataIdList, nil)
	if err != nil {
		return nil, err
	}
	dataValues := make(map[string]map[string]interface{})
	for tid, dataId := range tableIdDataIdMap {
		clusterId, ok := dataIdClusterIdMap[dataId]
		if !ok {
			continue
		}
		// 获取对应的集群及命名空间信息
		filter := clusterInfoMap[clusterId]
		if filter == nil {
			filter = make([]interface{}, 0)
		}
		dataValues[tid] = map[string]interface{}{"filter": filter}
	}
	return dataValues, nil

}

// 推送 bcs 类型空间下的集群数据
func (s SpacePusher) composeBcsSpaceClusterTableIds(spaceType, spaceId string) (map[string]map[string]interface{}, error) {
	logger.Infof("start to push cluster of bcs space table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	// 获取空间的集群数据
	resourceType := models.SpaceTypeBCS
	// 优先进行判断项目相关联的容器资源，减少等待
	db := mysql.GetDBSession().DB
	var sr space.SpaceResource
	if err := space.NewSpaceResourceQuerySet(db).SpaceTypeIdEq(spaceType).SpaceIdEq(spaceId).ResourceTypeEq(resourceType).ResourceIdEq(spaceId).One(&sr); err != nil {
		if gorm.IsRecordNotFoundError(err) {
			logger.Errorf("space: [%s__%s], resource_type [%s] not found", spaceType, spaceId, resourceType)
			return make(map[string]map[string]interface{}), nil
		}
		return nil, err
	}
	var resList []map[string]interface{}
	if err := jsonx.UnmarshalString(sr.DimensionValues, &resList); err != nil {
		return nil, errors.Wrap(err, "unmarshal space resource dimension failed")
	}
	// 如果关键维度数据为空，同样返回默认
	if len(resList) == 0 {
		return make(map[string]map[string]interface{}), nil
	}
	// 获取集群的数据, 格式: {cluster_id: {"bcs_cluster_id": xxx, "namespace": xxx}}
	clusterInfoMap := make(map[string]interface{})
	var clusterIdList []string
	for _, res := range resList {
		resOptions := optionx.NewOptions(res)
		clusterId, ok := resOptions.GetString("cluster_id")
		if !ok {
			return nil, errors.Errorf("parse space resource dimension values failed, %v", res)
		}
		clusterType, ok := resOptions.GetString("cluster_type")
		if !ok {
			clusterType = models.BcsClusterTypeSingle
		}
		namespaceList, _ := resOptions.GetStringSlice("namespace")

		if clusterType == models.BcsClusterTypeShared && len(namespaceList) != 0 {
			var nsDataList []map[string]interface{}
			for _, ns := range namespaceList {
				nsDataList = append(nsDataList, map[string]interface{}{"bcs_cluster_id": clusterId, "namespace": ns})
			}
			clusterInfoMap[clusterId] = nsDataList
		} else if clusterType == models.BcsClusterTypeSingle {
			clusterInfoMap[clusterId] = []map[string]interface{}{{"bcs_cluster_id": clusterInfoMap, "namespace": nil}}
		}
		clusterIdList = append(clusterIdList, clusterId)
	}
	dataIdClusterIdMap, err := s.getClusterDataIds(clusterIdList, nil)
	if err != nil {
		return nil, err
	}
	if len(dataIdClusterIdMap) == 0 {
		logger.Errorf("space [%s__%s] not found cluster", spaceType, spaceId)
		return make(map[string]map[string]interface{}), nil
	}
	var dataIdList []uint
	for dataId := range dataIdClusterIdMap {
		dataIdList = append(dataIdList, dataId)
	}
	// 获取结果表及数据源
	tableIdDataIdMap, err := s.getResultTablesByDataIds(dataIdList, nil)
	if err != nil {
		return nil, err
	}
	dataValues := make(map[string]map[string]interface{})
	for tid, dataId := range tableIdDataIdMap {
		clusterId, ok := dataIdClusterIdMap[dataId]
		if !ok {
			continue
		}
		// 获取对应的集群及命名空间信息
		filter := clusterInfoMap[clusterId]
		if filter == nil {
			filter = make([]interface{}, 0)
		}
		dataValues[tid] = map[string]interface{}{"filter": filter}
	}
	return dataValues, nil
}

// 获取集群及数据源
func (s SpacePusher) getClusterDataIds(clusterIdList, tableIdList []string) (map[uint]string, error) {
	// 如果指定结果表, 则仅过滤结果表对应的数据源
	db := mysql.GetDBSession().DB
	var dataIdList []uint
	if len(tableIdList) != 0 {
		var dsrtList []resulttable.DataSourceResultTable
		for _, chunkTableIdList := range slicex.ChunkSlice(tableIdList, 0) {
			var tempList []resulttable.DataSourceResultTable
			if err := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.BkDataId).TableIdIn(chunkTableIdList...).All(&tempList); err != nil {
				return nil, err
			}
			dsrtList = append(dsrtList, tempList...)
		}
		for _, dsrt := range dsrtList {
			dataIdList = append(dataIdList, dsrt.BkDataId)
		}
	} else if len(clusterIdList) != 0 {
		// 如果集群存在，则获取集群下的内置和自定义数据源
		var clusterList []bcs.BCSClusterInfo
		if err := bcs.NewBCSClusterInfoQuerySet(db).Select(bcs.BCSClusterInfoDBSchema.K8sMetricDataID, bcs.BCSClusterInfoDBSchema.CustomMetricDataID).StatusEq(models.BcsClusterStatusRunning).ClusterIDIn(clusterIdList...).All(&clusterList); err != nil {
			return nil, err
		}
		for _, cluster := range clusterList {
			dataIdList = append(dataIdList, cluster.K8sMetricDataID)
			dataIdList = append(dataIdList, cluster.CustomMetricDataID)
		}
	}
	if len(dataIdList) == 0 {
		return make(map[uint]string), nil
	}
	// 过滤到集群的数据源，仅包含两类，集群内置和集群自定义
	dataIdClusterIdMap := make(map[uint]string)

	var clusterListA []bcs.BCSClusterInfo
	if err := bcs.NewBCSClusterInfoQuerySet(db).Select(bcs.BCSClusterInfoDBSchema.K8sMetricDataID, bcs.BCSClusterInfoDBSchema.ClusterID).StatusEq(models.BcsClusterStatusRunning).K8sMetricDataIDIn(dataIdList...).All(&clusterListA); err != nil {
		return nil, err
	}
	for _, cluster := range clusterListA {
		dataIdClusterIdMap[cluster.K8sMetricDataID] = cluster.ClusterID
	}

	var clusterListB []bcs.BCSClusterInfo
	if err := bcs.NewBCSClusterInfoQuerySet(db).Select(bcs.BCSClusterInfoDBSchema.CustomMetricDataID, bcs.BCSClusterInfoDBSchema.ClusterID).StatusEq(models.BcsClusterStatusRunning).CustomMetricDataIDIn(dataIdList...).All(&clusterListB); err != nil {
		return nil, err
	}
	for _, cluster := range clusterListB {
		dataIdClusterIdMap[cluster.CustomMetricDataID] = cluster.ClusterID
	}

	return dataIdClusterIdMap, nil
}

// 通过数据源 ID 获取结果表数据
func (s SpacePusher) getResultTablesByDataIds(dataIdList []uint, tableIdList []string) (map[string]uint, error) {
	db := mysql.GetDBSession().DB
	var dsrtList []resulttable.DataSourceResultTable
	qs := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.BkDataId, resulttable.DataSourceResultTableDBSchema.TableId)
	if len(dataIdList) != 0 {
		qs = qs.BkDataIdIn(dataIdList...)
	}
	if len(tableIdList) != 0 {
		qs = qs.TableIdIn(tableIdList...)
	}
	if err := qs.All(&dsrtList); err != nil {
		return nil, err
	}
	dataMap := make(map[string]uint)
	for _, dsrt := range dsrtList {
		dataMap[dsrt.TableId] = dsrt.BkDataId
	}
	return dataMap, nil
}

// 组装 bkci 全局下的结果表
func (s SpacePusher) composeBkciLevelTableIds(spaceType, spaceId string) (map[string]map[string]interface{}, error) {
	logger.Infof("start to push bkci level table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	// 过滤空间级的数据源
	dataIds, err := s.getPlatformDataIds(spaceType)
	if err != nil {
		return nil, err
	}
	if len(dataIds) == 0 {
		return make(map[string]map[string]interface{}), nil
	}
	db := mysql.GetDBSession().DB
	var dsrtList []resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(db).Select(resulttable.DataSourceResultTableDBSchema.TableId).BkDataIdIn(dataIds...).All(&dsrtList); err != nil {
		return nil, err
	}
	if len(dsrtList) == 0 {
		return make(map[string]map[string]interface{}), nil
	}
	dataValues := make(map[string]map[string]interface{})
	var tableIds []string
	for _, dsrt := range dsrtList {
		tableIds = append(tableIds, dsrt.TableId)
	}
	// 过滤仅写入influxdb和vm的数据
	tableIds, err = s.refineTableIds(tableIds)
	if err != nil {
		return nil, err
	}
	for _, tid := range tableIds {
		dataValues[tid] = map[string]interface{}{"filters": []map[string]interface{}{{"projectId": spaceId}}}
	}
	return dataValues, nil
}

func (s SpacePusher) composeBkciOtherTableIds(spaceType, spaceId string) (map[string]map[string]interface{}, error) {
	logger.Infof("start to push bkci other table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	var excludeDataIdList []uint
	var clusters []bcs.BCSClusterInfo
	if err := bcs.NewBCSClusterInfoQuerySet(mysql.GetDBSession().DB).All(&clusters); err != nil {
		return nil, err
	}
	for _, c := range clusters {
		excludeDataIdList = append(excludeDataIdList, c.K8sMetricDataID)
		excludeDataIdList = append(excludeDataIdList, c.CustomMetricDataID)
	}
	options := optionx.NewOptions(map[string]interface{}{"includePlatformDataId": false, "fromAuthorization": false})
	tableIdDataIdMap, err := s.GetSpaceTableIdDataId(spaceType, spaceId, nil, excludeDataIdList, options)
	if err != nil {
		return nil, err
	}
	if len(tableIdDataIdMap) == 0 {
		logger.Errorf("space_type [%s], space_id [%s] not found table_id and data_id", spaceType, spaceId)
		return make(map[string]map[string]interface{}), nil
	}
	var tableIds []string
	tableIds, err = s.refineTableIds(tableIds)
	if err != nil {
		return nil, err
	}
	dataValues := make(map[string]map[string]interface{})
	for _, tid := range tableIds {
		// NOTE: 现阶段针对1001下 `system.` 或者 `dbm_system.` 开头的结果表不允许被覆盖
		if strings.HasPrefix(tid, models.SystemTableIdPrefix) || strings.HasPrefix(tid, models.Dbm1001TableIdPrefix) {
			continue
		}
		dataValues[tid] = map[string]interface{}{"filters": []map[string]interface{}{}}
	}
	return dataValues, nil

}

func (s SpacePusher) composeBkciCrossTableIds(spaceType, spaceId string) (map[string]map[string]interface{}, error) {
	logger.Infof("start to push bkci cross table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	db := mysql.GetDBSession().DB
	var rtList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).Select(resulttable.ResultTableDBSchema.TableId).TableIdLike(fmt.Sprintf("%s%%", models.Bkci1001TableIdPrefix)).All(&rtList); err != nil {
		return nil, err
	}
	dataValues := make(map[string]map[string]interface{})
	for _, rt := range rtList {
		dataValues[rt.TableId] = map[string]interface{}{"filters": []map[string]interface{}{{"projectId": rt.TableId}}}
	}
	return nil, nil
}

// 组装蓝鲸应用非集群数据
func (s SpacePusher) composeBksaasOtherTableIds(spaceType, spaceId string, tableIdList []string) (map[string]map[string]interface{}, error) {
	logger.Infof("start to push bksaas other table_id, space_type [%s], space_id [%s]", spaceType, spaceId)
	var excludeDataIdList []uint
	var clusters []bcs.BCSClusterInfo
	if err := bcs.NewBCSClusterInfoQuerySet(mysql.GetDBSession().DB).All(&clusters); err != nil {
		return nil, err
	}
	for _, c := range clusters {
		excludeDataIdList = append(excludeDataIdList, c.K8sMetricDataID)
		excludeDataIdList = append(excludeDataIdList, c.CustomMetricDataID)
	}
	options := optionx.NewOptions(map[string]interface{}{"includePlatformDataId": false})
	tableIdDataIdMap, err := s.GetSpaceTableIdDataId(spaceType, spaceId, tableIdList, excludeDataIdList, options)
	if err != nil {
		return nil, err
	}
	if len(tableIdDataIdMap) == 0 {
		logger.Errorf("space_type [%s], space_id [%s] not found table_id and data_id", spaceType, spaceId)
		return make(map[string]map[string]interface{}), nil
	}
	var tableIds []string
	// 提取仅包含写入 influxdb 和 vm 的结果表
	tableIds, err = s.refineTableIds(tableIds)
	if err != nil {
		return nil, err
	}
	dataValues := make(map[string]map[string]interface{})
	for _, tid := range tableIds {
		// 针对非集群的数据，不限制过滤条件
		dataValues[tid] = map[string]interface{}{"filters": []map[string]interface{}{}}
	}
	return dataValues, nil
}
