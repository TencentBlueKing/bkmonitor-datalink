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

package cmdbcache

import (
	"context"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
)

var demoModules = []cmdb.SearchModuleData{
	{
		BkBizId:           2,
		BkModuleId:        1,
		BkModuleName:      "module1",
		BkSetId:           2,
		BkBakOperator:     "admin",
		Operator:          "admin",
		ServiceCategoryId: 1,
		ServiceTemplateId: 1,
		SetTemplateId:     1,
	},
	{
		BkBizId:           2,
		BkModuleId:        2,
		BkModuleName:      "module2",
		BkSetId:           2,
		BkBakOperator:     "admin",
		Operator:          "admin,user1",
		ServiceCategoryId: 1,
		ServiceTemplateId: 2,
		SetTemplateId:     1,
	},
}

func TestModuleCacheManager(t *testing.T) {
	patch := gomonkey.ApplyFunc(getModuleListByBizID, func(ctx context.Context, bizID int) ([]cmdb.SearchModuleData, error) {
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
		cacheManager, err := NewModuleCacheManager(t.Name(), rOpts, 1)
		if err != nil {
			t.Error(err)
			return
		}

		err = cacheManager.RefreshByBiz(ctx, 2)
		if err != nil {
			t.Error(err)
			return
		}

		assert.EqualValues(t, 2, client.HLen(ctx, cacheManager.GetCacheKey(moduleCacheKey)).Val())
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
}
