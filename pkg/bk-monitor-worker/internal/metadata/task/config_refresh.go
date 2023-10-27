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

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// RefreshESStorage : update es storage()
func RefreshESStorage(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("Runtime panic caught: %v\n", err)
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
			logger.Errorf("Runtime panic caught: %v\n", err)
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
	consulInfluxdbVersionPath := fmt.Sprintf(models.InfluxdbInfoVersionConsulPathTemplate, viper.GetString(consul.ConsulBasePath))
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
			logger.Errorf("Runtime panic caught: %v\n", err)
		}
	}()

	dbSession := mysql.GetDBSession()
	// 过滤满足条件的记录
	var dataSourceRtList []resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(dbSession.DB).Select("bk_data_id").All(&dataSourceRtList); err != nil {
		logger.Errorf("query datasourceresulttable record error, %v", err)
		return err
	}
	if len(dataSourceRtList) == 0 {
		logger.Infof("no data source result table records, skip")
		return nil
	}
	var dataIdList []uint
	for _, dsrt := range dataSourceRtList {
		dataIdList = append(dataIdList, dsrt.BkDataId)
	}
	dataIdList = slicex.UintSet2List(slicex.UintList2Set(dataIdList))

	var dataSourceList []resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(dbSession.DB).IsEnableEq(true).
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
	// 遍历所有的ES存储并创建index, 并执行完整的es生命周期操作
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
