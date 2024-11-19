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
	"strings"
	"sync"
	"time"

	"github.com/Shopify/sarama"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	consulSvc "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
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

	db := mysql.GetDBSession().DB
	var influxdbHostInfoList []storage.InfluxdbHostInfo
	var influxdbClusterInfoList []storage.InfluxdbClusterInfo
	var influxdbStorageList []storage.InfluxdbStorage
	var accessVMRecordList []storage.AccessVMRecord
	var influxdbTagInfoList []storage.InfluxdbTagInfo

	// 更新influxdb路由信息至consul当中
	// 更新主机信息
	if err := storage.NewInfluxdbHostInfoQuerySet(db).All(&influxdbHostInfoList); err != nil {
		logger.Errorf("refresh_influxdb_route query influxdb host info error, %v", err)
	} else {
		storage.RefreshInfluxdbHostInfoConsulClusterConfig(ctx, &influxdbHostInfoList, GetGoroutineLimit("refresh_influxdb_route"))
	}

	// 更新集群信息
	if err := storage.NewInfluxdbClusterInfoQuerySet(db).All(&influxdbClusterInfoList); err != nil {
		logger.Errorf("refresh_influxdb_route query influxdb cluster info error, %v", err)
	} else {
		storage.RefreshInfluxdbClusterInfoConsulClusterConfig(ctx, &influxdbClusterInfoList, GetGoroutineLimit("refresh_influxdb_route"))
	}

	// 更新结果表信息
	if err := storage.NewInfluxdbStorageQuerySet(db).All(&influxdbStorageList); err != nil {
		logger.Errorf("refresh_influxdb_route query influxdb storage error, %v", err)
	} else {
		storage.RefreshInfluxdbStorageConsulClusterConfig(ctx, &influxdbStorageList, GetGoroutineLimit("refresh_influxdb_route"))
	}

	// 更新vm router信息
	if err := storage.NewAccessVMRecordQuerySet(db).All(&accessVMRecordList); err != nil {
		logger.Errorf("refresh_influxdb_route query access vm record error, %v", err)
	} else {
		storage.RefreshVmRouter(ctx, &accessVMRecordList, GetGoroutineLimit("refresh_influxdb_route"))
	}

	// 更新version
	consulInfluxdbVersionPath := fmt.Sprintf(models.InfluxdbInfoVersionConsulPathTemplate, config.StorageConsulPathPrefix, config.BypassSuffixPath)
	if err := models.RefreshRouterVersion(ctx, consulInfluxdbVersionPath); err != nil {
		logger.Errorf("refresh_influxdb_route refresh router version error, %v", err)
	} else {
		logger.Infof("influxdb router config refresh success")
	}

	// 更新TS结果表外部的依赖信息
	if influxdbStorageList == nil {
		if err := storage.NewInfluxdbStorageQuerySet(db).All(&influxdbStorageList); err != nil {
			logger.Errorf("refresh_influxdb_route query influxdb storage error, %v", err)
		} else {
			storage.RefreshInfluxDBStorageOuterDependence(ctx, &influxdbStorageList, GetGoroutineLimit("refresh_influxdb_route"))
		}
	} else {
		storage.RefreshInfluxDBStorageOuterDependence(ctx, &influxdbStorageList, GetGoroutineLimit("refresh_influxdb_route"))
	}

	// 更新tag路由信息
	if err := storage.NewInfluxdbTagInfoQuerySet(db).All(&influxdbTagInfoList); err != nil {
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
			logger.Errorf("RefreshDatasource: Runtime panic caught: %v", err)
		}
	}()

	logger.Infof("RefreshDatasource: start to refresh data source, start_time: %s", time.Now().Truncate(time.Second))

	db := mysql.GetDBSession().DB
	// 过滤满足条件的记录
	var dataSourceRtList []resulttable.DataSourceResultTable
	if err := resulttable.NewDataSourceResultTableQuerySet(db).Select("bk_data_id", "table_id").All(&dataSourceRtList); err != nil {
		logger.Errorf("RefreshDatasource: query datasourceresulttable record error, %v", err)
		return err
	}
	if len(dataSourceRtList) == 0 {
		logger.Infof("RefreshDatasource: no data source need update, skip")
		return nil
	}

	// 过滤到结果表
	var rtList []string
	for _, dsrt := range dataSourceRtList {
		rtList = append(rtList, dsrt.TableId)
	}

	// 过滤状态为启用的结果表
	var enabledResultTableList []resulttable.ResultTable
	// 拆分查询
	for _, chunkRts := range slicex.ChunkSlice(rtList, 0) {
		var tempList []resulttable.ResultTable
		if err := resulttable.NewResultTableQuerySet(db).IsDeletedEq(false).IsEnableEq(true).TableIdIn(chunkRts...).Select("table_id").All(&tempList); err != nil {
			logger.Errorf("RefreshDatasource: query enabled result table error, %v", err)
			continue
		}
		// 组装数据
		enabledResultTableList = append(enabledResultTableList, tempList...)
	}
	// 组装可用的结果表
	enabledRtList := rtList[:0]
	for _, rt := range enabledResultTableList {
		enabledRtList = append(enabledRtList, rt.TableId)
	}
	// 如果可用的结果表为空，则忽略
	if len(enabledRtList) == 0 {
		logger.Warn("RefreshDatasource: not found enabled result by result_table, skip")
		return nil
	}
	// 过滤到可用的数据源
	var dataIdList []uint
	// 用作重复数据的移除
	uniqueMap := make(map[uint]bool)
	for _, dsrt := range dataSourceRtList {
		// 如果结果表可用，并且数据源ID还没有追加过，则追加数据；否则，跳过
		if stringx.StringInSlice(dsrt.TableId, enabledRtList) && !uniqueMap[dsrt.BkDataId] {
			dataIdList = append(dataIdList, dsrt.BkDataId)
			uniqueMap[dsrt.BkDataId] = true
		}
	}

	var dataSourceList []resulttable.DataSource
	// data id 数量可控，先不拆分；仅刷新未迁移到计算平台的数据源 ID 及通过 gse 创建的数据源 ID
	if err := resulttable.NewDataSourceQuerySet(db).CreatedFromEq(common.DataIdFromBkGse).IsEnableEq(true).
		BkDataIdIn(dataIdList...).OrderDescByLastModifyTime().All(&dataSourceList); err != nil {
		logger.Errorf("RefreshDatasource: query datasource record error, %v", err)
		return err
	}

	if len(dataSourceList) == 0 {
		logger.Infof("RefreshDatasource: no datasource need update")
		return nil
	}

	wg := &sync.WaitGroup{}
	ch := make(chan struct{}, GetGoroutineLimit("refresh_datasource"))
	wg.Add(len(dataSourceList))
	for _, dataSource := range dataSourceList {
		ch <- struct{}{}
		go func(ds resulttable.DataSource, wg *sync.WaitGroup, ch chan struct{}) {
			defer func() {
				<-ch
				wg.Done()
			}()
			dsSvc := service.NewDataSourceSvc(&ds)
			consulClient, err := consul.GetInstance()
			if err != nil {
				logger.Errorf("RefreshDatasource: data_id [%v] failed to get consul client, %v,skip", dsSvc.BkDataId, err)
				return
			}

			oldIndex, oldValueBytes, err := consulClient.Get(dsSvc.ConsulConfigPath())
			if err != nil {
				logger.Errorf("RefreshDatasource: data_id [%v] failed to get old value from [%v], %v, will set modifyIndex as 0", dsSvc.BkDataId, dsSvc.ConsulConfigPath(), err)
				return
			}
			if oldValueBytes == nil {
				logger.Infof("RefreshDatasource: data_id [%v] consul path [%v] not found, will set modifyIndex as 0", dsSvc.BkDataId, dsSvc.ConsulConfigPath())
			}
			modifyIndex := oldIndex

			logger.Infof("RefreshDatasource: data_id [%v] try to refresh consul config, modifyIndex: %v", dsSvc.BkDataId, modifyIndex)

			if err := dsSvc.RefreshOuterConfig(ctx, modifyIndex, oldValueBytes); err != nil {
				logger.Errorf("RefreshDatasource: data_id [%v] failed to refresh outer config, %v", dsSvc.BkDataId, err)
				return
			}
			logger.Infof("RefreshDatasource: data_id [%v] refresh all outer success", dsSvc.BkDataId)
		}(dataSource, wg, ch)

	}
	wg.Wait()

	logger.Infof("RefreshDatasource: refresh data source end, end_time: %s", time.Now().Truncate(time.Second))
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
	// 获取可用的且来源于 gse 的 data_id
	var dataIdObjList []resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(db).IsEnableEq(true).CreatedFromEq(common.DataIdFromBkGse).Select(resulttable.DataSourceDBSchema.BkDataId, resulttable.DataSourceDBSchema.MqClusterId).All(&dataIdObjList); err != nil {
		return errors.Wrapf(err, "RefreshKafkaTopicInfo query data source error")
	}
	// 组装 data_id 和 对应的集群 id
	var dataIdList, mqClusterIdList []uint
	dataIdClusterId := make(map[uint]uint)
	for _, obj := range dataIdObjList {
		dataIdList = append(dataIdList, obj.BkDataId)
		mqClusterIdList = append(mqClusterIdList, obj.MqClusterId)
		dataIdClusterId[obj.BkDataId] = obj.MqClusterId
	}
	// 查询对应 kafka topic
	var kafkaTopicInfoList []storage.KafkaTopicInfo
	for _, chunkDataIds := range slicex.ChunkSlice(dataIdList, 0) {
		var tempList []storage.KafkaTopicInfo
		if err := storage.NewKafkaTopicInfoQuerySet(db).BkDataIdIn(chunkDataIds...).All(&tempList); err != nil {
			logger.Errorf("query kafka topic info record error, %s", err)
			continue
		}
		kafkaTopicInfoList = append(kafkaTopicInfoList, tempList...)
	}

	// 移除重复的集群 ID
	uniqueClusterIdList := slicex.RemoveDuplicate(&mqClusterIdList)

	// 通过 kafka 集群 ID, 获取集群信息；集群不会太多，直接查询
	var mqClusterList []storage.ClusterInfo
	if err := storage.NewClusterInfoQuerySet(db).ClusterIDIn(uniqueClusterIdList...).All(&mqClusterList); err != nil {
		logger.Errorf("query cluster info record error, %s", err)
		return err
	}

	// 组装 kafka 集群信息
	kafkaClientInfoMap := make(map[uint]sarama.Client)
	clusterInfoMap := make(map[uint]storage.ClusterInfo)
	for _, cluster := range mqClusterList {
		kafkaClient, err := service.NewClusterInfoSvc(&cluster).GetKafkaClient()
		if err != nil {
			logger.Errorf("get kafka cluster client failed, %s", err)
			continue
		}
		kafkaClientInfoMap[cluster.ClusterID] = kafkaClient
		clusterInfoMap[cluster.ClusterID] = cluster
	}

	wg := &sync.WaitGroup{}
	ch := make(chan struct{}, GetGoroutineLimit("refresh_kafka_topic_info"))
	wg.Add(len(kafkaTopicInfoList))
	// 遍历所有的ES存储并创建index, 并执行完整的es生命周期操作
	for _, info := range kafkaTopicInfoList {
		// 获取 cluster id
		clusterId, ok := dataIdClusterId[info.BkDataId]
		if !ok {
			logger.Infof("data_id [%v] not found in data_id_cluster_id map, skip", info.BkDataId)
			wg.Done()
			continue
		}
		// 获取 kafka 集群信息
		kafkaClient, ok := kafkaClientInfoMap[clusterId]
		if !ok {
			logger.Infof("data_id [%v] and cluster_id [%s] not found cluster info, skip", info.BkDataId, clusterId)
			kafkaClient = nil
		}
		clusterInfo, ok := clusterInfoMap[clusterId]
		if !ok && kafkaClient == nil {
			logger.Infof("data_id [%v] and cluster_id [%s] not found cluster info, skip", info.BkDataId, clusterId)
			wg.Done()
			continue
		}

		ch <- struct{}{}
		go func(info storage.KafkaTopicInfo, clusterInfo storage.ClusterInfo, kafkaClient sarama.Client, wg *sync.WaitGroup, ch chan struct{}) {
			defer func() {
				<-ch
				wg.Done()
			}()
			svc := service.NewKafkaTopicInfoSvc(&info)
			if err := svc.RefreshTopicInfo(clusterInfo, kafkaClient); err != nil {
				logger.Errorf("refresh kafka topic info [%v] failed, %v", svc.Topic, err)
			} else {
				logger.Infof("refresh kafka topic info [%v] success", svc.Topic)
			}
		}(info, clusterInfo, kafkaClient, wg, ch)

	}
	wg.Wait()

	// 关闭kafka client
	for _, client := range kafkaClientInfoMap {
		if client == nil {
			continue
		}
		if err := client.Close(); err != nil {
			logger.Errorf("close kafka client failed, %s", err)
		}
	}

	return nil
}

