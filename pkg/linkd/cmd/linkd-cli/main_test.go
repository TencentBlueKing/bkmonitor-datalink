// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestBinaryConsoleFlow 验证真实进程的 stdin、退出码、JSON 输出和 HTTP 契约，
// 仅连接 httptest Console，不依赖开发者配置或外部中间件。
func TestBinaryConsoleFlow(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "linkd-cli")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".") //nolint:gosec // binary 仅为 t.TempDir 内的构建输出路径，命令与其余参数固定。
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	var requests atomic.Int32
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		username, password, ok := r.BasicAuth()
		if !ok || username != "maintainer" || password != "e2e-private-password" {
			t.Error("missing auth")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /ops/local-api/alerts":
			if r.URL.Query().Get("bk_tenant_id") != "tenant-a" {
				t.Error("tenant missing")
			}
			_, _ = io.WriteString(w, `{"items":[{"id":"a","tenantId":"tenant-a"}],"nextCursor":"page-2"}`)
		case "POST /ops/local-api/alerts/a/close":
			mutations.Add(1)
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input["operation_id"] != "581e3d13-c28b-45be-b06e-bc3f21d233cc" || input["effective_at"] != "2026-09-24T00:00:00Z" {
				t.Error("operation identity changed")
			}
			_, _ = io.WriteString(w, `{"alert":{"alert_id":"a","state":"closed"},"already_closed":false}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	configPath := filepath.Join(dir, "config", "config.yaml")
	run := func(input string, args ...string) (string, string, error) {
		t.Helper()
		command := exec.CommandContext(t.Context(), binary, append([]string{"--config", configPath}, args...)...) //nolint:gosec // 仅执行当前测试刚构建的临时二进制，参数来自测试用例。
		command.Stdin = strings.NewReader(input)
		var out, errOut bytes.Buffer
		command.Stdout = &out
		command.Stderr = &errOut
		err := command.Run()
		return out.String(), errOut.String(), err
	}
	if out, errOut, err := run("e2e-private-password\n", "config", "set", "test", "--url", server.URL+"/ops", "--username", "maintainer", "--password-stdin"); err != nil || strings.Contains(out+errOut, "e2e-private-password") {
		t.Fatalf("configure: %v %s %s", err, out, errOut)
	}
	if requests.Load() != 0 {
		t.Fatal("config contacted server")
	}
	if out, errOut, err := run("", "api", "call", "alerts.list", "--profile", "test", "--query", "bk_tenant_id=tenant-a"); err != nil || !strings.Contains(out, `"nextCursor":"page-2"`) {
		t.Fatalf("read: %v %s %s", err, out, errOut)
	}
	body := `{"bk_tenant_id":"tenant-a","operation_id":"581e3d13-c28b-45be-b06e-bc3f21d233cc","reason":"synthetic e2e","effective_at":"2026-09-24T00:00:00Z"}`
	args := []string{"api", "call", "alerts.close", "--path", "id=a", "--body-file", "-"}
	if out, errOut, err := run(body, args...); err == nil || out != "" || !strings.Contains(errOut, "write_confirmation_required") {
		t.Fatalf("guard: %v %s %s", err, out, errOut)
	}
	if _, errOut, err := run(body, append(args, "--dry-run")...); err != nil {
		t.Fatalf("dry-run: %v %s", err, errOut)
	}
	if requests.Load() != 1 || mutations.Load() != 0 {
		t.Fatal("unconfirmed or dry-run call reached server")
	}
	if out, errOut, err := run(body, append(args, "--allow-write")...); err != nil || !strings.Contains(out, `"already_closed":false`) {
		t.Fatalf("write: %v %s %s", err, out, errOut)
	}
	if mutations.Load() != 1 {
		t.Fatal("write repeated")
	}
	if _, errOut, err := run("", "skills", "install", "--agent", "all", "--scope", "project", "--project-dir", dir); err != nil {
		t.Fatalf("install: %v %s", err, errOut)
	}
	for _, agentDir := range []string{".agents", ".claude"} {
		if _, err := os.Stat(filepath.Join(dir, agentDir, "skills", "linkd-ops", "SKILL.md")); err != nil {
			t.Fatal("skill missing", err)
		}
	}
}
