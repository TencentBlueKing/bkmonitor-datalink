// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

// Package blueking 负责 APIGW 应用鉴权、租户用户解析和有界 HTTP 调用，不更改业务租户身份。
package blueking

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
)

// ErrUnavailable 表示调用或用户名解析失败，错误不包含凭据、请求 URL 或响应体。
var ErrUnavailable = errors.New("BlueKing APIGW unavailable")

// ErrClosed 表示运行时已关闭，调用方不得继续使用该客户端。
var ErrClosed = errors.New("BlueKing APIGW client closed")

const requestTimeout = 3 * time.Second

// Client 在一个控制面或 Lifecycle 运行时内共享连接、租户缓存和查询合并。
// 资源由构造方持有并关闭；不能创建来源级副本或使用进程全局可变实例。
type Client struct {
	cfg       config.BluekingConfig
	http      *http.Client
	transport *http.Transport
	slots     chan struct{}
	mu        sync.Mutex
	closed    bool
	closedCh  chan struct{}
	cache     map[string]*list.Element
	lru       list.List
	flights   map[string]*userFlight
	active    map[*http.Request]context.CancelFunc
	wg        sync.WaitGroup
}

// New 校验全局应用凭据并建立连接句柄，不在启动时读取任何租户用户。
func New(cfg config.BluekingConfig) (*Client, error) {
	if err := cfg.Validate(true); err != nil {
		return nil, err
	}
	t := &http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: requestTimeout, KeepAlive: 30 * time.Second}).DialContext, MaxConnsPerHost: 4, MaxIdleConns: 8, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: requestTimeout, ResponseHeaderTimeout: requestTimeout}
	c := &Client{cfg: cfg, transport: t, slots: make(chan struct{}, 4), closedCh: make(chan struct{}), cache: map[string]*list.Element{}, flights: map[string]*userFlight{}, active: map[*http.Request]context.CancelFunc{}}
	c.http = &http.Client{Transport: t, Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return c, nil
}

// Do 先解析当前租户用户名，再申请 HTTP 配额；请求地址须由消费方的静态部署配置确定。
// 返回最多 8 MiB 原始响应，由消费者解释业务信封。不会透传调用者自带的蓝鲸认证或租户头。
func (c *Client) Do(ctx context.Context, tenant string, request *http.Request) ([]byte, error) {
	if request == nil {
		return nil, ErrUnavailable
	}
	username, err := c.Username(ctx, tenant)
	if err != nil {
		return nil, err
	}
	return c.perform(ctx, tenant, username, request, 8<<20)
}

func (c *Client) perform(ctx context.Context, tenant, username string, request *http.Request, limit int64) ([]byte, error) {
	call, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-c.closedCh:
		return nil, ErrClosed
	case <-call.Done():
		return nil, call.Err()
	}
	if err := call.Err(); err != nil {
		return nil, err
	}
	r := request.Clone(call)
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	// Header 可由调用者直接构造为非 canonical 键，Set 本身不会清除这些重复字段。
	for key := range r.Header {
		if strings.EqualFold(key, "X-Bkapi-Authorization") || strings.EqualFold(key, "X-Bk-Tenant-Id") {
			delete(r.Header, key)
		}
	}
	auth, _ := json.Marshal(map[string]string{"bk_app_code": c.cfg.AppCode, "bk_app_secret": c.cfg.AppSecret, "bk_username": username})
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Bk-Tenant-Id", tenant)
	r.Header.Set("X-Bkapi-Authorization", string(auth))
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	c.active[r] = cancel
	c.wg.Add(1)
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.active, r); c.mu.Unlock(); c.wg.Done() }()
	response, err := c.http.Do(r)
	if err != nil {
		if call.Err() != nil {
			return nil, call.Err()
		}
		return nil, fmt.Errorf("%w: transport", ErrUnavailable)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrUnavailable, response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		if call.Err() != nil {
			return nil, call.Err()
		}
		return nil, fmt.Errorf("%w: response read", ErrUnavailable)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w: response budget", ErrUnavailable)
	}
	return raw, call.Err()
}

// Close 阻止新调用，取消共享查询及正在发送的请求，等待退出并清除缓存；可重复调用。
// Add 与 closed 检查位于同一把锁，避免 Wait 开始后又注册新工作。
func (c *Client) Close() {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.closedCh)
		clear(c.cache)
		c.lru.Init()
		for _, flight := range c.flights {
			flight.cancel()
		}
		for _, cancel := range c.active {
			cancel()
		}
	}
	c.mu.Unlock()
	c.wg.Wait()
	c.transport.CloseIdleConnections()
}

func validTenant(ctx context.Context, tenant string) error {
	if ctx == nil || domain.ValidateIdentityPart("tenant", tenant, 64) != nil {
		return ErrUnavailable
	}
	return ctx.Err()
}

func (c *Client) userURL() string {
	return strings.TrimRight(c.cfg.APIURL, "/") + "/api/bk-user/prod/api/v3/open/tenant/virtual-users/-/lookup/?lookup_field=login_name&lookups=bk_admin"
}
