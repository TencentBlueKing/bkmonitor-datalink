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
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// RefreshTimeSeriesMetric : update ts metrics from redis or bkdata
func RefreshTimeSeriesMetric(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshTimeSeriesMetric Runtime panic caught: %v", err)
		}
	}()
	startTime := time.Now() // 记录开始时间
	logger.Info("RefreshTimeSeriesMetric started!")
	db := mysql.GetDBSession().DB
	var tsGroupList []customreport.TimeSeriesGroup
	if err := customreport.NewTimeSeriesGroupQuerySet(db).IsEnableEq(true).IsDeleteEq(false).All(&tsGroupList); err != nil {
		return errors.Wrap(err, "find ts group record error")
	}

	// 获取结果表对应的计算平台结果表
	var tableIdList []string
	for _, tg := range tsGroupList {
		tableIdList = append(tableIdList, tg.TableID)
	}
	rtMapVmRt := make(map[string]string)
	for _, chunkDataLabels := range slicex.ChunkSlice(tableIdList, 0) {
		var tempList []storage.AccessVMRecord
		if err := storage.NewAccessVMRecordQuerySet(db).Select(storage.AccessVMRecordDBSchema.ResultTableId, storage.AccessVMRecordDBSchema.VmResultTableId).ResultTableIdIn(chunkDataLabels...).All(&tempList); err != nil {
			logger.Errorf("RefreshTimeSeriesMetric get vm table id by monitor table id error, %s", err)
			continue
		}
		for _, rtInfo := range tempList {
			rtMapVmRt[rtInfo.ResultTableId] = rtInfo.VmResultTableId
		}
	}

	// 获取redis中数据
	client := redis.GetStorageRedisInstance()
	wlTableIdList := make([]string, 0)
	if wlTableIdByte, err := client.Get(config.BkDataTableIdListRedisPath); err == nil && wlTableIdByte != nil {
		if err := jsonx.Unmarshal(wlTableIdByte, &wlTableIdList); err != nil {
			logger.Errorf("RefreshTimeSeriesMetric get white list table id from redis failed, %v", err)
		}
	}

	// 收集需要更新推送redis的table_id
	tableIdChan := make(chan [2]string, GetGoroutineLimit("refresh_time_series_metric"))
	var updatedTableIds map[string][]string
	wgReceive := sync.WaitGroup{}
	wgReceive.Add(1)
	go func(wg *sync.WaitGroup) {
		defer wg.Done()
		for {
			tableId, ok := <-tableIdChan
			if !ok {
				break
			}
			bkTenantId := tableId[0]
			updatedTableIds[bkTenantId] = append(updatedTableIds[bkTenantId], tableId[1])
		}
	}(&wgReceive)
	ch := make(chan struct{}, GetGoroutineLimit("refresh_time_series_metric"))
	wg := sync.WaitGroup{}
	wg.Add(len(tsGroupList))
	for _, eg := range tsGroupList {
		ch <- struct{}{}
		// 默认不在白名单中
		queryFromBkdata := false
		// 如果不存在 vm rt, 则不会通过bkbase查询
		vmRt, ok := rtMapVmRt[eg.TableID]

		var ds resulttable.DataSource
		if err := resulttable.NewDataSourceQuerySet(db).BkDataIdEq(eg.BkDataID).One(&ds); err != nil {
			logger.Errorf("RefreshTimeSeriesMetric:table_id %s found datasource record error, %v", eg.TableID, err)
		}

		if !ok {
			logger.Errorf("RefreshTimeSeriesMetric:can not find vm result table id by monitor table id: %s", eg.TableID)
			queryFromBkdata = false
		} else if slicex.IsExistItem(wlTableIdList, eg.TableID) {
			// 判断是否在白名单中
			logger.Infof("RefreshTimeSeriesMetric:table_id %s ,data_id %v in white list, will query metrics from bkdata", eg.TableID, eg.BkDataID)
			queryFromBkdata = true
		} else if ds.CreatedFrom == common.DataIdFromBkData {
			logger.Infof("RefreshTimeSeriesMetric:table_id %s ,data_id %v created from bkbase, will query metrics from bkdata", eg.TableID, eg.BkDataID)
			// 如果TSGroup的创建来源是计算平台，则需从计算平台获取相应的指标
			queryFromBkdata = true
		}
		go func(ts customreport.TimeSeriesGroup, tableIdChan chan [2]string, wg *sync.WaitGroup, ch chan struct{}, vmRt string, queryFromBkdata bool) {
			defer func() {
				<-ch
				wg.Done()
			}()

			svc := service.NewTimeSeriesGroupSvc(&ts)
			updated, err := svc.UpdateTimeSeriesMetrics(vmRt, queryFromBkdata)
			if err != nil {
				logger.Errorf("RefreshTimeSeriesMetric: time_series_group: [%s] try to update metrics from bkdata or redis failed, %v", ts.TableID, err)
				return
			}
			logger.Infof("RefreshTimeSeriesMetric: time_series_group: [%s] metric update from bkdata or redis success, updated: %v", ts.TableID, updated)
			if updated {
				tableIdChan <- [2]string{ts.BkTenantId, svc.TableID}
			}
		}(eg, tableIdChan, &wg, ch, vmRt, queryFromBkdata)
	}

	wg.Wait()
	close(tableIdChan)
	// 防止数据没有读完
	wgReceive.Wait()
	if len(updatedTableIds) != 0 {
		logger.Info("RefreshTimeSeriesMetric,start to push table id to redis, updatedTableIds %v", updatedTableIds)
		pusher := service.NewSpacePusher()
		for bkTenantId, tableIds := range updatedTableIds {
			if err := pusher.PushTableIdDetail(bkTenantId, tableIds, true, false); err != nil {
				return errors.Wrapf(err, "RefreshTimeSeriesMetric,metric update to push table id detaild for [%v] failed", updatedTableIds)
			}
		}
		logger.Infof("RefreshTimeSeriesMetric,metric updated of table_id  [%v]", updatedTableIds)
	}
	elapsedTime := time.Since(startTime) // 计算耗时
	logger.Infof("RefreshTimeSeriesMetric finished succuessfully, took %s", elapsedTime)
	return nil
}

// RefreshEventDimension : update event dimension from es
func RefreshEventDimension(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshEventDimension Runtime panic caught: %v", err)
		}
	}()

	dbSession := mysql.GetDBSession()

	qs := customreport.NewEventGroupQuerySet(dbSession.DB)
	// 过滤满足条件的记录
	qs = qs.IsEnableEq(true).IsDeleteEq(false)
	var eventGroupList []customreport.EventGroup
	if err := qs.All(&eventGroupList); err != nil {
		logger.Errorf("find event group record error, %v", err)
		return err
	}
	wg := sync.WaitGroup{}
	ch := make(chan bool, GetGoroutineLimit("refresh_event_dimension"))
	wg.Add(len(eventGroupList))
	for _, eg := range eventGroupList {
		ch <- true
		go func(eg customreport.EventGroup, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()

			if err := eg.UpdateEventDimensionsFromES(ctx); err != nil {
				logger.Errorf("event_group: [%s] try to update event dimension from es failed, %v", eg.TableID, err)
			} else {
				logger.Infof("event_group: [%s] event dimension update from es success", eg.TableID)
			}
		}(eg, &wg, ch)

	}
	wg.Wait()

	return nil
}
