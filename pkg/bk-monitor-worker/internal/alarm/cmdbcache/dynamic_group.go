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
	"strconv"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	DynamicGroupCacheKey = "cmdb.dynamic_group"
)

type DynamicGroupCacheManager struct {
	*BaseCacheManager
}

func (m *DynamicGroupCacheManager) BuildRelationMetrics(ctx context.Context) error {
	// TODO implement me
	return errors.New("BuildRelationMetrics not implemented for DynamicGroupCacheManager")
}

// NewDynamicGroupCacheManager 创建动态分组缓存管理器
func NewDynamicGroupCacheManager(bkTenantId string, prefix string, opt *redis.Options, concurrentLimit int) (*DynamicGroupCacheManager, error) {
	base, err := NewBaseCacheManager(bkTenantId, prefix, opt, concurrentLimit)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create base cache Manager")
	}
	base.initUpdatedFieldSet(DynamicGroupCacheKey)
	return &DynamicGroupCacheManager{
		BaseCacheManager: base,
	}, nil
}

// Type 获取缓存类型
func (m *DynamicGroupCacheManager) Type() string {
	return "dynamic_group"
}

// getDynamicGroupTypeFields 获取动态分组类型字段
func getDynamicGroupTypeFields(dynamicGroupType string) string {
	switch dynamicGroupType {
	case "host":
		return "bk_host_id"
	case "set":
		return "bk_set_id"
	}
	return ""
}

// getDynamicGroup 获取动态分组
func getDynamicGroupRelatedIds(ctx context.Context, bkTenantId string, bizID int, dynamicGroupID string, dynamicGroupType string) ([]int, error) {
	cmdbApi, err := api.GetCmdbApi(bkTenantId)
	if err != nil {
		return nil, errors.Wrapf(err, "GetCmdbApi failed, bkTenantId: %s", bkTenantId)
	}

	// 根据动态分组类型获取对应的资源ID字段
	field := getDynamicGroupTypeFields(dynamicGroupType)
	if field == "" {
		return nil, errors.New("invalid dynamic group type")
	}

	// 获取动态分组下的资源列表
	result, err := api.BatchApiRequest(
		cmdbApiPageSize,
		func(resp any) (int, error) {
			var result cmdb.ExecuteDynamicGroupResp
			err := mapstructure.Decode(resp, &result)
			if err != nil {
				return 0, errors.Wrap(err, "failed to decode dynamic group list response")
			}
			if !result.Result {
				return 0, errors.New("failed to get dynamic group list")
			}
			return result.Data.Count, nil
		},
		func(page int) define.Operation {
			return cmdbApi.ExecuteDynamicGroup().SetContext(ctx).SetPathParams(map[string]string{"bk_biz_id": strconv.Itoa(bizID), "id": dynamicGroupID}).SetBody(map[string]any{"bk_biz_id": bizID, "id": dynamicGroupID, "fields": []string{field}, "page": map[string]int{"start": page * cmdbApiPageSize, "limit": cmdbApiPageSize}})
		},
		10,
	)
	if err != nil {
		return nil, err
	}

	// 获取动态分组下的相关资源ID列表
	relatedIDs := make([]int, 0)
	for _, item := range result {
		if item == nil {
			logger.Warn("dynamic group item is nil")
		}

		var res cmdb.ExecuteDynamicGroupResp
		err := mapstructure.Decode(item, &res)
		if err != nil {
			return nil, errors.Wrap(err, "failed to decode dynamic group list response")
		}

		for _, relatedObj := range res.Data.Info {
			relatedIDs = append(relatedIDs, int(relatedObj[field].(float64)))
		}
	}

	return relatedIDs, nil
}

