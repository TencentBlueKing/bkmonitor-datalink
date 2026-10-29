// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package elasticsearchstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// SearchAlerts 在配置的 Active/History 查询目标上执行已经通过 HTTP 入参校验的 Query DSL。
// 不创建索引、不触发 refresh；与其他搜索读取一样，结果受 Elasticsearch refresh 窗口影响。
func (r *Repository) SearchAlerts(ctx context.Context, q store.AlertSearchRequest) (store.AlertSearchPage, error) {
	targets, err := r.router.TerminalAlertTargets(ctx)
	if err != nil {
		return store.AlertSearchPage{}, err
	}
	active, err := r.router.ActiveAlertTargets(ctx)
	if err != nil {
		return store.AlertSearchPage{}, err
	}
	targets, err = normalizeTargets(append(targets, active...), r.config.MaxReadTargets)
	if err != nil {
		return store.AlertSearchPage{}, err
	}
	filters := []any{map[string]any{"term": map[string]any{"bk_tenant_id": q.TenantID}}}
	if len(q.Query) != 0 {
		// 完整保留用户子句的嵌套结构，避免加入租户 filter 后改变 should 的默认语义。
		filters = append(filters, q.Query)
	}
	if q.After != "" {
		filters = append(filters, map[string]any{"range": map[string]any{"alert_id": map[string]any{"gt": q.After}}})
	}
	// 归档期间同一 Alert 可同时存在于 Active 和 History。折叠与排序使用同一字段，
	// 多取一条判断后续页，不把未折叠的 ES hits.total 当成告警总数。
	request := map[string]any{
		"size": q.Limit + 1, "track_total_hits": false, "seq_no_primary_term": true,
		"collapse": map[string]any{"field": "alert_id"},
		"query":    map[string]any{"bool": map[string]any{"filter": filters}},
		"sort":     []any{map[string]any{"alert_id": "asc"}},
	}
	if q.IDsOnly {
		request["_source"] = []string{"alert_id"}
		request["seq_no_primary_term"] = false
	}
	body, err := marshalRequest(request)
	if err != nil {
		return store.AlertSearchPage{}, err
	}
	var response searchResponse
	err = r.performJSON(ctx, http.MethodPost, "/"+joinTargets(targets)+"/_search", url.Values{
		"ignore_unavailable": {"true"}, "allow_partial_search_results": {"false"},
	}, body, &response)
	if err != nil {
		var rejected *responseError
		if errors.As(err, &rejected) {
			// ES 错误 reason 可能回显查询值，管理响应与日志仅保留状态和错误类型。
			rejected.Reason = ""
			if rejected.StatusCode == http.StatusBadRequest {
				return store.AlertSearchPage{}, fmt.Errorf("%w: alert query rejected: %w", store.ErrInvalidArgument, err)
			}
		}
		return store.AlertSearchPage{}, fmt.Errorf("search alerts: %w", err)
	}
	page := store.AlertSearchPage{Items: make([]domain.Alert, 0, min(q.Limit, len(response.Hits.Hits)))}
	for _, hit := range response.Hits.Hits {
		if len(page.Items) == q.Limit {
			page.Next = page.Items[len(page.Items)-1].AlertID
			break
		}
		if q.IDsOnly {
			var identity struct {
				AlertID string `json:"alert_id"`
			}
			if err := json.Unmarshal(hit.Source, &identity); err != nil {
				return store.AlertSearchPage{}, err
			}
			page.Items = append(page.Items, domain.Alert{AlertID: identity.AlertID})
			continue
		}
		item, err := decodeAlertHit(hit)
		if err != nil {
			return store.AlertSearchPage{}, fmt.Errorf("decode alert search hit: %w", err)
		}
		page.Items = append(page.Items, item.Alert)
	}
	return page, nil
}
