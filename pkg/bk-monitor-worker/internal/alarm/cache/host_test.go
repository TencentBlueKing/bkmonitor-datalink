// Copyright 2024 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
