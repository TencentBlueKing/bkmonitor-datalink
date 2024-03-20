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
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
)

var demoSets = []cmdb.SearchSetData{
	{
		BkBizId:         2,
		BkSetId:         1,
		BkSetName:       "set1",
		SetTemplateId:   1,
		BkSetEnv:        "test",
		BkSetDesc:       "desc1",
		BkServiceStatus: "1",
		Description:     "desc",
	},
	{
		BkBizId:         2,
		BkSetId:         2,
		BkSetName:       "set2",
		SetTemplateId:   1,
		BkSetEnv:        "test",
		BkSetDesc:       "desc2",
		BkServiceStatus: "1",
		Description:     "desc",
	},
}

func TestSetCacheManager(t *testing.T) {
	patch := gomonkey.ApplyFunc(getSetListByBizID, func(ctx context.Context, bizID int) ([]cmdb.SearchSetData, error) {
		return demoSets, nil
	})
	defer patch.Reset()

	rOpts := &redis.RedisOptions{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	client, _ := redis.GetRedisClient(rOpts)
	ctx := context.Background()

	t.Run("TestSetCacheManager", func(t *testing.T) {
		cacheManager, err := NewSetCacheManager(t.Name(), rOpts)
		if err != nil {
			t.Error(err)
			return
		}

		err = cacheManager.RefreshByBiz(ctx, 2)
		if err != nil {
			t.Error(err)
			return
		}

		assert.EqualValues(t, 2, client.HLen(ctx, cacheManager.GetCacheKey(setCacheKey)).Val())
		assert.EqualValues(t, 1, client.HLen(ctx, cacheManager.GetCacheKey(setTemplateCacheKey)).Val())

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
