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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
)

func TestResourceWatchWithConfiguredAdmin(t *testing.T) {
	const businessTenant = "system"
	t.Setenv("BK_ADMIN_USERNAME", "watcher-login")
	t.Setenv("BKAPP_USER_API_BASE_URL", "")
	oldURL, oldMode := cfg.BkApiCmdbApiGatewayUrl, cfg.EnableMultiTenantMode
	t.Cleanup(func() {
		cfg.BkApiCmdbApiGatewayUrl, cfg.EnableMultiTenantMode = oldURL, oldMode
		delete(cmdbApiClients, businessTenant)
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/bk-user/prod/api/v3/open/tenant/virtual-users/-/lookup/":
			assert.Equal(t, "default", r.Header.Get("X-Bk-Tenant-Id"))
			fmt.Fprint(w, `{"data":[{"bk_username":"watcher-account"}]}`)
		case "/api/v3/event/watch/resource/host":
			assert.Equal(t, businessTenant, r.Header.Get("X-Bk-Tenant-Id"))
			var auth map[string]string
			assert.NoError(t, json.Unmarshal([]byte(r.Header.Get("X-Bkapi-Authorization")), &auth))
			assert.Equal(t, "watcher-account", auth["bk_username"])
			fmt.Fprint(w, `{"result":true,"data":{"bk_watched":false}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("BK_COMPONENT_API_URL", server.URL+"/")
	cfg.BkApiCmdbApiGatewayUrl, cfg.EnableMultiTenantMode = server.URL, false
	opts := &redis.Options{Mode: "standalone", Addrs: []string{testRedisAddr}}
	watcher, err := NewCmdbResourceWatcher(businessTenant, "test", opts)
	require.NoError(t, err)
	defer watcher.redisClient.Close()
	_, err = watcher.Watch(context.Background(), CmdbResourceTypeHost)
	require.NoError(t, err)
}

// import (
// 	"fmt"
// 	"testing"

// 	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
// 	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
// 	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
// )

//func TestResourceWatch(t *testing.T) {
//	redisOptions := redis.Options{
//		Mode:  "standalone",
//		Addrs: []string{"127.0.0.1:6379"},
//	}
//
//	// 系统信号
//	signalChan := make(chan os.Signal, 1)
//	signal.Notify(signalChan, os.Interrupt, os.Kill)
//
//	//调用cancel函数取消
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//
//	// 监听信号
//	go func() {
//		<-signalChan
//		cancel()
//	}()
//
//	prefix := t.Name()
//
//	wg := &sync.WaitGroup{}
//	wg.Add(1)
//
//	//go func() {
//	//	defer cancel()
//	//	defer wg.Done()
//	//
//	//	params := &WatchCmdbResourceChangeEventTaskParams{
//	//		Redis:  redisOptions,
//	//		Prefix: prefix,
//	//	}
//	//	payload, _ := json.Marshal(params)
//	//	if err := WatchCmdbResourceChangeEventTask(ctx, payload); err != nil {
//	//		t.Errorf("TestWatch failed, err: %v", err)
//	//		return
//	//	}
//	//}()
//
//	go func() {
//		defer cancel()
//		defer wg.Done()
//
//		params := &RefreshTaskParams{
//			Redis:                redisOptions,
//			Prefix:               prefix,
//			EventHandleInterval:  60,
//			CacheTypes:           []string{"host_topo"},
//			FullRefreshIntervals: map[string]int{"host_topo": 1800, "business": 1800, "module": 1800, "set": 1800, "service_instance": 60},
//		}
//		payload, _ := json.Marshal(params)
//		if err := CacheRefreshTask(ctx, payload); err != nil {
//			t.Errorf("TestHandle failed, err: %v", err)
//			return
//		}
//	}()
//
//	wg.Wait()
//}

// func TestManager(t *testing.T) {
// 	cmdbApi, err := api.GetCmdbApi(tenant.DefaultTenantId)
// 	if err != nil {
// 		t.Errorf("TestManager failed, err: %v", err)
// 		return
// 	}

// 	var result cmdb.SearchBusinessResp
// 	_, err = cmdbApi.SearchBusiness().SetPathParams(map[string]string{"bk_supplier_account": "0"}).SetResult(&result).Request()
// 	if err != nil {
// 		t.Errorf("TestManager failed, err: %v", err)
// 		return
// 	}
// 	fmt.Printf("result: %v\n", result)
// }
