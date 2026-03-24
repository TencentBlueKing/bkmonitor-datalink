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

	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
)

var demoModuleStr = `
	[{
		"bk_biz_id":           2,
		"bk_module_id":        1,
		"bk_module_name":      "module1",
		"bk_set_id":           2,
		"bk_bak_operator":     "admin",
		"operator":            "admin",
		"service_category_id": 1,
		"service_template_id": 1,
		"set_template_id":     1
	},
	{
		"bk_biz_id":           2,
		"bk_module_id":        2,
		"bk_module_name":      "module2",
		"bk_set_id":           2,
		"bk_bak_operator":     "admin",
		"operator":            "admin,user1",
		"service_category_id": 1,
		"service_template_id": 2,
		"set_template_id":     1
	},
	{
		"bk_biz_id":           2,
		"bk_module_id":        3,
		"bk_module_name":      "module3",
		"bk_set_id":           3,
		"bk_bak_operator":     "admin",
		"operator":            "admin,user1",
		"service_category_id": 1,
		"service_template_id": 2,
		"set_template_id":     1
	}]
`

func TestModuleCacheManager(t *testing.T) {
	patch := gomonkey.ApplyFunc(getModuleListByBizID, func(ctx context.Context, bkTenantId string, bizID int) ([]map[string]any, error) {
		var demoModules []map[string]any
		err := json.Unmarshal([]byte(demoModuleStr), &demoModules)
		if err != nil {
			return nil, err
		}
		return demoModules, nil
	})
	defer patch.Reset()

	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}
	client, _ := redis.GetClient(rOpts)
	ctx := context.Background()

	t.Run("TestModuleCacheManager", func(t *testing.T) {
		cacheManager, err := NewModuleCacheManager(tenant.DefaultTenantId, t.Name(), rOpts, 1)
		if err != nil {
			t.Error(err)
			return
		}

		err = cacheManager.RefreshByBiz(ctx, 2)
		if err != nil {
			t.Error(err)
			return
		}

		assert.EqualValues(t, 3, client.HLen(ctx, cacheManager.GetCacheKey(moduleCacheKey)).Val())
		assert.EqualValues(t, 2, client.HLen(ctx, cacheManager.GetCacheKey(serviceTemplateCacheKey)).Val())

		cacheManager.initUpdatedFieldSet(moduleCacheKey, serviceTemplateCacheKey)
		err = cacheManager.CleanGlobal(ctx)
		if err != nil {
			t.Error(err)
			return
		}

		assert.EqualValues(t, 0, client.HLen(ctx, cacheManager.GetCacheKey(moduleCacheKey)).Val())
		assert.EqualValues(t, 0, client.HLen(ctx, cacheManager.GetCacheKey(serviceTemplateCacheKey)).Val())
	})

	t.Run("TestModuleCacheManager_Events", func(t *testing.T) {
		cacheManager, err := NewModuleCacheManager(tenant.DefaultTenantId, t.Name(), rOpts, 1)
		if err != nil {
			t.Error(err)
			return
		}

		events := []map[string]any{
			{
				"bk_biz_id":    float64(2),
				"bk_module_id": float64(1),
			},
		}

		err = cacheManager.UpdateByEvents(ctx, "module", events)
		if err != nil {
			t.Error(err)
			return
		}

		assert.EqualValues(t, 3, client.HLen(ctx, cacheManager.GetCacheKey(moduleCacheKey)).Val())
		assert.EqualValues(t, 2, client.HLen(ctx, cacheManager.GetCacheKey(serviceTemplateCacheKey)).Val())

		events = []map[string]any{
			{
				"bk_biz_id":           float64(2),
				"bk_module_id":        float64(1),
				"service_template_id": float64(1),
			},
			{
				"bk_biz_id":           float64(2),
				"bk_module_id":        float64(2),
				"service_template_id": float64(2),
			},
		}

		err = cacheManager.CleanByEvents(ctx, "module", events)
		if err != nil {
			t.Error(err)
			return
		}

		assert.EqualValues(t, 1, client.HLen(ctx, cacheManager.GetCacheKey(moduleCacheKey)).Val())
		assert.EqualValues(t, 1, client.HLen(ctx, cacheManager.GetCacheKey(serviceTemplateCacheKey)).Val())
		assert.EqualValues(t, "[3]", client.HGet(ctx, cacheManager.GetCacheKey(serviceTemplateCacheKey), "2").Val())
	})
}
