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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	uqredis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// BindingInfo 是 unify-query 直连 SurrealDB 所需的结果表路由信息。
type BindingInfo struct {
	Name        string // result table ID
	BkBizID     string // space UID 中的业务 ID
	Database    string // result_table_detail.database
	Namespace   string // result_table_detail.namespace，如 "mapleleaf_39"
	ClusterName string // result_table_detail.cluster_name
	StorageID   string // result_table_detail.storage_id，Storage Map 的 key
	StorageType string // result_table_detail.storage_type
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

type bindingCacheSweepStats struct {
	removed int
	size    int
}

type bindingCacheStoreStats struct {
	expiredRemoved int
	evicted        bool
	size           int
}

type bindingRedisLookup func(ctx context.Context, key, field string) (string, error)

type bindingSubscription interface {
	Channel(opts ...goRedis.ChannelOption) <-chan *goRedis.Message
	Close() error
}

type bindingSubscribe func(ctx context.Context, channels ...string) bindingSubscription

const (
	BindingRedisChannel             = "bkmonitorv3:spaces:surrealdb_binding:channel"
	ResultTableDetailChannel        = "bkmonitorv3:spaces:result_table_detail:channel"
	BuiltInResultTableDetailChannel = "bkmonitorv3:spaces:built_in_result_table_detail:channel"
)

type bindingRouteDetail struct {
	Name        string `json:"name"`
	BkBizID     string `json:"bk_biz_id"`
	Database    string `json:"database"`
	Namespace   string `json:"namespace"`
	ClusterName string `json:"cluster_name"`
	StorageID   string `json:"storage_id"`
	StorageType string `json:"storage_type"`
	Phase       string `json:"phase"`
}

// BindingResolver 解析 spaceUID → BindingInfo，带 TTL 缓存。
type BindingResolver struct {
	redisLookup bindingRedisLookup

	cacheMu sync.RWMutex
	cache   map[string]*bindingCacheEntry // key = bk_biz_id
}

type bindingChange struct {
	SpaceUID string `json:"space_uid"`
	BkBizID  string `json:"bk_biz_id"`
	Field    string `json:"field"`
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
func (r *BindingResolver) Resolve(ctx context.Context, spaceUID string) (info *BindingInfo, err error) {
	ctx, span := trace.NewSpan(ctx, "cmdb-v2-binding-resolver")
	defer endV1Beta3TraceSpan(span, &err)

	span.Set("space-uid", spaceUID)

	bizID, err := parseBkBizIDFromSpaceUID(spaceUID)
	if err != nil {
		ObserveBindingLookup(spaceUID, "error")
		return nil, &BindingLookupError{SpaceUID: spaceUID, Reason: err.Error()}
	}
	span.Set("bk-biz-id", bizID)
	tenantID := metadata.GetUser(ctx).TenantID
	// 同一个 bk_biz_id 在不同租户下可能对应不同 SurrealDBBinding；
	// 缓存键必须带 tenantID，避免命中其它租户的 namespace/database。
	cacheKey := bindingCacheKey(tenantID, bizID)
	sweepStats := r.sweepExpiredCache(time.Now())
	span.Set("cache-expired-removed", sweepStats.removed)
	span.Set("cache-size", sweepStats.size)

	if info := r.lookupCache(cacheKey); info != nil {
		ObserveBindingLookup(spaceUID, "hit_cache")
		span.Set("cache", "hit")
		span.Set("lookup-result", "hit-cache")
		return info, nil
	}
	span.Set("cache", "miss")

	info, err = r.fetchFromRedis(ctx, tenantID, spaceUID, bizID)
	if err != nil {
		ObserveBindingLookup(spaceUID, "error")
		span.Set("lookup-result", "error")
		return nil, err
	}
	if info == nil {
		ObserveBindingLookup(spaceUID, "not_found")
		span.Set("lookup-result", "not-found")
		return nil, &BindingLookupError{SpaceUID: spaceUID, Reason: fmt.Sprintf("no usable SurrealDB result table route found for bk_biz_id=%s", bizID)}
	}

	storeStats := r.storeCache(cacheKey, info)
	ObserveBindingLookup(spaceUID, "miss_cache")
	span.Set("lookup-result", "miss-cache")
	span.Set("cache-expired-removed-on-store", storeStats.expiredRemoved)
	span.Set("cache-evicted", storeStats.evicted)
	span.Set("cache-size", storeStats.size)
	span.Set("binding-name", info.Name)
	span.Set("binding-database", info.Database)
	span.Set("binding-namespace", info.Namespace)
	return info, nil
}

func (r *BindingResolver) InvalidateSpace(spaceUID string) {
	bizID, err := parseBkBizIDFromSpaceUID(spaceUID)
	if err != nil {
		return
	}
	r.cacheMu.Lock()
	for key := range r.cache {
		if strings.HasSuffix(key, ":"+bizID) {
			delete(r.cache, key)
		}
	}
	size := len(r.cache)
	r.cacheMu.Unlock()
	ObserveBindingCacheSize(size)
}

func (r *BindingResolver) InvalidateAll() {
	r.cacheMu.Lock()
	r.cache = make(map[string]*bindingCacheEntry)
	r.cacheMu.Unlock()
	ObserveBindingCacheSize(0)
}

func StartBindingResolverWatcher(ctx context.Context) <-chan struct{} {
	return startBindingResolverWatcher(ctx, GetBindingResolver(), func(ctx context.Context, channels ...string) bindingSubscription {
		return uqredis.SubscribePubSub(ctx, channels...)
	})
}

func startBindingResolverWatcher(ctx context.Context, resolver *BindingResolver, subscribe bindingSubscribe) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		channels := []string{
			BindingRedisChannel,
			ResultTableDetailChannel,
			BuiltInResultTableDetailChannel,
		}
		for {
			if ctx.Err() != nil {
				return
			}
			if !watchBindingSubscription(ctx, resolver, subscribe(ctx, channels...)) {
				return
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return done
}

func watchBindingSubscription(ctx context.Context, resolver *BindingResolver, subscription bindingSubscription) bool {
	defer subscription.Close()
	messages := subscription.Channel()
	for {
		select {
		case <-ctx.Done():
			return false
		case message, ok := <-messages:
			if !ok {
				return true
			}
			handleBindingChange(resolver, message)
		}
	}
}

func handleBindingChange(resolver *BindingResolver, message *goRedis.Message) {
	if message == nil {
		return
	}
	if message.Channel == BuiltInResultTableDetailChannel {
		if spaceUID := parseBindingChangeSpaceUID(message.Payload); spaceUID != "" {
			resolver.InvalidateSpace(spaceUID)
		}
		return
	}
	if spaceUID := parseBindingChangeSpaceUID(message.Payload); spaceUID != "" {
		resolver.InvalidateSpace(spaceUID)
	} else {
		resolver.InvalidateAll()
	}
}

func parseBindingChangeSpaceUID(payload string) string {
	var change bindingChange
	if json.Unmarshal([]byte(payload), &change) == nil {
		if change.SpaceUID != "" {
			return parseBindingFieldSpaceUID(change.SpaceUID)
		}
		if change.BkBizID != "" {
			return "bkcc__" + change.BkBizID
		}
		if change.Field != "" {
			return parseBindingFieldSpaceUID(change.Field)
		}
	}
	return parseBindingFieldSpaceUID(payload)
}

func parseBindingFieldSpaceUID(field string) string {
	field = strings.TrimSpace(field)
	if separator := strings.IndexByte(field, '|'); separator >= 0 {
		field = field[:separator]
	}
	if strings.HasPrefix(field, "bkcc__") {
		return field
	}
	return ""
}

func (r *BindingResolver) lookupCache(cacheKey string) *BindingInfo {
	r.cacheMu.RLock()
	entry, ok := r.cache[cacheKey]
	if !ok {
		r.cacheMu.RUnlock()
		return nil
	}
	if time.Now().After(entry.expiry) {
		r.cacheMu.RUnlock()
		r.deleteExpiredCache(cacheKey, entry)
		return nil
	}
	r.cacheMu.RUnlock()
	return entry.info
}

func (r *BindingResolver) deleteExpiredCache(cacheKey string, expiredEntry *bindingCacheEntry) {
	r.cacheMu.Lock()
	if r.cache[cacheKey] == expiredEntry {
		delete(r.cache, cacheKey)
	}
	size := len(r.cache)
	r.cacheMu.Unlock()
	ObserveBindingCacheSize(size)
}

func (r *BindingResolver) storeCache(cacheKey string, info *BindingInfo) bindingCacheStoreStats {
	ttl := BindingCacheTTL
	if ttl <= 0 {
		ttl = DefaultBindingCacheTTL
	}
	now := time.Now()
	r.cacheMu.Lock()
	if r.cache == nil {
		r.cache = make(map[string]*bindingCacheEntry)
	}
	expiredRemoved := 0
	for key, entry := range r.cache {
		if entry == nil || !now.Before(entry.expiry) {
			delete(r.cache, key)
			expiredRemoved++
		}
	}
	maxSize := BindingCacheMaxSize
	if maxSize <= 0 {
		maxSize = DefaultBindingCacheMaxSize
	}
	evicted := false
	if _, exists := r.cache[cacheKey]; !exists && len(r.cache) >= maxSize {
		var oldestKey string
		var oldestExpiry time.Time
		for key, entry := range r.cache {
			if entry == nil || oldestKey == "" || entry.expiry.Before(oldestExpiry) {
				oldestKey = key
				if entry != nil {
					oldestExpiry = entry.expiry
				}
			}
		}
		if oldestKey != "" {
			delete(r.cache, oldestKey)
			evicted = true
		}
	}
	r.cache[cacheKey] = &bindingCacheEntry{
		info:   info,
		expiry: now.Add(ttl),
	}
	size := len(r.cache)
	r.cacheMu.Unlock()
	ObserveBindingCacheSize(size)
	return bindingCacheStoreStats{expiredRemoved: expiredRemoved, evicted: evicted, size: size}
}

func (r *BindingResolver) sweepExpiredCache(now time.Time) bindingCacheSweepStats {
	r.cacheMu.Lock()
	removed := 0
	for key, entry := range r.cache {
		if entry == nil || !now.Before(entry.expiry) {
			delete(r.cache, key)
			removed++
		}
	}
	size := len(r.cache)
	r.cacheMu.Unlock()
	ObserveBindingCacheSize(size)
	return bindingCacheSweepStats{removed: removed, size: size}
}

func bindingCacheKey(tenantID, bizID string) string {
	return fmt.Sprintf("%s:%s", tenantID, bizID)
}

func (r *BindingResolver) cacheSize() int {
	return r.sweepExpiredCache(time.Now()).size
}

func defaultBindingRedisLookup(ctx context.Context, key, field string) (string, error) {
	return uqredis.HGet(ctx, key, field)
}

func (r *BindingResolver) fetchFromRedis(ctx context.Context, tenantID, spaceUID, bizID string) (*BindingInfo, error) {
	lookup := r.redisLookup
	if lookup == nil {
		lookup = defaultBindingRedisLookup
	}
	info, err := r.fetchFromResultTableRoute(ctx, tenantID, spaceUID, bizID, lookup)
	if err != nil {
		return nil, err
	}
	return info, nil
}

func (r *BindingResolver) fetchFromResultTableRoute(ctx context.Context, tenantID, spaceUID, bizID string, lookup bindingRedisLookup) (*BindingInfo, error) {
	fields := routeRedisFields(tenantID, spaceUID)
	spaceValue, err := lookup(ctx, DefaultSpaceToResultTableRedisKey, fields[0])
	if errors.Is(err, goRedis.Nil) || spaceValue == "" {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get space_to_result_table route from redis failed: %w", err)
	}
	var tableRoutes map[string]any
	if err := json.Unmarshal([]byte(spaceValue), &tableRoutes); err != nil {
		return nil, fmt.Errorf("decode space_to_result_table route failed: %w", err)
	}
	tableIDs := make([]string, 0, len(tableRoutes))
	for tableID := range tableRoutes {
		tableIDs = append(tableIDs, tableID)
	}
	sort.Strings(tableIDs)

	for _, tableID := range tableIDs {
		value, err := lookup(ctx, DefaultResultTableDetailRedisKey, routeRedisFields(tenantID, tableID)[0])
		if errors.Is(err, goRedis.Nil) || value == "" {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get result_table_detail route from redis failed: table_id=%s: %w", tableID, err)
		}
		var detail map[string]any
		if err := json.Unmarshal([]byte(value), &detail); err != nil {
			return nil, fmt.Errorf("decode result_table_detail route failed: table_id=%s: %w", tableID, err)
		}
		surrealdbDetail := surrealDBRouteDetail(detail)
		if surrealdbDetail == nil {
			continue
		}
		storageID := routeStringValue(surrealdbDetail["storage_id"], "")
		if storageID == "" {
			return nil, fmt.Errorf("result_table_detail route missing storage_id: table_id=%s", tableID)
		}
		database := routeStringValue(surrealdbDetail["database"], routeStringValue(surrealdbDetail["db"], ""))
		namespace := routeStringValue(surrealdbDetail["namespace"], "")
		if database == "" || namespace == "" {
			return nil, fmt.Errorf("result_table_detail route missing database or namespace: table_id=%s", tableID)
		}
		return &BindingInfo{
			Name:        tableID,
			BkBizID:     bizID,
			Database:    database,
			Namespace:   namespace,
			ClusterName: routeStringValue(surrealdbDetail["cluster_name"], routeStringValue(surrealdbDetail["storage_name"], "")),
			StorageID:   storageID,
			StorageType: metadata.SurrealDBStorageType,
			Phase:       "Ok",
		}, nil
	}
	return nil, nil
}

func surrealDBRouteDetail(detail map[string]any) map[string]any {
	// SurrealDB-only 路由直接使用顶层配置；VM + SurrealDB 双写路由则读取
	// surrealdb 子配置，避免改变通用时序查询消费的顶层 VM 路由。
	if routeStringValue(detail["storage_type"], "") == metadata.SurrealDBStorageType {
		return detail
	}
	nested, ok := detail[metadata.SurrealDBStorageType].(map[string]any)
	if !ok || routeStringValue(nested["storage_type"], "") != metadata.SurrealDBStorageType {
		return nil
	}
	return nested
}

func routeRedisFields(tenantID, key string) []string {
	if tenantID == "" {
		return []string{key}
	}
	return []string{key + "|" + tenantID}
}

func routeStringValue(value any, fallback string) string {
	switch typed := value.(type) {
	case string:
		if typed != "" {
			return typed
		}
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case json.Number:
		return typed.String()
	}
	return fallback
}

func bindingRedisFields(tenantID, spaceUID string) []string {
	if tenantID == "" {
		// 老环境没有租户上下文时仍沿用 spaceUID 字段，保证单租户灰度验证可用。
		return []string{spaceUID}
	}
	// 多租户环境只查带租户后缀的字段，不 fallback 到裸 spaceUID，
	// 防止租户缺失配置时误读全局旧字段。
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
		StorageID:   detail.StorageID,
		StorageType: detail.StorageType,
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
