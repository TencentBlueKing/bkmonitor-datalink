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

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const DefaultFullRefreshInterval = time.Second * 600

// CmdbEventHandler cmdb资源变更事件处理器
type CmdbEventHandler struct {
	// 缓存key前缀
	prefix string

	// redis client
	redisClient redis.UniversalClient

	// 全量刷新间隔时间
	fullRefreshIntervals map[string]time.Duration

	// 全量刷新间隔时间
	concurrentLimit int

	// 缓存管理器
	cacheManagers map[string]Manager

	// 预处理结果
	// 是否刷新业务列表
	refreshBiz bool
	// 待刷新主机拓扑业务列表
	refreshBizHostTopo sync.Map
	// 待清理主机相关key
	cleanHostKeys sync.Map
	// 待清理AgentId相关key
	cleanAgentIdKeys sync.Map

	// 待更新拓扑节点
	refreshTopoNode sync.Map
	// 待删除拓扑节点
	cleanTopoNode sync.Map

	// 待刷新服务实例业务列表
	refreshBizServiceInstance sync.Map
	// 待清理服务实例相关key
	cleanServiceInstanceKeys sync.Map

	// 待刷新集群业务列表
	refreshBizSet sync.Map
	// 待清理集群相关key
	cleanSetKeys sync.Map
	// 待清理集群模板相关key
	cleanSetTemplateIds sync.Map

	// 待刷新模块业务列表
	refreshBizModule sync.Map
	// 待清理模块相关key
	cleanModuleKeys sync.Map
	// 待清理服务模板相关key
	cleanServiceTemplateIds sync.Map
}

// NewCmdbEventHandler 创建cmdb资源变更事件处理器
func NewCmdbEventHandler(prefix string, rOpt *redis.Options, fullRefreshIntervals map[string]time.Duration, concurrentLimit int) (*CmdbEventHandler, error) {
	// 创建redis client
	redisClient, err := redis.GetClient(rOpt)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create redis client")
	}

	// 创建缓存管理器
	cacheManagers := make(map[string]Manager)
	for _, cacheType := range cmdbCacheTypes {
		cacheManager, err := NewCacheManagerByType(rOpt, prefix, cacheType, concurrentLimit)
		if err != nil {
			return nil, errors.Wrap(err, "new cache Manager failed")
		}
		cacheManagers[cacheType] = cacheManager
	}

	return &CmdbEventHandler{
		prefix:               prefix,
		redisClient:          redisClient,
		fullRefreshIntervals: fullRefreshIntervals,
		concurrentLimit:      concurrentLimit,
		cacheManagers:        cacheManagers,
	}, nil
}

// Close 关闭操作
func (h *CmdbEventHandler) Close() {
	GetRelationMetricsBuilder().ClearAllMetrics()
}

// getEventKey 获取资源变更事件key
func (h *CmdbEventHandler) getEventKey(resourceType CmdbResourceType) string {
	return fmt.Sprintf("%s.cmdb_resource_watch_event.%s", h.prefix, resourceType)
}

