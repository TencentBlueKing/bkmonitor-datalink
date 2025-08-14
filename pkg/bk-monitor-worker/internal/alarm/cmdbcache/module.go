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
	moduleCacheKey          = "cmdb.module"
	serviceTemplateCacheKey = "cmdb.service_template"
)

type ModuleCacheManager struct {
	*BaseCacheManager
}

// BuildRelationMetrics 从缓存构建relation指标
func (m *ModuleCacheManager) BuildRelationMetrics(ctx context.Context) error {
	n := time.Now()
	logger.Infof("[cmdb_relation] build_cache type:module action:start")

	// 1. 从缓存获取数据（自动滚动获取所有数据）
	cacheData, err := m.batchQuery(ctx, m.GetCacheKey(moduleCacheKey), "*")
	if err != nil {
		return errors.Wrap(err, "get module cache failed")
	}

	// 2. 解析JSON数据并按业务ID分组
	bizDataMap := make(map[int][]map[string]interface{})
	for _, jsonStr := range cacheData {
		var item map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &item); err != nil {
			logger.Warnf("unmarshal module cache failed: %v", err)
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
	logger.Infof("[cmdb_relation] build cache type:module action:end biz_count: %d cost: %s", len(bizDataMap), time.Since(n))

	return nil
}

func (m *ModuleCacheManager) buildRelationMetricsByBizAndData(ctx context.Context, data []map[string]interface{}, bizID int) {
	infos := m.ModuleToRelationInfos(data)
	if err := relation.GetRelationMetricsBuilder().BuildInfosCache(ctx, bizID, relation.Module, infos); err != nil {
		logger.Errorf("build module relation metrics failed for biz %d: %v", bizID, err)
	}
}

// NewModuleCacheManager 创建模块缓存管理器
func NewModuleCacheManager(bkTenantId string, prefix string, opt *redis.Options, concurrentLimit int) (*ModuleCacheManager, error) {
	base, err := NewBaseCacheManager(bkTenantId, prefix, opt, concurrentLimit)
	if err != nil {
		return nil, err
	}

	base.initUpdatedFieldSet(moduleCacheKey, serviceTemplateCacheKey)
	return &ModuleCacheManager{
		BaseCacheManager: base,
	}, err
}

// getModuleListByBizID 通过业务ID获取模块列表
func getModuleListByBizID(ctx context.Context, bkTenantId string, bizID int) ([]map[string]interface{}, error) {
	cmdbApi := getCmdbApi(bkTenantId)
	result, err := api.BatchApiRequest(
		cmdbApiPageSize,
		func(resp interface{}) (int, error) {
			var result cmdb.SearchModuleResp
			err := mapstructure.Decode(resp, &result)
			if err != nil {
				return 0, errors.Wrap(err, "failed to decode response")
			}

			if !result.Result {
				return 0, errors.Errorf("cmdb api request failed: %s", result.Message)
			}
			return result.Data.Count, nil
		},
		func(page int) define.Operation {
			return cmdbApi.SearchModule().SetContext(ctx).SetPathParams(map[string]string{"bk_biz_id": strconv.Itoa(bizID), "bk_set_id": "0"}).SetBody(map[string]interface{}{"bk_biz_id": bizID, "page": map[string]int{"start": page * cmdbApiPageSize, "limit": cmdbApiPageSize}})
		},
		10,
	)

	if err != nil {
		return nil, errors.Wrap(err, "failed to request cmdb api")
	}

	var moduleList []map[string]interface{}
	for _, item := range result {
		if item == nil {
			logger.Warnf("cmdb api response is nil")
		}

		var res cmdb.SearchModuleResp
		err = mapstructure.Decode(item, &res)
		if err != nil {
			return nil, errors.Wrap(err, "failed to decode response")
		}

		for _, module := range res.Data.Info {
			moduleList = append(moduleList, module)
		}
	}

	return moduleList, nil
}

// Type 缓存类型
func (m *ModuleCacheManager) Type() string {
	return "module"
}

// RefreshByBiz 刷新业务模块缓存
func (m *ModuleCacheManager) RefreshByBiz(ctx context.Context, bizID int) error {
	// 业务ID为1的是资源池，不需要刷新
	if bizID == 1 {
		return nil
	}

	// 请求模块信息
	moduleList, err := getModuleListByBizID(ctx, m.GetBkTenantId(), bizID)
	if err != nil {
		return errors.Wrapf(err, "failed to get module list by biz: %d", bizID)
	}

	moduleCacheData := make(map[string]string)
	templateToModules := make(map[string][]string)

	for _, module := range moduleList {
		// 主备负责人字段处理
		for _, field := range []string{"bk_bak_operator", "operator"} {
			var operators []string
			operator, _ := module[field].(string)
			if operator != "" {
				operators = strings.Split(operator, ",")
			} else {
				operators = []string{}
			}
			module[field] = operators
		}

		// 注入 业务 ID
		module["bk_biz_id"] = bizID

		// 转换为json字符串
		moduleStr, err := json.Marshal(module)
		if err != nil {
			return errors.Wrap(err, "failed to marshal module")
		}

		// 记录模块缓存
		moduleId, ok := module["bk_module_id"].(float64)
		if !ok {
			continue
		}
		moduleIdStr := strconv.Itoa(int(moduleId))
		moduleCacheData[moduleIdStr] = string(moduleStr)

		// 记录服务模板关联的模块
		serviceTemplateId, ok := module["service_template_id"].(float64)
		if !ok || serviceTemplateId <= 0 {
			continue
		}
		serviceTemplateIdStr := strconv.Itoa(int(serviceTemplateId))
		templateToModules[serviceTemplateIdStr] = append(templateToModules[serviceTemplateIdStr], moduleIdStr)
	}

	// 更新模块缓存
	if len(moduleCacheData) > 0 {
		err = m.UpdateHashMapCache(ctx, m.GetCacheKey(moduleCacheKey), moduleCacheData)
		if err != nil {
			return errors.Wrapf(err, "refresh module cache by biz: %d failed", bizID)
		}
		logger.Infof("refresh module cache by biz: %d, module count: %d", bizID, len(moduleCacheData))
	}

	// 更新服务模板关联的模块缓存
	if len(templateToModules) > 0 {
		serviceTemplateCacheData := make(map[string]string)
		for templateID, moduleIDs := range templateToModules {
			serviceTemplateCacheData[templateID] = fmt.Sprintf("[%s]", strings.Join(moduleIDs, ","))
		}
		err = m.UpdateHashMapCache(ctx, m.GetCacheKey(serviceTemplateCacheKey), serviceTemplateCacheData)
		if err != nil {
			return errors.Wrapf(err, "refresh service_template cache by biz: %d failed", bizID)
		}
		logger.Infof("refresh service_template cache by biz: %d, service_template count: %d", bizID, len(templateToModules))
	}

	// 刷新 relation metrics 缓存
	m.buildRelationMetricsByBizAndData(ctx, moduleList, bizID)

	return nil
}

// ModuleToRelationInfos 模块信息转关联缓存信息
func (m *ModuleCacheManager) ModuleToRelationInfos(result []map[string]any) []*relation.Info {
	infos := make([]*relation.Info, 0, len(result))
	for _, r := range result {
		id := cast.ToString(r[relation.ModuleID])

		if id == "" {
			continue
		}

		var expands map[string]map[string]any
		if expandString, ok := r[relation.ExpandInfoColumn].(string); ok {
			err := json.Unmarshal([]byte(expandString), &expands)
			if err != nil {
				logger.Warnf("[cmdb_relation] ModuleToRelationInfos json unmarshal error with %s, %s", expandString, err)
				continue
			}
		}

		info := &relation.Info{
			ID:       id,
			Resource: relation.Module,
			Label: map[string]string{
				relation.ModuleID: id,
			},
			Expands: relation.TransformExpands(expands),
		}

		// 如果存在 module_info 数据，则需要注入 module_name 等扩展维度
		if info.Expands[relation.Module] != nil {
			info.Expands[relation.Module][relation.ModuleName] = cast.ToString(r[relation.ModuleName])
		}

		infos = append(infos, info)
	}

	return infos
}

// RefreshGlobal 刷新全局模块缓存
func (m *ModuleCacheManager) RefreshGlobal(ctx context.Context) error {
	result := m.RedisClient.Expire(ctx, m.GetCacheKey(moduleCacheKey), m.Expire)
	if err := result.Err(); err != nil {
		return errors.Wrap(err, "set module cache expire time failed")
	}

	result = m.RedisClient.Expire(ctx, m.GetCacheKey(serviceTemplateCacheKey), m.Expire)
	if err := result.Err(); err != nil {
		return errors.Wrap(err, "set service_template cache expire time failed")
	}

	return nil
}

// CleanGlobal 清理全局模块缓存
func (m *ModuleCacheManager) CleanGlobal(ctx context.Context) error {
	key := m.GetCacheKey(moduleCacheKey)
	err := m.DeleteMissingHashMapFields(ctx, key)
	if err != nil {
		return errors.Wrap(err, "failed to delete missing hashmap fields")
	}

	key = m.GetCacheKey(serviceTemplateCacheKey)
	err = m.DeleteMissingHashMapFields(ctx, key)
	if err != nil {
		return errors.Wrap(err, "failed to delete missing hashmap fields")
	}
	return nil
}

// CleanByEvents 根据事件清理缓存
func (m *ModuleCacheManager) CleanByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error {
	// 只处理模块事件
	if resourceType != "module" || len(events) == 0 {
		return nil
	}

	// 提取模块ID及服务模板ID
	needDeleteModuleIds := make(map[int]struct{})
	needUpdateServiceTemplateIds := make(map[string]struct{})

	for _, event := range events {
		moduleID, ok := event["bk_module_id"].(float64)
		if !ok {
			continue
		}
		// 记录需要删除的模块ID
		needDeleteModuleIds[int(moduleID)] = struct{}{}

		// 记录各个服务模板下需要删除的模块ID
		if serviceTemplateID, ok := event["service_template_id"].(float64); ok && serviceTemplateID > 0 {
			needUpdateServiceTemplateIds[strconv.Itoa(int(serviceTemplateID))] = struct{}{}
		}
	}

	// 删除服务模板关联的模块缓存
	serviceTemplateCacheData := make(map[string]string)
	needDeleteServiceTemplateIds := make([]string, 0)
	for serviceTemplateID := range needUpdateServiceTemplateIds {
		// 查询存量缓存
		result := m.RedisClient.HGet(ctx, m.GetCacheKey(serviceTemplateCacheKey), serviceTemplateID)
		if result.Err() != nil {
			continue
		}
		var oldModuleIDs []int
		err := json.Unmarshal([]byte(result.Val()), &oldModuleIDs)
		if err != nil {
			continue
		}

		// 清理需要删除的模块ID
		var newModuleIDs []string
		for _, moduleID := range oldModuleIDs {
			if _, ok := needDeleteModuleIds[moduleID]; !ok {
				newModuleIDs = append(newModuleIDs, strconv.Itoa(moduleID))
			}
		}

		// 如果删除后，服务模板下没有模块，则需要清理服务模板缓存，否则更新缓存
		if len(newModuleIDs) > 0 {
			serviceTemplateCacheData[serviceTemplateID] = fmt.Sprintf("[%s]", strings.Join(newModuleIDs, ","))
		} else {
			needDeleteServiceTemplateIds = append(needDeleteServiceTemplateIds, serviceTemplateID)
		}
	}

	// 删除模块缓存
	if len(needDeleteModuleIds) > 0 {
		moduleIds := make([]string, 0, len(needDeleteModuleIds))
		for moduleID := range needDeleteModuleIds {
			moduleIds = append(moduleIds, strconv.Itoa(moduleID))
		}

		// 清理 relationMetrics 里的缓存数据
		rmb := relation.GetRelationMetricsBuilder()
		result := m.RedisClient.HMGet(ctx, m.GetCacheKey(moduleCacheKey), moduleIds...)
		for _, value := range result.Val() {
			if value == nil {
				continue
			}

			var module map[string]interface{}
			err := json.Unmarshal([]byte(cast.ToString(value)), &module)
			if err != nil {
				continue
			}

			// 清理 relation metrics 里面的 set 资源
			rmb.ClearResourceWithID(cast.ToInt(module["bk_biz_id"]), relation.Module, cast.ToString(module["bk_module_id"]))
		}

		m.RedisClient.HDel(ctx, m.GetCacheKey(moduleCacheKey), moduleIds...)
	}

	// 更新服务模板关联的模块缓存
	if len(serviceTemplateCacheData) > 0 {
		err := m.UpdateHashMapCache(ctx, m.GetCacheKey(serviceTemplateCacheKey), serviceTemplateCacheData)
		if err != nil {
			return errors.Wrap(err, "failed to update service_template hashmap cache")
		}
	}

	// 清理服务模板关联的模块缓存
	if len(needDeleteServiceTemplateIds) > 0 {
		m.RedisClient.HDel(ctx, m.GetCacheKey(serviceTemplateCacheKey), needDeleteServiceTemplateIds...)
	}

	return nil
}

// UpdateByEvents 根据事件更新缓存
func (m *ModuleCacheManager) UpdateByEvents(ctx context.Context, resourceType string, events []map[string]interface{}) error {
	if resourceType != "module" || len(events) == 0 {
		return nil
	}

	// 提取业务ID
	needUpdateBizIds := make(map[int]struct{})
	for _, event := range events {
		if bizID, ok := event["bk_biz_id"].(float64); ok {
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
				logger.Errorf("failed to refresh module cache by biz: %d, err: %v", bizID, err)
			}
		}(bizID)
	}
	wg.Wait()
	return nil
}
