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
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/space"
)

var DemoBusinesses = []map[string]interface{}{
	{
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
	{
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
	},
}

func TestBusinessCacheManager(t *testing.T) {
	// mock相关接口调用与数据库查询
	getBusinessListPatch := gomonkey.ApplyFunc(getBusinessList, func(ctx context.Context) ([]map[string]interface{}, error) {
		return DemoBusinesses, nil
	})
	defer getBusinessListPatch.Reset()
	getBusinessAttributePatch := gomonkey.ApplyFunc(getBusinessAttribute, func(ctx context.Context) ([]cmdb.SearchObjectAttributeData, error) {
		return BusinessAttrs, nil
	})
	defer getBusinessAttributePatch.Reset()
	getSpaceListPatch := gomonkey.ApplyFunc(getSpaceList, func() ([]space.Space, error) {
		return DemoSpaces, nil
	})
	defer getSpaceListPatch.Reset()

	rOpts := &redis.RedisOptions{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	client, _ := redis.GetRedisClient(rOpts)
	ctx := context.Background()

	t.Run("TestBusinessCacheManager", func(t *testing.T) {
		// 创建业务缓存管理器
		cacheManager, err := NewBusinessCacheManager(t.Name(), rOpts)
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

		businesses := make(map[string]map[string]interface{})
		for k, v := range result.Val() {
			var business map[string]interface{}
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
			_, ok := biz["operator"].([]interface{})
			assert.Truef(t, ok, "operator type error, %v", biz["operator"])
		}

		assert.EqualValues(t, businesses["2"]["bk_pmp_qa"], []interface{}{"user1", "user2"})
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
		cacheManager, err := NewBusinessCacheManager(t.Name(), rOpts)
		if err != nil {
			t.Error(err)
			return
		}

		err = cacheManager.UpdateByEvents(ctx, "biz", []map[string]interface{}{
			{"bk_biz_id": 2},
		})
		if err != nil {
			t.Error(err)
			return
		}

		assert.Len(t, client.HKeys(ctx, cacheManager.GetCacheKey(businessCacheKey)).Val(), 3)

		err = cacheManager.CleanByEvents(ctx, "biz", []map[string]interface{}{
			{"bk_biz_id": 2},
		})
		err = cacheManager.CleanByEvents(ctx, "other", []map[string]interface{}{
			{"bk_biz_id": 3},
		})
		err = cacheManager.UpdateByEvents(ctx, "other", []map[string]interface{}{
			{"bk_biz_id": 3},
		})

		assert.Len(t, client.HKeys(ctx, cacheManager.GetCacheKey(businessCacheKey)).Val(), 2)
	})
}

// 真实运行测试
//func TestRunBusinessCache(t *testing.T) {
//	rOpts := &redis.RedisOptions{
//		Mode:  "standalone",
//		Addrs: []string{"127.0.0.1:6379"},
//	}
//
//	ctx := context.Background()
//
//	cm, err := NewCacheManagerByType(rOpts, t.Name(), "business")
//	if err != nil {
//		t.Fatal(err)
//	}
//
//	err = cm.RefreshGlobal(ctx)
//	if err != nil {
//		t.Fatal(err)
//	}
//
//	err = cm.CleanGlobal(ctx)
//	if err != nil {
//		t.Fatal(err)
//	}
//}
