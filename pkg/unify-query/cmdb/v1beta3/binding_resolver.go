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
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// BindingResourceAPIURLConfigPath 指向 bkbase v4 namespaces 资源 API 的基础 URL
// 例：https://bkapi.xxx.com/api/bk-base/prod/v4/namespaces/bkmonitor
//
// 之所以独立配置：bkbase resource API 的路径不同于 query_sync（后者走
// bk_data.address），因此不能复用 GetBkDataAPI().QueryUrl()。鉴权 header 仍然
// 通过 GetBkDataAPI().Headers() 注入（X-Bkapi-Authorization + X-Bkbase-Authorization）。
const BindingResourceAPIURLConfigPath = "cmdb.v1beta3.bkbase.resource_api_url"

// BindingInfo 是 SurrealDBBinding 对象里 unify-query 查询需要的字段，
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

// bkbase list 响应结构，只反序列化需要的字段
type bindingListResponse struct {
	Result  bool          `json:"result"`
	Code    string        `json:"code"`
	Message string        `json:"message"`
	Data    []bindingItem `json:"data"`
}

type bindingItem struct {
	Metadata struct {
		Name        string            `json:"name"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Storage struct {
			Name string `json:"name"`
		} `json:"storage"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

// cache 条目
type bindingCacheEntry struct {
	info   *BindingInfo
	expiry time.Time
}

// BindingResolver 解析 spaceUID → BindingInfo，带 TTL 缓存。
type BindingResolver struct {
	curl curl.Curl

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
			curl:  &curl.HttpCurl{},
			cache: make(map[string]*bindingCacheEntry),
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

	if info := r.lookupCache(bizID); info != nil {
		ObserveBindingLookup(spaceUID, "hit_cache")
		span.Set("cache", "hit")
		return info, nil
	}
	span.Set("cache", "miss")

	info, err := r.fetchFromBKBase(ctx, bizID)
	if err != nil {
		ObserveBindingLookup(spaceUID, "error")
		return nil, err
	}
	if info == nil {
		ObserveBindingLookup(spaceUID, "not_found")
		return nil, &BindingLookupError{SpaceUID: spaceUID, Reason: fmt.Sprintf("no usable SurrealDBBinding found for bk_biz_id=%s", bizID)}
	}

	r.storeCache(bizID, info)
	ObserveBindingCacheSize(r.cacheSize())
	ObserveBindingLookup(spaceUID, "miss_cache")
	span.Set("binding-name", info.Name)
	span.Set("binding-database", info.Database)
	span.Set("binding-namespace", info.Namespace)
	return info, nil
}

// Invalidate 从 cache 中剔除指定 biz_id 的 binding（通常用于错误恢复）。
func (r *BindingResolver) Invalidate(bizID string) {
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	delete(r.cache, bizID)
}

func (r *BindingResolver) lookupCache(bizID string) *BindingInfo {
	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()
	entry, ok := r.cache[bizID]
	if !ok {
		return nil
	}
	if time.Now().After(entry.expiry) {
		return nil
	}
	return entry.info
}

func (r *BindingResolver) storeCache(bizID string, info *BindingInfo) {
	ttl := BindingCacheTTL
	if ttl <= 0 {
		ttl = DefaultBindingCacheTTL
	}
	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()
	r.cache[bizID] = &bindingCacheEntry{
		info:   info,
		expiry: time.Now().Add(ttl),
	}
}

func (r *BindingResolver) cacheSize() int {
	r.cacheMu.RLock()
	defer r.cacheMu.RUnlock()
	return len(r.cache)
}

func (r *BindingResolver) fetchFromBKBase(ctx context.Context, bizID string) (*BindingInfo, error) {
	baseURL := viper.GetString(BindingResourceAPIURLConfigPath)
	if baseURL == "" {
		return nil, fmt.Errorf("binding resource api url not configured (%s)", BindingResourceAPIURLConfigPath)
	}
	url := fmt.Sprintf("%s/surrealdbbindings/?label_selector=bk_biz_id=%s", strings.TrimRight(baseURL, "/"), bizID)

	var resp bindingListResponse
	headers := bkapi.GetBkDataAPI().Headers(map[string]string{"Content-Type": "application/json"})
	_, err := r.curl.Request(ctx, curl.Get, curl.Options{
		UrlPath: url,
		Headers: headers,
		Timeout: BKBaseSurrealDBTimeout,
	}, &resp)
	if err != nil {
		return nil, fmt.Errorf("list SurrealDBBinding failed: %w", err)
	}
	if !resp.Result {
		return nil, fmt.Errorf("bkbase list SurrealDBBinding response error: code=%s, message=%s", resp.Code, resp.Message)
	}

	// 过滤 phase=Ok 的 candidate，直接取第一条
	for i := range resp.Data {
		item := &resp.Data[i]
		if item.Status.Phase != "Ok" {
			continue
		}
		db := item.Metadata.Annotations["database"]
		ns := item.Metadata.Annotations["namespace"]
		if db == "" || ns == "" {
			// phase=Ok 但 annotations 缺失，跳过
			continue
		}
		return &BindingInfo{
			Name:        item.Metadata.Name,
			BkBizID:     item.Metadata.Labels["bk_biz_id"],
			Database:    db,
			Namespace:   ns,
			ClusterName: item.Spec.Storage.Name,
			Phase:       item.Status.Phase,
		}, nil
	}
	return nil, nil
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

// 构建简单的 list 接口辅助 URL（供测试/调试）
var _ = json.Marshal
