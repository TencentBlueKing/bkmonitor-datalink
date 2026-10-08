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
)

type userEntry struct{ tenant, username string }

type userFlight struct {
	done     chan struct{}
	cancel   context.CancelFunc
	waiters  int
	finished bool
	username string
	err      error
}

// Username 在单租户模式返回 admin；多租户只使用当前租户唯一的 bk_admin 查询结果。
// 成功缓存采用 1000 项 LRU，无 TTL；失败不缓存。最多四个不同租户同时查询，超额直接返回依赖错误。
func (c *Client) Username(ctx context.Context, tenant string) (string, error) {
	if err := validTenant(ctx, tenant); err != nil {
		return "", err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", ErrClosed
	}
	if !c.cfg.EnableMultiTenantMode {
		c.mu.Unlock()
		return "admin", nil
	}
	if entry, ok := c.cache[tenant]; ok {
		c.lru.MoveToFront(entry)
		username := entry.Value.(userEntry).username
		c.mu.Unlock()
		return username, nil
	}
	flight, exists := c.flights[tenant]
	if !exists {
		if len(c.flights) >= 4 {
			c.mu.Unlock()
			return "", ErrUnavailable
		}
		// 共享读取不继承某个等待者的取消；全部等待者取消时立即取消该工作，避免后台悬挂。
		call, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
		flight = &userFlight{done: make(chan struct{}), cancel: cancel}
		c.flights[tenant] = flight
		c.wg.Add(1)
		go c.loadUser(call, tenant, flight)
	}
	flight.waiters++
	c.mu.Unlock()
	select {
	case <-flight.done:
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return flight.username, flight.err
	case <-ctx.Done():
		c.mu.Lock()
		flight.waiters--
		if flight.waiters == 0 && !flight.finished {
			flight.cancel()
			// 保留未退出的工作占用配额；后续请求等待这一有界失败后再重新查询。
		}
		c.mu.Unlock()
		return "", ctx.Err()
	}
}

func (c *Client) loadUser(ctx context.Context, tenant string, flight *userFlight) {
	defer c.wg.Done()
	defer flight.cancel()
	username, err := c.lookupUser(ctx, tenant)
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if c.closed {
		err = ErrClosed
	}
	if err == nil {
		entry := c.lru.PushFront(userEntry{tenant: tenant, username: username})
		c.cache[tenant] = entry
		if c.lru.Len() > 1000 {
			last := c.lru.Back()
			delete(c.cache, last.Value.(userEntry).tenant)
			c.lru.Remove(last)
		}
	}
	delete(c.flights, tenant)
	flight.username, flight.err, flight.finished = username, err, true
	close(flight.done)
}

func (c *Client) lookupUser(ctx context.Context, tenant string) (string, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, c.userURL(), nil)
	if err != nil {
		return "", ErrUnavailable
	}
	// 查询特殊用户使用固定 bootstrap 身份，不能递归调用 Username。
	raw, err := c.perform(ctx, tenant, "admin", r, 1<<20)
	if err != nil {
		return "", err
	}
	var envelope struct {
		Code   *int64 `json:"code"`
		Result *bool  `json:"result"`
		Data   []struct {
			Username string `json:"bk_username"`
			Tenant   string `json:"bk_tenant_id"`
			Login    string `json:"login_name"`
		} `json:"data"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if d.Decode(&envelope) != nil {
		return "", ErrUnavailable
	}
	var trailing any
	if !errors.Is(d.Decode(&trailing), io.EOF) || envelope.Code == nil || *envelope.Code != 0 || envelope.Result != nil && !*envelope.Result || len(envelope.Data) != 1 {
		return "", ErrUnavailable
	}
	row := envelope.Data[0]
	if row.Tenant != "" && row.Tenant != tenant || row.Login != "" && row.Login != "bk_admin" || row.Username == "" || len(row.Username) > 256 || !utf8.ValidString(row.Username) || strings.TrimSpace(row.Username) != row.Username || strings.ContainsFunc(row.Username, unicode.IsControl) {
		return "", ErrUnavailable
	}
	return row.Username, nil
}
