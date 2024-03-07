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
	"unsafe"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
)

func TestApiRequest(t *testing.T) {
	cmdbApi, _ := api.GetCmdbApi()

	t.Run("TestListBizHostsTopo", func(t *testing.T) {
		resp := cmdb.ListBizHostsTopoResp{}
		response, err := cmdbApi.ListBizHostsTopo().SetResult(&resp).SetBody(map[string]interface{}{"bk_biz_id": 2, "page": map[string]int{"start": 0, "limit": 500}, "fields": HostFields}).Request()
		if err != nil {
			t.Error(err)
			return
		}

		fmt.Println(response.StatusCode)
		fmt.Println(resp.Result)
		for _, host := range resp.Data.Info {
			fmt.Println(host.Host.BkHostInnerip)
		}
	})

	t.Run("getHostAndTopoByBiz", func(t *testing.T) {
		hosts, topo, err := getHostAndTopoByBiz(2)
		if err != nil {
			t.Error(err)
			return
		}
		for _, host := range hosts {
			fmt.Printf("%+v\n", host)
			fmt.Printf("%d\n", unsafe.Sizeof(*host))
		}

		fmt.Println(topo)
	})
}

func TestHostAndTopoCacheManager(t *testing.T) {
	rOpts := &alarm.RedisOptions{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}
	cacheManager, err := NewCacheManagerByType(rOpts, "test", "host_topo")
	if err != nil {
		t.Error(err)
		return
	}

	client, err := alarm.GetRedisClient(rOpts)
	ctx := context.Background()

	t.Run("RefreshByBiz", func(t *testing.T) {
		err := cacheManager.RefreshByBiz(ctx, 2)
		if err != nil {
			t.Error(err)
			return
		}

		// check cache
		result := client.Keys(ctx, "test.cmdb.*")
		if result.Err() != nil {
			t.Error(result.Err())
			return
		}
		keys := result.Val()

		assert.Equal(t, 4, len(keys))
	})
}
