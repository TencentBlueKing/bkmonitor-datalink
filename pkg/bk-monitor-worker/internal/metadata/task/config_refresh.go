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
	"fmt"
	"sync"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/stringx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// RefreshESStorage : update es storage()
func RefreshESStorage(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshESStorage Runtime panic caught: %v", err)
		}
	}()

	dbSession := mysql.GetDBSession()
	// 过滤满足条件的记录
	var allEsStorageList []storage.ESStorage
	if err := storage.NewESStorageQuerySet(dbSession.DB).All(&allEsStorageList); err != nil {
		logger.Errorf("query all es storage record error, %v", err)
		return err
	}

	var esStorageTableIdList []string
	var tableIdEsStorageMap = make(map[string]storage.ESStorage)
	for _, esStorage := range allEsStorageList {
		esStorageTableIdList = append(esStorageTableIdList, esStorage.TableID)
		tableIdEsStorageMap[esStorage.TableID] = esStorage
	}
	if len(esStorageTableIdList) == 0 {
		logger.Infof("no es storage need update")
		return nil
	}

	// 过滤到有效的table_id
	var resultTableList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(dbSession.DB).IsEnableEq(true).IsDeletedEq(false).
		TableIdIn(esStorageTableIdList...).All(&resultTableList); err != nil {
		logger.Errorf("query result table record error, %v", err)
		return err
	}
	// 需要刷新的es_storage
	var needUpdateEsStorageList []storage.ESStorage
	for _, rt := range resultTableList {
		esStorage, ok := tableIdEsStorageMap[rt.TableId]
		if ok {
			needUpdateEsStorageList = append(needUpdateEsStorageList, esStorage)
		}
	}
	if len(needUpdateEsStorageList) == 0 {
		logger.Infof("no es storage need update")
		return nil
	}

	wg := &sync.WaitGroup{}
	ch := make(chan bool, GetGoroutineLimit("refresh_es_storage"))
	wg.Add(len(needUpdateEsStorageList))
	// 遍历所有的ES存储并创建index, 并执行完整的es生命周期操作
	for _, esStorage := range needUpdateEsStorageList {
		ch <- true
		go func(ess storage.ESStorage, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()

			if err := ess.ManageESStorage(ctx); err != nil {
				logger.Errorf("es_storage: [%v] table_id [%s] try to refresh es failed, %v", ess.StorageClusterID, ess.TableID, err)
			} else {
				logger.Infof("es_storage: [%v] table_id [%s] refresh es success", ess.StorageClusterID, ess.TableID)
			}
		}(esStorage, wg, ch)

	}
	wg.Wait()

	return nil
}

