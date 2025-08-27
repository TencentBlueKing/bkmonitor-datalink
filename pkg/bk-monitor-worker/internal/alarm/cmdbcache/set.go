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
	"time"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/relation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	setCacheKey         = "cmdb.set"
	setTemplateCacheKey = "cmdb.set_template"
)

type SetCacheManager struct {
	*BaseCacheManager
}

// BuildRelationMetrics 从缓存构建relation指标
func (m *SetCacheManager) BuildRelationMetrics(ctx context.Context) error {
	n := time.Now()

	// 1. 从缓存获取数据（自动滚动获取所有数据）
	cacheData, err := m.batchQuery(ctx, m.GetCacheKey(setCacheKey), "*")
	if err != nil {
		return errors.Wrap(err, "get set cache failed")
	}

	// 2. 解析JSON数据并按业务ID分组
	bizDataMap := make(map[int][]map[string]interface{})
	for _, jsonStr := range cacheData {
		var item map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &item); err != nil {
			logger.Warnf("unmarshal set cache failed: %v", err)
			continue
		}

		// 从数据中提取业务ID
		bizID := int(item["bk_biz_id"].(float64))
		bizDataMap[bizID] = append(bizDataMap[bizID], item)
	}

	// 3. 按业务ID构建relation指标
	for bizID, data := range bizDataMap {
		m.buildRelationMetricsByBizAndData(ctx, data, bizID)
	}
	logger.Infof("[cmdb_relation] build cache type:set action:end biz_count: %d cost: %s", len(bizDataMap), time.Since(n))

	return nil
}

func (m *SetCacheManager) buildRelationMetricsByBizAndData(ctx context.Context, data []map[string]interface{}, bizID int) {
	infos := m.SetToRelationInfos(data)
	if err := relation.GetRelationMetricsBuilder().BuildInfosCache(ctx, bizID, relation.Set, infos); err != nil {
		logger.Errorf("build set relation metrics failed for biz %d: %v", bizID, err)
	}
}

// NewSetCacheManager 创建模块缓存管理器
func NewSetCacheManager(bkTenantId string, prefix string, opt *redis.Options, concurrentLimit int) (*SetCacheManager, error) {
	base, err := NewBaseCacheManager(bkTenantId, prefix, opt, concurrentLimit)
	if err != nil {
		return nil, err
	}

	base.initUpdatedFieldSet(setCacheKey, setTemplateCacheKey)
	return &SetCacheManager{
		BaseCacheManager: base,
	}, err
}

// getSetListByBizID 通过业务ID获取集群列表
func getSetListByBizID(ctx context.Context, bkTenantId string, bizID int) ([]map[string]interface{}, error) {
	cmdbApi := getCmdbApi(bkTenantId)

	// 请求集群信息
	result, err := api.BatchApiRequest(
		cmdbApiPageSize,
		func(resp interface{}) (int, error) {
			var result cmdb.SearchSetResp
			err := mapstructure.Decode(resp, &result)
			if err != nil {
				return 0, errors.Wrap(err, "failed to decode response")
			}

			if !result.Result {
				return 0, errors.Errorf("cmdb api request failed: %s", result.Message)
			}
			return result.Data.Count, nil
		},
		// 生成分页请求
		func(page int) define.Operation {
			return cmdbApi.SearchSet().SetContext(ctx).SetPathParams(map[string]string{"bk_biz_id": strconv.Itoa(bizID)}).SetBody(map[string]interface{}{"bk_biz_id": bizID, "page": map[string]int{"start": page * cmdbApiPageSize, "limit": cmdbApiPageSize}})
		},
		10,
	)

	if err != nil {
		return nil, errors.Wrap(err, "failed to request cmdb api")
	}

	// 准备缓存数据
	setList := make([]map[string]interface{}, 0)
	for _, item := range result {
		var res cmdb.SearchSetResp
		err = mapstructure.Decode(item, &res)
		if err != nil {
			return nil, errors.Wrap(err, "failed to decode response")
		}

		setList = append(setList, res.Data.Info...)
	}

	return setList, nil
}

// Type 缓存类型
func (m *SetCacheManager) Type() string {
	return "set"
}

