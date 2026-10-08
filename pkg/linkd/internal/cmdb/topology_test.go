// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package cmdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTopologyIncludesInternalModulesAndCompleteHosts(t *testing.T) {
	for _, mode := range []string{"apigw"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v3/find/topoinst/biz/2":
					respond(t, w, []any{map[string]any{"bk_obj_id": "biz", "bk_inst_id": 2, "child": []any{map[string]any{"bk_obj_id": "set", "bk_inst_id": 4, "child": []any{map[string]any{"bk_obj_id": "module", "bk_inst_id": 8}}}}}})
				case "/api/v3/topo/internal/0/2":
					if r.Method != http.MethodGet || r.URL.Query().Get("bk_tenant_id") != "tenant" || r.URL.Query().Get("bk_biz_id") != "2" {
						t.Error("wrong internal module scope/method")
					}
					respond(t, w, map[string]any{"bk_set_id": 5, "module": []any{map[string]any{"bk_module_id": 9}}})
				case "/api/v3/findmany/hosts/by_topo/biz/2":
					var body map[string]any
					d := json.NewDecoder(r.Body)
					d.UseNumber()
					if d.Decode(&body) != nil || body["bk_obj_id"] != "module" || body["bk_inst_id"] != json.Number("9") {
						t.Error("wrong topology node")
					}
					respond(t, w, map[string]any{"count": 1, "info": []any{map[string]any{"bk_host_id": 10, "bk_host_innerip": "10.0.0.1", "bk_cloud_id": 0, "bk_host_name": "host"}}})
				default:
					t.Error("unknown path", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			c, err := newTestClient(t, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			nodes, err := c.Topology(t.Context(), "tenant", 2)
			if err != nil || len(nodes) != 5 {
				t.Fatal("incomplete topology", nodes, err)
			}
			last := nodes[len(nodes)-1]
			if last.ObjectID != "module" || last.InstanceID != 9 || last.ParentObjectID != "set" || last.ParentInstanceID != 5 {
				t.Fatal("lost internal module ancestry")
			}
			hosts, err := c.HostsByTopology(t.Context(), "tenant", 2, "cw-Host", last)
			if err != nil || len(hosts) != 1 || hosts[0].ModelCode != "cw-Host" || hosts[0].InstanceID != "10" || hosts[0].TenantID != "tenant" {
				t.Fatal("wrong direct host identity", err)
			}
		})
	}
}

func TestTopologyRejectsInvalidScopeAndDuplicateNodes(t *testing.T) {
	for _, kind := range []string{"wrong-root", "duplicate", "cross-business", "cross-tenant", "bad-internal-module", "internal-no-set"} {
		t.Run(kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					switch kind {
					case "bad-internal-module":
						respond(t, w, map[string]any{"bk_set_id": 5, "module": []any{map[string]any{"bk_module_id": 0}}})
					case "internal-no-set":
						respond(t, w, map[string]any{"module": []any{map[string]any{"bk_module_id": 9}}})
					default:
						respond(t, w, map[string]any{})
					}
					return
				}
				root := map[string]any{"bk_obj_id": "biz", "bk_inst_id": 2}
				child := map[string]any{"bk_obj_id": "module", "bk_inst_id": 8}
				switch kind {
				case "wrong-root":
					root["bk_inst_id"] = 3
				case "duplicate":
					root["child"] = []any{child, child}
				case "cross-business":
					root["bk_biz_id"] = 3
				case "cross-tenant":
					root["bk_tenant_id"] = "other"
				}
				respond(t, w, []any{root})
			}))
			defer server.Close()
			c, err := newTestClient(t, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if nodes, err := c.Topology(t.Context(), "tenant", 2); !errors.Is(err, ErrIncomplete) || nodes != nil {
				t.Fatal("invalid tree accepted", kind, err)
			}
		})
	}
}
