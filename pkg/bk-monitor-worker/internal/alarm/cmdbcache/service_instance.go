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
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	serviceInstanceCacheKey       = "cmdb.service_instance"
	hostToServiceInstanceCacheKey = "cmdb.host_to_service_instance_id"
)

// AlarmServiceInstanceInfo 服务实例信息
type AlarmServiceInstanceInfo struct {
	BkBizId           int         `json:"bk_biz_id"`
	ID                int         `json:"id"`
	ServiceInstanceId int         `json:"service_instance_id"`
	Name              string      `json:"name"`
	BkModuleId        int         `json:"bk_module_id"`
	BkHostId          int         `json:"bk_host_id"`
	ServiceTemplateId int         `json:"service_template_id"`
	ProcessInstances  interface{} `json:"process_instances"`

	// 补充字段
	IP        string                              `json:"ip"`
	BkCloudId int                                 `json:"bk_cloud_id"`
	TopoLinks map[string][]map[string]interface{} `json:"topo_link"`
}

// ServiceInstanceCacheManager 服务实例缓存管理器
type ServiceInstanceCacheManager struct {
	*BaseCacheManager
}

func (m *ServiceInstanceCacheManager) BuildRelationMetrics(ctx context.Context) error {
	//TODO implement me
	return errors.New("ServiceInstanceCacheManager BuildRelationMetrics not implemented")
}

// NewServiceInstanceCacheManager 创建服务实例缓存管理器
func NewServiceInstanceCacheManager(bkTenantId string, prefix string, opt *redis.Options, concurrentLimit int) (*ServiceInstanceCacheManager, error) {
	manager, err := NewBaseCacheManager(bkTenantId, prefix, opt, concurrentLimit)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create base cache Manager")
	}

	manager.initUpdatedFieldSet(serviceInstanceCacheKey, hostToServiceInstanceCacheKey)
	return &ServiceInstanceCacheManager{
		BaseCacheManager: manager,
	}, nil
}

// Type 缓存类型
func (m *ServiceInstanceCacheManager) Type() string {
	return "service_instance"
}

// UseBiz 是否按业务执行
func (m *ServiceInstanceCacheManager) useBiz() bool {
	return true
}

// getServiceInstances 获取服务实例列表
func getServiceInstances(ctx context.Context, bkTenantId string, bkBizId int) ([]*AlarmServiceInstanceInfo, error) {
	cmdbApi := getCmdbApi(bkTenantId)
	// 设置超时时间
	_ = cmdbApi.AddOperationOptions()

	// 批量拉取业务下的服务实例信息
	results, err := api.BatchApiRequest(
		cmdbApiPageSize, func(resp interface{}) (int, error) {
			var res cmdb.ListServiceInstanceDetailResp
			err := mapstructure.Decode(resp, &res)
			if err != nil {
				return 0, errors.Wrap(err, "decode response failed")
			}
			return res.Data.Count, nil
		},
		func(page int) define.Operation {
			return cmdbApi.ListServiceInstanceDetail().SetContext(ctx).SetBody(map[string]interface{}{"page": map[string]int{"start": page * cmdbApiPageSize, "limit": cmdbApiPageSize}, "bk_biz_id": bkBizId})
		},
		10,
	)
	if err != nil {
		return nil, err
	}

	serviceInstances := make([]*AlarmServiceInstanceInfo, 0)
	for _, result := range results {
		var res cmdb.ListServiceInstanceDetailResp
		err := mapstructure.Decode(result, &res)
		if err != nil {
			return nil, errors.Wrap(err, "decode response failed")
		}
		for _, instance := range res.Data.Info {
			serviceInstance := &AlarmServiceInstanceInfo{
				BkBizId:           bkBizId,
				ID:                instance.ID,
				ServiceInstanceId: instance.ID,
				Name:              instance.Name,
				BkModuleId:        instance.BkModuleId,
				BkHostId:          instance.BkHostId,
				ServiceTemplateId: instance.ServiceTemplateId,
				ProcessInstances:  instance.ProcessInstances,
				TopoLinks:         make(map[string][]map[string]interface{}),
			}
			serviceInstances = append(serviceInstances, serviceInstance)
		}
	}

	return serviceInstances, nil
}

