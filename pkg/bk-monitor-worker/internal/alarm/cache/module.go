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

package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	moduleCacheKey          = "cmdb.module"
	serviceTemplateCacheKey = "cmdb.service_template"
)

type ModuleCacheManager struct {
	*BaseCacheManager
}

// NewModuleCacheManager 创建模块缓存管理器
func NewModuleCacheManager(prefix string, opt *redis.RedisOptions) (*ModuleCacheManager, error) {
	base, err := NewBaseCacheManager(prefix, opt)
	if err != nil {
		return nil, err
	}

	base.initUpdatedFieldSet(moduleCacheKey, serviceTemplateCacheKey)
	return &ModuleCacheManager{
		BaseCacheManager: base,
	}, err
}

// getModuleListByBizID 通过业务ID获取模块列表
func getModuleListByBizID(ctx context.Context, bizID int) ([]cmdb.SearchModuleData, error) {
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return nil, err
	}

	result, err := api.BatchApiRequest(
		CmdbApiPageSize,
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
			return cmdbApi.SearchModule().SetContext(ctx).SetBody(map[string]interface{}{"bk_biz_id": bizID, "page": map[string]int{"start": page * CmdbApiPageSize, "limit": CmdbApiPageSize}})
		},
		10,
	)

	if err != nil {
		return nil, errors.Wrap(err, "failed to request cmdb api")
	}

	var moduleList []cmdb.SearchModuleData
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
	// 请求模块信息
	moduleList, err := getModuleListByBizID(ctx, bizID)

	moduleCacheData := make(map[string]string)
	templateToModules := make(map[string][]string)

	for _, module := range moduleList {
		moduleStr, err := json.Marshal(module)
		if err != nil {
			return errors.Wrap(err, "failed to marshal module")
		}

		moduleCacheData[strconv.Itoa(module.BkModuleId)] = string(moduleStr)
		serviceTemplateId := strconv.Itoa(module.ServiceTemplateId)
		templateToModules[serviceTemplateId] = append(templateToModules[serviceTemplateId], strconv.Itoa(module.BkModuleId))
	}

	// 更新模块缓存
	if moduleCacheData != nil {
		key := m.GetCacheKey(moduleCacheKey)
		err = m.UpdateHashMapCache(ctx, key, moduleCacheData)
		if err != nil {
			return errors.Wrap(err, "failed to update module hashmap cache")
		}
	}
	logger.Infof("refresh module cache by biz: %d, module count: %d", bizID, len(moduleList))

	// 更新服务模板关联的模块缓存
	if templateToModules != nil {
		key := m.GetCacheKey(serviceTemplateCacheKey)
		serviceTemplateCacheData := make(map[string]string)
		for templateID, moduleIDs := range templateToModules {
			serviceTemplateCacheData[templateID] = fmt.Sprintf("[%s]", strings.Join(moduleIDs, ","))
		}
		err = m.UpdateHashMapCache(ctx, key, serviceTemplateCacheData)
		if err != nil {
			return errors.Wrap(err, "failed to update service_template hashmap cache")
		}
	}
	logger.Infof("refresh service_template cache by biz: %d, service_template count: %d", bizID, len(templateToModules))

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
	if resourceType != "module" || len(events) == 0 {
		return nil
	}

	// 提取模块ID及服务模板ID
	moduleIds := make([]string, 0, len(events))
	serviceTemplateIds := make([]string, 0, len(events))
	for _, event := range events {
		moduleID, ok := event["bk_module_id"].(int)
		if ok {
			moduleIds = append(moduleIds, strconv.Itoa(moduleID))
		}

		serviceTemplateID, ok := event["service_template_id"].(int)
		if ok && serviceTemplateID > 0 {
			serviceTemplateIds = append(serviceTemplateIds, strconv.Itoa(serviceTemplateID))
		}
	}

	// 删除缓存
	if len(moduleIds) > 0 {
		m.RedisClient.HDel(ctx, m.GetCacheKey("cmd.module"), moduleIds...)
	}

	if len(serviceTemplateIds) > 0 {
		m.RedisClient.HDel(ctx, m.GetCacheKey(serviceTemplateCacheKey), serviceTemplateIds...)
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
		bizID, ok := event["bk_biz_id"].(int)
		if ok {
			needUpdateBizIds[bizID] = struct{}{}
		}
	}

	// 按业务更新缓存
	for bizID := range needUpdateBizIds {
		err := m.RefreshByBiz(ctx, bizID)
		if err != nil {
			logger.Errorf("failed to refresh module cache by biz: %d, err: %v", bizID, err)
		}
	}

	return nil
}