// CleanDataIdConsulPath clean consul path of data_id
// data id cna
func CleanDataIdConsulPath(ctx context.Context, t *t.Task) error {
	logger.Info("start to clean consul path of data_id")
	db := mysql.GetDBSession().DB
	// 仅获来源为直接通过gse创建的数据源ID
	var dataIdObjList []resulttable.DataSource
	if err := resulttable.NewDataSourceQuerySet(db).IsEnableEq(true).CreatedFromEq(common.DataIdFromBkGse).All(&dataIdObjList); err != nil {
		logger.Errorf("query data source error, %s", err)
		return err
	}
	// 如果为空，则直接返回
	if len(dataIdObjList) == 0 {
		logger.Infof("query data source is null")
		return nil
	}

	// 组装 dataid 的 consul 路径
	var dataIdConsulPaths []string
	for _, ds := range dataIdObjList {
		dataIdConsulPaths = append(dataIdConsulPaths, service.NewDataSourceSvc(&ds).ConsulConfigPath())
	}

	// 获取 consul 中存在的数据源 ID
	consulClient, err := consulSvc.GetInstance()
	if err != nil {
		logger.Errorf("get consul client failed, %s", err)
		return err
	}
	// 按照前缀，获取所有路径
	consulKeys, err := consulClient.ListKeysWithPrefix(fmt.Sprintf(models.DataSourceConsulPathTemplate+"/", config.StorageConsulPathPrefix))
	if err != nil {
		logger.Errorf("list consul key with prefix error, %s", err)
		return err
	}

	// 过滤出全路径的内容
	var consulPaths []string
	for _, key := range consulKeys {
		if strings.HasSuffix(key, "/") {
			continue
		}
		consulPaths = append(consulPaths, key)
	}

	// 清理路径
	if err := service.CleanConsulPath(consulClient, &dataIdConsulPaths, &consulPaths); err != nil {
		logger.Errorf("clean consul path of data_id error, %s", err)
		return err
	}

	logger.Info("clean consul path of data_id end")
	return nil
}