// RefreshByBiz 按业务刷新缓存
func (m *ServiceInstanceCacheManager) RefreshByBiz(ctx context.Context, bkBizId int) error {
	serviceInstances, err := getServiceInstances(ctx, m.GetBkTenantId(), bkBizId)
	if err != nil {
		return errors.Wrap(err, "get service instances failed")

	}
	hostIdSet := make(map[int]struct{})
	for _, instance := range serviceInstances {
		hostIdSet[instance.BkHostId] = struct{}{}
	}

	// 查询主机信息
	hostIds := make([]string, 0, len(hostIdSet))
	for hostID := range hostIdSet {
		hostIds = append(hostIds, strconv.Itoa(hostID))
	}
	hosts := make(map[string]AlarmHostInfo)
	hostKey := m.GetCacheKey(hostCacheKey)

	// 按主机ID批量查询主机信息，1000个主机一次
	client := m.RedisClient
	for i := 0; i < len(hostIds); i += 1000 {
		result := client.HMGet(ctx, hostKey, hostIds[i:min(i+1000, len(hostIds))]...)
		if err := result.Err(); err != nil {
			return errors.Wrap(err, "hmget host failed")
		}
		for _, value := range result.Val() {
			if value == nil {
				continue
			}
			var host AlarmHostInfo
			if err := json.Unmarshal([]byte(value.(string)), &host); err != nil {
				return errors.Wrap(err, "unmarshal host failed")
			}
			hosts[strconv.Itoa(host.BkHostId)] = host
		}
	}

	// 补充IP/云区域及拓扑链路信息
	for _, instance := range serviceInstances {
		host, ok := hosts[strconv.Itoa(instance.BkHostId)]
		if ok {
			instance.IP = host.BkHostInnerip
			instance.BkCloudId = host.BkCloudId
			for moduleId, links := range host.TopoLinks {
				if moduleId == fmt.Sprintf("module|%d", instance.BkModuleId) {
					instance.TopoLinks[moduleId] = links
					break
				}
			}
		}
	}

	// 刷新服务实例缓存
	key := m.GetCacheKey(serviceInstanceCacheKey)
	serviceInstanceMap := make(map[string]string)
	for _, instance := range serviceInstances {
		value, err := json.Marshal(instance)
		if err != nil {
			return errors.Wrap(err, "marshal service instance failed")
		}
		serviceInstanceMap[strconv.Itoa(instance.ID)] = string(value)
	}
	err = m.UpdateHashMapCache(ctx, key, serviceInstanceMap)
	if err != nil {
		return errors.Wrap(err, "update hashmap cmdb service instance cache failed")
	}

	// 刷新主机到服务实例缓存
	key = m.GetCacheKey(hostToServiceInstanceCacheKey)
	hostToServiceInstances := make(map[string][]string)
	for _, instance := range serviceInstances {
		hostToServiceInstances[strconv.Itoa(instance.BkHostId)] = append(hostToServiceInstances[strconv.Itoa(instance.BkHostId)], strconv.Itoa(instance.ID))
	}
	hostToServiceInstancesStr := make(map[string]string)
	for hostId, instances := range hostToServiceInstances {
		hostToServiceInstancesStr[hostId] = fmt.Sprintf("[%s]", strings.Join(instances, ","))
	}
	err = m.UpdateHashMapCache(ctx, key, hostToServiceInstancesStr)
	if err != nil {
		return errors.Wrap(err, "update hashmap host to service instance cache failed")
	}

	return nil
}

// RefreshGlobal 刷新全局缓存
func (m *ServiceInstanceCacheManager) RefreshGlobal(ctx context.Context) error {
	// 刷新缓存过期时间
	for _, key := range []string{serviceInstanceCacheKey, hostToServiceInstanceCacheKey} {
		if err := m.RedisClient.Expire(ctx, m.GetCacheKey(key), m.Expire).Err(); err != nil {
			logger.Error("set cache expire time failed, key: %s, err: %v", key, err)
		}
	}
	return nil
}

// CleanGlobal 清理全局缓存
func (m *ServiceInstanceCacheManager) CleanGlobal(ctx context.Context) error {
	key := m.GetCacheKey(serviceInstanceCacheKey)
	if err := m.DeleteMissingHashMapFields(ctx, key); err != nil {
		return errors.Wrap(err, "delete missing fields failed")
	}

	key = m.GetCacheKey(hostToServiceInstanceCacheKey)
	if err := m.DeleteMissingHashMapFields(ctx, key); err != nil {
		return errors.Wrap(err, "delete missing fields failed")
	}

	return nil
}

// CleanByEvents 根据事件清理缓存
func (m *ServiceInstanceCacheManager) CleanByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error {
	return nil
}

// UpdateByEvents 根据事件更新缓存
func (m *ServiceInstanceCacheManager) UpdateByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error {
	if len(events) == 0 {
		return nil
	}

	needUpdateBizIds := make(map[int]struct{})
	switch resourceType {
	case "process":
		for _, event := range events {
			bkBizID, ok := event["bk_biz_id"].(float64)
			if !ok {
				continue
			}
			needUpdateBizIds[int(bkBizID)] = struct{}{}
		}
	}

	// 记录需要更新的业务ID
	needUpdateBizIdSlice := make([]string, 0, len(needUpdateBizIds))
	for bizID := range needUpdateBizIds {
		needUpdateBizIdSlice = append(needUpdateBizIdSlice, strconv.Itoa(bizID))
	}
	logger.Infof("need update service instance cache biz ids: %v", strings.Join(needUpdateBizIdSlice, ","))

	// 按业务刷新缓存
	wg := sync.WaitGroup{}
	limitChan := make(chan struct{}, m.ConcurrentLimit)
	for bizID := range needUpdateBizIds {
		wg.Add(1)
		limitChan <- struct{}{}

		go func(bizId int) {
			defer func() {
				<-limitChan
				wg.Done()
			}()
			err := m.RefreshByBiz(ctx, bizId)
			if err != nil {
				logger.Errorf("failed to refresh service instance cache by biz: %d, err: %v", bizId, err)
			}
		}(bizID)
	}

	wg.Wait()
	return nil
}
