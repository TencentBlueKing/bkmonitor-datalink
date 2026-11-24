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

// Tenant 是租户目录项，不表示该租户已接入 Linkd 或已获业务访问授权。
type Tenant struct {
	ID     string `json:"id"`     // ID 是稳定租户标识，单租户模式固定为 system。
	Name   string `json:"name"`   // Name 是租户展示名，单租户模式固定为 Blueking。
	Status string `json:"status"` // Status 保留用户管理的状态值，包括 disabled。
}

// SingleTenantDirectory 在单租户部署中提供固定目录，无需蓝鲸连接或应用凭据。
// 默认目录与 Kingeye 保持一致，不按来源配置推断租户，也不改变业务数据的租户身份。
type SingleTenantDirectory struct{}

// ListTenants 返回独立的 system/Blueking/enabled 目录，调用方修改结果不影响后续查询。
func (SingleTenantDirectory) ListTenants(ctx context.Context) ([]Tenant, error) {
	if ctx == nil {
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []Tenant{{ID: "system", Name: "Blueking", Status: "enabled"}}, nil
}

// ListTenants 在单租户模式返回固定 system 目录，多租户模式实时读取用户管理且不缓存或筛选状态。
// 多租户目录固定使用 system/admin 引导身份，避免递归解析未知租户的 bk_admin。
// 完整上游响应最多 1 MiB/4096 项，失败不返回部分目录或回退默认租户。
func (c *Client) ListTenants(ctx context.Context) ([]Tenant, error) {
	if ctx == nil {
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 本地目录也必须遵守客户端关闭契约；在锁内读取，使它与 Close 的状态切换有明确先后。
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, ErrClosed
	}
	if !c.cfg.EnableMultiTenantMode {
		return (SingleTenantDirectory{}).ListTenants(ctx)
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