// getEvents 获取资源变更事件
func (h *CmdbEventHandler) getEvents(ctx context.Context, resourceType CmdbResourceType) ([]cmdb.ResourceWatchEvent, error) {
	// 从redis中获取该资源类型的所有事件
	eventStrings := make([]string, 0)
	for {
		result, err := h.redisClient.LPop(ctx, h.getEventKey(resourceType)).Result()
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
	h.cleanAgentIdKeys = sync.Map{}
	h.refreshBizServiceInstance = sync.Map{}
	h.cleanServiceInstanceKeys = sync.Map{}
	h.refreshTopoNode = sync.Map{}
	h.cleanTopoNode = sync.Map{}
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

			// 将业务ID加入待刷新列表
			h.refreshBizSet.Store(int(bizId), struct{}{})

			// 如果是删除事件，将集群ID加入待清理列表
			if event.BkEventType == "delete" {
				h.cleanSetKeys.Store(int(bkSetId), struct{}{})
				setTemplateId, _ := event.BkDetail["set_template_id"].(float64)
				if int(setTemplateId) != 0 {
					h.cleanSetTemplateIds.Store(int(setTemplateId), struct{}{})
				}
			}
		case CmdbResourceTypeModule:
			bizId, ok1 := event.BkDetail["bk_biz_id"].(float64)
			bkModuleId, ok2 := event.BkDetail["bk_module_id"].(float64)
			if !ok1 || !ok2 {
				continue
			}

			// 将业务ID加入待刷新列表
			h.refreshBizModule.Store(int(bizId), struct{}{})

			// 如果是删除事件，将模块ID加入待清理列表
			if event.BkEventType == "delete" {
				h.cleanModuleKeys.Store(int(bkModuleId), struct{}{})
				serviceInstanceTemplateId, _ := event.BkDetail["service_template_id"].(float64)
				if int(serviceInstanceTemplateId) != 0 {
					h.cleanServiceTemplateIds.Store(int(serviceInstanceTemplateId), struct{}{})
				}
			}
		case CmdbResourceTypeHost:
			ip, _ := event.BkDetail["bk_host_innerip"].(string)
			cloudId, _ := event.BkDetail["bk_cloud_id"].(float64)
			agentId, _ := event.BkDetail["bk_agent_id"].(string)

			if event.BkEventType == "create" {
				continue
			}

			if event.BkEventType == "delete" {
				// 如果是删除事件，将主机ID加入待清理列表
				h.cleanHostKeys.Store(strconv.Itoa(int(bkHostId)), struct{}{})
			}

			// 尝试将主机关联字段加入待清理列表，如果刷新业务时发现这些字段不存在，将会进行清理
			if ip != "" {
				h.cleanHostKeys.Store(fmt.Sprintf("%s|%d", ip, int(cloudId)), struct{}{})
			}
			if agentId != "" {
				h.cleanAgentIdKeys.Store(agentId, struct{}{})
			}

			// 将主机所属业务加入待刷新列表
			if host != nil {
				h.refreshBizHostTopo.Store(host.BkBizId, struct{}{})
				if host.BkAgentId != "" {
					h.cleanAgentIdKeys.Store(host.BkAgentId, struct{}{})
				}
				if host.BkHostInnerip != "" {
					h.cleanHostKeys.Store(fmt.Sprintf("%s|%d", host.BkHostInnerip, host.BkCloudId), struct{}{})
				}
			}
		case CmdbResourceTypeHostRelation:
			bkBizId, ok := event.BkDetail["bk_biz_id"].(float64)
			if !ok {
				continue
			}

			// 将主机所属业务加入待刷新列表
			h.refreshBizHostTopo.Store(int(bkBizId), struct{}{})

			// 如果是删除事件或业务与主机业务不一致，将主机相关加入待清理列表
			if event.BkEventType == "delete" || (host != nil && host.BkBizId != int(bkBizId)) {
				// 如果是删除事件，将主机相关加入待清理列表
				h.cleanHostKeys.Store(strconv.Itoa(int(bkHostId)), struct{}{})

				if host != nil {
					if host.BkAgentId != "" {
						h.cleanAgentIdKeys.Store(host.BkAgentId, struct{}{})
					}
					if host.BkHostInnerip != "" {
						h.cleanHostKeys.Store(fmt.Sprintf("%s|%d", host.BkHostInnerip, host.BkCloudId), struct{}{})
					}
					// 如果是删除事件，将业务ID加入待刷新列表
					h.refreshBizHostTopo.Store(host.BkBizId, struct{}{})
				}

			}
		case CmdbResourceTypeMainlineInstance:
			bkObjId := event.BkDetail["bk_obj_id"].(string)
			bkInstId, ok := event.BkDetail["bk_inst_id"].(float64)
			if !ok {
				continue
			}
			topoNodeKey := fmt.Sprintf("%s|%d", bkObjId, int(bkInstId))
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
func (h *CmdbEventHandler) refreshByEvents(ctx context.Context) error {
	wg := sync.WaitGroup{}

	// 刷新业务列表
	if h.refreshBiz {
		businessCacheManager := h.cacheManagers["business"]
		wg.Add(1)
		go func() {
			defer wg.Done()

			err := RefreshAll(ctx, businessCacheManager, h.concurrentLimit)
			if err != nil {
				logger.Errorf("refresh all business cache by event failed: %v", err)
			} else {
				logger.Infof("refresh all business cache by event success")
			}
		}()

		// 重置
		businessCacheManager.Reset()
	}

	// 刷新主机拓扑业务列表
	hostTopoBizIds := make([]int, 0)
	h.refreshBizHostTopo.Range(func(key, value interface{}) bool {
		bizId, _ := key.(int)
		hostTopoBizIds = append(hostTopoBizIds, bizId)
		return true
	})
	if len(hostTopoBizIds) > 0 {
		hostTopoCacheManager := h.cacheManagers["host_topo"]

		wg.Add(1)
		go func() {
			defer wg.Done()

			// 刷新主机拓扑缓存
			if err := RefreshByBizIds(ctx, hostTopoCacheManager, hostTopoBizIds, h.concurrentLimit); err != nil {
				logger.Errorf("refresh host topo cache by biz failed: %v", err)
				// 如果刷新不顺利，后续清理操作也不执行，否则可能会清理掉正常的缓存
				return
			} else {
				logger.Infof("refresh host topo cache by event success, biz count: %d", len(hostTopoBizIds))
			}

			// 清理hostCacheKey缓存
			cleanFields := make([]string, 0)
			h.cleanHostKeys.Range(func(key, value interface{}) bool {
				cleanFields = append(cleanFields, key.(string))
				return true
			})

			hostTopoCacheManager.CleanPartial(ctx, hostCacheKey, cleanFields)

			// 清理hostAgentIDCacheKey缓存
			cleanFields = make([]string, 0)
			h.cleanAgentIdKeys.Range(func(key, value interface{}) bool {
				cleanFields = append(cleanFields, key.(string))
				return true
			})
			hostTopoCacheManager.CleanPartial(ctx, hostAgentIDCacheKey, cleanFields)

			// 清理topoCacheKey缓存
			cleanFields = make([]string, 0)
			h.cleanTopoNode.Range(func(key, value interface{}) bool {
				cleanFields = append(cleanFields, key.(string))
				return true
			})
			hostTopoCacheManager.CleanPartial(ctx, topoCacheKey, cleanFields)

			// todo: 清理hostIpCacheKey缓存

			// 重置
			hostTopoCacheManager.Reset()
		}()

		// 刷新动态分组业务列表
		wg.Add(1)
		go func() {
			defer wg.Done()

			dynamicGroupCacheManager := h.cacheManagers["dynamic_group"]
			if err := RefreshByBizIds(ctx, dynamicGroupCacheManager, hostTopoBizIds, h.concurrentLimit); err != nil {
				logger.Errorf("refresh dynamic group cache by biz failed: %v", err)
			}

			// 重置
			dynamicGroupCacheManager.Reset()
		}()
	}

	// 刷新服务实例业务列表
	serviceInstanceBizIds := make([]int, 0)
	h.refreshBizServiceInstance.Range(func(key, value interface{}) bool {
		bizId, _ := key.(int)
		serviceInstanceBizIds = append(serviceInstanceBizIds, bizId)
		return true
	})
	h.refreshBizHostTopo.Range(func(key, value interface{}) bool {
		bizId, _ := key.(int)
		_, ok := h.refreshBizServiceInstance.Load(bizId)
		if !ok {
			serviceInstanceBizIds = append(serviceInstanceBizIds, bizId)
		}
		return true
	})
	if len(serviceInstanceBizIds) > 0 {
		serviceInstanceCacheManager := h.cacheManagers["service_instance"]

		wg.Add(1)
		go func() {
			defer wg.Done()

			// 刷新服务实例缓存
			if err := RefreshByBizIds(ctx, serviceInstanceCacheManager, serviceInstanceBizIds, h.concurrentLimit); err != nil {
				logger.Errorf("refresh service instance cache by biz failed: %v", err)
			}

			// 清理serviceInstanceCacheKey缓存
			cleanFields := make([]string, 0)
			h.cleanServiceInstanceKeys.Range(func(key, value interface{}) bool {
				cleanFields = append(cleanFields, strconv.Itoa(key.(int)))
				return true
			})
			serviceInstanceCacheManager.CleanPartial(ctx, serviceInstanceCacheKey, cleanFields)

			// 重置
			serviceInstanceCacheManager.Reset()
		}()
	}

	// 刷新集群业务列表
	setBizIds := make([]int, 0)
	h.refreshBizSet.Range(func(key, value interface{}) bool {
		bizId, _ := key.(int)
		setBizIds = append(setBizIds, bizId)
		return true
	})
	if len(setBizIds) > 0 {
		setCacheManager := h.cacheManagers["set"]

		wg.Add(1)
		go func() {
			defer wg.Done()

			// 刷新集群缓存
			if err := RefreshByBizIds(ctx, setCacheManager, setBizIds, h.concurrentLimit); err != nil {
				logger.Errorf("refresh set cache by biz failed: %v", err)
			}

			// 清理setCacheKey缓存
			cleanFields := make([]string, 0)
			h.cleanSetKeys.Range(func(key, value interface{}) bool {
				cleanFields = append(cleanFields, strconv.Itoa(key.(int)))
				return true
			})
			setCacheManager.CleanPartial(ctx, setCacheKey, cleanFields)

			// 清理setTemplateCacheKey缓存
			cleanFields = make([]string, 0)
			h.cleanSetTemplateIds.Range(func(key, value interface{}) bool {
				cleanFields = append(cleanFields, strconv.Itoa(key.(int)))
				return true
			})
			setCacheManager.CleanPartial(ctx, setTemplateCacheKey, cleanFields)

			// 重置
			setCacheManager.Reset()
		}()
	}

	// 刷新模块业务列表
	moduleBizIds := make([]int, 0)
	h.refreshBizModule.Range(func(key, value interface{}) bool {
		bizId, _ := key.(int)
		moduleBizIds = append(moduleBizIds, bizId)
		return true
	})
	if len(moduleBizIds) > 0 {
		moduleCacheManager := h.cacheManagers["module"]
		wg.Add(1)
		go func() {
			defer wg.Done()

			// 刷新模块缓存
			if err := RefreshByBizIds(ctx, moduleCacheManager, moduleBizIds, h.concurrentLimit); err != nil {
				logger.Errorf("refresh module cache by biz failed: %v", err)
			}

			// 清理moduleCacheKey缓存
			cleanFields := make([]string, 0)
			h.cleanModuleKeys.Range(func(key, value interface{}) bool {
				cleanFields = append(cleanFields, strconv.Itoa(key.(int)))
				return true
			})
			moduleCacheManager.CleanPartial(ctx, moduleCacheKey, cleanFields)

			// 清理serviceTemplateCacheKey缓存
			cleanFields = make([]string, 0)
			h.cleanServiceTemplateIds.Range(func(key, value interface{}) bool {
				cleanFields = append(cleanFields, strconv.Itoa(key.(int)))
				return true
			})
			moduleCacheManager.CleanPartial(ctx, serviceTemplateCacheKey, cleanFields)

			// 重置
			moduleCacheManager.Reset()
		}()
	}

	wg.Wait()

	return nil
}

// getLastUpdateTime 获取最后一次全量刷新时间
func (h *CmdbEventHandler) getLastUpdateTimeKey(cacheType string) string {
	return fmt.Sprintf("%s.cmdb_last_refresh_all_time.%s", h.prefix, cacheType)
}

// getFullRefreshInterval 获取全量刷新间隔时间
func (h *CmdbEventHandler) getFullRefreshInterval(cacheType string) time.Duration {
	fullRefreshInterval, ok := h.fullRefreshIntervals[cacheType]
	// 默认全量刷新间隔时间为10分钟
	if !ok {
		fullRefreshInterval = DefaultFullRefreshInterval
	}

	// 最低全量刷新间隔时间为1分钟
	if fullRefreshInterval < time.Minute {
		fullRefreshInterval = time.Minute
	}

	return fullRefreshInterval
}

// ifRunRefreshAll 判断是否执行全量刷新
func (h *CmdbEventHandler) ifRunRefreshAll(ctx context.Context, cacheType string, now int64) bool {
	// 获取最后一次全量刷新时间
	lastUpdateTime, err := h.redisClient.Get(ctx, h.getLastUpdateTimeKey(cacheType)).Result()
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
	if now-lastUpdateTimestamp > int64(h.getFullRefreshInterval(cacheType).Seconds()) {
		return true
	}

	return false
}

// runRefreshAll 判断所有的缓存类型，如果超过全量刷新间隔时间，先执行全量刷新
func (h *CmdbEventHandler) runRefreshAll(ctx context.Context) {
	wg := sync.WaitGroup{}
	for _, cacheManager := range h.cacheManagers {
		wg.Add(1)

		cacheManager := cacheManager
		go func() {
			defer wg.Done()

			// 判断是否执行全量刷新
			if !h.ifRunRefreshAll(ctx, cacheManager.Type(), time.Now().Unix()) {
				return
			}

			// 全量刷新
			err := RefreshAll(ctx, cacheManager, cacheManager.GetConcurrentLimit())
			if err != nil {
				logger.Errorf("refresh all cache failed: %v", err)
			}

			// 重置
			cacheManager.Reset()

			logger.Infof("refresh all cmdb resource(%s) cache", cacheManager.Type())

			// 记录全量刷新时间
			_, err = h.redisClient.Set(
				ctx,
				h.getLastUpdateTimeKey(cacheManager.Type()),
				strconv.FormatInt(time.Now().Unix(), 10),
				24*time.Hour,
			).Result()
			if err != nil {
				logger.Errorf("set last update time error: %v", err)
			}
		}()
	}
	wg.Wait()
}

// Run 处理cmdb资源变更事件
// 1. 遍历所有缓存类型，如果超过全量刷新间隔时间，先执行全量刷新
// 2. 从缓存中获取资源变更并进行预处理
// 3. 根据预处理结果，执行缓存变更动作
func (h *CmdbEventHandler) Run(ctx context.Context) {
	// 如果超过全量刷新间隔时间，先执行全量刷新
	h.runRefreshAll(ctx)

	// 重置预处理结果
	h.resetPreprocessResults()

	// 从缓存中获取资源变更并进行预处理
	wg := sync.WaitGroup{}
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
	err := h.refreshByEvents(ctx)
	if err != nil {
		logger.Errorf("refresh cmdb resource event error: %v", err)
	}
}

// RefreshAll 执行缓存管理器
func RefreshAll(ctx context.Context, cacheManager Manager, concurrentLimit int) error {
	// 判断是否启用业务缓存刷新
	if cacheManager.useBiz() {
		// 获取业务列表
		businesses, err := getBusinessList(ctx)
		if err != nil {
			return errors.Wrap(err, "get business list failed")
		}

		// 并发控制
		wg := sync.WaitGroup{}
		limitChan := make(chan struct{}, concurrentLimit)

		// 按业务刷新缓存
		errChan := make(chan error, len(businesses))
		for _, biz := range businesses {
			limitChan <- struct{}{}
			wg.Add(1)
			go func(bizId int) {
				defer func() {
					wg.Done()
					<-limitChan
				}()
				err := cacheManager.RefreshByBiz(ctx, bizId)
				if err != nil {
					errChan <- errors.Wrapf(err, "refresh %s cache by biz failed, biz: %d", cacheManager.Type(), bizId)
				}
			}(int(biz["bk_biz_id"].(float64)))
		}

		// 等待所有任务完成
		wg.Wait()
		close(errChan)
		for err := range errChan {
			return err
		}
	}

	// 刷新全局缓存
	err := cacheManager.RefreshGlobal(ctx)
	if err != nil {
		return errors.Wrapf(err, "refresh global %s cache failed", cacheManager.Type())
	}

	// 清理全局缓存
	err = cacheManager.CleanGlobal(ctx)
	if err != nil {
		return errors.Wrapf(err, "clean global %s cache failed", cacheManager.Type())
	}

	return nil
}
