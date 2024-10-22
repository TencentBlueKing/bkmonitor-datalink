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
	"fmt"
	"sort"
	"strconv"
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
		TopoLinks: map[string][]map[string]interface{}{
			"module|3": {
				{"bk_inst_id": 3, "bk_inst_name": "空闲机", "bk_obj_id": "module", "bk_obj_name": "模块"},
				{"bk_inst_id": 2, "bk_inst_name": "空闲机池", "bk_obj_id": "set", "bk_obj_name": "集群"},
				{"bk_inst_id": 2, "bk_inst_name": "蓝鲸", "bk_obj_id": "biz", "bk_obj_name": "业务"},
			},
			"module|6": {
				{"bk_inst_id": 6, "bk_inst_name": "测试模块", "bk_obj_id": "module", "bk_obj_name": "模块"},
				{"bk_inst_id": 3, "bk_inst_name": "测试集群", "bk_obj_id": "set", "bk_obj_name": "集群"},
				{"bk_inst_id": 2, "bk_inst_name": "测试节点", "bk_obj_id": "test", "bk_obj_name": "测试"},
				{"bk_inst_id": 2, "bk_inst_name": "蓝鲸", "bk_obj_id": "biz", "bk_obj_name": "业务"},
			},
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
		TopoLinks: map[string][]map[string]interface{}{
			"module|4": {
				{"bk_inst_id": 4, "bk_inst_name": "故障机", "bk_obj_id": "module", "bk_obj_name": "模块"},
				{"bk_inst_id": 2, "bk_inst_name": "空闲机池", "bk_obj_id": "set", "bk_obj_name": "集群"},
				{"bk_inst_id": 2, "bk_inst_name": "蓝鲸", "bk_obj_id": "biz", "bk_obj_name": "业务"},
			},
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
		TopoLinks: map[string][]map[string]interface{}{
			"module|6": {
				{"bk_inst_id": 6, "bk_inst_name": "测试模块", "bk_obj_id": "module", "bk_obj_name": "模块"},
				{"bk_inst_id": 3, "bk_inst_name": "测试集群", "bk_obj_id": "set", "bk_obj_name": "集群"},
				{"bk_inst_id": 2, "bk_inst_name": "测试节点", "bk_obj_id": "test", "bk_obj_name": "测试"},
				{"bk_inst_id": 2, "bk_inst_name": "蓝鲸", "bk_obj_id": "biz", "bk_obj_name": "业务"},
			},
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

	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	client, _ := redis.GetClient(rOpts)
	ctx := context.Background()

	t.Run("RefreshAndEventHandler", func(t *testing.T) {
		cacheManager, err := NewHostAndTopoCacheManager(t.Name(), rOpts, 1)
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
		expectedAgentIds := make([]string, 0, len(DemoHosts))
		expectedHostIpKeys := make([]string, 0, len(DemoHosts))

		for _, host := range DemoHosts {
			if host.BkHostInnerip != "" {
				expectedHostIpKeys = append(expectedHostIpKeys, host.BkHostInnerip)
				expectedHostKeys = append(expectedHostKeys, fmt.Sprintf("%s|%d", host.BkHostInnerip, host.BkCloudId))
				expectedHostKeys = append(expectedHostKeys, strconv.Itoa(host.BkHostId))
			}
			if host.BkAgentId != "" {
				expectedAgentIds = append(expectedAgentIds, host.BkAgentId)
			}
		}

		sort.Strings(expectedHostKeys)
		actualHostKeys := client.HKeys(ctx, cacheManager.GetCacheKey(hostCacheKey)).Val()
		sort.Strings(actualHostKeys)

		assert.EqualValues(t, expectedHostKeys, actualHostKeys)
		assert.EqualValues(t, expectedAgentIds, client.HKeys(ctx, cacheManager.GetCacheKey(hostAgentIDCacheKey)).Val())
		assert.EqualValues(t, 8, int(client.HLen(ctx, cacheManager.GetCacheKey(topoCacheKey)).Val()))

		// 刷新全局缓存
		err = cacheManager.RefreshGlobal(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		assert.EqualValues(t, expectedHostIpKeys, client.HKeys(ctx, cacheManager.GetCacheKey(hostIpCacheKey)).Val())
	})

	t.Run("Clean", func(t *testing.T) {
		cacheManager, err := NewHostAndTopoCacheManager(t.Name(), rOpts, 1)
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
		assert.NotEmpty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostAgentIDCacheKey)).Val())
		assert.NotEmpty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostCacheKey)).Val())
		assert.NotEmpty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostIpCacheKey)).Val())
		assert.NotEmpty(t, client.HKeys(ctx, cacheManager.GetCacheKey(topoCacheKey)).Val())

		// 清理缓存
		cacheManager.initUpdatedFieldSet(hostAgentIDCacheKey, hostCacheKey, hostIpCacheKey, topoCacheKey)
		err = cacheManager.CleanGlobal(ctx)
		if err != nil {
			t.Error(err)
			return
		}

		// 判断清理后是否为空
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostAgentIDCacheKey)).Val())
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostCacheKey)).Val())
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostIpCacheKey)).Val())
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(topoCacheKey)).Val())
	})
}
