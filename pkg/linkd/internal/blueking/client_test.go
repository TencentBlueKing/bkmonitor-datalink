// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package blueking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/config"
)

func newClient(t *testing.T, endpoint string, multi bool) *Client {
	t.Helper()
	c, err := New(config.BluekingConfig{APIURL: endpoint, AppCode: "app", AppSecret: "test-value", EnableMultiTenantMode: multi})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func userReply(t *testing.T, w http.ResponseWriter, tenant string) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": []any{map[string]string{"bk_username": "admin-" + tenant, "bk_tenant_id": tenant, "login_name": "bk_admin"}}}); err != nil {
		t.Error(err)
	}
}

func checkAuth(t *testing.T, r *http.Request, username string) {
	t.Helper()
	if len(r.Header.Values("X-Bkapi-Authorization")) != 1 || len(r.Header.Values("X-Bk-Tenant-Id")) != 1 {
		t.Error("duplicate untrusted identity header forwarded")
	}
	var auth map[string]string
	if json.Unmarshal([]byte(r.Header.Get("X-Bkapi-Authorization")), &auth) != nil || auth["bk_app_code"] != "app" || auth["bk_app_secret"] != "test-value" || auth["bk_username"] != username {
		t.Error("incorrect application/user authorization")
	}
}

func TestSingleAndMultiTenantAPIGWIdentity(t *testing.T) {
	for _, multi := range []bool{false, true} {
		t.Run(fmt.Sprint(multi), func(t *testing.T) {
			var lookups atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tenant := r.Header.Get("X-Bk-Tenant-Id")
				if tenant != "one" && tenant != "two" {
					t.Error("request tenant changed")
				}
				if r.URL.Path == "/api/bk-user/prod/api/v3/open/tenant/virtual-users/-/lookup/" {
					lookups.Add(1)
					checkAuth(t, r, "admin")
					if r.Method != "GET" || r.URL.Query().Get("lookup_field") != "login_name" || r.URL.Query().Get("lookups") != "bk_admin" {
						t.Error("wrong lookup contract")
					}
					userReply(t, w, tenant)
					return
				}
				username := "admin"
				if multi {
					username = "admin-" + tenant
				}
				checkAuth(t, r, username)
				_, _ = w.Write([]byte(`{"code":0,"result":true,"data":[]}`))
			}))
			t.Cleanup(server.Close)
			c := newClient(t, server.URL, multi)
			for range 3 {
				for _, tenant := range []string{"one", "two"} {
					r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/cmdb", nil)
					if err != nil {
						t.Fatal(err)
					}
					r.Header.Set("X-Bk-Tenant-Id", "wrong")
					r.Header["x-bk-tenant-id"] = []string{"other"}
					r.Header["x-bkapi-authorization"] = []string{"caller-supplied"}
					if _, err := c.Do(t.Context(), tenant, r); err != nil {
						t.Fatal(err)
					}
				}
			}
			want := int32(0)
			if multi {
				want = 2
			}
			if lookups.Load() != want {
				t.Fatal("lookup caching/mode mismatch", lookups.Load())
			}
			if _, err := c.Username(t.Context(), ""); err == nil {
				t.Fatal("empty tenant accepted")
			}
		})
	}
}

func TestLookupFailureIsNeverCachedOrDefaulted(t *testing.T) {
	for _, body := range []string{`{"code":0,"data":[]}`, `{"code":0,"data":[{}]}`, `{"code":0,"data":[{"bk_username":"a"},{"bk_username":"b"}]}`, `{"code":1,"data":[{"bk_username":"a"}]}`, `{"result":false,"code":0,"data":[{"bk_username":"a"}]}`, `{"data":[{"bk_username":"a"}]}`, `{"code":0,"data":[{"bk_username":"a","bk_tenant_id":"other"}]}`, `{"code":0,"data":[{"bk_username":"a","login_name":"other"}]}`, `{"code":0,"data":[{"bk_username":" a "}]}`, `{"code":0,"data":null}`, `bad body test-value`, `{"code":0,"data":[]} {}`} {
		t.Run(body, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					_, _ = w.Write([]byte(body))
				} else {
					userReply(t, w, "one")
				}
			}))
			t.Cleanup(server.Close)
			c := newClient(t, server.URL, true)
			if got, err := c.Username(t.Context(), "one"); err == nil || got != "" || strings.Contains(err.Error(), "test-value") {
				t.Fatal("invalid lookup accepted or leaked")
			}
			if got, err := c.Username(t.Context(), "one"); err != nil || got != "admin-one" || calls.Load() != 2 {
				t.Fatal("failed lookup cached or not retried", err)
			}
		})
	}
}

