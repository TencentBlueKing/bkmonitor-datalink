// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package kaccompat

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"linkd/internal/store"
)

type alarmKey struct {
	tenant string
	alert  string
}

// locate 只定位原生告警的物理索引，批次内多租户共用一次搜索且各自限定租户。
func (c *Client) locate(ctx context.Context, keys []alarmKey) (map[alarmKey]string, error) {
	locations := make(map[alarmKey]string, len(keys))
	if len(keys) == 0 {
		return locations, nil
	}
	// 1. 复用已写入位置，rollover 后仍更新原物理索引。
	missing := map[string]map[string]bool{}
	c.mu.Lock()
	for _, key := range keys {
		if index, ok := c.locations[key]; ok {
			locations[key] = index
		}
	}
	c.mu.Unlock()
	for _, key := range keys {
		if _, ok := locations[key]; ok {
			continue
		}
		if missing[key.tenant] == nil {
			missing[key.tenant] = map[string]bool{}
		}
		missing[key.tenant][key.alert] = true
	}
	if len(missing) == 0 {
		return locations, nil
	}
	// 2. 每个租户的 ID 与租户条件绑定，不跨租户拼成笛卡尔积。
	clauses := []any{}
	size := 0
	for tenant, candidates := range missing {
		ids := make([]string, 0, len(candidates))
		for id := range candidates {
			ids = append(ids, id)
		}
		size += len(ids)
		clauses = append(clauses, map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{"bk_tenant_id": tenant}},
			map[string]any{"ids": map[string]any{"values": ids}},
		}}})
	}
	body := map[string]any{"size": size, "_source": []string{"bk_tenant_id"},
		"query": map[string]any{"bool": map[string]any{"should": clauses, "minimum_should_match": 1}}}
	var response struct {
		Took     *int64 `json:"took"`
		TimedOut bool   `json:"timed_out"`
		Shards   struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Hits []struct {
				ID     string `json:"_id"`
				Index  string `json:"_index"`
				Source struct {
					Tenant string `json:"bk_tenant_id"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	started := time.Now()
	err := c.request(ctx, http.MethodPost, "/"+url.PathEscape(c.config.AlarmEventIndex)+"/_search", nil, body, &response)
	outcome := "succeeded"
	if err != nil {
		outcome = "failed"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome = "canceled"
		}
	}
	store.ObserveBatchPhase(ctx, "projection.search."+outcome, time.Since(started))
	if err != nil {
		return nil, err
	}
	if response.Took != nil {
		store.ObserveBatchPhase(ctx, "projection.es_search_took", time.Duration(*response.Took)*time.Millisecond)
	}
	if response.TimedOut || response.Shards.Failed > 0 {
		return nil, fmt.Errorf("KAC document lookup returned incomplete results")
	}
	// 3. 只缓存已存在的原生文档，新文档由成功的 Bulk 回填位置。
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.locations)+len(response.Hits.Hits) >= 100000 {
		clear(c.locations)
	}
	for _, hit := range response.Hits.Hits {
		key := alarmKey{tenant: hit.Source.Tenant, alert: hit.ID}
		locations[key], c.locations[key] = hit.Index, hit.Index
	}
	return locations, nil
}
