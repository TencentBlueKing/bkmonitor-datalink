// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// CMDB 关系任务固定读 Redis。Redis 里没有任何定义时，relation_config 仍应按内置关系生成指标；
// 期望的指标名与主键来自内置定义 host_with_system（host: bk_host_id，system: bk_target_ip）。
func TestRelationConfigUsesBuiltinSchemaWhenRedisIsEmpty(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	provider, err := relation.NewRedisProvider(ctx, client, relation.WithReconcileInterval(0))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Close()) })

	var resources map[string]*ResourceInfo
	require.NoError(t, json.Unmarshal([]byte(`{
		"host": {"name": "host", "data": {"3001": {
			"id": "3001", "resource": "host",
			"label": {"bk_host_id": "3001"},
			"expands": {"host": {"bk_host_id": "3001"}},
			"relation_config": {"system": {"bk_target_ip": "192.0.2.10"}}
		}}}
	}`), &resources))
	builder := newRelationMetricsBuilder()
	builder.WithSchemaProvider(provider)
	builder.resources[2] = resources

	var found []map[string]string
	for _, metric := range builder.getCMDBMetrics(2) {
		if metric.Name != "host_with_system_relation" {
			continue
		}
		labels := map[string]string{}
		for _, label := range metric.Labels {
			labels[label.Name] = label.Value
		}
		found = append(found, labels)
	}
	require.Equal(t, []map[string]string{{
		"bk_biz_id": "2", "bk_host_id": "3001", "bk_target_ip": "192.0.2.10",
	}}, found)
}
