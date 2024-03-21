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
	"encoding/json"
	"fmt"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
)

var DemoHosts = []*AlarmHostInfo{
	{
		BkBizId:       2,
		BkHostId:      1,
		BkHostInnerip: "127.0.0.1",
		BkCloudId:     0,
		BkAgentId:     "12345678901234567890123456789012",
		BkSetIds:      []int{2, 3},
		BkModuleIds:   []int{3, 6},
		TopoLinks: [][]string{
			{"module|3", "set|2", "biz|2"},
			{"module|6", "set|3", "test|2", "biz|2"},
		},
	},
	{
		BkBizId:       2,
		BkHostId:      2,
		BkHostInnerip: "127.0.0.2",
		BkCloudId:     0,
		BkAgentId:     "",
		BkSetIds:      []int{2},
		BkModuleIds:   []int{4},
		TopoLinks: [][]string{
			{"module|4", "set|2", "biz|2"},
		},
	},
	{
		BkBizId:       2,
		BkHostId:      3,
		BkHostInnerip: "127.0.0.3",
		BkCloudId:     0,
		BkAgentId:     "12345678901234567890123456789014",
		BkSetIds:      []int{3},
		BkModuleIds:   []int{6},
		TopoLinks: [][]string{
			{"module|6", "set|3", "test|2", "biz|2"},
		},
	},
}

var DemoTopoTree = &cmdb.SearchBizInstTopoData{
	BkObjId:    "biz",
	BkObjName:  "业务",
	BkInstId:   2,
	BkInstName: "蓝鲸",
	Child: []cmdb.SearchBizInstTopoData{
		{
			BkObjId:    "set",
			BkObjName:  "集群",
			BkInstId:   2,
			BkInstName: "空闲机池",
			Child: []cmdb.SearchBizInstTopoData{
				{
					BkObjId:    "module",
					BkObjName:  "模块",
					BkInstId:   3,
					BkInstName: "空闲机",
				},
				{
					BkObjId:    "module",
					BkObjName:  "模块",
					BkInstId:   4,
					BkInstName: "故障机",
				},
				{
					BkObjId:    "module",
					BkObjName:  "模块",
					BkInstId:   5,
					BkInstName: "待回收",
				},
			},
		},
		{
			BkObjId:    "test",
			BkObjName:  "测试",
			BkInstId:   2,
			BkInstName: "测试节点",
			Child: []cmdb.SearchBizInstTopoData{
				{
					BkObjId:    "set",
					BkObjName:  "集群",
					BkInstId:   3,
					BkInstName: "测试集群",
					Child: []cmdb.SearchBizInstTopoData{
						{
							BkObjId:    "module",
							BkObjName:  "模块",
							BkInstId:   6,
							BkInstName: "测试模块",
						},
					},
				},
			},
		},
	},
}

