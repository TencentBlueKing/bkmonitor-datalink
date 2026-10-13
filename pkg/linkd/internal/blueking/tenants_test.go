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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestListTenantsProtocol(t *testing.T) {
	for _, multi := range []bool{false, true} {
		t.Run(fmt.Sprint(multi), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				checkAuth(t, r, "admin")
				if r.Method != http.MethodGet || r.URL.Path != "/root/api/bk-user/prod/api/v3/open/tenants/" || r.URL.RawQuery != "" || r.Header.Get("X-Bk-Tenant-Id") != "system" {
					t.Error("incorrect tenant directory request")
				}
				_, _ = w.Write([]byte(`{"code":0,"data":[{"id":"system","name":"蓝鲸运营","status":"enabled","extra":"ignored"},{"id":"test","name":"测试租户","status":"disabled"}]}`))
			}))
			defer server.Close()
			client := newClient(t, server.URL+"/root/", multi)
			want := []Tenant{{"system", "蓝鲸运营", "enabled"}, {"test", "测试租户", "disabled"}}
			wantCalls := 2
			if !multi {
				want = []Tenant{{"system", "Blueking", "enabled"}}
				wantCalls = 0
			}
			for range 2 {
				got, err := client.ListTenants(t.Context())
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("tenants=%v error=%v", got, err)
				}
				// 调用方修改返回值不能影响下一次读取的默认目录。
				got[0].Name = "changed"
			}
			if calls != wantCalls {
				t.Fatalf("directory was cached or performed identity lookup: calls=%d", calls)
			}
		})
	}
}

func TestSingleTenantDirectoryContext(t *testing.T) {
	directory := SingleTenantDirectory{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"missing context", nil, ErrUnavailable},
		{"canceled context", ctx, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := directory.ListTenants(tc.ctx); got != nil || !errors.Is(err, tc.want) {
				t.Fatalf("tenants=%v error=%v", got, err)
			}
		})
	}
	client := newClient(t, "http://unused.invalid", false)
	if got, err := client.ListTenants(ctx); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled client query: tenants=%v error=%v", got, err)
	}
	client.Close()
	if got, err := client.ListTenants(t.Context()); got != nil || !errors.Is(err, ErrClosed) {
		t.Fatalf("closed client query: tenants=%v error=%v", got, err)
	}
}

func TestListTenantsResponses(t *testing.T) {
	valid := `{"id":"system","name":"Blueking","status":"enabled"}`
	rows := make([]Tenant, maxTenants)
	for i := range rows {
		rows[i] = Tenant{fmt.Sprintf("tenant-%d", i), "tenant", "enabled"}
	}
	boundary, err := json.Marshal(map[string]any{"code": 0, "data": rows})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		body   string
		status int
		count  int
		ok     bool
	}{
		{"empty", `{"code":0,"data":[]}`, 200, 0, true},
		{"explicit result", `{"code":0,"result":true,"data":[` + valid + `]}`, 200, 1, true},
		{"maximum count", string(boundary), 200, maxTenants, true},
		{"too many", `{"code":0,"data":[` + strings.Repeat(valid+",", maxTenants) + valid + `]}`, 200, 0, false},
		{"duplicate", `{"code":0,"data":[` + valid + `,` + valid + `]}`, 200, 0, false},
		{"missing data", `{"code":0}`, 200, 0, false},
		{"null data", `{"code":0,"data":null}`, 200, 0, false},
		{"missing code", `{"data":[]}`, 200, 0, false},
		{"business error", `{"code":1,"data":[],"message":"test-value"}`, 200, 0, false},
		{"false result", `{"code":0,"result":false,"data":[]}`, 200, 0, false},
		{"invalid id", `{"code":0,"data":[{"id":"bad/id","name":"x","status":"enabled"}]}`, 200, 0, false},
		{"missing name", `{"code":0,"data":[{"id":"system","status":"enabled"}]}`, 200, 0, false},
		{"missing status", `{"code":0,"data":[{"id":"system","name":"x"}]}`, 200, 0, false},
		{"invalid name", `{"code":0,"data":[{"id":"system","name":" x ","status":"enabled"}]}`, 200, 0, false},
		{"oversized name", `{"code":0,"data":[{"id":"system","name":"` + strings.Repeat("x", 257) + `","status":"enabled"}]}`, 200, 0, false},
		{"malformed", `test-value`, 200, 0, false},
		{"trailing JSON", `{"code":0,"data":[]} {}`, 200, 0, false},
		{"HTTP failure", `test-value`, 403, 0, false},
		{"redirect", `test-value`, 302, 0, false},
		{"response limit", strings.Repeat("x", (1<<20)+1), 200, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := newClient(t, server.URL, true)
			got, err := client.ListTenants(t.Context())
			if tc.ok {
				if err != nil || got == nil || len(got) != tc.count {
					t.Fatalf("count=%d error=%v", len(got), err)
				}
			} else if !errors.Is(err, ErrUnavailable) || got != nil || strings.Contains(err.Error(), "test-value") {
				t.Fatalf("invalid response was accepted or leaked: count=%d error=%v", len(got), err)
			}
		})
	}
}

func TestListTenantsCancellationAndClose(t *testing.T) {
	started := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	client := newClient(t, server.URL, true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	results := make(chan error, 4)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			_, err := client.ListTenants(ctx)
			results <- err
		})
	}
	for range 4 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("request did not start")
		}
	}
	// 目录调用本身有准入上限，不能在 HTTP 配额外积累无界排队者。
	if _, err := client.ListTenants(t.Context()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("directory admission exceeded: %v", err)
	}
	// 即使配额已满，也应优先保留调用方的取消原因。
	queued, stop := context.WithCancel(t.Context())
	stop()
	if _, err := client.ListTenants(queued); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation: %v", err)
	}
	client.Close()
	workers.Wait()
	for range 4 {
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatalf("active request not canceled by close: %v", err)
		}
	}
	if _, err := client.ListTenants(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed client accepted request: %v", err)
	}
}
