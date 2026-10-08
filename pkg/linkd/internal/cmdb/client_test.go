// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package cmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"linkd/internal/blueking"
	"linkd/internal/config"
)

func newTestClient(t *testing.T, endpoint string) (*Client, error) {
	t.Helper()
	gateway, err := blueking.New(config.BluekingConfig{APIURL: endpoint, AppCode: "linkd", AppSecret: "synthetic-secret"})
	if err != nil {
		return nil, err
	}
	t.Cleanup(gateway.Close)
	return New(endpoint, gateway)
}

func respond(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{"result": true, "code": 0, "data": data}); err != nil {
		t.Error(err)
	}
}

func TestServiceInstancesReadCompletePagesAndTenantIdentity(t *testing.T) {
	for _, mode := range []string{"apigw"} {
		t.Run(mode, func(t *testing.T) {
			calls := atomic.Int32{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var auth map[string]string
				if json.Unmarshal([]byte(r.Header.Get("X-Bkapi-Authorization")), &auth) != nil || auth["bk_app_code"] != "linkd" || auth["bk_app_secret"] != "synthetic-secret" || auth["bk_username"] != "admin" || r.Header.Get("X-Bk-Tenant-Id") != "tenant" {
					t.Error("wrong tenant calling identity")
					w.WriteHeader(401)
					return
				}
				var params map[string]any
				d := json.NewDecoder(r.Body)
				d.UseNumber()
				if d.Decode(&params) != nil || params["bk_tenant_id"] != "tenant" || params["bk_biz_id"] != json.Number("2") {
					t.Error("wrong body scope")
					w.WriteHeader(400)
					return
				}
				servicePath := "/api/v3/findmany/proc/service_instance/details"
				hostPath := "/api/v3/hosts/app/2/list_hosts"
				switch r.URL.Path {
				case servicePath:
					start := params["page"].(map[string]any)["start"].(json.Number)
					id := 1
					if start == "1" {
						id = 2
					} else if start != "0" {
						t.Error("wrong pagination offset")
					}
					respond(t, w, map[string]any{"count": 2, "info": []any{map[string]any{"id": id, "bk_biz_id": 2, "bk_module_id": 8, "bk_host_id": 10, "name": fmt.Sprintf("svc-%d", id), "service_template_id": 0}}})
				case hostPath:
					if params["host_property_filter"] == nil {
						t.Error("host read lacked selected IDs")
					}
					respond(t, w, map[string]any{"count": 1, "info": []any{map[string]any{"bk_host_id": 10, "bk_cloud_id": 0, "bk_host_innerip": "10.0.0.1"}}})
				default:
					t.Error("unknown CMDB path", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			c, err := newTestClient(t, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := c.ServiceInstances(t.Context(), "tenant", 2, "cw-CCServiceInstance", nil)
			if err != nil || len(rows) != 2 || calls.Load() != 3 {
				t.Fatalf("rows=%d calls=%d err=%v", len(rows), calls.Load(), err)
			}
			for _, row := range rows {
				if row.TenantID != "tenant" || row.ModelCode != "cw-CCServiceInstance" || row.Attributes["bk_host_id"] != int64(10) {
					t.Fatal("service projection lost identity/host")
				}
			}

		})
	}
}

func TestServiceInstancesRejectIncompleteFacts(t *testing.T) {
	for _, kind := range []string{"missing-count", "changed-count", "empty-page", "duplicate", "cross-tenant", "cross-business", "missing-host", "missing-member", "invalid-id"} {
		t.Run(kind, func(t *testing.T) {
			calls := atomic.Int32{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if r.URL.Path == "/api/v3/hosts/app/2/list_hosts" {
					respond(t, w, map[string]any{"count": 0, "info": []any{}})
					return
				}
				row := map[string]any{"id": 1, "bk_biz_id": 2, "bk_host_id": 10, "bk_module_id": 8, "name": "service"}
				data := map[string]any{"count": 1, "info": []any{row}}
				switch kind {
				case "missing-count":
					delete(data, "count")
				case "changed-count":
					data["count"] = 2
					if n > 1 {
						data["count"] = 3
						row["id"] = 2
					}
				case "empty-page":
					data["count"] = 2
					if n > 1 {
						data["info"] = []any{}
					}
				case "duplicate":
					data["count"] = 2
				case "cross-tenant":
					row["bk_tenant_id"] = "other"
				case "cross-business":
					row["bk_biz_id"] = 3
				case "missing-member":
					data["count"] = 0
					data["info"] = []any{}
				case "invalid-id":
					row["id"] = "1"
				}
				respond(t, w, data)
			}))
			defer server.Close()
			c, err := newTestClient(t, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := c.ServiceInstances(t.Context(), "tenant", 2, "cw-CCServiceInstance", []string{"1"})
			if !errors.Is(err, ErrIncomplete) || rows != nil {
				t.Fatal("partial facts accepted", kind, err)
			}
		})
	}
}

func TestCMDBRequestBoundCancellationAndNoCredentialRedirect(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	c, err := newTestClient(t, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := c.request(t.Context(), "tenant", 2, "search_biz_inst_topo", nil, &out); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), server.URL) || redirected.Load() != 0 {
		t.Fatal("redirect or unsafe error", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := c.request(ctx, "tenant", 2, "search_biz_inst_topo", nil, &out); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := c.request(t.Context(), "tenant", 2, "delete_business", nil, &out); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unknown action accepted")
	}
}
