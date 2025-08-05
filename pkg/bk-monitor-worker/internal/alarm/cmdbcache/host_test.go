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

package cmdbcache

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/relation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
)

var DemoHosts = []*AlarmHostInfo{

	{
		BkBizId:       2,
		BkHostId:      1,
		BkHostName:    "name-1",
		BkHostInnerip: "127.0.0.1",
		BkCloudId:     0,
		BkAgentId:     "12345678901234567890123456789012",
		BkSetIds:      []int{2, 3},
		BkModuleIds:   []int{3, 6},
		Expands: map[string]map[string]any{
			"set": {
				"version":           "tlinux_update_20250729_134916_ver92184",
				"env_type":          "prod",
				"service_type":      "",
				"service_version":   "",
				"env_name":          "LIVE",
				"finish_time":       "2025-07-30 09:32:33",
				"finish_time_stamp": 1753839153,
			},
			"host": {
				"version":           "tlinux_update_20250729_134916_ver92184",
				"env_type":          "prod",
				"service_type":      "",
				"service_version":   "",
				"env_name":          "LIVE",
				"finish_time":       "2025-07-30 09:32:33",
				"finish_time_stamp": 1753839153,
			},
		},
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
		BkHostName:    "name-2",
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
		BkHostName:    "name-3",
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
	{
		BkBizId:       2,
		BkHostId:      9,
		BkHostName:    "name-9",
		BkHostInnerip: "127.0.1.1,127.0.2.1",
		BkCloudId:     0,
		BkAgentId:     "12345678901234567890123456789012",
		BkSetIds:      []int{2, 3},
		BkModuleIds:   []int{3, 6},
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
	patches := gomonkey.ApplyFunc(getHostAndTopoByBiz, func(ctx context.Context, bkTenantId string, bizId int) ([]*AlarmHostInfo, *cmdb.SearchBizInstTopoData, error) {
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
		cacheManager, err := NewHostAndTopoCacheManager(tenant.DefaultTenantId, t.Name(), rOpts, 1)
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

		// 判断关联指标
		metrics := strings.Split(relation.GetRelationMetricsBuilder().String(), "\n")
		sort.Strings(metrics)
		var metricsActual strings.Builder
		for _, m := range metrics {
			if m != "" {
				metricsActual.WriteString(m + "\n")
			}
		}

		assert.Equal(t, `host_info_relation{bk_biz_id="2",env_name="LIVE",env_type="prod",host_id="1",version="tlinux_update_20250729_134916_ver92184"} 1
host_with_system_relation{bk_biz_id="2",bk_cloud_id="0",bk_target_ip="127.0.0.1",host_id="1"} 1
host_with_system_relation{bk_biz_id="2",bk_cloud_id="0",bk_target_ip="127.0.0.2",host_id="2"} 1
host_with_system_relation{bk_biz_id="2",bk_cloud_id="0",bk_target_ip="127.0.0.3",host_id="3"} 1
`, metricsActual.String())

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
				"bk_host_id":      float64(host.BkHostId),
				"bk_host_innerip": host.BkHostInnerip,
				"bk_cloud_id":     float64(host.BkCloudId),
				// 测试agent_id变化后是否会被删除
				"bk_agent_id": fmt.Sprintf("%s-change", host.BkAgentId),
			})
		}

		fmt.Printf(client.HGet(ctx, cacheManager.GetCacheKey(hostCacheKey), "1").Val())

		// 基于事件更新缓存
		err = cacheManager.UpdateByEvents(ctx, "host", events)
		if err != nil {
			t.Error(err)
			return
		}

		// 判断agent_id是否被删除
		for _, event := range events {
			agentID := event["bk_agent_id"].(string)
			oldAgentID := agentID[:len(agentID)-7]
			host := client.HGet(ctx, cacheManager.GetCacheKey(hostCacheKey), strconv.Itoa(int(event["bk_host_id"].(float64)))).Val()
			assert.NotEmpty(t, host)
			assert.False(t, client.HExists(ctx, cacheManager.GetCacheKey(hostAgentIDCacheKey), oldAgentID).Val())
		}

		// 基于事件清理缓存
		err = cacheManager.CleanByEvents(ctx, "host", events)
		if err != nil {
			t.Error(err)
			return
		}

		topoEvent := map[string]interface{}{
			"bk_obj_id":    "module",
			"bk_inst_id":   float64(6),
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
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostAgentIDCacheKey)).Val())
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostCacheKey)).Val())
	})

	t.Run("Clean", func(t *testing.T) {
		cacheManager, err := NewHostAndTopoCacheManager(tenant.DefaultTenantId, t.Name(), rOpts, 1)
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
		assert.NotEmpty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostIPCacheKey)).Val())
		assert.NotEmpty(t, client.HKeys(ctx, cacheManager.GetCacheKey(topoCacheKey)).Val())

		// 清理缓存
		cacheManager.initUpdatedFieldSet(hostAgentIDCacheKey, hostCacheKey, hostIPCacheKey, topoCacheKey)
		err = cacheManager.CleanGlobal(ctx)
		if err != nil {
			t.Error(err)
			return
		}

		// 判断清理后是否为空
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostAgentIDCacheKey)).Val())
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostCacheKey)).Val())
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(hostIPCacheKey)).Val())
		assert.Empty(t, client.HKeys(ctx, cacheManager.GetCacheKey(topoCacheKey)).Val())
	})
}

