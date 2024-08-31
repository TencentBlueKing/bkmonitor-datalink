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
	"strings"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
)

const (
	moduleCacheKey          = "cmdb.module"
	serviceTemplateCacheKey = "cmdb.service_template"
)

type ModuleCacheManager struct {
	*BaseCacheManager
}

// NewModuleCacheManager 创建模块缓存管理器
func NewModuleCacheManager(prefix string, opt *redis.Options, concurrentLimit int) (*ModuleCacheManager, error) {
	base, err := NewBaseCacheManager(prefix, opt, concurrentLimit)
	if err != nil {
		return nil, err
	}

	base.initUpdatedFieldSet(moduleCacheKey, serviceTemplateCacheKey)
	return &ModuleCacheManager{
		BaseCacheManager: base,
	}, err
}

// getModuleListByBizID 通过业务ID获取模块列表
func getModuleListByBizID(ctx context.Context, bizID int) ([]map[string]interface{}, error) {
	cmdbApi := getCmdbApi()
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
			return cmdbApi.SearchModule().SetContext(ctx).SetBody(map[string]interface{}{"bk_biz_id": bizID, "page": map[string]int{"start": page * cmdbApiPageSize, "limit": cmdbApiPageSize}})
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
	// 请求模块信息
	moduleList, err := getModuleListByBizID(ctx, bizID)
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
	if moduleCacheData != nil {
		err = m.UpdateHashMapCache(ctx, moduleCacheKey, moduleCacheData)
		if err != nil {
			return errors.Wrapf(err, "refresh module cache by biz: %d failed", bizID)
		}
		logger.Infof("refresh module cache by biz: %d, module count: %d", bizID, len(moduleCacheData))
	}

	// 更新服务模板关联的模块缓存
	if templateToModules != nil {
		serviceTemplateCacheData := make(map[string]string)
		for templateID, moduleIDs := range templateToModules {
			serviceTemplateCacheData[templateID] = fmt.Sprintf("[%s]", strings.Join(moduleIDs, ","))
		}
		err = m.UpdateHashMapCache(ctx, serviceTemplateCacheKey, serviceTemplateCacheData)
		if err != nil {
			return errors.Wrapf(err, "refresh service_template cache by biz: %d failed", bizID)
		}
		logger.Infof("refresh service_template cache by biz: %d, service_template count: %d", bizID, len(templateToModules))
	}

	return nil
}

// RefreshGlobal 刷新全局模块缓存
func (m *ModuleCacheManager) RefreshGlobal(ctx context.Context) error {
	keys := []string{moduleCacheKey, serviceTemplateCacheKey}
	for _, key := range keys {
		if err := m.UpdateExpire(ctx, key); err != nil {
			logger.Errorf("failed to update %s cache expire time: %v", key, err)
		}
	}
	return nil
}

// CleanGlobal 清理全局模块缓存
func (m *ModuleCacheManager) CleanGlobal(ctx context.Context) error {
	err := m.DeleteMissingHashMapFields(ctx, moduleCacheKey)
	if err != nil {
		return errors.Wrap(err, "failed to delete missing hashmap fields")
	}

	err = m.DeleteMissingHashMapFields(ctx, serviceTemplateCacheKey)
	if err != nil {
		return errors.Wrap(err, "failed to delete missing hashmap fields")
	}
	return nil
}