// RefreshByBiz 刷新业务模块缓存
func (m *SetCacheManager) RefreshByBiz(ctx context.Context, bizID int) error {
	// 业务ID为1的是资源池，不需要刷新
	if bizID == 1 {
		return nil
	}

	// 请求集群信息
	result, err := getSetListByBizID(ctx, m.GetBkTenantId(), bizID)
	if err != nil {
		return errors.Wrapf(err, "failed to get set list by biz: %d", bizID)
	}

	// 准备缓存数据
	setCacheData := make(map[string]string)
	templateToSets := make(map[string][]string)
	for _, set := range result {
		// 注入业务 ID 信息
		set["bk_biz_id"] = bizID

		setStr, err := json.Marshal(set)
		if err != nil {
			return errors.Wrap(err, "failed to marshal set")
		}

		setId, ok := set["bk_set_id"].(float64)
		if !ok {
			continue
		}
		setIdStr := strconv.Itoa(int(setId))
		setCacheData[setIdStr] = string(setStr)

		setTemplateId, ok := set["set_template_id"].(float64)
		if !ok || setTemplateId <= 0 {
			continue
		}
		setTemplateIdStr := strconv.Itoa(int(setTemplateId))
		templateToSets[setTemplateIdStr] = append(templateToSets[setTemplateIdStr], setIdStr)
	}

	// 更新集群缓存
	if len(setCacheData) > 0 {
		key := m.GetCacheKey(setCacheKey)
		err = m.UpdateHashMapCache(ctx, key, setCacheData)
		if err != nil {
			return errors.Wrapf(err, "refresh set cache by biz: %d failed", bizID)
		}
		logger.Infof("refresh set cache by biz: %d, set count: %d", bizID, len(result))
	}

	// 更新服务模板关联的模块缓存
	if len(templateToSets) > 0 {
		key := m.GetCacheKey(setTemplateCacheKey)
		setTemplateCacheData := make(map[string]string)
		for templateID, setIDs := range templateToSets {
			setTemplateCacheData[templateID] = fmt.Sprintf("[%s]", strings.Join(setIDs, ","))
		}
		err = m.UpdateHashMapCache(ctx, key, setTemplateCacheData)
		if err != nil {
			return errors.Wrapf(err, "refresh set template cache by biz: %d failed", bizID)
		}
		logger.Infof("refresh set_template cache by biz: %d, set_template count: %d", bizID, len(setTemplateCacheData))
	}

	// 处理完所有主机信息之后，根据 hosts 生成 relation 指标
	m.buildRelationMetricsByBizAndData(ctx, result, bizID)

	return nil
}

// SetToRelationInfos Set 信息转关联缓存信息
func (m *SetCacheManager) SetToRelationInfos(result []map[string]any) []*relation.Info {
	infos := make([]*relation.Info, 0, len(result))
	for _, r := range result {
		id := cast.ToString(r[relation.SetID])

		if id == "" {
			continue
		}

		var expands map[string]map[string]any
		if expandString, ok := r[relation.ExpandInfoColumn].(string); ok {
			err := json.Unmarshal([]byte(expandString), &expands)
			if err != nil {
				logger.Warnf("[cmdb_relation] SetToRelationInfos json unmarshal error with %s, %s", expandString, err)
				continue
			}
		}

		info := &relation.Info{
			ID:       id,
			Resource: relation.Set,
			Label: map[string]string{
				relation.SetID: id,
			},
			Expands: relation.TransformExpands(expands),
		}

		// 如果存在 set_info 数据，则需要注入 set_name 等扩展维度
		if info.Expands[relation.Set] != nil {
			info.Expands[relation.Set][relation.SetName] = cast.ToString(r[relation.SetName])
		}

		infos = append(infos, info)
	}

	return infos
}

// RefreshGlobal 刷新全局模块缓存
func (m *SetCacheManager) RefreshGlobal(ctx context.Context) error {
	result := m.RedisClient.Expire(ctx, m.GetCacheKey(setCacheKey), m.Expire)
	if err := result.Err(); err != nil {
		return errors.Wrap(err, "set module cache expire time failed")
	}

	result = m.RedisClient.Expire(ctx, m.GetCacheKey(setTemplateCacheKey), m.Expire)
	if err := result.Err(); err != nil {
		return errors.Wrap(err, "set template module cache expire time failed")
	}
	return nil
}

// CleanGlobal 清理全局模块缓存
func (m *SetCacheManager) CleanGlobal(ctx context.Context) error {
	err := m.DeleteMissingHashMapFields(ctx, m.GetCacheKey(setCacheKey))
	if err != nil {
		return errors.Wrap(err, "failed to delete missing hashmap fields")
	}

	err = m.DeleteMissingHashMapFields(ctx, m.GetCacheKey(setTemplateCacheKey))
	if err != nil {
		return errors.Wrap(err, "failed to delete missing hashmap fields")
	}
	return nil
}

