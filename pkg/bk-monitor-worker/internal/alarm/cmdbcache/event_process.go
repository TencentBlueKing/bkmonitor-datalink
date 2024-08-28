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
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
)

// CmdbEventHandler cmdb资源变更事件处理器
type CmdbEventHandler struct {
	// 缓存key前缀
	prefix string

	// redis client
	redisClient redis.UniversalClient

	// 缓存管理器
	cacheManagers []Manager

	// 全量刷新间隔时间
	fullRefreshIntervals map[string]time.Duration

	// 预处理结果
	// 是否刷新业务列表
	refreshBiz bool
	// 待刷新主机拓扑业务列表
	refreshBizHostTopo sync.Map
	// 待清理主机相关key
	cleanHostKeys sync.Map
	// 待刷新服务实例业务列表
	refreshBizServiceInstance sync.Map
	// 待清理服务实例相关key
	cleanServiceInstanceKeys sync.Map
	// 待更新拓扑节点
	refreshTopoNode sync.Map
	// 待删除拓扑节点
	cleanTopoNode sync.Map
	// 待刷新动态分组业务列表
	refreshBizDynamicGroup sync.Map
	// 待刷新集群业务列表
	refreshBizSet sync.Map
	// 待清理集群相关key
	cleanSetKeys sync.Map
	// 待刷新模块业务列表
	refreshBizModule sync.Map
	// 待清理模块相关key
	cleanModuleKeys sync.Map
}

// NewCmdbEventHandler 创建cmdb资源变更事件处理器
func NewCmdbEventHandler(prefix string, rOpt *redis.Options, cacheTypes []string, fullRefreshIntervals map[string]time.Duration, concurrentLimit int) (*CmdbEventHandler, error) {
	// 创建redis client
	redisClient, err := redis.GetClient(rOpt)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create redis client")
	}

	// 创建缓存管理器
	cacheManagers := make([]Manager, 0, len(cacheTypes))
	for _, cacheType := range cacheTypes {
		cacheManager, err := NewCacheManagerByType(rOpt, prefix, cacheType, concurrentLimit)
		if err != nil {
			return nil, errors.Wrap(err, "new cache Manager failed")
		}
		cacheManagers = append(cacheManagers, cacheManager)
	}

	return &CmdbEventHandler{
		prefix:               prefix,
		redisClient:          redisClient,
		cacheManagers:        cacheManagers,
		fullRefreshIntervals: fullRefreshIntervals,
	}, nil
}

// Close 关闭操作
func (h *CmdbEventHandler) Close() {
	GetRelationMetricsBuilder().ClearAllMetrics()
}

