// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kaccompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/internaltoken"
)

// ActionRequest 是 KAC V3 实际消费的小参数，不包含 Alert 或摘要。
type ActionRequest struct {
	SchemaVersion string `json:"schema_version"`
	TenantID      string `json:"bk_tenant_id"`
	AlertID       string `json:"linkd_alert_id"`
	domain.AlertAction
}

// ActionSender 执行一次 HTTP 调用，失败不生成新任务。
type ActionSender interface {
	Send(context.Context, ActionRequest) error
}

// HTTPSender 复用独立连接池，鉴权信息只存在于配置和 HTTP 头。
type HTTPSender struct {
	client    *http.Client
	transport *http.Transport
	config    config.KACPluginConfig
}

// NewHTTPSender 创建可并发复用的发送器。
func NewHTTPSender(cfg config.KACPluginConfig) *HTTPSender {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = cfg.WithDefaults().Delivery.ActionConcurrency
	tr.MaxIdleConns = tr.MaxIdleConnsPerHost
	return &HTTPSender{config: cfg.WithDefaults(), transport: tr, client: &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Close 在运行器等待已开始的发送结束后调用。
func (s *HTTPSender) Close() { s.transport.CloseIdleConnections() }

// Send 将正常入参交给 KAC 校验，只检查 HTTP 成败，不核对业务回执或摘要。
func (s *HTTPSender) Send(ctx context.Context, q ActionRequest) error {
	raw, err := json.Marshal(q)
	if err != nil {
		return err
	}
	signer, err := internaltoken.New(s.config.JWT.SecretKey, nil)
	if err != nil {
		return err
	}
	token, err := signer.Sign(s.config.JWT.Username)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.config.ActionEndpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internaltoken.HeaderName, token)
	response, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("KAC action transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("KAC action HTTP %d", response.StatusCode)
	}
	return nil
}
