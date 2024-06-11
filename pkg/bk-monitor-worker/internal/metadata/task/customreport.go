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

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// RefreshTimeSeriesMetric : update ts metrics from redis
func RefreshTimeSeriesMetric(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshTimeSeriesMetric Runtime panic caught: %v", err)
		}
	}()
	logger.Info("start to refresh time series metric")
	db := mysql.GetDBSession().DB
	var tsGroupList []customreport.TimeSeriesGroup
	if err := customreport.NewTimeSeriesGroupQuerySet(db).TableIDEq("2_bkmonitor_time_series_1572978.__default__").IsEnableEq(true).IsDeleteEq(false).All(&tsGroupList); err != nil {
		return errors.Wrap(err, "find ts group record error")
	}
	// 获取redis中数据
	client := redis.GetStorageRedisInstance()
	wlTableIdList := make([]string, 0)
	if wlTableIdByte, err := client.Get(config.BkDataTableIdListRedisPath); err == nil && wlTableIdByte != nil {
		if err := jsonx.Unmarshal(wlTableIdByte, &wlTableIdList); err != nil {
			logger.Errorf("get white list table id from redis failed, %v", err)
		}
	}
	// 收集需要更新推送redis的table_id
	tableIdChan := make(chan string, GetGoroutineLimit("refresh_time_series_metric"))
	var updatedTableIds []string
	wgReceive := sync.WaitGroup{}
	go func(wg *sync.WaitGroup) {
		wg.Add(1)
		defer wg.Done()
		for {
			tableId, ok := <-tableIdChan
			if !ok {
				break
			}
			updatedTableIds = append(updatedTableIds, tableId)
		}
	}(&wgReceive)
	ch := make(chan bool, GetGoroutineLimit("refresh_time_series_metric"))
	wg := sync.WaitGroup{}
	wg.Add(len(tsGroupList))
	for _, eg := range tsGroupList {
		ch <- true
		go func(ts customreport.TimeSeriesGroup, tableIdChan chan string, wg *sync.WaitGroup, ch chan bool, wlTableIdList []string) {
			defer func() {
				<-ch
				wg.Done()
			}()

			svc := service.NewTimeSeriesGroupSvc(&ts, 0)
			updated, err := svc.UpdateTimeSeriesMetrics(wlTableIdList)
			logger.Infof("start to update time series metrics, %v", ts.TableID)
			if err != nil {
				logger.Errorf("time_series_group: [%s] try to update metrics from redis failed, %v", ts.TableID, err)
				return
			}
			logger.Infof("time_series_group: [%s] metric update from redis success", ts.TableID)
			if updated {
				tableIdChan <- svc.TableID
			}
		}(eg, tableIdChan, &wg, ch, wlTableIdList)
	}
	wg.Wait()
	close(tableIdChan)
	// 防止数据没有读完
	wgReceive.Wait()
	if len(updatedTableIds) != 0 {
		pusher := service.NewSpacePusher()
		if err := pusher.PushTableIdDetail(updatedTableIds, true); err != nil {
			return errors.Wrapf(err, "metric update to push table id detaild for [%v] failed", updatedTableIds)
		}
		logger.Infof("metric updated of table_id  [%v]", updatedTableIds)
	}
	logger.Info("refresh time series metric success")

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

// RefreshCustomReport2Nodeman : refresh custom report to nodeman
func RefreshCustomReport2Nodeman(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("RefreshCustomReport2Nodeman Runtime panic caught: %v", err)
		}
	}()
	if err := service.NewCustomReportSubscriptionSvc(nil).RefreshCustomReport2Config(nil); err != nil {
		return errors.Wrap(err, "RefreshCustomReport2Config failed")
	}
	return nil
}
