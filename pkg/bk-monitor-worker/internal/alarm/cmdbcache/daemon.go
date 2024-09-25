// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// WatchCmdbResourceChangeEventTaskParams 监听cmdb资源变更任务参数
type WatchCmdbResourceChangeEventTaskParams struct {
	Prefix string        `json:"prefix" mapstructure:"prefix"`
	Redis  redis.Options `json:"redis" mapstructure:"redis"`
}

// WatchCmdbResourceChangeEventTask 监听cmdb资源变更任务
func WatchCmdbResourceChangeEventTask(ctx context.Context, payload []byte) error {
	// 任务参数解析
	var params WatchCmdbResourceChangeEventTaskParams
	err := json.Unmarshal(payload, &params)
	if err != nil {
		return errors.Wrapf(err, "unmarshal payload failed, payload: %s", string(payload))
	}

	// 创建cmdb资源变更事件监听器
	watcher, err := NewCmdbResourceWatcher(params.Prefix, &params.Redis)
	if err != nil {
		return errors.Wrap(err, "new cmdb resource watcher failed")
	}

	watcher.Run(ctx)
	return nil
}

type ResourceWatchDaemon struct {
}

// Start 启动CMDB资源监控
func (c *ResourceWatchDaemon) Start(runInstanceCtx context.Context, errorReceiveChan chan<- error, payload []byte) {
	err := WatchCmdbResourceChangeEventTask(runInstanceCtx, payload)
	if err != nil {
		errorReceiveChan <- err
	}
}

// GetTaskDimension 获取任务维度
func (c *ResourceWatchDaemon) GetTaskDimension(payload []byte) string {
	return ""
}

// RefreshTaskParams cmdb缓存刷新任务参数
var cmdbCacheTypes = []string{"host_topo", "business", "module", "set", "service_instance", "dynamic_group"}

// RefreshTaskParams cmdb缓存刷新任务参数
type RefreshTaskParams struct {
	// 缓存key前缀
	Prefix string `json:"prefix" mapstructure:"prefix"`
	// redis配置
	Redis redis.Options `json:"redis" mapstructure:"redis"`

	// 事件处理间隔时间(秒)
	EventHandleInterval int `json:"event_handle_interval" mapstructure:"event_handle_interval"`
	// 全量刷新间隔时间(秒)
	FullRefreshIntervals map[string]int `json:"full_refresh_intervals" mapstructure:"full_refresh_intervals"`

	// 业务执行并发数
	BizConcurrent int `json:"biz_concurrent" mapstructure:"biz_concurrent"`
}

// CacheRefreshTask cmdb缓存刷新任务
func CacheRefreshTask(ctx context.Context, payload []byte) error {
	// 任务参数解析
	var params RefreshTaskParams
	err := json.Unmarshal(payload, &params)
	if err != nil {
		return errors.Wrapf(err, "unmarshal payload failed, payload: %s", string(payload))
	}

	// 业务执行并发数
	bizConcurrent := params.BizConcurrent
	if bizConcurrent <= 0 {
		bizConcurrent = 5
	}

	// 事件处理间隔时间，最低1分钟
	eventHandleInterval := time.Second * time.Duration(params.EventHandleInterval)
	if eventHandleInterval <= 60 {
		eventHandleInterval = time.Hour
	}

	// 全量刷新间隔时间
	fullRefreshIntervals := make(map[string]time.Duration, len(params.FullRefreshIntervals))
	for cacheType, interval := range params.FullRefreshIntervals {
		fullRefreshIntervals[cacheType] = time.Second * time.Duration(interval)
	}

	wg := sync.WaitGroup{}
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 推送自定义上报数据
	wg.Add(1)
	go func() {
		defer wg.Done()
		// 启动指标上报
		reporter, err := remote.NewSpaceReporter(config.BuildInResultTableDetailKey, config.PromRemoteWriteUrl)
		if err != nil {
			logger.Errorf("[cmdb_relation] new space reporter: %v", err)
			return
		}
		defer func() {
			err = reporter.Close(ctx)
		}()
		spaceReport := GetRelationMetricsBuilder().WithSpaceReport(reporter)

		for {
			ticker := time.NewTicker(time.Minute)

			// 事件处理间隔时间
			select {
			case <-cancelCtx.Done():
				GetRelationMetricsBuilder().ClearAllMetrics()
				ticker.Stop()
				return
			case <-ticker.C:
				// 上报指标
				logger.Infof("[cmdb_relation] space report push all")
				if err = spaceReport.PushAll(cancelCtx, time.Now()); err != nil {
					logger.Errorf("[cmdb_relation] relation metrics builder push all error: %v", err.Error())
				}
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		// 创建资源变更事件处理器
		handler, err := NewCmdbEventHandler(params.Prefix, &params.Redis, fullRefreshIntervals, bizConcurrent)
		if err != nil {
			logger.Errorf("[cmdb_relation] new cmdb event handler failed: %v", err)
			cancel()
			return
		}

		for {
			tn := time.Now()
			// 事件处理
			handler.Run(cancelCtx)

			// 事件处理间隔时间
			select {
			case <-cancelCtx.Done():
				handler.Close()
				return
			case <-time.After(eventHandleInterval - time.Now().Sub(tn)):
			}
		}
	}()

	wg.Wait()
	return nil
}

type CacheRefreshDaemon struct{}

// Start 启动缓存刷新
func (c *CacheRefreshDaemon) Start(runInstanceCtx context.Context, errorReceiveChan chan<- error, payload []byte) {
	err := CacheRefreshTask(runInstanceCtx, payload)
	if err != nil {
		errorReceiveChan <- err
	}
}

// GetTaskDimension 获取任务维度
func (c *CacheRefreshDaemon) GetTaskDimension(payload []byte) string {
	return ""
}
