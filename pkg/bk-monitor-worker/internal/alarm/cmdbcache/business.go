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

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	businessCacheKey = "cmdb.business"
)

// BusinessCacheManager 业务缓存管理器
// 业务缓存不分租户进行存储
type BusinessCacheManager struct {
	*BaseCacheManager
}

func (m *BusinessCacheManager) BuildRelationMetrics(ctx context.Context) error {
	// TODO implement me
	return errors.New("BuildRelationMetrics not implemented for BusinessCacheManager")
}

// NewBusinessCacheManager 创建业务缓存管理器
func NewBusinessCacheManager(bkTenantId string, prefix string, opt *redis.Options, concurrentLimit int) (*BusinessCacheManager, error) {
	manager, err := NewBaseCacheManager(bkTenantId, prefix, opt, concurrentLimit)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create base cache Manager")
	}

	manager.initUpdatedFieldSet(businessCacheKey)
	return &BusinessCacheManager{
		BaseCacheManager: manager,
	}, nil
}

// getBusinessList 获取业务列表
func getBusinessList(ctx context.Context, bkTenantId string) ([]map[string]any, error) {
	bizList := make([]map[string]any, 0)
	cmdbApi := getCmdbApi(bkTenantId)
	// 并发请求获取业务列表
	result, err := api.BatchApiRequest(
		cmdbApiPageSize,
		// 获取总数
		func(resp any) (int, error) {
			data, ok := resp.(map[string]any)["data"]
			if !ok {
				return 0, errors.Errorf("response data not found, resp: %v", resp)
			}
			count, ok := data.(map[string]any)["count"]
			if !ok {
				return 0, errors.Errorf("response count not found, resp: %v", resp)
			}
			return int(count.(float64)), nil
		},
		// 设置分页参数
		func(page int) define.Operation {
			return cmdbApi.SearchBusiness().SetContext(ctx).SetBody(map[string]any{"page": map[string]int{"start": page * cmdbApiPageSize, "limit": cmdbApiPageSize}})
		},
		10,
	)
	if err != nil {
		return nil, err
	}

	// 获取业务对象字段说明，并提取用户类型字段
	bizAttrs, err := getBusinessAttribute(ctx, bkTenantId)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get business attribute, tenantId: %s", bkTenantId)
	}
	userAttrs := make([]string, 0)
	for _, attr := range bizAttrs {
		if attr.BkPropertyType == "objuser" {
			userAttrs = append(userAttrs, attr.BkPropertyId)
		}
	}

	for _, item := range result {
		bizResp := item.(map[string]any)
		bizData := bizResp["data"].(map[string]any)
		bizInfo := bizData["info"].([]any)

		for _, info := range bizInfo {
			biz := info.(map[string]any)
			biz["bk_tenant_id"] = bkTenantId

			// 处理用户类型字段
			for _, attr := range userAttrs {
				userStr, ok := biz[attr].(string)
				if !ok {
					continue
				}

				// 转换为数组
				if userStr == "" {
					biz[attr] = []string{}
				} else {
					biz[attr] = strings.Split(userStr, ",")
				}
			}

			bizList = append(bizList, biz)
		}
	}

	return bizList, nil
}

// getBusinessAttribute 获取业务对象字段说明
func getBusinessAttribute(ctx context.Context, tenantId string) ([]cmdb.SearchObjectAttributeData, error) {
	cmdbApi := getCmdbApi(tenantId)

	// 获取业务对象字段说明
	var attrResult cmdb.SearchObjectAttributeResp
	_, err := cmdbApi.SearchObjectAttribute().SetContext(ctx).SetBody(map[string]any{"bk_obj_id": "biz"}).SetResult(&attrResult).Request()
	err = api.HandleApiResultError(attrResult.ApiCommonRespMeta, err, "search object attribute failed")
	if err != nil {
		return nil, err
	}

	return attrResult.Data, nil
}

// getSpaceList 获取空间列表
func getSpaceList() ([]space.Space, error) {
	var spaces []space.Space
	db := mysql.GetDBSession().DB
	err := space.NewSpaceQuerySet(db).All(&spaces)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get spaces")
	}
	return spaces, nil
}

