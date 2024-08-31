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
	patch := gomonkey.ApplyFunc(getSetListByBizID, func(ctx context.Context, bizID int) ([]map[string]interface{}, error) {
		demoSets := make([]map[string]interface{}, 0)
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
		cacheManager, err := NewSetCacheManager(t.Name(), rOpts, 1)
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
}
