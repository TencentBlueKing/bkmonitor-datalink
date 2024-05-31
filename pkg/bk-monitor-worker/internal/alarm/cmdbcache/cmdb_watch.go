// MIT License

// Copyright (c) 2021~2022 腾讯蓝鲸

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package cmdbcache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// CmdbResourceType cmdb监听资源类型
type CmdbResourceType string

const (
	CmdbResourceTypeHost             CmdbResourceType = "host"
	CmdbResourceTypeHostRelation     CmdbResourceType = "host_relation"
	CmdbResourceTypeBiz              CmdbResourceType = "biz"
	CmdbResourceTypeSet              CmdbResourceType = "set"
	CmdbResourceTypeModule           CmdbResourceType = "module"
	CmdbResourceTypeMainlineInstance CmdbResourceType = "mainline_instance"
	CmdbResourceTypeProcess          CmdbResourceType = "process"
)

// CmdbResourceTypeFields cmdb资源类型对应的监听字段
var CmdbResourceTypeFields = map[CmdbResourceType][]string{
	CmdbResourceTypeHost:             {"bk_host_id", "bk_host_innerip", "bk_cloud_id", "bk_agent_id"},
	CmdbResourceTypeHostRelation:     {"bk_host_id", "bk_biz_id"},
	CmdbResourceTypeBiz:              {"bk_biz_id"},
	CmdbResourceTypeSet:              {"bk_biz_id", "bk_set_id", "set_template_id"},
	CmdbResourceTypeModule:           {"bk_module_id", "bk_biz_id", "service_template_id"},
	CmdbResourceTypeMainlineInstance: {"bk_obj_id", "bk_inst_id", "bk_obj_name", "bk_inst_name"},
	CmdbResourceTypeProcess:          {"bk_biz_id"},
}

// CmdbResourceWatcher cmdb资源监听器
type CmdbResourceWatcher struct {
	// 缓存key前缀
	prefix string
	// cmdb api client
	cmdbApi *cmdb.Client

	// redis client
	redisClient redis.UniversalClient
}

// NewCmdbResourceWatcher 创建cmdb资源监听器
func NewCmdbResourceWatcher(prefix string, rOpt *redis.Options) (*CmdbResourceWatcher, error) {
	// 创建redis client
	redisClient, err := redis.GetClient(rOpt)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create redis client")
	}

	// 创建cmdb api client
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create cmdb api client")
	}

	return &CmdbResourceWatcher{
		prefix:      prefix,
		redisClient: redisClient,
		cmdbApi:     cmdbApi,
	}, nil

}

// getBkCursor 获取cmdb资源变更事件游标
func (w *CmdbResourceWatcher) getBkCursor(ctx context.Context, resourceType CmdbResourceType) string {
	// 从redis中获取cmdb资源变更游标
	bkCursorKey := fmt.Sprintf("%s.cmdb_resource_watch_cursor.%s", w.prefix, resourceType)
	bkCursorResult := w.redisClient.Get(ctx, bkCursorKey)
	if bkCursorResult.Err() != nil {
		if !errors.Is(bkCursorResult.Err(), redis.Nil) {
			logger.Errorf("get cmdb resource watch cursor error: %v", bkCursorResult.Err())
			return ""
		}
	}
	return bkCursorResult.Val()
}

// setBkCursor 记录cmdb资源变更事件游标
func (w *CmdbResourceWatcher) setBkCursor(ctx context.Context, resourceType CmdbResourceType, cursor string) error {
	// 设置cmdb资源变更游标
	bkCursorKey := fmt.Sprintf("%s.cmdb_resource_watch_cursor.%s", w.prefix, resourceType)
	if _, err := w.redisClient.Set(ctx, bkCursorKey, cursor, time.Hour).Result(); err != nil {
		return errors.Wrap(err, "set cmdb resource watch cursor error")
	}
	return nil
}

