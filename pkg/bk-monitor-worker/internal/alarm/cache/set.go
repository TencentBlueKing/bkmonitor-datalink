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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
)

type SetCacheManager struct {
	*BaseCacheManager
}

// NewSetCacheManager 创建模块缓存管理器
func NewSetCacheManager(prefix string, opt *alarm.RedisOptions) (*SetCacheManager, error) {
	base, err := NewBaseCacheManager(prefix, opt)

	return &SetCacheManager{
		BaseCacheManager: base,
	}, err
}

// RefreshByBiz 刷新业务模块缓存
func (m *SetCacheManager) RefreshByBiz(ctx context.Context, bizID int) error {
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return err
	}

	result, err := api.BatchApiRequest(
		cmdbApi.SearchSet().SetContext(ctx),
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
		func(req define.Operation, page int) define.Operation {
			return req.SetBody(map[string]interface{}{"bk_biz_id": bizID, "page": map[string]int{"start": page * CmdbApiPageSize, "limit": CmdbApiPageSize}})
		},
		10,
	)

	if err != nil {
		return errors.Wrap(err, "failed to request cmdb api")
	}

	var res cmdb.SearchSetResp
	setCacheData := make(map[string]string)
	templateToSets := make(map[string][]string)
	for _, item := range result {
		err = mapstructure.Decode(item, &res)
		if err != nil {
			return errors.Wrap(err, "failed to decode response")
		}

		for _, set := range res.Data.Info {
			setStr, err := json.Marshal(set)
			if err != nil {
				return errors.Wrap(err, "failed to marshal set")
			}

			setCacheData[strconv.Itoa(set.BkSetId)] = string(setStr)
			templateToSets[strconv.Itoa(set.SetTemplateId)] = append(templateToSets[strconv.Itoa(set.SetTemplateId)], strconv.Itoa(set.BkSetId))
		}
	}
	key := m.GetCacheKey("cmdb.set")
	err = m.UpdateHashMapCache(ctx, key, setCacheData)
	if err != nil {
		return errors.Wrap(err, "failed to update set hashmap cache")
	}

	// 更新服务模板关联的模块缓存
	key = m.GetCacheKey("cmdb.set_template")
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
	key := m.GetCacheKey("cmdb.set")
	err := m.DeleteMissingHashMapFields(ctx, key)
	if err != nil {
		return errors.Wrap(err, "failed to delete missing hashmap fields")
	}
	return nil
}
