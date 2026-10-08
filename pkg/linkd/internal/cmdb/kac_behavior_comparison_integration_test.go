// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package cmdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"linkd/internal/onemodel"
)

type comparisonBuffer struct {
	bytes.Buffer
	limit int
}

func (b *comparisonBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("comparison output limit")
	}
	return b.Buffer.Write(p)
}

// TestKACCMDBSourceBehaviorComparison 只在显式提供固定 KAC checkout 时运行，不将协议模拟称为实际 CMDB 联调。
func TestKACCMDBSourceBehaviorComparison(t *testing.T) {
	root := os.Getenv("LINKD_TEST_KAC_SOURCE_ROOT")
	if root == "" {
		t.Skip("set LINKD_TEST_KAC_SOURCE_ROOT")
	}
	python := os.Getenv("LINKD_TEST_KAC_PYTHON")
	if python == "" {
		python = "python3"
	}
	for _, minimal := range []bool{false, true} {
		t.Run(map[bool]string{false: "full facts", true: "KAC defaults"}[minimal], func(t *testing.T) {
			service := map[string]any{"id": 1, "bk_biz_id": 2, "bk_host_id": 10, "bk_module_id": 8}
			host := map[string]any{"bk_host_id": 10, "bk_host_name": "host-name"}
			if !minimal {
				service["name"] = "service"
				service["service_template_id"] = 5
				service["process_instances"] = []any{map[string]any{"id": 7}}
				host["bk_cloud_id"] = 0
				host["bk_host_innerip"] = "10.0.0.1"
			}
			input := map[string]any{"ids": []string{"1"}, "services": []any{service}, "hosts": []any{host}}
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			//nolint:gosec // G204: 显式测试提供解释器与只读 checkout，不经过 shell。
			cmd := exec.CommandContext(ctx, python, "-B", filepath.Join("..", "..", "tests", "kac_behavior_comparison", "kac_cmdb_behavior_comparison.py"), root)
			cmd.Env = append(os.Environ(), "PYTHONHASHSEED=0", "PYTHONDONTWRITEBYTECODE=1")
			cmd.Stdin = bytes.NewReader(raw)
			out, stderr := &comparisonBuffer{limit: 1 << 20}, &comparisonBuffer{limit: 8192}
			cmd.Stdout = out
			cmd.Stderr = stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("KAC comparison: %v %s", err, stderr.String())
			}
			var expected struct {
				Sources  map[string]string `json:"source_sha256"`
				Services struct {
					Found    []json.RawMessage `json:"found"`
					NotFound []string          `json:"not_found"`
				} `json:"services"`
				Hosts   []json.RawMessage `json:"hosts"`
				Locator string            `json:"locator"`
			}
			if err := json.Unmarshal(out.Bytes(), &expected); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v3/findmany/proc/service_instance/details" {
					respond(t, w, map[string]any{"count": 1, "info": []any{service}})
				} else {
					respond(t, w, map[string]any{"count": 1, "info": []any{host}})
				}
			}))
			defer server.Close()
			c, err := newTestClient(t, server.URL)
			if err != nil {
				t.Fatal(err)
			}
			services, err := c.ServiceInstances(t.Context(), "tenant", 2, "cw-CCServiceInstance", []string{"1"})
			if err != nil {
				t.Fatal(err)
			}
			hosts, err := c.HostsByTopology(t.Context(), "tenant", 2, "cw-Host", Node{ObjectID: "module", InstanceID: 8})
			if err != nil {
				t.Fatal(err)
			}
			compare := func(rows []onemodel.Instance, want []json.RawMessage) {
				t.Helper()
				if len(rows) != len(want) {
					t.Fatal("comparison member count differs")
				}
				for i, row := range rows {
					doc := row.Document()
					delete(doc, "bk_tenant_id")
					delete(doc, "entity_uid")
					got, err := json.Marshal(doc)
					if err != nil {
						t.Fatal(err)
					}
					var normalized any
					if err := json.Unmarshal(want[i], &normalized); err != nil {
						t.Fatal(err)
					}
					canonical, _ := json.Marshal(normalized)
					if !bytes.Equal(got, canonical) {
						t.Fatalf("KAC projection differs: got=%s want=%s", got, canonical)
					}
				}
			}
			compare(services, expected.Services.Found)
			compare(hosts, expected.Hosts)
			if len(expected.Sources) != 3 || len(expected.Services.NotFound) != 0 || expected.Locator != "tenant_cw-Module_8" {
				t.Fatal("KAC source/identity proof missing")
			}
		})
	}
}