// Watch 监听资源变更事件并记录
func (w *CmdbResourceWatcher) Watch(ctx context.Context, resourceType CmdbResourceType) (bool, error) {
	params := map[string]interface{}{
		"bk_fields":           CmdbResourceTypeFields[resourceType],
		"bk_resource":         resourceType,
		"bk_supplier_account": "0",
	}

	// 获取资源变更事件游标
	bkCursor := w.getBkCursor(ctx, resourceType)
	if bkCursor != "" {
		params["bk_cursor"] = bkCursor
	}

	// 请求监听资源变化事件API
	var resp cmdb.ResourceWatchResp
	_, err := w.cmdbApi.ResourceWatch().SetContext(ctx).SetBody(params).SetResult(&resp).Request()
	err = api.HandleApiResultError(resp.ApiCommonRespMeta, err, "watch cmdb resource api failed")
	if err != nil {
		return false, err
	}

	// 无资源变更事件
	if !resp.Data.BkWatched {
		if len(resp.Data.BkEvents) == 0 {
			return false, nil
		}

		// 记录资源变更事件游标
		newCursor := resp.Data.BkEvents[len(resp.Data.BkEvents)-1].BkCursor
		if newCursor != "" && newCursor != bkCursor {
			if err := w.setBkCursor(ctx, resourceType, newCursor); err != nil {
				logger.Error("set cmdb resource watch cursor error: %v", err)
			}
		}

		return false, nil
	}

	// 记录cmdb资源变更事件
	events := make([]string, 0)
	for _, event := range resp.Data.BkEvents {
		val, _ := json.Marshal(event)
		events = append(events, string(val))
	}
	bkEventKey := fmt.Sprintf("%s.cmdb_resource_watch_event.%s", w.prefix, resourceType)
	w.redisClient.RPush(ctx, bkEventKey, events)

	// 记录最后一个cmdb资源变更事件游标
	if len(resp.Data.BkEvents) > 0 {
		err = w.setBkCursor(ctx, resourceType, resp.Data.BkEvents[len(resp.Data.BkEvents)-1].BkCursor)
		if err != nil {
			logger.Error("set cmdb resource watch cursor error: %v", err)
		}
	}

	return true, nil
}

// Run 启动cmdb资源监听任务
func (w *CmdbResourceWatcher) Run(ctx context.Context) {
	waitGroup := sync.WaitGroup{}
	logger.Info("start watch cmdb resource")

	// 按资源类型启动处理任务
	for resourceType := range CmdbResourceTypeFields {
		waitGroup.Add(1)
		resourceType := resourceType
		// 启动监听任务
		go func() {
			defer waitGroup.Done()
			lastTime := time.Now()
			haveEvent, err := true, error(nil)
			for {
				select {
				case <-ctx.Done():
					return
				default:
					// 如果上次监听时间小于5秒且监听无事件，则等待到5秒
					if !haveEvent && time.Now().Sub(lastTime) < time.Second*5 {
						time.Sleep(time.Second*5 - time.Now().Sub(lastTime))
					}

					haveEvent, err = w.Watch(ctx, resourceType)
					if err != nil {
						logger.Errorf("watch cmdb resource(%s) error: %v", resourceType, err)
					}
				}
				// 记录上次监听时间
				lastTime = time.Now()
			}
		}()
	}

	// 等待任务结束
	waitGroup.Wait()
}

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

// CmdbEventHandler cmdb资源变更事件处理器
type CmdbEventHandler struct {
	// 缓存key前缀
	prefix string

	// redis client
	redisClient redis.UniversalClient

	// cache cacheManager
	cacheManager Manager

	// 资源类型
	resourceTypes []CmdbResourceType

	// full refresh interval
	fullRefreshInterval time.Duration
}