// getDynamicGroupList 获取动态分组列表
func getDynamicGroupList(ctx context.Context, bkTenantId string, bizID int) (map[string]map[string]any, error) {
	cmdbApi, err := api.GetCmdbApi(bkTenantId)
	if err != nil {
		return nil, errors.Wrapf(err, "GetCmdbApi failed, bkTenantId: %s", bkTenantId)
	}

	result, err := api.BatchApiRequest(
		cmdbApiPageSize,
		func(resp any) (int, error) {
			var result cmdb.SearchDynamicGroupResp
			err := mapstructure.Decode(resp, &result)
			if err != nil {
				return 0, errors.Wrap(err, "failed to decode dynamic group list response")
			}
			if !result.Result {
				return 0, errors.New("failed to get dynamic group list")
			}
			return result.Data.Count, nil
		},
		func(page int) define.Operation {
			return cmdbApi.SearchDynamicGroup().SetContext(ctx).SetPathParams(map[string]string{"bk_biz_id": strconv.Itoa(bizID)}).SetBody(map[string]any{"bk_biz_id": bizID, "page": map[string]int{"start": page * cmdbApiPageSize, "limit": cmdbApiPageSize}})
		},
		10,
	)
	if err != nil {
		return nil, err
	}

	// 获取所有动态分组信息
	dynamicGroupToRelatedIDs := make(map[string]map[string]any)
	for _, item := range result {
		if item == nil {
			logger.Warn("dynamic group item is nil")
		}

		var res cmdb.SearchDynamicGroupResp
		err := mapstructure.Decode(item, &res)
		if err != nil {
			return nil, errors.Wrap(err, "failed to decode dynamic group list response")
		}

		for _, dg := range res.Data.Info {
			relatedIDs, err := getDynamicGroupRelatedIds(ctx, bkTenantId, bizID, dg.ID, dg.BkObjId)
			if err != nil {
				return nil, errors.Wrap(err, "failed to get dynamic group related ids")
			}

			dynamicGroupToRelatedIDs[dg.ID] = map[string]any{
				"bk_biz_id":   bizID,
				"bk_inst_ids": relatedIDs,
				"bk_obj_id":   dg.BkObjId,
				"name":        dg.Name,
				"id":          dg.ID,
			}
		}
	}

	return dynamicGroupToRelatedIDs, nil
}

// RefreshByBiz 更新业务下的动态分组缓存
func (m *DynamicGroupCacheManager) RefreshByBiz(ctx context.Context, bizID int) error {
	dynamicGroupToRelatedIDs, err := getDynamicGroupList(ctx, m.GetBkTenantId(), bizID)
	if err != nil {
		return errors.Wrap(err, "failed to get dynamic group list")
	}

	// 将动态分组信息转换为字符串
	dataMap := make(map[string]string)
	for k, v := range dynamicGroupToRelatedIDs {
		dataStr, _ := json.Marshal(v)
		dataMap[k] = string(dataStr)
	}

	// 更新动态分组缓存
	err = m.UpdateHashMapCache(ctx, m.GetCacheKey(DynamicGroupCacheKey), dataMap)
	if err != nil {
		return errors.Wrap(err, "failed to update dynamic group cache")
	}

	return nil
}

// RefreshGlobal 更新全局动态分组缓存
func (m *DynamicGroupCacheManager) RefreshGlobal(ctx context.Context) error {
	result := m.RedisClient.Expire(ctx, m.GetCacheKey(DynamicGroupCacheKey), m.Expire)
	if err := result.Err(); err != nil {
		return errors.Wrap(err, "set dynamic group cache expire failed")
	}
	return nil
}

// CleanGlobal 清除全局动态分组缓存
func (m *DynamicGroupCacheManager) CleanGlobal(ctx context.Context) error {
	key := m.GetCacheKey(DynamicGroupCacheKey)
	err := m.DeleteMissingHashMapFields(ctx, key)
	if err != nil {
		return errors.Wrap(err, "failed to clean global dynamic group cache")
	}
	return nil
}

// CleanByEvents 清除事件相关的动态分组缓存
func (m *DynamicGroupCacheManager) CleanByEvents(ctx context.Context, resourceType string, events []map[string]any) error {
	return nil
}

// UpdateByEvents 更新事件相关的动态分组缓存
func (m *DynamicGroupCacheManager) UpdateByEvents(ctx context.Context, resourceType string, events []map[string]any) error {
	return nil
}
