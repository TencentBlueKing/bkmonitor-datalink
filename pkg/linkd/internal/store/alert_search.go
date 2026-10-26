// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package store

import (
	"encoding/json"

	"linkd/internal/domain"
)

// AlertSearchRequest 使用 Elasticsearch Query DSL 检索原生 Alert；仅支持此能力的后端实现查询。
// HTTP 边界负责校验租户、分页与 Query 对象，省略 Query 时匹配当前租户全部告警。
type AlertSearchRequest struct {
	// TenantID 是强制作用域，不能被 Query 中的租户条件替代。
	TenantID string `json:"bk_tenant_id"`
	// Query 是原生查询子句，不是包含索引、聚合或排序的完整 _search 请求。
	Query json.RawMessage `json:"query,omitempty"`
	// After 是上一页最后一条 AlertID；结果固定按 AlertID 升序，不承诺跨页快照。
	After string `json:"after,omitempty"`
	// Limit 是当前页最大数量，HTTP 边界默认使用 DefaultPageSize，上限为 MaxPageSize。
	Limit int `json:"limit,omitempty"`
}

// AlertSearchPage 返回完整的原生告警快照，归档副本按 AlertID 折叠。
type AlertSearchPage struct {
	Items []domain.Alert `json:"items"` // Items 不包含存储 CAS token 或物理索引。
	Next  string         `json:"next"`  // Next 为空表示此次读取没有更多匹配告警。
}
