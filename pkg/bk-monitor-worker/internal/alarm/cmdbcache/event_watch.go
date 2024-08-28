// MIT License

// Copyright (c) 2021~2024 腾讯蓝鲸

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

// CmdbResourceTypes cmdb资源类型
var CmdbResourceTypes = []CmdbResourceType{
	CmdbResourceTypeHost,
	CmdbResourceTypeHostRelation,
	CmdbResourceTypeBiz,
	CmdbResourceTypeSet,
	CmdbResourceTypeModule,
	CmdbResourceTypeMainlineInstance,
	CmdbResourceTypeProcess,
}

// CmdbResourceTypeFields cmdb资源类型对应的监听字段
var CmdbResourceTypeFields = map[CmdbResourceType][]string{
	CmdbResourceTypeHost:   {"bk_host_id", "bk_host_innerip", "bk_cloud_id", "bk_agent_id"},
	CmdbResourceTypeBiz:    {"bk_biz_id"},
	CmdbResourceTypeSet:    {"bk_biz_id", "bk_set_id", "set_template_id"},
	CmdbResourceTypeModule: {"bk_module_id", "bk_biz_id", "service_template_id"},
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
	cmdbApi := getCmdbApi()

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
		"bk_resource":         resourceType,
		"bk_supplier_account": "0",
	}

	// 获取资源变更事件游标
	bkCursor := w.getBkCursor(ctx, resourceType)
	if bkCursor != "" {
		params["bk_cursor"] = bkCursor
	}

	// 补充bk_fields参数
	if fields, ok := CmdbResourceTypeFields[resourceType]; ok {
		params["bk_fields"] = fields
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
		_ = fmt.Sprintf("%s", val)
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
	for _, resourceType := range CmdbResourceTypes {
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
