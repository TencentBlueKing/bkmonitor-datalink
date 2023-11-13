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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/customreport"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	// Goroutine数限制
	goroutineLimit = 10
)

// RefreshTimeSeriesMetric : update ts metrics from redis
func RefreshTimeSeriesMetric(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("Runtime panic caught: %v\n", err)
		}
	}()
	// funcName := runtimex.GetFuncName()
	dbSession := mysql.GetDBSession()
	qs := customreport.NewTimeSeriesGroupQuerySet(dbSession.DB)
	qs = qs.IsEnableEq(true).IsDeleteEq(false)
	// 过滤满足条件的记录
	var tsGroupList []customreport.TimeSeriesGroup
	if err := qs.All(&tsGroupList); err != nil {
		logger.Errorf("find ts group record error, %v", err)
		return err
	}
	// TODO: 先不拆分子任务，观察一下单个任务是不是可以满足需求
	for _, ts := range tsGroupList {
		if err := ts.UpdateMetricsFromRedis(); err != nil {
			logger.Errorf("time_series_group: [%s] try to update metrics from redis failed", ts.TableID)
		} else {
			logger.Infof("time_series_group: [%s] metric update from redis success", ts.TableID)
		}
	}

	return nil
}

// RefreshEventDimension : update event dimension from es
func RefreshEventDimension(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("Runtime panic caught: %v\n", err)
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
	ch := make(chan bool, goroutineLimit)
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
