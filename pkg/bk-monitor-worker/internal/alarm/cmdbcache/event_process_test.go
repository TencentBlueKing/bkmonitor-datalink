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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
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
	assert.EqualValues(t, handler.getFullRefreshInterval("module"), 600*time.Second)

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

	// 验证刷新时间间隔
	assert.False(t, handler.ifRunRefreshAll(ctx, cacheType, now.Unix()))
	assert.False(t, handler.ifRunRefreshAll(ctx, cacheType, now.Add(time.Second).Unix()))
	assert.True(t, handler.ifRunRefreshAll(ctx, cacheType, now.Add(2*time.Second).Unix()))
}