func TestUserCacheEvictsLeastRecentlyUsedTenant(t *testing.T) {
	var mu sync.Mutex
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenant := r.Header.Get("X-Bk-Tenant-Id")
		mu.Lock()
		calls[tenant]++
		mu.Unlock()
		userReply(t, w, tenant)
	}))
	t.Cleanup(server.Close)
	c := newClient(t, server.URL, true)
	for i := range 1000 {
		if _, err := c.Username(t.Context(), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tenant := range []string{"0", "1000", "0", "1"} {
		if _, err := c.Username(t.Context(), tenant); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls["0"] != 1 || calls["1"] != 2 || calls["1000"] != 1 {
		t.Fatal("LRU eviction did not preserve recent tenant")
	}
}

func waitForWaiters(t *testing.T, c *Client, tenant string, count int) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		c.mu.Lock()
		f := c.flights[tenant]
		ready := f != nil && f.waiters == count
		c.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("waiters did not join shared lookup")
		}
	}
}

func TestSharedLookupCancellationAndClose(t *testing.T) {
	entered, release := make(chan struct{}, 4), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			userReply(t, w, r.Header.Get("X-Bk-Tenant-Id"))
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	c := newClient(t, server.URL, true)
	ctx, cancel := context.WithCancel(t.Context())
	first := make(chan error, 1)
	go func() { _, err := c.Username(ctx, "one"); first <- err }()
	<-entered
	second := make(chan error, 1)
	go func() { _, err := c.Username(t.Context(), "one"); second <- err }()
	waitForWaiters(t, c, "one", 2)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatal("another waiter canceled shared result", err)
	}
	if calls.Load() != 1 {
		t.Fatal("same tenant duplicated lookup")
	}
	c.Close()
	c.Close()
	if _, err := c.Username(t.Context(), "one"); !errors.Is(err, ErrClosed) {
		t.Fatal("closed cache used", err)
	}
}

func TestCloseCancelsInflightAndBoundsDifferentTenants(t *testing.T) {
	entered := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-r.Context().Done() }))
	t.Cleanup(server.Close)
	c := newClient(t, server.URL, true)
	results := make(chan error, 4)
	for i := range 4 {
		go func() { _, err := c.Username(t.Context(), fmt.Sprint(i)); results <- err }()
	}
	for range 4 {
		<-entered
	}
	if _, err := c.Username(t.Context(), "excess"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("lookup bound bypassed", err)
	}
	c.Close()
	for range 4 {
		if err := <-results; !errors.Is(err, ErrClosed) {
			t.Fatal("lookup did not terminate on close", err)
		}
	}
}

func TestCanceledLookupReleasesWorkAndRetries(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-r.Context().Done()
			close(canceled)
			return
		}
		userReply(t, w, "one")
	}))
	t.Cleanup(server.Close)
	c := newClient(t, server.URL, true)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { _, err := c.Username(ctx, "one"); result <- err }()
	<-entered
	c.mu.Lock()
	flight := c.flights["one"]
	c.mu.Unlock()
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	<-canceled
	<-flight.done
	if got, err := c.Username(t.Context(), "one"); err != nil || got != "admin-one" || calls.Load() != 2 {
		t.Fatal("canceled flight retained/cache poisoned", err)
	}
}

func TestUserLookupTimeoutAndResponseBudget(t *testing.T) {
	t.Run("shared lookup deadline", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
		t.Cleanup(server.Close)
		c := newClient(t, server.URL, true)
		if _, err := c.Username(t.Context(), "one"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("lookup deadline missing", err)
		}
	})
	t.Run("response limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+1))) }))
		t.Cleanup(server.Close)
		c := newClient(t, server.URL, true)
		if _, err := c.Username(t.Context(), "one"); !errors.Is(err, ErrUnavailable) {
			t.Fatal("lookup payload bound missing", err)
		}
	})
}

func TestRequestErrorsAndRedirectDoNotExposeCredentials(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	t.Cleanup(other.Close)
	for _, status := range []int{301, 401, 403, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", other.URL)
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private test-value"))
			}))
			t.Cleanup(server.Close)
			c := newClient(t, server.URL, true)
			if _, err := c.Username(t.Context(), "one"); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "test-value") || strings.Contains(err.Error(), server.URL) {
				t.Fatal("unsafe error", err)
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("credentials followed redirect")
	}
}