func TestHostToRelationInfos(t *testing.T) {
	// mock 主机和拓扑查询
	patches := gomonkey.ApplyFunc(getHostAndTopoByBiz, func(ctx context.Context, bkTenantId string, bizId int) ([]*AlarmHostInfo, *cmdb.SearchBizInstTopoData, error) {
		return DemoHosts, DemoTopoTree, nil
	})
	defer patches.Reset()

	rOpts := &redis.Options{
		Mode:  "standalone",
		Addrs: []string{testRedisAddr},
	}

	cacheManager, err := NewHostAndTopoCacheManager(tenant.DefaultTenantId, t.Name(), rOpts, 1)
	if err != nil {
		t.Error(err)
		return
	}

	resourceInfo := cacheManager.HostToRelationInfos(DemoHosts)

	ris, err := json.Marshal(resourceInfo)
	assert.Nil(t, err)
	assert.Equal(t, `[{"id":"127.0.0.1|0","resource":"system","label":{"bk_cloud_id":"0","bk_target_ip":"127.0.0.1"},"links":[[{"id":"1","resource":"host","label":{"bk_host_id":"1"}}]]},{"id":"1","resource":"host","label":{"bk_host_id":"1"},"expands":{"host":{"bk_host_name":"name-1","env_name":"LIVE","env_type":"prod","version":"tlinux_update_20250729_134916_ver92184"},"set":{"env_name":"LIVE","env_type":"prod","version":"tlinux_update_20250729_134916_ver92184"}},"links":[[{"id":"3","resource":"module","label":{"bk_module_id":"3"}},{"id":"2","resource":"set","label":{"bk_set_id":"2"}},{"id":"2","resource":"biz","label":{"bk_biz_id":"2"}}],[{"id":"6","resource":"module","label":{"bk_module_id":"6"}},{"id":"3","resource":"set","label":{"bk_set_id":"3"}},{"id":"2","resource":"test","label":{"bk_test_id":"2"}},{"id":"2","resource":"biz","label":{"bk_biz_id":"2"}}]]},{"id":"127.0.0.2|0","resource":"system","label":{"bk_cloud_id":"0","bk_target_ip":"127.0.0.2"},"links":[[{"id":"2","resource":"host","label":{"bk_host_id":"2"}}]]},{"id":"2","resource":"host","label":{"bk_host_id":"2"},"links":[[{"id":"4","resource":"module","label":{"bk_module_id":"4"}},{"id":"2","resource":"set","label":{"bk_set_id":"2"}},{"id":"2","resource":"biz","label":{"bk_biz_id":"2"}}]]},{"id":"127.0.0.3|0","resource":"system","label":{"bk_cloud_id":"0","bk_target_ip":"127.0.0.3"},"links":[[{"id":"3","resource":"host","label":{"bk_host_id":"3"}}]]},{"id":"3","resource":"host","label":{"bk_host_id":"3"},"links":[[{"id":"6","resource":"module","label":{"bk_module_id":"6"}},{"id":"3","resource":"set","label":{"bk_set_id":"3"}},{"id":"2","resource":"test","label":{"bk_test_id":"2"}},{"id":"2","resource":"biz","label":{"bk_biz_id":"2"}}]]},{"id":"9","resource":"host","label":{"bk_host_id":"9"}}]`, string(ris))
}
