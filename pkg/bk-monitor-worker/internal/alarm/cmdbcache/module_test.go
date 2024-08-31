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
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
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
	patch := gomonkey.ApplyFunc(getModuleListByBizID, func(ctx context.Context, bizID int) ([]map[string]interface{}, error) {
		var demoModules []map[string]interface{}
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
}