// NewCmdbEventHandler 创建cmdb资源变更事件处理器
func NewCmdbEventHandler(prefix string, rOpt *redis.Options, cacheType string, fullRefreshInterval time.Duration, concurrentLimit int) (*CmdbEventHandler, error) {
	// 创建redis client
	redisClient, err := redis.GetClient(rOpt)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create redis client")
	}

	// 创建缓存管理器
	cacheManager, err := NewCacheManagerByType(rOpt, prefix, cacheType, concurrentLimit)
	if err != nil {
		return nil, errors.Wrap(err, "new cache Manager failed")
	}

	// 获取关联资源类型
	resourceTypes, ok := cmdbEventHandlerResourceTypeMap[cacheType]
	if !ok {
		return nil, errors.Errorf("unsupported cache type: %s", cacheType)
	}

	return &CmdbEventHandler{
		prefix:              prefix,
		redisClient:         redisClient,
		cacheManager:        cacheManager,
		resourceTypes:       resourceTypes,
		fullRefreshInterval: fullRefreshInterval,
	}, nil
}

// getBkEvents 获取全部资源变更事件
func (h *CmdbEventHandler) getBkEvents(ctx context.Context, resourceType CmdbResourceType) ([]cmdb.ResourceWatchEvent, error) {
	// 获取资源变更事件
	bkEventKey := fmt.Sprintf("%s.cmdb_resource_watch_event.%s", h.prefix, resourceType)

	// 从redis中获取该资源类型的所有事件
	eventStrings := make([]string, 0)
	for {
		result, err := h.redisClient.LPop(ctx, bkEventKey).Result()
		if err != nil {
			if !errors.Is(err, redis.Nil) {
				logger.Errorf("get cmdb resource(%s) watch event error: %v", resourceType, err)
				break
			}
		}
		// 如果没有事件了，退出
		if result == "" {
			break
		}

		eventStrings = append(eventStrings, result)
	}

	// 解析事件
	events := make([]cmdb.ResourceWatchEvent, 0)
	for _, eventStr := range eventStrings {
		var event cmdb.ResourceWatchEvent
		err := json.Unmarshal([]byte(eventStr), &event)
		if err != nil {
			logger.Errorf("unmarshal cmdb resource(%s) watch event error: %v", resourceType, err)
			continue
		}
		events = append(events, event)
	}

	return events, nil
}

// ifRunRefreshAll 判断是否执行全量刷新
func (h *CmdbEventHandler) ifRunRefreshAll(ctx context.Context, cacheType string) bool {
	// 获取最后一次全量刷新时间
	lastUpdateTimeKey := fmt.Sprintf("%s.cmdb_last_refresh_all_time.%s", h.prefix, cacheType)
	lastUpdateTime, err := h.redisClient.Get(ctx, lastUpdateTimeKey).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			logger.Errorf("get last update time error: %v", err)
			return false
		}
	}
	var lastUpdateTimestamp int64
	if lastUpdateTime != "" {
		lastUpdateTimestamp, err = strconv.ParseInt(lastUpdateTime, 10, 64)
	} else {
		lastUpdateTimestamp = 0
	}

	// 如果超过全量刷新间隔时间，执行全量刷新
	if time.Now().Unix()-lastUpdateTimestamp > int64(h.fullRefreshInterval.Seconds()) {
		return true
	}

	return false
}

