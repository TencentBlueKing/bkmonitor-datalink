// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datasources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	redis "github.com/redis/go-redis/v9"
)

const (
	maxDynamicGroupValueBytes = 64 << 10
	maxDynamicGroupIDs        = 1024
)

// DynamicGroupCache 限定动态分组投影只执行定向 Redis hash 读取。
type DynamicGroupCache interface {
	HStrLen(context.Context, string, string) *redis.IntCmd
	HGet(context.Context, string, string) *redis.StringCmd
}

// DynamicGroupTenantCache 将无租户后缀的 Kingeye keyspace 绑定到唯一租户。
// 每个租户必须配置独立的 Redis 逻辑数据库或 key 前缀，不能共享同一 keyspace。
type DynamicGroupTenantCache struct {
	Client    DynamicGroupCache
	KeyPrefix string
}

// DynamicGroupClient 读取 Kingeye 已物化的实例与分组关系。
type DynamicGroupClient struct {
	tenants map[string]DynamicGroupTenantCache
}

// NewDynamicGroupClient 创建仅接受显式租户的只读 Reader。
func NewDynamicGroupClient(tenants map[string]DynamicGroupTenantCache) (*DynamicGroupClient, error) {
	if len(tenants) == 0 {
		return nil, fmt.Errorf("dynamic group reader requires tenant caches")
	}
	copyOfTenants := make(map[string]DynamicGroupTenantCache, len(tenants))
	for tenant, cache := range tenants {
		if tenant == "" || cache.Client == nil || cache.KeyPrefix == "" {
			return nil, fmt.Errorf("dynamic group reader requires tenant, client, and key prefix")
		}
		copyOfTenants[tenant] = cache
	}
	return &DynamicGroupClient{tenants: copyOfTenants}, nil
}

// GetDynamicGroupIDs 按模型和 canonical 实例 ID 读取 Redis hash field。
// 未命中返回空列表；连接、格式和容量错误由调用方降级为资源部分成功。
func (c *DynamicGroupClient) GetDynamicGroupIDs(ctx context.Context, tenantID, modelCode, instanceID string) ([]string, error) {
	if ctx == nil {
		return nil, fmt.Errorf("read dynamic group: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil || tenantID == "" || modelCode == "" || instanceID == "" || len(modelCode) > 256 || len(instanceID) > 1024 {
		return nil, fmt.Errorf("read dynamic group: invalid identity")
	}
	cache, ok := c.tenants[tenantID]
	if !ok {
		return nil, fmt.Errorf("read dynamic group: tenant cache is unavailable")
	}
	key := cache.KeyPrefix + "dynamic_inst_group:" + modelCode
	length, err := cache.Client.HStrLen(ctx, key, instanceID).Result()
	if err != nil {
		return nil, fmt.Errorf("read dynamic group length: %w", err)
	}
	if length == 0 {
		return []string{}, nil
	}
	if length > maxDynamicGroupValueBytes {
		return nil, fmt.Errorf("read dynamic group: value exceeds size limit")
	}
	raw, err := cache.Client.HGet(ctx, key, instanceID).Result()
	if errors.Is(err, redis.Nil) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read dynamic group value: %w", err)
	}
	if len(raw) > maxDynamicGroupValueBytes {
		return nil, fmt.Errorf("read dynamic group: value exceeds size limit")
	}
	var value struct {
		GroupIDs []int64 `json:"group_ids"`
	}
	if err := json.Unmarshal([]byte(raw), &value); err != nil || value.GroupIDs == nil {
		return nil, fmt.Errorf("read dynamic group: invalid group_ids")
	}
	if len(value.GroupIDs) > maxDynamicGroupIDs {
		return nil, fmt.Errorf("read dynamic group: too many group_ids")
	}
	ids := make([]int64, 0, len(value.GroupIDs))
	for _, id := range value.GroupIDs {
		if id <= 0 {
			return nil, fmt.Errorf("read dynamic group: invalid group ID")
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if len(result) == 0 || result[len(result)-1] != strconv.FormatInt(id, 10) {
			result = append(result, strconv.FormatInt(id, 10))
		}
	}
	return result, nil
}