func TestHostAndTopoCacheManager(t *testing.T) {
	// mock 主机和拓扑查询
	patches := gomonkey.ApplyFunc(getHostAndTopoByBiz, func(ctx context.Context, bizId int) ([]*AlarmHostInfo, *cmdb.SearchBizInstTopoData, error) {
		return DemoHosts, DemoTopoTree, nil
	})
	defer patches.Reset()

	rOpts := &redis.RedisOptions{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	client, _ := redis.GetRedisClient(rOpts)
	ctx := context.Background()

	t.Run("RefreshAndEventHandler", func(t *testing.T) {
		cacheManager, err := NewHostAndTopoCacheManager(t.Name(), rOpts)
		if err != nil {
			t.Error(err)
			return
		}

		// 刷新业务缓存
		err = cacheManager.RefreshByBiz(ctx, 2)
		if err != nil {
			t.Error(err)
			return
		}

		// 判断是否存在所有的缓存键
		expectedHostKeys := make([]string, 0, len(DemoHosts))
		expectedHostIds := make([]string, 0, len(DemoHosts))
		expectedAgentIds := make([]string, 0, len(DemoHosts))
		expectedHostIpKeys := make([]string, 0, len(DemoHosts))
		for _, host := range DemoHosts {
			if host.BkHostInnerip != "" {
				expectedHostIpKeys = append(expectedHostIpKeys, host.BkHostInnerip)
				expectedHostKeys = append(expectedHostKeys, fmt.Sprintf("%s|%d", host.BkHostInnerip, host.BkCloudId))
			}
			if host.BkAgentId != "" {
				expectedAgentIds = append(expectedAgentIds, host.BkAgentId)
			}
			expectedHostIds = append(expectedHostIds, fmt.Sprintf("%d", host.BkHostId))
		}

		assert.EqualValues(t, expectedHostKeys, client.HKeys(ctx, cacheManager.GetCacheKey(hostCacheKey)).Val())
		assert.EqualValues(t, expectedHostIds, client.HKeys(ctx, cacheManager.GetCacheKey(hostIDCacheKey)).Val())
		assert.EqualValues(t, expectedAgentIds, client.HKeys(ctx, cacheManager.GetCacheKey(hostAgentIDCacheKey)).Val())

		assert.EqualValues(t, 8, int(client.HLen(ctx, cacheManager.GetCacheKey(topoCacheKey)).Val()))

		// 刷新全局缓存
		err = cacheManager.RefreshGlobal(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		assert.EqualValues(t, expectedHostIpKeys, client.HKeys(ctx, cacheManager.GetCacheKey(hostIPCacheKey)).Val())

		// 生成变更事件数据
		allResult := client.HGetAll(ctx, cacheManager.GetCacheKey(hostCacheKey))
		if allResult.Err() != nil {
			t.Error(allResult.Err())
			return
		}
		events := make([]map[string]interface{}, 0, len(allResult.Val()))
		for _, v := range allResult.Val() {
			var host *AlarmHostInfo
			err := json.Unmarshal([]byte(v), &host)
			if err != nil {
				t.Error(err)
				return
			}
			events = append(events, map[string]interface{}{
				"bk_host_id":      host.BkHostId,
				"bk_host_innerip": host.BkHostInnerip,
				"bk_cloud_id":     host.BkCloudId,
				"bk_agent_id":     host.BkAgentId,
			})
		}

		// 基于事件更新缓存
		err = cacheManager.UpdateByEvents(ctx, "host", events)
		if err != nil {
			t.Error(err)
			return
		}

		// 基于事件清理缓存
		err = cacheManager.CleanByEvents(ctx, "host", events)
		if err != nil {
			t.Error(err)
			return
		}

		topoEvent := map[string]interface{}{
			"bk_obj_id":    "module",
			"bk_inst_id":   6,
			"bk_inst_name": "测试模块",
			"bk_obj_name":  "模块",
		}

		err = cacheManager.CleanByEvents(ctx, "mainline_instance", []map[string]interface{}{topoEvent})
		if err != nil {
			t.Error(err)
			return
		}

		assert.False(t, client.HExists(ctx, cacheManager.GetCacheKey(topoCacheKey), "module|6").Val())

		err = cacheManager.UpdateByEvents(ctx, "mainline_instance", []map[string]interface{}{topoEvent})
		if err != nil {
			t.Error(err)
			return
		}

		assert.True(t, client.HExists(ctx, cacheManager.GetCacheKey(topoCacheKey), "module|6").Val())

		// 判断清理后是否为空
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostIDCacheKey)).Val())
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostAgentIDCacheKey)).Val())
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostCacheKey)).Val())
	})

	t.Run("Clean", func(t *testing.T) {
		cacheManager, err := NewHostAndTopoCacheManager(t.Name(), rOpts)
		if err != nil {
			t.Error(err)
			return
		}

		// 刷新业务缓存
		err = cacheManager.RefreshByBiz(ctx, 2)
		if err != nil {
			t.Error(err)
			return
		}

		// 刷新业务缓存
		err = cacheManager.RefreshGlobal(ctx)
		if err != nil {
			t.Error(err)
			return
		}

		// 判断是否存在所有的缓存键
		assert.NotEmpty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostIDCacheKey)).Val())
		assert.NotEmpty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostAgentIDCacheKey)).Val())
		assert.NotEmpty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostCacheKey)).Val())
		assert.NotEmpty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostIPCacheKey)).Val())
		assert.NotEmpty(t, client.HKeys(ctx, cacheManager.GetCacheKey(topoCacheKey)).Val())

		// 清理缓存
		cacheManager.initUpdatedFieldSet(hostIDCacheKey, hostAgentIDCacheKey, hostCacheKey, hostIPCacheKey, topoCacheKey)
		err = cacheManager.CleanGlobal(ctx)
		if err != nil {
			t.Error(err)
			return
		}

		// 判断清理后是否为空
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostIDCacheKey)).Val())
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostAgentIDCacheKey)).Val())
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostCacheKey)).Val())
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostIPCacheKey)).Val())
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(topoCacheKey)).Val())
	})
}
