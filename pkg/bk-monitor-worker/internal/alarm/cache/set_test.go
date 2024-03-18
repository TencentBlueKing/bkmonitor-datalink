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
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
)

func TestSetCacheManager(t *testing.T) {
	rOpts := &redis.RedisOptions{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}
	cacheManager, err := NewCacheManagerByType(rOpts, "test", "set")
	if err != nil {
		t.Error(err)
		return
	}

	client, err := redis.GetRedisClient(rOpts)
	ctx := context.Background()

	t.Run("TestSetCacheManager", func(t *testing.T) {
		err := cacheManager.RefreshByBiz(ctx, 2)
		if err != nil {
			t.Error(err)
			return
		}

		exists := client.Exists(ctx, "test.cmdb.set")
		if exists.Val() != 1 {
			t.Error("set cache failed")
			return
		}

		exists = client.Exists(ctx, "test.cmdb.set_template")
		if exists.Val() != 1 {
			t.Error("set_template cache failed")
			return
		}

		result, err := client.HGetAll(ctx, "test.cmdb.set").Result()
		if err != nil {
			t.Error(err)
			return
		}
		fmt.Println(result)
	})

}
