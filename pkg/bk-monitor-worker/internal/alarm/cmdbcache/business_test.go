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
	"testing"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/user"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
)

var DemoBusinesses = []map[string]any{
	{
		"data": map[string]any{
			"info": []any{
				map[string]any{
					"bk_biz_id":         2.0,
					"bk_biz_name":       "BlueKing",
					"bk_biz_developer":  "admin",
					"bk_biz_productor":  "admin,user1",
					"bk_biz_tester":     "admin,user1",
					"bk_biz_maintainer": "admin,user2",
					"operator":          "admin",
					"time_zone":         "Asia/Shanghai",
					"language":          "1",
					"life_cycle":        "2",
					"bk_pmp_qa":         "user1,user2",
					"bk_pmp_qa2":        "user1,user2",
				},
				map[string]any{
					"bk_biz_id":         3.0,
					"bk_biz_name":       "Test",
					"bk_biz_developer":  "user1",
					"bk_biz_productor":  "user1",
					"bk_biz_tester":     "user1,user2",
					"bk_biz_maintainer": "",
					"operator":          "user1",
					"time_zone":         "Asia/Shanghai",
					"language":          "1",
					"life_cycle":        "2",
				},
			},
		},
	},
}

var DefaultTenantBusinesses = []map[string]any{
	{
		"bk_tenant_id":      tenant.DefaultTenantId,
		"bk_biz_id":         2.0,
		"bk_biz_name":       "BlueKing",
		"bk_biz_developer":  []string{"admin"},
		"bk_biz_productor":  []string{"admin", "user1"},
		"bk_biz_tester":     []string{"admin", "user1"},
		"bk_biz_maintainer": []string{"admin", "user2"},
		"operator":          "admin",
		"time_zone":         "Asia/Shanghai",
		"language":          "1",
		"life_cycle":        "2",
		"bk_pmp_qa":         []string{"user1", "user2"},
		"bk_pmp_qa2":        "user1,user2",
	},
	{
		"bk_tenant_id":      tenant.DefaultTenantId,
		"bk_biz_id":         3.0,
		"bk_biz_name":       "Test",
		"bk_biz_developer":  []string{"user1"},
		"bk_biz_productor":  []string{"user1"},
		"bk_biz_tester":     []string{"user1", "user2"},
		"bk_biz_maintainer": []string{},
		"operator":          []string{"user1"},
		"time_zone":         "Asia/Shanghai",
		"language":          "1",
		"life_cycle":        "2",
	},
}

var Tenant1Businesses = []map[string]any{
	{
		"bk_tenant_id":      "tenant1",
		"bk_biz_id":         4.0,
		"bk_biz_name":       "Test2",
		"bk_biz_developer":  []string{"user1"},
		"bk_biz_productor":  []string{"user1"},
		"bk_biz_tester":     []string{"user1", "user2"},
		"bk_biz_maintainer": []string{},
		"operator":          []string{"user1"},
		"time_zone":         "Asia/Shanghai",
		"language":          "1",
		"life_cycle":        "2",
	},
}

var BusinessAttrs = []cmdb.SearchObjectAttributeData{
	{
		BkObjId:        "biz",
		BkPropertyId:   "bk_biz_id",
		BkPropertyName: "BusinessID",
		BkPropertyType: "system",
		Creator:        "admin",
	},
	{
		BkObjId:        "biz",
		BkPropertyId:   "bk_biz_developer",
		BkPropertyName: "Developer",
		BkPropertyType: "objuser",
		Creator:        "admin",
	},
	{
		BkObjId:        "biz",
		BkPropertyId:   "bk_biz_productor",
		BkPropertyName: "Productor",
		BkPropertyType: "objuser",
		Creator:        "admin",
	},
	{
		BkObjId:        "biz",
		BkPropertyId:   "bk_biz_tester",
		BkPropertyName: "Tester",
		BkPropertyType: "objuser",
		Creator:        "admin",
	},
	{
		BkObjId:        "biz",
		BkPropertyId:   "bk_biz_maintainer",
		BkPropertyName: "Maintainer",
		BkPropertyType: "objuser",
		Creator:        "admin",
	},
	{
		BkObjId:        "biz",
		BkPropertyId:   "operator",
		BkPropertyName: "Operator",
		BkPropertyType: "objuser",
		Creator:        "admin",
	},
	{
		BkObjId:        "biz",
		BkPropertyId:   "bk_pmp_qa",
		BkPropertyName: "PMPQA",
		BkPropertyType: "objuser",
		Creator:        "admin",
	},
}