// Type 缓存类型
func (m *BusinessCacheManager) Type() string {
	return "business"
}

// UseBiz 是否按业务执行
func (m *BusinessCacheManager) useBiz() bool {
	return true
}

// RefreshGlobal 刷新全局缓存
func (m *BusinessCacheManager) RefreshGlobal(ctx context.Context) error {
	// 业务缓存不分租户进行存储，只有系统租户需要刷新缓存
	// 如果租户不是系统租户，则不刷新缓存
	if m.GetBkTenantId() != tenant.DefaultTenantId {
		return nil
	}

	logger.Infof("start refresh business cache")
	defer logger.Infof("end refresh business cache")

	// 获取业务列表
	bizCacheData := make(map[string]string)
	tenants, err := tenant.GetTenantList()
	if err != nil {
		return errors.Wrap(err, "failed to get tenant list")
	}
	for _, tenant := range tenants {
		bizList, err := getBusinessList(ctx, tenant.Id)
		if err != nil {
			logger.Errorf("failed to get business list, tenantId: %s, err: %v", tenant.Id, err)
			continue
		}

		// 业务信息处理
		for _, biz := range bizList {
			bizID := strconv.Itoa(int(biz["bk_biz_id"].(float64)))
			biz["bk_tenant_id"] = tenant.Id

			// 转换为json字符串
			bizStr, err := json.Marshal(biz)
			if err != nil {
				continue
			}
			bizCacheData[bizID] = string(bizStr)
		}
	}

	// 如果没有拉到任何业务，则不更新缓存
	if len(bizCacheData) == 0 {
		logger.Errorf("no business found when refresh business cache")
		return nil
	}

	// 空间查询
	spaces, err := getSpaceList()
	if err != nil {
		return errors.Wrap(err, "failed to get spaces")
	}

	// 将空间信息转换为业务信息
	var bkBizId int
	for _, s := range spaces {
		// 业务ID，非bkcc空间为负数
		if s.SpaceTypeId == "bkcc" {
			continue
		} else {
			bkBizId = -s.Id
		}

		// 构造业务信息
		biz := map[string]any{
			"bk_tenant_id":      s.BkTenantId,
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

		if bizStr, err := json.Marshal(biz); err == nil {
			bizCacheData[strconv.Itoa(bkBizId)] = string(bizStr)
		}
	}

	// 更新缓存
	key := m.GetCacheKey(businessCacheKey)
	err = m.UpdateHashMapCache(ctx, key, bizCacheData)
	if err != nil {
		return errors.Wrap(err, "update business cache failed")
	}

	// 更新缓存过期时间
	if err := m.RedisClient.Expire(ctx, key, m.Expire).Err(); err != nil {
		return errors.Wrap(err, "set business cache expire time failed")
	}

	return nil
}

// CleanGlobal 清理全局缓存
func (m *BusinessCacheManager) CleanGlobal(ctx context.Context) error {
	key := m.GetCacheKey(businessCacheKey)
	if err := m.DeleteMissingHashMapFields(ctx, key); err != nil {
		return errors.Wrap(err, "delete missing fields failed")
	}
	return nil
}

// CleanByEvents 根据事件清理缓存
func (m *BusinessCacheManager) CleanByEvents(ctx context.Context, resourceType string, events []map[string]any) error {
	if resourceType != "biz" {
		return nil
	}

	// 获取业务ID
	bizIds := make([]string, 0, len(events))
	for _, event := range events {
		if bizID, ok := event["bk_biz_id"].(float64); ok {
			bizIds = append(bizIds, strconv.Itoa(int(bizID)))
		}
	}

	// 删除缓存
	if len(bizIds) > 0 {
		m.RedisClient.HDel(ctx, m.GetCacheKey(businessCacheKey), bizIds...)
	}

	return nil
}

// UpdateByEvents 根据事件更新缓存
func (m *BusinessCacheManager) UpdateByEvents(ctx context.Context, resourceType string, events []map[string]any) error {
	if resourceType != "biz" || len(events) == 0 {
		return nil
	}

	// 如果有更新就直接刷新全局缓存
	if err := m.RefreshGlobal(ctx); err != nil {
		return err
	}

	return nil
}
