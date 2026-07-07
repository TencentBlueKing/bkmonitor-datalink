// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	uqredis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// BindingInfo 是 SurrealDBBinding 路由里 unify-query 查询需要的字段，
// 对应 bkbase 回填的 metadata.annotations.{database, namespace} + storage name。
type BindingInfo struct {
	Name        string // binding metadata.name
	BkBizID     string // binding metadata.labels.bk_biz_id
	Database    string // binding metadata.annotations.database，作为 result_table_id
	Namespace   string // binding metadata.annotations.namespace，如 "mapleleaf_39"
	ClusterName string // binding spec.storage.name，即 SurrealDB 集群名
	Phase       string // binding status.phase
}

// BindingLookupError 表示 binding 查找失败的语义化错误。
type BindingLookupError struct {
	SpaceUID string
	Reason   string
}

func (e *BindingLookupError) Error() string {
	return fmt.Sprintf("binding lookup failed for space=%s: %s", e.SpaceUID, e.Reason)
}

// cache 条目
type bindingCacheEntry struct {
	info   *BindingInfo
	expiry time.Time
}

type bindingRedisLookup func(ctx context.Context, key, field string) (string, error)

type bindingRouteDetail struct {
	Name        string `json:"name"`
	BkBizID     string `json:"bk_biz_id"`
	Database    string `json:"database"`
	Namespace   string `json:"namespace"`
	ClusterName string `json:"cluster_name"`
	Phase       string `json:"phase"`
}

// BindingResolver 解析 spaceUID → BindingInfo，带 TTL 缓存。
type BindingResolver struct {
	redisLookup bindingRedisLookup

	cacheMu sync.RWMutex
	cache   map[string]*bindingCacheEntry // key = bk_biz_id
}

var (
	defaultBindingResolver     *BindingResolver
	defaultBindingResolverOnce sync.Once
)

// GetBindingResolver 返回全局单例。
func GetBindingResolver() *BindingResolver {
	defaultBindingResolverOnce.Do(func() {
		defaultBindingResolver = &BindingResolver{
			redisLookup: defaultBindingRedisLookup,
			cache:       make(map[string]*bindingCacheEntry),
		}
	})
	return defaultBindingResolver
}

// Resolve 根据 spaceUID 解析到一条 phase=Ok 的 SurrealDBBinding。
func (r *BindingResolver) Resolve(ctx context.Context, spaceUID string) (*BindingInfo, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "cmdb-v2-binding-resolver")
	defer span.End(&err)

	span.Set("space-uid", spaceUID)

	bizID, err := parseBkBizIDFromSpaceUID(spaceUID)
	if err != nil {
		ObserveBindingLookup(spaceUID, "error")
		return nil, &BindingLookupError{SpaceUID: spaceUID, Reason: err.Error()}
	}
	span.Set("bk-biz-id", bizID)
	tenantID := metadata.GetUser(ctx).TenantID
	cacheKey := bindingCacheKey(tenantID, bizID)

	if info := r.lookupCache(cacheKey); info != nil {
		ObserveBindingLookup(spaceUID, "hit_cache")
		span.Set("cache", "hit")
		return info, nil
	}
	span.Set("cache", "miss")

	info, err := r.fetchFromRedis(ctx, tenantID, spaceUID, bizID)
	if err != nil {
		ObserveBindingLookup(spaceUID, "error")
		return nil, err
	}
	if info == nil {
		ObserveBindingLookup(spaceUID, "not_found")
		return nil, &BindingLookupError{SpaceUID: spaceUID, Reason: fmt.Sprintf("no usable SurrealDBBinding found for bk_biz_id=%s", bizID)}
	}

	r.storeCache(cacheKey, info)
	ObserveBindingCacheSize(r.cacheSize())
	ObserveBindingLookup(spaceUID, "miss_cache")
	span.Set("binding-name", info.Name)
	span.Set("binding-database", info.Database)
	span.Set("binding-namespace", info.Namespace)
	return info, nil
}

func (r *BindingResolver) lookupCache(cacheKey string) *BindingInfo {
	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()
	entry, ok := r.cache[cacheKey]
	if !ok {
		return nil
	}
	if time.Now().After(entry.expiry) {
		return nil
	}
	return entry.info
}