var DemoSpaces = []space.Space{
	{
		Id:          1,
		SpaceTypeId: "bkcc",
		SpaceId:     "2",
		SpaceName:   "BlueKing",
		SpaceCode:   "bkcc__2",
		Status:      "normal",
		TimeZone:    "Asia/Shanghai",
		Language:    "zh-hans",
		IsBcsValid:  false,
		BkTenantId:  "system",
	},
	{
		Id:          2,
		SpaceTypeId: "bkci",
		SpaceId:     "test",
		SpaceName:   "Test",
		SpaceCode:   "bkci__3",
		Status:      "normal",
		TimeZone:    "Asia/Shanghai",
		Language:    "zh-hans",
		IsBcsValid:  true,
		BkTenantId:  "system",
	},
}

func TestBusinessCacheManager(t *testing.T) {
	// mock相关接口调用与数据库查询
	batchApiRequestPatch := gomonkey.ApplyFunc(api.BatchApiRequest, func(pageSize int, getTotalFunc func(any) (int, error), getReqFunc func(page int) define.Operation, concurrency int) ([]any, error) {
		result := make([]any, len(DemoBusinesses))
		for i, v := range DemoBusinesses {
			result[i] = v
		}
		return result, nil
	})
	defer batchApiRequestPatch.Reset()
	getBusinessAttributePatch := gomonkey.ApplyFunc(getBusinessAttribute, func(ctx context.Context) ([]cmdb.SearchObjectAttributeData, error) {
		return BusinessAttrs, nil
	})
	defer getBusinessAttributePatch.Reset()
	getSpaceListPatch := gomonkey.ApplyFunc(getSpaceList, func() ([]space.Space, error) {
		return DemoSpaces, nil
	})
	defer getSpaceListPatch.Reset()

	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	client, _ := redis.GetClient(rOpts)
	ctx := context.Background()

	t.Run("TestBusinessCacheManager", func(t *testing.T) {
		// 创建业务缓存管理器
		cacheManager, err := NewBusinessCacheManager(tenant.DefaultTenantId, t.Name(), rOpts, 1)
		if err != nil {
			t.Error(err)
			return
		}

		// 刷新业务缓存
		err = cacheManager.RefreshGlobal(ctx)
		if err != nil {
			t.Error(err)
			return
		}

		result := client.HGetAll(ctx, cacheManager.GetCacheKey(businessCacheKey))
		if result.Err() != nil {
			t.Error(result.Err())
			return
		}

		businesses := make(map[string]map[string]any)
		for k, v := range result.Val() {
			var business map[string]any
			err := json.Unmarshal([]byte(v), &business)
			if err != nil {
				t.Error(err)
				return
			}

			businesses[k] = business
		}

		// 检查业务缓存数据
		assert.Len(t, businesses, 3)
		assert.EqualValues(t, businesses["2"]["bk_biz_name"], "BlueKing")
		assert.EqualValues(t, businesses["3"]["bk_biz_name"], "Test")
		assert.EqualValues(t, businesses["-2"]["bk_biz_name"], "[test]Test")

		for _, biz := range businesses {
			_, ok := biz["operator"].([]any)
			assert.Truef(t, ok, "operator type error, %v", biz["operator"])
			assert.EqualValues(t, biz["bk_tenant_id"], tenant.DefaultTenantId)
		}

		assert.EqualValues(t, businesses["2"]["bk_pmp_qa"], []any{"user1", "user2"})
		assert.EqualValues(t, businesses["2"]["bk_pmp_qa2"], "user1,user2")

		// 清理业务缓存
		cacheManager.initUpdatedFieldSet(businessCacheKey)
		err = cacheManager.CleanGlobal(ctx)
		if err != nil {
			t.Error(err)
			return
		}

		// 检查业务缓存数据
		exists := client.Exists(ctx, cacheManager.GetCacheKey(businessCacheKey))
		assert.EqualValues(t, 0, exists.Val())
	})

	t.Run("Event", func(t *testing.T) {
		// 创建业务缓存管理器
		cacheManager, err := NewBusinessCacheManager(tenant.DefaultTenantId, t.Name(), rOpts, 1)
		if err != nil {
			t.Error(err)
			return
		}

		err = cacheManager.UpdateByEvents(ctx, "biz", []map[string]any{
			{"bk_biz_id": float64(2)},
		})
		if err != nil {
			t.Error(err)
			return
		}

		assert.Len(t, client.HKeys(ctx, cacheManager.GetCacheKey(businessCacheKey)).Val(), 3)

		err = cacheManager.CleanByEvents(ctx, "biz", []map[string]any{
			{"bk_biz_id": float64(2)},
		})
		err = cacheManager.CleanByEvents(ctx, "other", []map[string]any{
			{"bk_biz_id": float64(3)},
		})
		err = cacheManager.UpdateByEvents(ctx, "other", []map[string]any{
			{"bk_biz_id": float64(3)},
		})

		assert.Len(t, client.HKeys(ctx, cacheManager.GetCacheKey(businessCacheKey)).Val(), 2)
	})
}

