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
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
)

func TestGetEvents(t *testing.T) {
	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	client, _ := redis.GetClient(rOpts)
	ctx := context.Background()

	// 注入测试数据
	eventData := []interface{}{
		"{\"bk_cursor\":\"123\",\"bk_resource\":\"host_relation\",\"bk_event_type\":\"delete\",\"bk_detail\":{\"bk_biz_id\":2,\"bk_host_id\":1,\"bk_module_id\":1,\"bk_set_id\":1,\"bk_supplier_account\":\"0\"}}",
		"{\"bk_cursor\":\"124\",\"bk_resource\":\"host_relation\",\"bk_event_type\":\"create\",\"bk_detail\":{\"bk_biz_id\":2,\"bk_host_id\":2,\"bk_module_id\":2,\"bk_set_id\":2,\"bk_supplier_account\":\"0\"}}",
	}

	handler, err := NewCmdbEventHandler(t.Name(), rOpts, map[string]time.Duration{"host_topo": 61 * time.Second, "set": time.Second}, 1)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// 验证刷新间隔设置
	assert.EqualValues(t, handler.getFullRefreshInterval("host_topo"), 61*time.Second)

	// 验证最小刷新间隔1分钟
	assert.EqualValues(t, handler.getFullRefreshInterval("set"), time.Minute)

	// 验证默认值10分钟
	assert.EqualValues(t, handler.getFullRefreshInterval("module"), DefaultFullRefreshInterval)

	key := handler.getEventKey("host_relation")
	client.RPush(ctx, key, eventData...)

	// 获取事件
	events, err := handler.getEvents(ctx, CmdbResourceTypeHostRelation)
	if err != nil {
		t.Fatalf("failed to get events: %v", err)
	}

	assert.EqualValues(t, len(events), 2)

	// 验证事件内容
	assert.EqualValues(t, events[0].BkCursor, "123")
	assert.EqualValues(t, events[0].BkResource, "host_relation")
	assert.EqualValues(t, events[0].BkEventType, "delete")
	assert.EqualValues(t, events[1].BkCursor, "124")
	assert.EqualValues(t, events[1].BkResource, "host_relation")
	assert.EqualValues(t, events[1].BkEventType, "create")
}

func TestIfRunRefreshAll(t *testing.T) {
	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	client, _ := redis.GetClient(rOpts)
	ctx := context.Background()

	cacheType := "host_topo"
	handler, err := NewCmdbEventHandler(t.Name(), rOpts, map[string]time.Duration{cacheType: 61 * time.Second}, 1)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	now := time.Now()
	client.Set(ctx, handler.getLastUpdateTimeKey(cacheType), now.Add(-60*time.Second).Unix(), 0)

	// 验证全量刷新时间间隔判断
	assert.False(t, handler.ifRunRefreshAll(ctx, cacheType, now.Unix()))
	assert.False(t, handler.ifRunRefreshAll(ctx, cacheType, now.Add(time.Second).Unix()))
	assert.True(t, handler.ifRunRefreshAll(ctx, cacheType, now.Add(2*time.Second).Unix()))
}

