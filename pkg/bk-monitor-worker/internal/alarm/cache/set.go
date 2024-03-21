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
	setCacheKey         = "cmdb.set"
	setTemplateCacheKey = "cmdb.set_template"
)

type SetCacheManager struct {
	*BaseCacheManager
}

// NewSetCacheManager 创建模块缓存管理器
func NewSetCacheManager(prefix string, opt *redis.RedisOptions) (*SetCacheManager, error) {
	base, err := NewBaseCacheManager(prefix, opt)
	if err != nil {
		return nil, err
	}

	base.initUpdatedFieldSet(setCacheKey, setTemplateCacheKey)
	return &SetCacheManager{
		BaseCacheManager: base,
	}, err
}

// getSetListByBizID 通过业务ID获取集群列表
func getSetListByBizID(ctx context.Context, bizID int) ([]cmdb.SearchSetData, error) {
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return nil, err
	}

	// 请求集群信息
	result, err := api.BatchApiRequest(
		CmdbApiPageSize,
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
			return cmdbApi.SearchSet().SetContext(ctx).SetBody(map[string]interface{}{"bk_biz_id": bizID, "page": map[string]int{"start": page * CmdbApiPageSize, "limit": CmdbApiPageSize}})
		},
		10,
	)

	if err != nil {
		return nil, errors.Wrap(err, "failed to request cmdb api")
	}

	// 准备缓存数据
	setList := make([]cmdb.SearchSetData, 0)
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
	// 请求集群信息
	result, err := getSetListByBizID(ctx, bizID)
	if err != nil {
		return errors.Wrap(err, "failed to get set list by biz")
	}

	// 准备缓存数据
	setCacheData := make(map[string]string)
	templateToSets := make(map[string][]string)
	for _, set := range result {
		setStr, err := json.Marshal(set)
		if err != nil {
			return errors.Wrap(err, "failed to marshal set")
		}

		setCacheData[strconv.Itoa(set.BkSetId)] = string(setStr)
		templateToSets[strconv.Itoa(set.SetTemplateId)] = append(templateToSets[strconv.Itoa(set.SetTemplateId)], strconv.Itoa(set.BkSetId))
	}

	// 更新集群缓存
	key := m.GetCacheKey(setCacheKey)
	err = m.UpdateHashMapCache(ctx, key, setCacheData)
	if err != nil {
		return errors.Wrap(err, "failed to update set hashmap cache")
	}

	// 更新服务模板关联的模块缓存
	key = m.GetCacheKey(setTemplateCacheKey)
	setTemplateCacheData := make(map[string]string)
	for templateID, setIDs := range templateToSets {
		setTemplateCacheData[templateID] = fmt.Sprintf("[%s]", strings.Join(setIDs, ","))
	}
	err = m.UpdateHashMapCache(ctx, key, setTemplateCacheData)
	if err != nil {
		return errors.Wrap(err, "failed to update set template hashmap cache")
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
	setIds := make([]string, 0, len(events))
	setTemplateIds := make([]string, 0, len(events))
	for _, event := range events {
		setID, ok := event["bk_set_id"].(int)
		if ok {
			setIds = append(setIds, strconv.Itoa(setID))
		}

		setTemplateID, ok := event["set_template_id"].(int)
		if ok && setTemplateID > 0 {
			setTemplateIds = append(setTemplateIds, strconv.Itoa(setTemplateID))
		}
	}

	// 删除缓存
	if len(setIds) > 0 {
		m.RedisClient.HDel(ctx, m.GetCacheKey(setCacheKey), setIds...)
	}

	if len(setTemplateIds) > 0 {
		m.RedisClient.HDel(ctx, m.GetCacheKey(setTemplateCacheKey), setTemplateIds...)
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
		bizID, ok := event["bk_biz_id"].(int)
		if ok {
			needUpdateBizIds[bizID] = struct{}{}
		}
	}

	// 按业务更新缓存
	for bizID := range needUpdateBizIds {
		err := m.RefreshByBiz(ctx, bizID)
		if err != nil {
			logger.Errorf("failed to refresh set cache by biz: %d, err: %v", bizID, err)
		}
	}

	return nil
}