func TestMultiTenantBusinessCacheManager(t *testing.T) {
	getBusinessListPatch := gomonkey.ApplyFunc(getBusinessList, func(ctx context.Context, bkTenantId string) ([]map[string]any, error) {
		if bkTenantId == tenant.DefaultTenantId {
			return DefaultTenantBusinesses, nil
		}
		return Tenant1Businesses, nil
	})
	defer getBusinessListPatch.Reset()

	getSpaceListPatch := gomonkey.ApplyFunc(getSpaceList, func() ([]space.Space, error) {
		return DemoSpaces, nil
	})
	defer getSpaceListPatch.Reset()

	listTenantPatch := gomonkey.ApplyFunc(tenant.GetTenantList, func() ([]user.ListTenantData, error) {
		return []user.ListTenantData{
			{Id: tenant.DefaultTenantId, Name: "System", Status: "normal"},
			{Id: "tenant1", Name: "Tenant1", Status: "normal"},
		}, nil
	})
	defer listTenantPatch.Reset()

	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	client, _ := redis.GetClient(rOpts)
	ctx := context.Background()

	cacheManager, err := NewBusinessCacheManager(tenant.DefaultTenantId, t.Name(), rOpts, 1)
	if err != nil {
		t.Error(err)
		return
	}

	err = cacheManager.RefreshGlobal(ctx)
	if err != nil {
		t.Error(err)
		return
	}

	result := client.HGetAll(ctx, cacheManager.GetCacheKey(businessCacheKey))
	if result.Err() != nil {
		t.Error(result.Err())
		return
	}

	businesses := make(map[string]map[string]any)
	for k, v := range result.Val() {
		var business map[string]any
		err := json.Unmarshal([]byte(v), &business)
		if err != nil {
			t.Error(err)
			return
		}

		businesses[k] = business
	}

	assert.Len(t, businesses, 4)
	assert.EqualValues(t, businesses["2"]["bk_biz_name"], "BlueKing")
	assert.EqualValues(t, businesses["3"]["bk_biz_name"], "Test")
	assert.EqualValues(t, businesses["-2"]["bk_biz_name"], "[test]Test")
	assert.EqualValues(t, businesses["4"]["bk_biz_name"], "Test2")
}