// getEvents 获取资源变更事件
func (h *CmdbEventHandler) getEvents(ctx context.Context, resourceType CmdbResourceType) ([]cmdb.ResourceWatchEvent, error) {
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

// resetPreprocessResults 重置预处理结果
func (h *CmdbEventHandler) resetPreprocessResults() {
	h.refreshBiz = false
	h.refreshBizHostTopo = sync.Map{}
	h.cleanHostKeys = sync.Map{}
	h.refreshBizServiceInstance = sync.Map{}
	h.cleanServiceInstanceKeys = sync.Map{}
	h.refreshTopoNode = sync.Map{}
	h.cleanTopoNode = sync.Map{}
	h.refreshBizDynamicGroup = sync.Map{}
	h.refreshBizSet = sync.Map{}
	h.cleanSetKeys = sync.Map{}
	h.refreshBizModule = sync.Map{}
	h.cleanModuleKeys = sync.Map{}
}

// preprocessEvents 预处理资源变更事件
func (h *CmdbEventHandler) preprocessEvents(ctx context.Context, resourceType CmdbResourceType, events []cmdb.ResourceWatchEvent) error {
	var host *AlarmHostInfo
	hosts := make(map[int]*AlarmHostInfo)

	for _, event := range events {
		// 尝试获取主机信息
		bkHostId, ok := event.BkDetail["bk_host_id"].(float64)
		if ok {
			host, ok = hosts[int(bkHostId)]
			if !ok {
				result := h.redisClient.HGet(ctx, fmt.Sprintf("%s.%s", h.prefix, hostCacheKey), strconv.Itoa(int(bkHostId)))
				if result.Err() != nil {
					if !errors.Is(result.Err(), redis.Nil) {
						logger.Errorf("get host(%d) info error: %v", int(bkHostId), result.Err())
					}
				} else {
					err := json.Unmarshal([]byte(result.Val()), &host)
					if err != nil {
						logger.Errorf("unmarshal host(%d) info error: %v", int(bkHostId), err)
					} else {
						hosts[int(bkHostId)] = host
					}
				}
			}
		} else {
			host = nil
		}

		switch resourceType {
		case CmdbResourceTypeBiz:
			// 如果是业务事件，将刷新业务标志置为true
			h.refreshBiz = true
		case CmdbResourceTypeSet:
			bizId, ok1 := event.BkDetail["bk_biz_id"].(float64)
			bkSetId, ok2 := event.BkDetail["bk_set_id"].(float64)
			if !ok1 || !ok2 {
				continue
			}
			h.refreshBizSet.Store(int(bizId), struct{}{})

			// 如果是删除事件，将集群ID加入待清理列表
			if event.BkEventType == "delete" {
				h.cleanSetKeys.Store(int(bkSetId), struct{}{})
			}
		case CmdbResourceTypeModule:
			bizId, ok1 := event.BkDetail["bk_biz_id"].(float64)
			bkModuleId, ok2 := event.BkDetail["bk_module_id"].(float64)
			if !ok1 || !ok2 {
				continue
			}
			h.refreshBizModule.Store(int(bizId), struct{}{})

			// 如果是删除事件，将模块ID加入待清理列表
			if event.BkEventType == "delete" {
				h.cleanModuleKeys.Store(int(bkModuleId), struct{}{})
			}
		case CmdbResourceTypeHost:
			// todo: implement this
			continue
		case CmdbResourceTypeHostRelation:
			bkBizId, ok := event.BkDetail["bk_biz_id"].(float64)
			if !ok {
				continue
			}

			// 如果拉不到主机信息，直接刷新业务并清理主机ID
			if host == nil {
				h.refreshBizHostTopo.Store(int(bkBizId), struct{}{})
				h.cleanHostKeys.Store(int(bkHostId), struct{}{})
				continue
			}

			// 尝试将主机关联字段加入待清理列表，如果刷新业务时发现这些字段不存在，将会进行清理
			if host.BkAgentId != "" {
				h.cleanHostKeys.Store(host.BkAgentId, struct{}{})
			}
			if host.BkHostInnerip != "" {
				h.cleanHostKeys.Store(fmt.Sprintf("%s|%d", host.BkHostInnerip, host.BkCloudId), struct{}{})
			}

			if event.BkEventType == "delete" || host.BkBizId != int(bkBizId) {
				// 如果是删除事件，将主机ID加入待清理列表
				h.cleanHostKeys.Store(int(bkHostId), struct{}{})
				// 如果是删除事件，将业务ID加入待刷新列表
				h.refreshBizHostTopo.Store(host.BkBizId, struct{}{})
				h.refreshBizHostTopo.Store(int(bkBizId), struct{}{})
			} else {
				// 如果是更新事件，将业务ID加入待刷新列表
				h.refreshBizHostTopo.Store(int(bkBizId), struct{}{})
			}
		case CmdbResourceTypeMainlineInstance:
			bkObjId := event.BkDetail["bk_obj_id"].(string)
			bkInstId, ok := event.BkDetail["bk_inst_id"].(float64)
			if !ok {
				continue
			}
			topoNodeKey := fmt.Sprintf("%s.%d", bkObjId, int(bkInstId))
			if event.BkEventType == "delete" {
				// 如果是删除事件，将拓扑节点ID加入待清理列表
				h.cleanTopoNode.Store(topoNodeKey, struct{}{})
			} else {
				// 如果是更新事件，将拓扑节点ID加入待刷新列表
				topo := map[string]interface{}{
					"bk_inst_id":   int(bkInstId),
					"bk_inst_name": event.BkDetail["bk_inst_name"],
					"bk_obj_id":    bkObjId,
					"bk_obj_name":  event.BkDetail["bk_obj_name"],
				}
				value, _ := json.Marshal(topo)
				h.refreshTopoNode.Store(topoNodeKey, string(value))
			}
		case CmdbResourceTypeProcess:
			serviceInstanceId, ok1 := event.BkDetail["service_instance_id"].(float64)
			bkBizId, ok2 := event.BkDetail["bk_biz_id"].(float64)
			if !ok1 || !ok2 {
				continue
			}

			if event.BkEventType == "delete" {
				// 如果是删除事件，将服务实例ID加入待清理列表
				h.cleanServiceInstanceKeys.Store(int(serviceInstanceId), struct{}{})
			} else {
				// 如果是更新事件，将业务ID加入待刷新列表
				h.refreshBizServiceInstance.Store(int(bkBizId), struct{}{})
			}
		}
	}
	return nil
}

// refreshEvents 刷新资源变更事件
func (h *CmdbEventHandler) refreshEvents(ctx context.Context) error {
	// todo: implement this
	return nil
}

// getFullRefreshInterval 获取全量刷新间隔时间
func (h *CmdbEventHandler) getFullRefreshInterval(cacheType string) time.Duration {
	fullRefreshInterval, ok := h.fullRefreshIntervals[cacheType]
	// 最低600秒的间隔
	if !ok {
		fullRefreshInterval = time.Second * 300
	}
	return fullRefreshInterval
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
	if time.Now().Unix()-lastUpdateTimestamp > int64(h.getFullRefreshInterval(cacheType).Seconds()) {
		return true
	}

	return false
}

// Run 处理cmdb资源变更事件
// 1. 遍历所有缓存类型，如果超过全量刷新间隔时间，先执行全量刷新
// 2. 从缓存中获取资源变更并进行预处理
// 3. 根据预处理结果，执行缓存变更动作
func (h *CmdbEventHandler) Run(ctx context.Context) {
	wg := sync.WaitGroup{}

	// 如果超过全量刷新间隔时间，先执行全量刷新
	for _, cacheManager := range h.cacheManagers {
		wg.Add(1)

		cacheManager := cacheManager
		go func() {
			defer wg.Done()

			if h.ifRunRefreshAll(ctx, cacheManager.Type()) {
				// 全量刷新
				err := RefreshAll(ctx, cacheManager, cacheManager.GetConcurrentLimit())
				if err != nil {
					logger.Errorf("refresh all cache failed: %v", err)
				}

				logger.Infof("refresh all cmdb resource(%s) cache", cacheManager.Type())

				// 记录全量刷新时间
				lastUpdateTimeKey := fmt.Sprintf("%s.cmdb_last_refresh_all_time.%s", h.prefix, cacheManager.Type())
				_, err = h.redisClient.Set(ctx, lastUpdateTimeKey, strconv.FormatInt(time.Now().Unix(), 10), 24*time.Hour).Result()
				if err != nil {
					logger.Errorf("set last update time error: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	// 重置预处理结果
	h.resetPreprocessResults()

	// 从缓存中获取资源变更并进行预处理
	for _, resourceType := range CmdbResourceTypes {
		wg.Add(1)
		resourceType := resourceType
		go func() {
			defer wg.Done()

			// 获取资源变更事件
			events, err := h.getEvents(ctx, resourceType)
			if err != nil {
				logger.Errorf("get cmdb resource(%s) watch event error: %v", resourceType, err)
				return
			}

			// 预处理资源变更事件
			err = h.preprocessEvents(ctx, resourceType, events)
			if err != nil {
				logger.Errorf("preprocess cmdb resource(%s) watch event error: %v", resourceType, err)
			}
		}()
	}
	wg.Wait()

	// 根据预处理结果，执行缓存变更动作
	err := h.refreshEvents(ctx)
	if err != nil {
		logger.Errorf("refresh cmdb resource event error: %v", err)
	}
}