// Handle 处理cmdb资源变更事件
func (h *CmdbEventHandler) Handle(ctx context.Context) {
	for _, resourceType := range h.resourceTypes {
		// 获取资源变更事件
		events, err := h.getBkEvents(ctx, resourceType)
		if err != nil {
			logger.Errorf("get cmdb resource(%s) watch event error: %v", resourceType, err)
			continue
		}

		logger.Infof("get cmdb resource(%s) watch event: %d", resourceType, len(events))

		// 重置
		h.cacheManager.Reset()

		// 如果超过全量刷新间隔时间，执行全量刷新
		if h.ifRunRefreshAll(ctx, h.cacheManager.Type()) {
			// 全量刷新
			err := RefreshAll(ctx, h.cacheManager, h.cacheManager.GetConcurrentLimit())
			if err != nil {
				logger.Errorf("refresh all cache failed: %v", err)
			}

			logger.Infof("refresh all cmdb resource(%s) cache", h.cacheManager.Type())

			// 记录全量刷新时间
			lastUpdateTimeKey := fmt.Sprintf("%s.cmdb_last_refresh_all_time.%s", h.prefix, h.cacheManager.Type())
			_, err = h.redisClient.Set(ctx, lastUpdateTimeKey, strconv.FormatInt(time.Now().Unix(), 10), 24*time.Hour).Result()
			if err != nil {
				logger.Errorf("set last update time error: %v", err)
			}

			return
		}

		// 无事件
		if len(events) == 0 {
			continue
		}

		updateEvents := make([]map[string]interface{}, 0)
		cleanEvents := make([]map[string]interface{}, 0)

		for _, event := range events {
			switch event.BkEventType {
			case "update", "create":
				updateEvents = append(updateEvents, event.BkDetail)
			case "delete":
				cleanEvents = append(cleanEvents, event.BkDetail)
			}
		}

		// 更新缓存
		if len(updateEvents) > 0 {
			logger.Infof("update cmdb resource(%s) cache by events: %d", resourceType, len(updateEvents))
			err := h.cacheManager.UpdateByEvents(ctx, string(resourceType), updateEvents)
			if err != nil {
				logger.Errorf("update cache by events failed: %v", err)
			}
		}

		// 清理缓存
		if len(cleanEvents) > 0 {
			logger.Infof("clean cmdb resource(%s) cache by events: %d", resourceType, len(cleanEvents))
			err := h.cacheManager.CleanByEvents(ctx, string(resourceType), cleanEvents)
			if err != nil {
				logger.Errorf("clean cache by events failed: %v", err)
			}
		}
	}
}

// cmdbEventHandlerResourceTypeMap cmdb资源事件执行器与资源类型映射
var cmdbEventHandlerResourceTypeMap = map[string][]CmdbResourceType{
	"host_topo":        {CmdbResourceTypeHost, CmdbResourceTypeHostRelation, CmdbResourceTypeMainlineInstance},
	"business":         {CmdbResourceTypeBiz},
	"module":           {CmdbResourceTypeModule},
	"set":              {CmdbResourceTypeSet},
	"service_instance": {CmdbResourceTypeProcess},
}

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

	CacheTypes []string `json:"cache_types" mapstructure:"cache_types"`
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

	// 全量刷新间隔时间，最低10分钟
	fullRefreshIntervals := make(map[string]time.Duration, len(params.FullRefreshIntervals))
	for cacheType, interval := range params.FullRefreshIntervals {
		fullRefreshIntervals[cacheType] = time.Second * time.Duration(interval)
	}

	// 需要刷新的缓存类型
	cacheTypes := params.CacheTypes
	if len(cacheTypes) == 0 {
		for cacheType := range cmdbEventHandlerResourceTypeMap {
			cacheTypes = append(cacheTypes, cacheType)
		}
	} else {
		for _, cacheType := range cacheTypes {
			if _, ok := cmdbEventHandlerResourceTypeMap[cacheType]; !ok {
				return errors.Errorf("unsupported cache type: %s", cacheType)
			}
		}
	}

	wg := sync.WaitGroup{}
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	for _, cacheType := range cacheTypes {
		wg.Add(1)
		cacheType := cacheType
		fullRefreshInterval, ok := fullRefreshIntervals[cacheType]
		// 最低600秒的间隔
		if !ok {
			fullRefreshInterval = time.Second * 600
		}

		go func() {
			defer wg.Done()

			// 创建资源变更事件处理器
			handler, err := NewCmdbEventHandler(params.Prefix, &params.Redis, cacheType, fullRefreshInterval, bizConcurrent)
			if err != nil {
				logger.Errorf("new cmdb event handler failed: %v", err)
				cancel()
				return
			}

			logger.Infof("start handle cmdb resource(%s) event", cacheType)
			defer logger.Infof("end handle cmdb resource(%s) event", cacheType)

			for {
				tn := time.Now()
				// 事件处理
				handler.Handle(cancelCtx)

				// 事件处理间隔时间
				select {
				case <-cancelCtx.Done():
					return
				case <-time.After(eventHandleInterval - time.Now().Sub(tn)):
				}
			}
		}()
	}

	wg.Wait()
	return nil
}
