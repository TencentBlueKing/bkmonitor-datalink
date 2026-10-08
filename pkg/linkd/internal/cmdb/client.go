// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

// Package cmdb 通过全局蓝鲸 APIGW 客户端只读 CMDB，提供完整且有界的实例与拓扑事实。
// 不读写 KAC 数据，不重试部分分页，也不把读取失败降级为空集合。
package cmdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"linkd/internal/config"
	"linkd/internal/domain"
)

// ErrUnavailable 表示蓝鲸身份解析或远端读取失败，不应解释为零成员。
var ErrUnavailable = errors.New("CMDB unavailable")

// ErrIncomplete 表示响应不完整、身份冲突或超出硬预算。
var ErrIncomplete = errors.New("CMDB response incomplete")

// API 是 CMDB 消费的 APIGW 端口；实现负责用户解析、鉴权、超时和原始响应字节上限。
type API interface {
	Do(context.Context, string, *http.Request) ([]byte, error)
}

// Client 只处理 CMDB 路径与响应契约；共享 API 的生命周期由装配方管理。
type Client struct {
	base string
	api  API
}

// New 绑定完整 CMDB 网关前缀与已配置的蓝鲸客户端，不读取来源内凭据。
func New(baseURL string, api API) (*Client, error) {
	if api == nil || baseURL == "" {
		return nil, ErrUnavailable
	}
	if err := (config.CMDBResource{BaseURL: baseURL}).Validate(); err != nil {
		return nil, err
	}
	return &Client{base: strings.TrimRight(baseURL, "/"), api: api}, nil
}

// request 使用 KAC 固定版本中的 API 路径映射，禁止调用方提供任意路径/请求头。
// APIGW 端口统一处理有界身份查询和 HTTP；错误不包含凭据或响应体。
func (c *Client) request(ctx context.Context, tenant string, biz int64, action string, body map[string]any, result any) error {
	if ctx == nil || biz < 1 {
		return ErrUnavailable
	}
	if err := domain.ValidateIdentityPart("tenant", tenant, 64); err != nil {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	method, path := http.MethodPost, ""
	switch action {
	case "list_service_instance_detail":
		path = "api/v3/findmany/proc/service_instance/details"
	case "list_biz_hosts":
		path = "api/v3/hosts/app/" + strconv.FormatInt(biz, 10) + "/list_hosts"
	case "find_host_by_topo":
		path = "api/v3/findmany/hosts/by_topo/biz/" + strconv.FormatInt(biz, 10)
	case "search_biz_inst_topo":
		path = "api/v3/find/topoinst/biz/" + strconv.FormatInt(biz, 10)
	case "get_biz_internal_module":
		method = http.MethodGet
		path = "api/v3/topo/internal/0/" + strconv.FormatInt(biz, 10)
	default:
		return ErrUnavailable
	}
	params := map[string]any{"bk_biz_id": biz, "bk_tenant_id": tenant}
	for k, v := range body {
		if k == "bk_biz_id" || k == "bk_tenant_id" {
			return ErrUnavailable
		}
		params[k] = v
	}
	raw, err := json.Marshal(params)
	if err != nil || len(raw) > 1<<20 {
		return ErrIncomplete
	}
	endpoint := c.base + "/" + path
	var input io.Reader = bytes.NewReader(raw)
	if method == http.MethodGet {
		q := url.Values{"bk_biz_id": {strconv.FormatInt(biz, 10)}, "bk_tenant_id": {tenant}, "bk_supplier_account": {"0"}}
		endpoint += "?" + q.Encode()
		input = nil
	}
	r, err := http.NewRequestWithContext(ctx, method, endpoint, input)
	if err != nil {
		return ErrUnavailable
	}
	data, err := c.api.Do(ctx, tenant, r)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, action, err)
	}
	var envelope struct {
		Result *bool           `json:"result"`
		Code   *int64          `json:"code"`
		Data   json.RawMessage `json:"data"`
	}
	if err := decode(data, &envelope); err != nil {
		return err
	}
	if envelope.Result == nil || !*envelope.Result || envelope.Code == nil || *envelope.Code != 0 {
		return fmt.Errorf("%w: %s rejected", ErrUnavailable, action)
	}
	if len(envelope.Data) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
		return ErrIncomplete
	}
	return decode(envelope.Data, result)
}

func decode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(out) != nil {
		return ErrIncomplete
	}
	var trailing any
	if !errors.Is(d.Decode(&trailing), io.EOF) {
		return ErrIncomplete
	}
	return nil
}
