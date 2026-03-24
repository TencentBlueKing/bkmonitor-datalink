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

var demoSetStr = `
[
	{
		"bk_biz_id":         2,
		"bk_set_id":         1,
		"bk_set_name":       "set1",
		"set_template_id":   1,
		"bk_set_env":        "test",
		"bk_set_desc":       "desc1",
		"bk_service_status": "1",
		"description":       "desc"
	},
	{
		"bk_biz_id":         2,
		"bk_set_id":         2,
		"bk_set_name":       "set2",
		"set_template_id":   1,
		"bk_set_env":        "test",
		"bk_set_desc":       "desc2",
		"bk_service_status": "1",
		"description":       "desc"
	},
	{
		"bk_biz_id":         2,
		"bk_set_id":         3,
		"bk_set_name":       "set3",
		"set_template_id":   2,
		"bk_set_env":        "test",
		"bk_set_desc":       "desc3",
		"bk_service_status": "1",
		"description":       "desc"
	}
]
`

func TestSetCacheManager(t *testing.T) {
	patch := gomonkey.ApplyFunc(getSetListByBizID, func(ctx context.Context, bkTenantId string, bizID int) ([]map[string]any, error) {
		demoSets := make([]map[string]any, 0)
		err := json.Unmarshal([]byte(demoSetStr), &demoSets)
		if err != nil {
			return nil, err
		}
		return demoSets, nil
	})
	defer patch.Reset()

	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	client, _ := redis.GetClient(rOpts)
	ctx := context.Background()

	t.Run("TestSetCacheManager", func(t *testing.T) {
		cacheManager, err := NewSetCacheManager(tenant.DefaultTenantId, t.Name(), rOpts, 1)
		if err != nil {
			t.Error(err)
			return
		}

		err = cacheManager.RefreshByBiz(ctx, 2)
		if err != nil {
			t.Error(err)
			return
		}

		assert.EqualValues(t, 3, client.HLen(ctx, cacheManager.GetCacheKey(setCacheKey)).Val())
		assert.EqualValues(t, 2, client.HLen(ctx, cacheManager.GetCacheKey(setTemplateCacheKey)).Val())

		cacheManager.initUpdatedFieldSet(setCacheKey, setTemplateCacheKey)
		err = cacheManager.CleanGlobal(ctx)
		if err != nil {
			t.Error(err)
			return
		}

		assert.EqualValues(t, 0, client.HLen(ctx, cacheManager.GetCacheKey(setCacheKey)).Val())
		assert.EqualValues(t, 0, client.HLen(ctx, cacheManager.GetCacheKey(setTemplateCacheKey)).Val())
	})

	t.Run("TestSetCacheManager_Events", func(t *testing.T) {
		cacheManager, err := NewSetCacheManager(tenant.DefaultTenantId, t.Name(), rOpts, 1)
		if err != nil {
			t.Error(err)
			return
		}

		events := []map[string]any{
			{
				"bk_biz_id": float64(2),
				"bk_set_id": float64(1),
			},
		}

		err = cacheManager.UpdateByEvents(ctx, "set", events)
		if err != nil {
			t.Error(err)
			return
		}

		assert.EqualValues(t, 3, client.HLen(ctx, cacheManager.GetCacheKey(setCacheKey)).Val())
		assert.EqualValues(t, 2, client.HLen(ctx, cacheManager.GetCacheKey(setTemplateCacheKey)).Val())

		events = []map[string]any{
			{
				"bk_biz_id":       float64(2),
				"bk_set_id":       float64(1),
				"set_template_id": float64(1),
			},
			{
				"bk_biz_id":       float64(2),
				"bk_set_id":       float64(3),
				"set_template_id": float64(2),
			},
		}

		err = cacheManager.CleanByEvents(ctx, "set", events)
		if err != nil {
			t.Error(err)
			return
		}

		assert.EqualValues(t, 1, client.HLen(ctx, cacheManager.GetCacheKey(setCacheKey)).Val())
		assert.EqualValues(t, 1, client.HLen(ctx, cacheManager.GetCacheKey(setTemplateCacheKey)).Val())
		assert.EqualValues(t, `[2]`, client.HGet(ctx, cacheManager.GetCacheKey(setTemplateCacheKey), "1").Val())
	})
}
