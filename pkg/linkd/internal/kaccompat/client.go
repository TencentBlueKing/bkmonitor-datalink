// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

// Package kaccompat 直接维护 KAC alarm_event 索引和兼容文档；不执行告警策略或处置。
// 版本、物理位置和暂存处置态保存于独立元数据索引，保持 KAC 原 mapping。
package kaccompat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/projection"
	elasticsearchstore "linkd/internal/store/elasticsearch"
)

// Transport 必须传播请求取消、不自动重试写入，并由装配方限制连接数与重定向。
type Transport interface {
	Perform(*http.Request) (*http.Response, error)
}

// Relations 读取稳定合并关系身份，供 KAC 兼容关联字段映射；不执行关系裁决。
type Relations interface {
	GetMergeRelation(context.Context, string, string) (domain.MergeRelation, error)
}

// Client 拥有兼容存储连接；并发上限为四，单次操作预算由调用上下文和十秒 HTTP 超时共同限制。
type Client struct {
	transport  Transport
	close      func()
	config     config.KACPluginConfig
	stateIndex string
	level      func(string) (string, error)
	relations  Relations
	slots      chan struct{}
	ready      atomic.Bool
}

// Open 创建独立连接池；Maintain 成功前写入返回依赖未就绪，调用方可独立重试索引维护。Close 应在全部调用退出后执行。
func Open(c config.KACPluginConfig, level func(string) (string, error), relations Relations) (*Client, error) {
	if err := (config.PluginsConfig{KAC: &c}).Validate(); err != nil {
		return nil, err
	}
	if !c.Enabled || level == nil {
		return nil, projection.ErrInvalid
	}
	c = c.WithDefaults()
	es := c.Elasticsearch
	options := elasticsearchstore.HTTPTransportConfig{Addresses: es.Addresses, APIKey: es.APIKey, Timeout: 10 * time.Second, MaxConnectionsPerHost: 4, DisableRedirects: true}
	if es.BasicAuth != nil {
		options.BasicUsername = es.BasicAuth.Username
		options.BasicPassword = es.BasicAuth.Password
	}
	tr, err := elasticsearchstore.NewHTTPTransport(options)
	if err != nil {
		return nil, err
	}
	client := newClient(c, tr, tr.Close, level)
	client.relations = relations
	return client, nil
}

func newClient(c config.KACPluginConfig, tr Transport, closeFn func(), level func(string) (string, error)) *Client {
	// 独立点前缀不能匹配合法 KAC alias 的 {alias}* 模板，避免同步元数据被 KAC ILM 轮转。
	sum := sha256.Sum256([]byte(c.AlarmEventIndex))
	return &Client{transport: tr, close: closeFn, config: c.WithDefaults(), stateIndex: ".linkd-kac-state-" + hex.EncodeToString(sum[:16]), level: level, slots: make(chan struct{}, 4)}
}

// Close 释放专属空闲连接；调用方先取消并等待所有任务退出。
func (c *Client) Close() {
	if c.close != nil {
		c.close()
	}
}

type httpFailure struct{ status int }

func (f httpFailure) Error() string { return "KAC compatibility storage request failed" }

func hasStatus(err error, status int) bool {
	var f httpFailure
	return errors.As(err, &f) && f.status == status
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil || len(data) > 2<<20 {
			return projection.ErrInvalid
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(data))
	if err != nil {
		return projection.ErrInvalid
	}
	req.URL.RawQuery = query.Encode()
	req.Header.Set("Content-Type", "application/json")
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	res, err := c.transport.Perform(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return projection.Failure{Code: "transport_failed", Retryable: true}
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return httpFailure{res.StatusCode}
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20+1))
	if err != nil {
		return projection.Failure{Code: "transport_failed", Retryable: true}
	}
	if len(raw) > 8<<20 {
		return projection.Failure{Code: "response_too_large"}
	}
	if out != nil {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(out) != nil {
			return projection.Failure{Code: "response_invalid"}
		}
		var extra any
		if !errors.Is(decoder.Decode(&extra), io.EOF) {
			return projection.Failure{Code: "response_invalid"}
		}
	}
	return nil
}

func classify(err error) error {
	if err == nil {
		return nil
	}
	var h httpFailure
	if !errors.As(err, &h) {
		return err
	}
	switch {
	case h.status == 409:
		return projection.Failure{Code: "revision_conflict", Retryable: true}
	case h.status == 401 || h.status == 403:
		return projection.Failure{Code: "remote_unauthorized"}
	case h.status == 404 || h.status == 429 || h.status >= 500:
		return projection.Failure{Code: "remote_unavailable", Retryable: true}
	default:
		return projection.Failure{Code: "remote_rejected"}
	}
}

func (c *Client) physicalIndex(index string) bool {
	return validPhysicalName(index) && (strings.HasPrefix(index, c.config.AlarmEventIndex+"-") || strings.HasPrefix(index, c.config.AlarmEventIndex+"_v"))
}
