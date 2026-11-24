// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

// Package kaccompat 在控制面批量写入 KAC 兼容文档并发送轻量动作。
package kaccompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/processingerror"
	elasticsearchstore "linkd/internal/store/elasticsearch"
)

// Transport 传播调用上下文，连接由 Client 关闭。
type Transport interface {
	Perform(*http.Request) (*http.Response, error)
}

// Relations 只读取合并关系，不重新裁决。
type Relations interface {
	GetMergeRelation(context.Context, string, string) (domain.MergeRelation, error)
}

// Client 复用连接与物理索引缓存，不保存同步版本或回执。
type Client struct {
	transport Transport
	close     func()
	config    config.KACPluginConfig
	level     func(string) (string, error)
	relations Relations
	mu        sync.Mutex
	locations map[alarmKey]string
	// mappingReady 只在首次完整维护成功后放行写入，避免并发关闭触发动态 text 映射。
	// 后续周期维护的传输失败不撤销已准备好的 mapping。
	mappingReady atomic.Bool
}

// Open 装配连接；索引维护和写入使用同一 Client。
func Open(c config.KACPluginConfig, level func(string) (string, error), relations Relations) (*Client, error) {
	c = c.WithDefaults()
	es := c.Elasticsearch
	options := elasticsearchstore.HTTPTransportConfig{Addresses: es.Addresses, APIKey: es.APIKey, Timeout: 10 * time.Second, DisableRedirects: true}
	if es.BasicAuth != nil {
		options.BasicUsername, options.BasicPassword = es.BasicAuth.Username, es.BasicAuth.Password
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
	return &Client{transport: tr, close: closeFn, config: c.WithDefaults(), level: level, locations: make(map[alarmKey]string)}
}

func (c *Client) readyToWrite(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.mappingReady.Load() {
		// 由既有维护任务负责准备与重试；同步请求快速失败，不能逐条维护索引。
		return processingerror.RetryableError{Err: errors.New("KAC index mapping not ready; retry after successful maintenance")}
	}
	return nil
}

// Close 在运行器与同步请求退出后释放连接。
func (c *Client) Close() {
	if c.close != nil {
		c.close()
	}
}

type httpFailure struct {
	status    int
	errorType string
}

func (f httpFailure) Error() string { return fmt.Sprintf("KAC Elasticsearch HTTP %d", f.status) }

func hasStatus(err error, status int) bool {
	var failure httpFailure
	return errors.As(err, &failure) && failure.status == status
}

func (c *Client) request(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	return c.requestRaw(ctx, method, path, query, raw, "application/json", out)
}

func (c *Client) requestRaw(ctx context.Context, method, path string, query url.Values, raw []byte, contentType string, out any) error {
	request, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	request.URL.RawQuery = query.Encode()
	request.Header.Set("Content-Type", contentType)
	response, err := c.transport.Perform(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("KAC Elasticsearch transport failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var detail struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		// 只提取错误类型，不记录 ES reason 中可能包含的业务正文。
		_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&detail)
		return httpFailure{status: response.StatusCode, errorType: detail.Error.Type}
	}
	if out == nil {
		_, err = io.Copy(io.Discard, response.Body)
		return err
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 32<<20))
	decoder.UseNumber()
	return decoder.Decode(out)
}
