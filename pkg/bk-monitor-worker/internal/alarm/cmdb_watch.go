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

package alarm

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/cache"
	redis2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
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
)

var CmdbResourceTypeFields = map[CmdbResourceType][]string{
	CmdbResourceTypeHost:             {"bk_host_id", "bk_host_innerip", "bk_cloud_id", "bk_agent_id"},
	CmdbResourceTypeHostRelation:     {"bk_host_id"},
	CmdbResourceTypeBiz:              {"bk_biz_id", "bk_biz_name", "bk_biz_maintainer", "bk_biz_tester", "bk_biz_productor", "bk_biz_developer", "operator", "time_zone", "language", "life_cycle"},
	CmdbResourceTypeSet:              {"bk_biz_id", "bk_set_id", "bk_set_name", "bk_set_env", "bk_set_desc", "set_template_id", "bk_service_status", "description"},
	CmdbResourceTypeModule:           {"bk_module_id", "bk_module_name", "operator", "bk_biz_id", "bk_set_id", "bk_module_status", "service_template_id", "bk_bak_operator", "service_category_id", "set_template_id"},
	CmdbResourceTypeMainlineInstance: {"bk_obj_id", "bk_inst_id", "bk_obj_name", "bk_inst_name"},
}

// CmdbWatchRunner cmdb资源事件执行接口，支持
type CmdbWatchRunner interface {
	// CleanByEvents 清理缓存
	CleanByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error

	// UpdateByEvents 更新缓存
	UpdateByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error
}

// WatchCmdbResourceChangeTaskParams 监听cmdb资源变更任务参数
type WatchCmdbResourceChangeTaskParams struct {
	Redis redis2.RedisOptions `json:"redis" mapstructure:"redis"`
}

// CmdbResourceWatcher cmdb资源监听器
type CmdbResourceWatcher struct {
	prefix      string
	redisClient redis.UniversalClient
	redisOpt    *redis2.RedisOptions
	cmdbApi     *cmdb.Client

	bkCursorLock sync.Mutex
	bkCursors    map[CmdbResourceType]string
}

// NewCmdbResourceWatcher 创建cmdb资源监听器
func NewCmdbResourceWatcher(prefix string, opt *WatchCmdbResourceChangeTaskParams) (*CmdbResourceWatcher, error) {
	redisClient, err := redis2.GetRedisClient(&opt.Redis)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create redis client")
	}

	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return nil, errors.Wrap(err, "failed to create cmdb api client")
	}

	return &CmdbResourceWatcher{
		prefix:      prefix,
		redisClient: redisClient,
		cmdbApi:     cmdbApi,
		bkCursors:   make(map[CmdbResourceType]string),
	}, nil

}

// getBkCursor 获取cmdb资源变更游标
func (w *CmdbResourceWatcher) getBkCursor(ctx context.Context, resourceType CmdbResourceType) string {
	w.bkCursorLock.Lock()
	defer w.bkCursorLock.Unlock()

	// 从内存中获取cmdb资源变更游标
	bkCursor, ok := w.bkCursors[resourceType]
	if ok {
		return bkCursor
	}

	// 从redis中获取cmdb资源变更游标
	bkCursorKey := fmt.Sprintf("%s.cmdb_resource_watch_cursor.%s", w.prefix, resourceType)
	bkCursorResult := w.redisClient.Get(ctx, bkCursorKey)
	if bkCursorResult.Err() != nil {
		if !errors.Is(bkCursorResult.Err(), redis.Nil) {
			return ""
		}
	}

	// 更新内存中cmdb资源变更游标
	if bkCursorResult.Val() != "" {
		w.bkCursors[resourceType] = bkCursorResult.Val()
		return w.bkCursors[resourceType]
	}

	return ""
}

// setBkCursor 设置cmdb资源变更游标
func (w *CmdbResourceWatcher) setBkCursor(ctx context.Context, resourceType CmdbResourceType, cursor string) error {
	w.bkCursorLock.Lock()
	defer w.bkCursorLock.Unlock()

	// 设置cmdb资源变更游标
	bkCursorKey := fmt.Sprintf("%s.cmdb_resource_watch_cursor.%s", w.prefix, resourceType)
	if _, err := w.redisClient.Set(ctx, bkCursorKey, cursor, time.Hour).Result(); err != nil {
		return errors.Wrap(err, "set cmdb resource watch cursor error")
	}

	// 更新内存中cmdb资源变更游标
	w.bkCursors[resourceType] = cursor
	return nil
}

// setCmdbResourceEvent 记录cmdb资源变更事件
func (w *CmdbResourceWatcher) setCmdbResourceEvent(ctx context.Context, resourceType CmdbResourceType, resourceId string, eventType string) error {
	bkEventKey := fmt.Sprintf("%s.cmdb_resource_watch_event.%s", w.prefix, resourceType)

	// 记录事件类型和时间戳
	value := fmt.Sprintf("%s:%d", eventType, time.Now().Unix())
	if _, err := w.redisClient.HSet(ctx, bkEventKey, resourceId, value).Result(); err != nil {
		return err
	}
	return nil
}

