// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pluginruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"linkd/internal/internaltoken"
)

// TestPythonSourceQueryAuthentication 使用正式 Go 验证器检查真实 Python 查询头。
func TestPythonSourceQueryAuthentication(t *testing.T) {
	python := os.Getenv("LINKD_RUNTIME_PYTHON")
	if python == "" {
		t.Skip("set LINKD_RUNTIME_PYTHON")
	}
	verifier, err := internaltoken.New("integration-signing-key", nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := verifier.Sign("source-reader")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, err := verifier.VerifyHeader(r.Header)
		if err != nil || username != "source-reader" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "" || r.URL.Query().Get("bk_tenant_id") != "tenant" || r.URL.Query().Get("event_source_id") != "source" {
			http.Error(w, "unexpected query", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "next": ""})
	}))
	defer server.Close()
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../python")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	//nolint:gosec // 显式测试解释器和固定脚本，签发凭据仅写临时文件。
	command := exec.CommandContext(ctx, python, "-S", "-c", `import sys
from linkd_runtime.source_alerts import ActiveAlerts
assert ActiveAlerts(sys.argv[1], token_file=sys.argv[2])("tenant", "source", {}) == []
`, server.URL, tokenFile)
	command.Env = append(os.Environ(), "PYTHONPATH="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Python source query: %v %s", err, output)
	}
}