// RefreshInfluxdbRoute : update influxdb route
func RefreshInfluxdbRoute(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshInfluxdbRoute Runtime panic caught: %v", err)
		}
	}()

	dbSession := mysql.GetDBSession()
	var influxdbHostInfoList []storage.InfluxdbHostInfo
	var influxdbClusterInfoList []storage.InfluxdbClusterInfo
	var influxdbStorageList []storage.InfluxdbStorage
	var accessVMRecordList []storage.AccessVMRecord
	var influxdbTagInfoList []storage.InfluxdbTagInfo

	// 更新influxdb路由信息至consul当中
	// 更新主机信息
	err := storage.NewInfluxdbHostInfoQuerySet(dbSession.DB).All(&influxdbHostInfoList)
	if err != nil {
		logger.Errorf("refresh_influxdb_route query influxdb host info error, %v", err)
	} else {
		storage.RefreshInfluxdbHostInfoConsulClusterConfig(ctx, &influxdbHostInfoList, GetGoroutineLimit("refresh_influxdb_route"))
	}

	// 更新集群信息
	err = storage.NewInfluxdbClusterInfoQuerySet(dbSession.DB).All(&influxdbClusterInfoList)
	if err != nil {
		logger.Errorf("refresh_influxdb_route query influxdb cluster info error, %v", err)
	} else {
		storage.RefreshInfluxdbClusterInfoConsulClusterConfig(ctx, &influxdbClusterInfoList, GetGoroutineLimit("refresh_influxdb_route"))
	}

	// 更新结果表信息
	err = storage.NewInfluxdbStorageQuerySet(dbSession.DB).All(&influxdbStorageList)
	if err != nil {
		logger.Errorf("refresh_influxdb_route query influxdb storage error, %v", err)
	} else {
		storage.RefreshInfluxdbStorageConsulClusterConfig(ctx, &influxdbStorageList, GetGoroutineLimit("refresh_influxdb_route"))
	}

	// 更新vm router信息
	err = storage.NewAccessVMRecordQuerySet(dbSession.DB).All(&accessVMRecordList)
	if err != nil {
		logger.Errorf("refresh_influxdb_route query access vm record error, %v", err)
	} else {
		storage.RefreshVmRouter(ctx, &accessVMRecordList, GetGoroutineLimit("refresh_influxdb_route"))
	}

	// 更新version
	consulInfluxdbVersionPath := fmt.Sprintf(models.InfluxdbInfoVersionConsulPathTemplate, config.StorageConsulPathPrefix, config.BypassSuffixPath)
	err = models.RefreshRouterVersion(ctx, consulInfluxdbVersionPath)
	if err != nil {
		logger.Errorf("refresh_influxdb_route refresh router version error, %v", err)
	} else {
		logger.Infof("influxdb router config refresh success")
	}

	// 更新TS结果表外部的依赖信息
	if influxdbStorageList == nil {
		err := storage.NewInfluxdbStorageQuerySet(dbSession.DB).All(&influxdbStorageList)
		if err != nil {
			logger.Errorf("refresh_influxdb_route query influxdb storage error, %v", err)
		} else {
			storage.RefreshInfluxDBStorageOuterDependence(ctx, &influxdbStorageList, GetGoroutineLimit("refresh_influxdb_route"))
		}
	} else {
		storage.RefreshInfluxDBStorageOuterDependence(ctx, &influxdbStorageList, GetGoroutineLimit("refresh_influxdb_route"))
	}

	// 更新tag路由信息
	err = storage.NewInfluxdbTagInfoQuerySet(dbSession.DB).All(&influxdbTagInfoList)
	if err != nil {
		logger.Errorf("refresh_influxdb_route query influxdb tag info error, %v", err)
	} else {
		storage.RefreshConsulTagConfig(ctx, &influxdbTagInfoList, GetGoroutineLimit("refresh_influxdb_route"))
	}

	return nil
}

// RefreshDatasource update datasource
func RefreshDatasource(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshDatasource Runtime panic caught: %v", err)
		}
	}()

	db := mysql.GetDBSession().DB
	// 过滤满足条件的记录
	var dataSourceRtList []resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(db).Select("bk_data_id", "table_id").All(&dataSourceRtList); err != nil {
		logger.Errorf("query datasourceresulttable record error, %v", err)
		return err
	}
	// 过滤到结果表
	var rtList []string
	for _, dsrt := range dataSourceRtList {
		rtList = append(rtList, dsrt.TableId)
	}
	// 如果全部不可用，则直接返回
	if len(rtList) == 0{
		logger.Infof("not enabled result table by data_source_result_table, skip")
		return nil
	}
	// 过滤状态为启用的结果表
	var enabledResultTableList []resulttable.ResultTable
	if err := resulttable.NewResultTableQuerySet(db).TableIdIn(rtList...).Select("table_id").All(&enabledResultTableList); err != nil {
		logger.Errorf("query enabled result table error, %v", err)
		return err
	}
	// 组装可用的结果表
	var enabledRtList []string
	for _, rt := range enabledResultTableList {
		enabledRtList = append(enabledRtList, rt.TableId)
	}
	// 如果可用的结果表为空，则忽略
	if len(enabledRtList) == 0 {
		logger.Infof("not found enabled result by result_table, skip")
		return nil
	}
	// 过滤到可用的数据源
	var dataIdList []uint
	for _, dsrt := range dataSourceRtList {
		if stringx.StringInSlice(dsrt.TableId, enabledRtList){
			dataIdList = append(dataIdList, dsrt.BkDataId)
		}
	}
	// 如果为空，则认为所有数据源不可用
	if len(dataIdList) == 0 {
		logger.Infof("not found data source by enabled result table")
		return nil
	}

	dataIdList = slicex.RemoveDuplicate(&dataIdList)

	var dataSourceList []resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(db).IsEnableEq(true).
		BkDataIdIn(dataIdList...).OrderDescByLastModifyTime().All(&dataSourceList); err != nil {
		logger.Errorf("query datasource record error, %v", err)
		return err
	}

	if len(dataSourceList) == 0 {
		logger.Infof("no datasource need update")
		return nil
	}

	wg := &sync.WaitGroup{}
	ch := make(chan bool, GetGoroutineLimit("refresh_datasource"))
	wg.Add(len(dataSourceList))
	for _, dataSource := range dataSourceList {
		ch <- true
		go func(ds resulttable.DataSource, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			dsSvc := service.NewDataSourceSvc(&ds)
			if err := dsSvc.RefreshOuterConfig(ctx); err != nil {
				logger.Errorf("data_id [%v] failed to refresh outer config, %v", dsSvc.BkDataId, err)
			} else {
				logger.Infof("data_id [%v] refresh all outer success", dsSvc.BkDataId)
			}
		}(dataSource, wg, ch)

	}
	wg.Wait()

	return nil
}