// Watch 监听cmdb资源变更并记录事件
func (w *CmdbResourceWatcher) Watch(ctx context.Context, resourceType CmdbResourceType) (bool, error) {
	params := map[string]interface{}{
		"bk_fields":           CmdbResourceTypeFields[resourceType],
		"bk_resource":         resourceType,
		"bk_supplier_account": "0",
	}

	// 获取cmdb资源变更游标
	bkCursor := w.getBkCursor(ctx, resourceType)
	if bkCursor != "" {
		params["bk_cursor"] = bkCursor
	} else {
		params["bk_start_from"] = time.Now().Unix()
	}

	// 请求cmdb资源变更事件API
	var resp cmdb.ResourceWatchResp
	_, err := w.cmdbApi.ResourceWatch().SetContext(ctx).SetBody(params).SetResult(&resp).Request()
	err = api.HandleApiResultError(resp.ApiCommonRespMeta, err, "watch cmdb resource api failed")
	if err != nil {
		return false, err
	}

	// 无变更事件
	if !resp.Data.BkWatched {
		if len(resp.Data.BkEvents) == 0 {
			return false, nil
		}
		// 无变更事件，但有游标
		err := w.setBkCursor(ctx, resourceType, resp.Data.BkEvents[0].BkCursor)
		if err != nil {
			logger.Error("set cmdb resource watch cursor error: %v", err)
		}

		return false, nil
	}

	// 处理cmdb资源变更事件
	events := make([]string, 0)
	for _, event := range resp.Data.BkEvents {
		val, _ := json.Marshal(event)
		events = append(events, string(val))
	}

	// 记录cmdb资源变更事件
	bkEventKey := fmt.Sprintf("%s.cmdb_resource_watch_event.%s", w.prefix, resourceType)
	w.redisClient.RPush(ctx, bkEventKey, events)

	// 设置cmdb资源变更游标
	if len(resp.Data.BkEvents) > 0 {
		err = w.setBkCursor(ctx, resourceType, resp.Data.BkEvents[len(resp.Data.BkEvents)-1].BkCursor)
		if err != nil {
			logger.Error("set cmdb resource watch cursor error: %v", err)
		}
	}

	return true, nil
}

// Handle cmdb资源变更事件处理
func (w *CmdbResourceWatcher) Handle(ctx context.Context, resourceType CmdbResourceType) error {
	rt := string(resourceType)
	// 获取cmdb资源变更事件
	// todo: 加锁
	bkEventKey := fmt.Sprintf("%s.cmdb_resource_watch_event.%s", w.prefix, rt)
	result := w.redisClient.LRange(ctx, bkEventKey, 0, -1)
	w.redisClient.Del(ctx, bkEventKey)

	if result.Err() != nil {
		if !errors.Is(result.Err(), redis.Nil) {
			return errors.Wrap(result.Err(), "get cmdb resource watch event error")
		}
	}
	// 无cmdb资源变更事件
	if result.Val() == nil {
		return nil
	}

	// 提取需要处理的资源ID
	needUpdateDetails := make([]map[string]interface{}, 0)
	needDeleteDetails := make([]map[string]interface{}, 0)
	var event cmdb.ResourceWatchEvent
	for _, eventStr := range result.Val() {
		err := json.Unmarshal([]byte(eventStr), &event)
		if err != nil {
			return errors.Wrap(err, "unmarshal cmdb resource watch event error")
		}

		switch event.BkEventType {
		case "create", "update":
			needUpdateDetails = append(needUpdateDetails, event.BkDetail)
		case "delete":
			needDeleteDetails = append(needDeleteDetails, event.BkDetail)
		}
	}

	// 创建处理器
	hostCacheManager, _ := cache.NewHostAndTopoCacheManager(w.prefix, w.redisOpt)
	runners := map[string]CmdbWatchRunner{
		"host_topo": hostCacheManager,
	}

	// 处理cmdb资源变更事件
	for _, runner := range runners {
		// 清理缓存
		err := runner.CleanByEvents(ctx, rt, needDeleteDetails)
		if err != nil {
			return errors.Wrap(err, fmt.Sprintf("clean cmdb resource(%s) watch event error by %v", rt, runner))
		}

		err = runner.UpdateByEvents(ctx, rt, needUpdateDetails)
		if err != nil {
			return errors.Wrap(err, fmt.Sprintf("update cmdb resource(%s) watch event error by %v", rt, runner))
		}
	}

	return nil
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

		// 启动处理任务
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			ticker := time.NewTicker(time.Minute * 3)
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					err := w.Handle(ctx, resourceType)
					if err != nil {
						logger.Errorf("handle cmdb resource(%s) watch event error: %v", resourceType, err)
					}
				}
			}
		}()
	}

	// 等待所有cmdb资源监听任务退出
	waitGroup.Wait()
	logger.Info("watch cmdb resource exit")
}
