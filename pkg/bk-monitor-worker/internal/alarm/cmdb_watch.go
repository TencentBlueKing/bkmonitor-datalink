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
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/pkg/errors"

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

var CmdbResourceTypeMap = map[CmdbResourceType][]string{
	CmdbResourceTypeHost:             {"bk_host_id"},
	CmdbResourceTypeHostRelation:     {"bk_host_id"},
	CmdbResourceTypeBiz:              {"bk_biz_id"},
	CmdbResourceTypeSet:              {"bk_set_id"},
	CmdbResourceTypeModule:           {"bk_module_id"},
	CmdbResourceTypeMainlineInstance: {"bk_obj_id", "bk_inst_id"},
}

// WatchCmdbResourceChangeTaskParams 监听cmdb资源变更任务参数
type WatchCmdbResourceChangeTaskParams struct {
	Redis RedisOptions `json:"redis" mapstructure:"redis"`
}

// CmdbResourceWatcher cmdb资源监听器
type CmdbResourceWatcher struct {
	prefix      string
	redisClient redis.UniversalClient
	cmdbApi     *cmdb.Client

	bkCursorLock sync.Mutex
	bkCursors    map[CmdbResourceType]string
}

// NewCmdbResourceWatcher 创建cmdb资源监听器
func NewCmdbResourceWatcher(prefix string, opt *WatchCmdbResourceChangeTaskParams) (*CmdbResourceWatcher, error) {
	redisClient, err := GetRedisClient(&opt.Redis)
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

// GetBkCursor 获取cmdb资源变更游标
func (w *CmdbResourceWatcher) GetBkCursor(ctx context.Context, resourceType CmdbResourceType) string {
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

// SetBkCursor 设置cmdb资源变更游标
func (w *CmdbResourceWatcher) SetBkCursor(ctx context.Context, resourceType CmdbResourceType, cursor string) error {
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

// SetCmdbResourceEvent 记录cmdb资源变更事件
func (w *CmdbResourceWatcher) SetCmdbResourceEvent(ctx context.Context, resourceType CmdbResourceType, resourceId string, eventType string) error {
	bkEventKey := fmt.Sprintf("%s.cmdb_resource_watch_event.%s", w.prefix, resourceType)

	// 记录事件类型和时间戳
	value := fmt.Sprintf("%s:%d", eventType, time.Now().Unix())
	if _, err := w.redisClient.HSet(ctx, bkEventKey, resourceId, value).Result(); err != nil {
		return err
	}
	return nil
}

// Watch 监听cmdb资源变更
func (w *CmdbResourceWatcher) Watch(ctx context.Context, resourceType CmdbResourceType) error {
	params := map[string]interface{}{
		"bk_fields":           CmdbResourceTypeMap[resourceType],
		"bk_resource":         resourceType,
		"bk_supplier_account": "0",
	}

	// 获取cmdb资源变更游标
	bkCursor := w.GetBkCursor(ctx, resourceType)
	if bkCursor != "" {
		params["bk_cursor"] = bkCursor
	}

	// 请求cmdb资源变更事件API
	var resp cmdb.ResourceWatchResp
	_, err := w.cmdbApi.ResourceWatch().SetContext(ctx).SetBody(params).SetResult(&resp).Request()
	err = api.HandleApiResultError(resp.ApiCommonRespMeta, err, "watch cmdb resource api failed")
	if err != nil {
		return err
	}

	// 无变更事件
	if !resp.Data.BkWatched {
		if len(resp.Data.BkEvents) == 0 {
			return nil
		}
		// 无变更事件，但有游标
		err := w.SetBkCursor(ctx, resourceType, resp.Data.BkEvents[0].BkCursor)
		if err != nil {
			logger.Error("set cmdb resource watch cursor error: %v", err)
		}

		return nil
	}

	// 处理cmdb资源变更事件
	for _, event := range resp.Data.BkEvents {
		// 更新cmdb资源变更游标
		err := w.SetBkCursor(ctx, resourceType, event.BkCursor)
		if err != nil {
			logger.Error("set cmdb resource watch cursor error: %v", err)
		}

		// 提取资源ID
		var resourceIds []string
		for _, field := range CmdbResourceTypeMap[resourceType] {
			val, ok := event.BkDetail[field]
			if !ok || val == nil {
				resourceIds = nil
				break
			}
			switch val.(type) {
			case float64:
				resourceIds = append(resourceIds, fmt.Sprintf("%d", int(val.(float64))))
			case string:
				resourceIds = append(resourceIds, val.(string))
			default:
				continue
			}
		}

		if len(resourceIds) == 0 {
			continue
		}

		// cmdb资源变更事件记录
		err = w.SetCmdbResourceEvent(ctx, resourceType, strings.Join(resourceIds, "|"), event.BkEventType)
		if err != nil {
			logger.Error("set cmdb resource watch event error: %v", err)
		}
	}
	return nil
}

// Run 启动cmdb资源监听任务
func (w *CmdbResourceWatcher) Run(ctx context.Context) {
	waitGroup := sync.WaitGroup{}
	logger.Info("start watch cmdb resource")

	// 启动cmdb资源监听
	for resourceType := range CmdbResourceTypeMap {
		waitGroup.Add(1)
		resourceType := resourceType
		go func() {
			defer waitGroup.Done()

			for {
				select {
				case <-ctx.Done():
					return
				default:
					err := w.Watch(ctx, resourceType)
					if err != nil {
						logger.Errorf("watch cmdb resource(%s) error: %v", resourceType, err)
						return
					}
				}
			}
		}()
	}

	// 等待所有cmdb资源监听任务退出
	waitGroup.Wait()
	logger.Info("watch cmdb resource exit")
}