// RefreshESRestore 刷新回溯状态
func RefreshESRestore(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshEsRestore Runtime panic caught: %v", err)
		}
	}()

	db := mysql.GetDBSession().DB
	// 过滤满足条件的记录
	var restoreList []storage.EsSnapshotRestore
	if err := storage.NewEsSnapshotRestoreQuerySet(db).IsDeletedNe(true).All(&restoreList); err != nil {
		logger.Errorf("query EsSnapshotRestore record error, %v", err)
		return err
	}
	var notDoneRestores []storage.EsSnapshotRestore
	for _, r := range restoreList {
		if r.TotalDocCount != r.CompleteDocCount {
			notDoneRestores = append(notDoneRestores, r)
		}
	}
	if len(notDoneRestores) == 0 {
		logger.Infof("no restore need refresh, skip")
		return nil
	}

	wg := &sync.WaitGroup{}
	ch := make(chan bool, GetGoroutineLimit("refresh_es_restore"))
	wg.Add(len(notDoneRestores))
	// 遍历所有的ES存储并创建index, 并执行完整的es生命周期操作
	for _, restore := range notDoneRestores {
		ch <- true
		go func(r storage.EsSnapshotRestore, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			svc := service.NewEsSnapshotRestoreSvc(&r)
			if _, err := svc.GetCompleteDocCount(ctx); err != nil {
				logger.Errorf("es_restore [%v] failed to cron task, %v", svc.RestoreID, err)
			} else {
				logger.Infof("es_restore [%v] refresh success", svc.RestoreID)
			}
		}(restore, wg, ch)

	}
	wg.Wait()

	return nil
}

// RefreshKafkaTopicInfo 刷新kafka topic into的partitions
func RefreshKafkaTopicInfo(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshKafkaTopicInfo Runtime panic caught: %v\n", err)
		}
	}()
	db := mysql.GetDBSession().DB
	var kafkaTopicInfoList []storage.KafkaTopicInfo
	if err := storage.NewKafkaTopicInfoQuerySet(db).All(&kafkaTopicInfoList); err != nil {
		return errors.Wrapf(err, "query RefreshKafkaTopicInfo failed")
	}

	wg := &sync.WaitGroup{}
	ch := make(chan bool, GetGoroutineLimit("refresh_datasource"))
	wg.Add(len(kafkaTopicInfoList))
	// 遍历所有的ES存储并创建index, 并执行完整的es生命周期操作
	for _, info := range kafkaTopicInfoList {
		ch <- true
		go func(info storage.KafkaTopicInfo, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			svc := service.NewKafkaTopicInfoSvc(&info)
			if err := svc.RefreshTopicInfo(); err != nil {
				logger.Errorf("refresh kafka topic info [%v] failed, %v", svc.Topic, err)
			} else {
				logger.Infof("refresh kafka topic info [%v] success", svc.Topic)
			}
		}(info, wg, ch)

	}
	wg.Wait()

	return nil
}
