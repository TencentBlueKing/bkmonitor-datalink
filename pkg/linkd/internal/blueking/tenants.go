// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package blueking

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"linkd/internal/domain"
)

const maxTenants = 4096

// Tenant 是用户管理返回的租户目录项，不表示该租户已接入 Linkd 或已获业务访问授权。
type Tenant struct {
	ID     string `json:"id"`     // ID 是用户管理中的稳定租户标识。
	Name   string `json:"name"`   // Name 是用户管理中的租户展示名。
	Status string `json:"status"` // Status 保留用户管理的状态值，包括 disabled。
}

// ListTenants 实时读取用户管理租户目录，不缓存、不筛选状态，也不按来源配置推断租户。
// 全局目录固定使用 system/admin 引导身份，避免依赖某个尚未知晓的租户或递归解析 bk_admin。
// 不受多租户用户解析开关影响；完整响应最多 1 MiB/4096 项，失败不返回部分目录。
func (c *Client) ListTenants(ctx context.Context) ([]Tenant, error) {
	if ctx == nil {
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 不为管理查询积累无界等待者；最多四个目录调用参与共享 HTTP 配额竞争，超额立即失败。
	select {
	case c.tenants <- struct{}{}:
		defer func() { <-c.tenants }()
	default:
		return nil, ErrUnavailable
	}
	endpoint := strings.TrimRight(c.cfg.APIURL, "/") + "/api/bk-user/prod/api/v3/open/tenants/"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	raw, err := c.perform(ctx, "system", "admin", request, 1<<20)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Code   *int64   `json:"code"`
		Result *bool    `json:"result"`
		Data   []Tenant `json:"data"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if d.Decode(&envelope) != nil {
		return nil, ErrUnavailable
	}
	var trailing any
	if !errors.Is(d.Decode(&trailing), io.EOF) || envelope.Code == nil || *envelope.Code != 0 || envelope.Result != nil && !*envelope.Result || envelope.Data == nil || len(envelope.Data) > maxTenants {
		return nil, ErrUnavailable
	}
	seen := make(map[string]struct{}, len(envelope.Data))
	for _, tenant := range envelope.Data {
		if domain.ValidateIdentityPart("tenant", tenant.ID, 64) != nil || !validTenantText(tenant.Name, 256) || !validTenantText(tenant.Status, 64) {
			return nil, ErrUnavailable
		}
		if _, exists := seen[tenant.ID]; exists {
			return nil, ErrUnavailable
		}
		seen[tenant.ID] = struct{}{}
	}
	return envelope.Data, nil
}

func validTenantText(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}