func (r *BindingResolver) storeCache(cacheKey string, info *BindingInfo) {
	ttl := BindingCacheTTL
	if ttl <= 0 {
		ttl = DefaultBindingCacheTTL
	}
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	r.cache[cacheKey] = &bindingCacheEntry{
		info:   info,
		expiry: time.Now().Add(ttl),
	}
}

func bindingCacheKey(tenantID, bizID string) string {
	return fmt.Sprintf("%s:%s", tenantID, bizID)
}

func (r *BindingResolver) cacheSize() int {
	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()
	return len(r.cache)
}

func defaultBindingRedisLookup(ctx context.Context, key, field string) (string, error) {
	return uqredis.HGet(ctx, key, field)
}

func (r *BindingResolver) fetchFromRedis(ctx context.Context, tenantID, spaceUID, bizID string) (*BindingInfo, error) {
	lookup := r.redisLookup
	if lookup == nil {
		lookup = defaultBindingRedisLookup
	}
	key := BindingRedisKey
	if key == "" {
		key = DefaultBindingRedisKey
	}

	for _, field := range bindingRedisFields(tenantID, spaceUID) {
		value, err := lookup(ctx, key, field)
		if errors.Is(err, goRedis.Nil) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get SurrealDBBinding route from redis failed: key=%s field=%s: %w", key, field, err)
		}
		if value == "" {
			continue
		}

		info, err := decodeBindingInfo(value)
		if err != nil {
			return nil, fmt.Errorf("decode SurrealDBBinding route failed: key=%s field=%s: %w", key, field, err)
		}
		if info.BkBizID == "" {
			info.BkBizID = bizID
		} else if info.BkBizID != bizID {
			return nil, fmt.Errorf("SurrealDBBinding route biz mismatch: key=%s field=%s binding_bk_biz_id=%s request_bk_biz_id=%s", key, field, info.BkBizID, bizID)
		}
		if info.Phase != "" && info.Phase != "Ok" {
			return nil, fmt.Errorf("SurrealDBBinding route is not ready: key=%s field=%s phase=%s", key, field, info.Phase)
		}
		if info.Database == "" || info.Namespace == "" {
			return nil, fmt.Errorf("SurrealDBBinding route missing database or namespace: key=%s field=%s", key, field)
		}
		return info, nil
	}

	return nil, nil
}

func bindingRedisFields(tenantID, spaceUID string) []string {
	if tenantID == "" {
		return []string{spaceUID}
	}
	return []string{bindingRedisField(spaceUID, tenantID)}
}

func bindingRedisField(spaceUID, tenantID string) string {
	return fmt.Sprintf("%s|%s", spaceUID, tenantID)
}

func decodeBindingInfo(value string) (*BindingInfo, error) {
	var detail bindingRouteDetail
	if err := json.Unmarshal([]byte(value), &detail); err != nil {
		return nil, err
	}
	return &BindingInfo{
		Name:        detail.Name,
		BkBizID:     detail.BkBizID,
		Database:    detail.Database,
		Namespace:   detail.Namespace,
		ClusterName: detail.ClusterName,
		Phase:       detail.Phase,
	}, nil
}

// parseBkBizIDFromSpaceUID 把形如 "bkcc__39" 的 spaceUID 解析成 "39"。
//
// 阶段一仅支持 bkcc 前缀；其它 space 类型（bkci / bksaas / bcs）返回错误，符合
// 11.2 的硬失败策略 —— 这些 space 目前也不会有 SurrealDBBinding。
func parseBkBizIDFromSpaceUID(spaceUID string) (string, error) {
	parts := strings.SplitN(spaceUID, "__", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid spaceUID %q, expect <type>__<id>", spaceUID)
	}
	if parts[0] != "bkcc" {
		return "", fmt.Errorf("v1beta3 currently only supports bkcc__ spaceUIDs, got %q", spaceUID)
	}
	if parts[1] == "" {
		return "", fmt.Errorf("invalid spaceUID %q, empty biz id", spaceUID)
	}
	return parts[1], nil
}
