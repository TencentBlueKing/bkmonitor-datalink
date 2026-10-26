// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package store

import (
	"context"
	"fmt"

	"linkd/internal/domain"
)

// ActiveIndexRow 是活跃缓存修复所需的窄存储投影，不包含正文或 Enrich。
type ActiveIndexRow struct {
	BKTenantID    string              `json:"bk_tenant_id"`
	EventSourceID string              `json:"event_source_id"`
	AlertID       string              `json:"alert_id"`
	Fingerprint   string              `json:"fingerprint"`
	Labels        domain.DimensionMap `json:"labels,omitempty"`
	Dimensions    domain.DimensionMap `json:"dimensions,omitempty"`
	SubjectSystem string              `json:"subject_system,omitempty"`
	SubjectType   string              `json:"subject_type,omitempty"`
	SubjectID     string              `json:"subject_id,omitempty"`
}

// ActiveIndexQuery 约束流式扫描的来源、原始字段和单页预算。
// 指定 Fingerprints 时按租户批量核对集合成员，不进行逐成员查询。
type ActiveIndexQuery struct {
	Sources      []string
	BKTenantID   string
	Fingerprints []string
	Fields       []string
	PageSize     int
	MaxBytes     int
}

// Validate 在只读扫描入口检查来源和分页预算。
func (q ActiveIndexQuery) Validate() error {
	if len(q.Sources) == 0 || q.PageSize < 1 || q.PageSize > 10000 || q.MaxBytes < 1 || (len(q.Fingerprints) > 0 && q.BKTenantID == "") {
		return fmt.Errorf("invalid active index query")
	}
	return nil
}

// ActiveIndexReader 完整交付指定范围内的活跃原始字段；失败不能解释为不存在。
type ActiveIndexReader interface {
	ScanActiveIndex(context.Context, ActiveIndexQuery, func([]ActiveIndexRow) error) error
}
