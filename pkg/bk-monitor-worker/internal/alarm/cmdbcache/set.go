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
	"sync"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
)

const (
	setCacheKey         = "cmdb.set"
	setTemplateCacheKey = "cmdb.set_template"
)

type SetCacheManager struct {
	*BaseCacheManager
}

// NewSetCacheManager 创建模块缓存管理器
func NewSetCacheManager(prefix string, opt *redis.Options, concurrentLimit int) (*SetCacheManager, error) {
	base, err := NewBaseCacheManager(prefix, opt, concurrentLimit)
	if err != nil {
		return nil, err
	}

	base.initUpdatedFieldSet(setCacheKey, setTemplateCacheKey)
	return &SetCacheManager{
		BaseCacheManager: base,
	}, err
}

// getSetListByBizID 通过业务ID获取集群列表
func getSetListByBizID(ctx context.Context, bizID int) ([]map[string]interface{}, error) {
	cmdbApi := getCmdbApi()

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
			return cmdbApi.SearchSet().SetContext(ctx).SetBody(map[string]interface{}{"bk_biz_id": bizID, "page": map[string]int{"start": page * cmdbApiPageSize, "limit": cmdbApiPageSize}})
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
	// 请求集群信息
	result, err := getSetListByBizID(ctx, bizID)
	if err != nil {
		return errors.Wrapf(err, "failed to get set list by biz: %d", bizID)
	}

	// 准备缓存数据
	setCacheData := make(map[string]string)
	templateToSets := make(map[string][]string)
	for _, set := range result {
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
		err = m.UpdateHashMapCache(ctx, setCacheKey, setCacheData)
		if err != nil {
			return errors.Wrapf(err, "refresh set cache by biz: %d failed", bizID)
		}
		logger.Infof("refresh set cache by biz: %d, set count: %d", bizID, len(result))
	}

	// 更新服务模板关联的模块缓存
	if len(templateToSets) > 0 {
		setTemplateCacheData := make(map[string]string)
		for templateID, setIDs := range templateToSets {
			setTemplateCacheData[templateID] = fmt.Sprintf("[%s]", strings.Join(setIDs, ","))
		}
		err = m.UpdateHashMapCache(ctx, setTemplateCacheKey, setTemplateCacheData)
		if err != nil {
			return errors.Wrapf(err, "refresh set template cache by biz: %d failed", bizID)
		}
		logger.Infof("refresh set_template cache by biz: %d, set_template count: %d", bizID, len(setTemplateCacheData))
	}

	return nil
}

// RefreshGlobal 刷新全局模块缓存
func (m *SetCacheManager) RefreshGlobal(ctx context.Context) error {
	keys := []string{setCacheKey, setTemplateCacheKey}
	for _, key := range keys {
		if err := m.UpdateExpire(ctx, key); err != nil {
			logger.Errorf("expire hashmap failed, key: %s, err: %v", key, err)
		}
	}
	return nil
}

// CleanGlobal 清理全局模块缓存
func (m *SetCacheManager) CleanGlobal(ctx context.Context) error {
	err := m.DeleteMissingHashMapFields(ctx, setCacheKey)
	if err != nil {
		return errors.Wrap(err, "failed to delete missing hashmap fields")
	}

	err = m.DeleteMissingHashMapFields(ctx, setTemplateCacheKey)
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
		m.RedisClient.HDel(ctx, m.GetCacheKey(setCacheKey), setIds...)
	}

	// 删除集群模板关联的集群缓存
	if len(needDeleteSetTemplateIds) > 0 {
		m.RedisClient.HDel(ctx, m.GetCacheKey(setTemplateCacheKey), needDeleteSetTemplateIds...)
	}

	// 更新集群模板关联的集群缓存
	if len(setTemplateCacheData) > 0 {
		err := m.UpdateHashMapCache(ctx, setTemplateCacheKey, setTemplateCacheData)
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
