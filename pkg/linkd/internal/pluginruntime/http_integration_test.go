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
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestPythonHTTPIntegration 验证真实 Python HTTP 服务，无 Redis 或 Django 依赖。
func TestPythonHTTPIntegration(t *testing.T) {
	python := os.Getenv("LINKD_RUNTIME_PYTHON")
	if python == "" {
		t.Skip("set LINKD_RUNTIME_PYTHON")
	}
	ready := filepath.Join(t.TempDir(), "address")
	root, err := filepath.Abs("../../python")
	if err != nil {
		t.Fatal(err)
	}
	script := `import sys
from pathlib import Path
from linkd_runtime.executor import Executor
from linkd_runtime.http_server import RuntimeServer
server = RuntimeServer(("127.0.0.1", 0), Executor(), "test-token")
Path(sys.argv[1]).write_text(str(server.server_address[1]))
server.serve_forever()
`
	//nolint:gosec // 解释器由显式集成测试环境指定，脚本固定，路径来自 t.TempDir。
	command := exec.CommandContext(t.Context(), python, "-S", "-c", script, ready)
	command.Env = append(os.Environ(), "PYTHONPATH="+root)
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var port []byte
	for {
		//nolint:gosec // 仅读取本测试临时目录中的就绪文件。
		port, err = os.ReadFile(ready)
		if err == nil && len(port) > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("Python HTTP startup timed out")
		case <-time.After(20 * time.Millisecond):
		}
	}
	client, err := NewHTTPClient(&http.Client{Timeout: time.Second}, "http://127.0.0.1:"+string(port)+"/execute", "test-token", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Execute(ctx, Request{RequestID: "http-r", TenantID: "tenant", SourceID: "source", Plugin: "rest_api_push", Mode: "push", Data: json.RawMessage(`{"action":"heartbeat"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequestID != "http-r" || result.TenantID != "tenant" || len(result.Events) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}