// CleanByEvents 根据事件清理缓存
func (m *SetCacheManager) CleanByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error {
	if resourceType != "set" || len(events) == 0 {
		return nil
	}

	// 提取集群ID及集群模板ID
	needDeleteSetIds := make(map[int]struct{})
	needUpdateSetTemplateIds := make(map[string]struct{})
	for _, event := range events {
		setID, ok := event["bk_set_id"].(float64)
		if !ok {
			continue
		}
		// 记录需要删除的集群ID
		needDeleteSetIds[int(setID)] = struct{}{}

		// 记录需要删除的集群模板关联的集群ID
		if setTemplateID, ok := event["set_template_id"].(float64); ok && setTemplateID > 0 {
			needUpdateSetTemplateIds[strconv.Itoa(int(setTemplateID))] = struct{}{}
		}
	}

	setTemplateCacheData := make(map[string]string)
	needDeleteSetTemplateIds := make([]string, 0)
	for setTemplateID := range needUpdateSetTemplateIds {
		// 获取原有的集群ID
		result := m.RedisClient.HGet(ctx, m.GetCacheKey(setTemplateCacheKey), setTemplateID)
		if result.Err() != nil {
			continue
		}

		var oldSetIds []int
		err := json.Unmarshal([]byte(result.Val()), &oldSetIds)
		if err != nil {
			continue
		}

		// 计算新的集群ID
		var newSetIds []string
		for _, oldSetID := range oldSetIds {
			if _, ok := needDeleteSetIds[oldSetID]; !ok {
				newSetIds = append(newSetIds, strconv.Itoa(oldSetID))
			}
		}

		// 更新集群模板关联的集群缓存
		if len(newSetIds) > 0 {
			setTemplateCacheData[setTemplateID] = fmt.Sprintf("[%s]", strings.Join(newSetIds, ","))
		} else {
			needDeleteSetTemplateIds = append(needDeleteSetTemplateIds, setTemplateID)
		}
	}

	// 删除缓存
	if len(needDeleteSetIds) > 0 {
		setIds := make([]string, 0, len(needDeleteSetIds))
		for setID := range needDeleteSetIds {
			setIds = append(setIds, strconv.Itoa(setID))
		}

		// 清理 relationMetrics 里的缓存数据
		rmb := relation.GetRelationMetricsBuilder()
		result := m.RedisClient.HMGet(ctx, m.GetCacheKey(setCacheKey), setIds...)
		for _, value := range result.Val() {
			if value == nil {
				continue
			}

			var set map[string]interface{}
			err := json.Unmarshal([]byte(cast.ToString(value)), &set)
			if err != nil {
				continue
			}

			// 清理 relation metrics 里面的 set 资源
			rmb.ClearResourceWithID(cast.ToInt(set["bk_biz_id"]), relation.Set, cast.ToString(set["bk_set_id"]))
		}

		m.RedisClient.HDel(ctx, m.GetCacheKey(setCacheKey), setIds...)
	}

	// 删除集群模板关联的集群缓存
	if len(needDeleteSetTemplateIds) > 0 {
		m.RedisClient.HDel(ctx, m.GetCacheKey(setTemplateCacheKey), needDeleteSetTemplateIds...)
	}

	// 更新集群模板关联的集群缓存
	if len(setTemplateCacheData) > 0 {
		err := m.UpdateHashMapCache(ctx, m.GetCacheKey(setTemplateCacheKey), setTemplateCacheData)
		if err != nil {
			return errors.Wrap(err, "failed to update set template hashmap cache")
		}
	}

	return nil
}

// UpdateByEvents 根据事件更新缓存
func (m *SetCacheManager) UpdateByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error {
	if resourceType != "set" || len(events) == 0 {
		return nil
	}

	// 提取业务ID
	needUpdateBizIds := make(map[int]struct{})
	for _, event := range events {
		bizID, ok := event["bk_biz_id"].(float64)
		if ok {
			needUpdateBizIds[int(bizID)] = struct{}{}
		}
	}

	// 按业务更新缓存
	wg := sync.WaitGroup{}
	limitChan := make(chan struct{}, m.ConcurrentLimit)
	for bizID := range needUpdateBizIds {
		wg.Add(1)
		limitChan <- struct{}{}
		go func(bizID int) {
			defer func() {
				<-limitChan
				wg.Done()
			}()
			err := m.RefreshByBiz(ctx, bizID)
			if err != nil {
				logger.Errorf("failed to refresh set cache by biz: %d, err: %v", bizID, err)
			}
		}(bizID)
	}
	wg.Wait()

	return nil
}
