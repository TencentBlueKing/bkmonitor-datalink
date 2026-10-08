// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package allinone_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/policy"
)

// TestAllInOneCMDBTargetsE2E 使用真实 Worker、目录数据库和控制任务，CMDB 网关由协议服务器模拟。
// 不往 OneModel 注入服务实例或主机成员，证明显式服务与主机/服务拓扑来自部署级 CMDB 读取。
func TestAllInOneCMDBTargetsE2E(t *testing.T) {
	if os.Getenv(e2eEnabledEnv) != "1" {
		t.Skipf("set %s=1", e2eEnabledEnv)
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "linkd")
	buildLinkd(t, t.Context(), root, binary)
	for _, backend := range []string{"elasticsearch", "mysql"} {
		for _, multi := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/multi=%v", backend, multi), func(t *testing.T) {
				var unavailable, removed atomic.Bool
				var calls, lookupCalls atomic.Int64
				var lookupEmpty atomic.Bool
				remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var auth map[string]string
					if json.Unmarshal([]byte(r.Header.Get("X-Bkapi-Authorization")), &auth) != nil || auth["bk_app_code"] != "linkd-test" || auth["bk_app_secret"] != "cmdb-test-value" || r.Header.Get("X-Bk-Tenant-Id") != "cmdb-live" {
						t.Error("wrong CMDB identity")
						w.WriteHeader(401)
						return
					}
					if r.URL.Path == "/api/bk-user/prod/api/v3/open/tenant/virtual-users/-/lookup/" {
						lookupCalls.Add(1)
						if !multi || auth["bk_username"] != "admin" || r.URL.Query().Get("lookup_field") != "login_name" || r.URL.Query().Get("lookups") != "bk_admin" {
							t.Error("wrong user lookup")
							w.WriteHeader(400)
							return
						}
						data := []any{map[string]any{"bk_username": "tenant-reader", "bk_tenant_id": "cmdb-live"}}
						if lookupEmpty.Load() {
							data = []any{}
						}
						if err := json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data}); err != nil {
							t.Error(err)
						}
						return
					}
					wantUser := "admin"
					if multi {
						wantUser = "tenant-reader"
					}
					if auth["bk_username"] != wantUser {
						t.Error("wrong resolved CMDB user")
						w.WriteHeader(401)
						return
					}
					if unavailable.Load() {
						w.WriteHeader(503)
						return
					}
					var data any
					switch r.URL.Path {
					case "/api/bk-cmdb/prod/api/v3/find/topoinst/biz/2":
						data = []any{map[string]any{"bk_obj_id": "biz", "bk_inst_id": 2, "child": []any{map[string]any{"bk_obj_id": "set", "bk_inst_id": 4, "child": []any{map[string]any{"bk_obj_id": "module", "bk_inst_id": 8}}}}}}
					case "/api/bk-cmdb/prod/api/v3/topo/internal/0/2":
						data = map[string]any{}
					case "/api/bk-cmdb/prod/api/v3/findmany/proc/service_instance/details":
						if removed.Load() {
							data = map[string]any{"count": 0, "info": []any{}}
						} else {
							data = map[string]any{"count": 1, "info": []any{map[string]any{"id": 1, "bk_biz_id": 2, "bk_host_id": 10, "bk_module_id": 8, "name": "service-1"}}}
						}
					case "/api/bk-cmdb/prod/api/v3/hosts/app/2/list_hosts", "/api/bk-cmdb/prod/api/v3/findmany/hosts/by_topo/biz/2":
						if removed.Load() && r.URL.Path == "/api/bk-cmdb/prod/api/v3/findmany/hosts/by_topo/biz/2" {
							data = map[string]any{"count": 0, "info": []any{}}
						} else {
							data = map[string]any{"count": 1, "info": []any{map[string]any{"bk_host_id": 10, "bk_cloud_id": 0, "bk_host_innerip": "10.0.0.1", "bk_host_name": "host-10"}}}
						}
					default:
						t.Error("unexpected CMDB request", r.URL.Path)
						w.WriteHeader(404)
						return
					}
					if err := json.NewEncoder(w).Encode(map[string]any{"result": true, "code": 0, "data": data}); err != nil {
						t.Error(err)
					}
				}))
				t.Cleanup(remote.Close)
				h := startPolicyHarnessWithTimeout(t, root, binary, backend, 10*time.Minute, func(path string) {
					//nolint:gosec // G304: 本测试生成的独立配置路径。
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var document map[string]any
					if err := yaml.Unmarshal(raw, &document); err != nil {
						t.Fatal(err)
					}
					//nolint:gosec // G101: 隔离 HTTP 协议服务器的合成凭据。
					document["blueking"] = config.BluekingConfig{EnableMultiTenantMode: multi, APIURL: remote.URL, AppCode: "linkd-test", AppSecret: "cmdb-test-value"}
					document["resources"].(map[string]any)["cmdb"] = config.CMDBResource{}
					raw, err = yaml.Marshal(document)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, raw, 0600); err != nil {
						t.Fatal(err)
					}
				})
				if _, err := h.metadata.ExecContext(h.ctx, "INSERT INTO metadata_space VALUES ('cmdb-live','bkcc','2',false)"); err != nil {
					t.Fatal(err)
				}
				for _, obj := range []string{"biz", "set", "module", "host", "service_instance"} {
					if _, err := h.metadata.ExecContext(h.ctx, `INSERT INTO object_model_v2 VALUES ('cmdb-live',?,'cmdb',?,'{"config":[]}')`, "cmdb."+obj, obj); err != nil {
						t.Fatal(err)
					}
				}
				ids := map[string]string{}
				for _, kind := range []string{"explicit-service", "topology-service", "topology-host"} {
					model, instance := "cmdb.service_instance", "1"
					if kind == "topology-host" {
						model, instance = "cmdb.host", "10"
					}
					selector := map[string]any{"type": "topo_node", "provider": "cmdb_mainline", "topology_node_id": "cmdb-live_cmdb.module_8", "bk_biz_id": 2}
					if kind == "explicit-service" {
						selector = map[string]any{"type": "instances", "instances": []any{map[string]any{"model_id": model, "model_inst_id": instance, "entity_uid": model + "|" + instance}}}
					}
					h.publishID("cmdb-live", policy.Shield, kind, map[string]any{"policy": condition(kind), "shield_type": "time_shield", "model_id": model, "target_descriptor": map[string]any{"schema_version": 1, "model_id": model, "selectors": []any{selector}}}, time.Now().Add(15*time.Minute))
				}
				if multi {
					lookupEmpty.Store(true)
					failed := onlyAlert(t, h.send("cmdb-live", "policy-a", "user-missing", "user-missing", "explicit-service", "shared", "warning", "triggered", map[string]any{"model_id": "cmdb.service_instance", "model_inst_id": "1"}))
					a := h.alert(failed, func(a domain.Alert) bool { return a.Admission.AdmittedAt != nil })
					if a.Shield.Active {
						t.Fatal("empty bk_admin defaulted into successful shield")
					}
					h.close("cmdb-live", failed)
					h.expect(failed, "firing", "close")
					lookupEmpty.Store(false)
				}
				// 第一条 Event 前发布完整集合，避免把目录缓存刷新间隔误判为丢失新发布。
				for _, kind := range []string{"explicit-service", "topology-service", "topology-host"} {
					model, instance := "cmdb.service_instance", "1"
					if kind == "topology-host" {
						model, instance = "cmdb.host", "10"
					}
					ids[kind] = onlyAlert(t, h.send("cmdb-live", "policy-a", kind, kind, kind, "shared", "warning", "triggered", map[string]any{"model_id": model, "model_inst_id": instance}))
					h.alert(ids[kind], func(a domain.Alert) bool { return a.Shield.Active && a.Admission.AdmittedAt == nil })
				}
				unavailable.Store(true)
				for _, id := range ids {
					a := h.alert(id, func(a domain.Alert) bool { return a.Shield.Active })
					h.assertManualShieldCheck("cmdb-live", id, a.Revision, "partial")
				}
				unavailable.Store(false)
				removed.Store(true)
				for _, kind := range []string{"topology-service", "topology-host"} {
					id := ids[kind]
					released := h.alert(id, func(a domain.Alert) bool { return !a.Shield.Active && a.PolicyChange == nil })
					if released.Admission.AdmittedAt != nil || released.Status != domain.AlertStatusActive {
						t.Fatal("live membership removal admitted or ended Alert")
					}
					model, instance := "cmdb.service_instance", "1"
					if kind == "topology-host" {
						model, instance = "cmdb.host", "10"
					}
					h.send("cmdb-live", "policy-a", kind+"-next", kind, "ordinary", "shared", "warning", "triggered", map[string]any{"model_id": model, "model_inst_id": instance})
					h.close("cmdb-live", id)
					h.expect(id, "firing", "close")
				}
				explicit := h.alert(ids["explicit-service"], func(a domain.Alert) bool { return a.Shield.Active })
				h.assertManualShieldCheck("cmdb-live", explicit.AlertID, explicit.Revision, "partial")
				h.close("cmdb-live", explicit.AlertID)
				if multi && (lookupCalls.Load() < 2 || lookupCalls.Load() > 4) || !multi && lookupCalls.Load() != 0 {
					t.Fatal("runtime user cache/mode mismatch", lookupCalls.Load())
				}
				if calls.Load() < 12 {
					t.Fatal("live gateway was not used")
				}
				if err := h.process.stop(); err != nil {
					t.Fatal(err)
				}
				h.checkActionMessages()
			})
		}
	}
}
