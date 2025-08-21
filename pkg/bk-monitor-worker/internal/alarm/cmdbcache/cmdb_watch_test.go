// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

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
// 	//redisOptions := redis.Options{
// 	//	Mode:  "standalone",
// 	//	Addrs: []string{"127.0.0.1:6379"},
// 	//}

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