func TestRefreshAll(t *testing.T) {
	getBusinessListPatch := gomonkey.ApplyFunc(getBusinessList, func(ctx context.Context) ([]map[string]interface{}, error) {
		return DemoBusinesses, nil
	})
	defer getBusinessListPatch.Reset()

	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	ctx := context.Background()

	t.Run("TestRefreshAllWithBiz", func(t *testing.T) {
		cm, _ := NewSetCacheManager(t.Name(), rOpts, 1)
		refreshByBizCount := 0
		patchRefreshByBiz := gomonkey.ApplyMethod(reflect.TypeOf(cm), "RefreshByBiz", func(cm *SetCacheManager, ctx context.Context, bizID int) error {
			refreshByBizCount++
			return nil
		})
		defer patchRefreshByBiz.Reset()

		refreshGlobalCount := 0
		patchRefreshGlobal := gomonkey.ApplyMethod(reflect.TypeOf(cm), "RefreshGlobal", func(cm *SetCacheManager, ctx context.Context) error {
			refreshGlobalCount++
			return nil
		})
		defer patchRefreshGlobal.Reset()

		cleanGlobalCount := 0
		patchCleanGlobal := gomonkey.ApplyMethod(reflect.TypeOf(cm), "CleanGlobal", func(cm *SetCacheManager, ctx context.Context) error {
			cleanGlobalCount++
			return nil
		})
		defer patchCleanGlobal.Reset()

		err := RefreshAll(ctx, cm, 1)
		if err != nil {
			t.Fatalf("RefreshAll failed, err: %v", err)
		}

		assert.Equal(t, refreshByBizCount, 2)
		assert.Equal(t, refreshGlobalCount, 1)
		assert.Equal(t, cleanGlobalCount, 1)
	})

	t.Run("TestRefreshAllWithoutBiz", func(t *testing.T) {
		cm, _ := NewBusinessCacheManager(t.Name(), rOpts, 1)
		refreshByBizCount := 0
		patchRefreshByBiz := gomonkey.ApplyMethod(reflect.TypeOf(cm), "RefreshByBiz", func(cm *BusinessCacheManager, ctx context.Context, bizID int) error {
			refreshByBizCount++
			return nil
		})
		defer patchRefreshByBiz.Reset()

		refreshGlobalCount := 0
		patchRefreshGlobal := gomonkey.ApplyMethod(reflect.TypeOf(cm), "RefreshGlobal", func(cm *BusinessCacheManager, ctx context.Context) error {
			refreshGlobalCount++
			return nil
		})
		defer patchRefreshGlobal.Reset()

		cleanGlobalCount := 0
		patchCleanGlobal := gomonkey.ApplyMethod(reflect.TypeOf(cm), "CleanGlobal", func(cm *BusinessCacheManager, ctx context.Context) error {
			cleanGlobalCount++
			return nil
		})
		defer patchCleanGlobal.Reset()

		err := RefreshAll(ctx, cm, 1)
		if err != nil {
			t.Fatalf("RefreshAll failed, err: %v", err)
		}

		assert.Equal(t, refreshByBizCount, 0)
		assert.Equal(t, refreshGlobalCount, 1)
		assert.Equal(t, cleanGlobalCount, 1)
	})
}

func TestRunRefreshAll(t *testing.T) {
	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	client, _ := redis.GetClient(rOpts)
	ctx := context.Background()

	handler, err := NewCmdbEventHandler(t.Name(), rOpts, map[string]time.Duration{}, 1)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	refreshAllCount := 0
	patchRefreshAll := gomonkey.ApplyFunc(RefreshAll, func(ctx context.Context, cacheManager Manager, concurrentLimit int) error {
		refreshAllCount++
		return nil
	})
	defer patchRefreshAll.Reset()

	now := time.Now()
	handler.runRefreshAll(ctx)

	// 验证RefreshAll调用次数
	assert.Equal(t, refreshAllCount, len(handler.cacheManagers))

	// 验证全量刷新时间戳
	for cacheType := range handler.cacheManagers {
		lastUpdateTimeKey := handler.getLastUpdateTimeKey(cacheType)
		lastUpdateTime, _ := client.Get(ctx, lastUpdateTimeKey).Int64()
		t.Logf("lastUpdateTime: %d", lastUpdateTime)
		assert.True(t, lastUpdateTime >= now.Unix())
	}
}

