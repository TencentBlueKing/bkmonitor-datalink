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
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
)

type AlarmBusinessInfo struct {
	BkBizId         int      `json:"bk_biz_id"`
	BkBizName       string   `json:"bk_biz_name"`
	BkBizDeveloper  []string `json:"bk_biz_developer"`
	BkBizProductor  []string `json:"bk_biz_productor"`
	BkBizTester     []string `json:"bk_biz_tester"`
	BkBizMaintainer []string `json:"bk_biz_maintainer"`
	Operator        []string `json:"operator"`
	TimeZone        string   `json:"time_zone"`
	Language        string   `json:"language"`
	LifeCycle       string   `json:"life_cycle"`
}

// BusinessCacheManager 业务缓存管理器
type BusinessCacheManager struct {
	*BaseCacheManager
}

// NewBusinessCacheManager 创建业务缓存管理器
func NewBusinessCacheManager(prefix string, opt *redis.RedisOptions) (*BusinessCacheManager, error) {
	manager, err := NewBaseCacheManager(prefix, opt)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create base cache manager")
	}

	return &BusinessCacheManager{
		BaseCacheManager: manager,
	}, nil
}

// RefreshGlobal 刷新全局缓存
func (m *BusinessCacheManager) RefreshGlobal(ctx context.Context) error {
	cmdbApi, err := api.GetCmdbApi()
	if err != nil {
		return errors.Wrap(err, "failed to get cmdb api")
	}

	// 获取业务列表
	result, err := api.BatchApiRequest(
		cmdbApi.SearchBusiness(),
		CmdbApiPageSize,
		func(resp interface{}) (int, error) {
			data, ok := resp.(map[string]interface{})["data"]
			if !ok {
				return 0, errors.New("response data not found")
			}
			count, ok := data.(map[string]interface{})["count"]
			if !ok {
				return 0, errors.New("response count not found")
			}
			return int(count.(float64)), nil
		},
		func(req define.Operation, page int) define.Operation {
			return req.SetBody(map[string]interface{}{"page": map[string]int{"start": page * CmdbApiPageSize, "limit": CmdbApiPageSize}})
		},
		10,
	)
	if err != nil {
		return err
	}

	// 获取业务对象字段说明
	var attrResult cmdb.SearchObjectAttributeResp
	_, err = cmdbApi.SearchObjectAttribute().SetBody(map[string]interface{}{"bk_obj_id": "biz"}).SetResult(&attrResult).Request()
	err = api.HandleApiResultError(attrResult.ApiCommonRespMeta, err, "search object attribute failed")
	if err != nil {
		return err
	}

	// 业务缓存数据准备
	bizCacheData := make(map[string]string)
	for _, item := range result {
		// 业务信息提取
		bizResp := item.(map[string]interface{})
		bizData := bizResp["data"].(map[string]interface{})
		bizInfo := bizData["info"].([]interface{})

		// 业务信息处理
		for _, info := range bizInfo {
			biz := info.(map[string]interface{})
			bizID := strconv.Itoa(int(biz["bk_biz_id"].(float64)))

			// 处理用户类型字段
			for _, attr := range attrResult.Data {
				// 跳过非用户类型字段
				if attr.BkPropertyType != "objuser" {
					continue
				}

				// 跳过不存在的字段
				userStr, ok := biz[attr.BkPropertyId].(string)
				if !ok {
					continue
				}

				// 转换为数组
				if userStr == "" {
					biz[attr.BkPropertyId] = []string{}
				} else {
					biz[attr.BkPropertyId] = strings.Split(userStr, ",")
				}
			}

			// 转换为json字符串
			bizStr, err := json.Marshal(biz)
			if err != nil {
				continue
			}
			bizCacheData[bizID] = string(bizStr)
		}

	}

	// 空间查询
	var spaces []space.Space
	db := mysql.GetDBSession().DB
	err = space.NewSpaceQuerySet(db).All(&spaces)
	if err != nil {
		return errors.Wrap(err, "failed to get spaces")
	}

	// 将空间信息转换为业务信息
	var bkBizId int
	for _, s := range spaces {
		// 业务ID，非bkcc空间为负数
		if s.SpaceTypeId == "bkcc" {
			bkBizId, err = strconv.Atoi(s.SpaceId)
			if err != nil {
				continue
			}
		} else {
			bkBizId = -s.Id
		}
		biz := map[string]interface{}{
			"bk_biz_id":         bkBizId,
			"bk_biz_name":       fmt.Sprintf("[%s]%s", s.SpaceId, s.SpaceName),
			"bk_biz_developer":  []string{},
			"bk_biz_productor":  []string{},
			"bk_biz_tester":     []string{},
			"bk_biz_maintainer": []string{},
			"operator":          []string{},
			"time_zone":         s.TimeZone,
			// 这里的语言是固定的，参考的是python代码中的处理逻辑，如果后续需要支持空间的语言，需要修改这里
			"language":   "1",
			"life_cycle": "2",
		}
		bizStr, err := json.Marshal(biz)
		if err != nil {
			continue
		}
		bizCacheData[strconv.Itoa(bkBizId)] = string(bizStr)
	}

	// 更新缓存
	key := m.GetCacheKey("cmdb.business")
	err = m.UpdateHashMapCache(ctx, key, bizCacheData)
	if err != nil {
		return errors.Wrap(err, "update business cache failed")
	}

	return nil
}

// CleanGlobal 清理全局缓存
func (m *BusinessCacheManager) CleanGlobal(ctx context.Context) error {
	key := m.GetCacheKey("cmdb.business")
	err := m.DeleteMissingHashMapFields(ctx, key)
	if err != nil {
		return errors.Wrap(err, "delete missing fields failed")
	}
	return nil
}
