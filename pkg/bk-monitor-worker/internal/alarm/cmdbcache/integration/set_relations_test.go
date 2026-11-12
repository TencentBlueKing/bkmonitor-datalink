// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/relation"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
)

// 独立测试包使用真实 Redis，验证缓存读取、业务分组和指标构建的完整路径。
func newSetManager(t *testing.T) *cmdbcache.SetCacheManager {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	command := exec.Command("redis-server", "--bind", "127.0.0.1", "--port", port,
		"--save", "", "--appendonly", "no", "--dir", t.TempDir())
	require.NoError(t, command.Start())
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	probe := goRedis.NewClient(&goRedis.Options{Addr: addr})
	t.Cleanup(func() { require.NoError(t, probe.Close()) })
	require.Eventually(t, func() bool {
		return probe.Ping(context.Background()).Err() == nil
	}, 5*time.Second, 10*time.Millisecond)
	manager, err := cmdbcache.NewSetCacheManager(tenant.DefaultTenantId, t.Name(),
		&redis.Options{Mode: "standalone", Addrs: []string{addr}}, 1)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, manager.RedisClient.Close()) })
	return manager
}

func TestBusinessSetRelationsFromCache(t *testing.T) {
	manager := newSetManager(t)
	builder := relation.GetRelationMetricsBuilder()
	t.Cleanup(builder.ClearAllMetrics)
	ctx := context.Background()
	key := manager.GetCacheKey("cmdb.set")
	for _, test := range []struct {
		name     string
		sets     []map[string]any
		expected []string
	}{
		{name: "没有集群时不生成指标"},
		{
			name: "没有主机的集群也生成两种业务关系",
			sets: []map[string]any{{"bk_biz_id": 2, "bk_set_id": 42}},
			expected: []string{
				`biz_with_set_relation{bk_biz_id="2",bk_set_id="42"} 1`,
				`business_with_set_relation{bk_biz_id="2",bk_set_id="42"} 1`,
			},
		},
		{
			name: "重复刷新和缓存记录不产生重复指标",
			sets: []map[string]any{{"bk_biz_id": 2, "bk_set_id": 42}, {"bk_biz_id": 2, "bk_set_id": 42}},
			expected: []string{
				`biz_with_set_relation{bk_biz_id="2",bk_set_id="42"} 1`,
				`business_with_set_relation{bk_biz_id="2",bk_set_id="42"} 1`,
			},
		},
		{
			name: "不同业务隔离",
			sets: []map[string]any{{"bk_biz_id": 2, "bk_set_id": 42}, {"bk_biz_id": 3, "bk_set_id": 42}},
			expected: []string{
				`biz_with_set_relation{bk_biz_id="2",bk_set_id="42"} 1`,
				`business_with_set_relation{bk_biz_id="2",bk_set_id="42"} 1`,
				`biz_with_set_relation{bk_biz_id="3",bk_set_id="42"} 1`,
				`business_with_set_relation{bk_biz_id="3",bk_set_id="42"} 1`,
			},
		},
		{
			name: "保留集群扩展字段",
			sets: []map[string]any{{"bk_biz_id": 2, "bk_set_id": 42, "bk_set_name": "example", "version_meta": `{"set":{"version":"v1"}}`}},
			expected: []string{
				`biz_with_set_relation{bk_biz_id="2",bk_set_id="42"} 1`,
				`business_with_set_relation{bk_biz_id="2",bk_set_id="42"} 1`,
				`set_info_relation{bk_biz_id="2",bk_set_id="42",bk_set_name="example",version="v1"} 1`,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder.ClearAllMetrics()
			require.NoError(t, manager.RedisClient.Del(ctx, key).Err())
			for i, item := range test.sets {
				data, err := json.Marshal(item)
				require.NoError(t, err)
				require.NoError(t, manager.RedisClient.HSet(ctx, key, fmt.Sprint(i), data).Err())
			}
			for i := 0; i < 2; i++ {
				require.NoError(t, manager.BuildRelationMetrics(ctx))
				require.ElementsMatch(t, test.expected, strings.FieldsFunc(builder.String(), func(r rune) bool { return r == '\n' }))
			}
			for _, item := range test.sets {
				builder.ClearResourceWithID(item["bk_biz_id"].(int), relation.Set, fmt.Sprint(item["bk_set_id"]))
			}
			require.Empty(t, builder.String(), "删除集群后两种业务关系都应消失")
		})
	}
}

func TestSetRelationIdentities(t *testing.T) {
	manager := newSetManager(t)
	for _, test := range []struct {
		name string
		item map[string]any
		want int
	}{
		{name: "完整数字主键", item: map[string]any{"bk_biz_id": float64(2), "bk_set_id": float64(42)}, want: 2},
		{name: "完整字符串主键", item: map[string]any{"bk_biz_id": "2", "bk_set_id": "42"}, want: 2},
		{name: "缺少业务主键", item: map[string]any{"bk_set_id": 42}},
		{name: "业务主键为零", item: map[string]any{"bk_biz_id": 0, "bk_set_id": 42}},
		{name: "业务主键为负数", item: map[string]any{"bk_biz_id": -1, "bk_set_id": 42}},
		{name: "缺少集群主键", item: map[string]any{"bk_biz_id": 2}},
		{name: "集群主键为零", item: map[string]any{"bk_biz_id": 2, "bk_set_id": 0}},
		{name: "集群主键为负数", item: map[string]any{"bk_biz_id": 2, "bk_set_id": -1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			infos := manager.SetToRelationInfos([]map[string]any{test.item})
			if test.want > 0 {
				require.Len(t, infos, 1)
				require.Len(t, infos[0].Links, test.want)
			}
			for _, info := range infos {
				require.Len(t, info.Links, test.want)
				for _, link := range info.Links {
					require.Len(t, link, 1)
					require.Equal(t, "2", link[0].Label[relation.BizID])
				}
			}
		})
	}
}

func TestExistingHostLinks(t *testing.T) {
	builder := relation.GetRelationMetricsBuilder()
	t.Cleanup(builder.ClearAllMetrics)
	for _, test := range []struct {
		name string
		link relation.Link
		want []string
	}{
		{
			name: "主机经模块集群到业务的原路径",
			link: relation.Link{
				{ID: "10", Resource: relation.Module, Label: map[string]string{relation.ModuleID: "10"}},
				{ID: "42", Resource: relation.Set, Label: map[string]string{relation.SetID: "42"}},
				{ID: "2", Resource: relation.Biz, Label: map[string]string{relation.BizID: "2"}},
			},
			want: []string{
				`host_with_module_relation{bk_biz_id="2",bk_host_id="1",bk_module_id="10"} 1`,
				`module_with_set_relation{bk_biz_id="2",bk_module_id="10",bk_set_id="42"} 1`,
			},
		},
		{name: "只含业务节点时不增加主机到业务的边", link: relation.Link{{ID: "2", Resource: relation.Biz}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder.ClearAllMetrics()
			require.NoError(t, builder.BuildInfosCache(context.Background(), 2, relation.Host, []*relation.Info{{
				ID: "1", Resource: relation.Host, Label: map[string]string{relation.HostID: "1"}, Links: []relation.Link{test.link},
			}}))
			require.ElementsMatch(t, test.want, strings.FieldsFunc(builder.String(), func(r rune) bool { return r == '\n' }))
		})
	}
}