func TestPreprocessEvent(t *testing.T) {
	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	ctx := context.Background()

	// 测试用例
	testCases := []struct {
		name         string
		resourceType CmdbResourceType
		events       []string
		checkFunc    func(*testing.T, *CmdbEventHandler)
	}{
		{
			name:         "BizCreate",
			resourceType: CmdbResourceTypeBiz,
			events: []string{
				`{"bk_cursor":"1","bk_resource":"biz","bk_event_type":"create","bk_detail":{"bk_biz_id":1}}`,
			},
			checkFunc: func(t *testing.T, handler *CmdbEventHandler) {
				assert.Equal(t, handler.refreshBiz, true)
			},
		},
		{
			name:         "BizDelete",
			resourceType: CmdbResourceTypeBiz,
			events: []string{
				`{"bk_cursor":"1","bk_resource":"biz","bk_event_type":"delete","bk_detail":{"bk_biz_id":1}}`,
			},
			checkFunc: func(t *testing.T, handler *CmdbEventHandler) {
				assert.Equal(t, handler.refreshBiz, true)
			},
		},
		{
			name:         "BizUpdate",
			resourceType: CmdbResourceTypeBiz,
			events: []string{
				`{"bk_cursor":"1","bk_resource":"biz","bk_event_type":"update","bk_detail":{"bk_biz_id":1}}`,
			},
			checkFunc: func(t *testing.T, handler *CmdbEventHandler) {
				assert.Equal(t, handler.refreshBiz, true)
			},
		},
		{
			name:         "Set",
			resourceType: CmdbResourceTypeSet,
			events: []string{
				`{"bk_cursor":"1","bk_resource":"set","bk_event_type":"update","bk_detail":{"bk_biz_id":1,"bk_set_id":10, "set_template_id":1}}`,
				`{"bk_cursor":"2","bk_resource":"set","bk_event_type":"create","bk_detail":{"bk_biz_id":2,"bk_set_id":11, "set_template_id":2}}`,
				`{"bk_cursor":"3","bk_resource":"set","bk_event_type":"delete","bk_detail":{"bk_biz_id":2,"bk_set_id":12, "set_template_id":3}}`,
			},
			checkFunc: func(t *testing.T, handler *CmdbEventHandler) {
				_, ok := handler.refreshBizSet.Load(1)
				assert.True(t, ok)

				_, ok = handler.refreshBizSet.Load(2)
				assert.True(t, ok)

				_, ok = handler.cleanSetKeys.Load(12)
				assert.True(t, ok)

				_, ok = handler.cleanSetKeys.Load(10)
				assert.False(t, ok)

				_, ok = handler.cleanSetKeys.Load(11)
				assert.False(t, ok)

				_, ok = handler.cleanSetTemplateIds.Load(3)
				assert.True(t, ok)
			},
		},
		{
			name:         "Module",
			resourceType: CmdbResourceTypeModule,
			events: []string{
				`{"bk_cursor":"1","bk_resource":"module","bk_event_type":"update","bk_detail":{"bk_biz_id":1,"bk_module_id":10, "service_template_id":1}}`,
				`{"bk_cursor":"2","bk_resource":"module","bk_event_type":"create","bk_detail":{"bk_biz_id":2,"bk_module_id":11, "service_template_id":2}}`,
				`{"bk_cursor":"3","bk_resource":"module","bk_event_type":"delete","bk_detail":{"bk_biz_id":2,"bk_module_id":12, "service_template_id":3}}`,
			},
			checkFunc: func(t *testing.T, handler *CmdbEventHandler) {
				_, ok := handler.refreshBizModule.Load(1)
				assert.True(t, ok)

				_, ok = handler.refreshBizModule.Load(2)
				assert.True(t, ok)

				_, ok = handler.cleanModuleKeys.Load(12)
				assert.True(t, ok)

				_, ok = handler.cleanModuleKeys.Load(10)
				assert.False(t, ok)

				_, ok = handler.cleanModuleKeys.Load(11)
				assert.False(t, ok)

				_, ok = handler.cleanServiceTemplateIds.Load(3)
				assert.True(t, ok)
			},
		},
		{
			name:         "Topo",
			resourceType: CmdbResourceTypeMainlineInstance,
			events: []string{
				`{"bk_cursor":"1","bk_resource":"mainline_instance","bk_event_type":"update","bk_detail":{"bk_obj_id":"set","bk_inst_id":1, "bk_obj_name":"集群", "bk_inst_name":"node1"}}`,
				`{"bk_cursor":"2","bk_resource":"mainline_instance","bk_event_type":"create","bk_detail":{"bk_obj_id":"module","bk_inst_id":2, "bk_obj_name":"模块", "bk_inst_name":"node2"}}`,
				`{"bk_cursor":"3","bk_resource":"mainline_instance","bk_event_type":"delete","bk_detail":{"bk_obj_id":"module","bk_inst_id":3, "bk_obj_name":"集群", "bk_inst_name":"node3"}}`,
			},
			checkFunc: func(t *testing.T, handler *CmdbEventHandler) {
				_, ok := handler.refreshTopoNode.Load("set|1")
				assert.True(t, ok)

				_, ok = handler.refreshTopoNode.Load("module|2")
				assert.True(t, ok)

				_, ok = handler.cleanTopoNode.Load("module|3")
				assert.True(t, ok)
			},
		},
		{
			name:         "Host",
			resourceType: CmdbResourceTypeHost,
			events: []string{
				`{"bk_cursor":"1","bk_resource":"host","bk_event_type":"delete","bk_detail":{"bk_host_id":1, "bk_host_innerip":"127.0.0.1", "bk_cloud_id":0, "bk_agent_id":"xxx1"}}`,
				`{"bk_cursor":"2","bk_resource":"host","bk_event_type":"update","bk_detail":{"bk_host_id":2, "bk_host_innerip":"127.0.0.1", "bk_cloud_id":2, "bk_agent_id":"xxx2"}}`,
				`{"bk_cursor":"2","bk_resource":"host","bk_event_type":"create","bk_detail":{"bk_host_id":3, "bk_host_innerip":"127.0.0.1", "bk_cloud_id":3, "bk_agent_id":""}}`,
			},
			checkFunc: func(t *testing.T, handler *CmdbEventHandler) {
				_, ok := handler.refreshBizHostTopo.Load(1)
				assert.True(t, ok)

				_, ok = handler.refreshBizHostTopo.Load(2)
				assert.True(t, ok)

				_, ok = handler.cleanHostKeys.Load("1")
				assert.True(t, ok)

				_, ok = handler.cleanHostKeys.Load("127.0.0.1|0")
				assert.True(t, ok)

				_, ok = handler.cleanHostKeys.Load("127.0.0.1|2")
				assert.True(t, ok)

				_, ok = handler.cleanAgentIdKeys.Load("xxx1")
				assert.True(t, ok)

				_, ok = handler.cleanAgentIdKeys.Load("xxx2")
				assert.True(t, ok)
			},
		},
		{
			name:         "HostRelation",
			resourceType: CmdbResourceTypeHostRelation,
			events: []string{
				`{"bk_cursor":"1","bk_resource":"host_relation","bk_event_type":"delete","bk_detail":{"bk_host_id":1, "bk_biz_id":1, "bk_module_id":1, "bk_set_id":1}}`,
				`{"bk_cursor":"2","bk_resource":"host_relation","bk_event_type":"update","bk_detail":{"bk_host_id":2, "bk_biz_id":4, "bk_module_id":2, "bk_set_id":2}}`,
				`{"bk_cursor":"3","bk_resource":"host_relation","bk_event_type":"create","bk_detail":{"bk_host_id":3, "bk_biz_id":3, "bk_module_id":3, "bk_set_id":3}}`,
			},
			checkFunc: func(t *testing.T, handler *CmdbEventHandler) {
				_, ok := handler.refreshBizHostTopo.Load(1)
				assert.True(t, ok)

				_, ok = handler.refreshBizHostTopo.Load(2)
				assert.True(t, ok)

				_, ok = handler.refreshBizHostTopo.Load(3)
				assert.True(t, ok)

				_, ok = handler.refreshBizHostTopo.Load(4)
				assert.True(t, ok)

				_, ok = handler.cleanHostKeys.Load("1")
				assert.True(t, ok)

				_, ok = handler.cleanHostKeys.Load("2")
				assert.True(t, ok)

				_, ok = handler.cleanHostKeys.Load("127.0.0.1|0")
				assert.True(t, ok)

				_, ok = handler.cleanHostKeys.Load("127.0.0.1|2")
				assert.True(t, ok)

				_, ok = handler.cleanHostKeys.Load("3")
				assert.False(t, ok)
			},
		},
	}

	redisClient, err := redis.GetClient(rOpts)
	if err != nil {
		t.Fatalf("failed to create redis client: %v", err)
	}
	prefix := t.Name()

	hostKey := fmt.Sprintf("%s.%s", t.Name(), hostCacheKey)
	redisClient.HSet(
		ctx,
		hostKey,
		"1",
		`{"bk_host_id":1,"bk_host_innerip":"127.0.0.1","bk_cloud_id":0,"bk_agent_id":"xxx1", "bk_biz_id":1}`,
		"2",
		`{"bk_host_id":2,"bk_host_innerip":"127.0.0.1","bk_cloud_id":2,"bk_agent_id":"xxx3", "bk_biz_id":2}`,
	)

	// 执行测试用例
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 创建事件处理器
			handler, err := NewCmdbEventHandler(prefix, rOpts, map[string]time.Duration{}, 1)
			if err != nil {
				t.Fatalf("failed to create handler: %v", err)
			}

			// 事件处理
			events := make([]cmdb.ResourceWatchEvent, len(tc.events))
			for i, event := range tc.events {
				err := json.Unmarshal([]byte(event), &events[i])
				if err != nil {
					t.Fatalf("failed to unmarshal event: %v", err)
				}
			}
			err = handler.preprocessEvents(ctx, tc.resourceType, events)
			if err != nil {
				t.Fatalf("failed to preprocess events: %v", err)
			}

			// 验证处理结果
			tc.checkFunc(t, handler)
		})
	}
}

func TestRefreshByEvents(t *testing.T) {
	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	ctx := context.Background()

	handler, err := NewCmdbEventHandler(t.Name(), rOpts, map[string]time.Duration{}, 1)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	t.Run("Biz", func(t *testing.T) {
		refreshAllCount := 0
		patchRefreshAll := gomonkey.ApplyFunc(RefreshAll, func(ctx context.Context, cacheManager Manager, concurrentLimit int) error {
			refreshAllCount++
			return nil
		})
		defer patchRefreshAll.Reset()

		handler.refreshBiz = true

		err := handler.refreshByEvents(ctx)
		if err != nil {
			t.Fatalf("failed to refresh by events: %v", err)
		}

		assert.Equal(t, refreshAllCount, 1)
	})

	t.Run("Host", func(t *testing.T) {
		handler.refreshBizHostTopo.Store(1, struct{}{})
		handler.cleanHostKeys.Store("1", struct{}{})
		handler.cleanAgentIdKeys.Store("xxx1", struct{}{})
	})
}
